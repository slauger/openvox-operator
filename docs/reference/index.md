# CRD Reference

All resources use the API group `openvox.voxpupuli.org/v1alpha1`.

## Resource Hierarchy

```mermaid
graph TD
    Cfg["Config"]
    CA["CertificateAuthority"]
    SP["SigningPolicy"]
    NC["NodeClassifier"]
    RP["ReportProcessor"]
    Cert["Certificate"]
    Srv["Server"]
    DB["Database"]
    Pool["Pool"]

    Cfg -->|authorityRef| CA
    Cfg -.->|nodeClassifierRef| NC
    Cfg -.->|databaseRef| DB
    SP -.->|certificateAuthorityRef| CA
    Cert -->|authorityRef| CA
    Srv -->|certificateRef| Cert
    DB -->|certificateRef| Cert
    Srv -->|configRef| Cfg
    RP -.->|configRef| Cfg
    Srv -->|poolRefs| Pool
```

Each resource references its parent. The operator reconciles them in order: a Config references a CertificateAuthority via `authorityRef`, optionally a NodeClassifier via `nodeClassifierRef`, and optionally a Database via `databaseRef` (which auto-wires the PuppetDB connection URL). A CertificateAuthority must be `Ready` before a Certificate can be signed, and a Certificate must be `Signed` before a Server or Database creates its Deployment. SigningPolicies, NodeClassifiers, and ReportProcessors can be created at any time and take effect within ~60 seconds.

## Resources

| Kind | Short Name | Purpose |
|---|---|---|
| [Config](config.md) | `cfg` | Shared config (puppet.conf, auth.conf), OpenVox DB connection |
| [CertificateAuthority](certificateauthority.md) | `ca` | CA infrastructure: PVC, keys, 3 CA Secrets (cert, key, CRL) |
| [SigningPolicy](signingpolicy.md) | `sp` | Declarative CSR signing policy for a CA |
| [NodeClassifier](nodeclassifier.md) | `nc` | External Node Classifier (ENC) endpoint |
| [Certificate](certificate.md) | `cert` | Lifecycle of a single certificate (request, sign) |
| [Server](server.md) | - | OpenVox Server Deployment (CA and/or server role) |
| [Database](database.md) | `db` | OpenVox DB Deployment with external PostgreSQL |
| [Pool](pool.md) | - | Networking resource: Service for Servers that reference this Pool |
| [ReportProcessor](reportprocessor.md) | `rp` | Webhook-based report forwarding endpoint |

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

Used by [Config](config.md) and [Server](server.md) to define the Puppet code source. Either `claimName` or `image` may be set, not both.

| Field | Type | Default | Description |
|---|---|---|---|
| `claimName` | string | - | Name of an existing PVC containing Puppet code |
| `image` | string | - | OCI image reference containing Puppet code (Kubernetes 1.35+, or 1.31+ with feature gate) |
| `imagePullPolicy` | string | `IfNotPresent` | When to pull the code image |
| `imagePullSecret` | string | - | Secret name for pulling from private registries |
