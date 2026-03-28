# OpenVox Operator

A Kubernetes Operator that maps [OpenVox Server](https://github.com/OpenVoxProject) infrastructure onto native building blocks - CRDs, Secrets, OCI image volumes, and Gateway API - for running Puppet on **Kubernetes** and **OpenShift**.

## Features

- 🔐 **Automated CA Lifecycle** - CA initialization, certificate signing, distribution, and periodic CRL refresh - fully managed
- 📜 **Declarative Signing Policies** - CSR approval via patterns, CSR attributes, or open signing - no autosign scripts
- 🏷️ **External Node Classification** - Declarative ENC support for Foreman, Puppet Enterprise, or custom HTTP classifiers
- 📦 **One Image, Two Roles** - Same rootless image runs as CA or server, configured by the operator
- ⚡ **Scalable Servers** - Scale catalog compilation horizontally with multiple server pools and HPA
- 🔄 **Multi-Version Deployments** - Run different server versions side by side for canary deployments and rolling upgrades
- 🔒 **Rootless & OpenShift Ready** - Random UID compatible, no root, no ezbake, no privilege escalation
- 🪶 **Minimal Image** - UBI9-based, no system Ruby, no ezbake packaging - smaller footprint, fewer updates
- 🧠 **Auto-tuned JVM** - Heap size calculated from memory limits (90%) - no manual `-Xmx` tuning needed
- 📦 **OCI Image Volumes** - Package Puppet code as OCI images, deploy immutably with automatic rollout (K8s 1.35+)
- 🌐 **Gateway API** - SNI-based TLSRoute support - share a single LoadBalancer across environments via TLS passthrough
- 📊 **Report Processing** - Forward Puppet reports to OpenVox DB or custom HTTP endpoints via declarative webhook configuration
- 🔃 **Automatic Config Rollout** - Config and certificate changes trigger rolling restarts automatically
- ☸️ **Kubernetes-Native** - All config via ConfigMaps/Secrets, no entrypoint scripts, no ENV translation

## How It Works

The operator manages OpenVox Server infrastructure through a set of Custom Resource Definitions (CRDs):

| Kind | Purpose | Creates |
|---|---|---|
| **Config** | Shared config (puppet.conf, auth.conf, etc.), OpenVox DB connection | ConfigMaps, Secrets, ServiceAccount |
| **CertificateAuthority** | CA infrastructure: keys, signing, split Secrets (cert, key, CRL) | PVC, Job, ServiceAccount, Role, RoleBinding, 3 Secrets |
| **SigningPolicy** | Declarative CSR signing policy (any, pattern, CSR attributes) | *(rendered into Config's autosign Secret)* |
| **NodeClassifier** | External Node Classifier (ENC) endpoint (Foreman, PE, custom HTTP) | *(rendered into Config's ENC Secret)* |
| **Certificate** | Lifecycle of a single certificate (request, sign) | TLS Secret |
| **Server** | OpenVox Server instance pool (CA and/or server role), declares pool membership via `poolRefs` | Deployment |
| **Pool** | Networking resource: Service + optional Gateway API TLSRoute for Servers that reference this Pool | Service, TLSRoute (optional) |
| **ReportProcessor** | Webhook-based report forwarding (OpenVox DB or custom HTTP endpoints) | *(rendered into Config's report-webhook Secret)* |

For details on the CRD hierarchy and design rationale, see [Architecture](concepts/architecture.md). Puppet code is deployed via [OCI image volumes or PVCs](concepts/code-deployment.md). External Node Classifiers (Foreman, PE, custom HTTP) are configured via the [NodeClassifier CRD](concepts/external-node-classification.md). Report forwarding is configured via the [ReportProcessor CRD](concepts/report-processing.md). Pools support optional [Gateway API TLSRoute](concepts/gateway-api.md) for SNI-based routing. See [Traffic Flow](concepts/traffic-flow.md) for how agents connect to servers.

## License

Apache License 2.0
