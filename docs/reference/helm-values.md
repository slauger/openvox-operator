# Helm Values Reference

This document describes the configuration values for the OpenVox Helm charts.

## openvox-operator

The operator chart deploys the OpenVox Operator controller that reconciles custom resources.

### Installation

```bash
helm install openvox-operator \
  oci://ghcr.io/slauger/charts/openvox-operator \
  --namespace openvox-system \
  --create-namespace
```

### Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `replicaCount` | int | `1` | Number of operator pod replicas |
| `image.repository` | string | `ghcr.io/slauger/openvox-operator` | Image repository |
| `image.tag` | string | `latest` | Image tag (ignored if digest is set) |
| `image.digest` | string | `""` | Image digest (takes precedence over tag) |
| `image.pullPolicy` | string | `Always` | Image pull policy |
| `imagePullSecrets` | list | `[]` | Image pull secrets for private registries |
| `podAnnotations` | object | `{}` | Annotations for the operator Pod template |
| `serviceAccount.create` | bool | `true` | Create a service account |
| `serviceAccount.name` | string | `""` | Service account name (auto-generated if empty) |
| `serviceAccount.annotations` | object | `{}` | Service account annotations |
| `leaderElect` | bool | `true` | Enable leader election |
| `scope.mode` | string | `cluster` | Operator scope: `cluster` (all namespaces) or `namespace` (single namespace) |
| `scope.watchNamespace` | string | `""` | Namespace to watch when mode is `namespace` |
| `gatewayAPI.enabled` | bool | `true` | Enable TLSRoute support via Gateway API |
| `resources.limits.cpu` | string | `500m` | CPU limit |
| `resources.limits.memory` | string | `256Mi` | Memory limit |
| `resources.requests.cpu` | string | `10m` | CPU request |
| `resources.requests.memory` | string | `128Mi` | Memory request |
| `webhook.enabled` | bool | `false` | Enable CRD validation/mutation webhooks |
| `webhook.port` | int | `9443` | Webhook server port |
| `webhook.certManager.enabled` | bool | `true` | Use cert-manager for webhook certificates |
| `webhook.certManager.duration` | string | `8760h` | Certificate duration |
| `webhook.certManager.renewBefore` | string | `720h` | Renew certificate before expiry |
| `webhook.tls.certSecret` | string | `""` | Existing TLS Secret name |
| `webhook.tls.caBundle` | string | `""` | Base64-encoded CA certificate |
| `nodeSelector` | object | `{}` | Node selector |
| `tolerations` | list | `[]` | Tolerations |
| `affinity` | object | `{}` | Affinity rules |

---

## openvox-stack

The stack chart creates a complete OpenVox Server deployment including Config, CertificateAuthority, Certificates, Servers, and Pools.

### Installation

```bash
helm install openvox \
  oci://ghcr.io/slauger/charts/openvox-stack \
  --namespace openvox \
  --create-namespace \
  -f values.yaml
```

### Config Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `config.name` | string | `""` | Config resource name (defaults to release name) |
| `config.readOnlyRootFilesystem` | bool | `false` | Enable read-only root filesystem |
| `config.image.repository` | string | `ghcr.io/slauger/openvox-server` | OpenVox Server image |
| `config.image.tag` | string | `latest` | Image tag |
| `config.image.pullPolicy` | string | `Always` | Image pull policy |
| `config.image.pullSecrets` | list | `[]` | Image pull secrets |
| `config.puppetdb.serverUrls` | list | `[]` | PuppetDB server URLs |
| `config.puppet.environmentTimeout` | string | `""` | Environment timeout |
| `config.puppet.environmentPath` | string | `""` | Path to Puppet environments |
| `config.puppet.hieraConfig` | string | `""` | Path to Hiera configuration |
| `config.puppet.storeconfigs` | bool | `false` | Enable stored configurations |
| `config.puppet.storeBackend` | string | `""` | Storage backend |
| `config.puppet.reports` | string | `store` | Enabled report processors |
| `config.puppet.extraConfig` | object | `{}` | Extra puppet.conf sections |
| `config.code.image` | string | `""` | Code init container image |
| `config.code.imagePullPolicy` | string | `IfNotPresent` | Code init container pull policy |
| `config.code.imagePullSecret` | string | `""` | Code init container pull secret |
| `config.code.claimName` | string | `""` | Existing PVC for code volume |

### CA Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `ca.name` | string | `""` | CA resource name |
| `ca.ttl` | string | `5y` | CA certificate TTL |
| `ca.storage.size` | string | `1Gi` | PVC size for CA storage |
| `ca.storage.storageClass` | string | `""` | Storage class (empty uses cluster default) |

