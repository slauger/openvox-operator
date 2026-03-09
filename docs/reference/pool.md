# Pool

A Pool creates a Kubernetes Service that selects Server pods by label. Use Pools to expose Servers to agents - typically one Pool for the CA and one for catalog servers.

## Example

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Pool
metadata:
  name: puppet
spec:
  configRef: production
  selector:
    openvox.voxpupuli.org/role: server
  service:
    type: LoadBalancer
    port: 8140
```

## Spec

| Field | Type | Default | Description |
|---|---|---|---|
| `configRef` | string | **required** | Reference to the Config |
| `selector` | map[string]string | - | Label selector for Server pods. The config label is added automatically. If empty, selects all Servers in the Config. |
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
| `injectDNSAltName` | bool | `false` | Add hostname to Certificate dnsAltNames of matching Servers |

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

## Created Resources

| Resource | Name | Description |
|---|---|---|
| Service | `{name}` | Kubernetes Service selecting Server pods |
| TLSRoute | `{name}` | Gateway API TLSRoute (only when `route.enabled: true` and Gateway API CRDs are available) |

## Common Patterns

### Separate CA and Server Pools

```yaml
# CA pool - only CA servers
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Pool
metadata:
  name: puppet-ca
spec:
  configRef: production
  selector:
    openvox.voxpupuli.org/ca: "true"
  service:
    type: LoadBalancer
    port: 8140
---
# Server pool - all servers (including CA if it has server: true)
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Pool
metadata:
  name: puppet
spec:
  configRef: production
  selector:
    openvox.voxpupuli.org/role: server
  service:
    type: LoadBalancer
    port: 8140
```

### SNI-based Routing with Gateway API

Share a single LoadBalancer across environments using TLSRoute:

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Pool
metadata:
  name: puppet
spec:
  configRef: production
  selector:
    openvox.voxpupuli.org/role: server
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
