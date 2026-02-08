# K8s Wormsign Makefile

# Go parameters
GOBIN ?= $(shell go env GOPATH)/bin

# Tool versions
CONTROLLER_GEN_VERSION ?= v0.17.0
GOLANGCI_LINT_VERSION ?= v1.62.2

# Directories
CRD_DIR = deploy/helm/wormsign/crds
API_DIR = api/v1alpha1
HELM_CHART_DIR = deploy/helm/wormsign

##@ General

.PHONY: help
help: ## Display this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: generate
generate: controller-gen ## Generate deepcopy methods and CRD manifests
	$(CONTROLLER_GEN) object:headerFile=hack/boilerplate.go.txt paths=./$(API_DIR)/...
	mkdir -p $(CRD_DIR)
	$(CONTROLLER_GEN) crd:crdVersions=v1 paths=./$(API_DIR)/... output:crd:dir=$(CRD_DIR)

.PHONY: fmt
fmt: ## Run go fmt against code
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code
	go vet ./...

.PHONY: test
test: ## Run tests
	go test ./... -v

.PHONY: build
build: ## Build the project
	go build ./...

.PHONY: lint
lint: golangci-lint ## Run golangci-lint against code
	$(GOLANGCI_LINT) run ./...

.PHONY: helm-lint
helm-lint: ## Lint the Helm chart
	helm lint $(HELM_CHART_DIR)

.PHONY: verify-generate
verify-generate: generate ## Verify generated files are up to date
	@if [ -n "$$(git diff --name-only)" ]; then \
		echo "ERROR: Generated files are out of date. Run 'make generate' and commit the result."; \
		git diff --name-only; \
		exit 1; \
	fi

##@ Tools

CONTROLLER_GEN = $(GOBIN)/controller-gen
.PHONY: controller-gen
controller-gen: ## Install controller-gen if not present
	@test -s $(CONTROLLER_GEN) || go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION)

GOLANGCI_LINT = $(GOBIN)/golangci-lint
.PHONY: golangci-lint
golangci-lint: ## Install golangci-lint if not present
	@test -s $(GOLANGCI_LINT) || go install github.com/golangci/golangci-lint/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)
