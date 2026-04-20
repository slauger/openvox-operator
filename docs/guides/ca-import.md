# Importing or Connecting an External CA

## CA Import (One-Time Migration)

If you have an existing CA and want the operator to manage it going forward, you can import the CA data into the operator's PVC.

### Prerequisites

- An existing Puppet/OpenVox CA with `ca_crt.pem`, `ca_key.pem`, and `ca_crl.pem`
- The operator installed in the cluster

### Steps

1. Create the `CertificateAuthority` resource as usual (without `spec.external`):

    ```yaml
    apiVersion: openvox.voxpupuli.org/v1alpha1
    kind: CertificateAuthority
    metadata:
      name: production-ca
    spec:
      ttl: 5y
      storage:
        size: 1Gi
    ```

2. Wait for the PVC to be created, then copy your CA data into it:

    ```bash
    # Find the PVC
    kubectl get pvc -l openvox.voxpupuli.org/certificate-authority=production-ca

    # Create a temporary pod to copy data
    kubectl run ca-import --image=busybox --restart=Never \
      --overrides='{
        "spec": {
          "containers": [{
            "name": "ca-import",
            "image": "busybox",
            "command": ["sleep", "3600"],
            "volumeMounts": [{
              "name": "ca-data",
              "mountPath": "/ca"
            }]
          }],
          "volumes": [{
            "name": "ca-data",
            "persistentVolumeClaim": {
              "claimName": "production-ca-data"
            }
          }]
        }
      }'

    # Copy CA files
    kubectl cp ca_crt.pem ca-import:/ca/ca_crt.pem
    kubectl cp ca_key.pem ca-import:/ca/ca_key.pem
    kubectl cp ca_crl.pem ca-import:/ca/ca_crl.pem

    # Clean up
    kubectl delete pod ca-import
    ```

3. The CA setup Job will detect existing data and skip regeneration. The operator will create the corresponding Secrets and transition to `Ready`.

## External CA (Ongoing Delegation)

If you have a Puppet/OpenVox CA running outside the cluster and want to keep using it, configure `spec.external` on the `CertificateAuthority` resource. The operator will delegate CSR signing and CRL fetching to the external CA URL.

### Prerequisites

- A running Puppet/OpenVox CA accessible from the cluster (e.g. `https://puppet-ca.example.com:8140`)
- The CA's public certificate (`ca_crt.pem`)
- (Optional) A client certificate and key for mTLS authentication

!!! tip "Using an existing Puppet CA"
    On a traditional Puppet CA server, the CA certificate is typically located at `/etc/puppetlabs/puppet/ssl/certs/ca.pem`. You can copy it with:
    ```bash
    scp puppet-ca.example.com:/etc/puppetlabs/puppet/ssl/certs/ca.pem ca_crt.pem
    ```

### Steps

1. Create Secrets with the CA certificate and optional client credentials:

    ```bash
    # CA certificate for TLS verification
    kubectl create secret generic external-ca-cert \
      --from-file=ca_crt.pem=ca_crt.pem

    # (Optional) Client certificate for mTLS
    kubectl create secret generic external-ca-tls \
      --from-file=tls.crt=/path/to/client.pem \
      --from-file=tls.key=/path/to/client-key.pem
    ```

2. Create the `CertificateAuthority` resource with `spec.external`:

    ```yaml
    apiVersion: openvox.voxpupuli.org/v1alpha1
    kind: CertificateAuthority
    metadata:
      name: external-ca
    spec:
      allowSubjectAltNames: true
      allowAuthorizationExtensions: true
      enableInfraCRL: true
      crlRefreshInterval: 5m
      external:
        url: https://puppet-ca.example.com:8140
        caSecretRef: external-ca-cert
        tlsSecretRef: external-ca-tls
    ```

3. The operator will:
   - Skip PVC creation and CA setup Job
   - Validate the referenced Secrets
   - Set the CA phase to `External`
   - Periodically fetch the CRL from the external CA
   - Route CSR signing requests to the external CA

4. Verify the CA transitions to `External` phase:

    ```bash
    kubectl get ca external-ca -o jsonpath='{.status.phase}'
    # Expected: External
    ```

### External CA Fields

| Field | Required | Description |
|-------|----------|-------------|
| `url` | Yes | Base URL of the external CA (e.g. `https://puppet-ca.example.com:8140`) |
| `caSecretRef` | No | Secret name containing `ca_crt.pem` for TLS verification |
| `tlsSecretRef` | No | Secret name containing `tls.crt` and `tls.key` for mTLS client auth |
| `insecureSkipVerify` | No | Skip TLS verification (not recommended for production) |

### Notes

- `spec.external` and custom `spec.storage` are mutually exclusive. External CAs do not need local storage.
- The `Certificate` controller accepts both `Ready` and `External` phases as "CA is available", so existing `Certificate` resources work without changes.
- The operator does not manage the external CA's lifecycle (upgrades, backups, etc.). You are responsible for maintaining it.
- CRL refresh still works with external CAs -- the operator fetches the CRL via the Puppet CA HTTP API and stores it in a local Secret.

For the full field reference, see [ExternalCASpec](../reference/certificateauthority.md#externalcaspec).

## Using Another openvox-stack as External CA

In a multi-cluster or multi-namespace setup you can run one openvox-stack as the **primary CA** and point secondary stacks at it using `spec.external`. This avoids duplicate CA key material and keeps a single source of truth for certificate signing.

### Steps

1. In the primary stack's namespace, export the CA certificate from its Secret:

    ```bash
    # Extract the CA certificate from the primary stack
    kubectl get secret production-ca-ca -n primary \
      -o jsonpath='{.data.ca_crt\.pem}' | base64 -d > ca_crt.pem
    ```

2. Create the CA certificate Secret in the secondary namespace:

    ```bash
    kubectl create secret generic primary-ca-cert \
      -n secondary \
      --from-file=ca_crt.pem=ca_crt.pem
    ```

3. Determine the primary CA's Service URL. If the primary stack runs in the same cluster, use the internal ClusterIP Service:

    ```bash
    # Format: https://<ca-name>-internal.<namespace>.svc:8140
    # Example:
    https://production-ca-internal.primary.svc:8140
    ```

    If the primary stack runs in a different cluster, expose it via LoadBalancer, Ingress, or VPN and use that URL instead.

4. Create the secondary `CertificateAuthority` resource:

    ```yaml
    apiVersion: openvox.voxpupuli.org/v1alpha1
    kind: CertificateAuthority
    metadata:
      name: secondary-ca
      namespace: secondary
    spec:
      allowSubjectAltNames: true
      allowAuthorizationExtensions: true
      enableInfraCRL: true
      crlRefreshInterval: 5m
      external:
        url: https://production-ca-internal.primary.svc:8140
        caSecretRef: primary-ca-cert
    ```

5. The secondary stack will now delegate all CSR signing and CRL fetching to the primary CA.
