package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestServer(opts ...func(*server)) (*httptest.Server, *server) {
	s := &server{}
	for _, opt := range opts {
		opt(s)
	}
	return httptest.NewServer(newServeMux(s)), s
}

func TestHealthz(t *testing.T) {
	ts, _ := newTestServer()
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestENC_Default(t *testing.T) {
	ts, _ := newTestServer(func(s *server) {
		s.encClasses = []string{"base", "ntp"}
		s.encEnvironment = "production"
	})
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/node/web1.example.com")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	content := string(body)

	if !strings.Contains(content, "environment: production") {
		t.Errorf("missing environment, got: %s", content)
	}
	if !strings.Contains(content, "base:") {
		t.Errorf("missing class 'base', got: %s", content)
	}
	if !strings.Contains(content, "ntp:") {
		t.Errorf("missing class 'ntp', got: %s", content)
	}
}

func TestENC_ClassificationsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "classifications.yaml")

	content := `web1.example.com:
  classes: [apache, ntp]
  environment: staging
_default:
  classes: [base]
  environment: production
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	ts, s := newTestServer(func(s *server) {
		s.classificationsFile = path
	})
	defer ts.Close()

	if err := s.loadClassificationsFile(); err != nil {
		t.Fatal(err)
	}

	// Exact match
	resp, err := http.Get(ts.URL + "/node/web1.example.com")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	respContent := string(body)

	if !strings.Contains(respContent, "environment: staging") {
		t.Errorf("expected staging environment, got: %s", respContent)
	}
	if !strings.Contains(respContent, "apache:") {
		t.Errorf("missing class 'apache', got: %s", respContent)
	}
}

func TestENC_DefaultFallback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "classifications.yaml")

	content := `_default:
  classes: [base]
  environment: production
web1.example.com:
  classes: [apache]
  environment: staging
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	ts, s := newTestServer(func(s *server) {
		s.classificationsFile = path
	})
	defer ts.Close()

	if err := s.loadClassificationsFile(); err != nil {
		t.Fatal(err)
	}

	// Unknown certname → _default
	resp, err := http.Get(ts.URL + "/node/unknown.example.com")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	respContent := string(body)

	if !strings.Contains(respContent, "environment: production") {
		t.Errorf("expected production environment from _default, got: %s", respContent)
	}
	if !strings.Contains(respContent, "base:") {
		t.Errorf("missing class 'base' from _default, got: %s", respContent)
	}
}

func TestENC_FileReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "classifications.yaml")

	initial := `_default:
  classes: [base]
  environment: production
`
	if err := os.WriteFile(path, []byte(initial), 0644); err != nil {
		t.Fatal(err)
	}

	ts, s := newTestServer(func(s *server) {
		s.classificationsFile = path
	})
	defer ts.Close()

	if err := s.loadClassificationsFile(); err != nil {
		t.Fatal(err)
	}

	// Update the file
	updated := `_default:
  classes: [updated_class]
  environment: staging
`
	if err := os.WriteFile(path, []byte(updated), 0644); err != nil {
		t.Fatal(err)
	}

	// Trigger reload
	if err := s.loadClassificationsFile(); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(ts.URL + "/node/any.example.com")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	respContent := string(body)

	if !strings.Contains(respContent, "updated_class:") {
		t.Errorf("expected updated_class after reload, got: %s", respContent)
	}
	if !strings.Contains(respContent, "environment: staging") {
		t.Errorf("expected staging environment after reload, got: %s", respContent)
	}
}

func TestENC_RecordsClassification(t *testing.T) {
	ts, _ := newTestServer(func(s *server) {
		s.encClasses = []string{"base"}
	})
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/node/test-node")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	resp, err = http.Get(ts.URL + "/api/classifications")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	var classifications []storedClassification
	if err := json.NewDecoder(resp.Body).Decode(&classifications); err != nil {
		t.Fatal(err)
	}

	if len(classifications) != 1 {
		t.Fatalf("expected 1 classification, got %d", len(classifications))
	}
	if classifications[0].Certname != "test-node" {
		t.Errorf("certname = %q, want test-node", classifications[0].Certname)
	}
}

func TestReport_Store(t *testing.T) {
	ts, _ := newTestServer()
	defer ts.Close()

	report := `{"host":"test.example.com","status":"changed"}`
	resp, err := http.Post(ts.URL+"/reports", "application/json", strings.NewReader(report))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	resp, err = http.Get(ts.URL + "/api/reports")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	var reports []storedReport
	if err := json.NewDecoder(resp.Body).Decode(&reports); err != nil {
		t.Fatal(err)
	}

	if len(reports) != 1 {
		t.Fatalf("expected 1 report, got %d", len(reports))
	}

	var body map[string]any
	if err := json.Unmarshal(reports[0].Body, &body); err != nil {
		t.Fatal(err)
	}
	if body["host"] != "test.example.com" {
		t.Errorf("host = %v", body["host"])
	}
}

