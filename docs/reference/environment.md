# Environment

An Environment holds shared configuration for all Servers: the default container image, puppet.conf settings, PuppetDB connection, and an optional code volume. It is the root resource in the CRD hierarchy.

## Example

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Environment
metadata:
  name: production
spec:
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
  code:
    claimName: puppet-code
```

## Spec

| Field | Type | Default | Description |
|---|---|---|---|
| `image` | [ImageSpec](index.md#imagespec) | **required** | Default container image for all Servers |
| `ca` | [CASpec](#caspec) | - | CA configuration defaults |
| `puppet` | [PuppetSpec](#puppetspec) | - | Shared puppet.conf settings |
| `puppetdb` | [PuppetDBSpec](#puppetdbspec) | - | PuppetDB connection settings |
| `code` | [CodeSpec](index.md#codespec) | - | PVC for Puppet code (environments directory) |

### CASpec

| Field | Type | Default | Description |
|---|---|---|---|
| `ttl` | int64 | `157680000` (5 years) | CA certificate TTL in seconds |
| `allowSubjectAltNames` | bool | `true` | Allow SANs in CSRs |
| `autosign` | string | `"true"` | Autosigning: `"true"`, `"false"`, or path to script |
| `storage` | [StorageSpec](index.md#storagespec) | - | PVC settings for CA data |
| `intermediateCA` | [IntermediateCASpec](#intermediatecaspec) | - | Intermediate CA configuration |

### IntermediateCASpec

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Activate intermediate CA mode |
| `secretName` | string | - | Secret containing ca.pem, key.pem, crl.pem |

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
| `Pending` | Environment created, waiting for reconciliation |
| `Running` | ConfigMap created, ready for use |
| `Error` | Reconciliation failed |

## Created Resources

| Resource | Name | Description |
|---|---|---|
| ConfigMap | `{name}` | puppet.conf, puppetserver.conf, auth.conf, webserver.conf, etc. |
| ServiceAccount | `{name}-server` | Shared ServiceAccount for all Server pods (`automountServiceAccountToken: false`) |
