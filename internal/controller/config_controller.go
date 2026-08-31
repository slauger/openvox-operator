package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
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
	Scheme   *runtime.Scheme
	Recorder events.EventRecorder
}

// Event reasons for Config.
const (
	EventReasonReportWebhookRenderFailed = "ReportWebhookRenderFailed"
)

// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=configs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=configs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=configs/finalizers,verbs=update
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=signingpolicies,verbs=get;list;watch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=signingpolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=nodeclassifiers,verbs=get;list;watch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=nodeclassifiers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=databases,verbs=get;list;watch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=certificateauthorities,verbs=get;list;watch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=reportprocessors,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=configmaps;serviceaccounts;secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=coordination.k8s.io,resources=leases,verbs=get;list;watch;create;update;patch;delete

func (r *ConfigReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	cfg := &openvoxv1alpha1.Config{}
	if err := r.Get(ctx, req.NamespacedName, cfg); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Pausing comes after the deletion path: a paused resource must still be
	// deletable, otherwise the annotation turns into a trap.
	if paused, err := reconcilePauseState(ctx, r.Client, cfg, &cfg.Status.Conditions); err != nil {
		return ctrl.Result{}, err
	} else if paused {
		logger.Info("reconciliation paused by annotation", "name", cfg.Name)
		return ctrl.Result{}, nil
	}

	// Set initial phase
	if cfg.Status.Phase == "" {
		if err := updateStatusWithRetry(ctx, r.Client, cfg, func() {
			cfg.Status.Phase = openvoxv1alpha1.ConfigPhasePending
		}); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Step 1: Reconcile ConfigMaps
	logger.Info("reconciling ConfigMaps")
	if err := r.reconcileConfigMap(ctx, cfg); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling ConfigMaps: %w", err)
	}

	// Step 2: Reconcile autosign policy Secrets for all CAs in this Config
	if err := r.reconcileAutosignSecrets(ctx, cfg); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling autosign Secrets: %w", err)
	}

	// Step 3: Reconcile ENC Secret (if nodeClassifierRef is set)
	if err := r.reconcileENCSecret(ctx, cfg); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling ENC Secret: %w", err)
	}

	// Step 4: Reconcile report-webhook Secret (if any ReportProcessor references this Config)
	if err := r.reconcileReportWebhookSecret(ctx, cfg); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling report-webhook Secret: %w", err)
	}

	// Step 5: Ensure server ServiceAccount exists
	if err := r.reconcileServerServiceAccount(ctx, cfg); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling server ServiceAccount: %w", err)
	}

	// Update status
	if err := updateStatusWithRetry(ctx, r.Client, cfg, func() {
		cfg.Status.ObservedGeneration = cfg.Generation
		cfg.Status.Phase = openvoxv1alpha1.ConfigPhaseRunning
		meta.SetStatusCondition(&cfg.Status.Conditions, metav1.Condition{
			Type:               openvoxv1alpha1.ConditionConfigReady,
			Status:             metav1.ConditionTrue,
			Reason:             "ConfigMapsCreated",
			Message:            "Configuration ConfigMaps are up to date",
			ObservedGeneration: cfg.Generation,
		})
	}); err != nil {
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
		Watches(&openvoxv1alpha1.Database{}, handler.EnqueueRequestsFromMapFunc(
			r.enqueueConfigsForDatabase(mgr.GetClient()),
		)).
		Watches(&openvoxv1alpha1.CertificateAuthority{}, handler.EnqueueRequestsFromMapFunc(
			r.enqueueConfigsForCertificateAuthority(mgr.GetClient()),
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

	puppetDBConf, err := r.renderPuppetDBConf(ctx, cfg)
	if err != nil {
		return fmt.Errorf("rendering puppetdb.conf: %w", err)
	}

	ca, err := r.findCertificateAuthority(ctx, cfg)
	if err != nil {
		return err
	}

	data := map[string]string{
		"puppet.conf":       puppetConf,
		"puppetdb.conf":     puppetDBConf,
		"webserver.conf":    r.renderWebserverConf(cfg),
		"webserver-ca.conf": r.renderWebserverConfCA(cfg),
		"puppetserver.conf": r.renderPuppetserverConf(cfg),
		"auth.conf":         r.renderAuthConf(cfg, ca),
		"ca.conf":           r.renderCAConf(ca),
		"product.conf":      "product: {\n    check-for-updates: false\n}\n",
		"logback.xml":       r.renderLogbackXML(cfg),
		"metrics.conf":      r.renderMetricsConf(cfg),
		"ca-enabled.cfg":    "puppetlabs.services.ca.certificate-authority-service/certificate-authority-service\npuppetlabs.trapperkeeper.services.watcher.filesystem-watch-service/filesystem-watch-service\n",
		"ca-disabled.cfg":   "puppetlabs.services.ca.certificate-authority-disabled-service/certificate-authority-disabled-service\npuppetlabs.trapperkeeper.services.watcher.filesystem-watch-service/filesystem-watch-service\n",
	}

	// Route the facts terminus to PuppetDB (only when PuppetDB is the active
	// backend). Without this, facts never reach PuppetDB. The Server controller
	// mounts it at $confdir/routes.yaml under the same condition.
	if routes := renderRoutesYAML(cfg); routes != "" {
		data["routes.yaml"] = routes
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{Name: configMapName, Namespace: cfg.Namespace},
	}
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, cm, func() error {
		if err := assertControlledBy(cm, cfg, "ConfigMap"); err != nil {
			return err
		}
		cm.Labels = configLabels(cfg.Name)
		cm.Data = data
		return controllerutil.SetControllerReference(cfg, cm, r.Scheme)
	})
	if err != nil {
		return fmt.Errorf("reconciling ConfigMap %s: %w", configMapName, err)
	}
	if op == controllerutil.OperationResultCreated {
		logger.Info("created ConfigMap", "name", configMapName)
	}
	return nil
}

// reconcileSecret creates or updates a Secret owned by the given Config.
func (r *ConfigReconciler) reconcileSecret(ctx context.Context, cfg *openvoxv1alpha1.Config, name string, data map[string][]byte) error {
	logger := log.FromContext(ctx)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: cfg.Namespace},
	}
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		if err := assertControlledBy(secret, cfg, "Secret"); err != nil {
			return err
		}
		secret.Labels = configLabels(cfg.Name)
		secret.Data = data
		return controllerutil.SetControllerReference(cfg, secret, r.Scheme)
	})
	if err != nil {
		return fmt.Errorf("reconciling Secret %s: %w", name, err)
	}
	if op == controllerutil.OperationResultCreated {
		logger.Info("created Secret", "name", name)
	}
	return nil
}

