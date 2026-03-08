# Certificate

A Certificate manages the lifecycle of a single X.509 certificate signed by a CertificateAuthority. The controller automatically chooses the signing strategy based on whether a CA server is already running.

## Example

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Certificate
metadata:
  name: production-cert
spec:
  authorityRef: production-ca
  certname: puppet
  dnsAltNames:
    - puppet
    - production-ca
```

## Spec

| Field | Type | Default | Description |
|---|---|---|---|
| `authorityRef` | string | **required** | Reference to the CertificateAuthority |
| `certname` | string | `puppet` | Certificate common name (CN) |
| `dnsAltNames` | []string | - | DNS subject alternative names |

## Status

| Field | Type | Description |
|---|---|---|
| `phase` | string | Current lifecycle phase |
| `secretName` | string | Name of the Secret containing `cert.pem` and `key.pem` |
| `conditions` | []Condition | `CertSigned` |

## Phases

| Phase | Description |
|---|---|
| `Pending` | Waiting for CertificateAuthority to reach `Ready` |
| `Requesting` | Certificate signing Job is running |
| `Signed` | SSL Secret created, Servers can mount it |
| `Error` | Certificate signing failed |

## Signing Strategy

The controller automatically selects how to sign the certificate:

| Strategy | Condition | How it works |
|---|---|---|
| **Local signing** | No CA Server running | Job mounts the CA PVC directly and signs locally |
| **HTTP bootstrap** | CA Server is running | Job runs `puppet ssl bootstrap` against the CA Service |

The controller discovers the CA Service automatically by finding Servers with `ca: true` in the same Environment and the Pools whose selector matches them.

## Created Resources

| Resource | Name | Description |
|---|---|---|
| ServiceAccount | `{name}-cert-setup` | Job ServiceAccount with permission to create the SSL Secret |
| Role | `{name}-cert-setup` | Scoped to SSL and CA Secret access |
| RoleBinding | `{name}-cert-setup` | Binds Role to ServiceAccount |
| Job | `{name}-cert-setup` | Signs the certificate (local or HTTP) and creates the SSL Secret |
| Secret | `{name}-ssl` | Certificate data: `cert.pem`, `key.pem` |
