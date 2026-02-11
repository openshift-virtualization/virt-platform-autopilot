.PHONY: help
help: ## Display this help
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)

##@ Development

.PHONY: fmt
fmt: ## Run go fmt against code
	go fmt ./...

.PHONY: vet
vet: ## Run go vet against code
	go vet ./...

.PHONY: goimport
goimport:
	go install golang.org/x/tools/cmd/goimports@latest
	goimports -w -local="kubevirt.io,github.com/kubevirt,github.com/kubevirt/hyperconverged-cluster-operator"  $(shell find . -type f -name '*.go' ! -path "*/vendor/*" ! -path "./_kubevirtci/*" ! -path "*zz_generated*" )

.PHONY: test
test: fmt vet goimport ## Run unit tests
	go test ./pkg/... -coverprofile cover.out

# ENVTEST_K8S_VERSION refers to the version of kubebuilder assets to be downloaded by envtest binary.
ENVTEST_K8S_VERSION = 1.35.0
ENVTEST = $(shell pwd)/bin/setup-envtest
GINKGO = $(shell pwd)/bin/ginkgo

.PHONY: test-integration
test-integration: envtest ginkgo ## Run integration tests with envtest
	KUBEBUILDER_ASSETS="$(shell $(ENVTEST) use $(ENVTEST_K8S_VERSION) --bin-dir $(shell pwd)/bin -p path)" $(GINKGO) -v --trace ./test/

.PHONY: test-e2e
test-e2e: docker-build ## Run E2E tests on kind cluster
	./hack/run-e2e.sh

.PHONY: envtest
envtest: $(ENVTEST) ## Download envtest-setup locally if necessary
$(ENVTEST): $(LOCALBIN)
	GOBIN=$(shell pwd)/bin go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest

.PHONY: ginkgo
ginkgo: $(GINKGO) ## Download ginkgo locally if necessary
$(GINKGO): $(LOCALBIN)
	GOBIN=$(shell pwd)/bin go install github.com/onsi/ginkgo/v2/ginkgo@latest

LOCALBIN ?= $(shell pwd)/bin
$(LOCALBIN):
	mkdir -p $(LOCALBIN)

# Container tool - auto-detects docker or podman, or can be overridden
# Usage: make docker-build
#        CONTAINER_TOOL=podman make docker-build
CONTAINER_TOOL ?= $(shell command -v docker 2>/dev/null || command -v podman 2>/dev/null)

ifeq ($(CONTAINER_TOOL),)
$(error Neither docker nor podman found in PATH. Please install one of them.)
endif

##@ Build

.PHONY: build
build: fmt vet ## Build manager binary
	go build -o bin/manager cmd/main.go

.PHONY: run
run: fmt vet ## Run from your host
	go run cmd/main.go

.PHONY: docker-build
docker-build: ## Build container image
	$(CONTAINER_TOOL) build -t virt-platform-autopilot:latest .

.PHONY: docker-push
docker-push: ## Push container image
	$(CONTAINER_TOOL) push virt-platform-autopilot:latest

##@ Deployment

.PHONY: deploy
deploy: ## Deploy controller to the cluster
	kubectl apply -k config/default

.PHONY: undeploy
undeploy: ## Undeploy controller from the cluster
	kubectl delete -k config/default

##@ Code Generation

.PHONY: generate-rbac
generate-rbac: ## Generate RBAC from assets
	@echo "Generating RBAC from assets..."
	@go run cmd/rbac-gen/main.go

.PHONY: verify-rbac
verify-rbac: ## Verify RBAC matches generated (for CI)
	@echo "Verifying RBAC is up-to-date..."
	@go run cmd/rbac-gen/main.go --dry-run > /tmp/generated-rbac.yaml
	@if ! diff -u config/rbac/role.yaml /tmp/generated-rbac.yaml; then \
		echo ""; \
		echo "❌ ERROR: RBAC is out of sync with assets!"; \
		echo ""; \
		echo "The committed config/rbac/role.yaml does not match the generated RBAC."; \
		echo ""; \
		echo "To fix:"; \
		echo "  1. Run: make generate-rbac"; \
		echo "  2. Commit the updated config/rbac/role.yaml"; \
		echo ""; \
		rm -f /tmp/generated-rbac.yaml; \
		exit 1; \
	fi
	@rm -f /tmp/generated-rbac.yaml
	@echo "✓ RBAC is up-to-date"

.PHONY: update-crds
update-crds: ## Update CRD collection from upstream
	hack/update-crds.sh

.PHONY: verify-crds
verify-crds: ## Verify CRDs match upstream (for CI)
	@echo "Verifying CRDs match upstream..."
	@hack/update-crds.sh --verify

.PHONY: validate-crds
validate-crds: ## Validate CRDs can be loaded (parser check)
	@echo "Validating CRDs can be parsed..."
	@go test ./test/crd_test.go -v

##@ Dependencies

.PHONY: tidy
tidy: ## Run go mod tidy
	go mod tidy