func TestPDBCommand_Valid(t *testing.T) {
	ts, _ := newTestServer()
	defer ts.Close()

	cmd := `{"command":"store report","version":8,"payload":{"certname":"node1.example.com","status":"changed"}}`
	resp, err := http.Post(ts.URL+"/pdb/cmd/v1", "application/json", strings.NewReader(cmd))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, string(body))
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "mock-uuid") {
		t.Errorf("expected mock-uuid in response, got: %s", string(body))
	}

	// Verify stored command
	resp, err = http.Get(ts.URL + "/api/pdb-commands")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	var commands []storedPDBCommand
	if err := json.NewDecoder(resp.Body).Decode(&commands); err != nil {
		t.Fatal(err)
	}

	if len(commands) != 1 {
		t.Fatalf("expected 1 command, got %d", len(commands))
	}
	if commands[0].Command != "store report" {
		t.Errorf("command = %q", commands[0].Command)
	}
	if commands[0].Version != 8 {
		t.Errorf("version = %d", commands[0].Version)
	}
	if commands[0].Certname != "node1.example.com" {
		t.Errorf("certname = %q", commands[0].Certname)
	}
}

func TestPDBCommand_InvalidJSON(t *testing.T) {
	ts, _ := newTestServer()
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/pdb/cmd/v1", "application/json", strings.NewReader("not json"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestPDBCommand_MissingCommand(t *testing.T) {
	ts, _ := newTestServer()
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/pdb/cmd/v1", "application/json", strings.NewReader(`{"version":8}`))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestPDBCommand_WrongVersion(t *testing.T) {
	ts, _ := newTestServer()
	defer ts.Close()

	cmd := `{"command":"store report","version":7,"payload":{}}`
	resp, err := http.Post(ts.URL+"/pdb/cmd/v1", "application/json", strings.NewReader(cmd))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestPDBCommand_ExtractsCertname(t *testing.T) {
	ts, _ := newTestServer()
	defer ts.Close()

	cmd := `{"command":"store report","version":8,"payload":{"certname":"extracted.example.com"}}`
	resp, err := http.Post(ts.URL+"/pdb/cmd/v1", "application/json", strings.NewReader(cmd))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	resp, err = http.Get(ts.URL + "/api/pdb-commands")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()

	var commands []storedPDBCommand
	if err := json.NewDecoder(resp.Body).Decode(&commands); err != nil {
		t.Fatal(err)
	}

	if len(commands) != 1 {
		t.Fatalf("expected 1 command, got %d", len(commands))
	}
	if commands[0].Certname != "extracted.example.com" {
		t.Errorf("certname = %q, want extracted.example.com", commands[0].Certname)
	}
}

func TestAuth_Bearer(t *testing.T) {
	ts, _ := newTestServer(func(s *server) {
		s.auth = authConfig{authType: "bearer", authToken: "secret-token"}
		s.encClasses = []string{"base"}
	})
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/node/test", nil)
	req.Header.Set("Authorization", "Bearer secret-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestAuth_BearerReject(t *testing.T) {
	ts, _ := newTestServer(func(s *server) {
		s.auth = authConfig{authType: "bearer", authToken: "secret-token"}
	})
	defer ts.Close()

	// Wrong token
	req, _ := http.NewRequest("GET", ts.URL+"/node/test", nil)
	req.Header.Set("Authorization", "Bearer wrong-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}

	// No token
	req, _ = http.NewRequest("GET", ts.URL+"/node/test", nil)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 for missing token", resp.StatusCode)
	}
}

func TestAuth_Basic(t *testing.T) {
	ts, _ := newTestServer(func(s *server) {
		s.auth = authConfig{authType: "basic", authUsername: "admin", authPassword: "pass123"}
		s.encClasses = []string{"base"}
	})
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/node/test", nil)
	req.SetBasicAuth("admin", "pass123")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestAuth_BasicReject(t *testing.T) {
	ts, _ := newTestServer(func(s *server) {
		s.auth = authConfig{authType: "basic", authUsername: "admin", authPassword: "pass123"}
	})
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/node/test", nil)
	req.SetBasicAuth("admin", "wrong")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestAuth_Token(t *testing.T) {
	ts, _ := newTestServer(func(s *server) {
		s.auth = authConfig{authType: "token", authHeader: "X-Custom-Auth", authToken: "my-token"}
		s.encClasses = []string{"base"}
	})
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/node/test", nil)
	req.Header.Set("X-Custom-Auth", "my-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
}

func TestAuth_TokenReject(t *testing.T) {
	ts, _ := newTestServer(func(s *server) {
		s.auth = authConfig{authType: "token", authHeader: "X-Custom-Auth", authToken: "my-token"}
	})
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/node/test", nil)
	req.Header.Set("X-Custom-Auth", "wrong")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestAuth_NoAuthRequired(t *testing.T) {
	ts, _ := newTestServer(func(s *server) {
		s.encClasses = []string{"base"}
	})
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/node/test")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200 when no auth configured", resp.StatusCode)
	}
}

func TestAuth_APIEndpointsSkipAuth(t *testing.T) {
	ts, _ := newTestServer(func(s *server) {
		s.auth = authConfig{authType: "bearer", authToken: "secret"}
	})
	defer ts.Close()

	// API endpoints should not require auth
	for _, path := range []string{"/api/reports", "/api/pdb-commands", "/api/classifications", "/healthz"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		_ = resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s: status = %d, want 200 (API should skip auth)", path, resp.StatusCode)
		}
	}
}
