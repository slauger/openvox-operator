package controller

import (
	"fmt"
	"strings"
	"testing"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

func TestRenderJettyIni(t *testing.T) {
	ini := renderJettyIni("puppetdb.example.com")

	checks := map[string]string{
		"cleartext host":   "host = 127.0.0.1",
		"cleartext port":   fmt.Sprintf("port = %d", DatabaseHTTPPort),
		"ssl host":         "ssl-host = 0.0.0.0",
		"ssl port":         fmt.Sprintf("ssl-port = %d", DatabaseHTTPSPort),
		"client-auth":      "client-auth = need",
		"ssl-key path":     "ssl-key = /etc/puppetlabs/puppetdb/ssl/private_keys/puppetdb.example.com.pem",
		"ssl-cert path":    "ssl-cert = /etc/puppetlabs/puppetdb/ssl/certs/puppetdb.example.com.pem",
		"ssl-ca-cert path": "ssl-ca-cert = /etc/puppetlabs/puppetdb/ssl/certs/ca.pem",
	}
	for name, expected := range checks {
		if !strings.Contains(ini, expected) {
			t.Errorf("jetty.ini missing %s: expected %q in output:\n%s", name, expected, ini)
		}
	}
}

func TestRenderDatabaseIni(t *testing.T) {
	tests := []struct {
		name     string
		db       *openvoxv1alpha1.Database
		contains []string
	}{
		{
			name: "default port 5432",
			db: func() *openvoxv1alpha1.Database {
				db := newDatabase("db")
				db.Spec.Postgres.Port = 0
				return db
			}(),
			contains: []string{
				"subname = //pg-rw.openvox.svc:5432/openvoxdb?sslmode=require",
			},
		},
		{
			name: "custom port",
			db: func() *openvoxv1alpha1.Database {
				db := newDatabase("db")
				db.Spec.Postgres.Port = 5433
				return db
			}(),
			contains: []string{
				":5433/",
			},
		},
		{
			name: "sslMode verify-full",
			db: func() *openvoxv1alpha1.Database {
				db := newDatabase("db")
				db.Spec.Postgres.SSLMode = "verify-full"
				return db
			}(),
			contains: []string{
				"sslmode=verify-full",
			},
		},
		{
			name: "sslMode disable",
			db: func() *openvoxv1alpha1.Database {
				db := newDatabase("db")
				db.Spec.Postgres.SSLMode = "disable"
				return db
			}(),
			contains: []string{
				"sslmode=disable",
			},
		},
		{
			name: "default sslMode require",
			db: func() *openvoxv1alpha1.Database {
				db := newDatabase("db")
				db.Spec.Postgres.SSLMode = ""
				return db
			}(),
			contains: []string{
				"sslmode=require",
			},
		},
		{
			name: "custom database name",
			db: func() *openvoxv1alpha1.Database {
				db := newDatabase("db")
				db.Spec.Postgres.Database = "customdb"
				return db
			}(),
			contains: []string{
				"/customdb?",
			},
		},
		{
			name: "default database name openvoxdb",
			db: func() *openvoxv1alpha1.Database {
				db := newDatabase("db")
				db.Spec.Postgres.Database = ""
				return db
			}(),
			contains: []string{
				"/openvoxdb?",
			},
		},
		{
			name: "credentials from secret",
			db:   newDatabase("db"),
			contains: []string{
				"username = dbuser",
				"password = dbpass",
			},
		},
	}

	pgSecret := newSecret("pg-credentials", map[string][]byte{
		"username": []byte("dbuser"),
		"password": []byte("dbpass"),
	})

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := setupTestClient(pgSecret)
			r := newDatabaseReconciler(c)

			out, err := r.renderDatabaseIni(testCtx(), tt.db)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			for _, s := range tt.contains {
				if !strings.Contains(out, s) {
					t.Errorf("expected %q in output:\n%s", s, out)
				}
			}
		})
	}
}

func TestRenderConfigIni(t *testing.T) {
	out := renderConfigIni()

	for _, s := range []string{
		"vardir = /opt/puppetlabs/server/data/puppetdb",
		"logging-config = /etc/puppetlabs/puppetdb/logback.xml",
	} {
		if !strings.Contains(out, s) {
			t.Errorf("expected %q in output:\n%s", s, out)
		}
	}
}

func TestRenderAuthConfDatabase(t *testing.T) {
	out := renderAuthConf()

	for _, s := range []string{
		"path: \"/status/v1/services\"",
		"path: \"/status/v1/simple\"",
		"path: \"/metrics\"",
		"name: \"puppetlabs puppetdb metrics\"",
		"deny: \"*\"",
		"name: \"puppetlabs deny all\"",
	} {
		if !strings.Contains(out, s) {
			t.Errorf("expected %q in output:\n%s", s, out)
		}
	}
}
