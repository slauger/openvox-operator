# Connecting Agents

The operator manages the server side. Agents are ordinary Puppet agents: they
are not Kubernetes resources, and nothing in the operator creates or tracks
them. This page covers what they need from a stack deployed by the operator.

## What an agent needs

Three things, and they are the usual ones:

| | |
|---|---|
| **A reachable server address** | the Service of a Pool the server joins, on port 8140 |
| **A certname** | the identity the certificate is issued for |
| **A signed certificate** | either autosigned by a [SigningPolicy](../reference/signingpolicy.md) or signed by hand |

!!! warning "Without a SigningPolicy nothing is signed"

    The operator points `autosign` at its own binary as soon as a
    CertificateAuthority exists, and that binary denies every CSR no policy
    matches. An empty policy list is deny-all, not off. Agents then sit in
    `--waitforcert` while the servers look perfectly healthy.

## Where to point the agent

Agents talk to the Service of a Pool. With the default layout the CA server
joins both pools (`poolRefs: [ca, server]`), so one address serves catalog
requests and the CA:

```bash
puppet agent --test \
  --server <release>-server \
  --certname web01.example.com \
  --waitforcert 30
```

If you separate the roles - the CA in the `ca` pool only, compilers in
`server` - the agent needs both addresses, because catalog and CA no longer
share one:

```ini
[main]
server    = <release>-server
ca_server = <release>-ca
```

## From inside the cluster

Any Puppet agent works. This project also builds `openvox-agent-<major>`, but
be aware of what it is: a **test artifact**. It is built by CI and not by the
release workflow, so it carries only the `develop` tag - no `latest`, no
version. For anything but a throwaway check, use your own agent image and pin
it.

The shape below is what the e2e suite runs:

```yaml
apiVersion: batch/v1
kind: Job
metadata:
  name: puppet-agent
spec:
  backoffLimit: 0
  template:
    spec:
      restartPolicy: Never
      containers:
        - name: puppet-agent
          image: ghcr.io/slauger/openvox-agent-8:develop  # test artifact, see above
          command: ["sh", "-c"]
          args:
            - |
              puppet agent --test \
                --server <release>-server \
                --certname agent-01 \
                --waitforcert 30
              # 0 = no changes, 2 = changes applied; both are success
              EXIT=$?
              if [ $EXIT -eq 0 ] || [ $EXIT -eq 2 ]; then exit 0; else exit $EXIT; fi
```

The exit code check matters: `puppet agent --test` returns **2** when it
applied changes, which is a success and not a failure.

## From outside the cluster

A Pool Service defaults to `ClusterIP`, which is unreachable from outside.
Choose one:

| Option | How |
|---|---|
| **LoadBalancer** | `pools[].service.type: LoadBalancer` |
| **NodePort** | `pools[].service.type: NodePort`, optionally `nodePort` |
| **Gateway API** | `pools[].route`, see [Gateway API](../concepts/gateway-api.md) |

Whichever you pick, the name agents connect through must be in the server
certificate. The chart derives the Service names of every Pool a server joins
into `dnsAltNames` automatically; an external name such as a load balancer
address has to be added explicitly:

```yaml
servers:
  - name: ca
    certificate:
      certname: puppet
      dnsAltNames:
        - puppet.example.com
```

A missing name shows up as a TLS error on the agent, not as an operator
problem:

```
Server hostname 'puppet.example.com' did not match server certificate
```

## Signing by hand

Without a matching policy the CSR waits. List and sign on the CA pod:

```bash
kubectl exec -n <namespace> deploy/<ca-server> -- \
  puppetserver ca list --all

kubectl exec -n <namespace> deploy/<ca-server> -- \
  puppetserver ca sign --certname web01.example.com
```

## Removing an agent

Revoking is a CA operation, and the operator does not do it for you - it only
manages the certificates that belong to its own resources:

```bash
kubectl exec -n <namespace> deploy/<ca-server> -- \
  puppetserver ca clean --certname web01.example.com
```

The revocation reaches agents with the next CRL refresh, which runs on
`spec.crlRefreshInterval` (default `5m`). Until then the revoked certificate is
still accepted.

## Verifying it worked

The agent reports success itself, but two things are worth checking on the
server side.

**Did the catalog compile?**

```bash
kubectl logs -n <namespace> deploy/<ca-server> | grep -i "Compiled catalog"
```

**Did the facts reach PuppetDB?** Only when a Database is wired up:

```bash
kubectl exec -n <namespace> deploy/<database> -- \
  curl -sf "http://127.0.0.1:8080/pdb/query/v4/nodes"
```

An empty result with a healthy agent run usually means `routes.yaml` is not
routing the facts terminus to PuppetDB - see
[Database](../reference/database.md).
