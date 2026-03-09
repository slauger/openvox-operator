package controller

import (
	"context"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// ConfigReconciler reconciles a Config object.
type ConfigReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=configs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=configs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=configs/finalizers,verbs=update
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=signingpolicies,verbs=get;list;watch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=signingpolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=configmaps;serviceaccounts;secrets,verbs=get;list;watch;create;update;patch;delete

func (r *ConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	cfg := &openvoxv1alpha1.Config{}
	if err := r.Get(ctx, req.NamespacedName, cfg); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Set initial phase
	if cfg.Status.Phase == "" {
		cfg.Status.Phase = openvoxv1alpha1.ConfigPhasePending
		if err := r.Status().Update(ctx, cfg); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Step 1: Reconcile ConfigMaps
	logger.Info("reconciling ConfigMaps")
	if err := r.reconcileConfigMap(ctx, cfg); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling ConfigMaps: %w", err)
	}
	meta.SetStatusCondition(&cfg.Status.Conditions, metav1.Condition{
		Type:               openvoxv1alpha1.ConditionConfigReady,
		Status:             metav1.ConditionTrue,
		Reason:             "ConfigMapsCreated",
		Message:            "Configuration ConfigMaps are up to date",
		LastTransitionTime: metav1.Now(),
	})

	// Step 2: Reconcile autosign policy Secrets for all CAs in this Config
	if err := r.reconcileAutosignSecrets(ctx, cfg); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling autosign Secrets: %w", err)
	}

	// Step 3: Ensure server ServiceAccount exists
	if err := r.reconcileServerServiceAccount(ctx, cfg); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling server ServiceAccount: %w", err)
	}

	// Update status
	cfg.Status.Phase = openvoxv1alpha1.ConfigPhaseRunning

	if err := r.Status().Update(ctx, cfg); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *ConfigReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&openvoxv1alpha1.Config{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&corev1.Secret{}).
		Watches(&openvoxv1alpha1.SigningPolicy{}, handler.EnqueueRequestsFromMapFunc(
			r.enqueueConfigsForSigningPolicy(mgr.GetClient()),
		)).
		Complete(r)
}

// --- ConfigMap ---

func (r *ConfigReconciler) reconcileConfigMap(ctx context.Context, cfg *openvoxv1alpha1.Config) error {
	logger := log.FromContext(ctx)
	configMapName := fmt.Sprintf("%s-config", cfg.Name)

	puppetConf, err := r.renderPuppetConf(ctx, cfg)
	if err != nil {
		return fmt.Errorf("rendering puppet.conf: %w", err)
	}

	ca := r.findCertificateAuthority(ctx, cfg)

	data := map[string]string{
		"puppet.conf":       puppetConf,
		"puppetdb.conf":     r.renderPuppetDBConf(cfg),
		"webserver.conf":    r.renderWebserverConf(cfg),
		"webserver-ca.conf": r.renderWebserverConfCA(cfg),
		"puppetserver.conf": r.renderPuppetserverConf(cfg),
		"auth.conf":         r.renderAuthConf(cfg),
		"ca.conf":           r.renderCAConf(ca),
		"product.conf":      "product: {\n    check-for-updates: false\n}\n",
		"logback.xml":       r.renderLogbackXML(cfg),
		"metrics.conf":      r.renderMetricsConf(cfg),
		"ca-enabled.cfg":    "puppetlabs.services.ca.certificate-authority-service/certificate-authority-service\npuppetlabs.trapperkeeper.services.watcher.filesystem-watch-service/filesystem-watch-service\n",
		"ca-disabled.cfg":   "puppetlabs.services.ca.certificate-authority-disabled-service/certificate-authority-disabled-service\npuppetlabs.trapperkeeper.services.watcher.filesystem-watch-service/filesystem-watch-service\n",
	}

	cm := &corev1.ConfigMap{}
	err = r.Get(ctx, types.NamespacedName{Name: configMapName, Namespace: cfg.Namespace}, cm)
	if errors.IsNotFound(err) {
		logger.Info("creating ConfigMap", "name", configMapName)
		cm = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      configMapName,
				Namespace: cfg.Namespace,
				Labels:    configLabels(cfg.Name),
			},
			Data: data,
		}
		if err := controllerutil.SetControllerReference(cfg, cm, r.Scheme); err != nil {
			return err
		}
		return r.Create(ctx, cm)
	} else if err != nil {
		return err
	}

	cm.Data = data
	return r.Update(ctx, cm)
}

