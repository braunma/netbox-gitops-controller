# Repository Audit & Roadmap (June 2026)

Audit of the Go implementation: what works, what is missing, what should be
implemented next, and what should be refactored. Supersedes the stale findings
in `docs/BUGS_FOUND.md` (the rack / prefix-VLAN / VLAN-group cache collisions
listed there have since been fixed) and the historical Python-era
`REFACTORING_ANALYSIS.md`.

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

## 2. Bugs / Correctness Issues (fix first)

### 2.1 No API pagination — silent data truncation (CRITICAL)
`NetBoxClient.List()` (`pkg/client/client.go:119-155`) reads only the first
page (`results`) and never follows `next`. NetBox paginates at **50 items by
default**, so any instance with more than 50 objects of one type gets a
truncated cache → missed lookups, duplicate-create attempts, wrong diffs.
Fix: loop over `next` URLs (or request `?limit=0` where allowed) in `List`.

### 2.2 No retries / rate limiting
Single-shot HTTP requests; a transient 5xx or network blip aborts the whole
sync. Only tag creation has retry logic. Add retry with exponential backoff
for idempotent requests and optional rate limiting.

### 2.3 Mixed `GetID()` usage remains
Global resources still use the legacy `GetID()` (e.g. `devices.go:66-76`,
`device_types.go:31,71`, `network.go:63,171`). Correct today, but the
legacy fallback hides future scoping bugs. Migrate fully to
`GetGlobalID()`/`GetSiteID()` and delete `GetID()`.

### 2.4 No model validation
Models have no `Validate()` methods; cross-field constraints (e.g.
`device_bay` requires `parent_device`) surface only as NetBox 400 errors at
apply time. Add post-unmarshal validation in the loader so `--dry-run`
catches them.

---

## 3. Missing Features (implementation candidates, in suggested order)

1. **Orphan pruning (`--prune`)** — README advertises “Safe Pruning”
   (gitops-tagged objects removed from YAML get deleted), but no reconciler
   implements deletion except forced cable cleanup. Either implement
   (list managed-tag objects per type, diff against YAML, delete with
   confirmation/`--prune` flag) or correct the README. *Biggest gap between
   documentation and reality.*
2. **Plan/diff summary** — dry-run prints per-object diffs but no final
   summary (X create / Y update / Z delete) and no machine-readable output
   (`--output json`) for MR comments à la `terraform plan`.
3. **Selective sync** — `--only devices`, `--only network`, `--site <slug>`
   filters; currently every run reconciles everything in fixed phase order.
4. **Exit-code semantics for drift** — `--dry-run` returning non-zero when
   changes are pending enables CI-based drift detection cheaply, before
   building any daemon/webhook machinery.
5. **Virtualization & IPAM extensions** — clusters/VMs/VM interfaces,
   aggregates, RIRs, route targets, IP ranges. Add based on actual need.
6. **Drift-detection daemon / NetBox webhooks** — continuous reconcile loop;
   only worth it after pruning + plan output exist.
7. **Observability** — structured logging option (JSON), summary metrics,
   `--quiet`/`--verbose` levels.

---

## 4. Refactoring Candidates

1. **Split `pkg/reconciler/devices.go` (927 lines).** `reconcileDevice()`
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
5. **Constants for content types.** `"dcim.interface"`, `"dcim.frontport"`,
   `"dcim.rearport"` are string-matched in `cables.go` and `devices.go`;
   move into `internal/constants`.
6. **Consistent error strategy.** `devices.go` logs-and-continues where
   `cables.go` fail-fasts; decide per-class (config errors → fail in
   validation; per-object API errors → collect and report at end, non-zero
   exit).
7. **`context.Context` propagation.** No cancellation/timeout support in the
   client or reconcilers.
8. **Docs cleanup.** Remove or archive stale docs (`BUGS_FOUND.md` fixed
   items, `MIGRATION_COMPLETE.md`, `GO_MIGRATION.md`, `REFACTORING_*.md`) —
   they describe a state that no longer exists and mislead new contributors.

---

## 5. Testing & Tooling Gaps

- **Reconciler tests:** `foundation.go`, `network.go`, `device_types.go` have
  zero tests; `devices.go`/`cables.go` only partial. Highest-value additions
  after the devices.go split.
- **Integration test:** spin up NetBox in Docker in CI, run sync twice,
  assert second run is a no-op (idempotency proof).
- **Pagination/cache tests:** regression test for §2.1 (>50 objects).
- **Build tooling:** no `Makefile`, no `Dockerfile`, no release automation
  (goreleaser), no versioning. CI exists only for GitLab although the repo
  is hosted on GitHub — consider a GitHub Actions workflow mirroring
  `.gitlab-ci.yml`.

---

## 6. Suggested Order of Work

| Priority | Item | Why |
|---|---|---|
| 1 | Pagination in `List()` (§2.1) | Silent data corruption risk today |
| 2 | Retry/backoff in client (§2.2) | Cheap, removes flaky-sync failures |
| 3 | Orphan pruning or README fix (§3.1) | Advertised feature doesn't exist |
| 4 | Model validation (§2.4) | Fail fast in dry-run instead of 400s |
| 5 | Split devices.go + reconciler tests (§4.1, §5) | Unlocks safe iteration |
| 6 | Plan summary + drift exit code (§3.2, §3.4) | Makes CI workflow genuinely GitOps |
| 7 | Selective sync (§3.3) | Operator quality-of-life |
| 8 | Generic reconciler, constants, typed objects (§4.2/4/5) | Maintainability |
