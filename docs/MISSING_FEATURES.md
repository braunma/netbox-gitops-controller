# Missing Features

Features a production GitOps controller for NetBox would typically offer that
are not implemented yet, roughly in order of value. Bug fixes are tracked
separately in `docs/BUGFIX_PLAN.md`.

---

## 1. Orphan pruning (`--prune`)

**Status:** Advertised in README, not implemented. The only deletions in the
codebase are forced cable-conflict cleanups (`pkg/reconciler/cables.go`).

**Desired behavior:** When an object is removed from YAML, the next sync
deletes it from NetBox — but only if it carries the `gitops` managed tag.
Manually created objects are never touched.

**Sketch:**
- Per object type: list all NetBox objects with `tag=gitops`, diff against the
  set defined in YAML, delete the remainder.
- Deletion order must be reverse of creation order (cables → IPs → interfaces →
  devices → racks/VLANs/prefixes → sites) to satisfy NetBox FK constraints.
- Opt-in via `--prune` flag; in `--dry-run --prune` show planned deletions.
- Start with low-risk types (tags, roles, VLANs, prefixes) before devices.

## 2. Plan summary & machine-readable output

**Status:** ✅ Implemented. A `ChangeRecorder` (`pkg/client/recorder.go`)
collects every create/update/delete/no-op the client performs (in dry-run:
would perform). Each run ends with a `Plan: N to create, M to update, J to
delete, K unchanged` summary, and `--output json` emits the changes as
structured data on stdout (logs move to stderr).

## 3. Drift detection via exit code

**Status:** ✅ Implemented. `--dry-run --detailed-exitcode` (terraform
convention): exit 0 = in sync, exit 2 = changes pending. A scheduled CI
pipeline then becomes a drift monitor with zero extra infrastructure.

## 4. Selective sync

**Status:** ✅ Implemented.
- `--only <phase>` with values `foundation`, `network`, `device-types`,
  `devices` (comma-separated or repeated)
- `--site <slug>` restricts device reconciliation to one site
- `--device <name>` for targeted single-device runs (debugging, hotfixes)

Skipped phases are not validated: `--only devices` requires the referenced
sites, roles and device types to already exist in NetBox.

## 5. Config-driven settings

**Status:** Only `NETBOX_URL`, `NETBOX_TOKEN`, `IGNORE_SSL_ERRORS` via env.

**Desired:** Optional config file (YAML) for: managed tag slug, page size,
retry counts, HTTP timeout, default data dir. Cobra/Viper are already in use
or recommended, so wiring is cheap.

## 6. Virtualization support

**Status:** Not implemented. Only DCIM devices are managed.

**Desired:** Clusters, cluster types/groups, virtual machines, VM interfaces
with the same interface/IP/VLAN semantics as physical devices. The device
reconciler patterns (interfaces, IPs, primary IP) carry over largely 1:1.

## 7. Extended IPAM coverage

**Status:** VRFs, VLAN groups, VLANs, prefixes only.

**Missing object types:** aggregates, RIRs, IP ranges, route targets, ASNs,
services. Each is a simple "ensure" loop like the existing
foundation/network reconcilers — low effort once a generic reconciler helper
exists.

## 8. Daemon / watch mode & webhooks

**Status:** Run-once CLI only.

**Desired (long-term):** `--watch` mode reconciling on an interval, and/or a
small HTTP endpoint receiving NetBox webhooks to react to manual changes.
Only worth building after pruning (1) and plan output (2) exist, since a
daemon without deletion support cannot actually converge state.

## 9. Observability

**Status:** Colorful human-oriented console logging only.

**Desired:**
- `--log-format json` for machine ingestion
- `--quiet` / `--verbose` levels
- Run metrics (objects scanned/changed, API calls, duration) printed at the
  end and optionally exposed for Prometheus in daemon mode.

## 10. Release & packaging

**Status:** No versioned releases, no Dockerfile, no Makefile; CI exists only
for GitLab although the repository is hosted on GitHub.

**Desired:**
- `Makefile` (build/test/lint targets)
- Multi-stage `Dockerfile` + image publishing
- goreleaser config with semantic version tags and a `--version` flag
- GitHub Actions workflow mirroring `.gitlab-ci.yml` (test, lint, build)

## 11. Test infrastructure (enabler, not user-facing)

- Unit tests for `foundation.go`, `network.go`, `device_types.go` (currently
  zero coverage).
- Idempotency integration test: NetBox in Docker, sync the `example/` data
  twice, assert the second run is a no-op.
- This is the safety net required before any refactoring of the working
  device/cable logic is attempted.

---

## Suggested implementation order

| Order | Feature | Effort | Prerequisite |
|-------|---------|--------|--------------|
| 1 | ~~Plan summary (#2)~~ ✅ done | S | — |
| 2 | ~~Drift exit code (#3)~~ ✅ done | XS | #2 |
| 3 | Orphan pruning (#1) | M | pagination fix (see BUGFIX_PLAN) |
| 4 | ~~Selective sync (#4)~~ ✅ done | S | — |
| 5 | Test infrastructure (#11) | M | — |
| 6 | Config file (#5) | S | — |
| 7 | Extended IPAM (#7) | S–M | — |
| 8 | Virtualization (#6) | M | — |
| 9 | Release/packaging (#10) | S | — |
| 10 | Observability (#9) | M | — |
| 11 | Daemon/webhooks (#8) | L | #1, #2 |
