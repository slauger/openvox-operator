package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
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
// +kubebuilder:rbac:groups="",resources=configmaps;secrets;services;serviceaccounts;persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles;rolebindings,verbs=get;list;watch;create;update;patch;delete

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

	// Step 1b: Ensure server ServiceAccount exists
	if err := r.reconcileServerServiceAccount(ctx, env); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling server ServiceAccount: %w", err)
	}

	// Step 2: CA lifecycle
	// Step 2a: Ensure CA PVC exists
	if err := r.reconcileCAPVC(ctx, env); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling CA PVC: %w", err)
	}

	// Step 2b: Ensure CA setup RBAC exists
	if err := r.reconcileCASetupRBAC(ctx, env); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling CA setup RBAC: %w", err)
	}

	// Step 2c: Check if CA is initialized (Secret with ca_crt.pem exists)
	caSecretName := fmt.Sprintf("%s-ca", env.Name)
	caSecret := &corev1.Secret{}
	caInitialized := false
	if err := r.Get(ctx, types.NamespacedName{Name: caSecretName, Namespace: env.Namespace}, caSecret); err != nil {
		if !errors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
	} else if _, ok := caSecret.Data["ca_crt.pem"]; ok {
		caInitialized = true
	}

	if !caInitialized {
		logger.Info("CA not initialized, running setup job")
		env.Status.Phase = openvoxv1alpha1.EnvironmentPhaseCASetup
		if err := r.Status().Update(ctx, env); err != nil {
			return ctrl.Result{}, err
		}

		result, err := r.reconcileCASetupJob(ctx, env)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("reconciling CA setup job: %w", err)
		}
		if result.Requeue || result.RequeueAfter > 0 {
			return result, nil
		}
		// Job succeeded — Secret should now exist (created by the job itself)
		logger.Info("CA setup job completed, verifying Secret")
	}

	// Step 2c: CA Service
	if err := r.reconcileCAService(ctx, env); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling CA Service: %w", err)
	}

	// Step 2d: Default Server Service
	if err := r.reconcileServerService(ctx, env); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling Server Service: %w", err)
	}

	// Update status
	env.Status.CAReady = true
	env.Status.CASecretName = caSecretName
	env.Status.CAServiceName = fmt.Sprintf("%s-ca", env.Name)
	env.Status.Phase = openvoxv1alpha1.EnvironmentPhaseRunning
	meta.SetStatusCondition(&env.Status.Conditions, metav1.Condition{
		Type:               openvoxv1alpha1.ConditionCAInitialized,
		Status:             metav1.ConditionTrue,
		Reason:             "CASecretExists",
		Message:            "CA certificates are initialized",
		LastTransitionTime: metav1.Now(),
	})

	if err := r.Status().Update(ctx, env); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *EnvironmentReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&openvoxv1alpha1.Environment{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.Secret{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&batchv1.Job{}).
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

	if len(env.Spec.CA.DNSAltNames) > 0 {
		fmt.Fprintf(&sb, "dns_alt_names = %s\n", strings.Join(env.Spec.CA.DNSAltNames, ","))
	}

	if env.Spec.CA.Certname != "" {
		fmt.Fprintf(&sb, "certname = %s\n", env.Spec.CA.Certname)
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

// --- CA PVC ---

func (r *EnvironmentReconciler) reconcileCAPVC(ctx context.Context, env *openvoxv1alpha1.Environment) error {
	pvcName := fmt.Sprintf("%s-ca-data", env.Name)

	pvc := &corev1.PersistentVolumeClaim{}
	err := r.Get(ctx, types.NamespacedName{Name: pvcName, Namespace: env.Namespace}, pvc)
	if errors.IsNotFound(err) {
		storageSize := "1Gi"
		if env.Spec.CA.Storage.Size != "" {
			storageSize = env.Spec.CA.Storage.Size
		}

		pvc = &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      pvcName,
				Namespace: env.Namespace,
				Labels:    environmentLabels(env.Name),
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse(storageSize),
					},
				},
			},
		}

		if env.Spec.CA.Storage.StorageClass != "" {
			pvc.Spec.StorageClassName = &env.Spec.CA.Storage.StorageClass
		}

		if err := controllerutil.SetControllerReference(env, pvc, r.Scheme); err != nil {
			return err
		}
		return r.Create(ctx, pvc)
	}
	return err
}

