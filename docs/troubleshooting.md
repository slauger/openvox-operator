# Troubleshooting

This guide covers common issues and their solutions when running the OpenVox Operator.

## Resource Status Issues

### Nothing happens when I change a resource

Check whether reconciliation is paused for it:

```console
$ kubectl get <kind> <name> -o jsonpath='{.metadata.annotations.openvox\.voxpupuli\.org/paused}'
```

A value of `true` means the operator is deliberately leaving the resource
alone. Remove the annotation to resume -- see
[Pausing Reconciliation](guides/pausing-reconciliation.md).

If it is not paused, the resource may be waiting on a dependency; the `Ready`
condition names it.

### Config stuck in Pending phase

**Symptoms:** Config resource remains in `Pending` phase and never transitions to `Running`.

**Possible causes:**

1. **Missing CertificateAuthority:** The `authorityRef` points to a CA that doesn't exist.

    ```bash
    kubectl get certificateauthority -n <namespace>
    ```

2. **CA not ready:** The referenced CA hasn't completed initialization.

    ```bash
    kubectl get certificateauthority <ca-name> -o jsonpath='{.status.phase}'
    ```

**Solution:** Ensure the referenced CertificateAuthority exists and is in `Ready` phase before creating the Config.

### Certificate stuck in Pending phase

**Symptoms:** Certificate never reaches `Signed` phase.

A SigningPolicy is usually *not* the cause here. For an internal CA the
operator signs its own Certificate resources over the CA API, authenticated
with the operator signing certificate, so autosign is not involved. Policies
govern agents, not Certificate resources.

**Possible causes:**

1. **CA not ready.** The CertificateAuthority must report `CAReady`.

    ```bash
    kubectl get certificateauthority <name> -n <namespace> \
      -o jsonpath='{range .status.conditions[*]}{.type}={.status} {.reason}{"\n"}{end}'
    ```

2. **Operator signing certificate not available yet.** Without it the operator
   cannot sign and falls back to polling, which only succeeds if something else
   signs the CSR.

    ```bash
    kubectl get certificateauthority <name> -n <namespace> \
      -o jsonpath='{.status.signingSecretName}{"\n"}'
    ```

    An empty value means the `{ca}-operator-signing` Certificate is not signed
    yet. During bootstrap this resolves on its own.

3. **Certname already claimed.** Two Certificates cannot share a certname
   against the same CA. The condition names the holder:

    ```bash
    kubectl get certificate <name> -n <namespace> \
      -o jsonpath='{range .status.conditions[*]}{.type}={.status} {.reason}: {.message}{"\n"}{end}'
    ```

4. **External CA.** With `spec.external` the operator has no admin access and
   cannot sign. The CSR must be signed on the external CA.

