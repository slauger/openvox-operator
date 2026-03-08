package controller

import (
	"context"
	"fmt"
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
	"sigs.k8s.io/controller-runtime/pkg/log"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// EnvironmentReconciler reconciles an Environment object.
type EnvironmentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=environments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=environments/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=environments/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=configmaps;serviceaccounts,verbs=get;list;watch;create;update;patch;delete

func (r *EnvironmentReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	env := &openvoxv1alpha1.Environment{}
	if err := r.Get(ctx, req.NamespacedName, env); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Set initial phase
	if env.Status.Phase == "" {
		env.Status.Phase = openvoxv1alpha1.EnvironmentPhasePending
		if err := r.Status().Update(ctx, env); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Step 1: Reconcile ConfigMaps
	logger.Info("reconciling ConfigMaps")
	if err := r.reconcileConfigMap(ctx, env); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling ConfigMaps: %w", err)
	}
	meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
		Type:               openvoxv1alpha1.ConditionConfigReady,
		Status:             metav1.ConditionTrue,
		Reason:             "ConfigMapsCreated",
		Message:            "Configuration ConfigMaps are up to date",
		LastTransitionTime: metav1.Now(),
	})

	// Step 2: Ensure server ServiceAccount exists
	if err := r.reconcileServerServiceAccount(ctx, env); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling server ServiceAccount: %w", err)
	}

	// Update status
	env.Status.Phase = openvoxv1alpha1.EnvironmentPhaseRunning

	if err := r.Status().Update(ctx, env); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *EnvironmentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&openvoxv1alpha1.Environment{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.ServiceAccount{}).
		Complete(r)
}

// --- ConfigMap ---

func (r *EnvironmentReconciler) reconcileConfigMap(ctx context.Context, env *openvoxv1alpha1.Environment) error {
	logger := log.FromContext(ctx)
	configMapName := fmt.Sprintf("%s-config", env.Name)

	data := map[string]string{
		"puppet.conf":       r.renderPuppetConf(env),
		"puppetdb.conf":     r.renderPuppetDBConf(env),
		"webserver.conf":    r.renderWebserverConf(env),
		"puppetserver.conf": r.renderPuppetserverConf(env),
		"auth.conf":         r.renderAuthConf(),
		"product.conf":      "product: {\n    check-for-updates: false\n}\n",
		"ca-enabled.cfg":    "puppetlabs.services.ca.certificate-authority-service/certificate-authority-service\npuppetlabs.trapperkeeper.services.watcher.filesystem-watch-service/filesystem-watch-service\n",
		"ca-disabled.cfg":   "puppetlabs.services.ca.certificate-authority-disabled-service/certificate-authority-disabled-service\npuppetlabs.trapperkeeper.services.watcher.filesystem-watch-service/filesystem-watch-service\n",
	}

	cm := &corev1.ConfigMap{}
	err := r.Get(ctx, types.NamespacedName{Name: configMapName, Namespace: env.Namespace}, cm)
	if errors.IsNotFound(err) {
		logger.Info("creating ConfigMap", "name", configMapName)
		cm = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      configMapName,
				Namespace: env.Namespace,
				Labels:    environmentLabels(env.Name),
			},
			Data: data,
		}
		if err := controllerutil.SetControllerReference(env, cm, r.Scheme); err != nil {
			return err
		}
		return r.Create(ctx, cm)
	} else if err != nil {
		return err
	}

	cm.Data = data
	return r.Update(ctx, cm)
}

