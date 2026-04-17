package controller

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
	"github.com/slauger/openvox-operator/internal/puppet"
)

// caHTTPClientForCA returns an HTTP client configured for the CA.
// For external CAs, it builds an mTLS client from the referenced Secrets.
// For internal CAs, it uses the CA public certificate.
func caHTTPClientForCA(ctx context.Context, reader client.Reader, ca *openvoxv1alpha1.CertificateAuthority, namespace string) (*http.Client, error) {
	if ca.Spec.External != nil {
		return buildExternalCAHTTPClient(ctx, reader, ca.Spec.External, namespace)
	}

	caCertPEM, err := getCAPublicCert(ctx, reader, ca, namespace)
	if err != nil {
		return nil, fmt.Errorf("loading CA certificate: %w", err)
	}
	return caHTTPClient(caCertPEM)
}

// buildExternalCAHTTPClient creates an HTTP client for an external CA with optional mTLS and CA verification.
func buildExternalCAHTTPClient(ctx context.Context, reader client.Reader, ext *openvoxv1alpha1.ExternalCASpec, namespace string) (*http.Client, error) {
	tlsConfig := &tls.Config{
		InsecureSkipVerify: ext.InsecureSkipVerify, //nolint:gosec // user-controlled option for external CAs
	}

	// Load CA certificate for server verification
	if ext.CASecretRef != "" {
		secret := &corev1.Secret{}
		if err := reader.Get(ctx, types.NamespacedName{Name: ext.CASecretRef, Namespace: namespace}, secret); err != nil {
			return nil, fmt.Errorf("getting CA Secret %s: %w", ext.CASecretRef, err)
		}
		caCertPEM := secret.Data["ca_crt.pem"]
		if len(caCertPEM) > 0 {
			pool := x509.NewCertPool()
			if !pool.AppendCertsFromPEM(caCertPEM) {
				return nil, fmt.Errorf("failed to parse CA certificate from Secret %s", ext.CASecretRef)
			}
			tlsConfig.RootCAs = pool
		}
	}

	// Load client certificate for mTLS
	if ext.TLSSecretRef != "" {
		secret := &corev1.Secret{}
		if err := reader.Get(ctx, types.NamespacedName{Name: ext.TLSSecretRef, Namespace: namespace}, secret); err != nil {
			return nil, fmt.Errorf("getting TLS Secret %s: %w", ext.TLSSecretRef, err)
		}
		certPEM := secret.Data["tls.crt"]
		keyPEM := secret.Data["tls.key"]
		if len(certPEM) == 0 || len(keyPEM) == 0 {
			return nil, fmt.Errorf("TLS Secret %s missing tls.crt or tls.key", ext.TLSSecretRef)
		}
		clientCert, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			return nil, fmt.Errorf("parsing client certificate from Secret %s: %w", ext.TLSSecretRef, err)
		}
		tlsConfig.Certificates = []tls.Certificate{clientCert}
	}

	return &http.Client{
		Timeout:   HTTPClientTimeout,
		Transport: &http.Transport{TLSClientConfig: tlsConfig},
	}, nil
}

const (
	// AnnotationCSRPollAttempts tracks the number of CSR poll attempts on the pending Secret.
	AnnotationCSRPollAttempts = "openvox.voxpupuli.org/csr-poll-attempts"

	// CSRPollWaitingThreshold is the number of poll attempts before transitioning to WaitingForSigning.
	CSRPollWaitingThreshold = 10
)

// csrPollBackoff returns the requeue duration based on the number of poll attempts.
func csrPollBackoff(attempts int) time.Duration {
	switch {
	case attempts < 3:
		return 5 * time.Second
	case attempts < 6:
		return 30 * time.Second
	case attempts < 10:
		return 2 * time.Minute
	default:
		return 5 * time.Minute
	}
}

// caHTTPClient returns an HTTP client that trusts only the given CA certificate
// for communication with the Puppet CA service.
func caHTTPClient(caCertPEM []byte) (*http.Client, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caCertPEM) {
		return nil, fmt.Errorf("failed to parse CA certificate PEM")
	}
	return &http.Client{
		Timeout: HTTPClientTimeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{RootCAs: pool},
		},
	}, nil
}

