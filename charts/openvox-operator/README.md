# openvox-operator

![Version: 0.0.0](https://img.shields.io/badge/Version-0.0.0-informational?style=flat-square) ![Type: application](https://img.shields.io/badge/Type-application-informational?style=flat-square) ![AppVersion: 0.0.0](https://img.shields.io/badge/AppVersion-0.0.0-informational?style=flat-square)
Kubernetes Operator for running OpenVox Server environments

## Overview

The openvox-operator chart installs the Kubernetes operator for managing OpenVox (Puppet) infrastructure.
The operator manages the full lifecycle of OpenVox Server environments including certificate authorities,
server pools, PuppetDB databases, node classifiers, and report processors via Kubernetes Custom Resources.

For the full documentation visit [slauger.github.io/openvox-operator](https://slauger.github.io/openvox-operator/).

## Prerequisites

- Kubernetes 1.28+
- Helm 3.14+
- cert-manager (optional, for admission webhook TLS)

## Installation

```bash
helm install openvox-operator oci://ghcr.io/slauger/charts/openvox-operator
```

To deploy a complete OpenVox stack after installing the operator, use the
[openvox-stack](https://github.com/slauger/openvox-operator/tree/main/charts/openvox-stack) chart.

## Upgrading

Helm does not update CRDs on `helm upgrade`. Apply the CRDs of the target
version before upgrading the release, otherwise the operator runs against the
schema that was installed first:

```bash
helm pull oci://ghcr.io/slauger/charts/openvox-operator --version "$VERSION" --untar
kubectl apply -f openvox-operator/crds/
helm upgrade openvox-operator oci://ghcr.io/slauger/charts/openvox-operator --version "$VERSION"
```

See [Installation](https://slauger.github.io/openvox-operator/getting-started/installation/) for details.

## Maintainers

| Name | Email | Url |
| ---- | ------ | --- |
| Simon Lauger | <simon@lauger.de> | <https://lauger.de> |
## Source Code

* <https://github.com/slauger/openvox-operator>
## Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| affinity | object | `{}` | Affinity rules for pod scheduling. |
| gatewayAPI.enabled | bool | `true` | Enable TLSRoute support via Gateway API. |
| image.digest | string | `""` | Image digest. Takes precedence over tag. |
| image.pullPolicy | string | `"Always"` | Image pull policy. |
| image.repository | string | `"ghcr.io/slauger/openvox-operator"` | Image repository. |
| image.tag | string | `""` | Image tag. Defaults to the chart appVersion. Ignored if digest is set. |
| imagePullSecrets | list | `[]` | Image pull secrets for private registries. |
| leaderElect | bool | `true` | Enable leader election for controller manager. |
| metrics.certManager.duration | string | `"8760h"` | Certificate validity. |
| metrics.certManager.enabled | bool | `true` | Issue the metrics serving certificate with cert-manager. Only used when `secure` is true. Without it the operator generates a self-signed certificate at startup, which scrapers can only skip verifying. |
| metrics.certManager.renewBefore | string | `"720h"` | Renew this long before expiry. |
| metrics.enabled | bool | `true` | Enable the metrics endpoint. |
| metrics.port | int | `8080` | Port for the metrics endpoint. |
| metrics.secure | bool | `false` | Serve metrics over HTTPS and require an authenticated, authorized client. Off by default so existing scrape configurations keep working; expected to become the default in a later release. Leaving it off serves the metrics as plaintext to anything that can reach the pod. |
| metrics.service.enabled | bool | `true` | Create a Service for the metrics endpoint. |
| metrics.serviceMonitor.enabled | bool | `false` | Create a Prometheus ServiceMonitor resource. |
| metrics.serviceMonitor.interval | string | `"30s"` | Scrape interval for the ServiceMonitor. |
| metrics.serviceMonitor.labels | object | `{}` | Additional labels for the ServiceMonitor. |
| metrics.tls.certSecret | string | `""` | Secret holding `tls.crt` and `tls.key` for the metrics endpoint. Set this to bring your own certificate instead of the cert-manager issued one. |
| nodeSelector | object | `{}` | Node selector for pod scheduling. |
| podAnnotations | object | `{}` | Annotations applied to the operator Pod template (e.g. for log collectors, Prometheus scraping, or forcing rollouts via a checksum annotation). |
| replicaCount | int | `1` | Number of operator pod replicas. |
| resources | object | `{"limits":{"cpu":"500m","memory":"256Mi"},"requests":{"cpu":"10m","memory":"128Mi"}}` | CPU/memory resource requests and limits. |
| scope.mode | string | `"cluster"` | Operator scope mode. cluster: watches all namespaces (ClusterRole). namespace: watches a single namespace (Role). |
| scope.watchNamespace | string | `""` | Namespace to watch when mode is namespace. Defaults to release namespace if empty. |
| serviceAccount.annotations | object | `{}` | Annotations added to the service account. |
| serviceAccount.create | bool | `true` | Create a service account for the operator. |
| serviceAccount.name | string | `""` | Service account name. Auto-generated if empty. |
| tolerations | list | `[]` | Tolerations for pod scheduling. |
| webhook.certManager.duration | string | `"8760h"` | Certificate duration (e.g. 8760h for 1 year). |
| webhook.certManager.enabled | bool | `true` | Use cert-manager to provision webhook TLS certificates. |
| webhook.certManager.renewBefore | string | `"720h"` | Renew certificate before expiry (e.g. 720h for 30 days). |
| webhook.enabled | bool | `false` | Enable CRD validation/mutation webhooks. |
| webhook.port | int | `9443` | Port for the webhook server. |
| webhook.tls.caBundle | string | `""` | Base64-encoded CA certificate for the webhook configuration. |
| webhook.tls.certSecret | string | `""` | Name of an existing TLS Secret containing tls.crt, tls.key, and ca.crt. |

----------------------------------------------
Autogenerated from chart metadata using [helm-docs v1.14.2](https://github.com/norwoodj/helm-docs/releases/v1.14.2)
