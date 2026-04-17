# Installation

## Prerequisites

- Kubernetes or OpenShift cluster
- Helm 3.x

### Optional Components

| Component | When needed |
|---|---|
| Kubernetes 1.35+ | OCI Image Volumes for [code deployment](../concepts/code-deployment.md) (1.31+ with `ImageVolume` feature gate) |
| cert-manager | Webhook TLS certificate automation |
| Gateway API CRDs | [TLSRoute](../concepts/gateway-api.md) support in Pool |
| CloudNativePG (CNPG) | Managed PostgreSQL for [Database](../reference/database.md) |

## Install via Helm (OCI)

The Helm chart is published as an OCI artifact to GitHub Container Registry.

```bash
helm install openvox-operator \
  oci://ghcr.io/slauger/charts/openvox-operator \
  --namespace openvox-system \
  --create-namespace
```

## Verify

```bash
kubectl get pods -n openvox-system
```

You should see the operator pod running:

```
NAME                                READY   STATUS    AGE
openvox-operator-7b8f9d6c4-x2k9m   1/1     Running   30s
```

## Namespace-Scoped Mode

By default the operator watches all namespaces (cluster-scoped). To restrict it to a single namespace:

```bash
helm install openvox-operator \
  oci://ghcr.io/slauger/charts/openvox-operator \
  --namespace openvox-system \
  --create-namespace \
  --set scope.mode=namespace \
  --set scope.watchNamespace=my-namespace
```

In namespace mode the operator uses Role/RoleBinding instead of ClusterRole/ClusterRoleBinding and only reconciles resources in the configured namespace.

## Next Steps

Once the operator is running, follow the [Quick Start](quickstart.md) guide to deploy an OpenVox stack. The Quick Start uses the [`openvox-stack`](https://ghcr.io/slauger/charts/openvox-stack) Helm chart which bundles all required custom resources into a single install command.
