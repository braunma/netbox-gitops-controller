# CI/CD Pipeline

Two pipelines, with different jobs:

- **GitLab (`.gitlab-ci.yml`)** — the pipeline of record. Tests, builds,
  plans and applies against NetBox. Only this one holds credentials, so only
  this one writes anything.
- **GitHub Actions (`.github/workflows/ci.yml`)** — the review-time checks,
  for pull requests opened on GitHub: formatting, `go vet`, licence headers,
  the unit tests with the same coverage gate, `yamlcheck` (syntax, models and
  the cross-object inventory lint), and a build. It talks to no NetBox and
  needs no secrets.

A change is reviewed wherever the merge or pull request is opened; it reaches
NetBox only through the GitLab `apply` stage.

## Stages & jobs (GitLab)

| Stage | Job | When | Auto/Manual | Purpose |
|-------|-----|------|-------------|---------|
| test | `go_test` | branches + MRs | auto | `go test ./pkg/... -race` with a coverage gate (`COVERAGE_THRESHOLD`, default 65%); uploads `coverage.out`. |
| test | `go_lint` | branches + MRs | auto | `make lint` — `gofmt -l` (a check, not a rewrite), `go vet`, and the SPDX header check. Blocking. |
| test | `ingest_check` | branches + MRs | auto | `tests/e2e/ingest.sh` — the whole fact-ingestion path against the sample data. Needs no NetBox and no credentials, which is the claim it makes. |
| test | `yaml_check` | branches + MRs | auto | `go run ./cmd/yamlcheck` (YAML syntax, typed model validation, and the cross-object lint checks: duplicate names and IPs, rack collisions, cables, references). |
| test | `e2e` | branches + MRs, only when `RUN_E2E="true"` | auto | `tests/e2e/{run,rename,repo-data}.sh` against a throwaway NetBox started as a CI service. Never talks to the project's NetBox: it exports its own target in the job shell. |
| build | `go_build` | branches + MRs | auto | Builds the `netbox-gitops` binary (artifact, 1 week). |
| validate | `debug_environment` | — | manual | Dumps repo/Go/env info for debugging, with `go_build`'s binary. |
| validate | `go_validate` | non-default branches + MRs | auto | `./netbox-gitops validate` (choice values and references against the live instance), then `./netbox-gitops --dry-run`. |
| plan | `go_plan` | MRs only | auto | `--dry-run` → `plan-output.txt` artifact for review. |
| apply | `go_apply` | default branch | **manual** | `./netbox-gitops` (production deploy). |

### Fact ingestion (opt-in, not included by default)

A second, optional set of scheduled jobs scans Proxmox and iDRAC and writes
what they report into this repository's YAML, then opens a merge request. They
are in [`.gitlab-ci.ingest.example.yml`](.gitlab-ci.ingest.example.yml) and run
only once you `include:` it — a job that opens merge requests on a schedule
should be switched on deliberately.

| Job | Stage | Runs | Purpose |
|-----|-------|------|---------|
| `collect_proxmox` | validate | scheduled with `INGEST_SCHEDULE="true"`, else manual | `netbox-gitops collect`; opens a merge request when the repository would change, and nothing when it would not. |
| `scan_idrac` | test | same | Runs the sibling importer container, keeping its `-output json` document as an artifact. |
| `ingest_idrac` | validate | same | `netbox-gitops ingest --format idrac-json`, consuming that artifact. |
| `ingest_preview` | validate | manual | `collect --dry-run` into an artifact: a drift monitor that changes nothing. |

**None of these jobs holds a `NETBOX_TOKEN`,** because none of them talks to
NetBox. The facts reach it by being merged and applied by `go_apply` like every
hand-written line. See [`docs/INGEST.md`](docs/INGEST.md).

## Variables

Everything the pipeline reads, and what setting it does. Set them in
**GitLab → Settings → CI/CD → Variables** (project-wide) or under
**CI/CD → Run Pipeline** (one run).

