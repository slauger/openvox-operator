# Testing

## Overview

| Type | Tool | Command | Requires Cluster |
|------|------|---------|:---:|
| Go unit tests | `go test` | `make test` | No |
| CRD validation (CEL) | envtest | `go test ./api/...` | No |
| Webhook unit tests | envtest | `go test ./internal/webhook/...` | No |
| Helm chart lint | `helm lint` | `make helm-lint` | No |
| Helm chart unit tests | `helm unittest` | `make helm-unittest` | No |
| Go linting | `golangci-lint` | `make lint` | No |
| Go vulnerability check | `govulncheck` | `make vulncheck` | No |
| CRD/deepcopy drift check | `controller-gen` | `make check-manifests` | No |
| Container image lint | `hadolint` | via `_container-build.yaml` | No |
| Shell script lint | `shellcheck` | via `_shellcheck.yaml` | No |
| Unicode lint | `grep -P` | via `_unicode-lint.yaml` | No |
| E2E tests | Chainsaw | `make e2e-group-*` | Yes |

Run all offline checks at once:

```bash
make ci
```

## CI/CD Workflows

### Workflow Overview

| Workflow | File | Trigger | What it does |
|----------|------|---------|-------------|
| CI | `ci.yaml` | Push to main/develop, PRs | Linting + all 6 image builds; pushes `:develop` tag on develop branch |
| E2E | `e2e.yaml` | Push to develop, manual | Builds + pushes all 6 images, runs E2E test groups, cleans up images |
| Release | `release.yaml` | Manual (main only) | semantic-release, builds operator/server/db with version tag + `:latest`, publishes Helm charts |

### Reusable Workflows

| Workflow | File | Purpose |
|----------|------|---------|
| Container Build | `_container-build.yaml` | Multi-arch image build, optional push, signing, SBOM |
| Go | `_go.yaml` | Go build, test, vet, vulncheck, lint |
| Helm | `_helm.yaml` | Helm lint + unittest |
| Shellcheck | `_shellcheck.yaml` | Shell script linting |
| Unicode Lint | `_unicode-lint.yaml` | Detect suspicious Unicode characters |
| Cleanup Images | `_cleanup-images.yaml` | Delete E2E images from ghcr.io |

### Image Tagging Strategy

| Context | Images | Push | Tag | `:latest` |
|---------|--------|:----:|-----|:---------:|
| CI - PR | all 6 | No | - | - |
| CI - push develop | all 6 | Yes | `:develop` | No |
| CI - push main | all 6 | No | - | - |
| E2E - push develop | all 6 | Yes | `:develop` (or custom) | No |
| E2E - manual | all 6 | Yes | custom `image_tag` | No |
| Release | operator, server, db | Yes | `:x.y.z` | Yes |

**Key points:**

- `:develop` images are unstable, rebuilt on every push to develop
- `:latest` is only set by `release.yaml` and always points to the latest release
- E2E images tagged with short SHAs are automatically cleaned up after tests
- Cleanup can be disabled via `cleanup_images: false` in `workflow_dispatch`

### Which Images Are Released?

Only the three production images and three Helm charts are released:

| Artifact | Type | Released |
|----------|------|:-------:|
| `openvox-operator` | Container image | Yes |
| `openvox-server` | Container image | Yes |
| `openvox-db` | Container image | Yes |
| `openvox-agent` | Container image | No (E2E only) |
| `openvox-code` | Container image | No (E2E only) |
| `openvox-mock` | Container image | No (E2E only) |
| `openvox-operator` | Helm chart | Yes |
| `openvox-stack` | Helm chart | Yes |
| `openvox-db-postgres` | Helm chart | Yes |

## Go Unit Tests

```bash
make test
```

Runs `go test ./...` with coverage. Tests include:

- Controller reconciliation (`internal/controller/`)
- Certificate signing and CA HTTP client
- Webhook validation (`internal/webhook/`)
- Duration parsing (`api/v1alpha1/duration.go`)
- Volume helpers, hash functions, image resolution
- Label generation
- Signing policy evaluation (`cmd/autosign/policy.go`)
- ENC HTTP client (`cmd/enc/classifier.go`)
- PuppetDB Wire Format v8 transformation (`cmd/report/puppetdb.go`)

### CRD Validation Tests

The `api/v1alpha1/` package contains envtest-based tests that validate CEL rules and enum constraints against a real API server. These run as part of `go test ./...` but require envtest binaries.

For local development:

```bash
go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest
source <(setup-envtest use -p env)
go test ./api/...
```

In CI, envtest is set up automatically.

## Helm Tests

### Linting

```bash
make helm-lint
```

Lints all three charts: `openvox-operator`, `openvox-stack`, `openvox-db-postgres`.

### Unit Tests

```bash
make helm-unittest
```

