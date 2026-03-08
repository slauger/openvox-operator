# CRD Reference

All resources use the API group `openvox.voxpupuli.org/v1alpha1`.

## Resource Hierarchy

```
Environment
  └─ CertificateAuthority (environmentRef → Environment)
       └─ Certificate (authorityRef → CertificateAuthority)
            └─ Server (certificateRef → Certificate, environmentRef → Environment)
                 └─ Pool (selector → Server Pods, environmentRef → Environment)
```

Each resource references its parent. The operator reconciles them in order: an Environment must exist before a CertificateAuthority can reference it, a CertificateAuthority must be `Ready` before a Certificate can be signed, and a Certificate must be `Signed` before a Server creates its Deployment.

## Resources

| Kind | Short Name | Purpose |
|---|---|---|
| [Environment](environment.md) | `env` | Shared config (puppet.conf, auth.conf), PuppetDB connection, default image |
| [CertificateAuthority](certificateauthority.md) | `ca` | CA infrastructure: PVC, keys, public CA Secret |
| [Certificate](certificate.md) | `cert` | Lifecycle of a single certificate (request, sign) |
| [Server](server.md) | - | OpenVox Server Deployment (CA and/or server role) |
| [Pool](pool.md) | - | Kubernetes Service that selects Server Pods |

## Shared Types

These types are reused across multiple CRDs.

### ImageSpec

| Field | Type | Default | Description |
|---|---|---|---|
| `repository` | string | `ghcr.io/slauger/openvox-server` | Container image repository |
| `tag` | string | `latest` | Container image tag |
| `pullPolicy` | string | `IfNotPresent` | Image pull policy |
| `pullSecrets` | []LocalObjectReference | - | Image pull secrets |

### StorageSpec

| Field | Type | Default | Description |
|---|---|---|---|
| `size` | string | `1Gi` | Requested storage size |
| `storageClass` | string | - | Storage class name (empty = default) |

### CodeSpec

| Field | Type | Default | Description |
|---|---|---|---|
| `claimName` | string | **required** | Name of an existing PVC containing Puppet code |
