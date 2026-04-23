package controller

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	"k8s.io/utils/clock"
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
	Clock    clock.PassiveClock
}

// Finalizer for Certificate cleanup on the Puppet CA.
const certificateFinalizer = "openvox.voxpupuli.org/certificate-cleanup"

// Event reasons for Certificate.
const (
	EventReasonCertificateSigned           = "CertificateSigned"
	EventReasonCSRWaitingForSigning        = "CSRWaitingForSigning"
	EventReasonCertificateRenewalTriggered = "CertificateRenewalTriggered"
	EventReasonCertificateRenewed          = "CertificateRenewed"
	EventReasonCertificateExpiringSoon     = "CertificateExpiringSoon"
	EventReasonCertificateCleaned          = "CertificateCleaned"
)

// Maximum requeue interval for renewal checks (caps the time-based backoff).
const maxRenewalCheckInterval = 12 * time.Hour

// defaultRenewBeforeSeconds is the fallback renewBefore value (60 days).
const defaultRenewBeforeSeconds = 60 * 24 * 3600

// minRenewalCooldown prevents renewal loops when renewBefore exceeds the cert lifetime.
const minRenewalCooldown = 1 * time.Hour

// maxCleanupAttempts is the number of times the controller retries CA cleanup
// before removing the finalizer anyway (to avoid blocking deletion indefinitely).
const maxCleanupAttempts = 5

// Annotation keys for renewal tracking.
const (
	// AnnotationLastRenewalTime records when the cert was last renewed (RFC3339).
	AnnotationLastRenewalTime = "openvox.voxpupuli.org/last-renewal-time"

	// AnnotationExpiryWarned records which expiry warning thresholds have been emitted.
	AnnotationExpiryWarned = "openvox.voxpupuli.org/expiry-warned"

	// AnnotationCleanupAttempts tracks how many times cleanup was attempted during deletion.
	AnnotationCleanupAttempts = "openvox.voxpupuli.org/cleanup-attempts"
)

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

	// Handle deletion: clean the certificate on the Puppet CA before removing the finalizer.
	if !cert.DeletionTimestamp.IsZero() {
		if controllerutil.ContainsFinalizer(cert, certificateFinalizer) {
			if err := r.handleCertificateCleanup(ctx, cert); err != nil {
				attempts := 0
				if cert.Annotations != nil {
					if v, ok := cert.Annotations[AnnotationCleanupAttempts]; ok {
						attempts, _ = strconv.Atoi(v)
					}
				}
				attempts++
				if attempts >= maxCleanupAttempts {
					logger.Info("cleanup failed after max attempts, removing finalizer anyway",
						"attempts", attempts, "error", err)
				} else {
					logger.Error(err, "certificate cleanup failed, will retry",
						"attempt", attempts, "maxAttempts", maxCleanupAttempts)
					patch := client.MergeFrom(cert.DeepCopy())
					if cert.Annotations == nil {
						cert.Annotations = make(map[string]string)
					}
					cert.Annotations[AnnotationCleanupAttempts] = strconv.Itoa(attempts)
					if patchErr := r.Patch(ctx, cert, patch); patchErr != nil {
						logger.Error(patchErr, "failed to update cleanup attempt annotation")
					}
					return ctrl.Result{RequeueAfter: RequeueIntervalLong}, nil
				}
			}
			patch := client.MergeFrom(cert.DeepCopy())
			controllerutil.RemoveFinalizer(cert, certificateFinalizer)
			if err := r.Patch(ctx, cert, patch); err != nil {
				return ctrl.Result{}, err
			}
		}
		return ctrl.Result{}, nil
	}

	// Ensure finalizer is set
	if !controllerutil.ContainsFinalizer(cert, certificateFinalizer) {
		patch := client.MergeFrom(cert.DeepCopy())
		controllerutil.AddFinalizer(cert, certificateFinalizer)
		if err := r.Patch(ctx, cert, patch); err != nil {
			return ctrl.Result{}, err
		}
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

	// Handle renewal phase before anything else — a Renewing cert must not
	// be overridden by the adopt-existing-secret block below.
	if cert.Status.Phase == openvoxv1alpha1.CertificatePhaseRenewing {
		return r.reconcileCertRenewal(ctx, cert, ca)
	}

	// If the certificate is already signed, just schedule renewal checks.
	// This avoids re-adopting the TLS Secret on every reconcile, which would
	// reset the phase and prevent entering the Renewing state.
	if cert.Status.Phase == openvoxv1alpha1.CertificatePhaseSigned {
		if cert.Status.NotAfter == nil {
			return ctrl.Result{RequeueAfter: RequeueIntervalShort}, nil
		}
		return r.scheduleRenewalCheck(ctx, cert)
	}

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

	// Sign certificate via CA HTTP API
	return r.reconcileCertSigning(ctx, cert, ca)
}

