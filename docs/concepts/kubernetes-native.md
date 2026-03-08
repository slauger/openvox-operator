# Kubernetes-Native Design

## Why a New Approach?

Traditional Puppet/OpenVox Server installations on VMs use OS packages that install both a system Ruby (CRuby) and the server JAR with its embedded JRuby. Existing container images carry this VM-centric approach into containers, leading to several problems in a Kubernetes context.

## Problems with Existing Container Approaches

### ezbake Legacy

Upstream OpenVox Server uses ezbake for packaging. It generates init scripts that start as root and switch to the puppet user via `runuser`/`su`/`sudo`. This breaks rootless containers and OpenShift random UIDs.

### Duplicate Ruby Installation

The server needs JRuby (embedded in the JAR) for runtime. Existing containers additionally install system Ruby + the openvox gem just so entrypoint scripts can call `puppet config set/print`. This is unnecessary when configuration comes via ConfigMaps.

### Docker Logic in Kubernetes

Existing images use ~15 entrypoint scripts that translate environment variables into config files. This is a Docker-Compose pattern. In Kubernetes, the operator renders configuration into ConfigMaps and Secrets directly.

### No Role Separation

Existing containers decide at startup whether to run as CA or server based on environment variables. In Kubernetes, the operator handles orchestration and role assignment through the CRD model.

## How openvox-operator Differs

| | VM-based / Docker | openvox-operator |
|---|---|---|
| **Ruby** | System Ruby (CRuby) alongside JRuby | No system Ruby - only JRuby in the server JAR |
| **Configuration** | `puppet config set`, entrypoint scripts, ENV vars | Declarative CRDs, operator renders ConfigMaps |
| **Privileges** | Requires root | Fully rootless, random UID compatible |
| **CA Management** | `puppetserver ca` CLI (CRuby) | Custom JRuby wrapper via `clojure.main` |
| **Certificates** | Each server has its own certificate | All replicas of a Server share one certificate |
| **Scaling** | Manual VM provisioning | Deployment replicas + HPA |
| **Code Deployment** | r10k on the VM, cron/webhook | CodeDeploy CRD manages r10k as Job/CronJob |
| **Multi-Version** | Separate VMs or package pinning | Multiple Servers in the same Pool |

## Container Image

The operator uses a minimal container image:

**Included:**

- UBI9 + JDK 17
- Tarball installation (puppet-server-release.jar, CLI tools, vendored JRuby gems)
- PuppetDB termini
- OpenShift random-UID pattern (chgrp 0, chmod g=u)
- Direct `java` entrypoint (no wrapper scripts)

**Removed (compared to upstream images):**

- All entrypoint.d scripts
- System Ruby and openvox gem
- Gemfile / bundle install / ruby-devel / gcc / make
- ENV var to config translation logic
- Docker-Compose support
