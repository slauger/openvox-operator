# Quick Start

This guide sets up a minimal OpenVox Server environment with a single pod acting as both CA and server.

## Minimal Setup

Create an Environment and a Server:

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Environment
metadata:
  name: lab
spec:
  image:
    repository: ghcr.io/slauger/openvoxserver
    tag: "8.12.1"
  ca:
    autosign: "true"
---
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Server
metadata:
  name: puppet
spec:
  environmentRef: lab
  ca:
    enabled: true
    server: true
  replicas: 1
```

Apply it:

```bash
kubectl apply -f environment.yaml
```

The operator will:

1. Create a CA setup Job to initialize the Certificate Authority
2. Store CA certificates in a Kubernetes Secret
3. Create a Deployment for the Server pod
4. Create a ClusterIP Service for the CA

## Verify

```bash
kubectl get environment,server
```

```
NAME                                     CA READY   AGE
environment.openvox.voxpupuli.org/lab    true       2m

NAME                                     ENVIRONMENT   REPLICAS   READY
server.openvox.voxpupuli.org/puppet      lab           1          1
```

## Next Steps

See the [Examples](../examples/index.md) section for production setups with separate CA, server pools, canary deployments, and code deployment via r10k.
