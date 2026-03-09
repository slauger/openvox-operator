# Code Deployment

Puppet code (modules, Hiera data, environments) must be available on Server pods at `/etc/puppetlabs/code/environments`. The operator supports two methods to provide code: **OCI image volumes** and **PVCs**.

Code is only mounted on pods with `server: true`. CA-only pods are not affected by code changes.

## OCI Image Volumes (recommended)

Package Puppet code as an OCI image and reference it in the Environment. The operator mounts it as a read-only [image volume](https://kubernetes.io/docs/concepts/storage/volumes/#image) directly into the pod.

**Requirements:** Kubernetes 1.31+ with the `ImageVolume` feature gate enabled.

### Building a Code Image

Create a `Containerfile` that copies your Puppet environments into the image:

```dockerfile
FROM scratch
COPY environments/ /etc/puppetlabs/code/environments/
```

Build and push:

```bash
docker build -t ghcr.io/example/puppet-code:v1.0.0 -f Containerfile .
docker push ghcr.io/example/puppet-code:v1.0.0
```

A typical control repository layout:

```
control-repo/
  environments/
    production/
      manifests/
      modules/
      hiera.yaml
      data/
```

### Configuring the Environment

Set `code.image` on the Environment to apply the code to all Servers:

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Environment
metadata:
  name: production
spec:
  image:
    repository: ghcr.io/slauger/openvox-server
    tag: "8.12.1"
  code:
    image: ghcr.io/example/puppet-code:v1.0.0
```

### Rolling Out Code Changes

Update the image reference to deploy new code. The operator detects the change and triggers a rolling restart of all Server pods:

```yaml
spec:
  code:
    image: ghcr.io/example/puppet-code:v1.1.0
```

### Private Registries

For private registries, create a pull secret and reference it:

```yaml
spec:
  code:
    image: registry.example.com/puppet-code:v1.0.0
    imagePullSecret: registry-credentials
```

### Per-Server Override

A Server can override the Environment's code source. This is useful for testing new code on a canary server:

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Server
metadata:
  name: canary
spec:
  environmentRef: production
  certificateRef: canary-cert
  code:
    image: ghcr.io/example/puppet-code:v2.0.0-rc1
```

## PVC

Reference an existing PVC containing Puppet code. The PVC must be pre-populated externally (e.g. by a CI/CD pipeline or a CronJob running r10k).

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Environment
metadata:
  name: production
spec:
  image:
    repository: ghcr.io/slauger/openvox-server
    tag: "8.12.1"
  code:
    claimName: puppet-code
```

The PVC must contain the environments directory at `/etc/puppetlabs/code/environments`.

| Setup | Access Mode | Requirement |
|---|---|---|
| Single-node | RWO | Any storage provider |
| Multi-node | RWX | NFS, CephFS, EFS, Longhorn, etc. |

## Comparison

| | OCI Image Volume | PVC |
|---|---|---|
| **Immutability** | Code is immutable per image tag | Mutable, can change at any time |
| **Rollout** | Automatic rolling restart on image change | Manual restart or `environmentTimeout` |
| **Versioning** | Container registry tags | External (Git, CI/CD) |
| **Multi-node** | No RWX needed | Requires RWX for multi-node |
| **Kubernetes version** | 1.31+ | Any |
| **Use case** | Production, GitOps workflows | Legacy setups, external tooling |