func (r *ConfigReconciler) findCertificateAuthority(ctx context.Context, cfg *openvoxv1alpha1.Config) *openvoxv1alpha1.CertificateAuthority {
	caList := &openvoxv1alpha1.CertificateAuthorityList{}
	if err := r.List(ctx, caList, client.InNamespace(cfg.Namespace)); err != nil {
		return nil
	}
	for i := range caList.Items {
		if caList.Items[i].Spec.ConfigRef == cfg.Name {
			return &caList.Items[i]
		}
	}
	return nil
}

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

	if cfg.Spec.Puppet.Reports != "" {
		fmt.Fprintf(&sb, "reports = %s\n", cfg.Spec.Puppet.Reports)
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
		// This keeps puppet.conf static — policy changes only update the Secret,
		// which kubelet syncs without a pod restart.
		fmt.Fprintf(&sb, "autosign = %s\n", autosignBinaryPath)
	}

	for k, v := range cfg.Spec.Puppet.ExtraConfig {
		fmt.Fprintf(&sb, "%s = %s\n", k, v)
	}

	return sb.String(), nil
}

func (r *ConfigReconciler) renderPuppetDBConf(cfg *openvoxv1alpha1.Config) string {
	if len(cfg.Spec.PuppetDB.ServerURLs) == 0 {
		return "[main]\nserver_urls = https://openvoxdb:8081\nsoft_write_failure = true\n"
	}
	return fmt.Sprintf("[main]\nserver_urls = %s\nsoft_write_failure = true\n",
		strings.Join(cfg.Spec.PuppetDB.ServerURLs, ","))
}

// renderWebserverConf returns the webserver.conf for non-CA servers.
// CRL is read from the kubelet-synced secret mount at /etc/puppetlabs/puppet/crl/.
func (r *ConfigReconciler) renderWebserverConf(cfg *openvoxv1alpha1.Config) string {
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
    ssl-crl-path: /etc/puppetlabs/puppet/crl/ca_crl.pem
}
`, clientAuth)
}

// renderWebserverConfCA returns the webserver.conf for CA servers.
// CRL is read from the PVC-backed ssl directory, managed by Puppetserver itself.
func (r *ConfigReconciler) renderWebserverConfCA(cfg *openvoxv1alpha1.Config) string {
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
    ssl-crl-path: /etc/puppetlabs/puppet/ssl/crl.pem
}
`, clientAuth)
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
	sb.WriteString("    master-conf-dir: /etc/puppetlabs/puppet\n")
	sb.WriteString("    master-code-dir: /etc/puppetlabs/code\n")
	sb.WriteString("    master-var-dir: /opt/puppetlabs/server/data/puppetserver\n")
	sb.WriteString("    master-run-dir: /var/run/puppetlabs/puppetserver\n")
	sb.WriteString("    master-log-dir: /var/log/puppetlabs/puppetserver\n")
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
	sb.WriteString(`authorization: {
    version: 1
    rules: [
        {
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
`)

	// Insert custom authorization rules before the deny-all rule
	if len(cfg.Spec.PuppetServer.AuthorizationRules) > 0 {
		sb.Reset()
		sb.WriteString("authorization: {\n    version: 1\n    rules: [\n")

		// Re-emit the built-in rules (everything before deny-all is static)
		builtinRules := r.builtinAuthRules()
		sb.WriteString(builtinRules)

		// Append custom rules
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
	if cfg.Spec.Metrics == nil || !cfg.Spec.Metrics.Enabled {
		return "metrics: {\n    enabled: false\n}\n"
	}

	m := cfg.Spec.Metrics
	var sb strings.Builder
	sb.WriteString("metrics: {\n    enabled: true\n")

	if m.JMX != nil {
		fmt.Fprintf(&sb, "    reporters: {\n        jmx: {\n            enabled: %t\n        }\n    }\n", m.JMX.Enabled)
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
		sb.WriteString("    reporters: {\n")
		sb.WriteString("        graphite: {\n")
		sb.WriteString("            enabled: true\n")
		fmt.Fprintf(&sb, "            host: %q\n", host)
		fmt.Fprintf(&sb, "            port: %d\n", port)
		fmt.Fprintf(&sb, "            update-interval-seconds: %d\n", interval)
		sb.WriteString("        }\n")
		sb.WriteString("    }\n")
	}

	sb.WriteString("}\n")
	return sb.String()
}

