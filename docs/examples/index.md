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
  poolRefs: [puppet]
  ca: true
  server: true
  replicas: 1
---
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Pool
metadata:
  name: puppet
spec:
  service:
    port: 8140
```

## Production - CA + Server Pool + Canary + Database

Separate CA server, a stable server pool with 3 replicas, a canary server running a newer version, and an OpenVox DB instance backed by external PostgreSQL. Pools distribute traffic across matching servers; the Config wires the Database connection automatically via `databaseRef`.

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Config
metadata:
  name: production
spec:
  authorityRef: production-ca
  databaseRef: production-db
  image:
    repository: ghcr.io/slauger/openvox-server
    tag: "8.12.1"
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
kind: Certificate
metadata:
  name: production-db-cert
spec:
  authorityRef: production-ca
  certname: production-db
  dnsAltNames:
    - production-db
    - production-db.openvox.svc.cluster.local
---
apiVersion: v1
kind: Secret
metadata:
  name: pg-credentials
type: Opaque
stringData:
  username: openvoxdb
  password: change-me  # provision externally (e.g. via SealedSecrets, ESO, Vault)
---
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Database
metadata:
  name: production-db
spec:
  certificateRef: production-db-cert
  image:
    repository: ghcr.io/slauger/openvox-db
    tag: latest
  postgres:
    host: pg-rw.openvox.svc        # CloudNativePG read/write Service
    port: 5432
    database: openvoxdb
    credentialsSecretRef: pg-credentials
    sslMode: require
  replicas: 2
  pdb:
    enabled: true
    minAvailable: 1
  resources:
    requests:
      cpu: 500m
      memory: 1Gi
    limits:
      memory: 2Gi
---
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: ReportProcessor
metadata:
  name: production-reports
spec:
  configRef: production
  processor: puppetdb
  url: https://production-db:8081
  auth:
    mtls: true
---
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Pool
metadata:
  name: production-ca
spec:
  service:
    port: 8140
---
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Pool
metadata:
  name: puppet
spec:
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
  poolRefs: [production-ca, puppet]
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
  poolRefs: [puppet]
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
  poolRefs: [puppet]
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
