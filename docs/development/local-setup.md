# Local Setup

## Prerequisites

- Go (version from `go.mod`)
- Docker Desktop with Kubernetes enabled (or any local cluster)
- Helm 3.x
- kubectl

## Clone and Build

```bash
git clone https://github.com/slauger/openvox-operator.git
cd openvox-operator
go build ./...
```

## Tool Dependencies

Go tool dependencies are managed via the `tool` directive in `go.mod`. This includes `controller-gen`, `govulncheck`, and `chainsaw`. No manual installation is needed — Go resolves them automatically:

```bash
go tool controller-gen --version
go tool govulncheck -version
go tool chainsaw version
```

`golangci-lint` is the only tool managed externally. In CI it is provided by [golangci-lint-action](https://github.com/golangci/golangci-lint-action). For local usage, install it via [the official instructions](https://golangci-lint.run/welcome/install/).

## Deploy to Local Cluster

Build all container images and deploy the operator to your local cluster:

```bash
make local-deploy
```

Deploy an OpenVox stack (single-node by default):

```bash
make local-stack
```

Use a different scenario:

```bash
make local-stack STACK_VALUES=charts/openvox-stack/ci/multi-server-values.yaml
```

Override the image tag:

```bash
make local-deploy LOCAL_TAG=my-feature
make local-stack LOCAL_TAG=my-feature
```

## Teardown

Remove everything (stack, operator, CRDs):

```bash
make uninstall
```

## Code Generation

After modifying CRD types in `api/v1alpha1/`, regenerate manifests and deepcopy methods:

```bash
make manifests generate
```

Check for drift (used in CI):

```bash
make check-manifests
```

## Makefile Targets

| Target | Description |
|---|---|
| `build` | Build operator binary |
| `manifests` | Generate CRD manifests |
| `generate` | Generate deepcopy methods |
| `local-build` | Build all container images with the current git commit as tag |
| `local-deploy` | Build images and deploy the operator via Helm |
| `local-install` | Deploy operator via Helm with local images (no build) |
| `local-stack` | Deploy openvox-stack via Helm with local images |
| `install` | Install operator via Helm with default images |
| `stack` | Deploy openvox-stack via Helm with default images |
| `uninstall` | Remove stack, operator, and CRDs from the cluster |
| `unstack` | Remove only the openvox-stack |
| `ci` | Run all CI checks locally |
