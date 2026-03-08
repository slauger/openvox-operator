# Installation

!!! warning "Work in Progress"
    The operator is under active development. CRDs and APIs may change.

## Prerequisites

- Kubernetes 1.28+ or OpenShift 4.14+
- Helm 3.x

## Install via Helm (OCI)

The Helm chart is published as an OCI artifact to GitHub Container Registry.

```bash
helm install openvox-operator \
  oci://ghcr.io/slauger/charts/openvox-operator \
  --version 0.1.0 \
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
