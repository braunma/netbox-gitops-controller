# Repository Audit & Roadmap (June 2026)

Audit of the Go implementation: what works, and what should be refactored or
tested next. Actionable follow-ups live in dedicated docs, which take precedence
where they differ:

- `docs/BUGFIX_PLAN.md` — correctness fixes (all done; one `yamlcheck` gap).
- `docs/MISSING_FEATURES.md` — feature roadmap with implementation order.

## 1. Current feature coverage

Reconciled (create/update, idempotent, gitops-tagged):

- **Foundation:** sites, racks, device roles, tags, platforms, tenants/tenant
  groups, custom fields (`pkg/reconciler/foundation.go`)
- **Network:** VRFs, VLAN groups, VLANs, prefixes (`pkg/reconciler/network.go`)
- **Types:** device types incl. interface/front/rear-port templates, module
  types (`pkg/reconciler/device_types.go`)
- **Devices:** devices, interfaces, IP addresses (incl. primary IP), front/rear
  ports, device bays (self-healing), modules, parent/child blade installation
  (`pkg/reconciler/devices.go`)
- **Cables:** bidirectional idempotent wiring with conflict cleanup
  (`pkg/reconciler/cables.go`)
- **Virtualization:** cluster types/groups, clusters, virtual machines and VM
  interfaces (VLAN/IP/primary-IP) (`pkg/reconciler/virtualization.go`)

Infrastructure: site-aware composite-key cache, managed-tag injection, dry-run,
visual diff output, plan summary + `--output json`, `--only`/`--site`/`--device`/
`--vm` selective sync, drift exit code, opt-in `--prune`, GitLab CI, the
`yamlcheck` validator, and the optional Proxmox/Terraform pipeline (`cmd/tfgen`,
`terraform/`).

## 2. Correctness — ✅ all resolved

The four historical issues (no pagination, no retries, mixed `GetID()` usage, no
model validation) are fixed and verified against the code. See
`docs/BUGFIX_PLAN.md` for per-fix detail. The one remaining sub-item is wiring
`Validate()` into `cmd/yamlcheck`.

## 3. Missing features

The canonical list is `docs/MISSING_FEATURES.md`. In short: pruning, plan
summary, selective sync, drift exit code and virtualization are **done**;
extended IPAM (aggregates/RIRs/IP ranges/route targets), a config file, a
daemon/webhook watch mode, JSON-log observability, and release packaging
(Makefile/Dockerfile/goreleaser/GitHub Actions) remain.

## 4. Refactoring candidates

1. **Split `pkg/reconciler/devices.go` (~940 lines).** `reconcileDevice()` mixes
   device creation, bay installation, port/interface/IP reconciliation and cable
   queueing — extract into focused files (`device_create.go`, `device_ports.go`,
   …).
2. **Generic ensure-loop for simple reconcilers.** Sites, racks, roles, tags,
   VRFs, VLAN groups, cluster types/groups all repeat the same
   build-payload→lookup→`Apply` loop. A generic helper
   (`Reconcile[T](items, lookupFn, payloadFn)`) would remove ~150 lines of
   copy-paste and the loader's parallel type-switch duplication.
3. **Unify cable cleanup logic.** `checkAndCleanLocalPort`,
   `checkAndCleanPeerPort` and `cableConnectsTo` in `cables.go` overlap; a single
   decision function over (local state, peer state, desired link) would be easier
   to reason about and test.
4. **Typed object accessors.** `Object = map[string]interface{}` with ad-hoc type
   assertions everywhere; add safe accessors (`obj.ID()`, `obj.Slug()`) or typed
   response structs.
5. **Consistent error strategy.** `devices.go` logs-and-continues where
   `cables.go` fail-fasts; decide per class (config errors fail in validation;
   per-object API errors collect and report at the end with a non-zero exit).
6. **`context.Context` propagation.** No cancellation/timeout support in the
   client or reconcilers.

(Done: content-type/endpoint constants now live in `internal/constants`; the
stale historical docs have been removed.)

## 5. Testing & tooling gaps

- **Coverage (today):** models 99%, tfgen 97%, reconciler 80%, loader 73%,
  client 64%, utils 64%. CI gates total coverage at 65% (`COVERAGE_THRESHOLD`).
- **Integration test:** ❌ missing — spin up NetBox in Docker, sync the
  `example/` data twice, assert the second run is a no-op (idempotency proof).
  Current tests use fakes/`httptest`, not a real NetBox.
- **Build tooling:** ❌ missing — no `Makefile`, `Dockerfile`, release
  automation or `--version`. CI exists only for GitLab though the repo is on
  GitHub; a mirroring GitHub Actions workflow would help.
