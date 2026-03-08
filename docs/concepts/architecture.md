# Architecture

## Overview

The openvox-operator follows the standard Kubernetes operator pattern: a controller watches Custom Resources and reconciles the desired state by creating and managing Kubernetes-native workloads (Deployments, Services, ConfigMaps, Secrets, Jobs).

## CRD Relationships

The operator uses multiple CRDs that form a hierarchy:

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

- An **Environment** is the root resource. It generates ConfigMaps for puppet.conf/puppetdb.conf/webserver.conf and holds shared configuration.
- A **CertificateAuthority** references an Environment and manages the CA infrastructure: PVC, setup Job, and CA Secret.
- A **SigningPolicy** references a CertificateAuthority and defines declarative CSR signing rules (any, pattern match, or CSR attribute match). The Environment controller renders all SigningPolicies into an autosign policy file.
- A **Certificate** references a CertificateAuthority and manages the lifecycle of a single certificate: signing Job and TLS Secret.
- A **Server** references an Environment and a Certificate. It creates a Deployment (with Recreate strategy for CA, RollingUpdate for servers). The Server waits for the Certificate to reach the `Signed` phase before creating its Deployment.
- A **Pool** references an Environment and creates a Kubernetes Service. Server pods are selected by label.

## CA Lifecycle

The Certificate Authority is managed by the CertificateAuthority controller:

1. The CertificateAuthority controller creates a **PVC** for CA data and a **Job** that runs `puppetserver ca setup`
2. The Job stores CA keys on the PVC and creates three Kubernetes **Secrets**:
   - `{name}-ca` — public CA certificate (`ca_crt.pem`)
   - `{name}-ca-key` — CA private key (`ca_key.pem`, never mounted in pods)
   - `{name}-ca-crl` — CRL data (`ca_crl.pem`, `infra_crl.pem`)
3. The CertificateAuthority transitions to the `Ready` phase
4. The controller periodically fetches the CRL from the CA HTTP API and updates the CRL Secret (configurable via `crlRefreshInterval`, default `5m`)

## Certificate Lifecycle

Certificates are managed by the Certificate controller:

1. The Certificate controller waits for the referenced CertificateAuthority to be `Ready`
2. It determines the signing strategy:
   - **CA setup export**: The first Certificate (created with the CA) gets its cert+key exported directly by the CA setup Job
   - **HTTP signing**: Additional Certificates are signed by the operator in-process — it generates an RSA key pair, submits a CSR to the Puppet CA HTTP API, and polls for the signed certificate
3. The controller creates a TLS **Secret** with cert.pem and key.pem
4. The Certificate transitions to the `Signed` phase

```mermaid
sequenceDiagram
    participant Op as Operator
    participant CAJob as CA Setup Job
    participant PVC as CA PVC
    participant CASec as CA Secrets (3x)
    participant CA as CA Server
    participant TLS as TLS Secret
    participant Srv as Server Deployment

    Op->>CAJob: Create CA setup Job
    CAJob->>PVC: Write CA data
    CAJob->>CASec: Create ca, ca-key, ca-crl Secrets
    CAJob->>TLS: Export initial TLS Secret (cert + key)
    Op->>Srv: Create CA Deployment
    Op->>CA: Submit CSR via HTTP API
    CA-->>Op: Return signed certificate
    Op->>TLS: Create TLS Secret (cert + key)
    Op->>Srv: Create Server Deployment (mounts TLS + CA Secrets)
    loop crlRefreshInterval (default 5m)
        Op->>CA: Fetch CRL via HTTP API
        Op->>CASec: Update ca-crl Secret
    end
```

## Dedicated ServiceAccounts

The operator creates dedicated ServiceAccounts with minimal privileges:

| ServiceAccount | Created by | Purpose | K8s API Token |
|---|---|---|---|
| `{env}-server` | Environment controller | All server pods | No (`automountServiceAccountToken: false`) |
| `{ca}-ca-setup` | CertificateAuthority controller | CA setup job: creates CA Secrets | Yes (scoped to `{ca}-ca`, `{ca}-ca-key`, `{ca}-ca-crl` Secrets) |

The operator itself runs with its own ServiceAccount (managed by the Helm chart) with cluster-wide RBAC.

## Scaling

- **CA Server**: Always a single replica with Recreate deployment strategy (only one pod writes to the CA PVC)
- **Servers**: Horizontally scalable via `replicas` or HPA. All replicas of a Server share the same certificate from a Secret.
- **Multi-Version**: Multiple Server CRDs with different image tags can join the same Pool for canary deployments

## Code Deployment

The CodeDeploy CRD (planned) manages r10k in a separate image. It creates a PVC for code storage that Servers mount read-only.

| Setup | Access Mode | Requirement |
|---|---|---|
| Single-Node (default) | RWO | Any storage provider |
| Multi-Node | RWX | NFS, CephFS, EFS, Longhorn, etc. |

For single-node setups, RWO with pod affinity is sufficient. Multi-node clusters require an RWX-capable storage provider.