func (r *ConfigReconciler) renderCAConf(ca *openvoxv1alpha1.CertificateAuthority) string {
	allowSANs := true
	if ca != nil {
		allowSANs = ca.Spec.AllowSubjectAltNames
	}
	return fmt.Sprintf("certificate-authority: {\n    allow-subject-alt-names: %t\n}\n", allowSANs)
}

// --- Autosign Policy ---

const autosignBinaryPath = "/usr/local/bin/openvox-autosign"

// findSigningPolicies returns all SigningPolicies referencing the given CA.
func (r *ConfigReconciler) findSigningPolicies(ctx context.Context, ca *openvoxv1alpha1.CertificateAuthority) []openvoxv1alpha1.SigningPolicy {
	list := &openvoxv1alpha1.SigningPolicyList{}
	if err := r.List(ctx, list, client.InNamespace(ca.Namespace)); err != nil {
		return nil
	}
	var result []openvoxv1alpha1.SigningPolicy
	for _, sp := range list.Items {
		if sp.Spec.CertificateAuthorityRef == ca.Name {
			result = append(result, sp)
		}
	}
	return result
}

// reconcileAutosignSecrets reconciles autosign policy Secrets for all CAs in this Config.
func (r *ConfigReconciler) reconcileAutosignSecrets(ctx context.Context, cfg *openvoxv1alpha1.Config) error {
	caList := &openvoxv1alpha1.CertificateAuthorityList{}
	if err := r.List(ctx, caList, client.InNamespace(cfg.Namespace)); err != nil {
		return err
	}
	for i := range caList.Items {
		ca := &caList.Items[i]
		if ca.Spec.ConfigRef != cfg.Name {
			continue
		}
		if err := r.reconcileAutosignSecret(ctx, cfg, ca); err != nil {
			return fmt.Errorf("reconciling autosign Secret for CA %s: %w", ca.Name, err)
		}
	}
	return nil
}

// reconcileAutosignSecret renders the autosign policy config YAML into a Secret.
// The Secret is always created — the binary handles all cases (no policies = deny all,
// any:true = approve all). This keeps puppet.conf static and avoids pod restarts.
func (r *ConfigReconciler) reconcileAutosignSecret(ctx context.Context, cfg *openvoxv1alpha1.Config, ca *openvoxv1alpha1.CertificateAuthority) error {
	logger := log.FromContext(ctx)
	secretName := fmt.Sprintf("%s-autosign-policy", ca.Name)

	policies := r.findSigningPolicies(ctx, ca)

	// Render policy config YAML
	policyYAML, renderErr := r.renderAutosignPolicyConfig(ctx, cfg.Namespace, policies)
	if renderErr != nil {
		return fmt.Errorf("rendering autosign policy config: %w", renderErr)
	}

	// Update SigningPolicy status
	for i := range policies {
		r.updateSigningPolicyStatus(ctx, &policies[i], nil)
	}

	data := map[string][]byte{
		"autosign-policy.yaml": []byte(policyYAML),
	}

	existing := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: cfg.Namespace}, existing)
	if errors.IsNotFound(err) {
		logger.Info("creating autosign policy Secret", "name", secretName)
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: cfg.Namespace,
				Labels:    configLabels(cfg.Name),
			},
			Data: data,
		}
		if err := controllerutil.SetControllerReference(cfg, secret, r.Scheme); err != nil {
			return err
		}
		return r.Create(ctx, secret)
	} else if err != nil {
		return err
	}

	existing.Data = data
	return r.Update(ctx, existing)
}

