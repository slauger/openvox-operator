# Contributing to openvox-operator

Thanks for your interest in contributing! This document covers the basics for getting started.

## Getting Started

### Prerequisites

- Go 1.26+
- Docker or Podman
- Helm 3
- A Kubernetes cluster (Docker Desktop, kind, or minikube)
- [golangci-lint](https://golangci-lint.run/)

### Clone and Build

```bash
git clone https://github.com/slauger/openvox-operator.git
cd openvox-operator
make build
```

### Run Tests

```bash
make test          # Unit tests
make lint          # Linting
make helm-lint     # Helm chart linting
make ci            # All CI checks
```

### Local Development

Build all container images and deploy to your local cluster:

```bash
make local-deploy
```

Deploy a stack for testing:

```bash
make local-stack
```

### E2E Tests

E2E tests use [Chainsaw](https://kyverno.github.io/chainsaw/) and require a running cluster:

```bash
make e2e
```

## Development Workflow

1. Fork the repository
2. Create a feature branch (`feature/my-feature` or `fix/my-fix`)
3. Make your changes
4. Run `make ci` to verify everything passes
5. Run `make check-manifests` to ensure generated files are up to date
6. Commit with a descriptive message following [Conventional Commits](https://www.conventionalcommits.org/)
7. Open a pull request against `main`

### Commit Messages

We follow Conventional Commits:

```
feat: add new feature
fix: resolve bug in controller
docs: update README
chore: update dependencies
refactor: simplify reconcile loop
test: add unit tests for helpers
```

### Code Generation

After modifying CRD types in `api/v1alpha1/`:

```bash
make manifests generate
```

This regenerates CRD manifests and deepcopy methods. Always commit the generated files.

## Project Structure

```
api/v1alpha1/          # CRD type definitions
cmd/                   # Entrypoints (operator, ENC, autosign, report, mock)
internal/controller/   # Reconcilers
charts/                # Helm charts (openvox-operator, openvox-stack)
config/crd/bases/      # Generated CRD manifests
tests/e2e/             # Chainsaw E2E tests
images/                # Containerfiles
docs/                  # Documentation
```

## Reporting Issues

- Use [GitHub Issues](https://github.com/slauger/openvox-operator/issues)
- For security vulnerabilities, see [SECURITY.md](SECURITY.md)

## License

By contributing, you agree that your contributions will be licensed under the [Apache License 2.0](LICENSE).
