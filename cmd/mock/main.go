// Package main implements a mock ENC / report / PuppetDB receiver for E2E tests.
// It is a simple HTTP server using only the Go standard library.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type storedReport struct {
	ReceivedAt time.Time       `json:"received_at"`
	Body       json.RawMessage `json:"body"`
}

type storedPDBCommand struct {
	ReceivedAt time.Time       `json:"received_at"`
	Body       json.RawMessage `json:"body"`
}

type storedClassification struct {
	Certname   string    `json:"certname"`
	ServedAt   time.Time `json:"served_at"`
}

type server struct {
	mu              sync.Mutex
	reports         []storedReport
	pdbCommands     []storedPDBCommand
	classifications []storedClassification

	encClasses     []string
	encEnvironment string
}

func main() {
	listen := os.Getenv("LISTEN")
	if listen == "" {
		listen = ":8080"
	}

	encClasses := os.Getenv("ENC_CLASSES")
	encEnvironment := os.Getenv("ENC_ENVIRONMENT")

	s := &server{
		encEnvironment: encEnvironment,
	}
	if encClasses != "" {
		for _, c := range strings.Split(encClasses, ",") {
			c = strings.TrimSpace(c)
			if c != "" {
				s.encClasses = append(s.encClasses, c)
			}
		}
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /node/{certname}", s.handleENC)
	mux.HandleFunc("POST /reports", s.handleReport)
	mux.HandleFunc("POST /pdb/cmd/v1", s.handlePDBCommand)
	mux.HandleFunc("GET /api/reports", s.handleAPIReports)
	mux.HandleFunc("GET /api/pdb-commands", s.handleAPIPDBCommands)
	mux.HandleFunc("GET /api/classifications", s.handleAPIClassifications)
	mux.HandleFunc("GET /healthz", s.handleHealthz)

	log.Printf("openvox-mock listening on %s", listen)
	log.Fatal(http.ListenAndServe(listen, mux))
}

func (s *server) handleENC(w http.ResponseWriter, r *http.Request) {
	certname := r.PathValue("certname")
	log.Printf("ENC request for certname=%s", certname)

	s.mu.Lock()
	s.classifications = append(s.classifications, storedClassification{
		Certname: certname,
		ServedAt: time.Now(),
	})
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/x-yaml")
	fmt.Fprintf(w, "---\n")
	if s.encEnvironment != "" {
		fmt.Fprintf(w, "environment: %s\n", s.encEnvironment)
	}
	if len(s.encClasses) > 0 {
		fmt.Fprintf(w, "classes:\n")
		for _, c := range s.encClasses {
			fmt.Fprintf(w, "  %s:\n", c)
		}
	}
}

func (s *server) handleReport(w http.ResponseWriter, r *http.Request) {
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
	fmt.Fprintf(w, "ok")
}

func (s *server) handlePDBCommand(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "failed to read body", http.StatusBadRequest)
		return
	}
	log.Printf("Received PDB command (%d bytes)", len(body))

	s.mu.Lock()
	s.pdbCommands = append(s.pdbCommands, storedPDBCommand{
		ReceivedAt: time.Now(),
		Body:       json.RawMessage(body),
	})
	s.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, `{"uuid":"mock-uuid"}`)
}

func (s *server) handleAPIReports(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.reports)
}

func (s *server) handleAPIPDBCommands(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.pdbCommands)
}

func (s *server) handleAPIClassifications(w http.ResponseWriter, _ *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(s.classifications)
}

func (s *server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	fmt.Fprintf(w, "ok")
}
