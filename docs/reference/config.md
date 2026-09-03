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
    repository: ghcr.io/slauger/openvox-server-8
    tag: "latest"
  puppet:
    environmentTimeout: "0"
    storeconfigs: true
    reports: puppetdb
  puppetdb:
    serverUrls:
      - "https://openvoxdb.example.com:8081"
```

!!! note "Image naming"
    The content images are published as `openvox-server` / `openvox-db` (the current default
    major), with a major-suffixed variant (`openvox-server-8`) available to pin a specific
    OpenVox major - the unsuffixed name and the default-major suffix share the same image
    digest. The exact OpenVox versions baked into each image are pinned in
    `images/openvox-versions.yaml`, and every operator release lists the shipped component
    versions in its GitHub release notes.

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
| `code` | [[]CodeSpec](index.md#codespec) | - | Puppet code sources (OCI images / PVCs) for all Servers. A list; see [CodeSpec](index.md#codespec) for the mount target rules |
| `readOnlyRootFilesystem` | bool | `true` | Enable read-only root filesystem on all Server pods for security hardening |

### PuppetSpec

| Field | Type | Default | Description |
|---|---|---|---|
| `environmentTimeout` | string | - | How long Puppet caches environments. When unset, Puppet's default (`0` = no caching) applies |
| `environmentPath` | string | `/etc/puppetlabs/code/environments` | Path to Puppet environments |
| `hieraConfig` | string | `$confdir/hiera.yaml` | Path to Hiera configuration |
| `storeconfigs` | bool | `true` | Enable storeconfigs |
| `storeBackend` | string | `puppetdb` | Storeconfigs backend |
| `reports` | string | `puppetdb` | Report processors |
| `extraConfig` | [PuppetExtraConfig](#puppetextraconfig) | - | Additional puppet.conf entries per INI section |
| `autosignCommand` | string | - | Custom autosign executable path. When set, replaces the built-in binary and disables the SigningPolicy flow (see [Custom autosign / ENC commands](#custom-autosign--enc-commands)) |
| `externalNodesCommand` | string | - | Custom ENC (`external_nodes`) executable path. When set, replaces the built-in binary and disables the NodeClassifier flow (see [Custom autosign / ENC commands](#custom-autosign--enc-commands)) |

### Custom autosign / ENC commands

By default the operator ships two Go binaries in the server image and drives them
declaratively: `openvox-autosign` (configured by [SigningPolicy](signingpolicy.md)
resources) and `openvox-enc` (configured by a [NodeClassifier](nodeclassifier.md)).

`autosignCommand` and `externalNodesCommand` are escape hatches for teams that need
to run their own script instead - for example an autosign hook that registers the
node in an external inventory, or an ENC that queries a CMDB. When set:

- puppet.conf points `autosign` / `external_nodes` at the given executable.
- The corresponding built-in flow is disabled: the policy / ENC Secret is no longer
  rendered or mounted, and SigningPolicy / NodeClassifier resources are ignored.

The value must be an absolute path to an executable that already exists in the
server image or is mounted into the pod via the Server's
[`extraVolumes` / `extraVolumeMounts`](server.md). Any credentials the script needs
(client certificates, API tokens) are supplied the same way, via `extraVolumes` and
`extraEnv` / `envFrom`.

!!! warning
    Autosign is the certificate admission boundary - a command that signs
    unconditionally will sign every CSR. Review a custom `autosignCommand` as
    carefully as you would a firewall rule.

```yaml
spec:
  puppet:
    autosignCommand: /etc/puppetlabs/autosign/inventory-autosign
    externalNodesCommand: /etc/puppetlabs/enc/cmdb-enc
```

### PuppetExtraConfig

Additional puppet.conf entries grouped by INI section.

| Field | Type | Default | Description |
|---|---|---|---|
| `main` | map[string]string | - | Entries for the `[main]` section |
| `server` | map[string]string | - | Entries for the `[server]` section |
| `agent` | map[string]string | - | Entries for the `[agent]` section |

Example:

```yaml
puppet:
  extraConfig:
    main:
      environment: staging
    server:
      strict_variables: "true"
```

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

### Image resolution

`repository` and `tag` on a Server override the Config's values individually;
an unset field falls back to the Config. `pullPolicy` follows the same rule and
defaults to `IfNotPresent` when neither sets it. `pullSecrets` is different: a
non-empty list on the Server *replaces* the Config's rather than extending it,
so a Server pulling from another registry does not carry the Config's
credentials along. Secrets for code images are always added on top.

## Created Resources

| Resource | Name | Description |
|---|---|---|
| ConfigMap | `{name}-config` | puppet.conf, puppetserver.conf, auth.conf, webserver.conf, `routes.yaml` (facts terminus, when PuppetDB is the active backend), etc. |
| Secret | `{name}-enc` | ENC config for openvox-enc binary (only when `nodeClassifierRef` is set) |
| ServiceAccount | `{name}-server` | Shared ServiceAccount for all Server pods (`automountServiceAccountToken: false`) |