### Server Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `servers` | list | See below | List of Server definitions |
| `servers[].name` | string | Required | Server resource name |
| `servers[].ca` | bool | `false` | Enable CA role |
| `servers[].server` | bool | `true` | Enable server role |
| `servers[].poolRefs` | list | `[]` | Pool references |
| `servers[].certificate.certname` | string | Required | Certificate common name |
| `servers[].certificate.dnsAltNames` | list | `[]` | DNS subject alternative names |
| `servers[].replicas` | int | `1` | Number of replicas |
| `servers[].resources` | object | `{}` | CPU/memory resources |
| `servers[].javaArgs` | string | `""` | JVM arguments |
| `servers[].autoscaling.minReplicas` | int | | Minimum replicas for HPA |
| `servers[].autoscaling.maxReplicas` | int | | Maximum replicas for HPA |
| `servers[].autoscaling.targetCPUUtilizationPercentage` | int | | Target CPU utilization |
| `servers[].pdb.minAvailable` | int | | PodDisruptionBudget minimum available |
| `servers[].networkPolicy.enabled` | bool | | Enable NetworkPolicy |
| `servers[].affinity` | object | `{}` | Affinity rules |
| `servers[].topologySpreadConstraints` | list | `[]` | Topology spread constraints |
| `servers[].priorityClassName` | string | `""` | Priority class name |

Default servers configuration:

```yaml
servers:
  - name: ca
    ca: true
    server: true
    poolRefs: [ca, server]
    certificate:
      certname: puppet
    replicas: 1
  - name: server
    ca: false
    server: true
    poolRefs: [server]
    certificate:
      certname: server
    replicas: 2
```

### Pool Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `pools` | list | See below | List of Pool definitions |
| `pools[].name` | string | Required | Pool resource name |
| `pools[].service.type` | string | `ClusterIP` | Service type |
| `pools[].service.port` | int | `8140` | Service port |
| `pools[].route.enabled` | bool | `false` | Enable HTTPRoute |
| `pools[].route.hostname` | string | `""` | Route hostname |

### Signing Policy Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `signingPolicies` | list | `[]` | List of SigningPolicy definitions |
| `signingPolicies[].name` | string | Required | Policy name |
| `signingPolicies[].any` | bool | `false` | Auto-sign any certificate |
| `signingPolicies[].csrAttributes` | list | `[]` | CSR attribute matching rules |

Example:

```yaml
signingPolicies:
  - name: autosign
    any: true
  - name: preshared-key
    csrAttributes:
      - name: pp_preshared_key
        value: "my-secret-token"
```

### Database Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `database.enabled` | bool | `false` | Enable PuppetDB deployment |
| `database.name` | string | `""` | Database resource name |
| `database.image.repository` | string | `ghcr.io/slauger/openvox-db` | OpenVox DB image |
| `database.image.tag` | string | `latest` | Image tag |
| `database.certificate.certname` | string | `""` | Certificate CN |
| `database.postgres.host` | string | `""` | PostgreSQL host |
| `database.postgres.port` | int | `5432` | PostgreSQL port |
| `database.postgres.database` | string | `openvoxdb` | Database name |
| `database.postgres.credentialsSecretRef` | string | `""` | Secret with credentials |
| `database.postgres.sslMode` | string | `require` | SSL mode |
| `database.replicas` | int | `1` | Number of replicas |
| `database.resources` | object | | CPU/memory resources |

### Node Classifier Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `nodeClassifier.enabled` | bool | `false` | Enable ENC integration |
| `nodeClassifier.url` | string | `""` | ENC API URL |
| `nodeClassifier.request.method` | string | `GET` | HTTP method |
| `nodeClassifier.request.path` | string | `/node/{certname}` | Request path template |
| `nodeClassifier.response.format` | string | `yaml` | Response format |
| `nodeClassifier.timeoutSeconds` | int | `10` | Request timeout |
| `nodeClassifier.auth.mtls` | bool | `false` | Enable mTLS |
| `nodeClassifier.cache.enabled` | bool | `false` | Enable response caching |

### Gateway Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `gateway.name` | string | `""` | Shared Gateway resource name |
| `gateway.sectionName` | string | `""` | Gateway listener section name |

### Report Processor Values

| Key | Type | Default | Description |
|-----|------|---------|-------------|
| `reportProcessors` | list | `[]` | List of report processor definitions |
| `reportProcessors[].name` | string | Required | Processor name |
| `reportProcessors[].url` | string | Required | Webhook URL |
| `reportProcessors[].processor` | string | `""` | Processor type (e.g., `puppetdb`) |

Example:

```yaml
reportProcessors:
  - name: my-webhook
    url: http://my-webhook:8080/reports
  - name: my-puppetdb
    url: https://my-puppetdb:8081
    processor: puppetdb
```
