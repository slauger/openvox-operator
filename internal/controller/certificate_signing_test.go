package controller

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
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
		KeyUsage:     x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
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
	if httpClient == nil {
		t.Fatal("expected non-nil HTTP client")
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