.PHONY: vendor
vendor: ## Run go mod vendor
	go mod vendor

##@ Linting

GOLANGCI_LINT ?= $(shell which golangci-lint)
ifeq ($(GOLANGCI_LINT),)
GOLANGCI_LINT = $(shell go env GOPATH)/bin/golangci-lint
endif

.PHONY: lint
lint: ## Run golangci-lint
	@if ! command -v $(GOLANGCI_LINT) >/dev/null 2>&1; then \
		echo "golangci-lint v2 not found. Installing..."; \
		go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@latest; \
	fi
	$(GOLANGCI_LINT) run

SHELLCHECK ?= $(shell which shellcheck)

.PHONY: shellcheck
shellcheck: ## Run shellcheck on all shell scripts
	@if ! command -v shellcheck >/dev/null 2>&1; then \
		echo "shellcheck not found. Please install it:"; \
		echo "  On Fedora/RHEL: sudo dnf install shellcheck"; \
		echo "  On Ubuntu/Debian: sudo apt-get install shellcheck"; \
		echo "  On macOS: brew install shellcheck"; \
		exit 1; \
	fi
	@echo "Running shellcheck on hack/ scripts..."
	@find hack -name '*.sh' -type f -exec shellcheck -x {} +

##@ Observability

.PHONY: test-alerts
test-alerts: ## Test Prometheus alert rules with promtool
	@hack/test-alert-rules.sh

##@ Local Development (Kind)

CLUSTER_NAME ?= virt-platform-autopilot
IMAGE_NAME ?= virt-platform-autopilot:latest

.PHONY: kind-setup
kind-setup: ## Setup local Kind cluster with CRDs and mock HCO
	CLUSTER_NAME=$(CLUSTER_NAME) ./hack/kind-cluster.sh setup

.PHONY: kind-create
kind-create: ## Create local Kind cluster
	CLUSTER_NAME=$(CLUSTER_NAME) ./hack/kind-cluster.sh create

.PHONY: kind-delete
kind-delete: ## Delete local Kind cluster
	CLUSTER_NAME=$(CLUSTER_NAME) ./hack/kind-cluster.sh delete

.PHONY: kind-status
kind-status: ## Check Kind cluster status
	CLUSTER_NAME=$(CLUSTER_NAME) ./hack/kind-cluster.sh status

.PHONY: kind-install-crds
kind-install-crds: ## Install CRDs into Kind cluster
	CLUSTER_NAME=$(CLUSTER_NAME) ./hack/kind-cluster.sh install-crds

.PHONY: kind-create-mock-hco
kind-create-mock-hco: ## Create mock HCO instance
	CLUSTER_NAME=$(CLUSTER_NAME) ./hack/kind-cluster.sh create-mock-hco

.PHONY: kind-load
kind-load: docker-build ## Build and load operator image into Kind
	CLUSTER_NAME=$(CLUSTER_NAME) IMAGE_NAME=$(IMAGE_NAME) ./hack/deploy-local.sh load

.PHONY: deploy-local
deploy-local: ## Deploy operator to local Kind cluster (full)
	CLUSTER_NAME=$(CLUSTER_NAME) IMAGE_NAME=$(IMAGE_NAME) ./hack/deploy-local.sh deploy

.PHONY: redeploy-local
redeploy-local: ## Quick redeploy to Kind (rebuild + restart)
	CLUSTER_NAME=$(CLUSTER_NAME) IMAGE_NAME=$(IMAGE_NAME) ./hack/deploy-local.sh redeploy

.PHONY: undeploy-local
undeploy-local: ## Undeploy operator from Kind cluster
	CLUSTER_NAME=$(CLUSTER_NAME) ./hack/deploy-local.sh undeploy

.PHONY: logs-local
logs-local: ## Tail operator logs from Kind cluster
	kubectl logs -f -n openshift-cnv \
		-l app=virt-platform-autopilot \
		--context kind-$(CLUSTER_NAME) \
		--tail=100

.PHONY: status-local
status-local: ## Show operator status in Kind cluster
	CLUSTER_NAME=$(CLUSTER_NAME) ./hack/deploy-local.sh status

.PHONY: debug-local
debug-local: ## Show debugging info for local deployment
	@echo "=== Cluster Info ==="
	kubectl cluster-info --context kind-$(CLUSTER_NAME)
	@echo ""
	@echo "=== Operator Pods ==="
	kubectl get pods -n openshift-cnv --context kind-$(CLUSTER_NAME)
	@echo ""
	@echo "=== HyperConverged CR ==="
	kubectl get hyperconverged -n openshift-cnv --context kind-$(CLUSTER_NAME) -o wide
	@echo ""
	@echo "=== Managed Resources ==="
	kubectl get machineconfigs,kubeletconfigs,nodehealthchecks --context kind-$(CLUSTER_NAME)

##@ All-in-one targets

.PHONY: all
all: fmt vet test build ## Build and test everything

.PHONY: dev-cycle
dev-cycle: fmt vet test redeploy-local ## Full dev cycle: format, test, redeploy
