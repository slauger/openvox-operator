IMG ?= ghcr.io/slauger/openvox-operator:latest
OPENVOX_SERVER_IMG ?= ghcr.io/slauger/openvox-server:latest
OPENVOX_E2E_CODE_IMG ?= ghcr.io/slauger/openvox-e2e-code:latest
OPENVOX_AGENT_IMG ?= ghcr.io/slauger/openvox-agent:latest
OPENVOX_MOCK_IMG ?= ghcr.io/slauger/openvox-mock:latest
NAMESPACE ?= openvox-system
IMAGE_REGISTRY ?= ghcr.io/slauger
CONTAINER_TOOL ?= $(shell which podman 2>/dev/null || which docker 2>/dev/null)
CONTROLLER_GEN = go tool controller-gen
GOVULNCHECK = go tool govulncheck

LOCALBIN ?= $(shell pwd)/bin
CHAINSAW_VERSION ?= v0.2.14
CHAINSAW ?= $(LOCALBIN)/chainsaw

.PHONY: all
all: build

##@ Development

.PHONY: manifests
manifests: ## Generate CRD manifests.
	$(CONTROLLER_GEN) crd webhook paths="./..." output:crd:dir=config/crd/bases output:webhook:dir=config/webhook
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

IMAGE_TAG ?= $(shell git describe --always)

.PHONY: local-build
local-build: ## Build all images for local development (Docker Desktop K8s).
	$(CONTAINER_TOOL) build -t ghcr.io/slauger/openvox-operator:$(IMAGE_TAG) -f images/openvox-operator/Containerfile .
	$(CONTAINER_TOOL) build -t ghcr.io/slauger/openvox-server:$(IMAGE_TAG) -f images/openvox-server/Containerfile .
	$(CONTAINER_TOOL) build -t ghcr.io/slauger/openvox-e2e-code:latest -f images/openvox-e2e-code/Containerfile .
	$(CONTAINER_TOOL) build -t ghcr.io/slauger/openvox-agent:latest -f images/openvox-agent/Containerfile images/openvox-agent/
	$(CONTAINER_TOOL) build -t ghcr.io/slauger/openvox-mock:latest -f images/openvox-mock/Containerfile .
	@echo "Built ghcr.io/slauger/openvox-operator:$(IMAGE_TAG)"
	@echo "Built ghcr.io/slauger/openvox-server:$(IMAGE_TAG)"
	@echo "Built ghcr.io/slauger/openvox-e2e-code:latest"
	@echo "Built ghcr.io/slauger/openvox-agent:latest"
	@echo "Built ghcr.io/slauger/openvox-mock:latest"

.PHONY: local-deploy
local-deploy: local-build local-install ## Build images and deploy operator via Helm.
	@echo ""
	@echo "Operator deployed with openvox-operator:$(IMAGE_TAG)"

STACK_NAMESPACE ?= openvox
STACK_VALUES ?= charts/openvox-stack/values.yaml

##@ Deployment

# When IMAGE_TAG is explicitly passed (e.g. make install IMAGE_TAG=487ea36),
# configure helm to pull that specific image from the registry.
ifeq ($(origin IMAGE_TAG),command line)
HELM_SET ?= --set image.repository=$(IMAGE_REGISTRY)/openvox-operator --set image.tag=$(IMAGE_TAG) --set image.pullPolicy=Always
STACK_HELM_SET ?= --set config.image.repository=$(IMAGE_REGISTRY)/openvox-server --set config.image.tag=$(IMAGE_TAG) --set config.image.pullPolicy=Always
endif

.PHONY: install
install: manifests ## Install operator via Helm (supports IMAGE_TAG=<tag>).
	helm upgrade --install openvox-operator charts/openvox-operator \
		--namespace $(NAMESPACE) --create-namespace $(HELM_SET)

.PHONY: local-install
local-install: HELM_SET := --set image.tag=$(IMAGE_TAG) --set image.pullPolicy=Never
local-install: install ## Install operator via Helm with local images (no build).

.PHONY: stack
stack: ## Deploy openvox-stack via Helm (supports IMAGE_TAG=<tag>).
	helm upgrade --install openvox-stack charts/openvox-stack \
		--namespace $(STACK_NAMESPACE) --create-namespace \
		--values $(STACK_VALUES) $(STACK_HELM_SET)

.PHONY: local-stack
local-stack: STACK_HELM_SET := --set config.image.tag=$(IMAGE_TAG) --set config.image.pullPolicy=Never
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
	helm lint charts/openvox-stack
	helm lint charts/openvox-db-postgres

.PHONY: helm-unittest
helm-unittest: ## Run helm-unittest for all charts.
	helm unittest charts/openvox-operator
	helm unittest charts/openvox-stack
	helm unittest charts/openvox-db-postgres

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
ci: lint vet test check-manifests vulncheck helm-lint helm-unittest ## Run all CI checks locally.
	@echo "All CI checks passed."

##@ E2E

