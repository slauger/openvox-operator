package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestLoadENCConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "enc.yaml")

	content := `url: https://foreman.example.com
method: GET
path: /node/{certname}
responseFormat: yaml
timeoutSeconds: 30
auth:
  type: bearer
  token: my-token
cache:
  enabled: true
  directory: /var/cache/enc
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadENCConfig(path)
	if err != nil {
		t.Fatalf("loadENCConfig: %v", err)
	}

	if cfg.URL != "https://foreman.example.com" {
		t.Errorf("URL = %q", cfg.URL)
	}
	if cfg.Method != "GET" {
		t.Errorf("Method = %q", cfg.Method)
	}
	if cfg.Path != "/node/{certname}" {
		t.Errorf("Path = %q", cfg.Path)
	}
	if cfg.ResponseFormat != "yaml" {
		t.Errorf("ResponseFormat = %q", cfg.ResponseFormat)
	}
	if cfg.TimeoutSeconds != 30 {
		t.Errorf("TimeoutSeconds = %d", cfg.TimeoutSeconds)
	}
	if cfg.Auth.Type != "bearer" {
		t.Errorf("Auth.Type = %q", cfg.Auth.Type)
	}
	if cfg.Auth.Token != "my-token" {
		t.Errorf("Auth.Token = %q", cfg.Auth.Token)
	}
	if !cfg.Cache.Enabled {
		t.Error("Cache.Enabled = false")
	}
}

func TestLoadENCConfig_Defaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "enc.yaml")

	content := `url: https://example.com
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadENCConfig(path)
	if err != nil {
		t.Fatalf("loadENCConfig: %v", err)
	}

	if cfg.TimeoutSeconds != 10 {
		t.Errorf("expected default timeout 10, got %d", cfg.TimeoutSeconds)
	}
	if cfg.Method != "GET" {
		t.Errorf("expected default method GET, got %q", cfg.Method)
	}
	if cfg.ResponseFormat != "yaml" {
		t.Errorf("expected default response format yaml, got %q", cfg.ResponseFormat)
	}
}

func TestLoadENCConfig_FileNotFound(t *testing.T) {
	_, err := loadENCConfig("/nonexistent/path/enc.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestClassify_GET_YAML(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/node/web1.example.com" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Method != "GET" {
			t.Errorf("unexpected method: %s", r.Method)
		}

		w.Header().Set("Content-Type", "application/yaml")
		response := `classes:
  apache: {}
  ntp: {}
parameters:
  role: webserver
environment: production
`
		_, _ = w.Write([]byte(response))
	}))
	defer server.Close()

	cfg := &ENCConfig{
		URL:            server.URL,
		Method:         "GET",
		Path:           "/node/{certname}",
		ResponseFormat: "yaml",
		TimeoutSeconds: 5,
	}

	result, err := classify(cfg, "web1.example.com")
	if err != nil {
		t.Fatalf("classify: %v", err)
	}

	var enc ENCResult
	if err := yaml.Unmarshal([]byte(result), &enc); err != nil {
		t.Fatalf("parsing result: %v", err)
	}
	if enc.Environment != "production" {
		t.Errorf("Environment = %q", enc.Environment)
	}
}

func TestClassify_GET_JSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(ENCResult{
			Classes:     map[string]interface{}{"nginx": nil},
			Parameters:  map[string]interface{}{"role": "proxy"},
			Environment: "staging",
		})
	}))
	defer server.Close()

	cfg := &ENCConfig{
		URL:            server.URL,
		Method:         "GET",
		Path:           "/node/{certname}",
		ResponseFormat: "json",
		TimeoutSeconds: 5,
	}

	result, err := classify(cfg, "proxy1")
	if err != nil {
		t.Fatalf("classify: %v", err)
	}

	var enc ENCResult
	if err := yaml.Unmarshal([]byte(result), &enc); err != nil {
		t.Fatalf("parsing result: %v", err)
	}
	if enc.Environment != "staging" {
		t.Errorf("Environment = %q", enc.Environment)
	}
}

func TestClassify_POST(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q", ct)
		}

		w.Header().Set("Content-Type", "application/yaml")
		_, _ = w.Write([]byte("classes: {}\n"))
	}))
	defer server.Close()

	cfg := &ENCConfig{
		URL:            server.URL,
		Method:         "POST",
		Path:           "/classify",
		Body:           "certname",
		ResponseFormat: "yaml",
		TimeoutSeconds: 5,
	}

	_, err := classify(cfg, "node1")
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
}

