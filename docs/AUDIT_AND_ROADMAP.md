# Repository Audit & Roadmap (June 2026)

> **Actionable follow-ups are tracked in dedicated documents:**
> - `docs/BUGFIX_PLAN.md` — prioritized bug fixes (conservative; no changes to
>   working cable reconciliation logic)
> - `docs/MISSING_FEATURES.md` — missing features with implementation order
>
> This file is the full audit snapshot; the two documents above take precedence
> where they differ.

Audit of the Go implementation: what works, what is missing, what should be
implemented next, and what should be refactored. The earlier rack / prefix-VLAN
/ VLAN-group cache collisions have since been fixed (see §2), and the historical
Python-migration and refactoring docs that this audit superseded have now been
removed (see §4.8).

---

## 1. Current Feature Coverage

Reconciled (create/update, idempotent, gitops-tagged):

- **Foundation:** sites, racks, device roles, tags (`pkg/reconciler/foundation.go`)
- **Network:** VRFs, VLAN groups, VLANs, prefixes (`pkg/reconciler/network.go`)
- **Types:** device types incl. interface/front/rear-port templates, module types (`pkg/reconciler/device_types.go`)
- **Devices:** devices, interfaces, IP addresses (incl. primary IP), front/rear
  ports, device bays (self-healing), modules, parent/child blade installation
  (`pkg/reconciler/devices.go`)
- **Cables:** bidirectional idempotent cable wiring with conflict cleanup
  (`pkg/reconciler/cables.go`)

Infrastructure: site-aware composite-key cache, managed-tag injection,
dry-run mode, visual diff output, GitLab CI (lint, test, build, MR dry-run,
apply on main), `yamlcheck` validator.

---

## 2. Bugs / Correctness Issues — ✅ ALL RESOLVED

> All four issues below have been fixed and verified against the code. See
> `docs/BUGFIX_PLAN.md` for per-fix detail. Kept here for historical context.

### 2.1 No API pagination — silent data truncation (CRITICAL) — ✅ FIXED
`NetBoxClient.List()` (`pkg/client/client.go:196-242`) now follows the `next`
link through every page and sets a default `limit=250`. Regression test:
`pkg/client/pagination_test.go`. Previously it read only the first page and
truncated the cache for any type with >50 objects.

### 2.2 No retries / rate limiting — ✅ FIXED
`doWithRetry()` (`pkg/client/client.go:105-151`) retries network errors, 429,
and 5xx with exponential backoff (1s/2s/4s, up to 3 retries), and never retries
POST on a server response to avoid duplicate creation.

### 2.3 Mixed `GetID()` usage remains — ✅ FIXED
All reconcilers use `GetGlobalID()`/`GetSiteID()`, and the legacy
`CacheManager.GetID()` has been removed from `pkg/client/cache.go`. (The
remaining `TagManager.GetID(slug)` in `tags.go` is an unrelated tag lookup.)

### 2.4 No model validation — ✅ FIXED
Every model has a `Validate()` method (`pkg/models/validate.go`) covering
cross-field constraints (e.g. `device_bay` ↔ `parent_device`), called by the
loader after unmarshal (`pkg/loader/loader.go:188`). One small follow-up
remains: wiring the same checks into `cmd/yamlcheck` (see BUGFIX_PLAN §3).

---

## 3. Missing Features

Items 1–4 below are now **done**; the remaining candidates carry over to the
canonical list in `docs/MISSING_FEATURES.md`.

1. **Orphan pruning (`--prune`)** — ✅ done (`pkg/client/prune.go`).
   gitops-tagged objects removed from YAML are deleted on a `--prune` run;
   README's "Safe Pruning" claim is now accurate.
2. **Plan/diff summary** — ✅ done. `ChangeRecorder` emits a
   `Plan: N create / M update / J delete / K unchanged` summary, and
   `--output json` produces machine-readable output for MR comments.
3. **Selective sync** — ✅ done. `--only <phase>`, `--site <slug>`,
   `--device <name>`.
4. **Exit-code semantics for drift** — ✅ done.
   `--dry-run --detailed-exitcode` returns 2 when changes are pending.
5. **Virtualization & IPAM extensions** — ❌ not started. clusters/VMs/VM
   interfaces, aggregates, RIRs, route targets, IP ranges. Add based on need.
6. **Drift-detection daemon / NetBox webhooks** — ❌ not started. Continuous
   reconcile loop; prerequisites (pruning + plan output) are now in place.
7. **Observability** — ❌ not started. Structured logging option (JSON),
   summary metrics, `--quiet`/`--verbose` levels.

---

## 4. Refactoring Candidates

