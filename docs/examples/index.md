# Examples

## Minimal - Single Pod

A single Server with both CA and server role enabled. Suitable for development and lab environments.

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

## Production - CA + Server Pool + Canary

Separate CA server, a stable server pool with 3 replicas, and a canary server running a newer version. Pools distribute traffic across matching servers.

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Config
metadata:
  name: production
spec:
  authorityRef: production-ca
  image:
    repository: ghcr.io/slauger/openvox-server
    tag: "8.12.1"
  puppetdb:
    serverUrls:
      - https://openvoxdb:8081
  puppet:
    environmentTimeout: unlimited
    storeconfigs: true
    storeBackend: puppetdb
    reports: puppetdb
---
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: CertificateAuthority
metadata:
  name: production-ca
spec:
  storage:
    size: 1Gi
---
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: SigningPolicy
metadata:
  name: production-autosign
spec:
  certificateAuthorityRef: production-ca
  any: true
---
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Certificate
metadata:
  name: ca-cert
spec:
  authorityRef: production-ca
  certname: puppet
  dnsAltNames:
    - puppet
    - production-ca
---
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Certificate
metadata:
  name: stable-cert
spec:
  authorityRef: production-ca
  certname: puppet
  dnsAltNames:
    - puppet
---
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Certificate
metadata:
  name: canary-cert
spec:
  authorityRef: production-ca
  certname: puppet
  dnsAltNames:
    - puppet
---
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Pool
metadata:
  name: production-ca
spec:
  configRef: production
  selector:
    openvox.voxpupuli.org/ca: "true"
  service:
    port: 8140
---
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
---
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Server
metadata:
  name: ca
spec:
  configRef: production
  certificateRef: ca-cert
  ca: true
  server: true
  replicas: 1
  resources:
    requests:
      cpu: 500m
      memory: 1Gi
    limits:
      cpu: "2"
      memory: 2Gi
---
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Server
metadata:
  name: stable
spec:
  configRef: production
  certificateRef: stable-cert
  replicas: 3
  maxActiveInstances: 2
  code:
    claimName: puppet-code
  resources:
    requests:
      cpu: "1"
      memory: 2Gi
    limits:
      cpu: "4"
      memory: 4Gi
---
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Server
metadata:
  name: canary
spec:
  configRef: production
  certificateRef: canary-cert
  image:
    tag: "8.13.0"
  replicas: 1
  code:
    claimName: puppet-code
  resources:
    requests:
      cpu: "1"
      memory: 2Gi
    limits:
      cpu: "4"
      memory: 4Gi
```