func (r *EnvironmentReconciler) renderPuppetConf(env *openvoxv1alpha1.Environment) string {
	var sb strings.Builder
	sb.WriteString("[main]\n")
	sb.WriteString("confdir = /etc/puppetlabs/puppet\n")
	sb.WriteString("vardir = /opt/puppetlabs/puppet/cache\n")
	sb.WriteString("logdir = /var/log/puppetlabs/puppet\n")
	sb.WriteString("codedir = /etc/puppetlabs/code\n")
	sb.WriteString("rundir = /var/run/puppetlabs\n")
	sb.WriteString("manage_internal_file_permissions = false\n")

	if env.Spec.Puppet.EnvironmentPath != "" {
		fmt.Fprintf(&sb, "environmentpath = %s\n", env.Spec.Puppet.EnvironmentPath)
	}

	if env.Spec.Puppet.HieraConfig != "" {
		fmt.Fprintf(&sb, "hiera_config = %s\n", env.Spec.Puppet.HieraConfig)
	}

	sb.WriteString("\n[server]\n")

	if env.Spec.Puppet.EnvironmentTimeout != "" {
		fmt.Fprintf(&sb, "environment_timeout = %s\n", env.Spec.Puppet.EnvironmentTimeout)
	}

	if env.Spec.Puppet.Storeconfigs {
		sb.WriteString("storeconfigs = true\n")
		if env.Spec.Puppet.StoreBackend != "" {
			fmt.Fprintf(&sb, "storeconfigs_backend = %s\n", env.Spec.Puppet.StoreBackend)
		}
	}

	if env.Spec.Puppet.Reports != "" {
		fmt.Fprintf(&sb, "reports = %s\n", env.Spec.Puppet.Reports)
	}

	if env.Spec.CA.TTL > 0 {
		fmt.Fprintf(&sb, "ca_ttl = %d\n", env.Spec.CA.TTL)
	}
	if env.Spec.CA.Autosign != "" {
		fmt.Fprintf(&sb, "autosign = %s\n", env.Spec.CA.Autosign)
	}

	for k, v := range env.Spec.Puppet.ExtraConfig {
		fmt.Fprintf(&sb, "%s = %s\n", k, v)
	}

	return sb.String()
}

func (r *EnvironmentReconciler) renderPuppetDBConf(env *openvoxv1alpha1.Environment) string {
	if len(env.Spec.PuppetDB.ServerURLs) == 0 {
		return "[main]\nserver_urls = https://openvoxdb:8081\nsoft_write_failure = true\n"
	}
	return fmt.Sprintf("[main]\nserver_urls = %s\nsoft_write_failure = true\n",
		strings.Join(env.Spec.PuppetDB.ServerURLs, ","))
}

func (r *EnvironmentReconciler) renderWebserverConf(env *openvoxv1alpha1.Environment) string {
	return `webserver: {
    client-auth: want
    ssl-host: 0.0.0.0
    ssl-port: 8140
    ssl-cert: /etc/puppetlabs/puppet/ssl/certs/puppet.pem
    ssl-key: /etc/puppetlabs/puppet/ssl/private_keys/puppet.pem
    ssl-ca-cert: /etc/puppetlabs/puppet/ssl/certs/ca.pem
    ssl-crl-path: /etc/puppetlabs/puppet/ssl/crl.pem
}
`
}

func (r *EnvironmentReconciler) renderPuppetserverConf(env *openvoxv1alpha1.Environment) string {
	return `jruby-puppet: {
    ruby-load-path: [/opt/puppetlabs/puppet/lib/ruby/vendor_ruby]
    gem-home: /opt/puppetlabs/server/data/puppetserver/jruby-gems
    gem-path: [${jruby-puppet.gem-home}, "/opt/puppetlabs/server/data/puppetserver/vendored-jruby-gems", "/opt/puppetlabs/puppet/lib/ruby/vendor_gems"]
    master-conf-dir: /etc/puppetlabs/puppet
    master-code-dir: /etc/puppetlabs/code
    master-var-dir: /opt/puppetlabs/server/data/puppetserver
    master-run-dir: /var/run/puppetlabs/puppetserver
    master-log-dir: /var/log/puppetlabs/puppetserver
    max-active-instances: 1
    max-requests-per-instance: 0
}

http-client: {
}

profiler: {
}

dropsonde: {
    enabled: false
}
`
}

func (r *EnvironmentReconciler) renderAuthConf() string {
	return `authorization: {
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
`
}

// --- Server ServiceAccount ---

func (r *EnvironmentReconciler) reconcileServerServiceAccount(ctx context.Context, env *openvoxv1alpha1.Environment) error {
	saName := fmt.Sprintf("%s-server", env.Name)
	automount := false

	sa := &corev1.ServiceAccount{}
	if err := r.Get(ctx, types.NamespacedName{Name: saName, Namespace: env.Namespace}, sa); errors.IsNotFound(err) {
		sa = &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      saName,
				Namespace: env.Namespace,
				Labels:    environmentLabels(env.Name),
			},
			AutomountServiceAccountToken: &automount,
		}
		if err := controllerutil.SetControllerReference(env, sa, r.Scheme); err != nil {
			return err
		}
		return r.Create(ctx, sa)
	} else {
		return err
	}
}
