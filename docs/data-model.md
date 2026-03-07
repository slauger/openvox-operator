# Data Model - CRD Design

## API Group

`openvox.voxpupuli.org/v1alpha1`

## Overview

| Kind | Responsibility | Creates K8s Resources |
|---|---|---|
| `Environment` | Shared config, CA lifecycle, PuppetDB connection | ConfigMaps, CA Job, CA Secret, CA PVC, CA Service |
| `Pool` | Owns a K8s Service | Service |
| `Server` | OpenVox Server instance pool | Deployment, HPA |
| `CodeDeploy` | r10k code deployment from Git | PVC, Job, CronJob |
| `Database` | OpenVoxDB *(future)* | StatefulSet, Service |

## Relationships

```
Environment <-- Server (environmentRef)
Environment <-- Pool (environmentRef)
Environment <-- CodeDeploy (environmentRef)
Pool        <-- Server (poolRef)
```

Multiple Servers can reference the same Environment and the same Pool.
An Environment can be referenced by any number of Servers, Pools, and CodeDeploys.

---

## 1. `Environment`

The umbrella for a Puppet setup. Manages the CA, shared configuration, and PuppetDB connection.

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Environment
metadata:
  name: production
spec:
  # Default image for all Servers in this Environment (overridable per Server)
  image:
    repository: ghcr.io/slauger/openvoxserver
    tag: "8.12.1"

  # CA configuration
  ca:
    certname: puppet
    dnsAltNames:
      - puppet
      - puppet-ca
      - puppet-ca.openvox.svc
    ttl: 157680000                     # 5 years
    allowSubjectAltNames: true
    autosign: "true"                   # true/false/script-path
    storage:
      size: 1Gi
      storageClass: ""                 # empty = default StorageClass
    # Optional: Intermediate CA
    # intermediateCA:
    #   secretName: my-intermediate-ca

  # PuppetDB connection (externally provided)
  puppetdb:
    serverUrls:
      - https://openvoxdb:8081

  # Shared puppet.conf settings
  puppet:
    environmentTimeout: unlimited
    environmentPath: /etc/puppetlabs/code/environments
    hieraConfig: "$confdir/hiera.yaml"
    storeconfigs: true
    storeBackend: puppetdb
    reports: puppetdb
    extraConfig: {}                    # arbitrary key=value pairs

status:
  phase: Running                       # Pending | CASetup | Running | Error
  caReady: true
  caSecretName: production-ca-certs    # auto-generated
  caServiceName: production-ca         # auto-generated (internal ClusterIP)
  conditions:
    - type: CAInitialized
      status: "True"
    - type: ConfigReady
      status: "True"
```

**Creates**:
- CA Setup Job (one-time, `puppetserver ca setup`)
- CA Secret (`<name>-ca-certs`: ca_crt.pem, ca_key.pem, ca_crl.pem)
- CA PVC (`<name>-ca-data`)
- CA Service (`<name>-ca`, internal ClusterIP, always created - no Pool needed)
- ConfigMaps (`<name>-puppet-conf`, `<name>-puppetdb-conf`, `<name>-webserver-conf`, etc.)

---

## 2. `Pool`

Owns a Kubernetes Service. Solves the ownership problem when multiple Servers share the same Service.

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Pool
metadata:
  name: puppet
spec:
  environmentRef: production

  service:
    type: LoadBalancer               # ClusterIP | LoadBalancer | NodePort
    port: 8140
    annotations:
      service.beta.kubernetes.io/aws-load-balancer-type: nlb
    labels:
      team: platform

status:
  serviceName: puppet                # the created K8s Service
  endpoints: 4                       # number of Pods behind the Service
```

**Creates**:
- Kubernetes Service (`<name>:8140`)
- The Service selects all Server Pods referencing this Pool via label `openvox.voxpupuli.org/pool: <name>`

**Lifecycle**: Deleting a Server keeps the Service. Deleting the Pool removes the Service.

---

## 3. `Server`

A pool of OpenVox Server instances. Can run as CA, compiler, or both.

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Server
metadata:
  name: stable
