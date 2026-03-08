# Roadmap

## Completed

- [x] Rootless OpenVox Server container image (UBI9, tarball-based, no ezbake)
- [x] CRD data model design (Environment, Pool, Server, CodeDeploy, Database)
- [x] Initial Go operator scaffolding (go.mod, cmd/main.go, controller stubs)
- [x] Documentation: README.md, data-model.md, design.md, architecture.md
- [x] Multi-CRD Go types (Environment, Pool, Server)
- [x] Multi-CRD controllers (environment_controller.go, server_controller.go)
- [x] CRD manifests and RBAC (config/crd/bases/, config/rbac/)
- [x] Helm chart for operator deployment
- [x] CI pipeline (GitHub Actions: Go lint/vet/test, container builds, hadolint, shellcheck, helm lint)
- [x] Container image simplification (no System Ruby, JRuby-only CLI wrapper for `puppetserver ca`)
- [x] CA setup job creates K8s Secret with CA certificates (ca_crt.pem, ca_crl.pem, infra_crl.pem)
- [x] Dedicated ServiceAccounts per Environment (ca-setup with RBAC, server without token)
- [x] Compiler servers mount CA Secret for cert/CRL distribution
- [x] Job reconciliation with image-change detection and permanent-failure handling
- [x] Renovate for dependency management

## Architecture Decisions

These decisions were made during design and should be followed during implementation:

- **All Servers use Deployments** - CA with Recreate strategy, compilers with RollingUpdate. No StatefulSets.
- **Shared cert per Server CR** - all pods of a Server share the same cert from a Secret. No per-pod certs.
- **Pool owns the Service** - solves ownership when multiple Servers share a Service
- **Environment creates CA Service automatically** - internal ClusterIP, no Pool needed for CA
- **No System Ruby in container image** - JRuby wrapper for `puppetserver ca` CLI, config via ConfigMaps
- **CA public data via K8s Secret** - CA setup job creates Secret with certs/CRL, compiler servers mount it
- **Dedicated ServiceAccounts** - `{env}-ca-setup` (with token, for Secret creation), `{env}-server` (without token)
- **Never use em-dashes** - only normal hyphens everywhere

## In Progress

- [ ] End-to-end testing: CA server + compiler server with autosign
- [ ] Pool controller (manages Kubernetes Service for compiler load balancing)

## Next: Certificate Signing

See [certificate-signing.md](certificate-signing.md) for the detailed design.

- [ ] CertificateRequest CRD for declarative CSR approval
- [ ] Sidecar container on CA server pod for CSR polling and signing
- [ ] Dedicated `{env}-ca-signing` ServiceAccount with CRD access
- [ ] SigningPolicy CRD with modes: psk, pattern, token, any
- [ ] Environment references SigningPolicies (replaces autosign field)
- [ ] CRL auto-refresh in CA Secret after signing/revocation

## Later

- [ ] r10k code deployment (Job / CronJob with shared PVC)
- [ ] HPA for compiler autoscaling
- [ ] cert-manager intermediate CA support
- [ ] OLM bundle for OpenShift
- [ ] Rootless OpenVoxDB container image
- [ ] Policy-based auto-approval for CertificateRequests (OPA/Kyverno)

## Outdated Files

These files exist but are outdated and need rewrite or deletion:

| File | Status |
|---|---|
| `api/v1alpha1/openvoxserver_types.go` | Old single-CRD model, needs deletion |
| `internal/controller/openvoxserver_controller.go` | Old single-CRD controller, needs deletion |
| `internal/controller/configmap.go` | Old controller helper, needs deletion |
| `internal/controller/ca.go` | Old controller helper, needs deletion |
| `internal/controller/compiler.go` | Old controller helper, needs deletion |
| `docs/design.md` | Partially outdated, references StatefulSet for CA |
| `docs/architecture.drawio` | Outdated diagram |
| `docs/container-image-plan.md` | Largely completed, can be archived |
| `images/openvoxserver/` | Old image directory (renamed to openvox-server) |
