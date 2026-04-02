package controller

import (
	"context"
	"fmt"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// renderDatabaseIni renders the database.ini configuration file for OpenVox DB.
// It reads credentials from the referenced Secret and constructs the PostgreSQL connection string.
func (r *DatabaseReconciler) renderDatabaseIni(ctx context.Context, db *openvoxv1alpha1.Database) (string, error) {
	pg := db.Spec.Postgres

	username, err := resolveSecretKey(ctx, r.Client, db.Namespace, pg.CredentialsSecretRef, "username")
	if err != nil {
		return "", fmt.Errorf("resolving PostgreSQL username: %w", err)
	}

	password, err := resolveSecretKey(ctx, r.Client, db.Namespace, pg.CredentialsSecretRef, "password")
	if err != nil {
		return "", fmt.Errorf("resolving PostgreSQL password: %w", err)
	}

	port := pg.Port
	if port == 0 {
		port = 5432
	}

	database := pg.Database
	if database == "" {
		database = "openvoxdb"
	}

	sslMode := pg.SSLMode
	if sslMode == "" {
		sslMode = "require"
	}

	return fmt.Sprintf(`[database]
subname = //%s:%d/%s?sslmode=%s
username = %s
password = %s
`, pg.Host, port, database, sslMode, username, password), nil
}

// renderJettyIni renders the jetty.ini configuration file for OpenVox DB.
// It configures the HTTPS listener with TLS certificate paths.
func renderJettyIni(certname string) string {
	port := DatabaseHTTPSPort
	return fmt.Sprintf(`[jetty]
ssl-host = 0.0.0.0
ssl-port = %d
ssl-key = /etc/puppetlabs/puppetdb/ssl/private_keys/%s.pem
ssl-cert = /etc/puppetlabs/puppetdb/ssl/certs/%s.pem
ssl-ca-cert = /etc/puppetlabs/puppetdb/ssl/certs/ca.pem
`, port, certname, certname)
}

// renderConfigIni renders the config.ini configuration file for OpenVox DB.
func renderConfigIni() string {
	return `[global]
vardir = /opt/puppetlabs/server/data/puppetdb
logging-config = /etc/puppetlabs/puppetdb/logback.xml
`
}

// renderAuthConf renders the auth.conf for the PuppetDB TK authorization service.
// Based on the upstream OpenVoxDB default configuration.
func renderAuthConf() string {
	return `authorization: {
    version: 1
    rules: [
        {
            match-request: {
                path: "/status/v1/services"
                type: path
                method: get
            }
            allow-unauthenticated: true
            sort-order: 500
            name: "puppetlabs status service - full"
        },
        {
            match-request: {
                path: "/status/v1/simple"
                type: path
                method: get
            }
            allow-unauthenticated: true
            sort-order: 500
            name: "puppetlabs status service - simple"
        },
        {
            match-request: {
                path: "/metrics"
                type: path
                method: [get, post]
            }
            allow: "*"
            sort-order: 500
            name: "puppetlabs puppetdb metrics"
        },
        {
            match-request: {
                path: "/"
                type: path
            }
            deny: "*"
            sort-order: 999
            name: "puppetlabs deny all"
        }
    ]
}
`
}
