# Missing Features

Features a production GitOps controller for NetBox would typically offer,
roughly in order of value. Bug fixes are tracked in `docs/BUGFIX_PLAN.md`.

## Already shipped

These were once on this list and are now implemented (documented in `README.md`,
`EXAMPLES.md` and `docs/PLAN_VIRTUALIZATION.md`):

- **Orphan pruning** (`--prune`) — deletes gitops-tagged objects removed from
  YAML, in reverse dependency order, scoped to the phases that ran; never
  touches untagged objects; `--dry-run --prune` previews. See the README's
  pruning section for the full semantics (`pkg/client/prune.go`).
- **Plan summary & JSON output** — `Plan: N to create, …` per run; `--output
  json` emits structured changes on stdout (`pkg/client/recorder.go`).
- **Drift exit code** — `--dry-run --detailed-exitcode` returns 2 when changes
  are pending (terraform convention), so a scheduled CI job is a drift monitor.
- **Selective sync** — `--only <phase>`, `--site`, `--device`, `--vm`.
- **Virtualization** — clusters, VMs and VM interfaces, plus platforms/tenants
  in the foundation phase (`pkg/reconciler/virtualization.go`).
- **Reconciler unit tests** — `*_reconcile_test.go` driven by the in-process
  fake NetBox (`pkg/reconciler/fakenetbox_test.go`), plus a pagination
  regression test.

## Still missing

### Config-driven settings
Settings come from the environment, or from a `KEY=value` file read at startup
(`--config`, default `.env`; the environment wins over it): `NETBOX_URL`,
`NETBOX_TOKEN`, `IGNORE_SSL_ERRORS`, `DEVICETYPE_LIBRARY`,
`MODULETYPE_LIBRARY`, `IGNORED_FILES`. Desired: an optional YAML config for the
things that are still constants — managed tag slug, page size, retry counts,
HTTP timeout, default data dir.

### Extended IPAM coverage
VRFs, VLAN groups, VLANs and prefixes only. Missing object types: aggregates,
RIRs, IP ranges, route targets, ASNs, services — each a simple "ensure" loop,
low effort once a generic reconciler helper exists.

### Daemon / watch mode & webhooks
Run-once CLI only. Desired (long-term): a `--watch` interval mode and/or an HTTP
endpoint for NetBox webhooks. Only worth building now that pruning and plan
output exist (a daemon without deletion can't converge state).

### Observability
Human-oriented console logging only. Desired: `--log-format json`,
`--quiet`/`--verbose` levels, and end-of-run metrics (objects changed, API
calls, duration), optionally Prometheus-exposed in daemon mode.

### Release & packaging
Partly done: there is a `Makefile`, a multi-stage `Dockerfile`, an Apache-2.0
`LICENSE`, and `--version` stamped from ldflags. Still missing: goreleaser with
semver tags, image publishing, and a GitHub Actions workflow mirroring
`.gitlab-ci.yml` (CI exists only for GitLab though the repo is on GitHub).

### Live-state validation
`yamlcheck` validates the YAML against the typed models and, since the lint
checks landed (`pkg/lint`), against the rest of the repository: references that
resolve to nothing declared, a name or IP used twice, two devices in one rack
unit, a port claimed by two cables, an interface the device type does not have.
What it still cannot see is the *server*: a status value, interface type or
rack position that the models accept can still be rejected by a particular
NetBox instance, and an object that exists only in NetBox is invisible to it.
Desired: a `validate` command that resolves choices and references against the
live API without writing, so a merge request fails before it reaches apply.

## Done since this list was written

- **Idempotency integration test** — `tests/e2e/` drives the built binary
  against a real NetBox over randomized datasets and asserts the second apply
  is a no-op, that a dry-run writes nothing, that everything declared actually
  exists, and that prune leaves unmanaged objects alone. Wired into
  `.gitlab-ci.yml` behind `RUN_E2E`.

## Suggested order

| Order | Feature | Effort |
|-------|---------|--------|
| 1 | Live-state validation | M |
| 2 | Config file | S |
| 3 | Extended IPAM | S–M |
| 4 | Release / packaging (goreleaser, image publish) | S |
| 5 | Observability | M |
| 6 | Daemon / webhooks | L |
