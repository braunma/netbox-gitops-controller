# CI/CD Pipeline

Two pipelines, with different jobs:

- **GitLab (`.gitlab-ci.yml`)** — the pipeline of record. Tests, builds,
  plans and applies against NetBox, and optionally provisions the VMs in
  Proxmox via OpenTofu. Only this one holds credentials, so only this one
  writes anything.
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
| test | `go_lint` | branches + MRs | auto (`allow_failure`) | `go fmt` + `go vet`. |
| test | `yaml_check` | branches + MRs | auto | `go run ./cmd/yamlcheck` (YAML syntax, typed model validation, and the cross-object lint checks: duplicate names and IPs, rack collisions, cables, references). |
| build | `go_build` | branches + MRs | auto | Builds the `netbox-gitops` binary (artifact, 1 week). |
| build | `debug_environment` | — | manual | Dumps repo/Go/env info for debugging. |
| validate | `go_validate` | non-default branches + MRs | auto | `./netbox-gitops --dry-run`. |
| plan | `go_plan` | MRs only | auto | `--dry-run` → `plan-output.txt` artifact for review. |
| apply | `go_apply` | default branch | **manual** | `./netbox-gitops` (production deploy). |

Proxmox jobs (`tf_*`) are described below.

## Pipeline control variables

Set per-pipeline or as project variables to speed up runs:

- `RUN_TESTS="false"` — skip `go_test`.
- `RUN_QUALITY_CHECKS="false"` — skip `go_lint` and `yaml_check`.
- `COVERAGE_THRESHOLD` — minimum total statement coverage % (default `65`;
  current coverage is ~73%). Raise it as coverage improves.
- `GOLANG_IMAGE` — Go image (default `golang:1.24`).
- `GITLAB_RUNNER_TAG` — runner tag (default `docker`).

## Required environment variables

Set in **GitLab → Settings → CI/CD → Variables** (never commit tokens):

```bash
NETBOX_URL=https://netbox.example.com
NETBOX_TOKEN=your_api_token_here
```

Build config (already set in `.gitlab-ci.yml`): `CGO_ENABLED=1` (required for the
`-race` detector), `GOPATH`/`GOCACHE` under `$CI_PROJECT_DIR`, module + build
caches via the `cache:` block.

## Proxmox provisioning (optional)

The same VM inventory can also be provisioned in Proxmox via OpenTofu (the
`tofu` CLI; see [`terraform/README.md`](terraform/README.md)). These jobs run alongside the
`go_*` jobs and are **opt-in** — they only execute when `ENABLE_PROXMOX="true"`.

Each environment (`prod`, `stage`, `playground`) has its own VM folder
(`inventory/virtual/<env>/`) and its own OpenTofu state, and is generated,
planned and applied independently. The environment list lives in exactly one
place — the `.proxmox_envs` YAML anchor in `.gitlab-ci.yml` — which every job's
`parallel:matrix` references, so there is no list to keep in sync.

| Job | Stage | Runs | Purpose |
|-----|-------|------|---------|
| `tf_generate` | validate | always (MR + branches) | One matrix job per env: renders that env's VM YAML to `terraform/generated.<env>.tfvars.json` via `cmd/tfgen`; pure Go, so it guards against tfgen regressions even when Proxmox is disabled. A mistyped env fails the job (tfgen errors on a missing folder) rather than emitting an empty, destroy-everything tfvars file. |
| `tf_validate` | validate | `ENABLE_PROXMOX=="true"` | `tofu fmt -check -recursive` + `tofu validate` (env-agnostic, no backend). |
| `tf_plan` | plan | `ENABLE_PROXMOX=="true"`, MRs **and** default branch | One plan per env against its own state; saves the binary `tfplan.<env>` (consumed by `tf_apply`) + a human-readable `tf-plan-<env>.txt`. |
| `tf_apply` | apply | `ENABLE_PROXMOX=="true"`, default branch (manual) | One manual gate per env. Applies the **saved `tfplan.<env>` from `tf_plan`** — so it applies exactly the reviewed change set, never a fresh re-plan. A plan that has gone stale (state drifted) is rejected and the job fails safely. **Destroy-safe:** if the plan would destroy/replace any VM the job refuses unless re-run with `TF_ALLOW_DESTROY=<env>`; `resource_group` also serialises applies per env. See [`terraform/README.md` → Safety](terraform/README.md#safety). |

State is stored in GitLab's managed HTTP state backend (the `terraform/state`
API, used by both OpenTofu and Terraform), one state per env (`proxmox-<env>`,
derived from the matrix `$ENV`), initialised with the `CI_JOB_TOKEN`.

Required variables when enabled (Proxmox token masked):

```bash
ENABLE_PROXMOX=true
TF_VAR_proxmox_endpoint=https://pve.example.com:8006/
TF_VAR_proxmox_api_token=user@realm!tokenid=secret
# Optional: TF_VAR_default_gateway, TF_VAR_vlan_tags, TF_VAR_ci_ssh_keys, …
```

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
```
