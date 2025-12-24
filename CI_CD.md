# CI/CD Pipeline Documentation

This document describes the GitLab CI/CD pipeline for the NetBox GitOps Controller.

## 🎯 Overview

The pipeline supports **both Go (primary)** and **Python (legacy)** implementations with automatic testing, building, and deployment.

## 📊 Pipeline Stages

### 1. **Test Stage**
Runs all tests and validations:
- `go_test` - Go unit tests with race detection ✅ **Auto**
- `go_lint` - Go code linting (fmt, vet) ✅ **Auto**
- `yaml_check` - YAML syntax validation ✅ **Auto**
- `python_test` - Python tests (manual, legacy support)
- `debug_environment` - Environment debugging (manual)

### 2. **Build Stage**
Builds the Go binary:
- `go_build` - Compiles Go binary and saves as artifact ✅ **Auto**

### 3. **Validate Stage**
Validates configuration in dry-run mode:
- `go_validate` - Go implementation validation ✅ **Auto** (non-main branches)
- `python_validate` - Python validation (manual, legacy)

### 4. **Plan Stage**
Generates deployment preview for merge requests:
- `go_plan` - Shows planned changes with Go ✅ **Auto** (MRs only)

### 5. **Apply Stage**
Applies changes to production:
- `go_apply` - Production deployment with Go 🔒 **Manual** (main branch only)
- `python_apply` - Legacy Python deployment 🔒 **Manual** (fallback)

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
🔒 python_apply (manual) → Legacy fallback
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
Image: golang:1.21
Command: go test ./pkg/... -v -cover -race
Runs: Always (all branches and MRs)
```

#### `go_build`
```yaml
Stage: build
Image: golang:1.21
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

### Legacy Python Jobs

All Python jobs are marked as `when: manual` or `allow_failure: true` to support legacy deployments without interfering with the primary Go pipeline.

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
python -c "import yaml; yaml.safe_load(open('definitions/sites/sites.yaml'))"
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
4. **Separate environments** - Go and Python use different environment names

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

## 🔄 Migration Path

### Current State
- ✅ Go is the **primary** implementation
- ✅ Python jobs are **manual/fallback**
- ✅ All new pipelines use Go by default

### To Remove Python Support
1. Remove Python jobs from `.gitlab-ci.yml`
2. Remove Python variables and cache
3. Remove `requirements.txt` and `src/` directory

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
