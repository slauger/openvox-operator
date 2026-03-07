# OpenVox Operator - Architecture & Kubernetes Design

## Context

### Problems with Existing Container Approaches

1. **ezbake Legacy**: Upstream OpenVox Server uses ezbake for packaging. Generates init scripts that start as root and switch to the puppet user via `runuser`/`su`/`sudo`. Breaks rootless containers and OpenShift random UIDs.

2. **Duplicate Ruby Installation**: The server needs JRuby (embedded in the JAR) for runtime. Existing containers additionally install System Ruby + openvox gem just so the entrypoint.d scripts can call `puppet config set/print`. This is unnecessary - those scripts only write INI config files that come via ConfigMap in K8s.

3. **Docker Logic in K8s Context**: ~15 entrypoint.d scripts translate ENV vars into config files. This is a Docker pattern. In K8s the operator handles this via ConfigMaps/Secrets.

4. **No Role Separation**: The container has to decide itself whether to run as CA or compiler. In K8s the operator handles orchestration.

5. **chown/chmod Issues**: The `openvoxserver-ca` gem calls `FileUtils.chown` - fails rootless. Needs patching. Better: propose upstream fix in openvox (consistently implement `manage_internal_file_permissions`).

### Potential Upstream Improvements in OpenVox

- `openvoxserver-ca` gem: `chown`/`lchown` calls should respect `manage_internal_file_permissions`
- `symlink_to_old_cadir` should be optional/configurable
- Tarball should include puppet gem dependencies for JRuby completely (currently `puppetserver gem install openvox` needs to run separately)

## CRD Design

See [data-model.md](data-model.md) for the full multi-CRD data model (Environment, Pool, Server, CodeDeploy, Database).

## Components in Detail

### 1. Operator (Controller)

- **Language**: Go (kubebuilder/controller-runtime)
- **Reconciliation Loop**:
  1. Reads the CRs (Environment, Pool, Server, CodeDeploy)
  2. Generates ConfigMaps from `Environment.spec.puppet.*` (puppet.conf, puppetdb.conf, webserver.conf, etc.)
  3. If CA not yet initialized -> creates CA Setup Job
  4. Waits for Job completion -> reads CA certificates -> creates CA Secret
  5. Creates CA StatefulSet (replicas: 1, PVC for CA data)
  6. Waits for CA readiness (status endpoint)
  7. Creates compiler Deployments (replicas: N, CA Secret mounted)
  8. Compiler Pods bootstrap via `puppet ssl bootstrap` against the CA Server

### 2. CA Setup Job

A one-time Job that runs `puppetserver ca setup` and stores the CA certificates in a PVC.

### 3. CA Server (StatefulSet, replicas: 1)

- Mounts CA data PVC
- Mounts puppet.conf, puppetdb.conf, webserver.conf as ConfigMap
- CA Service enabled (`ca.cfg`)
- No System Ruby needed - config comes via ConfigMap
- Entrypoint: direct `java` (no entrypoint.d)
- Liveness/Readiness: TCP check on port 8140

### 4. Compiler (Deployment, replicas: N)

- CA Service disabled
- Mounts CA cert from Secret (read-only)
- InitContainer: `puppet ssl bootstrap --server puppet-ca`
- Code via PVC or r10k initContainer
- Entrypoint: direct `java`
- HPA support

### 5. Code Sync (r10k)

- **r10k Job/CronJob**: A single Job that writes `r10k deploy environment` to the code PVC
- **Separate image**: only Ruby + r10k, no openvox-server
- All compilers mount the same code PVC read-only

#### Code Volume Strategy

**Default: RWO PVC + Pod-Affinity**

RWO (ReadWriteOnce) allows multiple Pods on the **same node** simultaneously. The operator sets pod affinity so the r10k Job and compiler Pods run on the same node. This works without a special storage provider.

```
Node A:
  +-- r10k Job       --- mount RWO PVC (read-write)
  +-- Compiler Pod 1  -- mount RWO PVC (read-only)
  +-- Compiler Pod 2  -- mount RWO PVC (read-only)
```

**Multi-Node: RWX PVC**

When compilers are distributed across multiple nodes (anti-affinity, node failure, large clusters), RWX (ReadWriteMany) is needed.

| Setup | Access Mode | Requirement |
|-------|-------------|-------------|
| Single-Node (Default) | RWO | None - any storage provider |
| Multi-Node | RWX | NFS, CephFS, EFS, Longhorn, etc. |

## cert-manager Integration

Puppet uses **standard X.509 certificates** (RSA/EC keys, PEM format). The CA is a regular X.509 CA certificate. cert-manager **could** create the root CA.

**However**: Puppet CSRs have custom OID extensions (`1.3.6.1.4.1.34380.*` - pp_uuid, pp_instance_id, pp_auth_role). These are processed by the Puppet CA Service during signing. cert-manager cannot generate these.

| Approach | Advantage | Disadvantage |
|----------|-----------|--------------|
| **Puppet CA standalone** (Default) | Simple, works out-of-the-box | Own CA management needed |
| **cert-manager Root -> Puppet Intermediate** | Root CA lifecycle via cert-manager | More complex setup |
| **cert-manager for Server TLS, Puppet CA for Agents** | Separation of concerns | Two PKI systems |

**Recommendation**: Puppet CA standalone as default. cert-manager integration as optional feature via `spec.ca.intermediateCA`.

## Container Image (K8s-first)

### Removed

- All entrypoint.d scripts (config comes via ConfigMap)
- System Ruby openvox gem (no more `puppet config set/print` needed)
- Gemfile / bundle install / ruby-devel / gcc / make
- All ENV var -> config translation logic
- Docker-Compose support

### Included

- UBI9 + JDK 17
- Tarball installation (puppet-server-release.jar, CLI tools, vendored JRuby gems)
- PuppetDB termini
- `puppetserver gem install openvox` (JRuby)
- openvoxserver-ca patch (chown/symlink)
- OpenShift random-UID pattern (chgrp 0, chmod g=u, SGID)
- Slim entrypoint: direct `java` start

### No Docker-Compose Support

Docker-Compose is **not** supported. The two approaches conflict:
- K8s: Config via ConfigMap/Secret, CA via Job -> slim image without entrypoint.d
- Docker-Compose: needs entrypoint.d scripts that translate ENV vars -> config

**Local testing**: `kind` or `minikube` + the same K8s manifests.

## Phases

### Phase 1: Container Image
- [x] Rootless Containerfile (UBI9, tarball, no ezbake)
- [ ] Remove System Ruby (no Gemfile/bundle install)
- [ ] Simplify entrypoint (direct java, no entrypoint.d scripts)
- [ ] Build and test image

### Phase 2: Kubernetes Manifests
- [ ] Example ConfigMaps for puppet.conf, webserver.conf, etc.
- [ ] StatefulSet for CA Server
- [ ] Deployment for compilers
- [ ] Job for CA setup
- [ ] Services (puppet-ca, puppet)

### Phase 3: Operator
- [x] Initialize Go project (go.mod, cmd/main.go)
- [x] Define CRDs (api/v1alpha1/)
- [x] Controller: ConfigMap generation
- [x] Controller: CA Job lifecycle
- [x] Controller: CA StatefulSet management
- [x] Controller: Compiler Deployment management
- [ ] Generate CRD YAML
- [ ] RBAC manifests

### Phase 4: Extras
- [ ] r10k integration (initContainer / CronJob)
- [ ] HPA for compilers
- [ ] cert-manager intermediate CA support
- [ ] OLM bundle for OpenShift
