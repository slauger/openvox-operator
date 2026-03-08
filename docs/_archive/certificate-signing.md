# Certificate Signing

## Current Behavior

The operator supports `autosign` via `Environment.spec.ca.autosign`. When set to `"true"`, the CA server automatically signs all incoming CSRs. This is suitable for development and trusted environments.

For production use cases where manual or policy-based approval is required, a more granular signing mechanism is planned.

## ServiceAccount Model

Each Environment creates dedicated ServiceAccounts with minimal privileges:

| ServiceAccount | Purpose | K8s API Token |
|---|---|---|
| `{env}-ca-setup` | CA setup job: creates CA certs and writes the CA Secret | Yes (needs to create/update Secrets) |
| `{env}-server` | All server pods (CA + compiler) | No (`automountServiceAccountToken: false`) |

The operator itself runs with its own ServiceAccount (managed by the Helm chart) with cluster-wide RBAC.

## Planned: CertificateRequest CRD

### Problem

Without `autosign=true`, CSRs remain pending on the CA server until an administrator manually runs `puppetserver ca sign`. In Kubernetes, this should be handled declaratively through a CRD.

### Design

A new `CertificateRequest` CRD represents a pending CSR:

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: CertificateRequest
metadata:
  name: compiler-01
  namespace: openvox-system
spec:
  environmentRef: production
  certname: compiler-01.openvox-system.svc
status:
  phase: Pending  # Pending | Approved | Signed | Denied
  fingerprint: "AA:BB:CC:..."
  requestedAt: "2025-01-01T00:00:00Z"
  signedAt: null
```

### Architecture

The signing process runs as a **sidecar container** in the CA server pod:

```
CA Server Pod
+---------------------------+
| openvox-server            |  Main container: puppetserver (CA enabled)
|   - CA PVC (read-write)   |
|   - Port 8140             |
+---------------------------+
| ca-signing-agent          |  Sidecar: polls for pending CSRs, manages CertificateRequest CRDs
|   - CA PVC (shared)       |
|   - K8s API token (SA)    |
+---------------------------+
```

The sidecar shares the CA PVC with the main container and has access to the K8s API via a ServiceAccount token.

### Flow

```
1. Compiler starts
   |
   v
2. Init container sends CSR to CA server (puppet ssl bootstrap)
   |
   v
3. CSR lands on CA server filesystem (pending)
   |
   v
4. Sidecar polls `puppetserver ca list` periodically
   |
   v
5. Sidecar finds pending CSR
   -> Creates CertificateRequest CR (status: Pending)
   |
   v
6. Admin or policy controller sets status to Approved
   (kubectl, GitOps, OPA, custom webhook, etc.)
   |
   v
7. Sidecar detects Approved CertificateRequest
   -> Runs `puppetserver ca sign <certname>`
   -> Updates CertificateRequest status to Signed
   -> Updates CA Secret (refreshes CRL)
   |
   v
8. Compiler receives signed cert from CA server
   -> Main container starts
```

### RBAC Impact

When the sidecar is enabled, the CA server pod needs a different ServiceAccount:

| ServiceAccount | Purpose | K8s API Token |
|---|---|---|
| `{env}-ca-signing` | CA server pod with sidecar | Yes (needs CertificateRequest CRD access + Secret update) |

The `{env}-server` SA remains unchanged (no token) for compiler pods and CA pods without the sidecar.

### RBAC for the Signing Agent

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: {env}-ca-signing
rules:
  # CertificateRequest CRDs
  - apiGroups: ["openvox.voxpupuli.org"]
    resources: ["certificaterequests"]
    verbs: ["get", "list", "watch", "create", "update", "patch"]
  - apiGroups: ["openvox.voxpupuli.org"]
    resources: ["certificaterequests/status"]
    verbs: ["get", "update", "patch"]
  # Update CA Secret (CRL refresh after signing)
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["get", "update", "patch"]
```

### Configuration

The sidecar is enabled automatically when `signingPolicies` are referenced in the Environment spec (see SigningPolicy CRD below). The sidecar poll interval can be configured:

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Environment
metadata:
  name: production
spec:
  ca:
    signingPolicies:
      - name: psk-signing
      - name: trusted-patterns
    signing:
      pollInterval: 30s      # How often the sidecar checks for pending CSRs (default: 30s)
