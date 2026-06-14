# CI/CD Pipeline Documentation

This document describes the GitLab CI/CD pipeline for the NetBox GitOps Controller.

## 🎯 Overview

The pipeline tests, builds, and deploys the Go implementation automatically.

## 📊 Pipeline Stages

### 1. **Test Stage**
Runs all tests and validations:
- `go_test` - Go unit tests with race detection ✅ **Auto**
- `go_lint` - Go code linting (fmt, vet) ✅ **Auto**
- `yaml_check` - YAML syntax validation ✅ **Auto**
- `debug_environment` - Environment debugging (manual)

### 2. **Build Stage**
Builds the Go binary:
- `go_build` - Compiles Go binary and saves as artifact ✅ **Auto**

### 3. **Validate Stage**
Validates configuration in dry-run mode:
- `go_validate` - Go implementation validation ✅ **Auto** (non-main branches)

### 4. **Plan Stage**
Generates deployment preview for merge requests:
- `go_plan` - Shows planned changes with Go ✅ **Auto** (MRs only)

### 5. **Apply Stage**
Applies changes to production:
- `go_apply` - Production deployment with Go 🔒 **Manual** (main branch only)

## 🚀 Pipeline Behavior

### On Push to Feature Branch
```
✅ go_test (automatic)
✅ go_lint (automatic)
✅ yaml_check (automatic)
✅ go_build (automatic)
✅ go_validate (automatic)
```

### On Merge Request
```
✅ go_test (automatic)
✅ go_lint (automatic)
✅ yaml_check (automatic)
✅ go_build (automatic)
✅ go_validate (automatic)
✅ go_plan (automatic) → Saves plan-output.txt artifact
```

### On Main Branch
```
✅ go_test (automatic)
✅ go_build (automatic)
🔒 go_apply (manual) → Click to deploy to production
```

## 🖥️ Proxmox Provisioning (optional)

The same VM inventory that syncs to NetBox can also be provisioned in Proxmox via
Terraform (see [`terraform/README.md`](terraform/README.md)). These jobs run
alongside the `go_*` jobs and are **opt-in**: they only execute when the pipeline
variable `ENABLE_PROXMOX` is `"true"`.

| Job | Stage | Runs | Purpose |
|-----|-------|------|---------|
| `tf_generate` | validate | always (MR + branches) | Renders the VM YAML to `terraform/generated.tfvars.json` via `cmd/tfgen`; pure Go, so it guards against tfgen regressions even when Proxmox is disabled. |
| `tf_validate` | validate | `ENABLE_PROXMOX=="true"` | `terraform fmt -check` + `terraform validate`. |
| `tf_plan` | plan | `ENABLE_PROXMOX=="true"`, MRs | `terraform plan` against Proxmox; saves `tfplan` + `tf-plan-output.txt`. |
| `tf_apply` | apply | `ENABLE_PROXMOX=="true"`, main (manual) | `terraform apply` to provision/update VMs. |

State is stored in GitLab's managed Terraform HTTP backend (`TF_STATE_NAME`,
default `proxmox`), initialised with the `CI_JOB_TOKEN`.

### Required variables (only when `ENABLE_PROXMOX="true"`)
```bash
ENABLE_PROXMOX=true
TF_VAR_proxmox_endpoint=https://pve.example.com:8006/
TF_VAR_proxmox_api_token=user@realm!tokenid=secret   # masked
# Optional: TF_VAR_default_gateway, TF_VAR_vlan_tags, TF_VAR_ci_ssh_keys, …
```

## 📦 Artifacts

### Go Binary (`go_build`)
- **File**: `netbox-gitops`
- **Expires**: 1 week
- **Usage**: Downloaded by validate/plan/apply jobs

### Plan Output (`go_plan`)
- **File**: `plan-output.txt`
- **Expires**: 1 week
- **Usage**: Review changes before merging

## 🔧 Environment Variables

### Required (Set in GitLab CI/CD Settings)
```bash
NETBOX_URL=https://netbox.example.com
NETBOX_TOKEN=your_api_token_here
```