| Variable | Default | Effect |
|---|---|---|
| `NETBOX_URL` | — | **Required.** Base URL, no trailing `/api`. Read by `go_validate`, `go_plan`, `go_apply`. |
| `NETBOX_TOKEN` | — | **Required.** v1 (40 hex chars) or v2 (`nbt_<key>.<secret>`). Mark it masked. |
| `RUN_TESTS` | `true` | Exactly `"false"` skips `go_test`; any other value runs it. |
| `RUN_QUALITY_CHECKS` | `true` | Exactly `"false"` skips `go_lint`, `yaml_check` and `ingest_check`. |
| `RUN_E2E` | `false` | Exactly `"true"` enables `e2e`. Needs a Docker-executor runner. |
| `INGEST_SCHEDULE` | unset | Exactly `"true"`, set on a **pipeline schedule**, runs the optional fact-ingestion jobs. Only read when `.gitlab-ci.ingest.example.yml` is included. |
| `GITOPS_PUSH_TOKEN` | — | Required by those jobs: a project access token with `write_repository` and `api` scope, so they can push a branch and open a merge request. Mask it. |
| `COVERAGE_THRESHOLD` | `65` | Minimum total statement coverage (%) for `go_test` over `./pkg/...`. Coverage there is ~79% (August 2026). A non-numeric or empty value fails the job rather than disabling the gate. |
| `GITLAB_RUNNER_TAG` | `docker` | Runner tag every job requests. |
| `GOLANG_IMAGE` | `golang:1.24` | Toolchain image for the `go_*` jobs. |
| `NETBOX_IMAGE` | `netboxcommunity/netbox:v4.6-3.3.0` | The `e2e` job's throwaway NetBox service. |
| `POSTGRES_IMAGE` | `postgres:16-alpine` | Its database service. |
| `REDIS_IMAGE` | `valkey/valkey:8-alpine` | Its cache service. |

The `RUN_*` comparisons are literal string matches: `"False"` does not skip
anything and `"True"` does not enable anything. The two opt-outs (`RUN_TESTS`,
`RUN_QUALITY_CHECKS`) need exactly `"false"` to skip; the opt-in (`RUN_E2E`)
needs exactly `"true"` to run.

> **Precedence.** A variable set in the UI — project, group or instance level —
> outranks anything written in `.gitlab-ci.yml`, including a job's own
> `variables:` block. The `e2e` job
> therefore sets its NetBox target with a shell `export` instead: it wipes
> every `gitops`-tagged object between seeds, and a project `NETBOX_URL` would
> otherwise aim that at the real instance.

Build config, already set in `.gitlab-ci.yml` and not worth overriding:
`CGO_ENABLED=1` (required by the `-race` detector), `GOPATH`/`GOCACHE` under
`$CI_PROJECT_DIR`, and the module and build caches in the `cache:` block.

The end-to-end scripts additionally read `E2E_SEEDS`, `E2E_KEEP` and
`E2E_WORK` — see [`tests/e2e/README.md`](tests/e2e/README.md).

Build config (already set in `.gitlab-ci.yml`): `CGO_ENABLED=1` (required for the
`-race` detector), `GOPATH`/`GOCACHE` under `$CI_PROJECT_DIR`, module + build
caches via the `cache:` block.

## Typical flow

1. Push a feature branch → tests, lint, build and `--dry-run` validate run.
2. Open an MR → `go_plan` produces a `plan-output.txt` artifact; review it.
3. Merge to the default branch → trigger `go_apply` manually to deploy.

### Validating branches that add new objects

`go_validate`/`go_plan` run `--dry-run` against live NetBox, so references are
resolved against what NetBox already contains. Objects a branch *declares* but
has not applied yet (the site/role/device-type for a brand-new device, etc.)
do not exist in NetBox during validation. The controller resolves such
references against the objects reconciled earlier in the *same* run, so a
branch that adds a new site **and** a device in it validates as a plan rather
than failing with `site ... not found`. A reference that is neither in NetBox
nor declared anywhere in the YAML is still reported as an error, so genuine
typos are caught before merge.

## GitHub Actions jobs

| Job | Runs |
|-----|------|
| `lint` | `make lint` — `gofmt -l`, `go vet ./...`, and the SPDX header check |
| `test` | `go test ./... -race` with the `COVERAGE_THRESHOLD` gate (65%), uploads `coverage.out` |
| `data` | `go run ./cmd/yamlcheck` — YAML syntax, typed models, cross-object lint |
| `ingest` | `tests/e2e/ingest.sh` — the fact-ingestion path end to end; needs no NetBox |
| `build` | `make build`, then `--version` to show the stamped metadata |

The end-to-end suite is deliberately absent: it needs a NetBox to talk to. Run
it with `make e2e` against a disposable instance, or enable the GitLab `e2e`
job with `RUN_E2E=true`.

## Local equivalents

```bash
go test ./pkg/... -race        # tests
go vet ./... && go fmt ./...   # lint
go run ./cmd/yamlcheck         # YAML syntax + models + cross-object lint
go run ./cmd/yamlcheck --strict  # ... failing on warnings too
go build -o netbox-gitops ./cmd/netbox-gitops/
./netbox-gitops --dry-run      # validate / plan

# What the ingestion jobs do, without opening a merge request
./netbox-gitops collect --dry-run
./netbox-gitops ingest --format idrac-json --input scan.json --dry-run
```
