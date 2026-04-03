package controller

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/types"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

func (r *ConfigReconciler) renderPuppetConf(ctx context.Context, cfg *openvoxv1alpha1.Config) (string, error) {
	var sb strings.Builder
	sb.WriteString("[main]\n")
	sb.WriteString("confdir = /etc/puppetlabs/puppet\n")
	sb.WriteString("vardir = /opt/puppetlabs/puppet/cache\n")
	sb.WriteString("logdir = /var/log/puppetlabs/puppet\n")
	sb.WriteString("codedir = /etc/puppetlabs/code\n")
	sb.WriteString("rundir = /var/run/puppetlabs\n")
	sb.WriteString("manage_internal_file_permissions = false\n")

	if cfg.Spec.Puppet.EnvironmentPath != "" {
		fmt.Fprintf(&sb, "environmentpath = %s\n", cfg.Spec.Puppet.EnvironmentPath)
	}

	if cfg.Spec.Puppet.HieraConfig != "" {
		fmt.Fprintf(&sb, "hiera_config = %s\n", cfg.Spec.Puppet.HieraConfig)
	}

	sb.WriteString("\n[server]\n")

	if cfg.Spec.Puppet.EnvironmentTimeout != "" {
		fmt.Fprintf(&sb, "environment_timeout = %s\n", cfg.Spec.Puppet.EnvironmentTimeout)
	}

	if cfg.Spec.Puppet.Storeconfigs {
		sb.WriteString("storeconfigs = true\n")
		if cfg.Spec.Puppet.StoreBackend != "" {
			fmt.Fprintf(&sb, "storeconfigs_backend = %s\n", cfg.Spec.Puppet.StoreBackend)
		}
	}

	// Reports: include webhook if any ReportProcessor exists for this Config
	reports := cfg.Spec.Puppet.Reports
	hasRP, err := r.hasReportProcessors(ctx, cfg)
	if err != nil {
		return "", fmt.Errorf("checking report processors: %w", err)
	}
	if hasRP {
		if reports == "" {
			reports = "webhook"
		} else if !strings.Contains(reports, "webhook") {
			reports = reports + ",webhook"
		}
	}
	if reports != "" {
		fmt.Fprintf(&sb, "reports = %s\n", reports)
	}

	// CA settings from CertificateAuthority (if one exists for this Config)
	if ca := r.findCertificateAuthority(ctx, cfg); ca != nil {
		if ca.Spec.TTL != "" {
			ttlSeconds, err := openvoxv1alpha1.ParseDurationToSeconds(ca.Spec.TTL)
			if err != nil {
				return "", fmt.Errorf("parsing CA TTL: %w", err)
			}
			if ttlSeconds > 0 {
				fmt.Fprintf(&sb, "ca_ttl = %d\n", ttlSeconds)
			}
		}

		// Always point to the autosign binary. The binary reads the policy config
		// Secret (mounted by the server controller) and decides sign/deny.
		// This keeps puppet.conf static -- policy changes only update the Secret,
		// which kubelet syncs without a pod restart.
		fmt.Fprintf(&sb, "autosign = %s\n", autosignBinaryPath)
	}

	// ENC settings
	if cfg.Spec.NodeClassifierRef != "" {
		sb.WriteString("node_terminus = exec\n")
		fmt.Fprintf(&sb, "external_nodes = %s\n", encBinaryPath)
	}

	extraKeys := make([]string, 0, len(cfg.Spec.Puppet.ExtraConfig))
	for k := range cfg.Spec.Puppet.ExtraConfig {
		extraKeys = append(extraKeys, k)
	}
	sort.Strings(extraKeys)
	for _, k := range extraKeys {
		fmt.Fprintf(&sb, "%s = %s\n", k, cfg.Spec.Puppet.ExtraConfig[k])
	}

	return sb.String(), nil
}