Runs [helm-unittest](https://github.com/helm-unittest/helm-unittest) for all charts. Test files are in `charts/*/tests/`.

## Container Image Lint

All Containerfiles are linted with [hadolint](https://github.com/hadolint/hadolint) as part of `_container-build.yaml`. This runs automatically in CI for every image build.

## E2E Tests

E2E tests use [Chainsaw](https://kyverno.github.io/chainsaw/) to deploy the operator and OpenVox stack scenarios against a real Kubernetes cluster and verify the full resource lifecycle.

### Container Images

E2E tests require 6 container images pulled from ghcr.io at runtime:

| Image | Purpose |
|-------|---------|
| `openvox-operator` | The operator itself |
| `openvox-server` | OpenVox Server (CA + compiler) |
| `openvox-db` | OpenVox DB (PuppetDB) |
| `openvox-code` | OCI image with Puppet environments |
| `openvox-agent` | Puppet agent (UBI9) |
| `openvox-mock` | Mock server for ENC, reports, OpenVox DB endpoints |

OCI image volumes require Kubernetes 1.35+ (`ImageVolume` feature gate).

### Test Groups

Tests are organized into groups. Each group installs the operator with specific settings, runs its tests, and cleans up.

| Group | Operator Settings | Tests | Make Target |
|-------|-------------------|-------|-------------|
| base | webhook=false, gatewayAPI=false | single-node, multi-server, agent-basic, agent-broken, agent-idempotent, agent-concurrent, agent-report, database-cnpg | `make e2e-group-base` |
| enc | webhook=false, gatewayAPI=false | agent-enc, agent-full | `make e2e-group-enc` |
| gateway | webhook=false, gatewayAPI=true | pool-gateway | `make e2e-group-gateway` |
| webhooks-byo | webhook=true, BYO TLS cert | webhook-validation-server, webhook-validation-config, webhook-validation-database, webhook-smoke | `make e2e-group-webhooks-byo` |
| webhooks-cm | webhook=true, cert-manager | webhook-validation-server, webhook-validation-config, webhook-validation-database, webhook-smoke | `make e2e-group-webhooks-cm` |

Run all groups sequentially:

```bash
make e2e-all
```

### External Dependencies

Some tests require external operators. Install them with `setup.sh`:

```bash
bash tests/e2e/setup.sh all              # Install all dependencies
bash tests/e2e/setup.sh install-cnpg      # CNPG operator only
bash tests/e2e/setup.sh install-envoy-gateway  # Envoy Gateway only
bash tests/e2e/setup.sh install-cert-manager   # cert-manager only
bash tests/e2e/setup.sh status            # Check status
```

Or via Make:

```bash
make e2e-setup
```

Versions can be overridden via environment variables: `CNPG_VERSION`, `ENVOY_GATEWAY_VERSION`, `CERT_MANAGER_VERSION`.

### Running Locally

```bash
# Use develop images (auto-built on every push to develop)
make e2e-group-base IMAGE_TAG=develop

# Run all groups
make e2e-all IMAGE_TAG=develop

# For feature branches: trigger E2E workflow first to build images
gh workflow run e2e.yaml --ref $(git branch --show-current)
make e2e-all IMAGE_TAG=$(git branch --show-current)
```

### Running in CI

The E2E workflow connects to a persistent K3S cluster via `E2E_KUBECONFIG` secret. Test groups run sequentially: base, enc, gateway, webhooks-byo, webhooks-cm.

Manual trigger with group selection:

```bash
gh workflow run e2e.yaml -f group=webhooks-byo
gh workflow run e2e.yaml -f group=all -f cleanup_images=false  # keep images
```

### Chainsaw Configuration

Global settings in `tests/e2e/chainsaw-config.yaml`:

- `skipDelete: true` -- namespaces are preserved on failure for debugging
- `parallel: 1` -- tests run sequentially (single shared cluster)
- Timeouts: apply 1m, assert 5m, cleanup 2m

### Test Scenarios

Tests are in `tests/e2e/<scenario>/chainsaw-test.yaml`.

#### Stack Deployment Tests

- **single-node** -- Minimal CA+Server stack, verifies all resources reach terminal phases
- **multi-server** -- Dedicated CA + 2 compiler replicas, verifies pod count and phases

#### Agent Tests

- **agent-basic** -- Single Puppet agent run with autosign and code deployment
- **agent-concurrent** -- Three agents running in parallel
- **agent-idempotent** -- Two consecutive runs verifying idempotency
- **agent-broken** -- Agent with broken environment, verifies expected failure
- **agent-enc** -- Agent with ENC via mock server, verifies classification request
- **agent-report** -- Agent with report forwarding to mock server
- **agent-full** -- Full integration: ENC + reports + OpenVox DB via mock

#### Database Tests

- **database-cnpg** -- CNPG PostgreSQL cluster + OpenVox DB deployment, verifies connectivity

#### Gateway Tests

- **pool-gateway** -- Gateway API TLSRoute with Envoy Gateway, verifies SNI routing

#### Webhook Validation Tests

- **webhook-validation-server** -- Server ref validation (configRef, certificateRef, poolRef)
- **webhook-validation-config** -- Config ref validation (authorityRef, databaseRef/serverUrls mutual exclusion)
- **webhook-validation-database** -- Database field validation (certificateRef, host, credentialsSecretRef, sslMode)
- **webhook-smoke** -- ValidatingWebhookConfiguration exists, Service has endpoints, smoke create/reject

### Writing New Tests

Create a new directory under `tests/e2e/` with a `chainsaw-test.yaml`:

```
tests/e2e/
+-- chainsaw-config.yaml
+-- my-scenario/
    +-- chainsaw-test.yaml
```

Key patterns:

- Use `spec.namespace` to set an isolated namespace
- Use `script` operations for Helm install/uninstall
- Use `assert` operations with `resource` to wait for CRD status fields
- Place cleanup in the `finally` block of the last step
- Reference chart paths via `$(git rev-parse --show-toplevel)` for portability

Add the test to the appropriate group in the `Makefile` (`e2e-group-*` target).

See the [Chainsaw documentation](https://kyverno.github.io/chainsaw/) for the full API reference.
