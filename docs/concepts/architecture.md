# Architecture

## Overview

The openvox-operator follows the standard Kubernetes operator pattern: a controller watches Custom Resources and reconciles the desired state by creating and managing Kubernetes-native workloads (Deployments, Services, ConfigMaps, Secrets, Jobs).

## CRD Relationships

The operator uses multiple CRDs that form a hierarchy:

```mermaid
graph TD
    Cfg["Config"]
    CA["CertificateAuthority"]
    SP["SigningPolicy"]
    Cert["Certificate"]
    Srv["Server"]
    Pool["Pool"]
    Cfg -->|authorityRef| CA
    CA -->|certificateAuthorityRef| SP
    CA -->|authorityRef| Cert
    Cert -->|certificateRef| Srv
    Cfg -->|configRef| Srv
    Srv -->|selector| Pool
    Cfg -->|configRef| Pool
```

- A **Config** is the root resource. It generates ConfigMaps for puppet.conf/puppetdb.conf/webserver.conf and holds shared configuration.
- A **CertificateAuthority** is a standalone resource managing the CA infrastructure: PVC, setup Job, and CA Secret. A Config references it via `authorityRef`.
- A **SigningPolicy** references a CertificateAuthority and defines declarative CSR signing rules (any, pattern match, or CSR attribute match). The Config controller renders all SigningPolicies into an autosign policy file.
- A **Certificate** references a CertificateAuthority and manages the lifecycle of a single certificate: signing Job and TLS Secret.
- A **Server** references a Config and a Certificate. It creates a Deployment (with Recreate strategy for CA, RollingUpdate for servers). The Server waits for the Certificate to reach the `Signed` phase before creating its Deployment.
- A **Pool** references a Config and creates a Kubernetes Service. Server pods are selected by label.

## Why Separate CRDs for CA and Certificates?

Traditional Puppet/OpenVox Server bundles CA management, certificate signing, and server runtime into a single process. This works on VMs where `puppetserver ca` (a CRuby CLI) manages everything locally. This operator deliberately ships **no system Ruby** - only JRuby embedded in the server JAR - to keep the image small and reduce the update surface. CA operations are handled through a custom JRuby wrapper that calls `clojure.main` instead.

By separating the CA lifecycle (`CertificateAuthority`) from certificate signing (`Certificate`) and from the server runtime (`Server`), each concern becomes independently manageable. Certificates can be issued before a server is running, revoked without restarting pods, and the CA can be initialized once while multiple servers share the same signed certificate for horizontal scaling.

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
| `{cfg}-server` | Config controller | All server pods | No (`automountServiceAccountToken: false`) |
| `{ca}-ca-setup` | CertificateAuthority controller | CA setup job: creates CA Secrets | Yes (scoped to `{ca}-ca`, `{ca}-ca-key`, `{ca}-ca-crl` Secrets) |

The operator itself runs with its own ServiceAccount (managed by the Helm chart) with cluster-wide RBAC.

## Scaling

- **CA Server**: Always a single replica with Recreate deployment strategy (only one pod writes to the CA PVC)
- **Servers**: Horizontally scalable via `replicas` or HPA. All replicas of a Server share the same certificate from a Secret.
- **Multi-Version**: Multiple Server CRDs with different image tags can join the same Pool for canary deployments

## Code Deployment

Puppet code is deployed to Server pods via the `CodeSpec` on the Config or Server CRD. Two modes are supported:

### OCI Image Volume (recommended)

Package Puppet code as an OCI image and reference it in the Config's `code.image` field. The operator mounts it as a read-only image volume (requires Kubernetes 1.31+). Code changes are rolled out by updating the image reference.

### PVC

Reference an existing PVC via `code.claimName`. The PVC must contain the Puppet environments directory. Suitable for setups where code is deployed externally (e.g. CI/CD pipeline writing to a shared volume).

See [Code Deployment](code-deployment.md) for the full guide.

## Why a New Approach?

Traditional Puppet/OpenVox Server installations on VMs use OS packages that install both a system Ruby (CRuby) and the server JAR with its embedded JRuby. Existing container images carry this VM-centric approach into containers, leading to several problems in a Kubernetes context.

### ezbake Legacy

Upstream OpenVox Server uses ezbake for packaging. It generates init scripts that start as root and switch to the puppet user via `runuser`/`su`/`sudo`. This breaks rootless containers and OpenShift random UIDs.

### Duplicate Ruby Installation

The server needs JRuby (embedded in the JAR) for runtime. Existing containers additionally install system Ruby + the openvox gem just so entrypoint scripts can call `puppet config set/print`. This is unnecessary when configuration comes via ConfigMaps.

### Docker Logic in Kubernetes

Existing images use ~15 entrypoint scripts that translate environment variables into config files. This is a Docker-Compose pattern. In Kubernetes, the operator renders configuration into ConfigMaps and Secrets directly.

### No Role Separation

Existing containers decide at startup whether to run as CA or server based on environment variables. In Kubernetes, the operator handles orchestration and role assignment through the CRD model.

## How openvox-operator Differs

| | VM-based / Docker | openvox-operator |
|---|---|---|
| **Ruby** | System Ruby (CRuby) alongside JRuby | No system Ruby - only JRuby in the server JAR |
| **Configuration** | `puppet config set`, entrypoint scripts, ENV vars | Declarative CRDs, operator renders ConfigMaps |
| **Privileges** | Requires root | Fully rootless, random UID compatible |
| **CA Management** | `puppetserver ca` CLI (CRuby) | Custom JRuby wrapper via `clojure.main` |
| **Certificates** | Each server has its own certificate | `Certificate` CRD manages the cert lifecycle - all replicas of a Server share one certificate |
| **CSR Signing** | `autosign.conf` or Ruby scripts | `SigningPolicy` CRD with declarative rules (any, pattern, CSR attributes, DNS SAN validation) |
| **CRL** | File on disk, manual refresh | Split Secret (`{ca}-ca-crl`), operator-driven periodic refresh via CA HTTP API |
| **Scaling** | Manual VM provisioning | Deployment replicas + HPA |
| **Code Deployment** | r10k on the VM, cron/webhook | OCI image volumes or PVC - code packaged as immutable container images |
| **Traffic Routing** | DNS round-robin or hardware load balancer per environment | Gateway API TLSRoute with SNI-based routing - share a single LoadBalancer across environments |
| **Multi-Version** | Separate VMs or package pinning | Multiple Servers in the same Pool |

## Container Image

The operator uses a minimal container image:

**Included:**

- UBI9 + JDK 17
- Tarball installation (puppet-server-release.jar, CLI tools, vendored JRuby gems)
- PuppetDB termini
- OpenShift random-UID pattern (chgrp 0, chmod g=u)
- Direct `java` entrypoint (no wrapper scripts)

**Removed (compared to upstream images):**

- All entrypoint.d scripts
- System Ruby and openvox gem
- Gemfile / bundle install / ruby-devel / gcc / make
- ENV var to config translation logic
- Docker-Compose support
