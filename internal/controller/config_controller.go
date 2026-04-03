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

// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=configs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=configs/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=configs/finalizers,verbs=update
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=signingpolicies,verbs=get;list;watch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=signingpolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=nodeclassifiers,verbs=get;list;watch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=nodeclassifiers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=databases,verbs=get;list;watch
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

	// Step 4: Reconcile report-webhook Secret (if any ReportProcessor references this Config)
	if err := r.reconcileReportWebhookSecret(ctx, cfg); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling report-webhook Secret: %w", err)
	}

	// Step 5: Ensure server ServiceAccount exists
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
		Watches(&openvoxv1alpha1.Database{}, handler.EnqueueRequestsFromMapFunc(
			r.enqueueConfigsForDatabase(mgr.GetClient()),
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

	ca := r.findCertificateAuthority(ctx, cfg)

	data := map[string]string{
		"puppet.conf":       puppetConf,
		"puppetdb.conf":     puppetDBConf,
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

// reconcileSecret creates or updates a Secret owned by the given Config.
func (r *ConfigReconciler) reconcileSecret(ctx context.Context, cfg *openvoxv1alpha1.Config, name string, data map[string][]byte) error {
	logger := log.FromContext(ctx)
	existing := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: cfg.Namespace}, existing)
	if errors.IsNotFound(err) {
		logger.Info("creating Secret", "name", name)
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
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