func (r *ConfigReconciler) enqueueConfigsForDatabase(c client.Reader) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		db, ok := obj.(*openvoxv1alpha1.Database)
		if !ok {
			return nil
		}

		cfgList := &openvoxv1alpha1.ConfigList{}
		if err := c.List(ctx, cfgList, client.InNamespace(db.Namespace)); err != nil {
			log.FromContext(ctx).Error(err, "failed to list Configs in watcher")
			return nil
		}

		var requests []reconcile.Request
		for _, cfg := range cfgList.Items {
			if cfg.Spec.DatabaseRef == db.Name {
				requests = append(requests, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: cfg.Name, Namespace: cfg.Namespace},
				})
			}
		}
		return requests
	}
}

// enqueueConfigsForCertificateAuthority maps a CertificateAuthority change to
// every Config referencing it. puppet.conf, auth.conf and ca.conf are all
// rendered from the CA spec, so the Config has to re-render when it changes --
// including when the CA is created after the Config.
func (r *ConfigReconciler) enqueueConfigsForCertificateAuthority(c client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		cfgList := &openvoxv1alpha1.ConfigList{}
		if err := c.List(ctx, cfgList,
			client.InNamespace(obj.GetNamespace()),
			client.MatchingFields{IndexAuthorityRef: obj.GetName()}); err != nil {
			log.FromContext(ctx).Error(err, "failed to list Configs in watcher", "ca", obj.GetName())
			return nil
		}

		requests := make([]reconcile.Request, 0, len(cfgList.Items))
		for _, cfg := range cfgList.Items {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{Name: cfg.Name, Namespace: cfg.Namespace},
			})
		}
		return requests
	}
}

// findCertificateAuthority resolves the Config's authorityRef.
//
// It returns (nil, nil) when no authorityRef is set or the referenced
// CertificateAuthority does not exist -- both are legitimate states the caller
// renders around. Any other error is returned so the reconcile aborts instead
// of writing a configuration that silently omits the CA settings.
func (r *ConfigReconciler) findCertificateAuthority(ctx context.Context, cfg *openvoxv1alpha1.Config) (*openvoxv1alpha1.CertificateAuthority, error) {
	if cfg.Spec.AuthorityRef == "" {
		return nil, nil
	}
	ca := &openvoxv1alpha1.CertificateAuthority{}
	if err := r.Get(ctx, types.NamespacedName{Name: cfg.Spec.AuthorityRef, Namespace: cfg.Namespace}, ca); err != nil {
		if errors.IsNotFound(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("getting CertificateAuthority %s: %w", cfg.Spec.AuthorityRef, err)
	}
	return ca, nil
}
