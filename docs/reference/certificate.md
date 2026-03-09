# Certificate

A Certificate manages the lifecycle of a single X.509 certificate signed by a CertificateAuthority.

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
| `Requesting` | Certificate signing in progress |
| `Signed` | TLS Secret created, Servers can mount it |
| `Error` | Certificate signing failed |

## Signing Strategy

The controller uses two paths to obtain a signed certificate:

| Strategy | Condition | How it works |
|---|---|---|
| **CA setup export** | Certificate created before/with CA | CA setup Job creates the CA AND exports the server cert+key as a TLS Secret. The Certificate controller adopts the Secret. |
| **HTTP signing** | Certificate created after CA is Ready | Operator generates an RSA key pair in-process, submits a CSR to the Puppet CA HTTP API, and polls for the signed certificate. No Jobs or shell scripts involved. |

```mermaid
flowchart TD
    Start["Certificate created"] --> CheckCA{"CA Secret exists?"}
    CheckCA -->|No| Wait["Phase: Pending<br/>Wait for CA"]
    Wait --> CheckCA

    CheckCA -->|Yes| CheckTLS{"TLS Secret<br/>already exists?"}
    CheckTLS -->|Yes| Adopt["Adopt Secret<br/>(CA setup export path)"]
    Adopt --> Signed

    CheckTLS -->|No| FindSvc["Discover CA Service<br/>(Server ca:true → Pool → Service)"]
    FindSvc --> GenKey["Generate RSA 4096 key pair"]
    GenKey --> CSR["Submit CSR via<br/>PUT /puppet-ca/v1/certificate_request"]
    CSR --> Poll["Poll for signed cert<br/>GET /puppet-ca/v1/certificate"]
    Poll --> CreateSecret["Create TLS Secret<br/>cert.pem + key.pem"]
    CreateSecret --> Signed["Phase: Signed"]
```

The controller discovers the CA Service automatically by finding Servers with `ca: true` in the same Config and the Pools whose selector matches them.

## Created Resources

| Resource | Name | Description |
|---|---|---|
| Secret | `{name}-tls` | Certificate data: `cert.pem`, `key.pem` |
