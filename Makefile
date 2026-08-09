# NetBox GitOps Controller
BINARY  ?= netbox-gitops
GO      ?= go

# Build metadata. VERSION falls back to the git description, so a local build
# reports the tag it came from rather than "dev".
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT     ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS    := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.buildDate=$(BUILD_DATE)

.PHONY: help build version test lint check e2e e2e-local clean

help: ## Show this help
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

build: ## Build the controller binary (stamped with version metadata)
	$(GO) build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/netbox-gitops/

version: ## Print the version the next build will carry
	@echo "$(VERSION) (commit $(COMMIT), built $(BUILD_DATE))"

test: ## Run unit tests with race detection and coverage
	$(GO) test ./... -race -coverprofile=coverage.out -covermode=atomic
	$(GO) tool cover -func=coverage.out | tail -1

lint: ## Format check, vet, and SPDX headers
	@test -z "$$(gofmt -l . )" || { echo "gofmt needed:"; gofmt -l .; exit 1; }
	$(GO) vet ./...
	@missing=$$(git ls-files '*.go' | xargs grep -L 'SPDX-License-Identifier'); \
	 test -z "$$missing" || { echo "missing SPDX header:"; echo "$$missing"; exit 1; }

check: lint test ## Everything that does not need a NetBox
	$(GO) run ./cmd/yamlcheck

e2e: ## End-to-end tests (needs NETBOX_URL and NETBOX_TOKEN)
	./tests/e2e/run.sh

e2e-local: ## Provision a throwaway NetBox from source, then run e2e
	./tests/e2e/provision-local.sh

clean: ## Remove build output
	rm -f $(BINARY) coverage.out
