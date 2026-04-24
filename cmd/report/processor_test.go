package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestForward_Generic(t *testing.T) {
	var receivedBody []byte
	var receivedContentType string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedContentType = r.Header.Get("Content-Type")
		receivedBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	report := `{"host":"test.example.com","status":"changed"}`
	endpoint := EndpointConfig{
		URL:            ts.URL,
		TimeoutSeconds: 5,
	}

	if err := forward(endpoint, []byte(report)); err != nil {
		t.Fatalf("forward: %v", err)
	}

	if receivedContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", receivedContentType)
	}
	if string(receivedBody) != report {
		t.Errorf("body = %q, want %q", string(receivedBody), report)
	}
}

func TestForward_PuppetDB(t *testing.T) {
	var receivedBody []byte
	var receivedPath string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		receivedBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"uuid":"test-uuid"}`))
	}))
	defer ts.Close()

	report := sampleReport()
	reportJSON, _ := json.Marshal(report)

	endpoint := EndpointConfig{
		Processor:      "puppetdb",
		URL:            ts.URL,
		TimeoutSeconds: 5,
	}

	if err := forward(endpoint, reportJSON); err != nil {
		t.Fatalf("forward: %v", err)
	}

	if receivedPath != "/pdb/cmd/v1" {
		t.Errorf("path = %q, want /pdb/cmd/v1", receivedPath)
	}

	var cmd PuppetDBCommand
	if err := json.Unmarshal(receivedBody, &cmd); err != nil {
		t.Fatalf("parsing received body: %v", err)
	}
	if cmd.Command != "store report" {
		t.Errorf("command = %q, want 'store report'", cmd.Command)
	}
	if cmd.Version != 8 {
		t.Errorf("version = %d, want 8", cmd.Version)
	}
}

func TestForward_PuppetDB_TrailingSlash(t *testing.T) {
	var receivedPath string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"uuid":"test-uuid"}`))
	}))
	defer ts.Close()

	report := sampleReport()
	reportJSON, _ := json.Marshal(report)

	endpoint := EndpointConfig{
		Processor:      "puppetdb",
		URL:            ts.URL + "/",
		TimeoutSeconds: 5,
	}

	if err := forward(endpoint, reportJSON); err != nil {
		t.Fatalf("forward: %v", err)
	}

	if receivedPath != "/pdb/cmd/v1" {
		t.Errorf("path = %q, want /pdb/cmd/v1", receivedPath)
	}
}

func TestForward_BearerAuth(t *testing.T) {
	var receivedAuth string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	endpoint := EndpointConfig{
		URL:            ts.URL,
		TimeoutSeconds: 5,
		Auth: AuthConfig{
			Type:  "bearer",
			Token: "my-bearer-token",
		},
	}

	if err := forward(endpoint, []byte(`{}`)); err != nil {
		t.Fatalf("forward: %v", err)
	}

	if receivedAuth != "Bearer my-bearer-token" {
		t.Errorf("Authorization = %q, want 'Bearer my-bearer-token'", receivedAuth)
	}
}

func TestForward_TokenAuth(t *testing.T) {
	var receivedToken string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedToken = r.Header.Get("X-Report-Token")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	endpoint := EndpointConfig{
		URL:            ts.URL,
		TimeoutSeconds: 5,
		Auth: AuthConfig{
			Type:   "token",
			Header: "X-Report-Token",
			Token:  "secret-123",
		},
	}

	if err := forward(endpoint, []byte(`{}`)); err != nil {
		t.Fatalf("forward: %v", err)
	}

	if receivedToken != "secret-123" {
		t.Errorf("X-Report-Token = %q, want secret-123", receivedToken)
	}
}