// --- CA Setup RBAC ---

func (r *EnvironmentReconciler) reconcileCASetupRBAC(ctx context.Context, env *openvoxv1alpha1.Environment) error {
	saName := fmt.Sprintf("%s-ca-setup", env.Name)
	roleName := saName
	rbName := saName

	// ServiceAccount
	sa := &corev1.ServiceAccount{}
	if err := r.Get(ctx, types.NamespacedName{Name: saName, Namespace: env.Namespace}, sa); errors.IsNotFound(err) {
		sa = &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      saName,
				Namespace: env.Namespace,
				Labels:    environmentLabels(env.Name),
			},
		}
		if err := controllerutil.SetControllerReference(env, sa, r.Scheme); err != nil {
			return err
		}
		if err := r.Create(ctx, sa); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	// Role — allow creating/updating Secrets in the namespace
	role := &rbacv1.Role{}
	if err := r.Get(ctx, types.NamespacedName{Name: roleName, Namespace: env.Namespace}, role); errors.IsNotFound(err) {
		role = &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name:      roleName,
				Namespace: env.Namespace,
				Labels:    environmentLabels(env.Name),
			},
			Rules: []rbacv1.PolicyRule{
				{
					APIGroups: []string{""},
					Resources: []string{"secrets"},
					Verbs:     []string{"get", "create", "update", "patch"},
				},
			},
		}
		if err := controllerutil.SetControllerReference(env, role, r.Scheme); err != nil {
			return err
		}
		if err := r.Create(ctx, role); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	// RoleBinding
	rb := &rbacv1.RoleBinding{}
	if err := r.Get(ctx, types.NamespacedName{Name: rbName, Namespace: env.Namespace}, rb); errors.IsNotFound(err) {
		rb = &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      rbName,
				Namespace: env.Namespace,
				Labels:    environmentLabels(env.Name),
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "Role",
				Name:     roleName,
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					Name:      saName,
					Namespace: env.Namespace,
				},
			},
		}
		if err := controllerutil.SetControllerReference(env, rb, r.Scheme); err != nil {
			return err
		}
		if err := r.Create(ctx, rb); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}

	return nil
}

// --- CA Setup Job ---

func (r *EnvironmentReconciler) reconcileCASetupJob(ctx context.Context, env *openvoxv1alpha1.Environment) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	jobName := fmt.Sprintf("%s-ca-setup", env.Name)
	desiredImage := fmt.Sprintf("%s:%s", env.Spec.Image.Repository, env.Spec.Image.Tag)

	job := &batchv1.Job{}
	err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: env.Namespace}, job)
	if errors.IsNotFound(err) {
		logger.Info("creating CA setup job", "name", jobName)
		job = r.buildCASetupJob(env, jobName)
		if err := controllerutil.SetControllerReference(env, job, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Create(ctx, job); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	} else if err != nil {
		return ctrl.Result{}, err
	}

	// Check if the Job needs to be replaced (image changed)
	currentImage := ""
	if len(job.Spec.Template.Spec.Containers) > 0 {
		currentImage = job.Spec.Template.Spec.Containers[0].Image
	}

	if currentImage != desiredImage {
		logger.Info("deleting outdated CA setup job", "name", jobName, "reason", "image changed",
			"currentImage", currentImage, "desiredImage", desiredImage)
		propagation := metav1.DeletePropagationForeground
		if err := r.Delete(ctx, job, &client.DeleteOptions{
			PropagationPolicy: &propagation,
		}); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	if job.Status.Succeeded > 0 {
		logger.Info("CA setup job completed successfully")
		return ctrl.Result{}, nil
	}

	// Check if the Job has permanently failed (all retries exhausted)
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
			logger.Info("CA setup job permanently failed, recreating", "name", jobName)
			propagation := metav1.DeletePropagationForeground
			if err := r.Delete(ctx, job, &client.DeleteOptions{
				PropagationPolicy: &propagation,
			}); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
		}
	}

	// Job still running or retrying — wait
	return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
}

