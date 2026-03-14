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

	"gopkg.in/yaml.v3"
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
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=nodeclassifiers,verbs=get;list;watch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=nodeclassifiers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=reportprocessors,verbs=get;list;watch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=reportprocessors/status,verbs=get;update;patch
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

	// Step 3: Reconcile ENC Secret (if nodeClassifierRef is set)
	if err := r.reconcileENCSecret(ctx, cfg); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling ENC Secret: %w", err)
	}

	// Step 4: Ensure server ServiceAccount exists
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
		Watches(&openvoxv1alpha1.NodeClassifier{}, handler.EnqueueRequestsFromMapFunc(
			r.enqueueConfigsForNodeClassifier(mgr.GetClient()),
		)).
		Watches(&openvoxv1alpha1.ReportProcessor{}, handler.EnqueueRequestsFromMapFunc(
			r.enqueueConfigsForReportProcessor(mgr.GetClient()),
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
	if cfg.Spec.AuthorityRef == "" {
		return nil
	}
	ca := &openvoxv1alpha1.CertificateAuthority{}
	if err := r.Get(ctx, types.NamespacedName{Name: cfg.Spec.AuthorityRef, Namespace: cfg.Namespace}, ca); err != nil {
		return nil
	}
	return ca
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
		// This keeps puppet.conf static — policy changes only update the Secret,
		// which kubelet syncs without a pod restart.
		fmt.Fprintf(&sb, "autosign = %s\n", autosignBinaryPath)
	}

	// ENC settings
	if cfg.Spec.NodeClassifierRef != "" {
		sb.WriteString("node_terminus = exec\n")
		fmt.Fprintf(&sb, "external_nodes = %s\n", encBinaryPath)
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

// reconcileAutosignSecrets reconciles the autosign policy Secret for the CA referenced by this Config.
func (r *ConfigReconciler) reconcileAutosignSecrets(ctx context.Context, cfg *openvoxv1alpha1.Config) error {
	if cfg.Spec.AuthorityRef == "" {
		return nil
	}
	ca := &openvoxv1alpha1.CertificateAuthority{}
	if err := r.Get(ctx, types.NamespacedName{Name: cfg.Spec.AuthorityRef, Namespace: cfg.Namespace}, ca); err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if err := r.reconcileAutosignSecret(ctx, cfg, ca); err != nil {
		return fmt.Errorf("reconciling autosign Secret for CA %s: %w", ca.Name, err)
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
	if statusErr := r.Status().Update(ctx, sp); statusErr != nil {
		log.FromContext(ctx).Error(statusErr, "failed to update SigningPolicy status", "name", sp.Name)
	}
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

		// Enqueue all Configs whose authorityRef points to this CA
		cfgList := &openvoxv1alpha1.ConfigList{}
		if err := c.List(ctx, cfgList, client.InNamespace(ca.Namespace)); err != nil {
			return nil
		}

		var requests []reconcile.Request
		for _, cfg := range cfgList.Items {
			if cfg.Spec.AuthorityRef == ca.Name {
				requests = append(requests, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: cfg.Name, Namespace: cfg.Namespace},
				})
			}
		}
		return requests
	}
}

// --- ENC (External Node Classifier) ---

const encBinaryPath = "/usr/local/bin/openvox-enc"

