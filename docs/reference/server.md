# Server

A Server creates a Deployment of OpenVox Server pods. It references a Certificate for SSL and an Environment for shared configuration. A Server can run as CA, as a catalog server, or both.

## Example

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Server
metadata:
  name: production-ca
spec:
  environmentRef: production
  certificateRef: production-cert
  ca: true
  server: true
  replicas: 1
  javaArgs: "-Xms1g -Xmx2g"
  maxActiveInstances: 4
```

## Spec

| Field | Type | Default | Description |
|---|---|---|---|
| `environmentRef` | string | **required** | Reference to the Environment |
| `certificateRef` | string | **required** | Reference to the Certificate whose SSL Secret is mounted |
| `image` | [ImageSpec](index.md#imagespec) | - | Override the Environment's default image |
| `ca` | bool | `false` | Enable CA role (mounts CA PVC) |
| `server` | bool | `true` | Enable server role (catalog compilation, file serving) |
| `replicas` | int32 | `1` | Number of pod replicas |
| `autoscaling` | [AutoscalingSpec](#autoscalingspec) | - | HPA configuration |
| `resources` | ResourceRequirements | - | CPU/memory requests and limits |
| `javaArgs` | string | `-Xms512m -Xmx1024m` | JVM arguments |
| `maxActiveInstances` | int32 | `2` | Number of JRuby instances per pod |
| `code` | [CodeSpec](index.md#codespec) | - | Override the Environment's code volume |

### AutoscalingSpec

| Field | Type | Default | Description |
|---|---|---|---|
| `enabled` | bool | `false` | Activate HPA |
| `minReplicas` | int32 | `1` | Minimum replicas |
| `maxReplicas` | int32 | `5` | Maximum replicas |
| `targetCPU` | int32 | `75` | Target CPU utilization percentage |

## Status

| Field | Type | Description |
|---|---|---|
| `phase` | string | Current lifecycle phase |
| `ready` | int32 | Number of ready replicas |
| `desired` | int32 | Desired number of replicas |
| `conditions` | []Condition | `SSLBootstrapped`, `Ready` |

## Phases

| Phase | Description |
|---|---|
| `Pending` | Server created, resolving references |
| `WaitingForCert` | Certificate not yet `Signed` |
| `Running` | Deployment created and running |
| `Error` | Reconciliation failed |

## Deployment Strategy

| Role | Strategy | Reason |
|---|---|---|
| CA (`ca: true`) | `Recreate` | Only one pod can write to the CA PVC at a time |
| Server only | `RollingUpdate` | Zero-downtime updates for stateless catalog compilation |

## Created Resources

| Resource | Name | Description |
|---|---|---|
| Deployment | `{name}` | OpenVox Server pods |
| HPA | `{name}` | Only when `autoscaling.enabled: true` |
