package controller

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

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
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating test key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test-ca"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
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
