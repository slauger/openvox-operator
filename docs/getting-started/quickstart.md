# Quick Start

This guide sets up a minimal OpenVox Server deployment with a single pod acting as both CA and server.

## Minimal Setup

Create a Config, CertificateAuthority, SigningPolicy, Certificate, Server, and Pool:

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Config
metadata:
  name: lab
spec:
  authorityRef: lab-ca
  image:
    repository: ghcr.io/slauger/openvox-server
    tag: "8.12.1"
---
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: CertificateAuthority
metadata:
  name: lab-ca
---
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: SigningPolicy
metadata:
  name: lab-autosign
spec:
  certificateAuthorityRef: lab-ca
  any: true
---
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Certificate
metadata:
  name: lab-cert
spec:
  authorityRef: lab-ca
  certname: puppet
  dnsAltNames:
    - puppet
    - lab-ca
---
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Server
metadata:
  name: puppet
spec:
  configRef: lab
  certificateRef: lab-cert
  ca: true
  server: true
  replicas: 1
---
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Pool
metadata:
  name: puppet
spec:
  configRef: lab
  selector:
    openvox.voxpupuli.org/ca: "true"
  service:
    port: 8140
```

Apply it:

```bash
kubectl apply -f config.yaml
```

The operator will:

1. Create a ConfigMap with puppet configuration
2. Initialize the Certificate Authority (PVC, CA setup Job, CA Secret)
3. Sign a certificate for the Server (cert setup Job, SSL Secret)
4. Create a Deployment for the Server pod
5. Create a Service via the Pool

## Verify

```bash
kubectl get config,certificateauthority,signingpolicy,certificate,server,pool
```

```
NAME                                        CA       PHASE     AGE
config.openvox.voxpupuli.org/lab            lab-ca   Running   2m

NAME                                                PHASE   AGE
certificateauthority.openvox.voxpupuli.org/lab-ca   Ready   2m

NAME                                                     CA       PHASE    AGE
signingpolicy.openvox.voxpupuli.org/lab-autosign         lab-ca   Active   2m

NAME                                              AUTHORITY   CERTNAME   PHASE    AGE
certificate.openvox.voxpupuli.org/lab-cert        lab-ca      puppet     Signed   2m

NAME                                        CONFIG   REPLICAS   READY   PHASE     AGE
server.openvox.voxpupuli.org/puppet         lab      1          1       Running   2m

NAME                                        CONFIG   TYPE        ENDPOINTS   AGE
pool.openvox.voxpupuli.org/puppet           lab      ClusterIP   1           2m
```

## Next Steps

See the [Examples](../examples/index.md) section for production setups with separate CA, server pools, canary deployments, and code deployment via OCI image volumes.