// buildCSRExtensions converts a CSRExtensionsSpec into pkix.Extension values
// suitable for inclusion in a CSR template's ExtraExtensions field.
func buildCSRExtensions(spec *openvoxv1alpha1.CSRExtensionsSpec) ([]pkix.Extension, error) {
	if spec == nil {
		return nil, nil
	}

	var exts []pkix.Extension

	addExt := func(name, value string) error {
		oid, ok := puppet.OIDByName(name)
		if !ok {
			return fmt.Errorf("unknown Puppet extension: %s", name)
		}
		encoded, err := asn1.Marshal(value)
		if err != nil {
			return fmt.Errorf("encoding extension %s: %w", name, err)
		}
		exts = append(exts, pkix.Extension{Id: oid, Value: encoded})
		return nil
	}

	if spec.PpCliAuth {
		if err := addExt("pp_cli_auth", "true"); err != nil {
			return nil, err
		}
	}
	if spec.PpRole != "" {
		if err := addExt("pp_role", spec.PpRole); err != nil {
			return nil, err
		}
	}
	if spec.PpEnvironment != "" {
		if err := addExt("pp_environment", spec.PpEnvironment); err != nil {
			return nil, err
		}
	}

	// Sort custom extension keys for deterministic output
	keys := make([]string, 0, len(spec.CustomExtensions))
	for k := range spec.CustomExtensions {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		if err := addExt(k, spec.CustomExtensions[k]); err != nil {
			return nil, err
		}
	}

	return exts, nil
}

// submitCSR generates an RSA key (or reuses an existing one from a pending Secret),
// submits the CSR to the Puppet CA, and stores the private key in a pending Secret.
// Returns the pending Secret name.
func (r *CertificateReconciler) submitCSR(ctx context.Context, cert *openvoxv1alpha1.Certificate, ca *openvoxv1alpha1.CertificateAuthority, caBaseURL, namespace string) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	certname := cert.Spec.Certname
	if certname == "" {
		certname = "puppet"
	}

	pendingSecretName := fmt.Sprintf("%s-tls-pending", cert.Name)
	var keyPEM []byte

	// Check if we already have a pending key (from a previous attempt)
	pendingSecret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Name: pendingSecretName, Namespace: namespace}, pendingSecret)
	if err == nil {
		keyPEM = pendingSecret.Data["key.pem"]
	}

	if len(keyPEM) == 0 {
		// Generate new RSA 4096-bit key
		privateKey, err := rsa.GenerateKey(rand.Reader, RSAKeySize)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("generating RSA key: %w", err)
		}
		keyPEM = pem.EncodeToMemory(&pem.Block{
			Type:  "RSA PRIVATE KEY",
			Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
		})

		// Store key in pending Secret
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      pendingSecretName,
				Namespace: namespace,
				Labels: map[string]string{
					"openvox.voxpupuli.org/certificate": cert.Name,
				},
			},
			Data: map[string][]byte{"key.pem": keyPEM},
		}
		if err := controllerutil.SetControllerReference(cert, secret, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Create(ctx, secret); err != nil && !errors.IsAlreadyExists(err) {
			return ctrl.Result{}, fmt.Errorf("creating pending Secret: %w", err)
		}
	}

	// Parse private key from PEM to build CSR
	block, _ := pem.Decode(keyPEM)
	if block == nil {
		return ctrl.Result{}, fmt.Errorf("invalid PEM in pending key")
	}
	privateKey, err := x509.ParsePKCS1PrivateKey(block.Bytes)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("parsing pending key: %w", err)
	}

	// Build CSR extensions from spec
	extraExts, err := buildCSRExtensions(cert.Spec.CSRExtensions)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("building CSR extensions: %w", err)
	}

	// Build and submit CSR
	csrTemplate := &x509.CertificateRequest{
		Subject:         pkix.Name{CommonName: certname},
		DNSNames:        cert.Spec.DNSAltNames,
		ExtraExtensions: extraExts,
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, csrTemplate, privateKey)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("creating CSR: %w", err)
	}
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})

	httpClient, err := caHTTPClientForCA(ctx, r.Client, ca, namespace)
	if err != nil {
		return ctrl.Result{RequeueAfter: RequeueIntervalShort}, fmt.Errorf("creating CA HTTP client: %w", err)
	}
	csrURL := fmt.Sprintf("%s/puppet-ca/v1/certificate_request/%s?environment=production", caBaseURL, certname)

	logger.Info("submitting CSR to CA", "url", csrURL, "certname", certname)

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, csrURL, bytes.NewReader(csrPEM))
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("building CSR request: %w", err)
	}
	req.Header.Set("Content-Type", "text/plain")

	resp, err := httpClient.Do(req)
	if err != nil {
		return ctrl.Result{RequeueAfter: RequeueIntervalLong}, fmt.Errorf("submitting CSR: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, HTTPBodyLimit))
	if err != nil {
		logger.Error(err, "failed to read CSR response body")
	}
	if resp.StatusCode == http.StatusOK {
		logger.Info("CSR submitted successfully", "certname", certname)
	} else if resp.StatusCode == http.StatusBadRequest && strings.Contains(string(body), "already has a requested certificate") {
		logger.Info("CSR already pending", "certname", certname)
	} else {
		return ctrl.Result{RequeueAfter: RequeueIntervalCRL}, fmt.Errorf("CA rejected CSR (HTTP %d): %s", resp.StatusCode, string(body))
	}

	return ctrl.Result{}, nil
}

// fetchSignedCert checks if the CA has signed the certificate. Returns the PEM cert or nil.
func (r *CertificateReconciler) fetchSignedCert(ctx context.Context, cert *openvoxv1alpha1.Certificate, ca *openvoxv1alpha1.CertificateAuthority, caBaseURL, namespace string) ([]byte, error) {
	certname := cert.Spec.Certname
	if certname == "" {
		certname = "puppet"
	}

	httpClient, err := caHTTPClientForCA(ctx, r.Client, ca, namespace)
	if err != nil {
		return nil, fmt.Errorf("creating CA HTTP client: %w", err)
	}
	certURL := fmt.Sprintf("%s/puppet-ca/v1/certificate/%s?environment=production", caBaseURL, certname)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, certURL, nil)
	if err != nil {
		return nil, fmt.Errorf("building cert request: %w", err)
	}
	req.Header.Set("Accept", "text/plain")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching cert: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, HTTPBodyLimit))
	if err != nil {
		log.FromContext(ctx).Error(err, "failed to read certificate response body")
	}

	if resp.StatusCode == http.StatusOK && len(body) > 0 {
		block, _ := pem.Decode(body)
		if block != nil && block.Type == "CERTIFICATE" {
			return body, nil
		}
	}

	// Not yet signed
	return nil, nil
}

