package controller

import (
	"context"
	"fmt"
	"sort"
	"strings"

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
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
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
	EventReasonCAInitialized          = "CAInitialized"
	EventReasonCAExternal             = "CAExternal"
	EventReasonCAWaitingForConfig     = "WaitingForConfig"
	EventReasonCRLRefreshed           = "CRLRefreshed"
	EventReasonCRLRefreshFailed       = "CRLRefreshFailed"
	EventReasonOperatorSigningCreated = "OperatorSigningCreated"
	EventReasonOperatorSigningReady   = "OperatorSigningReady"
	EventReasonCADeletionBlocked      = "CADeletionBlocked"
	EventReasonMultipleConfigs        = "MultipleConfigs"
)

// certificateAuthorityFinalizer guards the CA private key and the CA data PVC,
// both of which are garbage-collected through their owner reference the moment
// the CertificateAuthority is gone.
const certificateAuthorityFinalizer = "openvox.voxpupuli.org/ca-protection"

// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=certificateauthorities,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=certificateauthorities/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=certificateauthorities/finalizers,verbs=update
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=certificates,verbs=get;list;watch;create;update
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
		return ctrl.Result{}, fmt.Errorf("getting CertificateAuthority %s: %w", req.NamespacedName, err)
	}

	// Deletion destroys the CA private key and the CA data PVC through their
	// owner references. Hold it back while Certificates still depend on them.
	if !ca.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, ca)
	}

	if !controllerutil.ContainsFinalizer(ca, certificateAuthorityFinalizer) {
		patch := client.MergeFrom(ca.DeepCopy())
		controllerutil.AddFinalizer(ca, certificateAuthorityFinalizer)
		if err := r.Patch(ctx, ca, patch); err != nil {
			return ctrl.Result{}, fmt.Errorf("adding finalizer to CertificateAuthority %s: %w", ca.Name, err)
		}
	}

	// Pausing comes after the deletion path: a paused resource must still be
	// deletable, otherwise the annotation turns into a trap.
	if paused, err := reconcilePauseState(ctx, r.Client, ca, &ca.Status.Conditions); err != nil {
		return ctrl.Result{}, err
	} else if paused {
		logger.Info("reconciliation paused by annotation", "name", ca.Name)
		return ctrl.Result{}, nil
	}

	// Set initial phase
	if ca.Status.Phase == "" {
		if err := updateStatusWithRetry(ctx, r.Client, ca, func() {
			ca.Status.Phase = openvoxv1alpha1.CertificateAuthorityPhasePending
		}); err != nil {
			return ctrl.Result{}, fmt.Errorf("setting initial phase for CertificateAuthority %s: %w", ca.Name, err)
		}
	}

	// External CA: delegate to dedicated reconciler, skip PVC/Job/Config
	if ca.Spec.External != nil {
		return r.reconcileExternalCA(ctx, ca)
	}

	// Resolve Config referencing this CA
	cfg, err := r.findConfigForCA(ctx, ca)
	if err != nil {
		return ctrl.Result{}, err
	}
	if cfg == nil {
		logger.Info("waiting for a Config with authorityRef pointing to this CA", "ca", ca.Name)
		r.Recorder.Eventf(ca, nil, corev1.EventTypeNormal, EventReasonCAWaitingForConfig, "Reconcile",
			"Waiting for a Config with authorityRef: %s", ca.Name)
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

	// Adopt CA secrets created by the setup job (sets ownerReference for GC)
	caSecretName := fmt.Sprintf("%s-ca", ca.Name)
	caKeySecretName := fmt.Sprintf("%s-ca-key", ca.Name)
	caCRLSecretName := fmt.Sprintf("%s-ca-crl", ca.Name)
	for _, secretName := range []string{caSecretName, caKeySecretName, caCRLSecretName} {
		if err := r.adoptSecret(ctx, ca, secretName); err != nil {
			return ctrl.Result{}, fmt.Errorf("adopting Secret %s: %w", secretName, err)
		}
	}

	// CA is ready
	wasReady := ca.Status.Phase == openvoxv1alpha1.CertificateAuthorityPhaseReady
	notAfter := r.extractCANotAfter(ctx, caSecretName, ca.Namespace)
	if notAfter != nil {
		certificateExpiryTimestamp.WithLabelValues(ca.Name, ca.Namespace).Set(float64(notAfter.Unix()))
	}
	serviceName := caInternalServiceName(ca.Name)

	// Only use Init-Job cert as signingSecret if operator-signing cert is not yet active
	var signingSecretName string
	if !meta.IsStatusConditionTrue(ca.Status.Conditions, openvoxv1alpha1.ConditionOperatorSigningReady) {
		if caCert := r.findCAServerCert(ctx, ca, certs); caCert != nil {
			signingSecretName = fmt.Sprintf("%s-tls", caCert.Name)
		}
	}

	if err := updateStatusWithRetry(ctx, r.Client, ca, func() {
		ca.Status.ObservedGeneration = ca.Generation
		ca.Status.Phase = openvoxv1alpha1.CertificateAuthorityPhaseReady
		ca.Status.CASecretName = caSecretName
		ca.Status.ServiceName = serviceName
		ca.Status.NotAfter = notAfter
		if signingSecretName != "" {
			ca.Status.SigningSecretName = signingSecretName
		}
		meta.SetStatusCondition(&ca.Status.Conditions, metav1.Condition{
			Type:               openvoxv1alpha1.ConditionCAReady,
			Status:             metav1.ConditionTrue,
			Reason:             "CAInitialized",
			Message:            "CA is initialized and ready",
			ObservedGeneration: ca.Generation,
		})
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating CertificateAuthority status %s: %w", ca.Name, err)
	}

	if !wasReady {
		r.Recorder.Eventf(ca, nil, corev1.EventTypeNormal, EventReasonCAInitialized, "Reconcile", "CA is initialized and ready")
	}

	// Requeue if NotAfter could not be extracted (informer cache may not have synced yet)
	if ca.Status.NotAfter == nil {
		logger.Info("NotAfter not yet available, requeueing", "secret", caSecretName)
		return ctrl.Result{RequeueAfter: RequeueIntervalShort}, nil
	}

	// Ensure dedicated operator signing certificate
	if signingResult, err := r.reconcileOperatorSigningCert(ctx, ca, certs); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling operator signing cert: %w", err)
	} else if signingResult.RequeueAfter > 0 {
		return signingResult, nil
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

// handleDeletion releases the finalizer once no Certificate references this CA
// any more. Until then it reports the blocking certificates and waits, so a
// stray delete cannot take the PKI down while servers and agents are still
// using it.
func (r *CertificateAuthorityReconciler) handleDeletion(ctx context.Context, ca *openvoxv1alpha1.CertificateAuthority) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	if !controllerutil.ContainsFinalizer(ca, certificateAuthorityFinalizer) {
		return ctrl.Result{}, nil
	}

	certs, err := r.findCertificatesForCA(ctx, ca)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("checking Certificates before deleting CertificateAuthority %s: %w", ca.Name, err)
	}

	// A Certificate that is itself being deleted is not in use any more, so it
	// must not hold the CA back. Counting those too deadlocks namespace
	// deletion: everything is marked for deletion at once, the Certificate
	// finalizers need the CA service to revoke on, and the CA waits for exactly
	// those Certificates to disappear.
	blocking := make([]string, 0, len(certs))
	for i := range certs {
		if certs[i].DeletionTimestamp.IsZero() {
			blocking = append(blocking, certs[i].Name)
		}
	}

	if len(blocking) > 0 {
		sort.Strings(blocking)
		msg := fmt.Sprintf("%d Certificate(s) still reference this CA: %s", len(blocking), strings.Join(blocking, ", "))
		logger.Info("CertificateAuthority deletion blocked", "certificates", blocking)

		if statusErr := updateStatusWithRetry(ctx, r.Client, ca, func() {
			meta.SetStatusCondition(&ca.Status.Conditions, metav1.Condition{
				Type:               openvoxv1alpha1.ConditionCADeletionBlocked,
				Status:             metav1.ConditionTrue,
				Reason:             "CertificatesExist",
				Message:            msg,
				ObservedGeneration: ca.Generation,
			})
		}); statusErr != nil {
			logger.Error(statusErr, "failed to record the deletion-blocked condition", "name", ca.Name)
		}
		r.Recorder.Eventf(ca, nil, corev1.EventTypeWarning, EventReasonCADeletionBlocked, "Delete",
			"Deletion blocked: %s", msg)
		return ctrl.Result{RequeueAfter: RequeueIntervalLong}, nil
	}

	logger.Info("no Certificates left, releasing CertificateAuthority finalizer", "name", ca.Name)
	patch := client.MergeFrom(ca.DeepCopy())
	controllerutil.RemoveFinalizer(ca, certificateAuthorityFinalizer)
	if err := r.Patch(ctx, ca, patch); err != nil {
		return ctrl.Result{}, fmt.Errorf("removing finalizer from CertificateAuthority %s: %w", ca.Name, err)
	}
	return ctrl.Result{}, nil
}

