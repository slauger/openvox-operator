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
	EventReasonCertificateSigned       = "CertificateSigned"
	EventReasonCSRWaitingForSigning    = "CSRWaitingForSigning"
)

// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=certificates,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=certificates/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=certificates/finalizers,verbs=update
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=certificateauthorities,verbs=get;list;watch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=configs,verbs=get;list;watch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=servers,verbs=get;list;watch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=pools,verbs=get;list;watch
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
		cert.Status.Phase = openvoxv1alpha1.CertificatePhasePending
		if err := r.Status().Update(ctx, cert); err != nil {
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

	// Wait for CA to be ready
	if ca.Status.Phase != openvoxv1alpha1.CertificateAuthorityPhaseReady {
		logger.Info("waiting for CertificateAuthority to be ready", "ca", ca.Name, "phase", ca.Status.Phase)
		cert.Status.Phase = openvoxv1alpha1.CertificatePhasePending
		if statusErr := r.Status().Update(ctx, cert); statusErr != nil {
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

		cert.Status.Phase = openvoxv1alpha1.CertificatePhaseSigned
		cert.Status.SecretName = tlsSecretName
		cert.Status.NotAfter = r.extractNotAfter(ctx, tlsSecretName, cert.Namespace)
		meta.SetStatusCondition(&cert.Status.Conditions, metav1.Condition{
			Type:               openvoxv1alpha1.ConditionCertSigned,
			Status:             metav1.ConditionTrue,
			Reason:             "CertificateSigned",
			Message:            "Certificate is signed and available",
			LastTransitionTime: metav1.Now(),
		})
		if err := r.Status().Update(ctx, cert); err != nil {
			return ctrl.Result{}, err
		}
		r.Recorder.Eventf(cert, nil, corev1.EventTypeNormal, EventReasonCertificateSigned, "Reconcile", "Certificate signed and available in Secret %s", tlsSecretName)
		if cert.Status.NotAfter == nil {
			return ctrl.Result{RequeueAfter: RequeueIntervalShort}, nil
		}
		return ctrl.Result{}, nil
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

	caServiceName := findCAServiceName(ctx, r.Client, ca, cert.Namespace)
	if caServiceName == "" {
		logger.Info("waiting for CA server to become available")
		return ctrl.Result{RequeueAfter: RequeueIntervalMedium}, nil
	}

	cert.Status.Phase = openvoxv1alpha1.CertificatePhaseRequesting
	if statusErr := r.Status().Update(ctx, cert); statusErr != nil {
		logger.Error(statusErr, "failed to update Certificate status", "name", cert.Name)
	}

	result, err := r.signCertificate(ctx, cert, ca, caServiceName, cert.Namespace)
	if err != nil {
		logger.Error(err, "certificate signing failed, will retry")
		cert.Status.Phase = openvoxv1alpha1.CertificatePhaseError
		meta.SetStatusCondition(&cert.Status.Conditions, metav1.Condition{
			Type:               openvoxv1alpha1.ConditionCertSigned,
			Status:             metav1.ConditionFalse,
			Reason:             "SigningFailed",
			Message:            err.Error(),
			LastTransitionTime: metav1.Now(),
		})
		if statusErr := r.Status().Update(ctx, cert); statusErr != nil {
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
	cert.Status.Phase = openvoxv1alpha1.CertificatePhaseSigned
	cert.Status.SecretName = tlsSecretName
	cert.Status.NotAfter = r.extractNotAfter(ctx, tlsSecretName, cert.Namespace)
	meta.SetStatusCondition(&cert.Status.Conditions, metav1.Condition{
		Type:               openvoxv1alpha1.ConditionCertSigned,
		Status:             metav1.ConditionTrue,
		Reason:             "CertificateSigned",
		Message:            "Certificate is signed and available",
		LastTransitionTime: metav1.Now(),
	})
	if err := r.Status().Update(ctx, cert); err != nil {
		return ctrl.Result{}, err
	}

	if cert.Status.NotAfter == nil {
		return ctrl.Result{RequeueAfter: RequeueIntervalShort}, nil
	}
	return ctrl.Result{}, nil
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

