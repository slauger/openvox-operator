# Installation

## Prerequisites

- Kubernetes or OpenShift cluster
- Helm 3.x

### Optional Components

| Component | When needed |
|---|---|
| Kubernetes 1.35+ | OCI Image Volumes for [code deployment](../concepts/code-deployment.md) (1.31+ with `ImageVolume` feature gate) |
| cert-manager | Webhook TLS certificate automation |
| Gateway API CRDs | [TLSRoute](../concepts/gateway-api.md) support in Pool |
| CloudNativePG (CNPG) | Managed PostgreSQL for [Database](../reference/database.md) |

## Install via Helm (OCI)

The Helm chart is published as an OCI artifact to GitHub Container Registry.

```bash
helm install openvox-operator \
  oci://ghcr.io/slauger/charts/openvox-operator \
  --namespace openvox-system \
  --create-namespace
```

## Verify

```bash
kubectl get pods -n openvox-system
```

You should see the operator pod running:

```
NAME                                READY   STATUS    AGE
openvox-operator-7b8f9d6c4-x2k9m   1/1     Running   30s
```

## Namespace-Scoped Mode

By default the operator watches all namespaces (cluster-scoped). To restrict it to a single namespace:

```bash
helm install openvox-operator \
  oci://ghcr.io/slauger/charts/openvox-operator \
  --namespace openvox-system \
  --create-namespace \
  --set scope.mode=namespace \
  --set scope.watchNamespace=my-namespace
```

In namespace mode the operator uses Role/RoleBinding instead of ClusterRole/ClusterRoleBinding and only reconciles resources in the configured namespace.

## Upgrading

### Status of SigningPolicy, NodeClassifier and ReportProcessor

From the version that introduced the `openvox.voxpupuli.org/rendered-from`
annotation, these three resources derive their `Ready` condition from the
Secrets the Config controller renders, and match themselves against that
annotation. Two things follow for an upgrade:

- Secrets rendered by the previous version carry no annotation, so every
  SigningPolicy, NodeClassifier and ReportProcessor reports
  `RenderedConfigSourceUnknown` with `Ready=False` until the Config controller
  re-renders -- normally seconds after the new operator starts. A Config that is
  [paused](../guides/pausing-reconciliation.md) never re-renders, so its
  resources stay in that state until it is resumed. Where several Configs
  reference one NodeClassifier, it can briefly flip to `Ready=False` while the
  Configs are re-rendered one at a time.
- The condition `reason` strings changed. `PolicyRendered` and `ConfigRendered`
  became `Rendered`, and the single catch-all `Error` reason was split into
  specific cases. Automation matching the old strings needs updating; see the
  reason tables in the [CRD reference](../reference/index.md).

A resource that is deliberately bypassed by an `autosignCommand` or
`externalNodesCommand` override now reports `phase: Disabled` rather than
`Active`, with `Ready=False` and a reason naming the override. Alerting on
`phase: Error` is unaffected by that case; alerting on `Ready=True` is not.

### OpenVox 9 is the default major

The content images now default to OpenVox 9. Three things move to 9 on upgrade:

- The `openvox-stack` chart defaults `config.image.repository` and
  `database.image.repository` to `openvox-server-9` and `openvox-db-9`.
- The unsuffixed images `openvox-server` and `openvox-db`, and their `:latest`
  tags, now carry OpenVox 9.
- The CR examples in this documentation reference the `-9` images.

Installations that set an explicit `-8` repository are unaffected. OpenVox 8
images are still built and published for every release. To stay on 8 with the
stack chart, pin the repositories:

```bash
helm upgrade openvox-stack oci://ghcr.io/slauger/charts/openvox-stack \
  --namespace openvox \
  --reuse-values \
  --set config.image.repository=ghcr.io/slauger/openvox-server-8 \
  --set database.image.repository=ghcr.io/slauger/openvox-db-8
```

When moving to 9, upgrade the servers and the database before the agents, and
back up the PostgreSQL database first: OpenVox DB migrates its schema on the
first start of a new major, and going back to 8 means restoring that backup.
Check the upstream
[openvox-server](https://github.com/OpenVoxProject/openvox-server/releases) and
[openvoxdb](https://github.com/OpenVoxProject/openvoxdb/releases) release notes
for changes that affect your code.

### CRDs are not upgraded by Helm

Helm installs the CRDs from the chart's `crds/` directory on the first install,
but [does not update them on `helm upgrade`](https://helm.sh/docs/chart_best_practices/custom_resource_definitions/).
An operator upgraded with `helm upgrade` alone keeps running against the CRDs of
whatever version was installed first: new spec fields are rejected, and status
fields the operator writes are silently stripped by the API server. The symptom
is a status that never fills in, which does not point at the CRDs.

Apply the CRDs of the target version before upgrading the release:

```bash
VERSION=<target-version>

helm pull oci://ghcr.io/slauger/charts/openvox-operator \
  --version "$VERSION" --untar

kubectl apply -f openvox-operator/crds/

helm upgrade openvox-operator \
  oci://ghcr.io/slauger/charts/openvox-operator \
  --namespace openvox-system \
  --version "$VERSION"
```

Applying the CRDs is additive and safe to repeat. Existing custom resources are
left untouched; only the schema is updated.

## Next Steps

Once the operator is running, follow the [Quick Start](quickstart.md) guide to deploy an OpenVox stack. The Quick Start uses the [`openvox-stack`](https://ghcr.io/slauger/charts/openvox-stack) Helm chart which bundles all required custom resources into a single install command.
