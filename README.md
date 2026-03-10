# 🦊 openvox-operator

[![CI](https://github.com/slauger/openvox-operator/actions/workflows/ci.yaml/badge.svg)](https://github.com/slauger/openvox-operator/actions/workflows/ci.yaml)
[![Go](https://github.com/slauger/openvox-operator/actions/workflows/go.yaml/badge.svg)](https://github.com/slauger/openvox-operator/actions/workflows/go.yaml)
[![Go Report Card](https://goreportcard.com/badge/github.com/slauger/openvox-operator)](https://goreportcard.com/report/github.com/slauger/openvox-operator)
[![Go Reference](https://pkg.go.dev/badge/github.com/slauger/openvox-operator.svg)](https://pkg.go.dev/github.com/slauger/openvox-operator)
[![License](https://img.shields.io/github/license/slauger/openvox-operator)](LICENSE)

A Kubernetes Operator that maps [OpenVox Server](https://github.com/OpenVoxProject) infrastructure onto native building blocks - CRDs, Secrets, OCI image volumes, and Gateway API - for running Puppet on **Kubernetes** and **OpenShift**.

- 🔐 **Automated CA Lifecycle** - CA initialization, certificate signing, distribution, and periodic CRL refresh - fully managed
- 📜 **Declarative Signing Policies** - CSR approval via patterns, DNS SANs, CSR attributes, or open signing - no autosign scripts
- 🏷️ **External Node Classification** - Declarative ENC support for Foreman, Puppet Enterprise, or custom HTTP classifiers
- 📦 **One Image, Two Roles** - Same rootless image runs as CA or server, configured by the operator
- ⚡ **Scalable Servers** - Scale catalog compilation horizontally - multiple server pools with HPA
- 🔄 **Multi-Version Deployments** - Run different server versions side by side - canary deployments, rolling upgrades
- 🔒 **Rootless & OpenShift Ready** - Random UID compatible, no root, no ezbake, no privilege escalation
- 🪶 **Minimal Image** - UBI9-based, no system Ruby, no ezbake packaging - smaller footprint, fewer updates
- 🧠 **Auto-tuned JVM** - Heap size calculated from memory limits (90%) - no manual `-Xmx` tuning needed
- 📦 **OCI Image Volumes** - Package Puppet code as OCI images, deploy immutably with automatic rollout (K8s 1.31+)
- 🌐 **Gateway API** - SNI-based TLSRoute support - share a single LoadBalancer across environments via TLS passthrough
- 🔃 **Automatic Config Rollout** - Config and certificate changes trigger rolling restarts automatically
- ☸️ **Kubernetes-Native** - All config via ConfigMaps/Secrets - no entrypoint scripts, no ENV translation

> **⚠️ Status: Early Development** - This project is experimental and under active development. CRDs, APIs, and behavior may change at any time. Not ready for production use. Feedback is welcome - especially on the CRD data model, which is still evolving. Feel free to open an [issue](https://github.com/slauger/openvox-operator/issues).

## Architecture

```mermaid
graph TD
    Op["🦊 OpenVox Operator"]
    Op -->|manages| Cfg

    Cfg["📋 Config<br/>production"]
    Cfg -->|authorityRef| CA_CRD["🔐 CertificateAuthority<br/>production-ca"]
    CA_CRD --> Cert_CA["📜 Certificate: ca-cert"]
    CA_CRD --> Cert_Stable["📜 Certificate: stable-cert"]
    CA_CRD --> Cert_Canary["📜 Certificate: canary-cert"]

    Cert_CA --> CA["⚙️ Server: ca<br/>ca: true, server: true"]
    Cert_Stable --> Stable["⚙️ Server: stable<br/>v8.12.1"]
    Cert_Canary --> Canary["⚙️ Server: canary<br/>v8.13.0"]

    CA --> CA_D["Deployment"]
    Stable --> ST_D["Deployment"]
    Canary --> CN_D["Deployment"]

    CA_D -->|mounts| CA_PVC["💾 CA Data PVC (RWO)"]
    CA_D -->|mounts| Code["📦 Code<br/>(OCI Image or PVC)"]
    ST_D -->|mounts| Code
    CN_D -->|mounts| Code
```

A **Config** is the root resource - it holds shared configuration (puppet.conf, PuppetDB connection), manages code deployment, and references a **CertificateAuthority** via `authorityRef`. A **CertificateAuthority** initializes the CA infrastructure and periodically refreshes the CRL. Each **Certificate** is signed by the CA and stored as a Kubernetes Secret. A **Server** references a Certificate and creates a Deployment - it can run as CA, catalog server, or both. Servers declare pool membership via `poolRefs`. **Pools** are pure networking resources that create Services selecting Server pods by pool label, with optional Gateway API TLSRoute for SNI-based routing.

Puppet code is mounted into Server pods via **OCI image volumes** (immutable, automatic rollout on image change, K8s 1.31+) or a **PVC** (mutable, externally managed). See [Code Deployment](docs/concepts/code-deployment.md) for details.

### Pool Traffic Flow

Each Pool owns a Kubernetes Service that selects Server pods. The CA server can be member of both pools - handling CA requests via its dedicated pool and also serving catalog requests through the server pool.

**LoadBalancer Services** - each Pool gets its own external IP:

```mermaid
graph LR
    Agent["🖥️ Agents"] --> LB
    Agent --> CA_SVC

    subgraph Kubernetes
        LB["🌐 Pool: puppet<br/>Service (LoadBalancer)"]
        CA_SVC["🔐 Pool: puppet-ca<br/>Service (LoadBalancer)"]

        LB --> CA["🔐 Server: ca<br/>replicas: 1"]
        LB --> Stable["⚙️ Server: stable<br/>replicas: 3 - v8.12.1"]
        LB --> Canary["⚙️ Server: canary<br/>replicas: 1 - v8.13.0"]

        CA_SVC --> CA
    end
```

**Gateway API TLSRoute** - all Pools share a single LoadBalancer, routed by SNI hostname:

```mermaid
graph LR
    Agent["🖥️ Agents"] --> GW

    subgraph Kubernetes
        GW["🌐 Gateway<br/>(shared LoadBalancer)"]

        GW --> TR1["🔀 TLSRoute<br/>puppet.example.com"]
        GW --> TR2["🔀 TLSRoute<br/>puppet-ca.example.com"]
        TR1 --> LB["Pool: puppet<br/>Service (ClusterIP)"]
        TR2 --> CA_SVC["🔐 Pool: puppet-ca<br/>Service (ClusterIP)"]

        LB --> CA["🔐 Server: ca<br/>replicas: 1"]
        LB --> Stable["⚙️ Server: stable<br/>replicas: 3 - v8.12.1"]
        LB --> Canary["⚙️ Server: canary<br/>replicas: 1 - v8.13.0"]

        CA_SVC --> CA
    end
```

## CRD Model

All resources use the API group `openvox.voxpupuli.org/v1alpha1`.

| Kind | Purpose | Creates |
|---|---|---|
| **`Config`** | Shared config (puppet.conf, auth.conf, etc.), PuppetDB connection | ConfigMaps, Secrets, ServiceAccount |
| **`CertificateAuthority`** | CA infrastructure: keys, signing, split Secrets (cert, key, CRL) | PVC, Job, ServiceAccount, Role, RoleBinding, 3 Secrets |
| **`SigningPolicy`** | Declarative CSR signing policy (any, pattern, DNS SANs, CSR attributes) | *(rendered into Config's autosign Secret)* |
| **`NodeClassifier`** | External Node Classifier (ENC) endpoint (Foreman, PE, custom HTTP) | *(rendered into Config's ENC Secret)* |
| **`Certificate`** | Lifecycle of a single certificate (request, sign) | TLS Secret |
| **`Server`** | OpenVox Server instance pool (CA and/or server role), declares pool membership via `poolRefs` | Deployment |
| **`Pool`** | Networking resource: Service + optional Gateway API TLSRoute for Servers that reference this Pool | Service, TLSRoute (optional) |

### Planned (not yet implemented)

| Kind | Purpose |
|---|---|
| *`Database`* | *OpenVox DB (StatefulSet, Service)* |

## Differences to VM-based Installations

Traditional Puppet/OpenVox Server installations on VMs use OS packages that install both a system Ruby (CRuby) and the server JAR with its embedded JRuby. The system Ruby is used by CLI tools like `puppet config set` and `puppetserver ca`. The server process requires root privileges.

This operator takes a **Kubernetes-native approach** that differs in several key areas:

| | VM-based | openvox-operator |
|---|---|---|
| **Ruby** | System Ruby (CRuby) installed alongside JRuby for CLI tooling | **No system Ruby** - only JRuby embedded in the server JAR |
| **Configuration** | `puppet.conf` managed via `puppet config set`, Puppet modules, or config management | Declarative CRDs, operator renders ConfigMaps and Secrets |
| **Privileges** | Requires root | Fully rootless, random UID compatible |
| **CA Management** | `puppetserver ca` CLI with CRuby shebang | Custom JRuby wrapper that routes through `clojure.main` |
| **Certificates** | Each server has its own certificate | `Certificate` CRD manages the cert lifecycle - all replicas of a `Server` share the same certificate, enabling seamless horizontal scaling |
| **CSR Signing** | `autosign.conf` or Ruby scripts | `SigningPolicy` CRD with declarative rules (any, pattern, DNS SANs, CSR attributes) |
| **ENC** | Script on disk, manually configured | `NodeClassifier` CRD with support for Foreman, PE, or custom HTTP endpoints |
| **CRL** | File on disk, manual refresh | Split Secret (`{ca}-ca-crl`), operator-driven periodic refresh via CA HTTP API |
| **Scaling** | Horizontal scaling possible but requires manual setup of additional server VMs | Horizontal via Deployment replicas and HPA |
| **Code Deployment** | r10k installed on the VM, triggered by cron or webhook | OCI image volumes or PVC - code packaged as immutable container images |
| **Traffic Routing** | DNS round-robin or hardware load balancer per environment | Gateway API TLSRoute with SNI-based routing - share a single LoadBalancer across environments |
| **Multi-Version** | Separate VMs or manual package pinning | Multiple `Server` CRDs in the same `Pool` with different image tags |

By eliminating system Ruby from the runtime image, the container has a smaller footprint and a reduced attack surface, avoiding the duplicate Ruby installation (CRuby + JRuby) that the OS packages carry.

## Quick Start

### 1. Install the Operator

```bash
helm install openvox-operator \
  oci://ghcr.io/slauger/charts/openvox-operator \
  --namespace openvox-system \
  --create-namespace
```

### 2. Deploy a Stack

The `openvox-stack` chart deploys a complete OpenVox stack (Config, CertificateAuthority, SigningPolicy, Certificate, Server, Pool) with a single Helm release:

```bash
helm install production \
  oci://ghcr.io/slauger/charts/openvox-stack \
  --namespace openvox \
  --create-namespace
```

This creates a single CA+Server with autosign enabled.

## Local Development

Build all container images locally and deploy the operator to Docker Desktop Kubernetes:

```bash
make local-deploy
```

Deploy the openvox-stack (single-node by default):

```bash
make local-stack
```

Remove everything:

```bash
make uninstall
```

Override the image tag or use a different scenario:

```bash
make local-deploy LOCAL_TAG=my-feature
make local-stack LOCAL_TAG=my-feature STACK_VALUES=charts/openvox-stack/ci/multi-server-values.yaml
```

### Available Targets

| Target | Description |
|---|---|
| `install` | Install operator via Helm with default images |
| `stack` | Deploy openvox-stack via Helm with default images |
| `uninstall` | Remove stack, operator, and CRDs from the cluster |
| `unstack` | Remove only the openvox-stack |
| `local-build` | Build all container images with the current git commit as tag |
| `local-deploy` | Build images and deploy the operator via Helm |
| `local-install` | Deploy operator via Helm with local images (no build) |
| `local-stack` | Deploy openvox-stack via Helm with local images |
| `ci` | Run all CI checks locally (lint, vet, test, manifests, vulncheck, helm-lint) |

## Documentation

For detailed architecture documentation and CRD reference, see the [documentation](https://slauger.github.io/openvox-operator).

## License

Apache License 2.0
