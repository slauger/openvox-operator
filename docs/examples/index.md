# Examples

## Minimal - Single Pod

A single Server with CA enabled that handles both CA operations and catalog compilation. Suitable for development and lab environments.

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

## Production - CA + Server Pool + Canary

Separate CA server, a stable compiler pool with 3 replicas, and a canary pool running a newer version. All compilers join the same Pool and share a LoadBalancer Service.

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Environment
metadata:
  name: production
spec:
  image:
    repository: ghcr.io/slauger/openvoxserver
    tag: "8.12.1"
  ca:
    certname: puppet
    dnsAltNames: [puppet, puppet-ca]
    ttl: 157680000
    storage:
      size: 1Gi
  puppetdb:
    serverUrls: ["https://openvoxdb:8081"]
  puppet:
    environmentTimeout: unlimited
    storeconfigs: true
    reports: puppetdb
---
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Pool
metadata:
  name: puppet
spec:
  environmentRef: production
  service:
    type: LoadBalancer
    port: 8140
---
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Server
metadata:
  name: ca
spec:
  environmentRef: production
  ca:
    enabled: true
  replicas: 1
  javaArgs: "-Xms512m -Xmx1024m"
---
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Server
metadata:
  name: stable
spec:
  environmentRef: production
  poolRef: puppet
  image:
    tag: "8.12.1"
  replicas: 3
  maxActiveInstances: 2
  javaArgs: "-Xms512m -Xmx1024m"
---
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Server
metadata:
  name: canary
spec:
  environmentRef: production
  poolRef: puppet
  image:
    tag: "8.13.0"
  replicas: 1
  javaArgs: "-Xms512m -Xmx1024m"
```

## CodeDeploy - r10k from Git

Deploy Puppet code from a Git control repository using r10k. The CodeDeploy CRD manages a Job for initial deployment and a CronJob for periodic syncs.

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
  schedule: "*/5 * * * *"
  volume:
    size: 5Gi
```