func TestForward_BasicAuth(t *testing.T) {
	var receivedUser, receivedPass string
	var receivedOK bool

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedUser, receivedPass, receivedOK = r.BasicAuth()
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	endpoint := EndpointConfig{
		URL:            ts.URL,
		TimeoutSeconds: 5,
		Auth: AuthConfig{
			Type:     "basic",
			Username: "admin",
			Password: "pass123",
		},
	}

	if err := forward(endpoint, []byte(`{}`)); err != nil {
		t.Fatalf("forward: %v", err)
	}

	if !receivedOK {
		t.Error("BasicAuth not set")
	}
	if receivedUser != "admin" {
		t.Errorf("username = %q, want admin", receivedUser)
	}
	if receivedPass != "pass123" {
		t.Errorf("password = %q, want pass123", receivedPass)
	}
}

func TestForward_CustomHeaders(t *testing.T) {
	var headers http.Header

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		headers = r.Header
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	endpoint := EndpointConfig{
		URL:            ts.URL,
		TimeoutSeconds: 5,
		Headers: []HeaderConfig{
			{Name: "X-Custom-One", Value: "value1"},
			{Name: "X-Custom-Two", Value: "value2"},
		},
	}

	if err := forward(endpoint, []byte(`{}`)); err != nil {
		t.Fatalf("forward: %v", err)
	}

	if headers.Get("X-Custom-One") != "value1" {
		t.Errorf("X-Custom-One = %q", headers.Get("X-Custom-One"))
	}
	if headers.Get("X-Custom-Two") != "value2" {
		t.Errorf("X-Custom-Two = %q", headers.Get("X-Custom-Two"))
	}
}

func TestForward_ServerError(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal error"))
	}))
	defer ts.Close()

	endpoint := EndpointConfig{
		URL:            ts.URL,
		TimeoutSeconds: 5,
	}

	err := forward(endpoint, []byte(`{}`))
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	if got := err.Error(); got == "" {
		t.Error("error message should not be empty")
	}
}