func (r *ConfigReconciler) renderPuppetDBConf(ctx context.Context, cfg *openvoxv1alpha1.Config) (string, error) {
	if cfg.Spec.DatabaseRef != "" {
		db := &openvoxv1alpha1.Database{}
		key := types.NamespacedName{Name: cfg.Spec.DatabaseRef, Namespace: cfg.Namespace}
		if err := r.Get(ctx, key, db); err != nil {
			return "", fmt.Errorf("looking up Database %q: %w", cfg.Spec.DatabaseRef, err)
		}
		if db.Status.URL == "" {
			return "", fmt.Errorf("Database %q has no status.URL yet", cfg.Spec.DatabaseRef)
		}
		return fmt.Sprintf("[main]\nserver_urls = %s\nsoft_write_failure = true\n", db.Status.URL), nil
	}
	if len(cfg.Spec.PuppetDB.ServerURLs) > 0 {
		return fmt.Sprintf("[main]\nserver_urls = %s\nsoft_write_failure = true\n",
			strings.Join(cfg.Spec.PuppetDB.ServerURLs, ",")), nil
	}
	return "[main]\nsoft_write_failure = true\n", nil
}

// renderWebserverConf returns the webserver.conf for non-CA servers.
// CRL is read from the kubelet-synced secret mount at /etc/puppetlabs/puppet/crl/.
func (r *ConfigReconciler) renderWebserverConf(cfg *openvoxv1alpha1.Config) string {
	return r.renderWebserverConfWithCRL(cfg, "/etc/puppetlabs/puppet/crl/ca_crl.pem")
}

// renderWebserverConfCA returns the webserver.conf for CA servers.
// CRL is read from the PVC-backed ssl directory, managed by Puppetserver itself.
func (r *ConfigReconciler) renderWebserverConfCA(cfg *openvoxv1alpha1.Config) string {
	return r.renderWebserverConfWithCRL(cfg, "/etc/puppetlabs/puppet/ssl/crl.pem")
}

func (r *ConfigReconciler) renderWebserverConfWithCRL(cfg *openvoxv1alpha1.Config, crlPath string) string {
	clientAuth := "want"
	if cfg.Spec.PuppetServer.ClientAuth != "" {
		clientAuth = cfg.Spec.PuppetServer.ClientAuth
	}
	return fmt.Sprintf(`webserver: {
    client-auth: %s
    ssl-host: 0.0.0.0
    ssl-port: 8140
    ssl-cert: /etc/puppetlabs/puppet/ssl/certs/puppet.pem
    ssl-key: /etc/puppetlabs/puppet/ssl/private_keys/puppet.pem
    ssl-ca-cert: /etc/puppetlabs/puppet/ssl/certs/ca.pem
    ssl-crl-path: %s
}
`, clientAuth, crlPath)
}

func (r *ConfigReconciler) renderPuppetserverConf(cfg *openvoxv1alpha1.Config) string {
	ps := cfg.Spec.PuppetServer

	maxRequests := int32(0)
	if ps.MaxRequestsPerInstance != 0 {
		maxRequests = ps.MaxRequestsPerInstance
	}

	borrowTimeout := int32(1200000)
	if ps.BorrowTimeout != 0 {
		borrowTimeout = ps.BorrowTimeout
	}

	compileMode := "off"
	if ps.CompileMode != "" {
		compileMode = ps.CompileMode
	}

	var sb strings.Builder
	sb.WriteString("jruby-puppet: {\n")
	sb.WriteString("    ruby-load-path: [/opt/puppetlabs/puppet/lib/ruby/vendor_ruby]\n")
	sb.WriteString("    gem-home: /opt/puppetlabs/server/data/puppetserver/jruby-gems\n")
	sb.WriteString("    gem-path: [${jruby-puppet.gem-home}, \"/opt/puppetlabs/server/data/puppetserver/vendored-jruby-gems\", \"/opt/puppetlabs/puppet/lib/ruby/vendor_gems\"]\n")
	sb.WriteString("    server-conf-dir: /etc/puppetlabs/puppet\n")
	sb.WriteString("    server-code-dir: /etc/puppetlabs/code\n")
	sb.WriteString("    server-var-dir: /run/puppetserver\n")
	sb.WriteString("    server-run-dir: /run/puppetserver/run\n")
	sb.WriteString("    server-log-dir: /var/log/puppetlabs/puppetserver\n")
	sb.WriteString("    max-active-instances: 1\n")
	fmt.Fprintf(&sb, "    max-requests-per-instance: %d\n", maxRequests)
	fmt.Fprintf(&sb, "    borrow-timeout: %d\n", borrowTimeout)
	fmt.Fprintf(&sb, "    compile-mode: %s\n", compileMode)
	sb.WriteString("}\n")

	sb.WriteString("\nhttp-client: {\n")
	if ps.HTTPClient != nil {
		if ps.HTTPClient.ConnectTimeoutMs != nil {
			fmt.Fprintf(&sb, "    connect-timeout-milliseconds: %d\n", *ps.HTTPClient.ConnectTimeoutMs)
		}
		if ps.HTTPClient.IdleTimeoutMs != nil {
			fmt.Fprintf(&sb, "    idle-timeout-milliseconds: %d\n", *ps.HTTPClient.IdleTimeoutMs)
		}
	}
	sb.WriteString("}\n")

	sb.WriteString("\nprofiler: {\n}\n")
	sb.WriteString("\ndropsonde: {\n    enabled: false\n}\n")

	return sb.String()
}