func TestClassify_404(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("not found"))
	}))
	defer server.Close()

	cfg := &ENCConfig{
		URL:            server.URL,
		Method:         "GET",
		Path:           "/node/{certname}",
		ResponseFormat: "yaml",
		TimeoutSeconds: 5,
	}

	_, err := classify(cfg, "unknown-node")
	if err == nil {
		t.Fatal("expected error for 404")
	}
	if !isNotFound(err) {
		t.Errorf("expected notFoundError, got %T: %v", err, err)
	}
}

func TestClassify_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal error"))
	}))
	defer server.Close()

	cfg := &ENCConfig{
		URL:            server.URL,
		Method:         "GET",
		Path:           "/node/{certname}",
		ResponseFormat: "yaml",
		TimeoutSeconds: 5,
	}

	_, err := classify(cfg, "node1")
	if err == nil {
		t.Fatal("expected error for 500")
	}
	if isNotFound(err) {
		t.Error("should not be notFoundError for 500")
	}
}

func TestClassify_BearerAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if auth != "Bearer test-token" {
			t.Errorf("Authorization = %q, want %q", auth, "Bearer test-token")
		}
		_, _ = w.Write([]byte("classes: {}\n"))
	}))
	defer server.Close()

	cfg := &ENCConfig{
		URL:            server.URL,
		Method:         "GET",
		Path:           "/node/{certname}",
		ResponseFormat: "yaml",
		TimeoutSeconds: 5,
		Auth: AuthConfig{
			Type:  "bearer",
			Token: "test-token",
		},
	}

	_, err := classify(cfg, "node1")
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
}

func TestClassify_TokenAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("X-Custom-Token")
		if token != "secret-123" {
			t.Errorf("X-Custom-Token = %q", token)
		}
		_, _ = w.Write([]byte("classes: {}\n"))
	}))
	defer server.Close()

	cfg := &ENCConfig{
		URL:            server.URL,
		Method:         "GET",
		Path:           "/node/{certname}",
		ResponseFormat: "yaml",
		TimeoutSeconds: 5,
		Auth: AuthConfig{
			Type:   "token",
			Header: "X-Custom-Token",
			Token:  "secret-123",
		},
	}

	_, err := classify(cfg, "node1")
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
}

func TestClassify_BasicAuth(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok := r.BasicAuth()
		if !ok || user != "admin" || pass != "secret" {
			t.Errorf("BasicAuth: ok=%v user=%q pass=%q", ok, user, pass)
		}
		_, _ = w.Write([]byte("classes: {}\n"))
	}))
	defer server.Close()

	cfg := &ENCConfig{
		URL:            server.URL,
		Method:         "GET",
		Path:           "/node/{certname}",
		ResponseFormat: "yaml",
		TimeoutSeconds: 5,
		Auth: AuthConfig{
			Type:     "basic",
			Username: "admin",
			Password: "secret",
		},
	}

	_, err := classify(cfg, "node1")
	if err != nil {
		t.Fatalf("classify: %v", err)
	}
}

func TestNormalizeResponse_YAML(t *testing.T) {
	input := `classes:
  apache: {}
parameters:
  role: web
environment: production
`
	result, err := normalizeResponse([]byte(input), "yaml")
	if err != nil {
		t.Fatalf("normalizeResponse: %v", err)
	}

	var enc ENCResult
	if err := yaml.Unmarshal([]byte(result), &enc); err != nil {
		t.Fatalf("parsing result: %v", err)
	}
	if enc.Environment != "production" {
		t.Errorf("Environment = %q", enc.Environment)
	}
}

func TestNormalizeResponse_JSON(t *testing.T) {
	input := `{"classes":{"ntp":{}},"environment":"staging"}`
	result, err := normalizeResponse([]byte(input), "json")
	if err != nil {
		t.Fatalf("normalizeResponse: %v", err)
	}

	var enc ENCResult
	if err := yaml.Unmarshal([]byte(result), &enc); err != nil {
		t.Fatalf("parsing result: %v", err)
	}
	if enc.Environment != "staging" {
		t.Errorf("Environment = %q", enc.Environment)
	}
}

func TestNormalizeResponse_NilClasses(t *testing.T) {
	input := `{"environment":"production"}`
	result, err := normalizeResponse([]byte(input), "json")
	if err != nil {
		t.Fatalf("normalizeResponse: %v", err)
	}

	var enc ENCResult
	if err := yaml.Unmarshal([]byte(result), &enc); err != nil {
		t.Fatalf("parsing result: %v", err)
	}
	if enc.Classes == nil {
		t.Error("expected classes to be non-nil")
	}
}

func TestNormalizeResponse_UnsupportedFormat(t *testing.T) {
	_, err := normalizeResponse([]byte("data"), "xml")
	if err == nil {
		t.Fatal("expected error for unsupported format")
	}
}

