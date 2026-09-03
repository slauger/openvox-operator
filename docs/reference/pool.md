# Pool

A Pool is a pure networking resource that creates a Kubernetes Service. [Servers](server.md) join a Pool by listing its name in their `poolRefs`. Use Pools to expose Servers to agents - typically one Pool for the CA and one for catalog servers.

## Example

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Pool
metadata:
  name: puppet
spec:
  service:
    type: LoadBalancer
    port: 8140
```

## Spec

| Field | Type | Default | Description |
|---|---|---|---|
| `service` | [PoolServiceSpec](#poolservicespec) | - | Kubernetes Service configuration |
| `route` | [PoolRouteSpec](#poolroutespec) | - | Gateway API TLSRoute configuration (see [Gateway API](../concepts/gateway-api.md)) |

### PoolServiceSpec

| Field | Type | Default | Description |
|---|---|---|---|
| `type` | string | `ClusterIP` | Service type (`ClusterIP`, `LoadBalancer`, `NodePort`) |
| `port` | int32 | `8140` | Service port |
| `nodePort` | int32 | - | Node port (for `NodePort` / `LoadBalancer`). Auto-assigned if unset. |
| `annotations` | map[string]string | - | Service annotations |
| `labels` | map[string]string | - | Additional Service labels |
| `externalIPs` | []string | - | External IPs for the Service |

### PoolRouteSpec

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Activates TLSRoute creation for this Pool |
| `hostname` | string | - | SNI hostname (required when enabled) |
| `gatewayRef` | [GatewayReference](#gatewayreference) | - | Gateway to attach the TLSRoute to (required when enabled) |
| `injectDNSAltName` | bool | `false` | Take this Pool's hostname into account for the certificates of Servers that reference it. The Certificate controller derives it into `status.effectiveDNSAltNames`; nothing is written to the Certificate spec. Adding it changes the signing-relevant state, so the certificate is re-signed and the TLS Secret briefly recreated. |

### GatewayReference

| Field | Type | Default | Description |
|---|---|---|---|
| `name` | string | **required** | Name of the Gateway |
| `sectionName` | string | - | Listener name on the Gateway |

## Status

| Field | Type | Description |
|---|---|---|
| `serviceName` | string | Name of the created Kubernetes Service |
| `endpoints` | int32 | Number of pods behind the Service |
| `observedGeneration` | int64 | The `.metadata.generation` the status was last derived from |
| `conditions` | []Condition | See below |

### Conditions

The `Ready` condition reports whether the Pool can actually carry traffic:

| Reason | Status | Meaning |
|---|---|---|
| `EndpointsAvailable` | True | At least one ready Server pod is behind the Service |
| `NoEndpoints` | False | The Service exists but no ready Server pod selects it |
| `HostnameConflict` | False | Another Pool holds `route.hostname`, so no TLSRoute is created |

A hostname can only be claimed by one Pool. When several ask for the same one,
the oldest Pool keeps it and the others report `HostnameConflict` naming the
holder. This is not retried: it resolves when the holder gives up the hostname
or is deleted, or when the losing Pool is given a different one.

## Created Resources

| Resource | Name | Description |
|---|---|---|
| Service | `{name}` | Kubernetes Service selecting Server pods |
| TLSRoute | `{name}` | Gateway API TLSRoute (only when `route.enabled: true` and Gateway API CRDs are available) |

## Common Patterns

### Separate CA and Server Pools

Servers declare pool membership via `poolRefs`. A CA server with both roles joins both pools:

```yaml
# Pools - pure networking, no selectors
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Pool
metadata:
  name: puppet-ca
spec:
  service:
    type: LoadBalancer
    port: 8140
---
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Pool
metadata:
  name: puppet
spec:
  service:
    type: LoadBalancer
    port: 8140
---
# CA server joins both pools
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Server
metadata:
  name: ca
spec:
  configRef: production
  certificateRef: production-cert
  poolRefs: [puppet-ca, puppet]
  ca: true
  server: true
```

### SNI-based Routing with Gateway API

Share a single LoadBalancer across environments using TLSRoute:

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Pool
metadata:
  name: puppet
spec:
  service:
    type: ClusterIP
    port: 8140
  route:
    enabled: true
    hostname: production.puppet.example.com
    gatewayRef:
      name: puppet-gateway
      sectionName: tls
    injectDNSAltName: true
```

See [Gateway API Integration](../concepts/gateway-api.md) for the full setup.
