package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// reconcileOperatorSigningCert ensures a dedicated operator-signing Certificate CR
// exists for internal CAs. This certificate is used for mTLS authentication when
// signing CSRs via the CA HTTP API; access is granted by CN-based auth.conf rules.
func (r *CertificateAuthorityReconciler) reconcileOperatorSigningCert(ctx context.Context, ca *openvoxv1alpha1.CertificateAuthority, certs []openvoxv1alpha1.Certificate) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Skip for external CAs - they manage their own signing credentials
	if ca.Spec.External != nil {
		return ctrl.Result{}, nil
	}

	certName := fmt.Sprintf("%s-operator-signing", ca.Name)
	certname := fmt.Sprintf("%s-operator", ca.Name)

	// Look for existing operator-signing Certificate in the list
	var signingCert *openvoxv1alpha1.Certificate
	for i := range certs {
		if certs[i].Name == certName {
			signingCert = &certs[i]
			break
		}
	}

	if signingCert == nil {
		// Create the operator-signing Certificate CR
		newCert := &openvoxv1alpha1.Certificate{
			ObjectMeta: metav1.ObjectMeta{
				Name:      certName,
				Namespace: ca.Namespace,
			},
			Spec: openvoxv1alpha1.CertificateSpec{
				AuthorityRef: ca.Name,
				Certname:     certname,
			},
		}
		if err := controllerutil.SetControllerReference(ca, newCert, r.Scheme); err != nil {
			return ctrl.Result{}, fmt.Errorf("setting owner reference on operator-signing cert: %w", err)
		}

		if err := r.Create(ctx, newCert); err != nil {
			if errors.IsAlreadyExists(err) {
				// Already exists, requeue to pick it up next time
				return ctrl.Result{RequeueAfter: RequeueIntervalShort}, nil
			}
			return ctrl.Result{}, fmt.Errorf("creating operator-signing Certificate: %w", err)
		}

		logger.Info("created operator-signing Certificate", "name", certName)
		r.Recorder.Eventf(ca, nil, corev1.EventTypeNormal, EventReasonOperatorSigningCreated, "Reconcile",
			"Created operator-signing Certificate %s", certName)
		return ctrl.Result{RequeueAfter: RequeueIntervalShort}, nil
	}

	// Certificate exists but is not yet signed - let the Certificate controller handle it
	if signingCert.Status.Phase != openvoxv1alpha1.CertificatePhaseSigned {
		logger.Info("operator-signing cert not yet signed, waiting", "name", certName, "phase", signingCert.Status.Phase)
		return ctrl.Result{RequeueAfter: RequeueIntervalShort}, nil
	}

	// Certificate is signed - update signingSecretName and set condition
	signingSecretName := fmt.Sprintf("%s-tls", certName)
	if ca.Status.SigningSecretName == signingSecretName &&
		meta.IsStatusConditionTrue(ca.Status.Conditions, openvoxv1alpha1.ConditionOperatorSigningReady) {
		// Already active, nothing to do
		return ctrl.Result{}, nil
	}

	if err := updateStatusWithRetry(ctx, r.Client, ca, func() {
		ca.Status.SigningSecretName = signingSecretName
		meta.SetStatusCondition(&ca.Status.Conditions, metav1.Condition{
			Type:               openvoxv1alpha1.ConditionOperatorSigningReady,
			Status:             metav1.ConditionTrue,
			Reason:             "OperatorSigningReady",
			Message:            fmt.Sprintf("Operator signing certificate %s is active", certName),
			LastTransitionTime: metav1.Now(),
		})
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating signing secret status: %w", err)
	}

	logger.Info("operator-signing cert active", "signingSecretName", signingSecretName)
	r.Recorder.Eventf(ca, nil, corev1.EventTypeNormal, EventReasonOperatorSigningReady, "Reconcile",
		"Operator signing certificate %s is active", certName)
	return ctrl.Result{}, nil
}
