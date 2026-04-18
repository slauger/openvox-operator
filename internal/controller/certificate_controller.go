package controller

import (
	"context"
	"fmt"
	"time"

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
	"sigs.k8s.io/controller-runtime/pkg/log"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// CertificateReconciler reconciles a Certificate object.
type CertificateReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder events.EventRecorder
}

// Event reasons for Certificate.
const (
	EventReasonCertificateSigned           = "CertificateSigned"
	EventReasonCSRWaitingForSigning        = "CSRWaitingForSigning"
	EventReasonCertificateRenewalTriggered = "CertificateRenewalTriggered"
	EventReasonCertificateRenewed          = "CertificateRenewed"
	EventReasonCertificateExpiringSoon     = "CertificateExpiringSoon"
)

// Maximum requeue interval for renewal checks (caps the time-based backoff).
const maxRenewalCheckInterval = 12 * time.Hour

// defaultRenewBeforeSeconds is the fallback renewBefore value (60 days).
const defaultRenewBeforeSeconds = 60 * 24 * 3600

// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=certificates,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=certificates/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=certificates/finalizers,verbs=update
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=certificateauthorities,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch;delete

func (r *CertificateReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	cert := &openvoxv1alpha1.Certificate{}
	if err := r.Get(ctx, req.NamespacedName, cert); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Set initial phase
	if cert.Status.Phase == "" {
		if err := updateStatusWithRetry(ctx, r.Client, cert, func() {
			cert.Status.Phase = openvoxv1alpha1.CertificatePhasePending
		}); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Resolve CertificateAuthority
	ca := &openvoxv1alpha1.CertificateAuthority{}
	if err := r.Get(ctx, types.NamespacedName{Name: cert.Spec.AuthorityRef, Namespace: cert.Namespace}, ca); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("waiting for CertificateAuthority", "authorityRef", cert.Spec.AuthorityRef)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Wait for CA to be ready (accept both Ready and External phases)
	if ca.Status.Phase != openvoxv1alpha1.CertificateAuthorityPhaseReady && ca.Status.Phase != openvoxv1alpha1.CertificateAuthorityPhaseExternal {
		logger.Info("waiting for CertificateAuthority to be ready", "ca", ca.Name, "phase", ca.Status.Phase)
		if statusErr := updateStatusWithRetry(ctx, r.Client, cert, func() {
			cert.Status.Phase = openvoxv1alpha1.CertificatePhasePending
		}); statusErr != nil {
			logger.Error(statusErr, "failed to update Certificate status", "name", cert.Name)
		}
		return ctrl.Result{RequeueAfter: RequeueIntervalMedium}, nil
	}

	tlsSecretName := fmt.Sprintf("%s-tls", cert.Name)

	// Check if TLS Secret already exists (may have been created by CA setup job)
	if isSecretReady(ctx, r.Client, tlsSecretName, cert.Namespace, "cert.pem") {
		if err := r.adoptTLSSecret(ctx, cert, tlsSecretName); err != nil {
			return ctrl.Result{}, fmt.Errorf("adopting TLS Secret: %w", err)
		}

		notAfter := r.extractNotAfter(ctx, tlsSecretName, cert.Namespace)
		if err := updateStatusWithRetry(ctx, r.Client, cert, func() {
			cert.Status.Phase = openvoxv1alpha1.CertificatePhaseSigned
			cert.Status.SecretName = tlsSecretName
			cert.Status.NotAfter = notAfter
			meta.SetStatusCondition(&cert.Status.Conditions, metav1.Condition{
				Type:               openvoxv1alpha1.ConditionCertSigned,
				Status:             metav1.ConditionTrue,
				Reason:             "CertificateSigned",
				Message:            "Certificate is signed and available",
				LastTransitionTime: metav1.Now(),
			})
		}); err != nil {
			return ctrl.Result{}, err
		}
		r.Recorder.Eventf(cert, nil, corev1.EventTypeNormal, EventReasonCertificateSigned, "Reconcile", "Certificate signed and available in Secret %s", tlsSecretName)
		if cert.Status.NotAfter == nil {
			return ctrl.Result{RequeueAfter: RequeueIntervalShort}, nil
		}
		return r.scheduleRenewalCheck(ctx, cert)
	}

	// Handle renewal phase
	if cert.Status.Phase == openvoxv1alpha1.CertificatePhaseRenewing {
		return r.reconcileCertRenewal(ctx, cert, ca)
	}

	// Sign certificate via CA HTTP API
	return r.reconcileCertSigning(ctx, cert, ca)
}

func (r *CertificateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&openvoxv1alpha1.Certificate{}).
		Owns(&corev1.Secret{}).
		Watches(&corev1.Secret{}, enqueueCertificatesForSecret(mgr.GetClient())).
		Complete(r)
}

