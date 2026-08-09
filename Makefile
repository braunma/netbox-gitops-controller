# NetBox GitOps Controller
BINARY ?= netbox-gitops
GO     ?= go

.PHONY: help build test lint check e2e e2e-local clean

help: ## Show this help
	@grep -hE '^[a-zA-Z0-9_-]+:.*?## ' $(MAKEFILE_LIST) | awk 'BEGIN{FS=":.*?## "}{printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'

build: ## Build the controller binary
	$(GO) build -o $(BINARY) ./cmd/netbox-gitops/

test: ## Run unit tests with race detection and coverage
	$(GO) test ./... -race -coverprofile=coverage.out -covermode=atomic
	$(GO) tool cover -func=coverage.out | tail -1

lint: ## Format check and vet
	@test -z "$$(gofmt -l . )" || { echo "gofmt needed:"; gofmt -l .; exit 1; }
	$(GO) vet ./...

check: lint test ## Everything that does not need a NetBox
	$(GO) run ./cmd/yamlcheck

e2e: ## End-to-end tests (needs NETBOX_URL and NETBOX_TOKEN)
	./tests/e2e/run.sh

e2e-local: ## Provision a throwaway NetBox from source, then run e2e
	./tests/e2e/provision-local.sh

clean: ## Remove build output
	rm -f $(BINARY) coverage.out
