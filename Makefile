# NetBox GitOps Controller
BINARY  ?= netbox-gitops
GO      ?= go

# Build metadata. VERSION falls back to the git description, so a local build
# reports the tag it came from rather than "dev".
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS    := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildDate=$(BUILD_DATE)

# Coverage gate. Both CI configurations call `make coverage`, so the scope and
# the bar are defined once here; override either on the command line.
COVERAGE_SCOPE     ?= ./...
COVERAGE_THRESHOLD ?= 65

.PHONY: help build version test coverage lint check ingest-preview e2e e2e-ingest e2e-rename e2e-repo e2e-local clean

help: ## Show this help
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

build: ## Build the controller binary (stamped with version metadata)
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/netbox-gitops/

version: ## Print the version the next build will carry
	@echo "$(VERSION) (commit $(COMMIT), built $(BUILD_DATE))"

test: ## Run unit tests with race detection and coverage
	$(GO) test $(COVERAGE_SCOPE) -race -coverprofile=coverage.out -covermode=atomic
	$(GO) tool cover -func=coverage.out | tail -1

coverage: test ## Run the tests and fail if total coverage is below the threshold
	@# The gate lives here rather than in each CI config, so GitLab and GitHub
	@# Actions cannot drift apart on what they measure or where the bar is.
	@# Both operands are checked first: awk compares a non-numeric operand as a
	@# string, so an unreadable total or an empty threshold would pass the gate
	@# instead of failing it.
	@case "$(COVERAGE_THRESHOLD)" in \
	   ''|*[!0-9.]*) echo "COVERAGE_THRESHOLD is '$(COVERAGE_THRESHOLD)', which is not a number"; exit 1 ;; \
	 esac; \
	 TOTAL=$$($(GO) tool cover -func=coverage.out | tail -1 | awk '{gsub(/%/, "", $$3); print $$3}'); \
	 case "$$TOTAL" in \
	   ''|*[!0-9.]*) echo "Could not read the coverage total from coverage.out"; exit 1 ;; \
	 esac; \
	 echo "Total statement coverage: $$TOTAL% (minimum: $(COVERAGE_THRESHOLD)%)"; \
	 if awk -v t="$$TOTAL" -v m="$(COVERAGE_THRESHOLD)" 'BEGIN { exit !(t < m) }'; then \
	   echo "Coverage $$TOTAL% is below the required $(COVERAGE_THRESHOLD)%"; exit 1; \
	 fi

lint: ## Format check, vet, and SPDX headers
	@# Both checks below run over the repository's own Go files and never
	@# over the whole tree: CI points GOPATH at $$CI_PROJECT_DIR/.go so the module
	@# cache can be cached between jobs, which puts the downloaded dependencies
	@# inside the project directory. `gofmt -l .` walks into them and reports
	@# upstream sources as needing formatting, failing the job on code this
	@# repository does not own. --others --exclude-standard keeps new, not-yet-
	@# staged files in scope (as `gofmt -l .` had them) while honouring
	@# .gitignore, which is what excludes the module cache.
	@# Fail closed on the file list itself. git exits non-zero when it will not
	@# read the tree -- no .git directory, or the "dubious ownership" refusal a
	@# container runner triggers when the checkout belongs to another UID -- and
	@# an unchecked empty list would otherwise skip both checks and report
	@# success on unformatted code.
	@files=$$(git ls-files --cached --others --exclude-standard '*.go') || \
	   { echo "cannot list this repository's Go files; is it a readable git checkout?"; exit 1; }; \
	 test -n "$$files" || { echo "found no Go files to check; refusing to report success"; exit 1; }; \
	 unformatted=$$(gofmt -l $$files); \
	 test -z "$$unformatted" || { echo "gofmt needed:"; echo "$$unformatted"; exit 1; }; \
	 missing=$$(grep -L 'SPDX-License-Identifier' $$files); \
	 test -z "$$missing" || { echo "missing SPDX header:"; echo "$$missing"; exit 1; }
	$(GO) vet ./...

check: lint test e2e-ingest ## Everything that does not need a NetBox
	$(GO) run ./cmd/yamlcheck

e2e-ingest: ## Walk the whole ingestion path (needs no NetBox and no credentials)
	./tests/e2e/ingest.sh

ingest-preview: build ## Show what a collector run would write, without writing it
	./$(BINARY) collect --dry-run

e2e: ## End-to-end tests (needs NETBOX_URL and NETBOX_TOKEN)
	./tests/e2e/run.sh
	./tests/e2e/rename.sh
	./tests/e2e/repo-data.sh

e2e-rename: ## Rename coverage only (needs NETBOX_URL and NETBOX_TOKEN)
	./tests/e2e/rename.sh

e2e-repo: ## Apply this repository's own data (needs NETBOX_URL and NETBOX_TOKEN)
	./tests/e2e/repo-data.sh

e2e-local: ## Provision a throwaway NetBox from source, then run e2e
	./tests/e2e/provision-local.sh

clean: ## Remove build output
	rm -f $(BINARY) coverage.out
