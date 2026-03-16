# CA Import Guide

If you have an existing Puppet/OpenVox CA and want to migrate to the operator-managed CA,
you can seed the CA PVC manually. This is a one-time operation.

## Steps

1. Create the CertificateAuthority resource without applying it yet
2. Create the PVC manually with the correct size
3. Run a Kubernetes Job to copy CA data from a Secret into the PVC:
   - Mount your CA data as a Secret
   - Copy to the expected paths under `/etc/puppetlabs/puppetserver/ca/`
4. Create the required Secrets manually:
   - `{name}-ca`: contains `ca_crt.pem`
   - `{name}-ca-key`: contains `ca_key.pem`
   - `{name}-ca-crl`: contains `ca_crl.pem`
5. Patch the CertificateAuthority status to Ready

## External CA Alternative

Instead of importing the CA data, you can use an External CA (`spec.external`) to keep the
CA running outside the cluster. The operator will connect to the external CA's HTTP API for
certificate signing operations.

See the [External CA sample](../../config/samples/certificateauthority-external.yaml) for
a complete example.

### External CA Configuration

```yaml
apiVersion: openvox.voxpupuli.org/v1alpha1
kind: CertificateAuthority
metadata:
  name: my-ca
spec:
  external:
    url: "https://puppet-ca.example.com:8140"
    caSecretRef: puppet-ca-cert        # Secret with ca_crt.pem
    tlsSecretRef: puppet-client-tls    # Secret with tls.crt and tls.key for mTLS
```

When `spec.external` is set:

- The operator skips PVC creation and CA setup Job
- Certificate signing requests are sent to the external CA URL
- CRL refresh is not performed (managed externally)
- The CA enters the `External` phase instead of `Ready`
