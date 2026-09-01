# Pausing Reconciliation

Sometimes the operator has to stop touching a single resource for a while --
during a GitOps migration, while investigating an incident, or when you want to
try something on a Deployment without having it reverted a second later.

Setting the annotation `openvox.voxpupuli.org/paused` to `"true"` suspends
reconciliation for that one object:

```console
$ kubectl annotate server web openvox.voxpupuli.org/paused=true
```

The operator records this in a `Paused` condition and then leaves the resource
and everything below it alone:

```console
$ kubectl get server web -o jsonpath='{.status.conditions[?(@.type=="Paused")]}'
{"type":"Paused","status":"True","reason":"Paused",
 "message":"Reconciliation is suspended by the openvox.voxpupuli.org/paused annotation"}
```

Remove the annotation to resume. The condition disappears and the next
reconcile brings the resource back in line with its spec:

```console
$ kubectl annotate server web openvox.voxpupuli.org/paused-
```

## What pausing does and does not do

| | |
|---|---|
| Reconciliation of the annotated resource | **suspended** |
| Child resources (Deployments, Services, Secrets, ...) | left as they are, not reverted |
| Other resources of the same kind | unaffected |
| Deletion | **still works** -- finalizers run as usual |
| Running workloads | untouched; pods keep serving |

Pausing is deliberately per-resource. Scaling the operator Deployment to zero
also stops reconciliation, but for every resource in the cluster at once.

## Supported resources

`Config`, `CertificateAuthority`, `Certificate`, `Server`, `Pool`, `Database`
and `ReportProcessor`.

`SigningPolicy` and `NodeClassifier` have no controller of their own -- they are
rendered by the `Config` controller, so pause the `Config` instead.

## Why an annotation and not a spec field

Pausing is a temporary operational measure, not a declared desired state. As an
annotation it stays out of the manifests in your GitOps repository, where a
permanent `suspend: true` would eventually be committed by accident and then
puzzle the next person to look at it.

Note that Argo CD and Flux will revert an annotation you set by hand if the
resource is under their control and they prune extra metadata. Pause the
Application or Kustomization on their side as well, or add the annotation to
the manifest for as long as you need it.
