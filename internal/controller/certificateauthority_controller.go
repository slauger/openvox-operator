package controller

import (
	"context"
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// CertificateAuthorityReconciler reconciles a CertificateAuthority object.
type CertificateAuthorityReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder events.EventRecorder
}

// Event reasons for CertificateAuthority.
const (
	EventReasonCAInitialized    = "CAInitialized"
	EventReasonCAExternal       = "CAExternal"
	EventReasonCRLRefreshed     = "CRLRefreshed"
	EventReasonCRLRefreshFailed = "CRLRefreshFailed"
)

// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=certificateauthorities,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=certificateauthorities/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=certificateauthorities/finalizers,verbs=update
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=certificates,verbs=get;list;watch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=configs,verbs=get;list;watch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=servers,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims;secrets;serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles;rolebindings,verbs=get;list;watch;create;update;patch;delete

func (r *CertificateAuthorityReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	ca := &openvoxv1alpha1.CertificateAuthority{}
	if err := r.Get(ctx, req.NamespacedName, ca); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Set initial phase
	if ca.Status.Phase == "" {
		ca.Status.Phase = openvoxv1alpha1.CertificateAuthorityPhasePending
		if err := r.Status().Update(ctx, ca); err != nil {
			return ctrl.Result{}, err
		}
	}

	// External CA: delegate to dedicated reconciler, skip PVC/Job/Config
	if ca.Spec.External != nil {
		return r.reconcileExternalCA(ctx, ca)
	}

	// Resolve Config referencing this CA
	cfg := r.findConfigForCA(ctx, ca)
	if cfg == nil {
		logger.Info("waiting for a Config with authorityRef pointing to this CA", "ca", ca.Name)
		return ctrl.Result{RequeueAfter: RequeueIntervalShort}, nil
	}

	// Step 1: Ensure CA data PVC exists
	if err := r.reconcileCAPVC(ctx, ca); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling CA PVC: %w", err)
	}

	// Step 2: Discover Certificates referencing this CA
	certs, err := r.findCertificatesForCA(ctx, ca)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("finding certificates for CA: %w", err)
	}

	// Step 2a: Reconcile CA Service for internal operator communication
	if err := r.reconcileCAService(ctx, ca); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling CA Service: %w", err)
	}

	// Step 3: Ensure RBAC for CA setup job
	if err := r.reconcileCASetupRBAC(ctx, ca, certs); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling CA setup RBAC: %w", err)
	}

	// Step 4: Run CA setup job
	result, err := r.reconcileCASetupJob(ctx, ca, cfg, certs)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling CA setup job: %w", err)
	}
	if result.RequeueAfter > 0 {
		return result, nil
	}

	// CA is ready
	wasReady := ca.Status.Phase == openvoxv1alpha1.CertificateAuthorityPhaseReady
	caSecretName := fmt.Sprintf("%s-ca", ca.Name)
	ca.Status.Phase = openvoxv1alpha1.CertificateAuthorityPhaseReady
	ca.Status.CASecretName = caSecretName
	ca.Status.ServiceName = caInternalServiceName(ca.Name)
	ca.Status.NotAfter = r.extractCANotAfter(ctx, caSecretName, ca.Namespace)

	// Find the CA server cert's TLS secret for signing credentials
	if caCert := r.findCAServerCert(ctx, ca, certs); caCert != nil {
		ca.Status.SigningSecretName = fmt.Sprintf("%s-tls", caCert.Name)
	}
	meta.SetStatusCondition(&ca.Status.Conditions, metav1.Condition{
		Type:               openvoxv1alpha1.ConditionCAReady,
		Status:             metav1.ConditionTrue,
		Reason:             "CAInitialized",
		Message:            "CA is initialized and ready",
		LastTransitionTime: metav1.Now(),
	})

	if err := r.Status().Update(ctx, ca); err != nil {
		return ctrl.Result{}, err
	}

	if !wasReady {
		r.Recorder.Eventf(ca, nil, corev1.EventTypeNormal, EventReasonCAInitialized, "Reconcile", "CA is initialized and ready")
	}

	// Requeue if NotAfter could not be extracted (informer cache may not have synced yet)
	if ca.Status.NotAfter == nil {
		logger.Info("NotAfter not yet available, requeueing", "secret", caSecretName)
		return ctrl.Result{RequeueAfter: RequeueIntervalShort}, nil
	}

	// Periodic CRL refresh: fetch CRL from CA service and update the CRL secret
	crlResult, err := r.reconcileCRLRefresh(ctx, ca)
	if err != nil {
		logger.Info("CRL refresh failed, will retry", "error", err)
		r.Recorder.Eventf(ca, nil, corev1.EventTypeWarning, EventReasonCRLRefreshFailed, "Reconcile", "CRL refresh failed: %v", err)
		return ctrl.Result{RequeueAfter: RequeueIntervalCRL}, nil
	}
	return crlResult, nil
}