func (r *CertificateReconciler) SetupWithManager(mgr ctrl.Manager) error {
	if r.Clock == nil {
		r.Clock = clock.RealClock{}
	}
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

	now := r.Clock.Now()
	notAfter := cert.Status.NotAfter.Time
	timeUntilExpiry := notAfter.Sub(now)

	renewBefore := parseCertRenewBefore(cert)
	renewalTime := notAfter.Add(-renewBefore)
	timeUntilRenewal := renewalTime.Sub(now)

	// If within the renewal window, trigger renewal (with cooldown protection)
	if timeUntilRenewal <= 0 {
		// Prevent renewal loops: if the cert was renewed recently, skip
		if r.isWithinRenewalCooldown(cert) {
			logger := log.FromContext(ctx)
			logger.Info("certificate within renewal window but recently renewed, skipping",
				"certname", cert.Spec.Certname,
				"renewBefore", renewBefore,
				"timeUntilExpiry", timeUntilExpiry)
			r.Recorder.Eventf(cert, nil, corev1.EventTypeWarning, EventReasonCertificateExpiringSoon, "Reconcile",
				"Certificate renewBefore (%s) exceeds remaining lifetime; renewal skipped to avoid loop", renewBefore)
			return ctrl.Result{RequeueAfter: minRenewalCooldown}, nil
		}

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
		return ctrl.Result{RequeueAfter: 1 * time.Second}, nil
	}

	// Emit warning events at thresholds (only outside the renewal window)
	r.emitExpiryWarnings(ctx, cert, timeUntilExpiry)

	// Schedule next check: half the time until renewal, capped at 12h
	requeueAfter := timeUntilRenewal / 2
	if requeueAfter > maxRenewalCheckInterval {
		requeueAfter = maxRenewalCheckInterval
	}

	return ctrl.Result{RequeueAfter: requeueAfter}, nil
}

// isWithinRenewalCooldown checks if the certificate was renewed recently
// (within minRenewalCooldown). This prevents infinite renewal loops when
// renewBefore exceeds the certificate's total lifetime.
func (r *CertificateReconciler) isWithinRenewalCooldown(cert *openvoxv1alpha1.Certificate) bool {
	if cert.Annotations == nil {
		return false
	}
	lastRenewal, ok := cert.Annotations[AnnotationLastRenewalTime]
	if !ok {
		return false
	}
	t, err := time.Parse(time.RFC3339, lastRenewal)
	if err != nil {
		return false
	}
	return r.Clock.Since(t) < minRenewalCooldown
}

// expiryThresholds defines the warning thresholds for certificate expiry.
var expiryThresholds = []struct {
	dur   time.Duration
	label string
}{
	{30 * 24 * time.Hour, "30d"},
	{7 * 24 * time.Hour, "7d"},
	{24 * time.Hour, "1d"},
}

// emitExpiryWarnings emits warning events when the certificate is approaching expiry.
// All crossed thresholds are emitted at once, tracked via an annotation on the Certificate.
func (r *CertificateReconciler) emitExpiryWarnings(ctx context.Context, cert *openvoxv1alpha1.Certificate, timeUntilExpiry time.Duration) {
	warned := ""
	if cert.Annotations != nil {
		warned = cert.Annotations[AnnotationExpiryWarned]
	}

	var newWarnings []string
	for _, th := range expiryThresholds {
		if timeUntilExpiry <= th.dur && !strings.Contains(warned, th.label) {
			r.Recorder.Eventf(cert, nil, corev1.EventTypeWarning, EventReasonCertificateExpiringSoon, "Reconcile",
				"Certificate expires in less than %s", th.label)
			newWarnings = append(newWarnings, th.label)
		}
	}

	if len(newWarnings) == 0 {
		return
	}

	// Track the thresholds in an annotation
	for _, w := range newWarnings {
		if warned != "" {
			warned += "," + w
		} else {
			warned = w
		}
	}
	patch := client.MergeFrom(cert.DeepCopy())
	if cert.Annotations == nil {
		cert.Annotations = make(map[string]string)
	}
	cert.Annotations[AnnotationExpiryWarned] = warned
	if err := r.Patch(ctx, cert, patch); err != nil {
		log.FromContext(ctx).Error(err, "failed to update expiry-warned annotation")
	}
}

// handleCertificateCleanup calls the Puppet CA clean API to revoke and remove the certificate.
func (r *CertificateReconciler) handleCertificateCleanup(ctx context.Context, cert *openvoxv1alpha1.Certificate) error {
	logger := log.FromContext(ctx)

	// Resolve CertificateAuthority
	ca := &openvoxv1alpha1.CertificateAuthority{}
	if err := r.Get(ctx, types.NamespacedName{Name: cert.Spec.AuthorityRef, Namespace: cert.Namespace}, ca); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("CertificateAuthority not found, skipping cleanup", "authorityRef", cert.Spec.AuthorityRef)
			return nil
		}
		return err
	}

	// Skip cleanup for external CAs without a signing secret (no admin access)
	if ca.Spec.External != nil || ca.Status.SigningSecretName == "" {
		logger.Info("skipping CA cleanup (external CA or no signing secret)", "ca", ca.Name)
		return nil
	}

	// Resolve CA base URL
	caBaseURL := fmt.Sprintf("https://%s.%s.svc:8140", caInternalServiceName(ca.Name), cert.Namespace)

	if err := r.cleanCertViaAPI(ctx, cert, ca, caBaseURL, cert.Namespace); err != nil {
		return err
	}

	r.Recorder.Eventf(cert, nil, corev1.EventTypeNormal, EventReasonCertificateCleaned, "Reconcile",
		"Certificate %s cleaned from Puppet CA", cert.Spec.Certname)
	return nil
}
