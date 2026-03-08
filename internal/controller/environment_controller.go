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

// EnvironmentReconciler reconciles an Environment object.
type EnvironmentReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=environments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=environments/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=environments/finalizers,verbs=update
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=signingpolicies,verbs=get;list;watch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=signingpolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=configmaps;serviceaccounts;secrets,verbs=get;list;watch;create;update;patch;delete

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

	// Step 2: Reconcile autosign policy Secrets for all CAs in this Environment
	if err := r.reconcileAutosignSecrets(ctx, env); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling autosign Secrets: %w", err)
	}

	// Step 3: Ensure server ServiceAccount exists
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
		Owns(&corev1.Secret{}).
		Watches(&openvoxv1alpha1.SigningPolicy{}, handler.EnqueueRequestsFromMapFunc(
			r.enqueueEnvironmentsForSigningPolicy(mgr.GetClient()),
		)).
		Complete(r)
}

// --- ConfigMap ---

func (r *EnvironmentReconciler) reconcileConfigMap(ctx context.Context, env *openvoxv1alpha1.Environment) error {
	logger := log.FromContext(ctx)
	configMapName := fmt.Sprintf("%s-config", env.Name)

	puppetConf, err := r.renderPuppetConf(ctx, env)
	if err != nil {
		return fmt.Errorf("rendering puppet.conf: %w", err)
	}

	data := map[string]string{
		"puppet.conf":       puppetConf,
		"puppetdb.conf":     r.renderPuppetDBConf(env),
		"webserver.conf":    r.renderWebserverConf(env),
		"puppetserver.conf": r.renderPuppetserverConf(env),
		"auth.conf":         r.renderAuthConf(),
		"product.conf":      "product: {\n    check-for-updates: false\n}\n",
		"ca-enabled.cfg":    "puppetlabs.services.ca.certificate-authority-service/certificate-authority-service\npuppetlabs.trapperkeeper.services.watcher.filesystem-watch-service/filesystem-watch-service\n",
		"ca-disabled.cfg":   "puppetlabs.services.ca.certificate-authority-disabled-service/certificate-authority-disabled-service\npuppetlabs.trapperkeeper.services.watcher.filesystem-watch-service/filesystem-watch-service\n",
	}

	cm := &corev1.ConfigMap{}
	err = r.Get(ctx, types.NamespacedName{Name: configMapName, Namespace: env.Namespace}, cm)
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

func (r *EnvironmentReconciler) findCertificateAuthority(ctx context.Context, env *openvoxv1alpha1.Environment) *openvoxv1alpha1.CertificateAuthority {
	caList := &openvoxv1alpha1.CertificateAuthorityList{}
	if err := r.List(ctx, caList, client.InNamespace(env.Namespace)); err != nil {
		return nil
	}
	for i := range caList.Items {
		if caList.Items[i].Spec.EnvironmentRef == env.Name {
			return &caList.Items[i]
		}
	}
	return nil
}

func (r *EnvironmentReconciler) renderPuppetConf(ctx context.Context, env *openvoxv1alpha1.Environment) (string, error) {
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

	// CA settings from CertificateAuthority (if one exists for this Environment)
	if ca := r.findCertificateAuthority(ctx, env); ca != nil {
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
		fmt.Fprintf(&sb, "autosign = %s --config %s\n", autosignBinaryPath, autosignPolicyPath)
	}

	for k, v := range env.Spec.Puppet.ExtraConfig {
		fmt.Fprintf(&sb, "%s = %s\n", k, v)
	}

	return sb.String(), nil
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

// --- Autosign Policy ---

const autosignPolicyPath = "/etc/puppetlabs/puppet/autosign-policy.yaml"
const autosignBinaryPath = "/usr/local/bin/openvox-autosign"

// findSigningPolicies returns all SigningPolicies referencing the given CA.
func (r *EnvironmentReconciler) findSigningPolicies(ctx context.Context, ca *openvoxv1alpha1.CertificateAuthority) []openvoxv1alpha1.SigningPolicy {
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

// reconcileAutosignSecrets reconciles autosign policy Secrets for all CAs in this Environment.
func (r *EnvironmentReconciler) reconcileAutosignSecrets(ctx context.Context, env *openvoxv1alpha1.Environment) error {
	caList := &openvoxv1alpha1.CertificateAuthorityList{}
	if err := r.List(ctx, caList, client.InNamespace(env.Namespace)); err != nil {
		return err
	}
	for i := range caList.Items {
		ca := &caList.Items[i]
		if ca.Spec.EnvironmentRef != env.Name {
			continue
		}
		if err := r.reconcileAutosignSecret(ctx, env, ca); err != nil {
			return fmt.Errorf("reconciling autosign Secret for CA %s: %w", ca.Name, err)
		}
	}
	return nil
}

// reconcileAutosignSecret renders the autosign policy config YAML into a Secret.
// The Secret is always created — the binary handles all cases (no policies = deny all,
// any:true = approve all). This keeps puppet.conf static and avoids pod restarts.
func (r *EnvironmentReconciler) reconcileAutosignSecret(ctx context.Context, env *openvoxv1alpha1.Environment, ca *openvoxv1alpha1.CertificateAuthority) error {
	logger := log.FromContext(ctx)
	secretName := fmt.Sprintf("%s-autosign-policy", ca.Name)

	policies := r.findSigningPolicies(ctx, ca)

	// Render policy config YAML
	policyYAML, renderErr := r.renderAutosignPolicyConfig(ctx, env.Namespace, policies)
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
	err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: env.Namespace}, existing)
	if errors.IsNotFound(err) {
		logger.Info("creating autosign policy Secret", "name", secretName)
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: env.Namespace,
				Labels:    environmentLabels(env.Name),
			},
			Data: data,
		}
		if err := controllerutil.SetControllerReference(env, secret, r.Scheme); err != nil {
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
func (r *EnvironmentReconciler) renderAutosignPolicyConfig(ctx context.Context, namespace string, policies []openvoxv1alpha1.SigningPolicy) (string, error) {
	var sb strings.Builder
	sb.WriteString("policies:\n")

	// Sort policies by name for deterministic output
	sort.Slice(policies, func(i, j int) bool {
		return policies[i].Name < policies[j].Name
	})

	for _, p := range policies {
		sb.WriteString(fmt.Sprintf("  - name: %s\n", p.Name))

		if p.Spec.Any {
			sb.WriteString("    any: true\n")
			continue
		}

		if p.Spec.Pattern != nil {
			sb.WriteString("    pattern:\n")
			sb.WriteString("      allow:\n")
			for _, a := range p.Spec.Pattern.Allow {
				sb.WriteString(fmt.Sprintf("        - %q\n", a))
			}
		}

		if p.Spec.PSK != nil {
			// Resolve PSK value from Secret
			pskValue, err := r.resolveSecretKey(ctx, namespace, p.Spec.PSK.SecretRef.Name, p.Spec.PSK.SecretRef.Key)
			if err != nil {
				r.updateSigningPolicyStatus(ctx, &p, err)
				return "", fmt.Errorf("resolving PSK for policy %s: %w", p.Name, err)
			}
			csrAttr := p.Spec.PSK.CSRAttribute
			if csrAttr == "" {
				csrAttr = "pp_preshared_key"
			}
			sb.WriteString("    psk:\n")
			sb.WriteString(fmt.Sprintf("      value: %q\n", pskValue))
			sb.WriteString(fmt.Sprintf("      csrAttribute: %s\n", csrAttr))
		}

		if p.Spec.Token != nil {
			// Resolve token values from Secret
			tokens, err := r.resolveSecretData(ctx, namespace, p.Spec.Token.SecretRef.Name)
			if err != nil {
				r.updateSigningPolicyStatus(ctx, &p, err)
				return "", fmt.Errorf("resolving tokens for policy %s: %w", p.Name, err)
			}
			csrAttr := p.Spec.Token.CSRAttribute
			if csrAttr == "" {
				csrAttr = "pp_auth_token"
			}
			sb.WriteString("    token:\n")
			sb.WriteString("      tokens:\n")
			// Sort token keys for deterministic output
			tokenKeys := make([]string, 0, len(tokens))
			for k := range tokens {
				tokenKeys = append(tokenKeys, k)
			}
			sort.Strings(tokenKeys)
			for _, k := range tokenKeys {
				sb.WriteString(fmt.Sprintf("        %s: %q\n", k, tokens[k]))
			}
			sb.WriteString(fmt.Sprintf("      csrAttribute: %s\n", csrAttr))
		}
	}

	return sb.String(), nil
}

// resolveSecretKey reads a specific key from a Secret.
func (r *EnvironmentReconciler) resolveSecretKey(ctx context.Context, namespace, secretName, key string) (string, error) {
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

// resolveSecretData reads all key-value pairs from a Secret.
func (r *EnvironmentReconciler) resolveSecretData(ctx context.Context, namespace, secretName string) (map[string]string, error) {
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: namespace}, secret); err != nil {
		return nil, fmt.Errorf("getting Secret %s: %w", secretName, err)
	}
	result := make(map[string]string, len(secret.Data))
	for k, v := range secret.Data {
		result[k] = string(v)
	}
	return result, nil
}

// updateSigningPolicyStatus sets the phase and condition on a SigningPolicy.
func (r *EnvironmentReconciler) updateSigningPolicyStatus(ctx context.Context, sp *openvoxv1alpha1.SigningPolicy, err error) {
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

// enqueueEnvironmentsForSigningPolicy maps SigningPolicy changes to Environment reconciles.
func (r *EnvironmentReconciler) enqueueEnvironmentsForSigningPolicy(c client.Reader) handler.MapFunc {
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

		// Enqueue the Environment referenced by the CA
		return []reconcile.Request{
			{NamespacedName: types.NamespacedName{Name: ca.Spec.EnvironmentRef, Namespace: ca.Namespace}},
		}
	}
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