// reconcileCertSigning signs a certificate directly via the Puppet CA HTTP API.
// This is non-blocking: it submits the CSR, checks once for the signed cert,
// and returns RequeueAfter if the cert isn't signed yet.
func (r *CertificateReconciler) reconcileCertSigning(ctx context.Context, cert *openvoxv1alpha1.Certificate, ca *openvoxv1alpha1.CertificateAuthority) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Resolve CA base URL: external URL or internal CA Service
	var caBaseURL string
	if ca.Spec.External != nil {
		caBaseURL = ca.Spec.External.URL
	} else {
		caBaseURL = fmt.Sprintf("https://%s.%s.svc:8140", caInternalServiceName(ca.Name), cert.Namespace)
	}

	if statusErr := updateStatusWithRetry(ctx, r.Client, cert, func() {
		cert.Status.Phase = openvoxv1alpha1.CertificatePhaseRequesting
	}); statusErr != nil {
		logger.Error(statusErr, "failed to update Certificate status", "name", cert.Name)
	}

	result, err := r.signCertificate(ctx, cert, ca, caBaseURL, cert.Namespace)
	if err != nil {
		logger.Error(err, "certificate signing failed, will retry")
		errMsg := err.Error()
		if statusErr := updateStatusWithRetry(ctx, r.Client, cert, func() {
			cert.Status.Phase = openvoxv1alpha1.CertificatePhaseError
			meta.SetStatusCondition(&cert.Status.Conditions, metav1.Condition{
				Type:               openvoxv1alpha1.ConditionCertSigned,
				Status:             metav1.ConditionFalse,
				Reason:             "SigningFailed",
				Message:            errMsg,
				LastTransitionTime: metav1.Now(),
			})
		}); statusErr != nil {
			logger.Error(statusErr, "failed to update Certificate status", "name", cert.Name)
		}
		if result.RequeueAfter > 0 {
			return result, nil
		}
		return ctrl.Result{RequeueAfter: RequeueIntervalLong}, nil
	}

	if result.RequeueAfter > 0 {
		// Still waiting for cert to be signed
		return result, nil
	}

	// Mark as signed
	tlsSecretName := fmt.Sprintf("%s-tls", cert.Name)
	notAfter := r.extractNotAfter(ctx, tlsSecretName, cert.Namespace)
	if err := updateStatusWithRetry(ctx, r.Client, cert, func() {
		cert.Status.Phase = openvoxv1alpha1.CertificatePhaseSigned
		cert.Status.SecretName = tlsSecretName
		cert.Status.NotAfter = notAfter
		meta.SetStatusCondition(&cert.Status.Conditions, metav1.Condition{
			Type:               openvoxv1alpha1.ConditionCertSigned,
			Status:             metav1.ConditionTrue,
			Reason:             "CertificateSigned",
			Message:            "Certificate is signed and available",
			LastTransitionTime: metav1.Now(),
		})
	}); err != nil {
		return ctrl.Result{}, err
	}

	if cert.Status.NotAfter == nil {
		return ctrl.Result{RequeueAfter: RequeueIntervalShort}, nil
	}
	return r.scheduleRenewalCheck(ctx, cert)
}

// createOrUpdateTLSSecret creates or updates a Secret containing cert.pem and key.pem.
func (r *CertificateReconciler) createOrUpdateTLSSecret(ctx context.Context, cert *openvoxv1alpha1.Certificate, ca *openvoxv1alpha1.CertificateAuthority, name string, certPEM, keyPEM []byte) error {
	labels := caLabels(ca.Name)
	labels["openvox.voxpupuli.org/certificate"] = cert.Name

	return createOrUpdateSecret(ctx, r.Client, r.Scheme, cert, name, cert.Namespace, labels, map[string][]byte{
		"cert.pem": certPEM,
		"key.pem":  keyPEM,
	})
}

