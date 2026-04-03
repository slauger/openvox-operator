# Config

A Config holds shared configuration for all Servers: the default container image, puppet.conf settings, and OpenVox DB (PuppetDB) connection. It is the root resource in the CRD hierarchy. The `authorityRef` field references a CertificateAuthority; CA settings (`ca_ttl`, `autosign`) are automatically pulled from it. The `databaseRef` field can reference a [Database](database.md) resource to automatically wire the PuppetDB connection URL from its status.

## Example

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Config
metadata:
  name: production
spec:
  authorityRef: production-ca
  image:
    repository: ghcr.io/slauger/openvox-server
    tag: "8.12.1"
  puppet:
    environmentTimeout: unlimited
    storeconfigs: true
    reports: puppetdb
  puppetdb:
    serverUrls:
      - "https://openvoxdb.example.com:8081"
```

## Spec

| Field | Type | Default | Description |
|---|---|---|---|
| `image` | [ImageSpec](index.md#imagespec) | **required** | Default container image for all Servers |
| `authorityRef` | string | - | Reference to the CertificateAuthority used by this Config |
| `databaseRef` | string | - | Reference to a [Database](database.md) whose `status.url` is used for `puppetdb.conf` (mutually exclusive with `puppetdb`) |
| `nodeClassifierRef` | string | - | Reference to a [NodeClassifier](nodeclassifier.md) for ENC support |
| `puppet` | [PuppetSpec](#puppetspec) | - | Shared puppet.conf settings |
| `puppetdb` | [PuppetDBSpec](#puppetdbspec) | - | OpenVox DB (PuppetDB) connection settings (mutually exclusive with `databaseRef`) |
| `puppetserver` | [PuppetServerSpec](#puppetserverspec) | - | puppetserver.conf, webserver.conf, and auth.conf settings |
| `logging` | [LoggingSpec](#loggingspec) | - | Logback.xml log level configuration |
| `metrics` | [MetricsSpec](#metricsspec) | - | Puppet Server metrics (JMX, Graphite) |
| `code` | [CodeSpec](index.md#codespec) | - | Puppet code source (OCI image or PVC) for all Servers |
| `readOnlyRootFilesystem` | bool | `false` | Enable read-only root filesystem on all Server pods for security hardening |

### PuppetSpec

| Field | Type | Default | Description |
|---|---|---|---|
| `environmentTimeout` | string | `unlimited` | How long Puppet caches environments |
| `environmentPath` | string | `/etc/puppetlabs/code/environments` | Path to Puppet environments |
| `hieraConfig` | string | `$confdir/hiera.yaml` | Path to Hiera configuration |
| `storeconfigs` | bool | `true` | Enable storeconfigs |
| `storeBackend` | string | `puppetdb` | Storeconfigs backend |
| `reports` | string | `puppetdb` | Report processors |
| `extraConfig` | map[string]string | - | Additional puppet.conf entries |

### PuppetDBSpec

| Field | Type | Default | Description |
|---|---|---|---|
| `serverUrls` | []string | - | OpenVox DB server URLs |

### PuppetServerSpec

Controls puppetserver.conf, webserver.conf, and auth.conf settings.

| Field | Type | Default | Description |
|---|---|---|---|
| `maxRequestsPerInstance` | int32 | `0` | Max requests per JRuby instance before restart (0 = unlimited) |
| `borrowTimeout` | int32 | `1200000` | Timeout in ms for borrowing a JRuby instance from the pool |
| `compileMode` | string | `off` | JRuby compilation mode (`jit` or `off`) |
| `clientAuth` | string | `want` | SSL client authentication mode (`want`, `need`, or `none`) |
| `httpClient` | [HTTPClientSpec](#httpclientspec) | - | HTTP client settings for outgoing connections |
| `authorizationRules` | [][AuthorizationRule](#authorizationrule) | - | Custom auth.conf rules inserted before the deny-all rule |

### HTTPClientSpec

| Field | Type | Default | Description |
|---|---|---|---|
| `connectTimeoutMs` | int32 | - | Connection timeout in milliseconds |
| `idleTimeoutMs` | int32 | - | Idle timeout in milliseconds |

### AuthorizationRule

Custom rules for auth.conf. Rules are evaluated in `sortOrder` (lower = earlier) and inserted before the default deny-all rule.

| Field | Type | Default | Description |
|---|---|---|---|
| `name` | string | **required** | Descriptive name for the rule |
| `matchRequest` | [AuthorizationMatchRequest](#authorizationmatchrequest) | **required** | Request matching criteria |
| `allow` | string | - | Clients to allow (`*` for all authenticated) |
| `allowUnauthenticated` | bool | `false` | Allow unauthenticated access |
| `deny` | string | - | Clients to deny (`*` for all) |
| `sortOrder` | int32 | `500` | Rule evaluation order (lower = earlier) |

### AuthorizationMatchRequest

| Field | Type | Default | Description |
|---|---|---|---|
| `path` | string | **required** | URL path or pattern to match |
| `type` | string | `path` | Match type (`path` or `regex`) |
| `method` | []string | - | HTTP methods to match (e.g. `["GET", "POST"]`) |

### LoggingSpec

Controls the Puppet Server logback.xml configuration.

| Field | Type | Default | Description |
|---|---|---|---|
| `level` | string | `INFO` | Root log level (`TRACE`, `DEBUG`, `INFO`, `WARN`, `ERROR`) |
| `loggers` | map[string]string | - | Per-logger level overrides (key = logger name, value = level) |

### MetricsSpec

Controls Puppet Server metrics.conf settings.

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Activate Puppet Server metrics |
| `jmx` | [JMXSpec](#jmxspec) | - | JMX metrics settings |
| `graphite` | [GraphiteSpec](#graphitespec) | - | Graphite metrics settings |

### JMXSpec

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Activate JMX metrics |

### GraphiteSpec

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Activate Graphite reporting |
| `host` | string | - | Graphite server hostname |
| `port` | int32 | `2003` | Graphite server port |
| `updateIntervalSeconds` | int32 | `60` | Interval between metric reports in seconds |

## Status

| Field | Type | Description |
|---|---|---|
| `phase` | string | Current lifecycle phase |
| `conditions` | []Condition | `ConfigReady` |

## Phases

| Phase | Description |
|---|---|
| `Pending` | Config created, waiting for reconciliation |
| `Running` | ConfigMap created, ready for use |
| `Error` | Reconciliation failed |

## Created Resources

| Resource | Name | Description |
|---|---|---|
| ConfigMap | `{name}` | puppet.conf, puppetserver.conf, auth.conf, webserver.conf, etc. |
| Secret | `{name}-enc` | ENC config for openvox-enc binary (only when `nodeClassifierRef` is set) |
| ServiceAccount | `{name}-server` | Shared ServiceAccount for all Server pods (`automountServiceAccountToken: false`) |