func (r *ConfigReconciler) renderAuthConf(cfg *openvoxv1alpha1.Config) string {
	var sb strings.Builder
	sb.WriteString("authorization: {\n    version: 1\n    rules: [\n")

	// Built-in rules (always included)
	sb.WriteString(r.builtinAuthRules())

	// Custom authorization rules (inserted before the deny-all rule)
	for _, rule := range cfg.Spec.PuppetServer.AuthorizationRules {
		sb.WriteString("        {\n")
		sb.WriteString("            match-request: {\n")
		fmt.Fprintf(&sb, "                path: %q\n", rule.MatchRequest.Path)
		matchType := rule.MatchRequest.Type
		if matchType == "" {
			matchType = "path"
		}
		fmt.Fprintf(&sb, "                type: %s\n", matchType)
		if len(rule.MatchRequest.Method) > 0 {
			if len(rule.MatchRequest.Method) == 1 {
				fmt.Fprintf(&sb, "                method: %s\n", rule.MatchRequest.Method[0])
			} else {
				fmt.Fprintf(&sb, "                method: [%s]\n", strings.Join(rule.MatchRequest.Method, ", "))
			}
		}
		sb.WriteString("            }\n")
		if rule.AllowUnauthenticated {
			sb.WriteString("            allow-unauthenticated: true\n")
		} else if rule.Allow != "" {
			fmt.Fprintf(&sb, "            allow: %q\n", rule.Allow)
		} else if rule.Deny != "" {
			fmt.Fprintf(&sb, "            deny: %q\n", rule.Deny)
		}
		sortOrder := rule.SortOrder
		if sortOrder == 0 {
			sortOrder = 500
		}
		fmt.Fprintf(&sb, "            sort-order: %d\n", sortOrder)
		fmt.Fprintf(&sb, "            name: %q\n", rule.Name)
		sb.WriteString("        },\n")
	}

	// Deny-all rule (always last)
	sb.WriteString("        {\n")
	sb.WriteString("            match-request: {\n")
	sb.WriteString("                path: \"/\"\n")
	sb.WriteString("                type: path\n")
	sb.WriteString("            }\n")
	sb.WriteString("            deny: \"*\"\n")
	sb.WriteString("            sort-order: 999\n")
	sb.WriteString("            name: \"puppetlabs deny all\"\n")
	sb.WriteString("        }\n")
	sb.WriteString("    ]\n}\n")

	return sb.String()
}

