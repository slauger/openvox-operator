# OpenVox Operator

A Kubernetes Operator that maps [OpenVox Server](https://github.com/OpenVoxProject) infrastructure onto native building blocks - CRDs, Secrets, OCI image volumes, and Gateway API - for running Puppet on **Kubernetes** and **OpenShift**.

## Features

--8<-- "docs/_snippets/features.md"

## How It Works

The operator manages OpenVox Server infrastructure through a set of Custom Resource Definitions (CRDs):

| Kind | Purpose | Creates |
|---|---|---|
| **Config** | Shared config (puppet.conf, auth.conf, etc.), OpenVox DB connection | ConfigMaps, Secrets, ServiceAccount |
| **CertificateAuthority** | CA infrastructure: keys, signing, split Secrets (cert, key, CRL) | PVC, Job, ServiceAccount, Role, RoleBinding, 3 Secrets |
| **SigningPolicy** | Declarative CSR signing policy (any, pattern, CSR attributes) | *(rendered into Config's autosign Secret)* |
| **NodeClassifier** | External Node Classifier (ENC) endpoint (Foreman, PE, custom HTTP) | *(rendered into Config's ENC Secret)* |
| **Certificate** | Lifecycle of a single certificate (request, sign) | TLS Secret |
| **Server** | OpenVox Server instance pool (CA and/or server role), declares pool membership via `poolRefs` | Deployment, HPA, PDB, NetworkPolicy |
| **Pool** | Networking resource: Service + optional Gateway API TLSRoute for Servers that reference this Pool | Service, TLSRoute (optional) |
| **Database** | OpenVox DB (PuppetDB) instance with external PostgreSQL backend | Deployment, Service, ConfigMap, Secret, PDB, NetworkPolicy |
| **ReportProcessor** | Webhook-based report forwarding (OpenVox DB or custom HTTP endpoints) | *(rendered into Config's report-webhook Secret)* |

For details on the CRD hierarchy and design rationale, see [Architecture](concepts/architecture.md). Puppet code is deployed via [OCI image volumes or PVCs](concepts/code-deployment.md). External Node Classifiers (Foreman, PE, custom HTTP) are configured via the [NodeClassifier CRD](concepts/external-node-classification.md). Report forwarding is configured via the [ReportProcessor CRD](concepts/report-processing.md). OpenVox DB is managed via the [Database CRD](concepts/database.md). Pools support optional [Gateway API TLSRoute](concepts/gateway-api.md) for SNI-based routing. See [Traffic Flow](concepts/traffic-flow.md) for how agents connect to servers. Existing CAs can be imported or connected via the [External CA guide](guides/ca-import.md).

## License

Apache License 2.0