1. **Split `pkg/reconciler/devices.go` (~940 lines).** `reconcileDevice()`
   mixes device creation, bay installation, port/interface/IP reconciliation,
   and cable queueing. Extract into focused files/methods
   (`device_create.go`, `device_ports.go`, `device_modules.go`, …) — this is
   also a precondition for testing it (current reconciler coverage ≈ 6 %).
2. **Generic ensure-loop for simple reconcilers.** Sites, racks, roles, tags,
   VRFs, VLAN groups all repeat the same build-payload→lookup→`Apply` loop
   (`foundation.go`, `network.go`). A small generic helper
   (`Reconcile[T](items, lookupFn, payloadFn)`) removes ~150 lines of
   copy-paste and makes new object types one-liner additions.
3. **Unify cable cleanup logic.** `checkAndCleanLocalPort`,
   `checkAndCleanPeerPort`, and `cableConnectsTo` (`cables.go:360-539`)
   overlap; a single decision function over (local port state, peer port
   state, desired link) would be easier to reason about and test.
4. **Typed object accessors.** `Object = map[string]interface{}` with ad-hoc
   type assertions everywhere (`utils.GetIDFromObject` etc.). Add safe
   accessor methods (`obj.ID()`, `obj.Slug()`) or typed response structs.
5. **Constants for content types.** ✅ Done — `"dcim.interface"`,
   `"dcim.frontport"`, `"dcim.rearport"` and the endpoint/transform strings now
   live in `internal/constants/constants.go`.
6. **Consistent error strategy.** `devices.go` logs-and-continues where
   `cables.go` fail-fasts; decide per-class (config errors → fail in
   validation; per-object API errors → collect and report at end, non-zero
   exit).
7. **`context.Context` propagation.** No cancellation/timeout support in the
   client or reconcilers.
8. **Docs cleanup.** ✅ Done — the stale historical docs (`BUGS_FOUND.md`,
   `MIGRATION_COMPLETE.md`, `GO_MIGRATION.md`, `GO_INTERFACES.md`,
   `REFACTORING_*.md`, `CACHE_REDESIGN.md`, `ENTERPRISE_CODE_REVIEW.md`,
   `CABLE_RECONCILIATION_ENHANCEMENTS.md`, `TEST_COVERAGE_REPORT.md`) have been
   removed; they described a state that no longer exists and misled new
   contributors. The current docs are `README.md`, `EXAMPLES.md`, `CI_CD.md`,
   `terraform/README.md`, the `example/` guides, and the `docs/` audit/roadmap/
   plan set.

---

## 5. Testing & Tooling Gaps

- **Reconciler tests:** ✅ Done — `foundation.go`, `network.go`,
  `device_types.go` now have `*_reconcile_test.go` suites alongside the
  device/cable/prune tests, all driven by `pkg/reconciler/fakenetbox_test.go`.
- **Pagination/cache tests:** ✅ Done — `pkg/client/pagination_test.go` is the
  regression guard for §2.1 (>50 objects).
- **Integration test:** ❌ Still missing — spin up NetBox in Docker in CI, run
  sync twice, assert the second run is a no-op (idempotency proof). Current
  tests use fakes/`httptest`, not a real NetBox.
- **Build tooling:** ❌ Still missing — no `Makefile`, no `Dockerfile`, no
  release automation (goreleaser), no versioning/`--version`. CI exists only
  for GitLab although the repo is hosted on GitHub — consider a GitHub Actions
  workflow mirroring `.gitlab-ci.yml`.

---

## 6. Order of Work — progress

| Priority | Item | Status |
|---|---|---|
| 1 | Pagination in `List()` (§2.1) | ✅ done |
| 2 | Retry/backoff in client (§2.2) | ✅ done |
| 3 | Orphan pruning (§3.1) | ✅ done (implemented, not just README fix) |
| 4 | Model validation (§2.4) | ✅ done (⚠️ `yamlcheck` wiring open) |
| 5 | Split devices.go + reconciler tests (§4.1, §5) | 🟡 reconciler tests done; **devices.go split still pending** (~940 lines) |
| 6 | Plan summary + drift exit code (§3.2, §3.4) | ✅ done |
| 7 | Selective sync (§3.3) | ✅ done |
| 8 | Generic reconciler, constants, typed objects (§4.2/4/5) | 🟡 constants done; generic reconciler + typed accessors pending |

**What's left from this audit:** split `devices.go` (§4.1), generic ensure-loop
(§4.2), unify cable cleanup (§4.3), typed object accessors (§4.4), consistent
error strategy (§4.6), `context.Context` propagation (§4.7), and the Docker
idempotency integration test (§5). (Docs archival, §4.8, is now done.) New-capability work
(virtualization, extended IPAM, daemon, observability, packaging) is tracked in
`docs/MISSING_FEATURES.md`.
