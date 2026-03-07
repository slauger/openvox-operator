# Roadmap

- [x] Rootless OpenVox Server container image (UBI9, tarball-based, no ezbake)
- [x] CRD data model design (Environment, Pool, Server, CodeDeploy)
- [ ] Implement multi-CRD Go types and controllers
- [ ] Simplify container image (remove entrypoint.d, Gemfile, System Ruby)
- [ ] CRD manifest generation and RBAC
- [ ] r10k code deployment (Job / CronJob with shared PVC)
- [ ] HPA for compiler autoscaling
- [ ] cert-manager intermediate CA support
- [ ] OLM bundle for OpenShift
- [ ] Rootless OpenVoxDB container image
