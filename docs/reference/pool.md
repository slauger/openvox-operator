# Pool

A Pool creates a Kubernetes Service that selects Server pods by label. Use Pools to expose Servers to agents - typically one Pool for the CA and one for catalog servers.

## Example

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Pool
metadata:
  name: puppet
spec:
  environmentRef: production
  selector:
    openvox.voxpupuli.org/role: server
  service:
    type: LoadBalancer
    port: 8140
```

## Spec

| Field | Type | Default | Description |
|---|---|---|---|
| `environmentRef` | string | **required** | Reference to the Environment |
| `selector` | map[string]string | - | Label selector for Server pods. The environment label is added automatically. If empty, selects all Servers in the Environment. |
| `service` | [PoolServiceSpec](#poolservicespec) | - | Kubernetes Service configuration |

### PoolServiceSpec

| Field | Type | Default | Description |
|---|---|---|---|
| `type` | string | `ClusterIP` | Service type (`ClusterIP`, `LoadBalancer`, `NodePort`) |
| `port` | int32 | `8140` | Service port |
| `nodePort` | int32 | - | Node port (for `NodePort` / `LoadBalancer`). Auto-assigned if unset. |
| `annotations` | map[string]string | - | Service annotations |
| `labels` | map[string]string | - | Additional Service labels |
| `externalIPs` | []string | - | External IPs for the Service |

## Status

| Field | Type | Description |
|---|---|---|
| `serviceName` | string | Name of the created Kubernetes Service |
| `endpoints` | int32 | Number of pods behind the Service |

## Created Resources

| Resource | Name | Description |
|---|---|---|
| Service | `{name}` | Kubernetes Service selecting Server pods |

## Common Patterns

### Separate CA and Server Pools

```yaml
# CA pool - only CA servers
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Pool
metadata:
  name: puppet-ca
spec:
  environmentRef: production
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
  environmentRef: production
  selector:
    openvox.voxpupuli.org/role: server
  service:
    type: LoadBalancer
    port: 8140
```
