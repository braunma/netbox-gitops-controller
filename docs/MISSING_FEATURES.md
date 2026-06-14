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
Only `NETBOX_URL`, `NETBOX_TOKEN`, `IGNORE_SSL_ERRORS` (env) today. Desired: an
optional YAML config for managed tag slug, page size, retry counts, HTTP
timeout, default data dir.

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
No versioned releases, Dockerfile or Makefile, and CI exists only for GitLab
though the repo is on GitHub. Desired: a `Makefile`, multi-stage `Dockerfile` +
image publishing, goreleaser with semver tags and a `--version` flag, and a
GitHub Actions workflow mirroring `.gitlab-ci.yml`.

### Idempotency integration test
Unit tests use fakes/`httptest`, not a real NetBox. Missing: spin up NetBox in
Docker in CI, sync the `example/` data twice, and assert the second run is a
no-op.

## Suggested order

| Order | Feature | Effort |
|-------|---------|--------|
| 1 | Idempotency integration test | M |
| 2 | Config file | S |
| 3 | Extended IPAM | S–M |
| 4 | Release / packaging | S |
| 5 | Observability | M |
| 6 | Daemon / webhooks | L |