.PHONY: chainsaw
chainsaw: $(CHAINSAW) ## Download chainsaw binary.
$(CHAINSAW):
	@mkdir -p $(LOCALBIN)
	@OS=$(shell go env GOOS) ARCH=$(shell go env GOARCH); \
	curl -sSfL "https://github.com/kyverno/chainsaw/releases/download/$(CHAINSAW_VERSION)/chainsaw_$${OS}_$${ARCH}.tar.gz" | tar xz -C $(LOCALBIN) chainsaw
	@chmod +x $(CHAINSAW)

E2E_CHAINSAW = IMAGE_TAG=$(IMAGE_TAG) IMAGE_REGISTRY=$(IMAGE_REGISTRY) $(CHAINSAW) test --config tests/e2e/chainsaw-config.yaml


.PHONY: e2e-setup
e2e-setup: ## Install all E2E external dependencies (CNPG, Envoy Gateway, cert-manager).
	bash tests/e2e/setup.sh all

.PHONY: e2e-wait
e2e-wait: ## Wait for E2E dependencies to be available (pre-installed via ArgoCD in CI).
	@echo "Waiting for E2E dependencies (up to 5m)..."
	@for DEP in cnpg-system/cnpg-controller-manager envoy-gateway-system/envoy-gateway cert-manager/cert-manager cert-manager/cert-manager-webhook; do \
		NS=$${DEP%%/*}; DEPLOY=$${DEP##*/}; \
		echo "  Waiting for deployment/$${DEPLOY} in $${NS}..."; \
		END=$$(( $$(date +%s) + 300 )); \
		until kubectl get deployment/"$${DEPLOY}" -n "$${NS}" >/dev/null 2>&1; do \
			if [ $$(date +%s) -ge $${END} ]; then echo "Timed out waiting for deployment/$${DEPLOY} in $${NS}"; exit 1; fi; \
			sleep 5; \
		done; \
		kubectl wait --for=condition=Available deployment/"$${DEPLOY}" -n "$${NS}" --timeout=3m; \
	done
	@echo "All E2E dependencies are available."

.PHONY: e2e-cleanup
e2e-cleanup: ## Remove operator and all E2E test namespaces (keeps CRDs).
	@echo "Cleaning up leftover E2E namespaces (operator still running to process finalizers)..."
	@kubectl get namespaces -o name | grep '^namespace/e2e-' | xargs -r kubectl delete --timeout=120s --ignore-not-found 2>/dev/null || true
	@# Strip certificate finalizers from any namespaces stuck in Terminating
	@for ns in $$(kubectl get namespaces -o jsonpath='{range .items[?(@.status.phase=="Terminating")]}{.metadata.name}{"\n"}{end}' 2>/dev/null); do \
		echo "Namespace $${ns} stuck in Terminating, stripping certificate finalizers..."; \
		kubectl get certificates.openvox.voxpupuli.org -n "$${ns}" -o name 2>/dev/null \
			| xargs -r -I{} kubectl patch {} -n "$${ns}" --type=merge -p '{"metadata":{"finalizers":null}}' 2>/dev/null || true; \
	done
	@# Wait for Terminating namespaces to be fully gone
	@for ns in $$(kubectl get namespaces -o jsonpath='{range .items[?(@.status.phase=="Terminating")]}{.metadata.name}{"\n"}{end}' 2>/dev/null); do \
		echo "Waiting for namespace $${ns} to terminate..."; \
		kubectl wait --for=delete namespace/"$${ns}" --timeout=30s 2>/dev/null || true; \
	done
	helm uninstall openvox-operator --namespace $(NAMESPACE) --wait 2>/dev/null || true
	@# Strip certificate finalizers in operator namespace if stuck
	@if kubectl get namespace $(NAMESPACE) -o jsonpath='{.status.phase}' 2>/dev/null | grep -q Terminating; then \
		echo "Namespace $(NAMESPACE) stuck in Terminating, stripping certificate finalizers..."; \
		kubectl get certificates.openvox.voxpupuli.org -n $(NAMESPACE) -o name 2>/dev/null \
			| xargs -r -I{} kubectl patch {} -n $(NAMESPACE) --type=merge -p '{"metadata":{"finalizers":null}}' 2>/dev/null || true; \
	fi
	kubectl delete namespace $(NAMESPACE) --ignore-not-found --wait 2>/dev/null || true

.PHONY: e2e-cleanup-full
e2e-cleanup-full: e2e-cleanup ## Full cleanup including CRDs (use between test groups).
	@echo "Removing CRDs..."
	@kubectl get crds -o name | grep 'openvox\.voxpupuli\.org' | xargs -r kubectl delete --ignore-not-found 2>/dev/null || true

.PHONY: e2e-operator-base
e2e-operator-base: e2e-cleanup ## Install operator: webhooks=false, gatewayAPI=false.
	helm upgrade --install openvox-operator charts/openvox-operator \
		--namespace $(NAMESPACE) --create-namespace \
		--set image.repository=$(IMAGE_REGISTRY)/openvox-operator \
		--set image.tag=$(IMAGE_TAG) \
		--set image.pullPolicy=Always \
		--set resources.limits.cpu=null \
		--set resources.limits.memory=null \
		--set webhook.enabled=false \
		--set gatewayAPI.enabled=false
	kubectl wait --for=condition=Available deployment/openvox-operator \
		-n $(NAMESPACE) --timeout=2m

