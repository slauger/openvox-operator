// Package main implements a mock ENC / report / OpenVox DB receiver for E2E tests.
// It is a simple HTTP server using only the Go standard library (plus gopkg.in/yaml.v3).
package main

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

type storedReport struct {
	ReceivedAt time.Time       `json:"received_at"`
	Body       json.RawMessage `json:"body"`
}

type storedPDBCommand struct {
	ReceivedAt time.Time       `json:"received_at"`
	Command    string          `json:"command"`
	Version    int             `json:"version"`
	Certname   string          `json:"certname,omitempty"`
	Body       json.RawMessage `json:"body"`
}

type storedClassification struct {
	Certname string    `json:"certname"`
	ServedAt time.Time `json:"served_at"`
}

type storedHECEvent struct {
	ReceivedAt time.Time       `json:"received_at"`
	Body       json.RawMessage `json:"body"`
}

type classificationEntry struct {
	Classes     []string `yaml:"classes"`
	Environment string   `yaml:"environment"`
}

type authConfig struct {
	authType     string
	authToken    string
	authHeader   string
	authUsername string
	authPassword string
}

type server struct {
	mu              sync.Mutex
	reports         []storedReport
	pdbCommands     []storedPDBCommand
	classifications []storedClassification
	hecEvents       []storedHECEvent

	// Static ENC config (env vars)
	encClasses     []string
	encEnvironment string

	// File-based classifications (ConfigMap)
	classificationsFile    string
	classificationsData    map[string]classificationEntry
	classificationsModTime time.Time

	// Auth config
	auth authConfig
}

func main() {
	listen := os.Getenv("LISTEN")
	if listen == "" {
		listen = ":8080"
	}

	encClasses := os.Getenv("ENC_CLASSES")
	encEnvironment := os.Getenv("ENC_ENVIRONMENT")

	s := &server{
		encEnvironment:      encEnvironment,
		classificationsFile: os.Getenv("CLASSIFICATIONS_FILE"),
		auth: authConfig{
			authType:     os.Getenv("AUTH_TYPE"),
			authToken:    os.Getenv("AUTH_TOKEN"),
			authHeader:   os.Getenv("AUTH_HEADER"),
			authUsername: os.Getenv("AUTH_USERNAME"),
			authPassword: os.Getenv("AUTH_PASSWORD"),
		},
	}

	if s.auth.authHeader == "" {
		s.auth.authHeader = "X-Auth-Token"
	}

	if encClasses != "" {
		for _, c := range strings.Split(encClasses, ",") {
			c = strings.TrimSpace(c)
			if c != "" {
				s.encClasses = append(s.encClasses, c)
			}
		}
	}

	// Load classifications file on startup
	if s.classificationsFile != "" {
		if err := s.loadClassificationsFile(); err != nil {
			log.Printf("WARNING: failed to load classifications file: %v", err)
		} else {
			log.Printf("Loaded classifications from %s", s.classificationsFile)
		}
		go s.watchClassificationsFile()
	}

	mux := newServeMux(s)

	log.Printf("openvox-mock listening on %s", listen)
	log.Fatal(http.ListenAndServe(listen, mux))
}

func newServeMux(s *server) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /node/{certname}", s.handleENC)
	mux.HandleFunc("POST /reports", s.handleReport)
	mux.HandleFunc("POST /pdb/cmd/v1", s.handlePDBCommand)
	mux.HandleFunc("GET /api/reports", s.handleAPIReports)
	mux.HandleFunc("GET /api/pdb-commands", s.handleAPIPDBCommands)
	mux.HandleFunc("GET /api/classifications", s.handleAPIClassifications)
	mux.HandleFunc("POST /services/collector/event", s.handleHECEvent)
	mux.HandleFunc("GET /api/hec-events", s.handleAPIHECEvents)
	mux.HandleFunc("DELETE /api/reset", s.handleAPIReset)
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	return mux
}

// checkAuth validates the request against the configured auth method.
// Returns true if auth passes. Writes 401 and returns false on failure.
func (s *server) checkAuth(w http.ResponseWriter, r *http.Request) bool {
	if s.auth.authType == "" {
		return true
	}

	switch s.auth.authType {
	case "bearer":
		auth := r.Header.Get("Authorization")
		if auth != "Bearer "+s.auth.authToken {
			http.Error(w, "unauthorized: invalid bearer token", http.StatusUnauthorized)
			return false
		}
	case "basic":
		user, pass, ok := r.BasicAuth()
		if !ok || user != s.auth.authUsername || pass != s.auth.authPassword {
			http.Error(w, "unauthorized: invalid basic auth", http.StatusUnauthorized)
			return false
		}
	case "token":
		token := r.Header.Get(s.auth.authHeader)
		if token != s.auth.authToken {
			http.Error(w, "unauthorized: invalid token", http.StatusUnauthorized)
			return false
		}
	}

	return true
}