spec:
  environmentRef: production         # required: belongs to this Environment
  poolRef: puppet                    # optional: joins this Pool/Service

  # Image override (otherwise uses Environment default)
  image:
    tag: "8.12.1"

  # CA role
  ca:
    enabled: false                   # true = CA service enabled, mounts CA data
    compiler: false                  # true = CA also participates in Pool Service

  replicas: 3
  autoscaling:
    enabled: false
    minReplicas: 1
    maxReplicas: 10
    targetCPU: 75

  resources:
    requests: { memory: "1Gi", cpu: "500m" }
    limits:   { memory: "2Gi" }
  javaArgs: "-Xms512m -Xmx1024m"
  maxActiveInstances: 2              # JRuby instances per Pod

  # Optional: additional DNS alt names for the server certificate
  dnsAltNames:
    - puppet
    - puppet.openvox.svc

status:
  phase: Running                     # Pending | WaitingForCA | Running | Error
  ready: 3
  desired: 3
  conditions:
    - type: SSLBootstrapped
      status: "True"
    - type: Ready
      status: "True"
```

**Creates**:
- Deployment with `Recreate` strategy (when `ca.enabled: true`) or `RollingUpdate` strategy (when `ca.enabled: false`). CA uses Recreate to ensure only one pod writes to the CA data PVC at a time. Compilers share a cert from a Secret.
- HPA (when `autoscaling.enabled`)
- InitContainer for SSL bootstrap against CA Service (when `ca.enabled: false`)

**Pod Labels**:
- `openvox.voxpupuli.org/environment: production`
- `openvox.voxpupuli.org/pool: puppet` (when poolRef is set)
- `openvox.voxpupuli.org/server: stable`
- `openvox.voxpupuli.org/role: compiler` or `ca`

**CA Server + Compiler Combo** (small setups):
```yaml
kind: Server
metadata:
  name: puppet
spec:
  environmentRef: lab
  poolRef: puppet
  ca:
    enabled: true
    compiler: true      # Pods also get the Pool label -> join the Service
  replicas: 1
```

---

## 4. `CodeDeploy`

r10k code deployment from Git. Independent of Server - manages a PVC that Servers mount.

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: CodeDeploy
metadata:
  name: control-repo
spec:
  environmentRef: production

  image:
    repository: ghcr.io/slauger/r10k
    tag: "latest"

  sources:
    - name: puppet
      remote: https://github.com/example/control-repo.git
      basedir: /etc/puppetlabs/code/environments

  # Git authentication (optional)
  # gitSecret: r10k-git-credentials

  schedule: "*/5 * * * *"           # CronJob for periodic updates

  volume:
    size: 5Gi
    accessMode: ReadWriteOnce        # ReadWriteOnce or ReadWriteMany
    # storageClass: ""
    # existingClaim: ""              # use existing PVC

status:
  phase: Ready                       # Pending | Deploying | Ready | Error
  lastDeployTime: "2026-03-07T22:00:00Z"
  pvcName: control-repo-code         # auto-generated
  conditions:
    - type: VolumeReady
      status: "True"
    - type: LastDeploySucceeded
      status: "True"
```

**Creates**:
- PVC (`<name>-code`)
- Job (initial deploy)
- CronJob (periodic sync per `schedule`)

### Code Volume Strategy

Servers automatically mount the code PVC when a `CodeDeploy` in the same Environment exists.

**Single-Node (Default): RWO + Pod-Affinity**

RWO allows multiple Pods on the **same Node**. The operator sets pod affinity so the r10k Job and Server Pods run on the same node.

```
Node A:
  +-- r10k CronJob      --- mount RWO PVC (read-write)
  +-- Server Pod 1       -- mount RWO PVC (read-only)
  +-- Server Pod 2       -- mount RWO PVC (read-only)
```

**Multi-Node: RWX**

When Servers are distributed across multiple nodes, `accessMode: ReadWriteMany` is required. Needs an RWX-capable storage provider (NFS, CephFS, EFS, Longhorn).

| Setup | accessMode | Requirement |
|---|---|---|
| Single-Node | `ReadWriteOnce` | Any storage provider |
| Multi-Node | `ReadWriteMany` | NFS, CephFS, EFS, Longhorn |

---