// findConfigForCA returns the first Config in the same namespace whose authorityRef matches this CA.
func (r *CertificateAuthorityReconciler) findConfigForCA(ctx context.Context, ca *openvoxv1alpha1.CertificateAuthority) *openvoxv1alpha1.Config {
	cfgList := &openvoxv1alpha1.ConfigList{}
	if err := r.List(ctx, cfgList, client.InNamespace(ca.Namespace)); err != nil {
		log.FromContext(ctx).Error(err, "failed to list Configs", "namespace", ca.Namespace)
		return nil
	}
	for i := range cfgList.Items {
		if cfgList.Items[i].Spec.AuthorityRef == ca.Name {
			return &cfgList.Items[i]
		}
	}
	return nil
}

func (r *CertificateAuthorityReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&openvoxv1alpha1.CertificateAuthority{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&batchv1.Job{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.ServiceAccount{}).
		Watches(&openvoxv1alpha1.Certificate{}, handler.EnqueueRequestsFromMapFunc(
			func(ctx context.Context, obj client.Object) []ctrl.Request {
				cert, ok := obj.(*openvoxv1alpha1.Certificate)
				if !ok || cert.Spec.AuthorityRef == "" {
					return nil
				}
				return []ctrl.Request{
					{NamespacedName: types.NamespacedName{Name: cert.Spec.AuthorityRef, Namespace: cert.Namespace}},
				}
			},
		)).
		Watches(&openvoxv1alpha1.Config{}, handler.EnqueueRequestsFromMapFunc(
			func(ctx context.Context, obj client.Object) []ctrl.Request {
				cfg, ok := obj.(*openvoxv1alpha1.Config)
				if !ok || cfg.Spec.AuthorityRef == "" {
					return nil
				}
				return []ctrl.Request{
					{NamespacedName: types.NamespacedName{Name: cfg.Spec.AuthorityRef, Namespace: cfg.Namespace}},
				}
			},
		)).
		Complete(r)
}

// reconcileExternalCA handles CertificateAuthority resources configured with spec.external.
// It validates the CA Secret (if referenced), sets the External phase, and triggers CRL refresh.
func (r *CertificateAuthorityReconciler) reconcileExternalCA(ctx context.Context, ca *openvoxv1alpha1.CertificateAuthority) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	ext := ca.Spec.External
	caSecretName := fmt.Sprintf("%s-ca", ca.Name)

	// If caSecretRef is set, validate the Secret exists and has ca_crt.pem
	if ext.CASecretRef != "" {
		caSecretName = ext.CASecretRef
		secret := &corev1.Secret{}
		if err := r.Get(ctx, types.NamespacedName{Name: ext.CASecretRef, Namespace: ca.Namespace}, secret); err != nil {
			if errors.IsNotFound(err) {
				logger.Info("waiting for CA Secret referenced by external CA", "secret", ext.CASecretRef)
				return ctrl.Result{RequeueAfter: RequeueIntervalShort}, nil
			}
			return ctrl.Result{}, fmt.Errorf("getting CA Secret %s: %w", ext.CASecretRef, err)
		}
		if len(secret.Data["ca_crt.pem"]) == 0 {
			logger.Info("CA Secret missing ca_crt.pem key", "secret", ext.CASecretRef)
			return ctrl.Result{RequeueAfter: RequeueIntervalShort}, nil
		}
	}

	wasExternal := ca.Status.Phase == openvoxv1alpha1.CertificateAuthorityPhaseExternal
	ca.Status.Phase = openvoxv1alpha1.CertificateAuthorityPhaseExternal
	ca.Status.CASecretName = caSecretName
	ca.Status.NotAfter = r.extractCANotAfter(ctx, caSecretName, ca.Namespace)

	meta.SetStatusCondition(&ca.Status.Conditions, metav1.Condition{
		Type:               openvoxv1alpha1.ConditionCAReady,
		Status:             metav1.ConditionTrue,
		Reason:             "ExternalCA",
		Message:            fmt.Sprintf("External CA configured at %s", ext.URL),
		LastTransitionTime: metav1.Now(),
	})

	if err := r.Status().Update(ctx, ca); err != nil {
		return ctrl.Result{}, err
	}

	if !wasExternal {
		r.Recorder.Eventf(ca, nil, corev1.EventTypeNormal, EventReasonCAExternal, "Reconcile", "External CA configured at %s", ext.URL)
	}

	// CRL refresh from external CA
	crlResult, err := r.reconcileCRLRefresh(ctx, ca)
	if err != nil {
		logger.Info("CRL refresh failed, will retry", "error", err)
		r.Recorder.Eventf(ca, nil, corev1.EventTypeWarning, EventReasonCRLRefreshFailed, "Reconcile", "CRL refresh failed: %v", err)
		return ctrl.Result{RequeueAfter: RequeueIntervalCRL}, nil
	}
	return crlResult, nil
}

// extractCANotAfter reads the ca_crt.pem from the CA Secret and returns its NotAfter time.
func (r *CertificateAuthorityReconciler) extractCANotAfter(ctx context.Context, secretName, namespace string) *metav1.Time {
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: namespace}, secret); err != nil {
		if !errors.IsNotFound(err) {
			log.FromContext(ctx).Error(err, "failed to get CA Secret", "name", secretName, "namespace", namespace)
		}
		return nil
	}
	return parseCertNotAfter(ctx, secret.Data["ca_crt.pem"])
}
