package controller

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

func TestReconcileOperatorSigningCert_CreatesWhenMissing(t *testing.T) {
	ca := newCertificateAuthority("test-ca")
	c := setupTestClient(ca)
	r := newCertificateAuthorityReconciler(c)

	result, err := r.reconcileOperatorSigningCert(testCtx(), ca, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter == 0 {
		t.Error("expected requeue after creating cert")
	}

	// Verify the Certificate was created
	cert := &openvoxv1alpha1.Certificate{}
	if err := c.Get(context.Background(), types.NamespacedName{
		Name:      "test-ca-operator-signing",
		Namespace: testNamespace,
	}, cert); err != nil {
		t.Fatalf("expected operator-signing cert to be created: %v", err)
	}

	if cert.Spec.AuthorityRef != "test-ca" {
		t.Errorf("expected authorityRef 'test-ca', got %q", cert.Spec.AuthorityRef)
	}
	if cert.Spec.Certname != "test-ca-operator" {
		t.Errorf("expected certname 'test-ca-operator', got %q", cert.Spec.Certname)
	}
	if cert.Spec.CSRExtensions != nil {
		t.Error("expected no csrExtensions on operator-signing cert")
	}
	if len(cert.OwnerReferences) == 0 {
		t.Error("expected ownerReference to be set")
	}
}

func TestReconcileOperatorSigningCert_NoopWhenPending(t *testing.T) {
	ca := newCertificateAuthority("test-ca")
	cert := newCertificate("test-ca-operator-signing", "test-ca", openvoxv1alpha1.CertificatePhaseRequesting)
	c := setupTestClient(ca, cert)
	r := newCertificateAuthorityReconciler(c)

	certs := []openvoxv1alpha1.Certificate{*cert}
	result, err := r.reconcileOperatorSigningCert(testCtx(), ca, certs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter == 0 {
		t.Error("expected requeue when cert is not yet signed")
	}
}

func TestReconcileOperatorSigningCert_UpdatesSigningSecretWhenSigned(t *testing.T) {
	ca := newCertificateAuthority("test-ca")
	ca.Status.SigningSecretName = "old-secret"

	cert := newCertificate("test-ca-operator-signing", "test-ca", openvoxv1alpha1.CertificatePhaseSigned)
	c := setupTestClient(ca, cert)
	r := newCertificateAuthorityReconciler(c)

	certs := []openvoxv1alpha1.Certificate{*cert}
	result, err := r.reconcileOperatorSigningCert(testCtx(), ca, certs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Error("expected no requeue when cert is signed")
	}

	// Verify status was updated
	updatedCA := &openvoxv1alpha1.CertificateAuthority{}
	if err := c.Get(context.Background(), types.NamespacedName{
		Name:      "test-ca",
		Namespace: testNamespace,
	}, updatedCA); err != nil {
		t.Fatalf("unexpected error getting CA: %v", err)
	}

	if updatedCA.Status.SigningSecretName != "test-ca-operator-signing-tls" {
		t.Errorf("expected signingSecretName 'test-ca-operator-signing-tls', got %q", updatedCA.Status.SigningSecretName)
	}
	if !meta.IsStatusConditionTrue(updatedCA.Status.Conditions, openvoxv1alpha1.ConditionOperatorSigningReady) {
		t.Error("expected OperatorSigningReady condition to be True")
	}
}

func TestReconcileOperatorSigningCert_SkippedForExternalCA(t *testing.T) {
	ca := newCertificateAuthority("test-ca", withExternal("https://puppet-ca.example.com:8140"))
	c := setupTestClient(ca)
	r := newCertificateAuthorityReconciler(c)

	result, err := r.reconcileOperatorSigningCert(testCtx(), ca, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Error("expected no requeue for external CA")
	}

	// Verify no Certificate was created
	certList := &openvoxv1alpha1.CertificateList{}
	if err := c.List(context.Background(), certList); err != nil {
		t.Fatalf("unexpected error listing certs: %v", err)
	}
	if len(certList.Items) != 0 {
		t.Errorf("expected no certs created for external CA, got %d", len(certList.Items))
	}
}

func TestReconcileOperatorSigningCert_IdempotentWhenAlreadyActive(t *testing.T) {
	ca := newCertificateAuthority("test-ca")
	ca.Status.SigningSecretName = "test-ca-operator-signing-tls"
	ca.Status.Conditions = []metav1.Condition{
		{
			Type:               openvoxv1alpha1.ConditionOperatorSigningReady,
			Status:             metav1.ConditionTrue,
			Reason:             "OperatorSigningReady",
			LastTransitionTime: metav1.Now(),
		},
	}

	cert := newCertificate("test-ca-operator-signing", "test-ca", openvoxv1alpha1.CertificatePhaseSigned)
	c := setupTestClient(ca, cert)
	r := newCertificateAuthorityReconciler(c)

	certs := []openvoxv1alpha1.Certificate{*cert}
	result, err := r.reconcileOperatorSigningCert(testCtx(), ca, certs)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.RequeueAfter != 0 {
		t.Error("expected no requeue when already active")
	}
}