// builtinAuthRules returns the built-in auth.conf rules as HOCON (without the deny-all).
func (r *ConfigReconciler) builtinAuthRules() string {
	return `        {
            match-request: {
                path: "^/puppet/v3/catalog/([^/]+)$"
                type: regex
                method: [get, post]
            }
            allow: "$1"
            sort-order: 500
            name: "puppetlabs v3 catalog from agents"
        },
        {
            match-request: {
                path: "^/puppet/v4/catalog/?$"
                type: regex
                method: post
            }
            deny: "*"
            sort-order: 500
            name: "puppetlabs v4 catalog for services"
        },
        {
            match-request: {
                path: "/puppet-ca/v1/certificate/"
                type: path
                method: get
            }
            allow-unauthenticated: true
            sort-order: 500
            name: "puppetlabs certificate"
        },
        {
            match-request: {
                path: "/puppet-ca/v1/certificate_revocation_list/ca"
                type: path
                method: get
            }
            allow-unauthenticated: true
            sort-order: 500
            name: "puppetlabs crl"
        },
        {
            match-request: {
                path: "/puppet-ca/v1/certificate_request"
                type: path
                method: [get, put]
            }
            allow-unauthenticated: true
            sort-order: 500
            name: "puppetlabs csr"
        },
        {
            match-request: {
                path: "/puppet-ca/v1/certificate_renewal"
                type: path
                method: post
            }
            allow: "*"
            sort-order: 500
            name: "puppetlabs certificate renewal"
        },
        {
            match-request: {
                path: "/puppet-ca/v1/certificate_status"
                type: path
                method: [get, put, delete]
            }
            allow: {
               extensions: {
                   pp_cli_auth: "true"
               }
            }
            sort-order: 500
            name: "puppetlabs cert status"
        },
        {
            match-request: {
                path: "^/puppet-ca/v1/certificate_revocation_list$"
                type: regex
                method: put
            }
            allow: {
               extensions: {
                   pp_cli_auth: "true"
               }
            }
            sort-order: 500
            name: "puppetlabs CRL update"
        },
        {
            match-request: {
                path: "/puppet-ca/v1/certificate_statuses"
                type: path
                method: get
            }
            allow: {
               extensions: {
                   pp_cli_auth: "true"
               }
            }
            sort-order: 500
            name: "puppetlabs cert statuses"
        },
        {
            match-request: {
                path: "/puppet-ca/v1/expirations"
                type: path
                method: get
            }
            allow: "*"
            sort-order: 500
            name: "puppetlabs CA cert and CRL expirations"
        },
        {
            match-request: {
                path: "/puppet-ca/v1/clean"
                type: path
                method: put
            }
            allow: {
               extensions: {
                   pp_cli_auth: "true"
               }
            }
            sort-order: 500
            name: "puppetlabs cert clean"
        },
        {
            match-request: {
                path: "/puppet-ca/v1/sign"
                type: path
                method: post
            }
            allow: {
               extensions: {
                   pp_cli_auth: "true"
               }
            }
            sort-order: 500
            name: "puppetlabs cert sign"
        },
        {
            match-request: {
                path: "/puppet-ca/v1/sign/all"
                type: path
                method: post
            }
            allow: {
               extensions: {
                   pp_cli_auth: "true"
               }
            }
            sort-order: 500
            name: "puppetlabs cert sign all"
        },
        {
            match-request: {
                path: "^/puppet/v3/resource_type/([^/]+)$"
                type: regex
                method: [get, post]
            }
            allow: "*"
            sort-order: 500
            name: "puppetlabs resource type"
        },
        {
            match-request: {
                path: "^/puppet/v3/status/([^/]+)$"
                type: regex
                method: get
            }
            allow: "$1"
            sort-order: 500
            name: "puppetlabs status"
        },
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
                path: "/puppet/v3/environments"
                type: path
                method: get
            }
            allow: "*"
            sort-order: 500
            name: "puppetlabs environments"
        },
        {
            match-request: {
                path: "/puppet/v3/file_bucket_file"
                type: path
                method: [get, head, post, put]
            }
            allow: "*"
            sort-order: 500
            name: "puppetlabs file bucket file"
        },
        {
            match-request: {
                path: "/puppet/v3/file_content"
                type: path
                method: [get, post]
            }
            allow: "*"
            sort-order: 500
            name: "puppetlabs file content"
        },
        {
            match-request: {
                path: "/puppet/v3/file_metadata"
                type: path
                method: [get, post]
            }
            allow: "*"
            sort-order: 500
            name: "puppetlabs file metadata"
        },
        {
            match-request: {
                path: "^/puppet/v3/node/([^/]+)$"
                type: regex
                method: get
            }
            allow: "$1"
            sort-order: 500
            name: "puppetlabs node"
        },
        {
            match-request: {
                path: "^/puppet/v3/report/([^/]+)$"
                type: regex
                method: put
            }
            allow: "$1"
            sort-order: 500
            name: "puppetlabs report"
        },
        {
            match-request: {
                path: "^/puppet/v3/facts/([^/]+)$"
                type: regex
                method: put
            }
            allow: "$1"
            sort-order: 500
            name: "puppetlabs facts"
        },
        {
            match-request: {
                path: "/puppet/v3/static_file_content"
                type: path
                method: get
            }
            allow: "*"
            sort-order: 500
            name: "puppetlabs static file content"
        },
        {
            match-request: {
                path: "/puppet/v3/tasks"
                type: path
            }
            allow: "*"
            sort-order: 500
            name: "puppet tasks information"
        },
`
}