func TestForward_Timeout(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	endpoint := EndpointConfig{
		URL:            ts.URL,
		TimeoutSeconds: 1,
	}

	err := forward(endpoint, []byte(`{}`))
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestForward_NoAuth(t *testing.T) {
	var receivedAuth string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	endpoint := EndpointConfig{
		URL:            ts.URL,
		TimeoutSeconds: 5,
	}

	if err := forward(endpoint, []byte(`{}`)); err != nil {
		t.Fatalf("forward: %v", err)
	}

	if receivedAuth != "" {
		t.Errorf("Authorization should be empty when no auth configured, got %q", receivedAuth)
	}
}

func TestLoadReportConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.yaml")

	content := `endpoints:
  - name: puppetdb
    processor: puppetdb
    url: https://puppetdb.example.com
    timeoutSeconds: 15
    auth:
      type: bearer
      token: my-token
  - name: splunk
    url: https://splunk.example.com/services/collector
    headers:
      - name: Authorization
        value: "Splunk secret-token"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadReportConfig(path)
	if err != nil {
		t.Fatalf("loadReportConfig: %v", err)
	}

	if len(cfg.Endpoints) != 2 {
		t.Fatalf("expected 2 endpoints, got %d", len(cfg.Endpoints))
	}
	if cfg.Endpoints[0].Name != "puppetdb" {
		t.Errorf("endpoint[0].Name = %q", cfg.Endpoints[0].Name)
	}
	if cfg.Endpoints[0].Processor != "puppetdb" {
		t.Errorf("endpoint[0].Processor = %q", cfg.Endpoints[0].Processor)
	}
	if cfg.Endpoints[0].TimeoutSeconds != 15 {
		t.Errorf("endpoint[0].TimeoutSeconds = %d, want 15", cfg.Endpoints[0].TimeoutSeconds)
	}
	if cfg.Endpoints[1].TimeoutSeconds != 30 {
		t.Errorf("endpoint[1].TimeoutSeconds = %d, want 30 (default)", cfg.Endpoints[1].TimeoutSeconds)
	}
}

func TestLoadReportConfig_FileNotFound(t *testing.T) {
	_, err := loadReportConfig("/nonexistent/report.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadReportConfig_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.yaml")
	if err := os.WriteFile(path, []byte(":\n\t- invalid\x00"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := loadReportConfig(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestBuildHTTPClient_Basic(t *testing.T) {
	endpoint := EndpointConfig{
		TimeoutSeconds: 20,
	}

	client, err := buildHTTPClient(endpoint)
	if err != nil {
		t.Fatalf("buildHTTPClient: %v", err)
	}
	if client.Timeout != 20*time.Second {
		t.Errorf("Timeout = %v, want 20s", client.Timeout)
	}
}

func TestBuildHTTPClient_WithValidCA(t *testing.T) {
	dir := t.TempDir()
	caFile := filepath.Join(dir, "ca.pem")

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	if err := os.WriteFile(caFile, caPEM, 0644); err != nil {
		t.Fatal(err)
	}

	endpoint := EndpointConfig{
		TimeoutSeconds: 5,
		SSL:            SSLConfig{CAFile: caFile},
	}

	client, err := buildHTTPClient(endpoint)
	if err != nil {
		t.Fatalf("buildHTTPClient: %v", err)
	}
	transport := client.Transport.(*http.Transport)
	if transport.TLSClientConfig.RootCAs == nil {
		t.Error("expected RootCAs to be set")
	}
}

func TestBuildHTTPClient_MissingCA(t *testing.T) {
	endpoint := EndpointConfig{
		TimeoutSeconds: 5,
		SSL:            SSLConfig{CAFile: "/nonexistent/ca.pem"},
	}

	_, err := buildHTTPClient(endpoint)
	if err == nil {
		t.Fatal("expected error for missing CA file")
	}
}

func TestBuildHTTPClient_InvalidCAPEM(t *testing.T) {
	dir := t.TempDir()
	caFile := filepath.Join(dir, "bad-ca.pem")
	if err := os.WriteFile(caFile, []byte("not a cert"), 0644); err != nil {
		t.Fatal(err)
	}

	endpoint := EndpointConfig{
		TimeoutSeconds: 5,
		SSL:            SSLConfig{CAFile: caFile},
	}

	_, err := buildHTTPClient(endpoint)
	if err == nil {
		t.Fatal("expected error for invalid CA PEM")
	}
}

func TestBuildHTTPClient_MTLS(t *testing.T) {
	dir := t.TempDir()

	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	clientKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	clientTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "client"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	clientDER, _ := x509.CreateCertificate(rand.Reader, clientTemplate, caTemplate, &clientKey.PublicKey, caKey)

	certFile := filepath.Join(dir, "client.pem")
	keyFile := filepath.Join(dir, "client-key.pem")

	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientDER}), 0644); err != nil {
		t.Fatal(err)
	}
	keyBytes, _ := x509.MarshalECPrivateKey(clientKey)
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes}), 0644); err != nil {
		t.Fatal(err)
	}

	endpoint := EndpointConfig{
		TimeoutSeconds: 5,
		Auth:           AuthConfig{Type: "mtls"},
		SSL: SSLConfig{
			CertFile: certFile,
			KeyFile:  keyFile,
		},
	}

	client, err := buildHTTPClient(endpoint)
	if err != nil {
		t.Fatalf("buildHTTPClient: %v", err)
	}
	transport := client.Transport.(*http.Transport)
	if len(transport.TLSClientConfig.Certificates) != 1 {
		t.Errorf("expected 1 client cert, got %d", len(transport.TLSClientConfig.Certificates))
	}
}

func TestBuildHTTPClient_MTLS_InvalidKeyPair(t *testing.T) {
	dir := t.TempDir()
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")

	if err := os.WriteFile(certFile, []byte("not a cert"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, []byte("not a key"), 0644); err != nil {
		t.Fatal(err)
	}

	endpoint := EndpointConfig{
		TimeoutSeconds: 5,
		Auth:           AuthConfig{Type: "mtls"},
		SSL: SSLConfig{
			CertFile: certFile,
			KeyFile:  keyFile,
		},
	}

	_, err := buildHTTPClient(endpoint)
	if err == nil {
		t.Fatal("expected error for invalid cert/key")
	}
}