// findConfigForCA returns the Config whose authorityRef points at this CA.
//
// A CertificateAuthority belongs to exactly one Config -- the Config supplies
// the image for the CA setup Job, among other things. The Config webhook
// enforces that, but the webhooks can be disabled, so the ambiguity is resolved
// here as well: the alphabetically first name wins, which at least makes the
// choice stable across reconciles instead of depending on listing order.
//
// Returns (nil, nil) when no Config references the CA yet, which is a normal
// state during bring-up.
func (r *CertificateAuthorityReconciler) findConfigForCA(ctx context.Context, ca *openvoxv1alpha1.CertificateAuthority) (*openvoxv1alpha1.Config, error) {
	cfgList := &openvoxv1alpha1.ConfigList{}
	if err := r.List(ctx, cfgList,
		client.InNamespace(ca.Namespace),
		client.MatchingFields{IndexAuthorityRef: ca.Name}); err != nil {
		return nil, fmt.Errorf("listing Configs for CertificateAuthority %s: %w", ca.Name, err)
	}
	if len(cfgList.Items) == 0 {
		return nil, nil
	}

	sort.Slice(cfgList.Items, func(i, j int) bool {
		return cfgList.Items[i].Name < cfgList.Items[j].Name
	})

	if len(cfgList.Items) > 1 {
		names := make([]string, 0, len(cfgList.Items))
		for i := range cfgList.Items {
			names = append(names, cfgList.Items[i].Name)
		}
		log.FromContext(ctx).Info("several Configs reference this CA, using the first by name",
			"configs", names, "using", names[0])
		r.Recorder.Eventf(ca, nil, corev1.EventTypeWarning, EventReasonMultipleConfigs, "Reconcile",
			"%d Configs reference this CA (%s); using %s", len(names), strings.Join(names, ", "), names[0])
	}

	return &cfgList.Items[0], nil
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
	notAfter := r.extractCANotAfter(ctx, caSecretName, ca.Namespace)
	if notAfter != nil {
		certificateExpiryTimestamp.WithLabelValues(ca.Name, ca.Namespace).Set(float64(notAfter.Unix()))
	}
	extMsg := fmt.Sprintf("External CA configured at %s", ext.URL)

	// Note: ServiceName is intentionally not set for external CAs -
	// no internal ClusterIP Service is created.
	if err := updateStatusWithRetry(ctx, r.Client, ca, func() {
		ca.Status.ObservedGeneration = ca.Generation
		ca.Status.Phase = openvoxv1alpha1.CertificateAuthorityPhaseExternal
		ca.Status.CASecretName = caSecretName
		ca.Status.NotAfter = notAfter
		meta.SetStatusCondition(&ca.Status.Conditions, metav1.Condition{
			Type:               openvoxv1alpha1.ConditionCAReady,
			Status:             metav1.ConditionTrue,
			Reason:             "ExternalCA",
			Message:            extMsg,
			ObservedGeneration: ca.Generation,
		})
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating external CertificateAuthority status %s: %w", ca.Name, err)
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

// adoptSecret sets the controller ownerReference on a Secret so it is garbage-collected
// when the CertificateAuthority is deleted. This replaces the previous approach of setting
// ownerReferences inside the CA setup job script.
func (r *CertificateAuthorityReconciler) adoptSecret(ctx context.Context, ca *openvoxv1alpha1.CertificateAuthority, secretName string) error {
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: ca.Namespace}, secret); err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("getting Secret %s: %w", secretName, err)
	}

	for _, ref := range secret.OwnerReferences {
		if ref.UID == ca.UID {
			return nil
		}
	}

	if err := controllerutil.SetControllerReference(ca, secret, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference on Secret %s: %w", secretName, err)
	}
	return r.Update(ctx, secret)
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