// adoptTLSSecret sets the ownerReference on the TLS Secret to this Certificate.
func (r *CertificateReconciler) adoptTLSSecret(ctx context.Context, cert *openvoxv1alpha1.Certificate, secretName string) error {
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: cert.Namespace}, secret); err != nil {
		return err
	}

	for _, ref := range secret.OwnerReferences {
		if ref.UID == cert.UID {
			return nil
		}
	}

	if err := controllerutil.SetControllerReference(cert, secret, r.Scheme); err != nil {
		return err
	}
	return r.Update(ctx, secret)
}

// extractNotAfter reads the cert.pem from a TLS Secret and returns its NotAfter time.
func (r *CertificateReconciler) extractNotAfter(ctx context.Context, secretName, namespace string) *metav1.Time {
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: namespace}, secret); err != nil {
		if !errors.IsNotFound(err) {
			log.FromContext(ctx).Error(err, "failed to get TLS Secret", "name", secretName, "namespace", namespace)
		}
		return nil
	}
	return parseCertNotAfter(ctx, secret.Data["cert.pem"])
}

// parseCertRenewBefore parses the RenewBefore spec field and returns the duration.
// Falls back to 60 days if the field is empty or unparseable.
func parseCertRenewBefore(cert *openvoxv1alpha1.Certificate) time.Duration {
	if cert.Spec.RenewBefore == "" {
		return time.Duration(defaultRenewBeforeSeconds) * time.Second
	}
	secs, err := openvoxv1alpha1.ParseDurationToSeconds(cert.Spec.RenewBefore)
	if err != nil || secs <= 0 {
		return time.Duration(defaultRenewBeforeSeconds) * time.Second
	}
	return time.Duration(secs) * time.Second
}

// scheduleRenewalCheck computes when to next check for renewal and emits warning
// events at 30d/7d/1d thresholds. If the certificate is within the renewal window,
// it transitions to the Renewing phase and requeues immediately.
func (r *CertificateReconciler) scheduleRenewalCheck(ctx context.Context, cert *openvoxv1alpha1.Certificate) (ctrl.Result, error) {
	if cert.Status.NotAfter == nil {
		return ctrl.Result{RequeueAfter: RequeueIntervalShort}, nil
	}

	now := time.Now()
	notAfter := cert.Status.NotAfter.Time
	timeUntilExpiry := notAfter.Sub(now)

	renewBefore := parseCertRenewBefore(cert)
	renewalTime := notAfter.Add(-renewBefore)
	timeUntilRenewal := renewalTime.Sub(now)

	// Emit warning events at thresholds
	r.emitExpiryWarnings(cert, timeUntilExpiry)

	// If within the renewal window, trigger renewal
	if timeUntilRenewal <= 0 {
		logger := log.FromContext(ctx)
		logger.Info("certificate within renewal window, triggering renewal",
			"certname", cert.Spec.Certname,
			"notAfter", notAfter,
			"renewBefore", renewBefore)

		if err := updateStatusWithRetry(ctx, r.Client, cert, func() {
			cert.Status.Phase = openvoxv1alpha1.CertificatePhaseRenewing
		}); err != nil {
			return ctrl.Result{}, err
		}
		r.Recorder.Eventf(cert, nil, corev1.EventTypeNormal, EventReasonCertificateRenewalTriggered, "Reconcile",
			"Certificate renewal triggered, expires %s", notAfter.Format(time.RFC3339))
		return ctrl.Result{Requeue: true}, nil
	}

	// Schedule next check: half the time until renewal, capped at 12h
	requeueAfter := timeUntilRenewal / 2
	if requeueAfter > maxRenewalCheckInterval {
		requeueAfter = maxRenewalCheckInterval
	}

	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// emitExpiryWarnings emits warning events when the certificate is approaching expiry.
func (r *CertificateReconciler) emitExpiryWarnings(cert *openvoxv1alpha1.Certificate, timeUntilExpiry time.Duration) {
	switch {
	case timeUntilExpiry <= 24*time.Hour:
		r.Recorder.Eventf(cert, nil, corev1.EventTypeWarning, EventReasonCertificateExpiringSoon, "Reconcile",
			"Certificate expires in less than 1 day")
	case timeUntilExpiry <= 7*24*time.Hour:
		r.Recorder.Eventf(cert, nil, corev1.EventTypeWarning, EventReasonCertificateExpiringSoon, "Reconcile",
			"Certificate expires in less than 7 days")
	case timeUntilExpiry <= 30*24*time.Hour:
		r.Recorder.Eventf(cert, nil, corev1.EventTypeWarning, EventReasonCertificateExpiringSoon, "Reconcile",
			"Certificate expires in less than 30 days")
	}
}
