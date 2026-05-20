# openvox-stack

![Version: 0.0.0](https://img.shields.io/badge/Version-0.0.0-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: 0.0.0](https://img.shields.io/badge/AppVersion-0.0.0-informational?style=flat-square)
Deploy a complete OpenVox stack (Config, CA, Servers, Pools)

## Overview

The openvox-stack chart deploys a complete OpenVox (Puppet) environment using the openvox-operator CRDs.
It creates Config, CertificateAuthority, Server, Pool, and optionally Database, NodeClassifier,
ReportProcessor, and SigningPolicy resources in a single release.

This chart requires the [openvox-operator](https://github.com/slauger/openvox-operator/tree/main/charts/openvox-operator)
to be installed in the cluster.

For the full documentation visit [slauger.github.io/openvox-operator](https://slauger.github.io/openvox-operator/).

## Prerequisites

- Kubernetes 1.28+
- Helm 3.14+
- openvox-operator installed in the cluster

## Installation

```bash
helm install openvox oci://ghcr.io/slauger/charts/openvox-stack
```

## Maintainers

| Name | Email | Url |
| ---- | ------ | --- |
| Simon Lauger | <simon@lauger.de> | <https://lauger.de> |
## Source Code

* <https://github.com/slauger/openvox-operator>
## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| ca.name | string | `""` | CA resource name. Defaults to {fullname}-ca. |
| ca.storage.size | string | `"1Gi"` | PVC size for CA storage. |
| ca.storage.storageClass | string | `""` | Storage class for CA PVC. Empty uses cluster default. |
| ca.ttl | string | `"5y"` | CA certificate time-to-live. |
| config.code.claimName | string | `""` | Existing PVC claim name for code volume. |
| config.code.image | string | `""` | Init container image for Puppet code deployment. |
| config.code.imagePullPolicy | string | `"IfNotPresent"` | Pull policy for the code init container. |
| config.code.imagePullSecret | string | `""` | Pull secret for the code init container. |
| config.image.pullPolicy | string | `"Always"` | Image pull policy. |
| config.image.pullSecrets | list | `[]` | Image pull secrets. |
| config.image.repository | string | `"ghcr.io/slauger/openvox-server"` | OpenVox Server image repository. |
| config.image.tag | string | `"latest"` | Image tag. |
| config.name | string | `""` | Config resource name. Defaults to release name. |
| config.puppet.environmentPath | string | `""` | Path to Puppet environments. |
| config.puppet.environmentTimeout | string | `""` | Environment timeout setting. |
| config.puppet.extraConfig | object | `{}` | Extra puppet.conf sections and settings. |
| config.puppet.hieraConfig | string | `""` | Path to Hiera configuration file. |
| config.puppet.reports | string | `"store"` | Enabled report processors. |
| config.puppet.storeBackend | string | `""` | Storage backend for stored configurations. |
| config.puppet.storeconfigs | bool | `false` | Enable stored configurations. |
| config.puppetdb.serverUrls | list | `[]` | PuppetDB server URLs for report storage. |
| config.readOnlyRootFilesystem | bool | `false` | Enable read-only root filesystem for server containers. |
| database.certificate.certname | string | `""` | Certificate common name for PuppetDB. Required when enabled. |
| database.certificate.dnsAltNames | list | `[]` | Additional DNS subject alternative names. |
| database.enabled | bool | `false` | Enable PuppetDB database deployment. |
| database.image.pullPolicy | string | `"IfNotPresent"` | Image pull policy. |
| database.image.repository | string | `"ghcr.io/slauger/openvox-db"` | OpenVox DB image repository. |
| database.image.tag | string | `"latest"` | Image tag. |
| database.javaArgs | string | `""` | JVM arguments for PuppetDB. |
| database.name | string | `""` | Database resource name. Required when enabled. |
| database.postgres.credentialsSecretRef | string | `""` | Name of Secret containing PostgreSQL credentials. Required when enabled. |
| database.postgres.database | string | `"openvoxdb"` | PostgreSQL database name. |
| database.postgres.host | string | `""` | PostgreSQL host. Required when enabled. |
| database.postgres.port | int | `5432` | PostgreSQL port. |
| database.postgres.sslMode | string | `"require"` | PostgreSQL SSL mode. |
| database.replicas | int | `1` | Number of PuppetDB replicas. |
| database.resources | object | `{"limits":{"memory":"1Gi"},"requests":{"cpu":"250m","memory":"512Mi"}}` | CPU/memory resource requests and limits for PuppetDB. |
| gateway.name | string | `""` | Shared Gateway resource name for TLSRoute integration. |
| gateway.sectionName | string | `""` | Listener section name on the Gateway. |
| nodeClassifier.auth.mtls | bool | `false` | Enable mutual TLS authentication for ENC requests. |
| nodeClassifier.cache.directory | string | `"/var/cache/openvox-enc"` | Directory path for cached ENC responses. |
| nodeClassifier.cache.enabled | bool | `false` | Enable response caching for ENC requests. |
| nodeClassifier.enabled | bool | `false` | Enable external node classifier integration. |
| nodeClassifier.name | string | `""` | NodeClassifier resource name. Defaults to {fullname}-enc. |
| nodeClassifier.request.body | string | `""` | Request body template. |
| nodeClassifier.request.method | string | `"GET"` | HTTP method for ENC requests. |
| nodeClassifier.request.path | string | `"/node/{certname}"` | Request path template. {certname} is replaced with the node name. |
| nodeClassifier.response.format | string | `"yaml"` | Response format from the ENC endpoint. |
| nodeClassifier.timeoutSeconds | int | `10` | Timeout in seconds for ENC requests. |
| nodeClassifier.url | string | `""` | URL of the external node classifier API. |
| pools | list | `[{"name":"ca","service":{"port":8140,"type":"ClusterIP"}},{"name":"server","service":{"port":8140,"type":"ClusterIP"}}]` | Pool definitions. Each entry creates a Pool resource with a Service. |
| reportProcessors | list | `[]` | Report processor endpoints. |
| servers | list | `[{"ca":true,"certificate":{"certname":"puppet","dnsAltNames":[]},"name":"ca","poolRefs":["ca","server"],"replicas":1,"resources":{"limits":{"memory":"2Gi"},"requests":{"cpu":"500m","memory":"1Gi"}},"server":true},{"ca":false,"certificate":{"certname":"server","dnsAltNames":[]},"name":"server","poolRefs":["server"],"replicas":2,"resources":{"limits":{"memory":"2Gi"},"requests":{"cpu":"500m","memory":"1Gi"}},"server":true}]` | Server definitions. Each entry creates a Server resource. |
| signingPolicies | list | `[]` | Signing policies for certificate auto-signing. |

----------------------------------------------
Autogenerated from chart metadata using [helm-docs v1.14.2](https://github.com/norwoodj/helm-docs/releases/v1.14.2)
