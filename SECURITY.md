# Security Policy

## Supported Versions

| Version | Supported          |
|---------|--------------------|
| main    | :white_check_mark: |

As the project is in early development, only the latest commit on `main` is supported with security fixes.

## Reporting a Vulnerability

If you discover a security vulnerability, please report it responsibly:

1. **Do not** open a public GitHub issue.
2. Email [simon@lauger.de](mailto:simon@lauger.de) with a description of the vulnerability.
3. Include steps to reproduce, if possible.

You should receive a response within 7 days. Once confirmed, a fix will be developed and released as soon as possible.

## Security Considerations

The openvox-operator manages sensitive resources including:

- **TLS certificates and private keys** (stored in Kubernetes Secrets)
- **CA private keys** (stored in Kubernetes Secrets, never mounted into pods)
- **Authentication tokens** (for ReportProcessor and NodeClassifier endpoints)

### RBAC

The operator follows least-privilege principles. It requests only the permissions required by each controller. Namespace-scoped deployment is supported via `scope.mode: namespace` in the Helm chart.

### Container Security

All operator-managed pods run with:

- Non-root user (UID 1001)
- `readOnlyRootFilesystem` (opt-in via `readOnlyRootFilesystem: true` on Config)
- `allowPrivilegeEscalation: false`
- All capabilities dropped
- Seccomp profile: RuntimeDefault
