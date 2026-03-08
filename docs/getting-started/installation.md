# Installation

!!! warning "Work in Progress"
    The operator is under active development. CRDs and APIs may change.

## Prerequisites

- Kubernetes 1.28+ or OpenShift 4.14+
- Helm 3.x

## Install via Helm

```bash
helm repo add openvox-operator https://slauger.github.io/openvox-operator
helm repo update

helm install openvox-operator openvox-operator/openvox-operator \
  --namespace openvox-system \
  --create-namespace
```

This installs the operator and its CRDs into the `openvox-system` namespace.

## Verify

```bash
kubectl get pods -n openvox-system
```

You should see the operator pod running:

```
NAME                                READY   STATUS    AGE
openvox-operator-7b8f9d6c4-x2k9m   1/1     Running   30s
```
