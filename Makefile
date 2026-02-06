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

.PHONY: test
test: fmt vet ## Run unit tests
	go test ./pkg/... -coverprofile cover.out

.PHONY: test-integration
test-integration: ## Run integration tests with envtest
	go test ./test/... -tags=integration -v

##@ Build

.PHONY: build
build: fmt vet ## Build manager binary
	go build -o bin/manager cmd/main.go

.PHONY: run
run: fmt vet ## Run from your host
	go run cmd/main.go

.PHONY: docker-build
docker-build: ## Build docker image
	docker build -t virt-platform-operator:latest .

.PHONY: docker-push
docker-push: ## Push docker image
	docker push virt-platform-operator:latest

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
	go run cmd/rbac-gen/main.go -output config/rbac/role.yaml

.PHONY: update-crds
update-crds: ## Update CRD collection from upstream
	hack/update-crds.sh

.PHONY: verify-crds
verify-crds: ## Verify CRDs can be loaded
	@echo "Verifying CRDs..."
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

##@ Local Development (Kind)

CLUSTER_NAME ?= virt-platform-operator
IMAGE_NAME ?= virt-platform-operator:latest

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
	kubectl logs -f -n virt-platform-operator-system \
		-l app=virt-platform-operator \
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
	kubectl get pods -n virt-platform-operator-system --context kind-$(CLUSTER_NAME)
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
