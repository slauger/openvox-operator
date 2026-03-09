IMG ?= ghcr.io/slauger/openvox-operator:latest
OPENVOX_SERVER_IMG ?= ghcr.io/slauger/openvox-server:latest
NAMESPACE ?= openvox-system
CONTAINER_TOOL ?= $(shell which podman 2>/dev/null || which docker 2>/dev/null)
CONTROLLER_GEN ?= $(shell which controller-gen 2>/dev/null || echo $(GOBIN)/controller-gen)
GOBIN ?= $(shell go env GOPATH)/bin

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
	@echo "Built openvox-operator:$(LOCAL_TAG)"
	@echo "Built openvox-server:$(LOCAL_TAG)"

.PHONY: local-deploy
local-deploy: local-build install ## Build images, install CRDs, and deploy operator via Helm.
	helm upgrade --install openvox-operator charts/openvox-operator \
		--namespace $(NAMESPACE) --create-namespace \
		--set image.repository=openvox-operator \
		--set image.tag=$(LOCAL_TAG) \
		--set image.pullPolicy=Never \
		--set podAnnotations.imageId=$$($(CONTAINER_TOOL) image inspect openvox-operator:$(LOCAL_TAG) --format '{{.Id}}')
	@echo ""
	@echo "Operator deployed with openvox-operator:$(LOCAL_TAG)"

STACK_NAMESPACE ?= openvox
STACK_VALUES ?= charts/openvox-stack/ci/single-node-values.yaml

.PHONY: local-stack
local-stack: ## Deploy openvox-stack via Helm with local images.
	helm upgrade --install openvox-stack charts/openvox-stack \
		--namespace $(STACK_NAMESPACE) --create-namespace \
		--values $(STACK_VALUES) \
		--set environment.image.repository=openvox-server \
		--set environment.image.tag=$(LOCAL_TAG) \
		--set environment.image.pullPolicy=Never
	@echo ""
	@echo "Stack deployed with openvox-server:$(LOCAL_TAG) in $(STACK_NAMESPACE) using $(STACK_VALUES)"

##@ Deployment

.PHONY: install
install: manifests ## Install CRDs into the cluster.
	kubectl apply -f config/crd/bases/

.PHONY: uninstall
uninstall: ## Uninstall CRDs from the cluster.
	kubectl delete -f config/crd/bases/

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

##@ Tools

.PHONY: controller-gen
controller-gen: ## Install controller-gen.
	go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.16.5

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)
