# Bug-Fix Plan

> **Status: all five fixes implemented and verified against the code (June
> 2026).** The only remaining sub-item is wiring model validation into the
> `yamlcheck` CLI (Priority 3, step 3 — see note below). Additionally fixed
> along the way: the loader now accepts single-mapping YAML files (the
> `example/definitions/device_types/*.yaml` files were previously unloadable),
> and two test fixtures were corrected (`u_height: 0` is valid for child
> device types; NetBox stores cable colors as hex, not names).
>
> This document is now a historical record of the fixes; each priority below
> carries its current status. Open follow-up work lives in
> `docs/MISSING_FEATURES.md` and `docs/AUDIT_AND_ROADMAP.md`.

Prioritized plan for correctness fixes. **Guiding principle: the current
reconciliation logic — especially cable reconciliation (`pkg/reconciler/cables.go`)
— is functional and field-tested. None of the fixes below change reconciliation
decision logic.** They are confined to the HTTP client layer, the loader, and
lookup plumbing, so observable behavior only changes where it is currently wrong.

---

## Priority 1: API pagination in `NetBoxClient.List()` — ✅ DONE

**Location:** `pkg/client/client.go` (`List()`, used by `Filter()` and
all cache loaders)

**Resolved:** `List()` (`pkg/client/client.go:196-242`) now follows the `next`
link through every page and sets a default `limit=250` to reduce round-trips,
keeping the fallback for direct-array responses. Regression test:
`pkg/client/pagination_test.go`.

**Bug (historical):** Only the first page of a list response was read
(`results` field); the `next` link was never followed. NetBox paginates at
**50 items by default**, so any instance with more than 50 objects of a single
type (VLANs, devices, interfaces, cables, …) got silently truncated results.

**Impact:**
- Cache (`LoadGlobal()` / `LoadSite()`) misses objects → lookups fail →
  duplicate-create attempts or skipped updates.
- Interface/port listings during device reconciliation miss ports on devices
  with >50 interfaces (realistic for switches) → cable wiring against missing
  ports fails or re-creates.

**Fix (client-only, no reconciler changes):**
1. In `List()`, parse `next` from the response envelope alongside `results`.
2. Loop: keep requesting `next` until it is `null`, appending to the result
   slice. Keep the existing fallback for direct-array responses.
3. Optionally request `?limit=250` to reduce round-trips.

**Test:** Unit test with a mock server returning 3 pages; assert all objects
are returned. Regression guard for >50-object environments.

---

## Priority 2: Retry with backoff for transient API failures — ✅ DONE

**Location:** `pkg/client/client.go` (`Request()` and `List()`)

**Resolved:** `doWithRetry()` (`pkg/client/client.go:105-151`) wraps every HTTP
call with up to 3 retries and exponential backoff (1s/2s/4s) on network errors,
HTTP 429, and 5xx. `isRetryableStatus()` keeps 4xx (except 429) non-retryable
and never retries POST on a server response, to avoid duplicate creation. Each
retry is logged at warning level.

**Bug (historical):** Every HTTP call was single-shot. One transient
502/503/timeout from NetBox (or a proxy) aborted the entire sync run. Only tag
creation (`pkg/client/tags.go`) had retry handling.

**Fix (client-only):**
1. Wrap the HTTP execution in a retry loop: up to 3 retries with exponential
   backoff (e.g. 1s/2s/4s) on network errors, HTTP 429, and 5xx.
2. Never retry on 4xx (except 429) — those are genuine request errors.
3. GET/PUT/PATCH/DELETE are safe to retry; for POST, retry only on network
   errors where no response was received, to avoid duplicate creation.
4. Log each retry at warning level.

---

## Priority 3: Post-unmarshal model validation (fail fast in dry-run) — ✅ DONE (one gap)

**Location:** `pkg/loader/loader.go`, `pkg/models/*.go`

**Resolved:** Every model has a `Validate() error` method
(`pkg/models/validate.go`), including the cross-field cases below (e.g.
`DeviceConfig.Validate` rejects `device_bay` without `parent_device` and vice
versa). The loader calls `item.Validate()` after unmarshal
(`pkg/loader/loader.go:188`). Covered by `pkg/models/validate_test.go`.

