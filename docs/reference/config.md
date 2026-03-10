# Config

A Config holds shared configuration for all Servers: the default container image, puppet.conf settings, and PuppetDB connection. It is the root resource in the CRD hierarchy. The `authorityRef` field references a CertificateAuthority; CA settings (`ca_ttl`, `autosign`) are automatically pulled from it.

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
      - "https://puppetdb.example.com:8081"
```

## Spec

| Field | Type | Default | Description |
|---|---|---|---|
| `image` | [ImageSpec](index.md#imagespec) | **required** | Default container image for all Servers |
| `authorityRef` | string | - | Reference to the CertificateAuthority used by this Config |
| `nodeClassifierRef` | string | - | Reference to a [NodeClassifier](nodeclassifier.md) for ENC support |
| `puppet` | [PuppetSpec](#puppetspec) | - | Shared puppet.conf settings |
| `puppetdb` | [PuppetDBSpec](#puppetdbspec) | - | PuppetDB connection settings |

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
| `serverUrls` | []string | - | PuppetDB server URLs |

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
