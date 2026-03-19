package controller

import (
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/types"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

func TestCertReconcile_NotFound(t *testing.T) {
	c := setupTestClient()
	r := newCertificateReconciler(c)

	res, err := r.Reconcile(testCtx(), testRequest("nonexistent"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.RequeueAfter != 0 {
		t.Error("expected no requeue for missing Certificate")
	}
}

func TestCertReconcile_CANotFound(t *testing.T) {
	cert := newCertificate("my-cert", "missing-ca", "")
	c := setupTestClient(cert)
	r := newCertificateReconciler(c)

	res, err := r.Reconcile(testCtx(), testRequest("my-cert"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// When CA is not found, controller returns empty result (no requeue)
	if res.RequeueAfter != 0 {
		t.Errorf("expected no requeue when CA not found, got %v", res.RequeueAfter)
	}
}

func TestCertReconcile_CANotReady(t *testing.T) {
	cert := newCertificate("my-cert", "test-ca", "")
	ca := newCertificateAuthority("test-ca")
	ca.Status.Phase = openvoxv1alpha1.CertificateAuthorityPhasePending // not ready

	c := setupTestClient(cert, ca)
	r := newCertificateReconciler(c)

	res, err := r.Reconcile(testCtx(), testRequest("my-cert"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.RequeueAfter != 10*time.Second {
		t.Errorf("expected requeue after 10s, got %v", res.RequeueAfter)
	}

	updated := &openvoxv1alpha1.Certificate{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "my-cert", Namespace: testNamespace}, updated); err != nil {
		t.Fatalf("failed to get Certificate: %v", err)
	}
	if updated.Status.Phase != openvoxv1alpha1.CertificatePhasePending {
		t.Errorf("expected phase %q, got %q", openvoxv1alpha1.CertificatePhasePending, updated.Status.Phase)
	}
}

func TestCertReconcile_TLSSecretExists(t *testing.T) {
	cert := newCertificate("my-cert", "test-ca", "")
	ca := newCertificateAuthority("test-ca")
	tlsSecret := newSecret("my-cert-tls", map[string][]byte{
		"cert.pem": []byte("signed-cert"),
		"key.pem":  []byte("private-key"),
	})

	c := setupTestClient(cert, ca, tlsSecret)
	r := newCertificateReconciler(c)

	res, err := r.Reconcile(testCtx(), testRequest("my-cert"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updated := &openvoxv1alpha1.Certificate{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "my-cert", Namespace: testNamespace}, updated); err != nil {
		t.Fatalf("failed to get Certificate: %v", err)
	}
	if updated.Status.Phase != openvoxv1alpha1.CertificatePhaseSigned {
		t.Errorf("expected phase %q, got %q", openvoxv1alpha1.CertificatePhaseSigned, updated.Status.Phase)
	}
	if updated.Status.SecretName != "my-cert-tls" {
		t.Errorf("expected SecretName %q, got %q", "my-cert-tls", updated.Status.SecretName)
	}

	// Verify CertSigned condition
	found := false
	for _, cond := range updated.Status.Conditions {
		if cond.Type == openvoxv1alpha1.ConditionCertSigned {
			found = true
			if cond.Status != "True" {
				t.Errorf("expected condition status True, got %q", cond.Status)
			}
		}
	}
	if !found {
		t.Error("CertSigned condition not set")
	}

	// Non-parseable cert data means NotAfter is nil, so should requeue
	if res.RequeueAfter != 5*time.Second {
		t.Errorf("expected requeue after 5s when NotAfter is nil, got %v", res.RequeueAfter)
	}
}

func TestCertReconcile_PhasePending(t *testing.T) {
	cert := newCertificate("my-cert", "test-ca", "")
	ca := newCertificateAuthority("test-ca")
	ca.Status.Phase = openvoxv1alpha1.CertificateAuthorityPhasePending

	c := setupTestClient(cert, ca)
	r := newCertificateReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("my-cert")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	updated := &openvoxv1alpha1.Certificate{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "my-cert", Namespace: testNamespace}, updated); err != nil {
		t.Fatalf("failed to get Certificate: %v", err)
	}
	if updated.Status.Phase != openvoxv1alpha1.CertificatePhasePending {
		t.Errorf("expected phase %q, got %q", openvoxv1alpha1.CertificatePhasePending, updated.Status.Phase)
	}
}

func TestCertReconcile_CAExternalPhase_Accepted(t *testing.T) {
	cert := newCertificate("my-cert", "ext-ca", "")
	ca := newCertificateAuthority("ext-ca", withExternal("https://puppet-ca.example.com:8140"))
	ca.Status.Phase = openvoxv1alpha1.CertificateAuthorityPhaseExternal
	// Pre-create the TLS secret so reconcile completes without HTTP calls
	tlsSecret := newSecret("my-cert-tls", map[string][]byte{
		"cert.pem": []byte("signed-cert"),
		"key.pem":  []byte("private-key"),
	})

	c := setupTestClient(cert, ca, tlsSecret)
	r := newCertificateReconciler(c)

	res, err := r.Reconcile(testCtx(), testRequest("my-cert"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should NOT requeue with 10s (that would mean CA was treated as not-ready)
	if res.RequeueAfter == 10*time.Second {
		t.Error("certificate should not wait for CA when phase is External")
	}

	updated := &openvoxv1alpha1.Certificate{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "my-cert", Namespace: testNamespace}, updated); err != nil {
		t.Fatalf("failed to get Certificate: %v", err)
	}
	if updated.Status.Phase != openvoxv1alpha1.CertificatePhaseSigned {
		t.Errorf("expected phase %q, got %q", openvoxv1alpha1.CertificatePhaseSigned, updated.Status.Phase)
	}
}
