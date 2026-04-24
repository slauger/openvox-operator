package controller

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

func TestBuildCSRExtensions_Nil(t *testing.T) {
	exts, err := buildCSRExtensions(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exts != nil {
		t.Errorf("expected nil, got %v", exts)
	}
}

func TestBuildCSRExtensions_PpCliAuth(t *testing.T) {
	spec := &openvoxv1alpha1.CSRExtensionsSpec{
		PpCliAuth: true,
	}
	exts, err := buildCSRExtensions(spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(exts) != 1 {
		t.Fatalf("expected 1 extension, got %d", len(exts))
	}
	// pp_cli_auth OID: 1.3.6.1.4.1.34380.1.3.39
	expectedOID := asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 34380, 1, 3, 39}
	if !exts[0].Id.Equal(expectedOID) {
		t.Errorf("expected OID %v, got %v", expectedOID, exts[0].Id)
	}
}

func TestBuildCSRExtensions_AllFields(t *testing.T) {
	spec := &openvoxv1alpha1.CSRExtensionsSpec{
		PpCliAuth:     true,
		PpRole:        "compiler",
		PpEnvironment: "production",
		CustomExtensions: map[string]string{
			"pp_cost_center": "IT",
			"pp_department":  "Engineering",
		},
	}
	exts, err := buildCSRExtensions(spec)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// pp_cli_auth + pp_role + pp_environment + 2 custom = 5
	if len(exts) != 5 {
		t.Fatalf("expected 5 extensions, got %d", len(exts))
	}
}

func TestBuildCSRExtensions_UnknownCustomExtension(t *testing.T) {
	spec := &openvoxv1alpha1.CSRExtensionsSpec{
		CustomExtensions: map[string]string{
			"unknown_extension": "value",
		},
	}
	_, err := buildCSRExtensions(spec)
	if err == nil {
		t.Fatal("expected error for unknown extension")
	}
	if !strings.Contains(err.Error(), "unknown Puppet extension") {
		t.Errorf("expected 'unknown Puppet extension' error, got: %v", err)
	}
}

func TestCSRPollBackoff(t *testing.T) {
	tests := []struct {
		attempts int
		expected time.Duration
	}{
		{0, 5 * time.Second},
		{1, 5 * time.Second},
		{2, 5 * time.Second},
		{3, 30 * time.Second},
		{4, 30 * time.Second},
		{5, 30 * time.Second},
		{6, 2 * time.Minute},
		{7, 2 * time.Minute},
		{8, 2 * time.Minute},
		{9, 2 * time.Minute},
		{10, 5 * time.Minute},
		{11, 5 * time.Minute},
		{12, 5 * time.Minute},
	}

	for _, tt := range tests {
		got := csrPollBackoff(tt.attempts)
		if got != tt.expected {
			t.Errorf("csrPollBackoff(%d) = %v, want %v", tt.attempts, got, tt.expected)
		}
	}
}

// generateTestCert creates a self-signed CA certificate and key pair for testing.
// Returns PEM-encoded certificate, PEM-encoded private key.
// The cert includes 127.0.0.1 as an IP SAN for use with httptest.NewTLSServer.
func generateTestCert(t *testing.T) ([]byte, []byte) {
	t.Helper()
	return generateTestCertWithExpiry(t, 24*time.Hour)
}

// generateTestCertWithExpiry creates a self-signed certificate with a specific validity duration.
func generateTestCertWithExpiry(t *testing.T, validity time.Duration) ([]byte, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating test key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test-ca"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(validity),
		IsCA:         true,
		KeyUsage:     x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating test certificate: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return certPEM, keyPEM
}

func TestBuildExternalCAHTTPClient_Minimal(t *testing.T) {
	ext := &openvoxv1alpha1.ExternalCASpec{
		URL: "https://puppet-ca.example.com:8140",
	}
	c := setupTestClient()

	httpClient, err := buildExternalCAHTTPClient(testCtx(), c, ext, testNamespace)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	transport := httpClient.Transport.(*http.Transport)
	if transport.TLSClientConfig.InsecureSkipVerify {
		t.Error("expected InsecureSkipVerify=false")
	}
	if transport.TLSClientConfig.RootCAs != nil {
		t.Error("expected no custom RootCAs")
	}
}

func TestBuildExternalCAHTTPClient_InsecureSkipVerify(t *testing.T) {
	ext := &openvoxv1alpha1.ExternalCASpec{
		URL:                "https://puppet-ca.example.com:8140",
		InsecureSkipVerify: true,
	}
	c := setupTestClient()

	httpClient, err := buildExternalCAHTTPClient(testCtx(), c, ext, testNamespace)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	transport := httpClient.Transport.(*http.Transport)
	if !transport.TLSClientConfig.InsecureSkipVerify {
		t.Error("expected InsecureSkipVerify=true")
	}
}

func TestBuildExternalCAHTTPClient_WithCASecret(t *testing.T) {
	certPEM, _ := generateTestCert(t)
	caSecret := newSecret("ca-secret", map[string][]byte{
		"ca_crt.pem": certPEM,
	})
	ext := &openvoxv1alpha1.ExternalCASpec{
		URL:         "https://puppet-ca.example.com:8140",
		CASecretRef: "ca-secret",
	}
	c := setupTestClient(caSecret)

	httpClient, err := buildExternalCAHTTPClient(testCtx(), c, ext, testNamespace)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	transport := httpClient.Transport.(*http.Transport)
	if transport.TLSClientConfig.RootCAs == nil {
		t.Error("expected custom RootCAs pool")
	}
}

func TestBuildExternalCAHTTPClient_CASecretNotFound(t *testing.T) {
	ext := &openvoxv1alpha1.ExternalCASpec{
		URL:         "https://puppet-ca.example.com:8140",
		CASecretRef: "missing-secret",
	}
	c := setupTestClient()

	_, err := buildExternalCAHTTPClient(testCtx(), c, ext, testNamespace)
	if err == nil {
		t.Fatal("expected error when CA secret is missing")
	}
}

func TestBuildExternalCAHTTPClient_WithTLSSecret(t *testing.T) {
	certPEM, keyPEM := generateTestCert(t)
	tlsSecret := newSecret("tls-secret", map[string][]byte{
		"tls.crt": certPEM,
		"tls.key": keyPEM,
	})
	ext := &openvoxv1alpha1.ExternalCASpec{
		URL:          "https://puppet-ca.example.com:8140",
		TLSSecretRef: "tls-secret",
	}
	c := setupTestClient(tlsSecret)

	httpClient, err := buildExternalCAHTTPClient(testCtx(), c, ext, testNamespace)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	transport := httpClient.Transport.(*http.Transport)
	if len(transport.TLSClientConfig.Certificates) != 1 {
		t.Errorf("expected 1 client certificate, got %d", len(transport.TLSClientConfig.Certificates))
	}
}

func TestBuildExternalCAHTTPClient_TLSSecretMissingKey(t *testing.T) {
	certPEM, _ := generateTestCert(t)
	// Secret has tls.crt but missing tls.key
	tlsSecret := newSecret("tls-secret", map[string][]byte{
		"tls.crt": certPEM,
	})
	ext := &openvoxv1alpha1.ExternalCASpec{
		URL:          "https://puppet-ca.example.com:8140",
		TLSSecretRef: "tls-secret",
	}
	c := setupTestClient(tlsSecret)

	_, err := buildExternalCAHTTPClient(testCtx(), c, ext, testNamespace)
	if err == nil {
		t.Fatal("expected error when TLS secret is missing tls.key")
	}
}

func TestSignCSRViaAPI_Success(t *testing.T) {
	certPEM, keyPEM := generateTestCert(t)

	// Start test HTTPS server that accepts the sign request
	server := newTestTLSServer(t, certPEM, keyPEM, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		if !strings.Contains(r.URL.Path, "/puppet-ca/v1/certificate_status/test-certname") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", r.Header.Get("Content-Type"))
		}
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"desired_state": "signed"`) {
			t.Errorf("expected desired_state signed in body, got %s", string(body))
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	// Create secrets: CA public cert and signing secret (CA server cert+key)
	caSecret := newSecret("test-ca-ca", map[string][]byte{
		"ca_crt.pem": certPEM,
	})
	signingSecret := newSecret("ca-cert-tls", map[string][]byte{
		"cert.pem": certPEM,
		"key.pem":  keyPEM,
	})

	ca := newCertificateAuthority("test-ca")
	ca.Status.SigningSecretName = "ca-cert-tls"

	cert := newCertificate("my-cert", "test-ca", openvoxv1alpha1.CertificatePhaseRequesting)
	cert.Spec.Certname = "test-certname"

	c := setupTestClient(ca, cert, caSecret, signingSecret)
	r := newCertificateReconciler(c)

	err := r.signCSRViaAPI(testCtx(), cert, ca, server.URL, testNamespace)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSignCSRViaAPI_MissingSigningSecret(t *testing.T) {
	ca := newCertificateAuthority("test-ca")
	ca.Status.SigningSecretName = "nonexistent-secret"

	cert := newCertificate("my-cert", "test-ca", openvoxv1alpha1.CertificatePhaseRequesting)
	cert.Spec.Certname = "test-certname"

	// CA public cert exists but signing secret does not
	certPEM, _ := generateTestCert(t)
	caSecret := newSecret("test-ca-ca", map[string][]byte{
		"ca_crt.pem": certPEM,
	})

	c := setupTestClient(ca, cert, caSecret)
	r := newCertificateReconciler(c)

	err := r.signCSRViaAPI(testCtx(), cert, ca, "https://localhost:8140", testNamespace)
	if err == nil {
		t.Fatal("expected error when signing secret is missing")
	}
	if !strings.Contains(err.Error(), "getting signing Secret") {
		t.Errorf("expected 'getting signing Secret' error, got: %v", err)
	}
}

func TestSignCSRViaAPI_SigningSecretMissingKeys(t *testing.T) {
	certPEM, _ := generateTestCert(t)

	caSecret := newSecret("test-ca-ca", map[string][]byte{
		"ca_crt.pem": certPEM,
	})
	// Signing secret exists but is missing cert.pem/key.pem
	signingSecret := newSecret("ca-cert-tls", map[string][]byte{
		"wrong-key": []byte("data"),
	})

	ca := newCertificateAuthority("test-ca")
	ca.Status.SigningSecretName = "ca-cert-tls"

	cert := newCertificate("my-cert", "test-ca", openvoxv1alpha1.CertificatePhaseRequesting)

	c := setupTestClient(ca, cert, caSecret, signingSecret)
	r := newCertificateReconciler(c)

	err := r.signCSRViaAPI(testCtx(), cert, ca, "https://localhost:8140", testNamespace)
	if err == nil {
		t.Fatal("expected error when signing secret is missing cert.pem/key.pem")
	}
	if !strings.Contains(err.Error(), "missing cert.pem or key.pem") {
		t.Errorf("expected 'missing cert.pem or key.pem' error, got: %v", err)
	}
}

func TestSignCSRViaAPI_CARejectsRequest(t *testing.T) {
	certPEM, keyPEM := generateTestCert(t)

	server := newTestTLSServer(t, certPEM, keyPEM, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("Not Authorized"))
	}))
	defer server.Close()

	caSecret := newSecret("test-ca-ca", map[string][]byte{
		"ca_crt.pem": certPEM,
	})
	signingSecret := newSecret("ca-cert-tls", map[string][]byte{
		"cert.pem": certPEM,
		"key.pem":  keyPEM,
	})

	ca := newCertificateAuthority("test-ca")
	ca.Status.SigningSecretName = "ca-cert-tls"

	cert := newCertificate("my-cert", "test-ca", openvoxv1alpha1.CertificatePhaseRequesting)
	cert.Spec.Certname = "test-certname"

	c := setupTestClient(ca, cert, caSecret, signingSecret)
	r := newCertificateReconciler(c)

	err := r.signCSRViaAPI(testCtx(), cert, ca, server.URL, testNamespace)
	if err == nil {
		t.Fatal("expected error when CA rejects sign request")
	}
	if !strings.Contains(err.Error(), "HTTP 403") {
		t.Errorf("expected HTTP 403 error, got: %v", err)
	}
}

func TestSignCSRViaAPI_DefaultCertname(t *testing.T) {
	certPEM, keyPEM := generateTestCert(t)

	var requestedPath string
	server := newTestTLSServer(t, certPEM, keyPEM, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	caSecret := newSecret("test-ca-ca", map[string][]byte{
		"ca_crt.pem": certPEM,
	})
	signingSecret := newSecret("ca-cert-tls", map[string][]byte{
		"cert.pem": certPEM,
		"key.pem":  keyPEM,
	})

	ca := newCertificateAuthority("test-ca")
	ca.Status.SigningSecretName = "ca-cert-tls"

	cert := newCertificate("my-cert", "test-ca", openvoxv1alpha1.CertificatePhaseRequesting)
	cert.Spec.Certname = "" // should default to "puppet"

	c := setupTestClient(ca, cert, caSecret, signingSecret)
	r := newCertificateReconciler(c)

	err := r.signCSRViaAPI(testCtx(), cert, ca, server.URL, testNamespace)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(requestedPath, "/puppet-ca/v1/certificate_status/puppet") {
		t.Errorf("expected path with default certname 'puppet', got: %s", requestedPath)
	}
}

func TestCleanCertViaAPI_Success(t *testing.T) {
	certPEM, keyPEM := generateTestCert(t)

	var requestedPath string
	var requestBody string
	server := newTestTLSServer(t, certPEM, keyPEM, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			t.Errorf("expected PUT, got %s", r.Method)
		}
		requestedPath = r.URL.Path
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("expected Content-Type application/json, got %s", r.Header.Get("Content-Type"))
		}
		body, _ := io.ReadAll(r.Body)
		requestBody = string(body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	caSecret := newSecret("test-ca-ca", map[string][]byte{
		"ca_crt.pem": certPEM,
	})
	signingSecret := newSecret("ca-cert-tls", map[string][]byte{
		"cert.pem": certPEM,
		"key.pem":  keyPEM,
	})

	ca := newCertificateAuthority("test-ca")
	ca.Status.SigningSecretName = "ca-cert-tls"

	cert := newCertificate("my-cert", "test-ca", openvoxv1alpha1.CertificatePhaseSigned)
	cert.Spec.Certname = "test-certname"

	c := setupTestClient(ca, cert, caSecret, signingSecret)
	r := newCertificateReconciler(c)

	err := r.cleanCertViaAPI(testCtx(), cert, ca, server.URL, testNamespace)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(requestedPath, "/puppet-ca/v1/clean") {
		t.Errorf("expected path containing /puppet-ca/v1/clean, got: %s", requestedPath)
	}
	if !strings.Contains(requestBody, `"test-certname"`) {
		t.Errorf("expected certname in body, got: %s", requestBody)
	}
}

func TestCleanCertViaAPI_CARejects(t *testing.T) {
	certPEM, keyPEM := generateTestCert(t)

	server := newTestTLSServer(t, certPEM, keyPEM, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("Not Authorized"))
	}))
	defer server.Close()

	caSecret := newSecret("test-ca-ca", map[string][]byte{
		"ca_crt.pem": certPEM,
	})
	signingSecret := newSecret("ca-cert-tls", map[string][]byte{
		"cert.pem": certPEM,
		"key.pem":  keyPEM,
	})

	ca := newCertificateAuthority("test-ca")
	ca.Status.SigningSecretName = "ca-cert-tls"

	cert := newCertificate("my-cert", "test-ca", openvoxv1alpha1.CertificatePhaseSigned)
	cert.Spec.Certname = "test-certname"

	c := setupTestClient(ca, cert, caSecret, signingSecret)
	r := newCertificateReconciler(c)

	err := r.cleanCertViaAPI(testCtx(), cert, ca, server.URL, testNamespace)
	if err == nil {
		t.Fatal("expected error when CA rejects clean request")
	}
	if !strings.Contains(err.Error(), "HTTP 403") {
		t.Errorf("expected HTTP 403 error, got: %v", err)
	}
}

func TestCleanCertViaAPI_EmptyCertname(t *testing.T) {
	ca := newCertificateAuthority("test-ca")
	ca.Status.SigningSecretName = "ca-cert-tls"

	cert := newCertificate("my-cert", "test-ca", openvoxv1alpha1.CertificatePhaseSigned)
	cert.Spec.Certname = ""

	c := setupTestClient(ca, cert)
	r := newCertificateReconciler(c)

	err := r.cleanCertViaAPI(testCtx(), cert, ca, "https://localhost:8140", testNamespace)
	if err == nil {
		t.Fatal("expected error when certname is empty")
	}
	if !strings.Contains(err.Error(), "certname is empty") {
		t.Errorf("expected 'certname is empty' error, got: %v", err)
	}
}

func TestReconcileCertRenewal_Success(t *testing.T) {
	certPEM, keyPEM := generateTestCertWithExpiry(t, 24*time.Hour)
	newCertPEM, newKeyPEM := generateTestCertWithExpiry(t, 90*24*time.Hour)

	server := newTestTLSServer(t, certPEM, keyPEM, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(newCertPEM)
	}))
	defer server.Close()

	caSecret := newSecret("ext-ca-ca", map[string][]byte{
		"ca_crt.pem": certPEM,
	})
	tlsSecret := newSecret("my-cert-tls", map[string][]byte{
		"cert.pem": certPEM,
		"key.pem":  keyPEM,
	})

	ca := newCertificateAuthority("ext-ca", withExternal(server.URL))
	ca.Status.Phase = openvoxv1alpha1.CertificateAuthorityPhaseExternal

	cert := newCertificate("my-cert", "ext-ca", openvoxv1alpha1.CertificatePhaseRenewing)
	cert.Spec.Certname = "test-certname"

	c := setupTestClient(ca, cert, caSecret, tlsSecret)
	r := newCertificateReconciler(c)

	res, err := r.reconcileCertRenewal(testCtx(), cert, ca)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify phase transitioned to Signed
	updated := &openvoxv1alpha1.Certificate{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "my-cert", Namespace: testNamespace}, updated); err != nil {
		t.Fatalf("failed to get Certificate: %v", err)
	}
	if updated.Status.Phase != openvoxv1alpha1.CertificatePhaseSigned {
		t.Errorf("expected phase %q, got %q", openvoxv1alpha1.CertificatePhaseSigned, updated.Status.Phase)
	}
	if updated.Status.SecretName != "my-cert-tls" {
		t.Errorf("expected SecretName %q, got %q", "my-cert-tls", updated.Status.SecretName)
	}

	// Verify CertSigned condition with reason CertificateRenewed
	found := false
	for _, cond := range updated.Status.Conditions {
		if cond.Type == openvoxv1alpha1.ConditionCertSigned && cond.Reason == "CertificateRenewed" {
			found = true
			if cond.Status != "True" {
				t.Errorf("expected condition status True, got %q", cond.Status)
			}
		}
	}
	if !found {
		t.Error("CertSigned condition with reason CertificateRenewed not set")
	}

	// Verify renewal annotation was set
	if updated.Annotations == nil || updated.Annotations[AnnotationLastRenewalTime] == "" {
		t.Error("expected last-renewal-time annotation to be set")
	}

	// Verify expiry-warned annotation was cleared
	if warned, ok := updated.Annotations[AnnotationExpiryWarned]; ok && warned != "" {
		t.Errorf("expected expiry-warned annotation to be cleared, got %q", warned)
	}

	// Verify RequeueAfter is set (scheduleRenewalCheck called)
	if res.RequeueAfter == 0 {
		t.Error("expected RequeueAfter > 0 after successful renewal")
	}

	// Verify the TLS secret was updated with new cert
	tlsUpdated := &corev1.Secret{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "my-cert-tls", Namespace: testNamespace}, tlsUpdated); err != nil {
		t.Fatalf("failed to get TLS Secret: %v", err)
	}
	if string(tlsUpdated.Data["cert.pem"]) == string(certPEM) {
		t.Error("expected TLS secret cert.pem to be updated with new cert")
	}

	_ = newKeyPEM // new key is generated internally
}

func TestRenewCertificate_Success(t *testing.T) {
	certPEM, keyPEM := generateTestCertWithExpiry(t, 24*time.Hour)

	// Start test HTTPS server that accepts the renewal request and returns a new cert
	newCertPEM, _ := generateTestCertWithExpiry(t, 90*24*time.Hour)
	server := newTestTLSServer(t, certPEM, keyPEM, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if !strings.Contains(r.URL.Path, "/puppet-ca/v1/certificate_renewal") {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Content-Type") != "text/plain" {
			t.Errorf("expected Content-Type text/plain, got %s", r.Header.Get("Content-Type"))
		}
		// Read CSR body
		body, _ := io.ReadAll(r.Body)
		if len(body) == 0 {
			t.Error("expected CSR in request body")
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(newCertPEM)
	}))
	defer server.Close()

	caSecret := newSecret("test-ca-ca", map[string][]byte{
		"ca_crt.pem": certPEM,
	})
	tlsSecret := newSecret("my-cert-tls", map[string][]byte{
		"cert.pem": certPEM,
		"key.pem":  keyPEM,
	})

	ca := newCertificateAuthority("test-ca")
	cert := newCertificate("my-cert", "test-ca", openvoxv1alpha1.CertificatePhaseRenewing)
	cert.Spec.Certname = "test-certname"

	c := setupTestClient(ca, cert, caSecret, tlsSecret)
	r := newCertificateReconciler(c)

	err := r.renewCertificate(testCtx(), cert, ca, server.URL, testNamespace)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRenewCertificate_CADown(t *testing.T) {
	certPEM, keyPEM := generateTestCertWithExpiry(t, 24*time.Hour)

	// Server returns an error
	server := newTestTLSServer(t, certPEM, keyPEM, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("Service Unavailable"))
	}))
	defer server.Close()

	caSecret := newSecret("test-ca-ca", map[string][]byte{
		"ca_crt.pem": certPEM,
	})
	tlsSecret := newSecret("my-cert-tls", map[string][]byte{
		"cert.pem": certPEM,
		"key.pem":  keyPEM,
	})

	ca := newCertificateAuthority("test-ca")
	cert := newCertificate("my-cert", "test-ca", openvoxv1alpha1.CertificatePhaseRenewing)
	cert.Spec.Certname = "test-certname"

	c := setupTestClient(ca, cert, caSecret, tlsSecret)
	r := newCertificateReconciler(c)

	err := r.renewCertificate(testCtx(), cert, ca, server.URL, testNamespace)
	if err == nil {
		t.Fatal("expected error when CA returns error")
	}
	if !strings.Contains(err.Error(), "HTTP 503") {
		t.Errorf("expected HTTP 503 error, got: %v", err)
	}
}

func TestRenewCertificate_mTLSAuth(t *testing.T) {
	certPEM, keyPEM := generateTestCertWithExpiry(t, 24*time.Hour)

	newCertPEM, _ := generateTestCertWithExpiry(t, 90*24*time.Hour)

	// Verify that the client presents a certificate (mTLS)
	var clientCertPresented bool
	server := newTestTLSServer(t, certPEM, keyPEM, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
			clientCertPresented = true
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(newCertPEM)
	}))
	// Enable client cert verification on the server
	caCertPool := x509.NewCertPool()
	caCertPool.AppendCertsFromPEM(certPEM)
	server.TLS.ClientAuth = tls.VerifyClientCertIfGiven
	server.TLS.ClientCAs = caCertPool
	defer server.Close()

	caSecret := newSecret("test-ca-ca", map[string][]byte{
		"ca_crt.pem": certPEM,
	})
	tlsSecret := newSecret("my-cert-tls", map[string][]byte{
		"cert.pem": certPEM,
		"key.pem":  keyPEM,
	})

	ca := newCertificateAuthority("test-ca")
	cert := newCertificate("my-cert", "test-ca", openvoxv1alpha1.CertificatePhaseRenewing)
	cert.Spec.Certname = "test-certname"

	c := setupTestClient(ca, cert, caSecret, tlsSecret)
	r := newCertificateReconciler(c)

	err := r.renewCertificate(testCtx(), cert, ca, server.URL, testNamespace)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !clientCertPresented {
		t.Error("expected client certificate to be presented for mTLS authentication")
	}
}

func TestSubmitCSR_Success(t *testing.T) {
	certPEM, keyPEM := generateTestCert(t)

	var csrReceived bool
	server := newTestTLSServer(t, certPEM, keyPEM, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/puppet-ca/v1/certificate_request/") {
			csrReceived = true
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	caSecret := newSecret("test-ca-ca", map[string][]byte{
		"ca_crt.pem": certPEM,
	})
	ca := newCertificateAuthority("test-ca")
	cert := newCertificate("my-cert", "test-ca", "")
	cert.Spec.Certname = "test-node"

	c := setupTestClient(ca, cert, caSecret)
	r := newCertificateReconciler(c)

	res, err := r.submitCSR(testCtx(), cert, ca, server.URL, testNamespace)
	if err != nil {
		t.Fatalf("submitCSR: %v", err)
	}
	if !csrReceived {
		t.Error("expected CSR to be submitted to the server")
	}

	// Should have created a pending secret with the key
	pendingSecret := &corev1.Secret{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "my-cert-tls-pending", Namespace: testNamespace}, pendingSecret); err != nil {
		t.Fatalf("pending Secret not created: %v", err)
	}
	if len(pendingSecret.Data["key.pem"]) == 0 {
		t.Error("pending Secret should contain key.pem")
	}
	_ = res
}

func TestSubmitCSR_AlreadyPending(t *testing.T) {
	certPEM, keyPEM := generateTestCert(t)

	server := newTestTLSServer(t, certPEM, keyPEM, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("already has a requested certificate"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	caSecret := newSecret("test-ca-ca", map[string][]byte{
		"ca_crt.pem": certPEM,
	})
	ca := newCertificateAuthority("test-ca")
	cert := newCertificate("my-cert", "test-ca", "")
	cert.Spec.Certname = "test-node"

	c := setupTestClient(ca, cert, caSecret)
	r := newCertificateReconciler(c)

	_, err := r.submitCSR(testCtx(), cert, ca, server.URL, testNamespace)
	if err != nil {
		t.Fatalf("submitCSR should not error for already-pending: %v", err)
	}
}

func TestSubmitCSR_Rejected(t *testing.T) {
	certPEM, keyPEM := generateTestCert(t)

	server := newTestTLSServer(t, certPEM, keyPEM, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("forbidden"))
	}))
	defer server.Close()

	caSecret := newSecret("test-ca-ca", map[string][]byte{
		"ca_crt.pem": certPEM,
	})
	ca := newCertificateAuthority("test-ca")
	cert := newCertificate("my-cert", "test-ca", "")
	cert.Spec.Certname = "test-node"

	c := setupTestClient(ca, cert, caSecret)
	r := newCertificateReconciler(c)

	_, err := r.submitCSR(testCtx(), cert, ca, server.URL, testNamespace)
	if err == nil {
		t.Fatal("expected error for rejected CSR")
	}
}

func TestSubmitCSR_ExistingPendingKey(t *testing.T) {
	certPEM, keyPEM := generateTestCert(t)

	var csrReceived bool
	server := newTestTLSServer(t, certPEM, keyPEM, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPut {
			csrReceived = true
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	caSecret := newSecret("test-ca-ca", map[string][]byte{
		"ca_crt.pem": certPEM,
	})
	// Pre-create the pending secret with a key
	pendingSecret := newSecret("my-cert-tls-pending", map[string][]byte{
		"key.pem": keyPEM,
	})

	ca := newCertificateAuthority("test-ca")
	cert := newCertificate("my-cert", "test-ca", "")
	cert.Spec.Certname = "test-node"

	c := setupTestClient(ca, cert, caSecret, pendingSecret)
	r := newCertificateReconciler(c)

	_, err := r.submitCSR(testCtx(), cert, ca, server.URL, testNamespace)
	if err != nil {
		t.Fatalf("submitCSR with existing key: %v", err)
	}
	if !csrReceived {
		t.Error("expected CSR to be submitted even with existing key")
	}
}

func TestFetchSignedCert_Signed(t *testing.T) {
	certPEM, keyPEM := generateTestCert(t)

	server := newTestTLSServer(t, certPEM, keyPEM, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/puppet-ca/v1/certificate/") {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(certPEM) // return the cert as "signed"
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	caSecret := newSecret("test-ca-ca", map[string][]byte{
		"ca_crt.pem": certPEM,
	})
	ca := newCertificateAuthority("test-ca")
	cert := newCertificate("my-cert", "test-ca", openvoxv1alpha1.CertificatePhaseRequesting)
	cert.Spec.Certname = "test-node"

	c := setupTestClient(ca, cert, caSecret)
	r := newCertificateReconciler(c)

	signedCert, err := r.fetchSignedCert(testCtx(), cert, ca, server.URL, testNamespace)
	if err != nil {
		t.Fatalf("fetchSignedCert: %v", err)
	}
	if signedCert == nil {
		t.Fatal("expected signed cert to be returned")
	}
}

func TestFetchSignedCert_NotYetSigned(t *testing.T) {
	certPEM, keyPEM := generateTestCert(t)

	server := newTestTLSServer(t, certPEM, keyPEM, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	caSecret := newSecret("test-ca-ca", map[string][]byte{
		"ca_crt.pem": certPEM,
	})
	ca := newCertificateAuthority("test-ca")
	cert := newCertificate("my-cert", "test-ca", openvoxv1alpha1.CertificatePhaseRequesting)
	cert.Spec.Certname = "test-node"

	c := setupTestClient(ca, cert, caSecret)
	r := newCertificateReconciler(c)

	signedCert, err := r.fetchSignedCert(testCtx(), cert, ca, server.URL, testNamespace)
	if err != nil {
		t.Fatalf("fetchSignedCert: %v", err)
	}
	if signedCert != nil {
		t.Error("expected nil for unsigned cert")
	}
}

func TestFetchSignedCert_EmptyCertname(t *testing.T) {
	certPEM, keyPEM := generateTestCert(t)

	var requestedPath string
	server := newTestTLSServer(t, certPEM, keyPEM, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestedPath = r.URL.Path
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(certPEM)
	}))
	defer server.Close()

	caSecret := newSecret("test-ca-ca", map[string][]byte{
		"ca_crt.pem": certPEM,
	})
	ca := newCertificateAuthority("test-ca")
	cert := newCertificate("my-cert", "test-ca", openvoxv1alpha1.CertificatePhaseRequesting)
	cert.Spec.Certname = "" // empty -> defaults to "puppet"

	c := setupTestClient(ca, cert, caSecret)
	r := newCertificateReconciler(c)

	_, err := r.fetchSignedCert(testCtx(), cert, ca, server.URL, testNamespace)
	if err != nil {
		t.Fatalf("fetchSignedCert: %v", err)
	}
	if !strings.Contains(requestedPath, "/puppet") {
		t.Errorf("expected path to contain /puppet for empty certname, got %s", requestedPath)
	}
}

func TestSignCertificate_FullFlow(t *testing.T) {
	certPEM, keyPEM := generateTestCert(t)

	// Server handles CSR submit and cert fetch
	server := newTestTLSServer(t, certPEM, keyPEM, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/certificate_request/"):
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/certificate/"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(certPEM)
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/certificate_status/"):
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	caSecret := newSecret("test-ca-ca", map[string][]byte{
		"ca_crt.pem": certPEM,
	})
	signingSecret := newSecret("ca-cert-tls", map[string][]byte{
		"cert.pem": certPEM,
		"key.pem":  keyPEM,
	})

	ca := newCertificateAuthority("test-ca")
	ca.Status.SigningSecretName = "ca-cert-tls"

	cert := newCertificate("my-cert", "test-ca", "")
	cert.Spec.Certname = "test-node"

	c := setupTestClient(ca, cert, caSecret, signingSecret)
	r := newCertificateReconciler(c)

	res, err := r.signCertificate(testCtx(), cert, ca, server.URL, testNamespace)
	if err != nil {
		t.Fatalf("signCertificate: %v", err)
	}

	// Should have created the TLS secret
	tlsSecret := &corev1.Secret{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "my-cert-tls", Namespace: testNamespace}, tlsSecret); err != nil {
		t.Fatalf("TLS Secret not created: %v", err)
	}
	if len(tlsSecret.Data["cert.pem"]) == 0 {
		t.Error("TLS Secret should contain cert.pem")
	}
	if len(tlsSecret.Data["key.pem"]) == 0 {
		t.Error("TLS Secret should contain key.pem")
	}

	// Pending secret should be cleaned up
	pendingSecret := &corev1.Secret{}
	err = c.Get(testCtx(), types.NamespacedName{Name: "my-cert-tls-pending", Namespace: testNamespace}, pendingSecret)
	if err == nil {
		t.Error("pending Secret should have been deleted")
	}
	_ = res
}

func TestEnsurePendingKey_New(t *testing.T) {
	cert := newCertificate("my-cert", "test-ca", "")
	c := setupTestClient(cert)
	r := newCertificateReconciler(c)

	keyPEM, err := r.ensurePendingKey(testCtx(), cert, "my-cert-tls-pending", testNamespace)
	if err != nil {
		t.Fatalf("ensurePendingKey: %v", err)
	}
	if len(keyPEM) == 0 {
		t.Fatal("expected non-empty key PEM")
	}

	// Verify the secret was created
	secret := &corev1.Secret{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "my-cert-tls-pending", Namespace: testNamespace}, secret); err != nil {
		t.Fatalf("pending Secret not created: %v", err)
	}
}

func TestEnsurePendingKey_Existing(t *testing.T) {
	cert := newCertificate("my-cert", "test-ca", "")
	existingKey := []byte("-----BEGIN RSA PRIVATE KEY-----\nexisting-key\n-----END RSA PRIVATE KEY-----\n")
	pendingSecret := newSecret("my-cert-tls-pending", map[string][]byte{
		"key.pem": existingKey,
	})

	c := setupTestClient(cert, pendingSecret)
	r := newCertificateReconciler(c)

	keyPEM, err := r.ensurePendingKey(testCtx(), cert, "my-cert-tls-pending", testNamespace)
	if err != nil {
		t.Fatalf("ensurePendingKey: %v", err)
	}
	if string(keyPEM) != string(existingKey) {
		t.Error("expected existing key to be returned")
	}
}

func TestHandleCertificateCleanup_ExternalCA(t *testing.T) {
	ca := newCertificateAuthority("test-ca", withExternal("https://puppet.example.com"))
	cert := newCertificate("my-cert", "test-ca", openvoxv1alpha1.CertificatePhaseSigned)
	cert.Spec.Certname = "test-node"

	c := setupTestClient(ca, cert)
	r := newCertificateReconciler(c)

	// External CA: cleanup should be skipped gracefully
	if err := r.handleCertificateCleanup(testCtx(), cert); err != nil {
		t.Fatalf("handleCertificateCleanup: %v", err)
	}
}

func TestHandleCertificateCleanup_NoSigningSecret(t *testing.T) {
	ca := newCertificateAuthority("test-ca")
	ca.Status.SigningSecretName = ""
	cert := newCertificate("my-cert", "test-ca", openvoxv1alpha1.CertificatePhaseSigned)
	cert.Spec.Certname = "test-node"

	c := setupTestClient(ca, cert)
	r := newCertificateReconciler(c)

	// No signing secret: cleanup should be skipped
	if err := r.handleCertificateCleanup(testCtx(), cert); err != nil {
		t.Fatalf("handleCertificateCleanup: %v", err)
	}
}

func TestHandleCertificateCleanup_CANotFound(t *testing.T) {
	cert := newCertificate("my-cert", "missing-ca", openvoxv1alpha1.CertificatePhaseSigned)
	cert.Spec.Certname = "test-node"

	c := setupTestClient(cert)
	r := newCertificateReconciler(c)

	// Missing CA: should skip cleanup gracefully
	if err := r.handleCertificateCleanup(testCtx(), cert); err != nil {
		t.Fatalf("handleCertificateCleanup: %v", err)
	}
}

func TestReconcileCertSigning_ExternalCA(t *testing.T) {
	// Use long-lived cert to avoid immediate renewal trigger (default renewBefore is 60d)
	certPEM, keyPEM := generateTestCertWithExpiry(t, 365*24*time.Hour)

	// Server returns the signed cert immediately
	server := newTestTLSServer(t, certPEM, keyPEM, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/certificate_request/"):
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/certificate/"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(certPEM)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	ca := newCertificateAuthority("ext-ca", withExternal(server.URL))
	ca.Spec.External.InsecureSkipVerify = true
	ca.Status.Phase = openvoxv1alpha1.CertificateAuthorityPhaseExternal

	cert := newCertificate("my-cert", "ext-ca", "")
	cert.Spec.Certname = "test-node"

	c := setupTestClient(ca, cert)
	r := newCertificateReconciler(c)

	res, err := r.reconcileCertSigning(testCtx(), cert, ca)
	if err != nil {
		t.Fatalf("reconcileCertSigning: %v", err)
	}

	// Should have created TLS secret and updated status
	updated := &openvoxv1alpha1.Certificate{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "my-cert", Namespace: testNamespace}, updated); err != nil {
		t.Fatalf("failed to get Certificate: %v", err)
	}
	if updated.Status.Phase != openvoxv1alpha1.CertificatePhaseSigned {
		t.Errorf("expected phase %q, got %q", openvoxv1alpha1.CertificatePhaseSigned, updated.Status.Phase)
	}
	if updated.Status.SecretName != "my-cert-tls" {
		t.Errorf("expected SecretName %q, got %q", "my-cert-tls", updated.Status.SecretName)
	}
	_ = res
}

func TestReconcileCertSigning_Error(t *testing.T) {
	certPEM, keyPEM := generateTestCert(t)

	// Server rejects all requests
	server := newTestTLSServer(t, certPEM, keyPEM, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("server error"))
	}))
	defer server.Close()

	ca := newCertificateAuthority("ext-ca", withExternal(server.URL))
	ca.Spec.External.InsecureSkipVerify = true
	ca.Status.Phase = openvoxv1alpha1.CertificateAuthorityPhaseExternal

	cert := newCertificate("my-cert", "ext-ca", "")
	cert.Spec.Certname = "test-node"

	c := setupTestClient(ca, cert)
	r := newCertificateReconciler(c)

	res, err := r.reconcileCertSigning(testCtx(), cert, ca)
	if err != nil {
		// reconcileCertSigning swallows errors and requeues
		t.Fatalf("reconcileCertSigning should not return error: %v", err)
	}
	if res.RequeueAfter == 0 {
		t.Error("expected requeue after error")
	}

	// Phase should be Error
	updated := &openvoxv1alpha1.Certificate{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "my-cert", Namespace: testNamespace}, updated); err != nil {
		t.Fatalf("failed to get Certificate: %v", err)
	}
	if updated.Status.Phase != openvoxv1alpha1.CertificatePhaseError {
		t.Errorf("expected phase %q, got %q", openvoxv1alpha1.CertificatePhaseError, updated.Status.Phase)
	}
}

func TestSignCertificate_WaitingForSigning(t *testing.T) {
	certPEM, keyPEM := generateTestCert(t)

	// Server accepts CSR but never returns signed cert
	server := newTestTLSServer(t, certPEM, keyPEM, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/certificate_request/"):
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodGet && strings.Contains(r.URL.Path, "/certificate/"):
			w.WriteHeader(http.StatusNotFound) // not yet signed
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/certificate_status/"):
			w.WriteHeader(http.StatusForbidden) // signing fails
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	caSecret := newSecret("test-ca-ca", map[string][]byte{
		"ca_crt.pem": certPEM,
	})
	signingSecret := newSecret("ca-cert-tls", map[string][]byte{
		"cert.pem": certPEM,
		"key.pem":  keyPEM,
	})

	ca := newCertificateAuthority("test-ca")
	ca.Status.SigningSecretName = "ca-cert-tls"

	cert := newCertificate("my-cert", "test-ca", "")
	cert.Spec.Certname = "test-node"

	c := setupTestClient(ca, cert, caSecret, signingSecret)
	r := newCertificateReconciler(c)

	res, err := r.signCertificate(testCtx(), cert, ca, server.URL, testNamespace)
	if err != nil {
		t.Fatalf("signCertificate: %v", err)
	}

	// Should requeue since cert is not yet signed
	if res.RequeueAfter == 0 {
		t.Error("expected RequeueAfter > 0 when cert is not yet signed")
	}

	// Pending secret should still exist
	pendingSecret := &corev1.Secret{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "my-cert-tls-pending", Namespace: testNamespace}, pendingSecret); err != nil {
		t.Fatalf("pending Secret should still exist: %v", err)
	}
}

// newTestTLSServer creates a test HTTPS server using the given cert/key for TLS.
func newTestTLSServer(t *testing.T, certPEM, keyPEM []byte, handler http.Handler) *httptest.Server {
	t.Helper()
	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("loading test TLS cert: %v", err)
	}
	server := httptest.NewUnstartedServer(handler)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{tlsCert}}
	server.StartTLS()
	return server
}
