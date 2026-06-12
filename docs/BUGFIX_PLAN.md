# Bug-Fix Plan

> **Status: all five fixes implemented** (June 2026). Additionally fixed
> along the way: the loader now accepts single-mapping YAML files (the
> `example/definitions/device_types/*.yaml` files were previously unloadable),
> and two test fixtures were corrected (`u_height: 0` is valid for child
> device types; NetBox stores cable colors as hex, not names).

Prioritized plan for correctness fixes. **Guiding principle: the current
reconciliation logic — especially cable reconciliation (`pkg/reconciler/cables.go`)
— is functional and field-tested. None of the fixes below change reconciliation
decision logic.** They are confined to the HTTP client layer, the loader, and
lookup plumbing, so observable behavior only changes where it is currently wrong.

---

## Priority 1: API pagination in `NetBoxClient.List()` — CRITICAL

**Location:** `pkg/client/client.go:119-175` (`List()`, used by `Filter()` and
all cache loaders)

**Bug:** Only the first page of a list response is read (`results` field);
the `next` link is never followed. NetBox paginates at **50 items by default**,
so any instance with more than 50 objects of a single type (VLANs, devices,
interfaces, cables, …) gets silently truncated results.

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

## Priority 2: Retry with backoff for transient API failures

**Location:** `pkg/client/client.go` (`Request()` and `List()`)

**Bug:** Every HTTP call is single-shot. One transient 502/503/timeout from
NetBox (or a proxy) aborts the entire sync run. Only tag creation
(`pkg/client/tags.go`) has retry handling today.

**Fix (client-only):**
1. Wrap the HTTP execution in a retry loop: up to 3 retries with exponential
   backoff (e.g. 1s/2s/4s) on network errors, HTTP 429, and 5xx.
2. Never retry on 4xx (except 429) — those are genuine request errors.
3. GET/PUT/PATCH/DELETE are safe to retry; for POST, retry only on network
   errors where no response was received, to avoid duplicate creation.
4. Log each retry at warning level.

---

## Priority 3: Post-unmarshal model validation (fail fast in dry-run)

**Location:** `pkg/loader/loader.go`, `pkg/models/*.go`

**Bug:** No validation after YAML unmarshaling. Config mistakes surface as
NetBox 400 errors mid-sync (after some objects were already applied) instead
of failing before any API call. Known cases:
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

## Priority 4: Finish `GetID()` → `GetGlobalID()`/`GetSiteID()` migration

**Locations:** `pkg/reconciler/devices.go:66-76, 358, 369, 431`,
`pkg/reconciler/device_types.go:31, 71`, `pkg/reconciler/network.go:63, 171,
178, 200, 216`

**Issue:** Global resources (sites, roles, device types, VRFs, module types,
manufacturers) still use the legacy `GetID()`. Functionally correct today, but
the legacy fallback masks scoping mistakes in future code (this is exactly how
the past VLAN/rack collisions happened).

**Fix (mechanical rename, no behavior change):**
1. Replace each legacy call with the explicit `GetGlobalID()`.
2. Once no callers remain, remove `GetID()` from `pkg/client/cache.go`
   (or mark deprecated for one release).

---

## Priority 5: Documentation correctness — “Safe Pruning” claim

**Location:** `README.md` (Key Features and “The gitops Tag” sections)

**Bug:** README states that objects removed from YAML are deleted if they
carry the `gitops` tag. No reconciler implements deletion (only forced cable
conflict cleanup in `cables.go:431, 529-534`). Operators may rely on cleanup
that never happens.

**Fix:** Update README to describe actual behavior (create/update only,
orphans are left in place) until pruning is implemented — see
`docs/MISSING_FEATURES.md` for the pruning feature itself.

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

## Suggested order of execution

| Step | Fix | Risk | Touches |
|------|-----|------|---------|
| 1 | Pagination | Low (client only) | `pkg/client/client.go` |
| 2 | Retry/backoff | Low (client only) | `pkg/client/client.go` |
| 3 | Model validation | Low (pre-flight only) | `pkg/loader`, `pkg/models`, `cmd/yamlcheck` |
| 4 | Cache API migration | Very low (rename) | reconcilers, `pkg/client/cache.go` |
| 5 | README pruning claim | None (docs) | `README.md` |

Each step is independently shippable and verifiable with `--dry-run` against a
live instance: the dry-run diff before and after steps 1–4 must be identical
for environments with <50 objects per type.