func TestBuildRequestBody_Certname(t *testing.T) {
	body, err := buildRequestBody("certname", "web1.example.com")
	if err != nil {
		t.Fatalf("buildRequestBody: %v", err)
	}

	var data map[string]string
	if err := json.Unmarshal([]byte(body), &data); err != nil {
		t.Fatalf("parsing body: %v", err)
	}
	if data["certname"] != "web1.example.com" {
		t.Errorf("certname = %q", data["certname"])
	}
}

func TestBuildRequestBody_Unknown(t *testing.T) {
	body, err := buildRequestBody("unknown", "node1")
	if err != nil {
		t.Fatalf("buildRequestBody: %v", err)
	}
	if body != "" {
		t.Errorf("expected empty body, got %q", body)
	}
}

func TestSaveAndReadCache(t *testing.T) {
	dir := t.TempDir()
	certname := "node1.example.com"
	data := "classes:\n  ntp: {}\n"

	if err := saveCache(dir, certname, data); err != nil {
		t.Fatalf("saveCache: %v", err)
	}

	cached, err := readCache(dir, certname)
	if err != nil {
		t.Fatalf("readCache: %v", err)
	}
	if cached != data {
		t.Errorf("cached data mismatch: got %q, want %q", cached, data)
	}
}

func TestReadCache_NotExists(t *testing.T) {
	dir := t.TempDir()
	_, err := readCache(dir, "nonexistent")
	if err == nil {
		t.Fatal("expected error for missing cache file")
	}
}

func TestSaveCache_CreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "subdir", "cache")

	if err := saveCache(dir, "node1", "data"); err != nil {
		t.Fatalf("saveCache: %v", err)
	}

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Error("expected directory to be created")
	}
}

func TestIsNotFound(t *testing.T) {
	nfe := &notFoundError{msg: "not found"}
	if !isNotFound(nfe) {
		t.Error("expected isNotFound to return true")
	}

	generic := fmt.Errorf("some error")
	if isNotFound(generic) {
		t.Error("expected isNotFound to return false for generic error")
	}
}

func TestValidateCertname(t *testing.T) {
	valid := []string{
		"web1.example.com",
		"node-01.prod",
		"my_host.local",
		"UPPERCASE.host",
		"simple",
	}
	for _, name := range valid {
		if err := validateCertname(name); err != nil {
			t.Errorf("validateCertname(%q) = %v, want nil", name, err)
		}
	}

	invalid := []string{
		"../../etc/shadow",
		"foo/bar",
		"foo;bar",
		"node name",
		"node\ttab",
		"",
		"foo/../admin",
		"node@host",
	}
	for _, name := range invalid {
		if err := validateCertname(name); err == nil {
			t.Errorf("validateCertname(%q) = nil, want error", name)
		}
	}
}

func TestClassify_InvalidCertname(t *testing.T) {
	cfg := &ENCConfig{
		URL:            "http://localhost",
		Method:         "GET",
		Path:           "/node/{certname}",
		ResponseFormat: "yaml",
		TimeoutSeconds: 5,
	}

	invalid := []string{
		"../../etc/shadow",
		"foo/bar",
		"foo;bar",
	}
	for _, name := range invalid {
		_, err := classify(cfg, name)
		if err == nil {
			t.Errorf("classify with certname %q should have failed", name)
		}
	}
}

func TestBuildHTTPClient(t *testing.T) {
	cfg := &ENCConfig{
		TimeoutSeconds: 15,
	}

	client, err := buildHTTPClient(cfg)
	if err != nil {
		t.Fatalf("buildHTTPClient: %v", err)
	}
	if client.Timeout.Seconds() != 15 {
		t.Errorf("Timeout = %v, want 15s", client.Timeout)
	}
}

func TestBuildHTTPClient_WithCA(t *testing.T) {
	// This test verifies that a non-existent CA file returns an error
	cfg := &ENCConfig{
		TimeoutSeconds: 5,
		SSL: SSLConfig{
			CAFile: "/nonexistent/ca.pem",
		},
	}

	_, err := buildHTTPClient(cfg)
	if err == nil {
		t.Fatal("expected error for missing CA file")
	}
}

func TestBuildHTTPClient_WithValidCA(t *testing.T) {
	dir := t.TempDir()
	caFile := filepath.Join(dir, "ca.pem")

	// Generate a self-signed CA certificate
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

	cfg := &ENCConfig{
		TimeoutSeconds: 5,
		SSL:            SSLConfig{CAFile: caFile},
	}

	client, err := buildHTTPClient(cfg)
	if err != nil {
		t.Fatalf("buildHTTPClient: %v", err)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatal("expected *http.Transport")
	}
	if transport.TLSClientConfig.RootCAs == nil {
		t.Error("expected RootCAs to be set")
	}
}

