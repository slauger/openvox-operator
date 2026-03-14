IMG ?= ghcr.io/slauger/openvox-operator:latest
OPENVOX_SERVER_IMG ?= ghcr.io/slauger/openvox-server:latest
OPENVOX_CODE_IMG ?= ghcr.io/slauger/openvox-code:latest
OPENVOX_AGENT_IMG ?= ghcr.io/slauger/openvox-agent:latest
OPENVOX_MOCK_IMG ?= ghcr.io/slauger/openvox-mock:latest
NAMESPACE ?= openvox-system
CONTAINER_TOOL ?= $(shell which podman 2>/dev/null || which docker 2>/dev/null)
CONTROLLER_GEN = go tool controller-gen
GOVULNCHECK = go tool govulncheck
CHAINSAW = go tool chainsaw

.PHONY: all
all: build

##@ Development

.PHONY: manifests
manifests: ## Generate CRD manifests.
	$(CONTROLLER_GEN) crd paths="./api/..." output:crd:dir=config/crd/bases
	cp config/crd/bases/*.yaml charts/openvox-operator/crds/

.PHONY: generate
generate: ## Generate deepcopy methods.
	$(CONTROLLER_GEN) object paths="./api/..."

.PHONY: fmt
fmt: ## Run go fmt.
	go fmt ./...

.PHONY: vet
vet: ## Run go vet.
	go vet ./...

.PHONY: test
test: manifests generate fmt vet ## Run tests.
	go test ./... -coverprofile cover.out

##@ Build

.PHONY: build
build: manifests generate fmt vet ## Build operator binary.
	go build -o bin/manager ./cmd/main.go

.PHONY: run
run: manifests generate fmt vet ## Run the operator locally against the configured cluster.
	go run ./cmd/main.go

.PHONY: docker-build
docker-build: ## Build container image.
	$(CONTAINER_TOOL) build -t $(IMG) -f images/openvox-operator/Containerfile .

.PHONY: docker-push
docker-push: ## Push container image.
	$(CONTAINER_TOOL) push $(IMG)

##@ Local Development

LOCAL_TAG ?= $(shell git describe --always)

.PHONY: local-build
local-build: ## Build all images for local development (Docker Desktop K8s).
	$(CONTAINER_TOOL) build -t openvox-operator:$(LOCAL_TAG) -f images/openvox-operator/Containerfile .
	$(CONTAINER_TOOL) build -t openvox-server:$(LOCAL_TAG) -f images/openvox-server/Containerfile .
	$(CONTAINER_TOOL) build -t openvox-code:latest -f images/openvox-code/Containerfile .
	$(CONTAINER_TOOL) build -t openvox-agent:latest -f images/openvox-agent/Containerfile images/openvox-agent/
	$(CONTAINER_TOOL) build -t openvox-mock:latest -f images/openvox-mock/Containerfile .
	@echo "Built openvox-operator:$(LOCAL_TAG)"
	@echo "Built openvox-server:$(LOCAL_TAG)"
	@echo "Built openvox-code:latest"
	@echo "Built openvox-agent:latest"
	@echo "Built openvox-mock:latest"

.PHONY: local-deploy
local-deploy: local-build local-install ## Build images and deploy operator via Helm.
	@echo ""
	@echo "Operator deployed with openvox-operator:$(LOCAL_TAG)"

STACK_NAMESPACE ?= openvox
STACK_VALUES ?= charts/openvox-stack/ci/single-node-values.yaml

##@ Deployment

.PHONY: install
install: manifests ## Install operator via Helm with default images.
	helm upgrade --install openvox-operator charts/openvox-operator \
		--namespace $(NAMESPACE) --create-namespace $(HELM_SET)

.PHONY: local-install
local-install: HELM_SET := --set image.repository=openvox-operator --set image.tag=$(LOCAL_TAG) --set image.pullPolicy=Never
local-install: install ## Install operator via Helm with local images (no build).

.PHONY: stack
stack: ## Deploy openvox-stack via Helm with default images.
	helm upgrade --install openvox-stack charts/openvox-stack \
		--namespace $(STACK_NAMESPACE) --create-namespace \
		--values $(STACK_VALUES) $(STACK_HELM_SET)

.PHONY: local-stack
local-stack: STACK_HELM_SET := --set config.image.repository=openvox-server --set config.image.tag=$(LOCAL_TAG) --set config.image.pullPolicy=Never
local-stack: stack ## Deploy openvox-stack via Helm with local images.

.PHONY: unstack
unstack: ## Remove openvox-stack from the cluster.
	helm uninstall openvox-stack --namespace $(STACK_NAMESPACE)

.PHONY: uninstall
uninstall: ## Remove stack, operator, and CRDs from the cluster.
	-helm uninstall openvox-stack --namespace $(STACK_NAMESPACE) 2>/dev/null
	-helm uninstall openvox-operator --namespace $(NAMESPACE) 2>/dev/null
	-kubectl delete -f config/crd/bases/ --ignore-not-found

.PHONY: deploy
deploy: manifests ## Deploy operator to the cluster.
	kubectl create namespace openvox-system --dry-run=client -o yaml | kubectl apply -f -
	kubectl apply -f config/rbac/
	kubectl apply -f config/manager/

.PHONY: undeploy
undeploy: ## Undeploy operator from the cluster.
	kubectl delete -f config/manager/ --ignore-not-found
	kubectl delete -f config/rbac/ --ignore-not-found

##@ Helm

.PHONY: helm-lint
helm-lint: ## Lint the Helm chart.
	helm lint charts/openvox-operator

.PHONY: helm-template
helm-template: ## Render Helm chart templates locally.
	helm template openvox-operator charts/openvox-operator

##@ CI

GOLANGCI_LINT ?= $(shell which golangci-lint 2>/dev/null)

.PHONY: lint
lint: ## Run golangci-lint.
	$(GOLANGCI_LINT) run ./...

.PHONY: vulncheck
vulncheck: ## Run govulncheck.
	$(GOVULNCHECK) ./...

.PHONY: check-manifests
check-manifests: manifests generate ## Check for CRD and deepcopy drift.
	@if ! git diff --quiet; then \
		echo "error: generated files are out of date. Run 'make manifests generate' and commit the result."; \
		git diff --stat; \
		exit 1; \
	fi

.PHONY: ci
ci: lint vet test check-manifests vulncheck helm-lint ## Run all CI checks locally.
	@echo "All CI checks passed."

##@ E2E

.PHONY: e2e
e2e: local-deploy ## Run E2E tests against the current cluster.
	$(CHAINSAW) test tests/e2e/

##@ Help

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)
