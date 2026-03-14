package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
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