func TestBuildHTTPClient_InvalidCAPEM(t *testing.T) {
	dir := t.TempDir()
	caFile := filepath.Join(dir, "bad-ca.pem")
	if err := os.WriteFile(caFile, []byte("not a cert"), 0644); err != nil {
		t.Fatal(err)
	}

	cfg := &ENCConfig{
		TimeoutSeconds: 5,
		SSL:            SSLConfig{CAFile: caFile},
	}

	_, err := buildHTTPClient(cfg)
	if err == nil {
		t.Fatal("expected error for invalid CA PEM")
	}
}

func TestBuildHTTPClient_MTLS(t *testing.T) {
	dir := t.TempDir()

	// Generate CA
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
	caDER, _ := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)

	// Generate client cert signed by CA
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
	caFile := filepath.Join(dir, "ca.pem")

	os.WriteFile(caFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER}), 0644)
	os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientDER}), 0644)

	keyBytes, _ := x509.MarshalECPrivateKey(clientKey)
	os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyBytes}), 0644)

	cfg := &ENCConfig{
		TimeoutSeconds: 5,
		Auth:           AuthConfig{Type: "mtls"},
		SSL: SSLConfig{
			CAFile:   caFile,
			CertFile: certFile,
			KeyFile:  keyFile,
		},
	}

	client, err := buildHTTPClient(cfg)
	if err != nil {
		t.Fatalf("buildHTTPClient: %v", err)
	}
	transport := client.Transport.(*http.Transport)
	if len(transport.TLSClientConfig.Certificates) != 1 {
		t.Errorf("expected 1 client certificate, got %d", len(transport.TLSClientConfig.Certificates))
	}
}

func TestBuildHTTPClient_MTLS_InvalidKeyPair(t *testing.T) {
	dir := t.TempDir()
	certFile := filepath.Join(dir, "cert.pem")
	keyFile := filepath.Join(dir, "key.pem")

	os.WriteFile(certFile, []byte("not a cert"), 0644)
	os.WriteFile(keyFile, []byte("not a key"), 0644)

	cfg := &ENCConfig{
		TimeoutSeconds: 5,
		Auth:           AuthConfig{Type: "mtls"},
		SSL: SSLConfig{
			CertFile: certFile,
			KeyFile:  keyFile,
		},
	}

	_, err := buildHTTPClient(cfg)
	if err == nil {
		t.Fatal("expected error for invalid client cert/key")
	}
}

func TestLoadFacts(t *testing.T) {
	// loadFacts reads from a hardcoded path, so we test the fallback behavior
	// when the file doesn't exist (returns empty map)
	facts := loadFacts("nonexistent-node")
	if facts == nil {
		t.Fatal("expected non-nil map")
	}
	if len(facts) != 0 {
		t.Errorf("expected empty facts, got %d entries", len(facts))
	}
}

func TestBuildRequestBody_Facts(t *testing.T) {
	// "facts" body type calls loadFacts, which will return empty map for
	// non-existent nodes, but the JSON structure should be correct
	body, err := buildRequestBody("facts", "test-node")
	if err != nil {
		t.Fatalf("buildRequestBody: %v", err)
	}

	var data map[string]interface{}
	if err := json.Unmarshal([]byte(body), &data); err != nil {
		t.Fatalf("parsing body: %v", err)
	}
	if data["certname"] != "test-node" {
		t.Errorf("certname = %v", data["certname"])
	}
	if _, ok := data["facts"]; !ok {
		t.Error("expected 'facts' key in body")
	}
}

func TestNotFoundError(t *testing.T) {
	err := &notFoundError{msg: "HTTP 404: https://example.com/node/test"}
	if err.Error() != "HTTP 404: https://example.com/node/test" {
		t.Errorf("Error() = %q", err.Error())
	}
}

func TestNormalizeResponse_InvalidJSON(t *testing.T) {
	_, err := normalizeResponse([]byte("{invalid json"), "json")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestNormalizeResponse_InvalidYAML(t *testing.T) {
	_, err := normalizeResponse([]byte(":\n  :\n    - :\n  invalid"), "yaml")
	// YAML parser is lenient, so this may or may not error; just ensure no panic
	_ = err
}

func TestLoadENCConfig_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "enc.yaml")
	os.WriteFile(path, []byte(":\n\t- invalid yaml\x00"), 0644)

	_, err := loadENCConfig(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}