// renderAutosignPolicyConfig renders the policy config YAML that openvox-autosign reads.
func (r *ConfigReconciler) renderAutosignPolicyConfig(ctx context.Context, namespace string, policies []openvoxv1alpha1.SigningPolicy) (string, error) {
	var sb strings.Builder
	sb.WriteString("policies:\n")

	// Sort policies by name for deterministic output
	sort.Slice(policies, func(i, j int) bool {
		return policies[i].Name < policies[j].Name
	})

	for _, p := range policies {
		fmt.Fprintf(&sb, "  - name: %s\n", p.Name)

		if p.Spec.Any {
			sb.WriteString("    any: true\n")
			continue
		}

		if p.Spec.Pattern != nil {
			sb.WriteString("    pattern:\n")
			sb.WriteString("      allow:\n")
			for _, a := range p.Spec.Pattern.Allow {
				fmt.Fprintf(&sb, "        - %q\n", a)
			}
		}

		if p.Spec.DNSAltNames != nil {
			sb.WriteString("    dnsAltNames:\n")
			sb.WriteString("      allow:\n")
			for _, a := range p.Spec.DNSAltNames.Allow {
				fmt.Fprintf(&sb, "        - %q\n", a)
			}
		}

		if len(p.Spec.CSRAttributes) > 0 {
			sb.WriteString("    csrAttributes:\n")
			for _, attr := range p.Spec.CSRAttributes {
				value := attr.Value
				if attr.ValueFrom != nil {
					var err error
					value, err = r.resolveSecretKey(ctx, namespace,
						attr.ValueFrom.SecretKeyRef.Name, attr.ValueFrom.SecretKeyRef.Key)
					if err != nil {
						r.updateSigningPolicyStatus(ctx, &p, err)
						return "", fmt.Errorf("resolving csrAttribute %q for policy %s: %w", attr.Name, p.Name, err)
					}
				}
				fmt.Fprintf(&sb, "      - name: %s\n", attr.Name)
				fmt.Fprintf(&sb, "        value: %q\n", value)
			}
		}
	}

	return sb.String(), nil
}

// resolveSecretKey reads a specific key from a Secret.
func (r *ConfigReconciler) resolveSecretKey(ctx context.Context, namespace, secretName, key string) (string, error) {
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: namespace}, secret); err != nil {
		return "", fmt.Errorf("getting Secret %s: %w", secretName, err)
	}
	val, ok := secret.Data[key]
	if !ok {
		return "", fmt.Errorf("key %q not found in Secret %s", key, secretName)
	}
	return string(val), nil
}

// updateSigningPolicyStatus sets the phase and condition on a SigningPolicy.
func (r *ConfigReconciler) updateSigningPolicyStatus(ctx context.Context, sp *openvoxv1alpha1.SigningPolicy, err error) {
	if err != nil {
		sp.Status.Phase = openvoxv1alpha1.SigningPolicyPhaseError
		meta.SetStatusCondition(&sp.Status.Conditions, metav1.Condition{
			Type:               openvoxv1alpha1.ConditionSigningPolicyReady,
			Status:             metav1.ConditionFalse,
			Reason:             "Error",
			Message:            err.Error(),
			LastTransitionTime: metav1.Now(),
		})
	} else {
		sp.Status.Phase = openvoxv1alpha1.SigningPolicyPhaseActive
		meta.SetStatusCondition(&sp.Status.Conditions, metav1.Condition{
			Type:               openvoxv1alpha1.ConditionSigningPolicyReady,
			Status:             metav1.ConditionTrue,
			Reason:             "PolicyRendered",
			Message:            "Signing policy is active",
			LastTransitionTime: metav1.Now(),
		})
	}
	_ = r.Status().Update(ctx, sp)
}

// enqueueConfigsForSigningPolicy maps SigningPolicy changes to Config reconciles.
func (r *ConfigReconciler) enqueueConfigsForSigningPolicy(c client.Reader) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		sp, ok := obj.(*openvoxv1alpha1.SigningPolicy)
		if !ok {
			return nil
		}

		// Find the CA referenced by this SigningPolicy
		ca := &openvoxv1alpha1.CertificateAuthority{}
		if err := c.Get(ctx, types.NamespacedName{Name: sp.Spec.CertificateAuthorityRef, Namespace: sp.Namespace}, ca); err != nil {
			return nil
		}

		// Enqueue the Config referenced by the CA
		return []reconcile.Request{
			{NamespacedName: types.NamespacedName{Name: ca.Spec.ConfigRef, Namespace: ca.Namespace}},
		}
	}
}

// --- Server ServiceAccount ---

func (r *ConfigReconciler) reconcileServerServiceAccount(ctx context.Context, cfg *openvoxv1alpha1.Config) error {
	saName := fmt.Sprintf("%s-server", cfg.Name)
	automount := false

	sa := &corev1.ServiceAccount{}
	if err := r.Get(ctx, types.NamespacedName{Name: saName, Namespace: cfg.Namespace}, sa); errors.IsNotFound(err) {
		sa = &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      saName,
				Namespace: cfg.Namespace,
				Labels:    configLabels(cfg.Name),
			},
			AutomountServiceAccountToken: &automount,
		}
		if err := controllerutil.SetControllerReference(cfg, sa, r.Scheme); err != nil {
			return err
		}
		return r.Create(ctx, sa)
	} else {
		return err
	}
}