**Agents** stuck in `--waitforcert` are the case where SigningPolicy matters -
see [Agents cannot connect to server](#agents-cannot-connect-to-server).

### Server pods not starting

**Symptoms:** Server Deployment exists but pods are not running.

**Possible causes:**

1. **Certificate not signed:** The referenced Certificate must be in `Signed` phase.
2. **Image pull errors:** Container image is unavailable.
3. **Resource constraints:** Insufficient CPU or memory on nodes.

**Debugging steps:**

```bash
kubectl describe deployment <server-name> -n <namespace>
kubectl describe pod -l openvox.voxpupuli.org/server=<server-name> -n <namespace>
kubectl get events -n <namespace> --sort-by='.lastTimestamp'
```

## Pod Runtime Issues

### Server container CrashLoopBackOff

**Symptoms:** Server pods repeatedly restart.

**Debugging steps:**

```bash
kubectl logs <pod-name> -n <namespace> --previous
```

**Common causes:**

1. **Invalid puppet.conf:** Check the Config spec for invalid Puppet settings.
2. **Missing CA certificate:** The CA Secret may not exist or is empty.
3. **Database connection issues:** If using PuppetDB, verify database connectivity.

### Certificate errors in logs

**Symptoms:** SSL handshake failures or certificate verification errors.

**Possible causes:**

1. **Expired certificate:** Check certificate expiration.

    ```bash
    kubectl get certificate <cert-name> -o jsonpath='{.status.notAfter}'
    ```

2. **Hostname mismatch:** The `dnsAltNames` in the Certificate spec doesn't include the service hostname.

**Solution:** Update the Certificate spec with correct `dnsAltNames` and wait for re-signing.

## Networking Issues

### Agents cannot connect to server

**Symptoms:** Puppet agents fail to connect to the server endpoint, or hang in
`puppet agent --waitforcert`.

An agent that reaches the server but hangs waiting for its certificate has a
signing problem, not a connectivity one. The operator points `autosign` at its
own binary as soon as a CertificateAuthority exists, and that binary denies
every CSR no policy matches - so **no SigningPolicy means deny-all, not off**.
The servers run normally and the Config reports `Running` either way.

```bash
kubectl get signingpolicy -n <namespace>
```

An empty list is the common cause on a fresh install. See
[SigningPolicy](reference/signingpolicy.md) for the available match rules, or
sign by hand:

```bash
kubectl exec -n <namespace> deploy/<ca-server> -- \
  puppetserver ca list --all
kubectl exec -n <namespace> deploy/<ca-server> -- \
  puppetserver ca sign --certname <agent-certname>
```

**Debugging steps for connectivity:**

1. Verify the Pool Service exists:

    ```bash
    kubectl get svc -n <namespace> -l app.kubernetes.io/name=openvox
    ```

2. Check endpoints are populated:

    ```bash
    kubectl get endpoints <pool-name> -n <namespace>
    ```

3. Test connectivity from within the cluster:

    ```bash
    kubectl run -it --rm debug --image=busybox --restart=Never -- \
      nc -zv <service-name>.<namespace>.svc 8140
    ```

### Gateway API TLSRoute not working

**Symptoms:** External traffic doesn't reach the server via TLSRoute.

**Possible causes:**

1. **Gateway not ready:** The referenced Gateway must be in `Accepted` state.
2. **Missing RBAC:** Ensure the operator has permissions for gateway.networking.k8s.io resources.

**Debugging steps:**

```bash
kubectl get gateway -A
kubectl get tlsroute -n <namespace> -o yaml
```

## Operator Issues

### Operator pod not running

**Symptoms:** The operator Deployment exists but pods are not ready.

**Debugging steps:**

```bash
kubectl describe deployment openvox-operator -n openvox-system
kubectl logs deployment/openvox-operator -n openvox-system
```

**Common causes:**

1. **Missing CRDs:** Custom Resource Definitions not installed.
2. **RBAC issues:** ServiceAccount lacks required permissions.
3. **Leader election failure:** Multiple replicas competing for leadership.

### Resources not reconciling

**Symptoms:** Changes to custom resources are not reflected in the cluster.

**Debugging steps:**

1. Check operator logs for errors:

    ```bash
    kubectl logs deployment/openvox-operator -n openvox-system -f
    ```

2. Verify the resource has the correct owner references:

    ```bash
    kubectl get <resource> -o jsonpath='{.metadata.ownerReferences}'
    ```

3. Force reconciliation by adding an annotation:

    ```bash
    kubectl annotate <resource-type> <name> reconcile=$(date +%s) --overwrite
    ```

## Helm Installation Issues

### Chart installation fails

**Symptoms:** `helm install` returns an error.

**Common causes:**

1. **CRDs not installed:** The operator CRDs must be installed before the stack chart.

    ```bash
    kubectl get crd | grep openvox
    ```

2. **Namespace doesn't exist:** Use `--create-namespace` flag.
3. **Values validation error:** Check values against the chart schema.

### Upgrade fails with immutable field error

**Symptoms:** `helm upgrade` fails because a field cannot be changed.

**Solution:** Some Kubernetes fields are immutable after creation (e.g., PVC storage class). You may need to delete and recreate the affected resources.

## Getting Help

If these steps don't resolve your issue:

1. Collect diagnostic information:

    ```bash
    kubectl get all,config,certificateauthority,signingpolicy,certificate,server,pool,database -n <namespace> -o yaml > diagnostics.yaml
    kubectl logs deployment/openvox-operator -n openvox-system --tail=1000 >> diagnostics.yaml
    ```

2. Open an issue at [GitHub Issues](https://github.com/slauger/openvox-operator/issues) with the diagnostic output.
