# CI/CD Pipeline

The GitLab pipeline (`.gitlab-ci.yml`) tests, builds and deploys the Go
controller, and optionally provisions the VMs in Proxmox via Terraform.

## Stages & jobs

| Stage | Job | When | Auto/Manual | Purpose |
|-------|-----|------|-------------|---------|
| test | `go_test` | branches + MRs | auto | `go test ./pkg/... -race` with a coverage gate (`COVERAGE_THRESHOLD`, default 65%); uploads `coverage.out`. |
| test | `go_lint` | branches + MRs | auto (`allow_failure`) | `go fmt` + `go vet`. |
| test | `yaml_check` | branches + MRs | auto | `go run ./cmd/yamlcheck` (YAML syntax). |
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

The same VM inventory can also be provisioned in Proxmox via Terraform (see
[`terraform/README.md`](terraform/README.md)). These jobs run alongside the
`go_*` jobs and are **opt-in** — they only execute when `ENABLE_PROXMOX="true"`.

| Job | Stage | Runs | Purpose |
|-----|-------|------|---------|
| `tf_generate` | validate | always (MR + branches) | Renders the VM YAML to `terraform/generated.tfvars.json` via `cmd/tfgen`; pure Go, so it guards against tfgen regressions even when Proxmox is disabled. |
| `tf_validate` | validate | `ENABLE_PROXMOX=="true"` | `terraform fmt -check -recursive` + `terraform validate`. |
| `tf_plan` | plan | `ENABLE_PROXMOX=="true"`, MRs | `terraform plan`; saves `tfplan` + `tf-plan-output.txt`. |
| `tf_apply` | apply | `ENABLE_PROXMOX=="true"`, default branch (manual) | `terraform apply`. |

State is stored in GitLab's managed Terraform HTTP backend (`TF_STATE_NAME`,
default `proxmox`), initialised with the `CI_JOB_TOKEN`.

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

## Local equivalents

```bash
go test ./pkg/... -race        # tests
go vet ./... && go fmt ./...   # lint
go run ./cmd/yamlcheck         # YAML check
go build -o netbox-gitops ./cmd/netbox-gitops/
./netbox-gitops --dry-run      # validate / plan
```