### Optional
```bash
# Already configured in .gitlab-ci.yml
CGO_ENABLED=0           # Static binary compilation
GOPATH=$CI_PROJECT_DIR/.go
GOCACHE=$CI_PROJECT_DIR/.cache/go-build
```

## 📝 Job Details

### Primary Go Jobs

#### `go_test`
```yaml
Stage: test
Image: golang:1.24
Command: go test ./pkg/... -v -cover -race
Runs: Always (all branches and MRs)
```

#### `go_build`
```yaml
Stage: build
Image: golang:1.24
Command: go build -v -o netbox-gitops ./cmd/netbox-gitops/
Artifact: netbox-gitops binary
Runs: Always (all branches and MRs)
```

#### `go_validate`
```yaml
Stage: validate
Dependencies: go_build
Command: ./netbox-gitops --dry-run
Runs: Non-main branches and MRs
```

#### `go_plan`
```yaml
Stage: plan
Dependencies: go_build
Command: ./netbox-gitops --dry-run | tee plan-output.txt
Artifact: plan-output.txt
Runs: Merge requests only
```

#### `go_apply`
```yaml
Stage: apply
Dependencies: go_build
Command: ./netbox-gitops
Environment: production
Runs: Main branch (manual trigger required)
```

## 🎨 Best Practices

### For Developers

1. **Create Feature Branch**
   ```bash
   git checkout -b feature/my-change
   ```

2. **Make Changes**
   - Edit YAML files in `definitions/` or `inventory/`
   - Update Go code if needed

3. **Push and Create MR**
   ```bash
   git push origin feature/my-change
   ```

4. **Review Pipeline**
   - ✅ Check that all tests pass
   - 📄 Download `plan-output.txt` artifact
   - 👀 Review planned changes

5. **Merge to Main**
   - Pipeline runs automatically
   - Go to Pipelines → Click `go_apply` → Run manual job

### For Reviewers

1. **Check Pipeline Status** - All green ✅
2. **Download Plan Artifact** - Review `plan-output.txt`
3. **Verify Changes** - Match expected modifications
4. **Approve MR** - If everything looks good

## 🔍 Troubleshooting

### Pipeline Fails at `go_test`
```bash
# Run locally
go test ./pkg/... -v

# Fix issues and commit
```

### Pipeline Fails at `go_build`
```bash
# Run locally
go build -o netbox-gitops ./cmd/netbox-gitops/

# Check for compilation errors
```

### Pipeline Fails at `go_validate`
```bash
# Check NetBox connectivity
export NETBOX_URL="https://netbox.example.com"
export NETBOX_TOKEN="your_token"

# Run locally
./netbox-gitops --dry-run
```

### YAML Syntax Errors
```bash
# Validate YAML locally
go run ./cmd/yamlcheck
```

### Need to Debug
1. Go to Pipelines
2. Find `debug_environment` job
3. Click "Play" button (▶️)
4. Review output

## 🔐 Security Notes

1. **Never commit tokens** - Use GitLab CI/CD variables
2. **Review plan output** - Always check before applying
3. **Manual approval** - Production deploys require manual trigger

## 📊 Pipeline Performance

### Typical Execution Times
- `go_test`: ~10 seconds
- `go_build`: ~20 seconds
- `go_validate`: ~5 seconds
- `go_plan`: ~5 seconds
- `go_apply`: ~30 seconds (depends on changes)

### Cache Benefits
- Go module cache: Speeds up builds by ~50%
- Go build cache: Speeds up compilation by ~70%

## 📚 References

- [GitLab CI/CD Documentation](https://docs.gitlab.com/ee/ci/)
- [Go Testing Documentation](https://pkg.go.dev/testing)
- [NetBox API Documentation](https://demo.netbox.dev/api/docs/)

## ✅ Quick Reference

| Action | Command |
|--------|---------|
| Run tests locally | `go test ./pkg/... -v` |
| Build locally | `go build -o netbox-gitops ./cmd/netbox-gitops/` |
| Validate locally | `./netbox-gitops --dry-run` |
| Apply locally | `./netbox-gitops` |
| Check pipeline | GitLab → CI/CD → Pipelines |
| Download artifacts | Pipeline → Job → Browse artifacts |
| Trigger deploy | Pipelines → `go_apply` → Play ▶️ |
