# Testing

## Unit Tests

Run all unit tests:

```bash
make test
```

This runs `go test ./...` with coverage output. Tests include:

- Duration parsing (`api/v1alpha1/duration.go`)
- Volume helpers, hash functions, image resolution (`internal/controller/helpers.go`)
- Label generation (`internal/controller/labels.go`)
- Signing policy evaluation (`cmd/autosign/policy.go`)
- ENC HTTP client and response handling (`cmd/enc/classifier.go`)
- PuppetDB Wire Format v8 transformation (`cmd/report/puppetdb.go`)

### CRD Validation Tests

The `api/v1alpha1/` package contains envtest-based tests that validate CEL rules and enum constraints on the CRDs against a real API server. These run as part of `go test ./...` but require envtest binaries.

For local development, install envtest:

```bash
go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest
source <(setup-envtest use -p env)
go test ./api/...
```

In CI, envtest is set up automatically.

## E2E Tests

E2E tests use [Chainsaw](https://kyverno.github.io/chainsaw/) to deploy the operator and OpenVox stack scenarios against a real Kubernetes cluster and verify the full resource lifecycle.

### Prerequisites

- A running Kubernetes cluster (Docker Desktop, kind, k3s, etc.)
- Container images buildable locally (Docker or Podman)

### Running

```bash
make e2e
```

This will:

1. Build all container images (`openvox-operator`, `openvox-server`, `openvox-code`, `openvox-agent`, `openvox-mock`)
2. Deploy the operator via Helm
3. Run all Chainsaw test scenarios

### Test Scenarios

Tests are located in `tests/e2e/` with a shared configuration in `tests/e2e/chainsaw-config.yaml`.

#### Stack Deployment Tests

These tests verify that the operator deploys OpenVox stacks correctly.

##### Single-Node (`tests/e2e/single-node/`)

Deploys a minimal stack with a single CA+Server and verifies:

- CertificateAuthority reaches `Ready` phase
- Config reaches `Running` phase
- Server reaches `Running` phase with 1/1 replicas ready
- Operator logs contain no error-level entries

##### Multi-Server (`tests/e2e/multi-server/`)

Deploys a stack with a dedicated CA server and 2 compiler replicas:

- CertificateAuthority reaches `Ready` phase
- CA Server reaches `Running` phase (1 replica)
- Compiler Server reaches `Running` phase (2 replicas)
- 3 total server pods exist (1 CA + 2 compilers)
- Operator logs contain no error-level entries

#### Agent Tests

These tests run real Puppet agents against deployed stacks to verify the full Puppet lifecycle: certificate signing, catalog compilation, catalog application, and report submission.

They require additional test images:

- **openvox-agent** — Puppet agent based on UBI9
- **openvox-code** — OCI image with Puppet environments (production, staging, broken)
- **openvox-mock** — Mock server for ENC, report webhook, and PuppetDB endpoints

##### Agent Basic (`tests/e2e/agent-basic/`)

Single Puppet agent run against a server with autosign and code deployment:

- Stack deploys with CA, Server, and code image
- Puppet agent connects, gets certificate, compiles catalog, applies it
- Agent Job completes successfully

##### Agent Concurrent (`tests/e2e/agent-concurrent/`)

Three Puppet agents running in parallel against the same server:

- All three agents get certificates and apply catalogs concurrently
- All three Jobs complete successfully

##### Agent Idempotent (`tests/e2e/agent-idempotent/`)

Two consecutive Puppet agent runs verifying idempotency:

- First run applies changes (exit code 2 → success)
- Second run completes successfully

##### Agent Broken (`tests/e2e/agent-broken/`)

Puppet agent run with a broken environment (`include nonexistent_class`):

- Agent connects to server with `--environment broken`
- Catalog compilation fails
- Agent Job fails as expected

##### Agent ENC (`tests/e2e/agent-enc/`)

Puppet agent with External Node Classification via mock server:

- Mock server deployed with `ENC_CLASSES=e2e_test`
- NodeClassifier configured to query mock at `/node/{certname}`
- Agent completes successfully
- Mock received classification request for the agent's certname

##### Agent Report (`tests/e2e/agent-report/`)

Puppet agent with report forwarding to mock server:

- ReportProcessors configured for generic webhook and PuppetDB
- Agent completes successfully
- Mock received report via webhook endpoint
- Mock received PuppetDB command

##### Agent Full (`tests/e2e/agent-full/`)

Full integration test combining ENC, reports, and PuppetDB:

- NodeClassifier + ReportProcessors all pointing to mock server
- Agent completes successfully
- Mock received classification, report, and PuppetDB command

### Cleanup

Each test scenario cleans up after itself (Helm uninstall + namespace deletion) via Chainsaw's `finally` block, even on failure. Scenarios use isolated namespaces so they can run in parallel.

### CI Workflow

Agent E2E tests are available via the **E2E** workflow (`.github/workflows/e2e.yaml`), triggered manually via `workflow_dispatch`. It accepts an optional `image_tag` input:

- **Empty** — builds operator and server from source
- **Set** (e.g. `latest`, `0a6a7f0`) — pulls pre-built images from `ghcr.io/slauger/openvox-operator` and `ghcr.io/slauger/openvox-server`

Test images (code, agent, mock) are always built from source. Their container image builds are managed separately in `.github/workflows/container-build-test.yaml` (push to main with path filter + manual trigger).

### Writing New Tests

Create a new directory under `tests/e2e/` with a `chainsaw-test.yaml`:

```
tests/e2e/
├── chainsaw-config.yaml
├── my-scenario/
│   └── chainsaw-test.yaml
```

Each test file follows the Chainsaw v1alpha1 Test spec. Key patterns:

- Use `spec.namespace` to set an isolated namespace
- Use `script` operations for Helm install/uninstall
- Use `assert` operations with `resource` to wait for CRD status fields
- Place cleanup in the `finally` block of the last step
- Reference chart paths via `$(git rev-parse --show-toplevel)` for portability

Refer to the [Chainsaw documentation](https://kyverno.github.io/chainsaw/) for the full API reference.

## CI

All CI checks can be run locally:

```bash
make ci
```

This runs lint, vet, test, manifest drift check, vulncheck, and Helm lint. E2E tests are not part of `make ci` since they require a running cluster.
