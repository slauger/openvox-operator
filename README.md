# 🦊 openvox-operator

A Kubernetes Operator for running [OpenVox Server](https://github.com/OpenVoxProject) environments on **Kubernetes** and **OpenShift**.

- 🔐 **Automated CA Lifecycle** - CA initialization, certificate signing and distribution - fully managed
- 📦 **One Image, Two Roles** - Same rootless image runs as CA or server, configured by the operator
- ⚡ **Scalable Servers** - Scale catalog compilation horizontally - multiple server pools with HPA
- 🔄 **Multi-Version Deployments** - Run different server versions side by side - canary deployments, rolling upgrades
- 🔒 **Rootless & OpenShift Ready** - Random UID compatible, no root, no ezbake, no privilege escalation
- ☸️ **Kubernetes-Native** - All config via ConfigMaps/Secrets - no entrypoint scripts, no ENV translation

## Architecture

```mermaid
graph TD
    Op["🦊 OpenVox Operator"]
    Op -->|manages| Env

    Env["📋 Environment CRD<br/>production"]
    Env --> CA["🔐 Server CRD: ca<br/>role: ca + server"]
    Env --> Stable["⚙️ Server CRD: stable<br/>role: server - v8.8.1"]
    Env --> Canary["⚙️ Server CRD: canary<br/>role: server - v8.9.0"]

    CA --> CA_D["Deployment"]
    Stable --> ST_D["Deployment"]
    Canary --> CN_D["Deployment"]

    CA_D -->|mounts| CA_PVC["💾 CA Data PVC (RWO)"]
    CA_D -->|mounts| Code_PVC
    ST_D -->|mounts| Code_PVC["📦 Code PVC (RWX)"]
    CN_D -->|mounts| Code_PVC
```

### Pool Traffic Flow

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

The CA server can be member of both pools - it handles CA requests via the `puppet-ca` service and can also serve catalog requests from external agents via the `puppet` service.

## CRD Model

All resources use the API group `openvox.voxpupuli.org/v1alpha1`.

| Kind | Purpose | Creates |
|---|---|---|
| **`Environment`** | Shared config, CA lifecycle, OpenVox DB connection | ConfigMaps, CA Job, CA Secret, CA PVC, CA Service |
| **`Pool`** | Owns a Kubernetes Service | Service (type, annotations, port) |
| **`Server`** | OpenVox Server instance pool | Deployment, HPA |
| **`CodeDeploy`** | r10k code deployment from Git | PVC, Job, CronJob |
| **`SigningPolicy`** | Policy-based CSR approval (psk, pattern, token, any) | — |
| **`CertificateRequest`** | Represents a pending/signed CSR | — |
| *`Database`* | *OpenVox DB (future)* | *StatefulSet, Service* |


## Differences to VM-based Installations

Traditional Puppet/OpenVox Server installations on VMs use OS packages that install both a system Ruby (CRuby) and the server JAR with its embedded JRuby. The system Ruby is used by CLI tools like `puppet config set` and `puppetserver ca`. The server process requires root privileges.

This operator takes a **Kubernetes-native approach** that differs in several key areas:

| | VM-based | openvox-operator |
|---|---|---|
| **Ruby** | System Ruby (CRuby) installed alongside JRuby for CLI tooling | **No system Ruby** - only JRuby embedded in the server JAR |
| **Configuration** | `puppet.conf` managed via `puppet config set`, Puppet modules, or config management | Declarative CRDs, operator renders ConfigMaps and Secrets |
| **Privileges** | Requires root | Fully rootless, random UID compatible |
| **CA Management** | `puppetserver ca` CLI with CRuby shebang | Custom JRuby wrapper that routes through `clojure.main` |
| **Certificates** | Each server has its own certificate | All replicas of a `Server` share the same certificate, enabling seamless horizontal scaling |
| **Scaling** | Horizontal scaling possible but requires manual setup of additional server VMs | Horizontal via Deployment replicas and HPA |
| **Code Deployment** | r10k installed on the VM, triggered by cron or webhook | `CodeDeploy` CRD manages r10k as a Kubernetes Job/CronJob |
| **Multi-Version** | Separate VMs or manual package pinning | Multiple `Server` CRDs in the same `Pool` with different image tags |

By eliminating system Ruby from the runtime image, the container has a smaller footprint and a reduced attack surface, avoiding the duplicate Ruby installation (CRuby + JRuby) that the OS packages carry.

## Installation

```bash
helm install openvox-operator \
  oci://ghcr.io/slauger/charts/openvox-operator \
  --version 0.1.0 \
  --namespace openvox-system \
  --create-namespace
```

## Documentation

For getting started guides, examples, and detailed architecture documentation, see the [documentation](https://slauger.github.io/openvox-operator).

## License

Apache License 2.0