// signCertificate is the non-blocking orchestrator. It submits the CSR (if not already done),
// checks for the signed cert, and returns RequeueAfter if still waiting.
func (r *CertificateReconciler) signCertificate(ctx context.Context, cert *openvoxv1alpha1.Certificate, ca *openvoxv1alpha1.CertificateAuthority, caBaseURL, namespace string) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Step 1: Ensure CSR is submitted and key is persisted
	if result, err := r.submitCSR(ctx, cert, ca, caBaseURL, namespace); err != nil {
		return result, err
	}

	// Step 2: Check if cert is signed (non-blocking, single attempt)
	signedCertPEM, err := r.fetchSignedCert(ctx, cert, ca, caBaseURL, namespace)
	if err != nil {
		logger.Info("failed to fetch signed cert, will retry", "error", err)
		return ctrl.Result{RequeueAfter: RequeueIntervalMedium}, nil
	}

	if signedCertPEM == nil {
		// For internal CAs with a signing secret, sign the CSR directly via the CA API
		if ca.Spec.External == nil && ca.Status.SigningSecretName != "" {
			logger.Info("CSR not auto-signed, signing via operator mTLS", "certname", cert.Spec.Certname, "signingSecret", ca.Status.SigningSecretName)
			if err := r.signCSRViaAPI(ctx, cert, ca, caBaseURL, namespace); err != nil {
				logger.Info("operator signing failed, falling through to poll", "error", err)
			} else {
				// Fetch the now-signed cert immediately
				signedCertPEM, err = r.fetchSignedCert(ctx, cert, ca, caBaseURL, namespace)
				if err != nil {
					logger.Info("failed to fetch cert after operator signing", "error", err)
				}
			}
		}
	}

	if signedCertPEM == nil {
		// Read and increment poll attempt count from pending Secret annotation
		pendingSecretName := fmt.Sprintf("%s-tls-pending", cert.Name)
		pendingSecret := &corev1.Secret{}
		if err := r.Get(ctx, types.NamespacedName{Name: pendingSecretName, Namespace: namespace}, pendingSecret); err != nil {
			return ctrl.Result{}, fmt.Errorf("reading pending Secret for poll tracking: %w", err)
		}

		attempts := 0
		if v, ok := pendingSecret.Annotations[AnnotationCSRPollAttempts]; ok {
			attempts, _ = strconv.Atoi(v)
		}
		attempts++

		if pendingSecret.Annotations == nil {
			pendingSecret.Annotations = make(map[string]string)
		}
		pendingSecret.Annotations[AnnotationCSRPollAttempts] = strconv.Itoa(attempts)
		if err := r.Update(ctx, pendingSecret); err != nil {
			return ctrl.Result{}, fmt.Errorf("updating poll attempt annotation: %w", err)
		}

		backoff := csrPollBackoff(attempts)
		logger.Info("certificate not yet signed, will retry", "certname", cert.Spec.Certname, "attempt", attempts, "backoff", backoff)

		// After threshold, transition to WaitingForSigning phase
		if attempts >= CSRPollWaitingThreshold {
			certname := cert.Spec.Certname
			if certname == "" {
				certname = "puppet"
			}
			waitMsg := fmt.Sprintf("CSR submitted but not yet signed after %d attempts", attempts)
			if statusErr := updateStatusWithRetry(ctx, r.Client, cert, func() {
				cert.Status.Phase = openvoxv1alpha1.CertificatePhaseWaitingForSigning
				meta.SetStatusCondition(&cert.Status.Conditions, metav1.Condition{
					Type:               openvoxv1alpha1.ConditionCertSigned,
					Status:             metav1.ConditionFalse,
					Reason:             "WaitingForManualSigning",
					Message:            waitMsg,
					LastTransitionTime: metav1.Now(),
				})
			}); statusErr != nil {
				logger.Error(statusErr, "failed to update Certificate status to WaitingForSigning")
			}
			r.Recorder.Eventf(cert, nil, corev1.EventTypeWarning, EventReasonCSRWaitingForSigning, "Reconcile",
				"CSR submitted but not yet signed after %d attempts. To sign manually: puppetserver ca sign --certname %s",
				attempts, certname)
		}

		return ctrl.Result{RequeueAfter: backoff}, nil
	}

	// Step 3: Cert is signed -- read key from pending Secret and create TLS Secret
	pendingSecretName := fmt.Sprintf("%s-tls-pending", cert.Name)
	pendingSecret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: pendingSecretName, Namespace: namespace}, pendingSecret); err != nil {
		return ctrl.Result{}, fmt.Errorf("reading pending key Secret: %w", err)
	}
	keyPEM := pendingSecret.Data["key.pem"]

	tlsSecretName := fmt.Sprintf("%s-tls", cert.Name)
	if err := r.createOrUpdateTLSSecret(ctx, cert, ca, tlsSecretName, signedCertPEM, keyPEM); err != nil {
		return ctrl.Result{}, fmt.Errorf("creating TLS Secret: %w", err)
	}

	// Clean up pending Secret
	if err := r.Delete(ctx, pendingSecret); err != nil && !errors.IsNotFound(err) {
		logger.Info("failed to delete pending Secret", "error", err)
	}

	logger.Info("certificate signed successfully", "certname", cert.Spec.Certname)
	return ctrl.Result{}, nil
}

