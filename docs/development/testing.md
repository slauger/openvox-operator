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

1. Build local container images (`openvox-operator` and `openvox-server`)
2. Deploy the operator via Helm
3. Run all Chainsaw test scenarios

### Test Scenarios

Tests are located in `tests/e2e/` with a shared configuration in `tests/e2e/chainsaw-config.yaml`.

#### Single-Node (`tests/e2e/single-node/`)

Deploys a minimal stack with a single CA+Server and verifies:

- CertificateAuthority reaches `Ready` phase
- Config reaches `Running` phase
- Server reaches `Running` phase with 1/1 replicas ready
- Operator logs contain no error-level entries

#### Multi-Server (`tests/e2e/multi-server/`)

Deploys a stack with a dedicated CA server and 2 compiler replicas:

- CertificateAuthority reaches `Ready` phase
- CA Server reaches `Running` phase (1 replica)
- Compiler Server reaches `Running` phase (2 replicas)
- 3 total server pods exist (1 CA + 2 compilers)
- Operator logs contain no error-level entries

### Cleanup

Each test scenario cleans up after itself (Helm uninstall + namespace deletion) via Chainsaw's `finally` block, even on failure. Both scenarios use isolated namespaces (`e2e-single-node`, `e2e-multi-server`) so they can run in parallel.

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