func (r *EnvironmentReconciler) buildCASetupJob(env *openvoxv1alpha1.Environment, name string) *batchv1.Job {
	image := fmt.Sprintf("%s:%s", env.Spec.Image.Repository, env.Spec.Image.Tag)
	backoffLimit := int32(3)
	saName := fmt.Sprintf("%s-ca-setup", env.Name)
	caSecretName := fmt.Sprintf("%s-ca", env.Name)

	var sb strings.Builder
	sb.WriteString("#!/bin/bash\nset -euo pipefail\n\n")

	// CA setup (idempotent)
	sb.WriteString("if [ -f /etc/puppetlabs/puppetserver/ca/ca_crt.pem ]; then\n")
	sb.WriteString("  echo \"CA already initialized, skipping setup.\"\nelse\n")
	sb.WriteString("  echo \"Starting CA setup...\"\n  puppetserver ca setup \\\n")
	sb.WriteString("      --config /etc/puppetlabs/puppet/puppet.conf")
	if env.Spec.CA.Certname != "" {
		fmt.Fprintf(&sb, " \\\n      --certname %s", env.Spec.CA.Certname)
	}
	if len(env.Spec.CA.DNSAltNames) > 0 {
		fmt.Fprintf(&sb, " \\\n      --subject-alt-names %s", strings.Join(env.Spec.CA.DNSAltNames, ","))
	}
	sb.WriteString("\n  echo \"CA setup complete.\"\nfi\n\n")

	// Create/update K8s Secret with CA public data
	fmt.Fprintf(&sb, `# Create K8s Secret with CA public data
NAMESPACE=$(cat /var/run/secrets/kubernetes.io/serviceaccount/namespace)
TOKEN=$(cat /var/run/secrets/kubernetes.io/serviceaccount/token)
CA_API="https://kubernetes.default.svc/api/v1/namespaces/${NAMESPACE}/secrets"
SECRET_NAME="%s"

CA_CRT=$(base64 -w0 /etc/puppetlabs/puppetserver/ca/ca_crt.pem)
CA_CRL=$(base64 -w0 /etc/puppetlabs/puppetserver/ca/ca_crl.pem)
INFRA_CRL=$(base64 -w0 /etc/puppetlabs/puppetserver/ca/infra_crl.pem)

PAYLOAD=$(cat <<ENDOFPAYLOAD
{
  "apiVersion": "v1",
  "kind": "Secret",
  "metadata": {
    "name": "${SECRET_NAME}",
    "namespace": "${NAMESPACE}",
    "labels": {
      "app.kubernetes.io/managed-by": "openvox-operator",
      "app.kubernetes.io/name": "openvox",
      "openvox.voxpupuli.org/environment": "%s"
    }
  },
  "data": {
    "ca_crt.pem": "${CA_CRT}",
    "ca_crl.pem": "${CA_CRL}",
    "infra_crl.pem": "${INFRA_CRL}"
  }
}
ENDOFPAYLOAD
)

# Try PUT (update), fall back to POST (create) on 404
HTTP_CODE=$(curl -sk -o /tmp/api-response -w '%%{http_code}' -X PUT \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  "${CA_API}/${SECRET_NAME}" -d "$PAYLOAD")

if [ "$HTTP_CODE" = "404" ]; then
  HTTP_CODE=$(curl -sk -o /tmp/api-response -w '%%{http_code}' -X POST \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    "${CA_API}" -d "$PAYLOAD")
fi

if [ "${HTTP_CODE:0:1}" != "2" ]; then
  echo "Failed to create/update CA Secret (HTTP ${HTTP_CODE}):" >&2
  cat /tmp/api-response >&2
  exit 1
fi

echo "CA Secret '${SECRET_NAME}' created/updated successfully."
`, caSecretName, env.Name)

	setupScript := sb.String()

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: env.Namespace,
			Labels:    environmentLabels(env.Name),
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoffLimit,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: environmentLabels(env.Name),
				},
				Spec: corev1.PodSpec{
					ServiceAccountName: saName,
					RestartPolicy:      corev1.RestartPolicyNever,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsUser:    int64Ptr(1001),
						RunAsGroup:   int64Ptr(0),
						RunAsNonRoot: boolPtr(true),
					},
					Containers: []corev1.Container{
						{
							Name:            "ca-setup",
							Image:           image,
							ImagePullPolicy: env.Spec.Image.PullPolicy,
							Command:         []string{"/bin/bash", "-c", setupScript},
							VolumeMounts: []corev1.VolumeMount{
								{
									Name:      "ca-data",
									MountPath: "/etc/puppetlabs/puppetserver/ca",
								},
								{
									Name:      "ssl",
									MountPath: "/etc/puppetlabs/puppet/ssl",
								},
								{
									Name:      "puppet-conf",
									MountPath: "/etc/puppetlabs/puppet/puppet.conf",
									SubPath:   "puppet.conf",
									ReadOnly:  true,
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "ca-data",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: fmt.Sprintf("%s-ca-data", env.Name),
								},
							},
						},
						{
							Name: "ssl",
							VolumeSource: corev1.VolumeSource{
								EmptyDir: &corev1.EmptyDirVolumeSource{},
							},
						},
						{
							Name: "puppet-conf",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: fmt.Sprintf("%s-config", env.Name),
									},
									Items: []corev1.KeyToPath{
										{Key: "puppet.conf", Path: "puppet.conf"},
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

// --- Default Services ---

func (r *EnvironmentReconciler) reconcileServerService(ctx context.Context, env *openvoxv1alpha1.Environment) error {
	svcName := fmt.Sprintf("%s-server", env.Name)

	svc := &corev1.Service{}
	err := r.Get(ctx, types.NamespacedName{Name: svcName, Namespace: env.Namespace}, svc)
	if errors.IsNotFound(err) {
		labels := environmentLabels(env.Name)
		// Default server service selects all server-role pods in this environment
		selector := map[string]string{
			LabelEnvironment: env.Name,
			LabelRole:        RoleServer,
		}

		svc = &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      svcName,
				Namespace: env.Namespace,
				Labels:    labels,
			},
			Spec: corev1.ServiceSpec{
				Selector: selector,
				Ports: []corev1.ServicePort{
					{
						Name:       "https",
						Port:       8140,
						TargetPort: intstr.FromInt32(8140),
						Protocol:   corev1.ProtocolTCP,
					},
				},
			},
		}

		if err := controllerutil.SetControllerReference(env, svc, r.Scheme); err != nil {
			return err
		}
		return r.Create(ctx, svc)
	}
	return err
}

func (r *EnvironmentReconciler) reconcileCAService(ctx context.Context, env *openvoxv1alpha1.Environment) error {
	svcName := fmt.Sprintf("%s-ca", env.Name)

	svc := &corev1.Service{}
	err := r.Get(ctx, types.NamespacedName{Name: svcName, Namespace: env.Namespace}, svc)
	if errors.IsNotFound(err) {
		labels := environmentLabels(env.Name)
		// CA Service selects pods with ca=true in this environment
		selector := map[string]string{
			LabelEnvironment: env.Name,
			LabelCA:          "true",
		}

		svc = &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      svcName,
				Namespace: env.Namespace,
				Labels:    labels,
			},
			Spec: corev1.ServiceSpec{
				Selector: selector,
				Ports: []corev1.ServicePort{
					{
						Name:       "https",
						Port:       8140,
						TargetPort: intstr.FromInt32(8140),
						Protocol:   corev1.ProtocolTCP,
					},
				},
			},
		}

		if err := controllerutil.SetControllerReference(env, svc, r.Scheme); err != nil {
			return err
		}
		return r.Create(ctx, svc)
	}
	return err
}