.PHONY: e2e-operator-gateway
e2e-operator-gateway: e2e-cleanup ## Install operator: webhooks=false, gatewayAPI=true.
	helm upgrade --install openvox-operator charts/openvox-operator \
		--namespace $(NAMESPACE) --create-namespace \
		--set image.repository=$(IMAGE_REGISTRY)/openvox-operator \
		--set image.tag=$(IMAGE_TAG) \
		--set image.pullPolicy=Always \
		--set resources.limits.cpu=null \
		--set resources.limits.memory=null \
		--set webhook.enabled=false \
		--set gatewayAPI.enabled=true
	kubectl wait --for=condition=Available deployment/openvox-operator \
		-n $(NAMESPACE) --timeout=2m

.PHONY: e2e-operator-webhooks-cm
e2e-operator-webhooks-cm: e2e-cleanup ## Install operator: webhooks=true, cert-manager.
	helm upgrade --install openvox-operator charts/openvox-operator \
		--namespace $(NAMESPACE) --create-namespace \
		--set image.repository=$(IMAGE_REGISTRY)/openvox-operator \
		--set image.tag=$(IMAGE_TAG) \
		--set image.pullPolicy=Always \
		--set resources.limits.cpu=null \
		--set resources.limits.memory=null \
		--set webhook.enabled=true \
		--set webhook.certManager.enabled=true \
		--set gatewayAPI.enabled=false
	kubectl wait --for=condition=Available deployment/openvox-operator \
		-n $(NAMESPACE) --timeout=2m

.PHONY: e2e-run-test
e2e-run-test: chainsaw ## Run a single E2E test. Usage: make e2e-run-test TEST=single-node
	$(E2E_CHAINSAW) tests/e2e/$(TEST); \
	EXIT=$$?; kubectl get namespaces -o name | grep '^namespace/e2e-' | xargs -r kubectl delete --timeout=120s --ignore-not-found 2>/dev/null || true; exit $$EXIT

.PHONY: e2e-group-base
e2e-group-base: e2e-operator-base chainsaw ## Group: base tests (stack, agent, database).
	$(E2E_CHAINSAW) \
		tests/e2e/single-node \
		tests/e2e/multi-server \
		tests/e2e/agent-basic \
		tests/e2e/agent-broken \
		tests/e2e/agent-idempotent \
		tests/e2e/agent-concurrent \
		tests/e2e/agent-report \
		tests/e2e/database-cnpg \
		tests/e2e/readonly-rootfs \
		tests/e2e/code-pvc \
		tests/e2e/autosign-policy \
		tests/e2e/cert-rotation; \
	EXIT=$$?; $(MAKE) e2e-cleanup; exit $$EXIT

.PHONY: e2e-group-enc
e2e-group-enc: e2e-operator-base chainsaw ## Group: ENC and full agent tests.
	$(E2E_CHAINSAW) \
		tests/e2e/agent-enc \
		tests/e2e/agent-full; \
	EXIT=$$?; $(MAKE) e2e-cleanup; exit $$EXIT

.PHONY: e2e-group-ca
e2e-group-ca: e2e-operator-base chainsaw ## Group: CA tests (external CA).
	$(E2E_CHAINSAW) \
		tests/e2e/external-ca; \
	EXIT=$$?; $(MAKE) e2e-cleanup; exit $$EXIT

.PHONY: e2e-group-gateway
e2e-group-gateway: e2e-operator-gateway chainsaw ## Group: Gateway API TLSRoute tests.
	$(E2E_CHAINSAW) \
		tests/e2e/pool-gateway; \
	EXIT=$$?; $(MAKE) e2e-cleanup; exit $$EXIT

.PHONY: e2e-group-webhooks-cm
e2e-group-webhooks-cm: e2e-operator-webhooks-cm chainsaw ## Group: webhook tests with cert-manager.
	$(E2E_CHAINSAW) \
		tests/e2e/webhook-validation-server \
		tests/e2e/webhook-validation-config \
		tests/e2e/webhook-validation-database \
		tests/e2e/webhook-smoke; \
	EXIT=$$?; $(MAKE) e2e-cleanup; exit $$EXIT

.PHONY: e2e-all
e2e-all: ## Run all E2E test groups sequentially.
	$(MAKE) e2e-group-base
	$(MAKE) e2e-group-enc
	$(MAKE) e2e-group-ca
	$(MAKE) e2e-group-gateway
	$(MAKE) e2e-group-webhooks-cm

##@ Help

.PHONY: help
help: ## Display this help.
	@awk 'BEGIN {FS = ":.*##"; printf "\nUsage:\n  make \033[36m<target>\033[0m\n"} /^[a-zA-Z_0-9-]+:.*?##/ { printf "  \033[36m%-15s\033[0m %s\n", $$1, $$2 } /^##@/ { printf "\n\033[1m%s\033[0m\n", substr($$0, 5) } ' $(MAKEFILE_LIST)