// signCSRViaAPI signs a pending CSR via the Puppet CA HTTP API using mTLS with the
// CA server's own certificate (which has the pp_cli_auth extension required by auth.conf).
func (r *CertificateReconciler) signCSRViaAPI(ctx context.Context, cert *openvoxv1alpha1.Certificate, ca *openvoxv1alpha1.CertificateAuthority, caBaseURL, namespace string) error {
	certname := cert.Spec.Certname
	if certname == "" {
		certname = "puppet"
	}

	// Load CA public cert for TLS server verification
	caCertPEM, err := getCAPublicCert(ctx, r.Client, ca, namespace)
	if err != nil {
		return fmt.Errorf("loading CA certificate: %w", err)
	}

	// Load signing secret (CA server cert + key) for mTLS client auth
	signingSecret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: ca.Status.SigningSecretName, Namespace: namespace}, signingSecret); err != nil {
		return fmt.Errorf("getting signing Secret %s: %w", ca.Status.SigningSecretName, err)
	}

	clientCertPEM := signingSecret.Data["cert.pem"]
	clientKeyPEM := signingSecret.Data["key.pem"]
	if len(clientCertPEM) == 0 || len(clientKeyPEM) == 0 {
		return fmt.Errorf("signing Secret %s missing cert.pem or key.pem", ca.Status.SigningSecretName)
	}

	// Build mTLS HTTP client
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caCertPEM) {
		return fmt.Errorf("failed to parse CA certificate PEM")
	}
	clientCert, err := tls.X509KeyPair(clientCertPEM, clientKeyPEM)
	if err != nil {
		return fmt.Errorf("parsing signing certificate: %w", err)
	}
	httpClient := &http.Client{
		Timeout: HTTPClientTimeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				RootCAs:      pool,
				Certificates: []tls.Certificate{clientCert},
			},
		},
	}

	// PUT /puppet-ca/v1/certificate_status/{certname}?environment=production
	signURL := fmt.Sprintf("%s/puppet-ca/v1/certificate_status/%s?environment=production", caBaseURL, certname)
	body := strings.NewReader(`{"desired_state": "signed"}`)

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, signURL, body)
	if err != nil {
		return fmt.Errorf("building sign request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("signing CSR via API: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, HTTPBodyLimit))
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("CA rejected sign request (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	log.FromContext(ctx).Info("CSR signed via operator mTLS", "certname", certname)
	return nil
}