// renderLogbackXML generates logback.xml from LoggingSpec.
func (r *ConfigReconciler) renderLogbackXML(cfg *openvoxv1alpha1.Config) string {
	rootLevel := "INFO"
	if cfg.Spec.Logging != nil && cfg.Spec.Logging.Level != "" {
		rootLevel = cfg.Spec.Logging.Level
	}

	var sb strings.Builder
	sb.WriteString(`<configuration scan="true">
    <appender name="STDOUT" class="ch.qos.logback.core.ConsoleAppender">
        <encoder>
            <pattern>%d %-5p [%t] [%c{2}] %m%n</pattern>
        </encoder>
    </appender>

`)

	// Per-logger overrides
	if cfg.Spec.Logging != nil {
		for name, level := range cfg.Spec.Logging.Loggers {
			fmt.Fprintf(&sb, "    <logger name=%q level=%q />\n", name, level)
		}
		if len(cfg.Spec.Logging.Loggers) > 0 {
			sb.WriteString("\n")
		}
	}

	fmt.Fprintf(&sb, "    <root level=%q>\n", rootLevel)
	sb.WriteString("        <appender-ref ref=\"STDOUT\" />\n")
	sb.WriteString("    </root>\n")
	sb.WriteString("</configuration>\n")

	return sb.String()
}

// renderMetricsConf generates metrics.conf HOCON from MetricsSpec.
func (r *ConfigReconciler) renderMetricsConf(cfg *openvoxv1alpha1.Config) string {
	var sb strings.Builder
	sb.WriteString("metrics: {\n")
	sb.WriteString("    server-id: localhost\n")

	if cfg.Spec.Metrics != nil && cfg.Spec.Metrics.Enabled {
		m := cfg.Spec.Metrics
		sb.WriteString("    registries: {\n")
		sb.WriteString("        puppetserver: {\n")

		// Collect reporters
		var reporters []string

		if m.JMX != nil && m.JMX.Enabled {
			reporters = append(reporters, "                jmx: {\n                    enabled: true\n                }")
		}

		if m.Graphite != nil && m.Graphite.Enabled {
			host := m.Graphite.Host
			port := int32(2003)
			if m.Graphite.Port != 0 {
				port = m.Graphite.Port
			}
			interval := int32(60)
			if m.Graphite.UpdateIntervalSeconds != 0 {
				interval = m.Graphite.UpdateIntervalSeconds
			}
			reporters = append(reporters, fmt.Sprintf("                graphite: {\n                    enabled: true\n                    host: %q\n                    port: %d\n                    update-interval-seconds: %d\n                }", host, port, interval))
		}

		if len(reporters) > 0 {
			sb.WriteString("            reporters: {\n")
			for _, r := range reporters {
				sb.WriteString(r)
				sb.WriteString("\n")
			}
			sb.WriteString("            }\n")
		}

		sb.WriteString("        }\n")
		sb.WriteString("    }\n")
	}

	sb.WriteString("}\n")
	return sb.String()
}

func (r *ConfigReconciler) renderCAConf(ca *openvoxv1alpha1.CertificateAuthority) string {
	allowSANs := true
	allowAuthzExt := true
	enableInfraCRL := true
	allowAutoRenewal := true
	autoRenewalCertTTL := "90d"

	if ca != nil {
		allowSANs = ca.Spec.AllowSubjectAltNames
		allowAuthzExt = ca.Spec.AllowAuthorizationExtensions
		enableInfraCRL = ca.Spec.EnableInfraCRL
		allowAutoRenewal = ca.Spec.AllowAutoRenewal
		if ca.Spec.AutoRenewalCertTTL != "" {
			autoRenewalCertTTL = ca.Spec.AutoRenewalCertTTL
		}
	}

	var sb strings.Builder
	sb.WriteString("certificate-authority: {\n")
	fmt.Fprintf(&sb, "    allow-subject-alt-names: %t\n", allowSANs)
	fmt.Fprintf(&sb, "    allow-authorization-extensions: %t\n", allowAuthzExt)
	fmt.Fprintf(&sb, "    enable-infra-crl: %t\n", enableInfraCRL)
	fmt.Fprintf(&sb, "    allow-auto-renewal: %t\n", allowAutoRenewal)
	fmt.Fprintf(&sb, "    auto-renewal-cert-ttl: %s\n", autoRenewalCertTTL)
	sb.WriteString("}\n")
	return sb.String()
}
