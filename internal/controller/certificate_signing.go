package controller

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// caHTTPClient returns an HTTP client for talking to the Puppet CA (internal, self-signed).
func caHTTPClient() *http.Client {
	return &http.Client{
		Timeout: HTTPClientTimeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // internal CA
		},
	}
}

// caHTTPClientForCA returns an HTTP client appropriate for the CA type.
// For external CAs with TLS credentials, it builds an mTLS client.
// For internal CAs, it returns the default insecure client.
func (r *CertificateReconciler) caHTTPClientForCA(ctx context.Context, ca *openvoxv1alpha1.CertificateAuthority, namespace string) *http.Client {
	if ca.Spec.External == nil {
		return caHTTPClient()
	}

	logger := log.FromContext(ctx)
	tlsCfg := &tls.Config{} //nolint:gosec // configured below

	// Load CA certificate for verification
	if ca.Spec.External.CASecretRef != "" {
		caSecret := &corev1.Secret{}
		if err := r.Get(ctx, types.NamespacedName{Name: ca.Spec.External.CASecretRef, Namespace: namespace}, caSecret); err == nil {
			if caCertPEM, ok := caSecret.Data["ca_crt.pem"]; ok {
				pool := x509.NewCertPool()
				if pool.AppendCertsFromPEM(caCertPEM) {
					tlsCfg.RootCAs = pool
				}
			}
		} else {
			logger.Error(err, "failed to read external CA Secret", "secret", ca.Spec.External.CASecretRef)
		}
	}

	// Load client TLS credentials for mTLS
	if ca.Spec.External.TLSSecretRef != "" {
		tlsSecret := &corev1.Secret{}
		if err := r.Get(ctx, types.NamespacedName{Name: ca.Spec.External.TLSSecretRef, Namespace: namespace}, tlsSecret); err == nil {
			certPEM := tlsSecret.Data["tls.crt"]
			keyPEM := tlsSecret.Data["tls.key"]
			if len(certPEM) > 0 && len(keyPEM) > 0 {
				cert, err := tls.X509KeyPair(certPEM, keyPEM)
				if err == nil {
					tlsCfg.Certificates = []tls.Certificate{cert}
				} else {
					logger.Error(err, "failed to load client TLS credentials", "secret", ca.Spec.External.TLSSecretRef)
				}
			}
		} else {
			logger.Error(err, "failed to read TLS Secret", "secret", ca.Spec.External.TLSSecretRef)
		}
	}

	if ca.Spec.External.InsecureSkipVerify {
		tlsCfg.InsecureSkipVerify = true //nolint:gosec // user explicitly opted in
	} else if tlsCfg.RootCAs == nil {
		// No CA cert and no insecureSkipVerify: fall back to system roots
		// (which works if the external CA uses a publicly trusted cert)
	}

	return &http.Client{
		Timeout:   HTTPClientTimeout,
		Transport: &http.Transport{TLSClientConfig: tlsCfg},
	}
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

	// Build and submit CSR
	csrTemplate := &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: certname},
		DNSNames: cert.Spec.DNSAltNames,
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, csrTemplate, privateKey)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("creating CSR: %w", err)
	}
	csrPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})

	httpClient := r.caHTTPClientForCA(ctx, ca, namespace)
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

	httpClient := r.caHTTPClientForCA(ctx, ca, namespace)
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
		logger.Info("certificate not yet signed, will retry", "certname", cert.Spec.Certname)
		return ctrl.Result{RequeueAfter: RequeueIntervalShort}, nil
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