// reconcileENCSecret renders the ENC config into a Secret when nodeClassifierRef is set.
func (r *ConfigReconciler) reconcileENCSecret(ctx context.Context, cfg *openvoxv1alpha1.Config) error {
	if cfg.Spec.NodeClassifierRef == "" {
		return nil
	}
	logger := log.FromContext(ctx)

	nc := &openvoxv1alpha1.NodeClassifier{}
	if err := r.Get(ctx, types.NamespacedName{Name: cfg.Spec.NodeClassifierRef, Namespace: cfg.Namespace}, nc); err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}

	encYAML, renderErr := r.renderENCConfig(ctx, cfg, nc)
	if renderErr != nil {
		r.updateNodeClassifierStatus(ctx, nc, renderErr)
		return fmt.Errorf("rendering ENC config: %w", renderErr)
	}

	r.updateNodeClassifierStatus(ctx, nc, nil)

	secretName := fmt.Sprintf("%s-enc", cfg.Name)
	data := map[string][]byte{
		"enc.yaml": []byte(encYAML),
	}

	existing := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: cfg.Namespace}, existing)
	if errors.IsNotFound(err) {
		logger.Info("creating ENC Secret", "name", secretName)
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

// encYAMLConfig mirrors the YAML structure read by openvox-enc.
type encYAMLConfig struct {
	URL            string         `yaml:"url"`
	Method         string         `yaml:"method"`
	Path           string         `yaml:"path"`
	Body           string         `yaml:"body,omitempty"`
	ResponseFormat string         `yaml:"responseFormat"`
	TimeoutSeconds int32          `yaml:"timeoutSeconds"`
	Auth           *encAuthConfig `yaml:"auth,omitempty"`
	Cache          *encCache      `yaml:"cache,omitempty"`
	SSL            encSSLConfig   `yaml:"ssl"`
}

type encAuthConfig struct {
	Type     string `yaml:"type"`
	Header   string `yaml:"header,omitempty"`
	Token    string `yaml:"token,omitempty"`
	Username string `yaml:"username,omitempty"`
	Password string `yaml:"password,omitempty"`
}

type encCache struct {
	Enabled   bool   `yaml:"enabled"`
	Directory string `yaml:"directory"`
}

type encSSLConfig struct {
	CertFile string `yaml:"certFile"`
	KeyFile  string `yaml:"keyFile"`
	CAFile   string `yaml:"caFile"`
}

// renderENCConfig renders the enc.yaml that openvox-enc reads.
func (r *ConfigReconciler) renderENCConfig(ctx context.Context, cfg *openvoxv1alpha1.Config, nc *openvoxv1alpha1.NodeClassifier) (string, error) {
	timeout := int32(10)
	if nc.Spec.TimeoutSeconds != 0 {
		timeout = nc.Spec.TimeoutSeconds
	}

	encCfg := encYAMLConfig{
		URL:            nc.Spec.URL,
		Method:         nc.Spec.Request.Method,
		Path:           nc.Spec.Request.Path,
		Body:           nc.Spec.Request.Body,
		ResponseFormat: nc.Spec.Response.Format,
		TimeoutSeconds: timeout,
		SSL: encSSLConfig{
			CertFile: "/etc/puppetlabs/puppet/ssl/certs/puppet.pem",
			KeyFile:  "/etc/puppetlabs/puppet/ssl/private_keys/puppet.pem",
			CAFile:   "/etc/puppetlabs/puppet/ssl/certs/ca.pem",
		},
	}

	// Auth
	if nc.Spec.Auth != nil {
		auth := &encAuthConfig{}
		switch {
		case nc.Spec.Auth.MTLS:
			auth.Type = "mtls"
		case nc.Spec.Auth.Token != nil:
			auth.Type = "token"
			auth.Header = nc.Spec.Auth.Token.Header
			token, err := r.resolveSecretKey(ctx, cfg.Namespace,
				nc.Spec.Auth.Token.SecretKeyRef.Name, nc.Spec.Auth.Token.SecretKeyRef.Key)
			if err != nil {
				return "", fmt.Errorf("resolving token secret: %w", err)
			}
			auth.Token = token
		case nc.Spec.Auth.Bearer != nil:
			auth.Type = "bearer"
			token, err := r.resolveSecretKey(ctx, cfg.Namespace,
				nc.Spec.Auth.Bearer.SecretKeyRef.Name, nc.Spec.Auth.Bearer.SecretKeyRef.Key)
			if err != nil {
				return "", fmt.Errorf("resolving bearer secret: %w", err)
			}
			auth.Token = token
		case nc.Spec.Auth.Basic != nil:
			auth.Type = "basic"
			username, err := r.resolveSecretKey(ctx, cfg.Namespace,
				nc.Spec.Auth.Basic.SecretRef.Name, nc.Spec.Auth.Basic.SecretRef.UsernameKey)
			if err != nil {
				return "", fmt.Errorf("resolving basic auth username: %w", err)
			}
			password, err := r.resolveSecretKey(ctx, cfg.Namespace,
				nc.Spec.Auth.Basic.SecretRef.Name, nc.Spec.Auth.Basic.SecretRef.PasswordKey)
			if err != nil {
				return "", fmt.Errorf("resolving basic auth password: %w", err)
			}
			auth.Username = username
			auth.Password = password
		}
		encCfg.Auth = auth
	}

	// Cache
	if nc.Spec.Cache != nil && nc.Spec.Cache.Enabled {
		dir := "/var/cache/openvox-enc"
		if nc.Spec.Cache.Directory != "" {
			dir = nc.Spec.Cache.Directory
		}
		encCfg.Cache = &encCache{Enabled: true, Directory: dir}
	}

	out, err := yaml.Marshal(encCfg)
	if err != nil {
		return "", fmt.Errorf("marshaling ENC config: %w", err)
	}
	return string(out), nil
}

// updateNodeClassifierStatus sets the phase and condition on a NodeClassifier.
func (r *ConfigReconciler) updateNodeClassifierStatus(ctx context.Context, nc *openvoxv1alpha1.NodeClassifier, err error) {
	if err != nil {
		nc.Status.Phase = openvoxv1alpha1.NodeClassifierPhaseError
		meta.SetStatusCondition(&nc.Status.Conditions, metav1.Condition{
			Type:               openvoxv1alpha1.ConditionNodeClassifierReady,
			Status:             metav1.ConditionFalse,
			Reason:             "Error",
			Message:            err.Error(),
			LastTransitionTime: metav1.Now(),
		})
	} else {
		nc.Status.Phase = openvoxv1alpha1.NodeClassifierPhaseActive
		meta.SetStatusCondition(&nc.Status.Conditions, metav1.Condition{
			Type:               openvoxv1alpha1.ConditionNodeClassifierReady,
			Status:             metav1.ConditionTrue,
			Reason:             "ConfigRendered",
			Message:            "Node classifier configuration is active",
			LastTransitionTime: metav1.Now(),
		})
	}
	if statusErr := r.Status().Update(ctx, nc); statusErr != nil {
		log.FromContext(ctx).Error(statusErr, "failed to update NodeClassifier status", "name", nc.Name)
	}
}

// enqueueConfigsForNodeClassifier maps NodeClassifier changes to Config reconciles.
func (r *ConfigReconciler) enqueueConfigsForNodeClassifier(c client.Reader) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		nc, ok := obj.(*openvoxv1alpha1.NodeClassifier)
		if !ok {
			return nil
		}

		cfgList := &openvoxv1alpha1.ConfigList{}
		if err := c.List(ctx, cfgList, client.InNamespace(nc.Namespace)); err != nil {
			return nil
		}

		var requests []reconcile.Request
		for _, cfg := range cfgList.Items {
			if cfg.Spec.NodeClassifierRef == nc.Name {
				requests = append(requests, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: cfg.Name, Namespace: cfg.Namespace},
				})
			}
		}
		return requests
	}
}

// --- ReportProcessor ---

// hasReportProcessors returns true if any ReportProcessor references this Config.
func (r *ConfigReconciler) hasReportProcessors(ctx context.Context, cfg *openvoxv1alpha1.Config) (bool, error) {
	rpList := &openvoxv1alpha1.ReportProcessorList{}
	if err := r.List(ctx, rpList, client.InNamespace(cfg.Namespace)); err != nil {
		return false, err
	}
	for _, rp := range rpList.Items {
		if rp.Spec.ConfigRef == cfg.Name {
			return true, nil
		}
	}
	return false, nil
}

// enqueueConfigsForReportProcessor maps ReportProcessor changes to Config reconciles.
func (r *ConfigReconciler) enqueueConfigsForReportProcessor(c client.Reader) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		rp, ok := obj.(*openvoxv1alpha1.ReportProcessor)
		if !ok {
			return nil
		}

		// Enqueue the Config that this ReportProcessor references
		if rp.Spec.ConfigRef != "" {
			return []reconcile.Request{{
				NamespacedName: types.NamespacedName{Name: rp.Spec.ConfigRef, Namespace: rp.Namespace},
			}}
		}
		return nil
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