## 5. `Database` *(future)*

For managing OpenVoxDB via the operator.

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Database
metadata:
  name: openvoxdb
spec:
  environmentRef: production
  image:
    repository: ghcr.io/slauger/openvoxdb
    tag: "8.12.1"
  postgresql:
    host: postgres.db.svc
    port: 5432
    database: puppetdb
    credentialsSecret: puppetdb-postgres-credentials
  storage:
    size: 10Gi
```

Currently **out of scope** - PuppetDB and PostgreSQL are provided externally via `Environment.spec.puppetdb`. The data model is prepared for future use.

---

## Reference Resolution

The Server controller resolves references during each reconciliation:

```
Server.spec.environmentRef: "production"
  -> Environment "production"
    -> status.caSecretName: "production-ca-certs"    (CA Secret to mount)
    -> status.caServiceName: "production-ca"          (for SSL bootstrap)
    -> ConfigMaps: "production-puppet-conf", etc.     (to mount)

Server.spec.poolRef: "puppet"
  -> Pool "puppet"
    -> Label "openvox.voxpupuli.org/pool: puppet"     (for Service selection)

CodeDeploy in the same Environment:
  -> CodeDeploy "control-repo"
    -> status.pvcName: "control-repo-code"            (code PVC to mount)
```

---

## Full Example

```yaml
---
# 1. Environment: Shared Config + CA
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Environment
metadata:
  name: production
  namespace: openvox
spec:
  image: { repository: ghcr.io/slauger/openvoxserver, tag: "8.12.1" }
  ca:
    certname: puppet
    dnsAltNames: [puppet, puppet-ca]
    autosign: "true"
    storage: { size: 1Gi }
  puppetdb:
    serverUrls: ["https://openvoxdb:8081"]
  puppet:
    environmentTimeout: unlimited
    storeconfigs: true
    reports: puppetdb
---
# 2. Pool: Owns the Service for agents
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Pool
metadata:
  name: puppet
  namespace: openvox
spec:
  environmentRef: production
  service:
    type: LoadBalancer
    port: 8140
---
# 3. Server: CA (1 replica, no Pool)
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Server
metadata:
  name: ca
  namespace: openvox
spec:
  environmentRef: production
  ca: { enabled: true }
  replicas: 1
  javaArgs: "-Xms512m -Xmx1024m"
---
# 4. Server: Stable compiler pool
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Server
metadata:
  name: stable
  namespace: openvox
spec:
  environmentRef: production
  poolRef: puppet
  replicas: 3
  maxActiveInstances: 2
  javaArgs: "-Xms512m -Xmx1024m"
---
# 5. Server: Canary (newer version, same Pool)
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Server
metadata:
  name: canary
  namespace: openvox
spec:
  environmentRef: production
  poolRef: puppet
  image: { tag: "8.13.0" }
  replicas: 1
  javaArgs: "-Xms512m -Xmx1024m"
---
# 6. CodeDeploy: r10k
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: CodeDeploy
metadata:
  name: control-repo
  namespace: openvox
spec:
  environmentRef: production
  image: { repository: ghcr.io/slauger/r10k, tag: "latest" }
  sources:
    - name: puppet
      remote: https://github.com/example/control-repo.git
      basedir: /etc/puppetlabs/code/environments
  schedule: "*/5 * * * *"
  volume: { size: 5Gi }
```

### kubectl Output

```
$ kubectl -n openvox get environment,pool,server,codedeploy

NAME                                          CA READY   AGE
environment.openvox.voxpupuli.org/production  true       1h

NAME                                 TYPE           AGE
pool.openvox.voxpupuli.org/puppet    LoadBalancer   1h

NAME                                  ENVIRONMENT   POOL     IMAGE    REPLICAS   READY
server.openvox.voxpupuli.org/ca       production             8.12.1   1          1
server.openvox.voxpupuli.org/stable   production    puppet   8.12.1   3          3
server.openvox.voxpupuli.org/canary   production    puppet   8.13.0   1          1

NAME                                            ENVIRONMENT   SCHEDULE      LAST DEPLOY
codedeploy.openvox.voxpupuli.org/control-repo   production    */5 * * * *   2m ago
```
