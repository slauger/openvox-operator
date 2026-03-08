# SigningPolicy

A SigningPolicy defines a policy for automatic CSR signing against a CertificateAuthority. Multiple policies can reference the same CA — if **any** policy matches, the CSR is signed (OR logic between policies). Within a single policy, **all** set fields must match (AND logic).

## Example

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: SigningPolicy
metadata:
  name: auto-approve
spec:
  certificateAuthorityRef: production-ca
  any: true
```

### Pattern Matching

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: SigningPolicy
metadata:
  name: trusted-hosts
spec:
  certificateAuthorityRef: production-ca
  pattern:
    allow:
      - "*.example.com"
      - "web-*"
```

### Pre-Shared Key

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: SigningPolicy
metadata:
  name: bootstrap-key
spec:
  certificateAuthorityRef: production-ca
  psk:
    secretRef:
      name: signing-psk
      key: psk
    csrAttribute: pp_preshared_key
```

### Token-Based Authentication

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: SigningPolicy
metadata:
  name: one-time-tokens
spec:
  certificateAuthorityRef: production-ca
  token:
    secretRef:
      name: signing-tokens
    csrAttribute: pp_auth_token
```

### Combined (AND within policy)

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: SigningPolicy
metadata:
  name: trusted-with-psk
spec:
  certificateAuthorityRef: production-ca
  pattern:
    allow:
      - "*.example.com"
  psk:
    secretRef:
      name: signing-psk
      key: psk
```

This policy requires **both** a matching certname pattern **and** a valid PSK.

## Spec

| Field | Type | Default | Description |
|---|---|---|---|
| `certificateAuthorityRef` | string | **required** | Reference to the CertificateAuthority |
| `any` | bool | `false` | Sign all CSRs unconditionally |
| `pattern` | [PatternSpec](#patternspec) | - | Certname glob matching |
| `psk` | [PSKSpec](#pskspec) | - | Pre-shared key matching via CSR extension |
| `token` | [TokenSpec](#tokenspec) | - | Token-based matching via CSR extension |

### PatternSpec

| Field | Type | Default | Description |
|---|---|---|---|
| `allow` | []string | **required** | Glob patterns; certname must match at least one |

### PSKSpec

| Field | Type | Default | Description |
|---|---|---|---|
| `secretRef` | [SecretKeyRef](#secretkeyref) | **required** | Secret containing the PSK value |
| `csrAttribute` | string | `pp_preshared_key` | CSR extension attribute name |

### TokenSpec

| Field | Type | Default | Description |
|---|---|---|---|
| `secretRef` | [LocalSecretReference](#localsecretreference) | **required** | Secret with certname-to-token mappings |
| `csrAttribute` | string | `pp_auth_token` | CSR extension attribute name |

### SecretKeyRef

| Field | Type | Default | Description |
|---|---|---|---|
| `name` | string | **required** | Name of the Secret |
| `key` | string | **required** | Key within the Secret |

### LocalSecretReference

| Field | Type | Default | Description |
|---|---|---|---|
| `name` | string | **required** | Name of the Secret |

## Status

| Field | Type | Description |
|---|---|---|
| `phase` | string | Current lifecycle phase |
| `conditions` | []Condition | `Ready` |

## Phases

| Phase | Description |
|---|---|
| `Active` | Policy is rendered and active |
| `Error` | Policy has a configuration error (e.g. referenced Secret not found) |

## How It Works

1. The operator collects all SigningPolicies for a CertificateAuthority
2. It renders a policy config YAML into a Secret, mounted into the CA pod
3. puppet.conf always points to the `openvox-autosign` binary (static config, no pod restarts)
4. When a SigningPolicy changes, the operator updates the Secret. Kubelet syncs the mounted file (~60s). **No pod restart needed.**

The `openvox-autosign` binary shipped in the openvox-server container image evaluates policies at CSR signing time:

- Receives certname as argument, CSR on stdin
- Evaluates all policies (OR between policies, AND within a policy)
- **No policies** → deny all (exit 1)
- **`any: true`** → approve unconditionally (exit 0)
- **Pattern/PSK/Token** → evaluate rules
- Exits 0 (sign) or 1 (deny)
