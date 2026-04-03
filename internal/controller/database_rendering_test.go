package controller

import (
	"fmt"
	"strings"
	"testing"
)

func TestRenderJettyIni(t *testing.T) {
	ini := renderJettyIni("puppetdb.example.com")

	checks := map[string]string{
		"cleartext host":  "host = 127.0.0.1",
		"cleartext port":  fmt.Sprintf("port = %d", DatabaseHTTPPort),
		"ssl host":        "ssl-host = 0.0.0.0",
		"ssl port":        fmt.Sprintf("ssl-port = %d", DatabaseHTTPSPort),
		"client-auth":     "client-auth = need",
		"ssl-key path":    "ssl-key = /etc/puppetlabs/puppetdb/ssl/private_keys/puppetdb.example.com.pem",
		"ssl-cert path":   "ssl-cert = /etc/puppetlabs/puppetdb/ssl/certs/puppetdb.example.com.pem",
		"ssl-ca-cert path": "ssl-ca-cert = /etc/puppetlabs/puppetdb/ssl/certs/ca.pem",
	}
	for name, expected := range checks {
		if !strings.Contains(ini, expected) {
			t.Errorf("jetty.ini missing %s: expected %q in output:\n%s", name, expected, ini)
		}
	}
}
