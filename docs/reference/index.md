# CRD Reference

All resources use the API group `openvox.voxpupuli.org/v1alpha1`.

## Resource Hierarchy

```mermaid
graph TD
    Env["Environment"]
    CA["CertificateAuthority"]
    SP["SigningPolicy"]
    Cert["Certificate"]
    Srv["Server"]
    Pool["Pool"]

    Env -->|environmentRef| CA
    CA -->|certificateAuthorityRef| SP
    CA -->|authorityRef| Cert
    Cert -->|certificateRef| Srv
    Env -->|environmentRef| Srv
    Srv -->|selector| Pool
    Env -->|environmentRef| Pool
```

Each resource references its parent. The operator reconciles them in order: an Environment must exist before a CertificateAuthority can reference it, a CertificateAuthority must be `Ready` before a Certificate can be signed, and a Certificate must be `Signed` before a Server creates its Deployment. SigningPolicies can be created at any time and take effect within ~60 seconds.

## Resources

| Kind | Short Name | Purpose |
|---|---|---|
| [Environment](environment.md) | `env` | Shared config (puppet.conf, auth.conf), PuppetDB connection |
| [CertificateAuthority](certificateauthority.md) | `ca` | CA infrastructure: PVC, keys, 3 CA Secrets (cert, key, CRL) |
| [SigningPolicy](signingpolicy.md) | `sp` | Declarative CSR signing policy for a CA |
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

Used by [Server](server.md) to mount a PVC containing Puppet code.

| Field | Type | Default | Description |
|---|---|---|---|
| `claimName` | string | **required** | Name of an existing PVC containing Puppet code |