// loadClassificationsFile reads and parses the classifications YAML file.
func (s *server) loadClassificationsFile() error {
	info, err := os.Stat(s.classificationsFile)
	if err != nil {
		return err
	}

	data, err := os.ReadFile(s.classificationsFile)
	if err != nil {
		return err
	}

	var classifications map[string]classificationEntry
	if err := yaml.Unmarshal(data, &classifications); err != nil {
		return err
	}

	s.mu.Lock()
	s.classificationsData = classifications
	s.classificationsModTime = info.ModTime()
	s.mu.Unlock()

	return nil
}

// reloadClassificationsIfChanged checks if the file has changed and reloads it.
func (s *server) reloadClassificationsIfChanged() {
	info, err := os.Stat(s.classificationsFile)
	if err != nil {
		return
	}

	s.mu.Lock()
	lastMod := s.classificationsModTime
	s.mu.Unlock()

	if info.ModTime().After(lastMod) {
		if err := s.loadClassificationsFile(); err != nil {
			log.Printf("WARNING: failed to reload classifications file: %v", err)
		} else {
			log.Printf("Reloaded classifications from %s", s.classificationsFile)
		}
	}
}

// watchClassificationsFile polls the classifications file for changes.
func (s *server) watchClassificationsFile() {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		s.reloadClassificationsIfChanged()
	}
}

func (s *server) handleENC(w http.ResponseWriter, r *http.Request) {
	if !s.checkAuth(w, r) {
		return
	}

	certname := r.PathValue("certname")
	log.Printf("ENC request for certname=%s", certname)

	s.mu.Lock()
	s.classifications = append(s.classifications, storedClassification{
		Certname: certname,
		ServedAt: time.Now(),
	})

	// Try file-based classifications first
	var classes []string
	var environment string
	if s.classificationsData != nil {
		if entry, ok := s.classificationsData[certname]; ok {
			classes = entry.Classes
			environment = entry.Environment
		} else if entry, ok := s.classificationsData["_default"]; ok {
			classes = entry.Classes
			environment = entry.Environment
		}
	}
	s.mu.Unlock()

	// Fall back to env var config
	if classes == nil {
		classes = s.encClasses
	}
	if environment == "" {
		environment = s.encEnvironment
	}

	w.Header().Set("Content-Type", "application/x-yaml")
	_, _ = w.Write([]byte("---\n"))
	if environment != "" {
		_, _ = w.Write([]byte("environment: " + environment + "\n"))
	}
	if len(classes) > 0 {
		_, _ = w.Write([]byte("classes:\n"))
		for _, c := range classes {
			_, _ = w.Write([]byte("  " + c + ":\n"))
		}
	}
}

func (s *server) handleReport(w http.ResponseWriter, r *http.Request) {
	if !s.checkAuth(w, r) {
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	log.Printf("Received report (%d bytes)", len(body))

	s.mu.Lock()
	s.reports = append(s.reports, storedReport{
		ReceivedAt: time.Now(),
		Body:       json.RawMessage(body),
	})
	s.mu.Unlock()

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *server) handlePDBCommand(w http.ResponseWriter, r *http.Request) {
	if !s.checkAuth(w, r) {
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	log.Printf("Received PDB command (%d bytes)", len(body))

	// Parse and validate PuppetDB Wire Format envelope
	var envelope struct {
		Command string `json:"command"`
		Version int    `json:"version"`
		Payload any    `json:"payload"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if envelope.Command == "" {
		http.Error(w, "missing 'command' field", http.StatusBadRequest)
		return
	}
	if envelope.Version == 0 {
		http.Error(w, "missing or zero 'version' field", http.StatusBadRequest)
		return
	}
	if envelope.Command == "store report" && envelope.Version != 8 {
		http.Error(w, "store report command requires version 8", http.StatusBadRequest)
		return
	}

	// Extract certname from payload if available
	var certname string
	if payloadMap, ok := envelope.Payload.(map[string]any); ok {
		certname, _ = payloadMap["certname"].(string)
	}

	s.mu.Lock()
	s.pdbCommands = append(s.pdbCommands, storedPDBCommand{
		ReceivedAt: time.Now(),
		Command:    envelope.Command,
		Version:    envelope.Version,
		Certname:   certname,
		Body:       json.RawMessage(body),
	})
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"uuid":"mock-uuid"}`))
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, "failed to encode response", http.StatusInternalServerError)
	}
}

func (s *server) handleAPIReports(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, s.reports)
}

func (s *server) handleAPIPDBCommands(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, s.pdbCommands)
}

func (s *server) handleAPIClassifications(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, s.classifications)
}

func (s *server) handleHECEvent(w http.ResponseWriter, r *http.Request) {
	if !s.checkAuth(w, r) {
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	log.Printf("Received HEC event (%d bytes)", len(body))

	if !json.Valid(body) {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}

	s.mu.Lock()
	s.hecEvents = append(s.hecEvents, storedHECEvent{
		ReceivedAt: time.Now(),
		Body:       json.RawMessage(body),
	})
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"text":"Success","code":0}`))
}

func (s *server) handleAPIHECEvents(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	writeJSON(w, s.hecEvents)
}

func (s *server) handleAPIReset(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	s.reports = nil
	s.pdbCommands = nil
	s.classifications = nil
	s.hecEvents = nil
	s.mu.Unlock()

	log.Printf("All stored data cleared")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}