**Remaining gap:** step 3 below — wiring the same `Validate()` checks into the
`cmd/yamlcheck` CLI — is **not done**; `yamlcheck` only checks YAML syntax, not
model constraints. Tracked as a small follow-up.

**Bug (historical):** No validation after YAML unmarshaling. Config mistakes
surfaced as NetBox 400 errors mid-sync (after some objects were already applied)
instead of failing before any API call. Known cases:
- `device_bay` set without `parent_device` (and vice versa)
- interface templates without `type` (documented as a common error in README)
- `rack_slug` together with `parent_device` (mutually exclusive placement)
- missing required slugs (`site_slug`, `role_slug`, `device_type_slug`)

**Fix (loader-only, no reconciler changes):**
1. Add a `Validate() error` method per model in `pkg/models/`.
2. Call it in the loader after unmarshal; collect all errors and report them
   together with file name before phase 1 starts.
3. Wire the same checks into `cmd/yamlcheck` so CI catches them.

---

## Priority 4: Finish `GetID()` → `GetGlobalID()`/`GetSiteID()` migration — ✅ DONE

**Resolved:** All reconcilers now use the explicit `GetGlobalID()`/`GetSiteID()`
accessors (`pkg/reconciler/{devices,device_types,network}.go`), and the legacy
`CacheManager.GetID()` has been removed from `pkg/client/cache.go` (the only
`GetID` left is `TagManager.GetID(slug)` in `pkg/client/tags.go`, an unrelated
tag-by-slug lookup).

**Issue (historical):** Global resources (sites, roles, device types, VRFs,
module types, manufacturers) used the legacy `GetID()`. Functionally correct,
but the legacy fallback masked scoping mistakes in future code (this is exactly
how the past VLAN/rack collisions happened).

**Fix (mechanical rename, no behavior change):**
1. Replace each legacy call with the explicit `GetGlobalID()`.
2. Once no callers remain, remove `GetID()` from `pkg/client/cache.go`
   (or mark deprecated for one release).

---

## Priority 5: Documentation correctness — “Safe Pruning” claim — ✅ RESOLVED

**Location:** `README.md` (Key Features and “The gitops Tag” sections)

**Resolved:** Pruning is now actually implemented (`pkg/client/prune.go`, opt-in
via `--prune`; see `docs/MISSING_FEATURES.md` §1), so the README's "Safe
Pruning" claim is now accurate rather than aspirational. Orphaned
`gitops`-tagged objects removed from YAML are deleted on a `--prune` run.

**Bug (historical):** README stated that objects removed from YAML are deleted
if they carry the `gitops` tag, but no reconciler implemented deletion (only
forced cable conflict cleanup).

---

## Explicitly out of scope

- **Any change to cable reconciliation decision logic** in
  `pkg/reconciler/cables.go` (bidirectional matching, conflict cleanup,
  port-type discovery). It works; refactoring it is deferred until it is
  covered by more tests.
- Refactoring `devices.go` structure — tracked separately as maintainability
  work, not a bug.
- New features (pruning, selective sync, plan output) — see
  `docs/MISSING_FEATURES.md`.

## Order of execution (all complete)

| Step | Fix | Status | Touches |
|------|-----|--------|---------|
| 1 | Pagination | ✅ done | `pkg/client/client.go`, `pkg/client/pagination_test.go` |
| 2 | Retry/backoff | ✅ done | `pkg/client/client.go` |
| 3 | Model validation | ✅ done (⚠️ `yamlcheck` wiring still open) | `pkg/loader`, `pkg/models` |
| 4 | Cache API migration | ✅ done | reconcilers, `pkg/client/cache.go` |
| 5 | README pruning claim | ✅ resolved (pruning implemented) | `README.md`, `pkg/client/prune.go` |

Each step was independently shippable and verifiable with `--dry-run` against a
live instance: the dry-run diff before and after steps 1–4 is identical for
environments with <50 objects per type.