```

When no `signingPolicies` are referenced, CSRs must be approved manually via CertificateRequest CRDs (deny-all default). See the next section for details.

### Denial and Revocation

- **Deny**: Setting a CertificateRequest to `Denied` triggers `puppetserver ca clean <certname>` (removes the pending CSR)
- **Revoke**: A separate `certificaterequests/revoke` subresource or a `revoked` status triggers `puppetserver ca revoke <certname>` and updates the CRL

## SigningPolicy CRD

### Problem

Different environments need different certificate signing strategies. Development environments may want automatic signing, while production environments require pre-shared key validation or pattern-based restrictions. These policies should be reusable across environments and managed as GitOps-friendly Kubernetes objects.

### Design

A `SigningPolicy` CRD defines how CSRs are automatically approved:

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: SigningPolicy
metadata:
  name: psk-signing
  namespace: openvox-system
spec:
  mode: psk
  psk:
    secretRef:
      name: signing-psk        # Secret containing the pre-shared key
      key: psk                  # Key within the Secret
    csrAttribute: pp_preshared_key  # CSR extension attribute to match (default: pp_preshared_key)
```

### Modes

| Mode | Description | Use Case |
|---|---|---|
| `psk` | Pre-shared key in CSR extension attribute | Automated agent bootstrapping with shared secret |
| `pattern` | Certname pattern matching (glob or regex) | Trusted naming conventions |
| `token` | One-time token validated against a Secret | Controlled single-use provisioning |
| `any` | Approve all CSRs unconditionally | Development and trusted environments |

### Mode Examples

**PSK (Pre-Shared Key)**

The agent includes a pre-shared key as a CSR extension attribute (`pp_preshared_key`). The sidecar validates the key against a Kubernetes Secret.

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: SigningPolicy
metadata:
  name: psk-signing
spec:
  mode: psk
  psk:
    secretRef:
      name: signing-psk
      key: psk
    csrAttribute: pp_preshared_key
---
apiVersion: v1
kind: Secret
metadata:
  name: signing-psk
type: Opaque
stringData:
  psk: "my-secret-bootstrap-key"
```

**Pattern Matching**

CSRs are approved if the certname matches one of the allowed patterns.

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: SigningPolicy
metadata:
  name: trusted-patterns
spec:
  mode: pattern
  pattern:
    allow:
      - "*.openvox-system.svc"
      - "compiler-*.example.com"
```

**One-Time Token**

Each agent uses a unique token that is consumed after signing.

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: SigningPolicy
metadata:
  name: token-signing
spec:
  mode: token
  token:
    secretRef:
      name: signing-tokens    # Secret with certname->token mappings
    csrAttribute: pp_auth_token
```

**Automatic (Any)**

Approves all CSRs unconditionally. Replaces the legacy `autosign: "true"` behavior.

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: SigningPolicy
metadata:
  name: auto-approve
spec:
  mode: any
```

### Environment Reference

An Environment references zero or more SigningPolicies:

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: Environment
metadata:
  name: production
spec:
  ca:
    signingPolicies:
      - name: psk-signing
      - name: trusted-patterns
```

**Evaluation logic:**

- **No `signingPolicies`** (default): Manual signing only. All CSRs create CertificateRequest CRDs in `Pending` state. An administrator or external policy controller must approve them.
- **One policy**: CSRs are evaluated against that policy. If it matches, the CSR is auto-approved. If not, a CertificateRequest CRD is created for manual approval.
- **Multiple policies**: OR logic - if any policy matches, the CSR is auto-approved. This allows combining e.g. PSK for automated agents with pattern matching for known hostnames.

### Signing Flow with Policies

```
1. CSR arrives on CA server filesystem
   |
   v
2. Sidecar detects pending CSR
   |
   v
3. Sidecar evaluates CSR against referenced SigningPolicies
   |
   +-- Policy matches --> Auto-approve: sign immediately
   |                      Update CertificateRequest status to Signed
   |
   +-- No policy matches --> Create CertificateRequest CR (status: Pending)
                             Wait for manual/external approval
```

### Migration from autosign

The `autosign` field on the Environment spec is replaced by SigningPolicies:

| Legacy | SigningPolicy equivalent |
|---|---|
| `autosign: "true"` | Reference a SigningPolicy with `mode: any` |
| `autosign: "false"` | No `signingPolicies` (manual default) |

The `autosign` field will be deprecated once SigningPolicy support is implemented.

## Implementation Phases

1. **Phase 1** (current): `autosign=true` for all environments
2. **Phase 2**: CertificateRequest CRD + sidecar for manual approval
3. **Phase 3**: SigningPolicy CRD with PSK, pattern, token, and any modes
4. **Phase 4**: CRL auto-refresh (sidecar updates CA Secret after any signing/revocation)
