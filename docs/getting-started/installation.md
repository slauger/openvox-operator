# Installation

!!! warning "Work in Progress"
    The operator is under active development. CRDs and APIs may change.

## Prerequisites

- Kubernetes or OpenShift cluster
- Helm 3.x

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

## Next Steps

Once the operator is running, follow the [Quick Start](quickstart.md) guide to deploy an OpenVox stack.
