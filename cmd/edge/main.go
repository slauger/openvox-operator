// Command openvox-edge is a small mTLS reverse proxy that enforces puppet
// auth.conf-style rules and forwards to a CRuby compile backend (Puma + the
// openvox gem). It is the "Go edge + native Ruby behind it" front for the
// experimental non-JVM openvox-server-native image: the edge terminates mTLS and
// authorizes requests (the job the JVM's trapperkeeper-authorization does today),
// while catalog compilation happens in plain CRuby instead of a JRuby pool.
//
// Everything is config-driven: the authorization rules come from a JSON file
// (EDGE_AUTH_RULES) modeled on the operator's builtinAuthRules, so the operator
// can render the same intent it already renders as HOCON auth.conf.
package main

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
)

func main() {
	var (
		listen   = getenv("EDGE_LISTEN", ":8140")
		backend  = getenv("EDGE_BACKEND_URL", "http://127.0.0.1:9140")
		certFile = getenv("EDGE_TLS_CERT", "/etc/edge/server.pem")
		keyFile  = getenv("EDGE_TLS_KEY", "/etc/edge/server-key.pem")
		caFile   = getenv("EDGE_TLS_CA", "/etc/edge/ca.pem")
		rulesF   = getenv("EDGE_AUTH_RULES", "/etc/edge/auth-rules.json")
	)

	backendURL, err := url.Parse(backend)
	if err != nil {
		log.Fatalf("bad EDGE_BACKEND_URL %q: %v", backend, err)
	}

	rulesData, err := os.ReadFile(rulesF)
	if err != nil {
		log.Fatalf("read auth rules %q: %v", rulesF, err)
	}
	rules, err := parseRuleset(rulesData)
	if err != nil {
		log.Fatalf("parse auth rules %q: %v", rulesF, err)
	}

	caPEM, err := os.ReadFile(caFile)
	if err != nil {
		log.Fatalf("read CA %q: %v", caFile, err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		log.Fatalf("no CA certificates parsed from %q", caFile)
	}

	proxy := httputil.NewSingleHostReverseProxy(backendURL)
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var id Identity
		if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
			id = identityFromCert(r.TLS.PeerCertificates[0])
		}

		d := rules.Authorize(r.Method, r.URL.Path, id)
		if !d.Allowed {
			log.Printf("DENY  %s %s (cn=%q): %s", r.Method, r.URL.Path, id.CN, d.Reason)
			http.Error(w, d.Reason+"\n", d.Status)
			return
		}

		// Hand the verified identity to the backend, which trusts these headers
		// because only the edge can reach it (localhost). Mirrors the classic
		// nginx/Passenger master pattern.
		if id.Authenticated {
			r.Header.Set("X-Client-Verify", "SUCCESS")
			r.Header.Set("X-Client-DN", "CN="+id.CN)
			r.Header.Set("X-Client-CN", id.CN)
		} else {
			r.Header.Set("X-Client-Verify", "NONE")
			r.Header.Del("X-Client-DN")
			r.Header.Del("X-Client-CN")
		}
		log.Printf("ALLOW %s %s (cn=%q): %s", r.Method, r.URL.Path, id.CN, d.Reason)
		proxy.ServeHTTP(w, r)
	})

	srv := &http.Server{
		Addr:    listen,
		Handler: handler,
		TLSConfig: &tls.Config{
			ClientAuth: tls.VerifyClientCertIfGiven,
			ClientCAs:  pool,
			MinVersion: tls.VersionTLS12,
		},
		// Drop the connection-noise handshake errors (probes, port scans, LB
		// health checks that open and close a socket) while keeping real mTLS
		// failures -- bad/unknown client certificates -- visible.
		ErrorLog: log.New(&handshakeNoiseFilter{out: os.Stderr}, "", log.LstdFlags),
	}
	log.Printf("openvox-edge listening on %s (mTLS) -> %s, %d auth rules (default %s)",
		listen, backendURL, len(rules.Rules), rules.Default)
	log.Fatal(srv.ListenAndServeTLS(certFile, keyFile))
}

// handshakeNoiseFilter forwards http.Server error lines to out, except benign
// "TLS handshake error ... EOF / connection reset / broken pipe" lines caused by
// clients that close the socket before completing the handshake. Real handshake
// failures (e.g. "remote error: tls: bad certificate", "unknown authority",
// "client didn't provide a certificate") are kept.
type handshakeNoiseFilter struct{ out io.Writer }

func (f *handshakeNoiseFilter) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte("TLS handshake error")) &&
		(bytes.HasSuffix(bytes.TrimRight(p, "\n"), []byte("EOF")) ||
			bytes.Contains(p, []byte("connection reset by peer")) ||
			bytes.Contains(p, []byte("broken pipe"))) {
		return len(p), nil
	}
	return f.out.Write(p)
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
