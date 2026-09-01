package controller

import (
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// TestCertReconcile_CleanupSkippedWhenCAIsTerminating covers the second half of
// the namespace teardown deadlock.
//
// Deleting a namespace marks everything at once. A certificate that still tries
// to revoke against its CA then talks to a Service whose endpoints are already
// gone -- a ClusterIP with no backends drops the packets rather than refusing
// them, so every attempt burns the full HTTP timeout. With the retry budget
// that is 5 x 30s plus 4 x 15s of requeue, per certificate, which is what made
// the e2e cleanup step time out.
//
// A CA that is being deleted has nothing left to revoke against, so the
// certificate releases its finalizer immediately.
func TestCertReconcile_CleanupSkippedWhenCAIsTerminating(t *testing.T) {
	now := metav1.Now()

	ca := newCertificateAuthority("production-ca")
	ca.DeletionTimestamp = &now
	ca.Finalizers = []string{certificateAuthorityFinalizer}
	ca.Status.SigningSecretName = "production-ca-signing-tls"

	cert := newCertificate("web-cert", "production-ca", openvoxv1alpha1.CertificatePhaseSigned)
	cert.DeletionTimestamp = &now
	cert.Finalizers = []string{certificateFinalizer}

	c := setupTestClient(ca, cert)
	r := newCertificateReconciler(c)

	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := r.Reconcile(testCtx(), testRequest("web-cert")); err != nil {
			t.Errorf("reconcile: %v", err)
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the reconcile is still running; the cleanup must not attempt to reach a CA that is going away")
	}

	got := &openvoxv1alpha1.Certificate{}
	err := c.Get(testCtx(), types.NamespacedName{Name: "web-cert", Namespace: testNamespace}, got)
	switch {
	case apierrors.IsNotFound(err):
	case err != nil:
		t.Fatalf("reading Certificate: %v", err)
	case controllerutil.ContainsFinalizer(got, certificateFinalizer):
		t.Error("the finalizer must be released when the CA is being deleted too")
	}
	if got.Annotations[AnnotationCleanupAttempts] != "" {
		t.Errorf("no cleanup attempt should have been recorded, got %q", got.Annotations[AnnotationCleanupAttempts])
	}
}

// TestCertReconcile_CleanupStillRunsForLiveCA keeps the guarantee: deleting a
// single certificate while its CA is healthy still revokes it.
func TestCertReconcile_CleanupStillRunsForLiveCA(t *testing.T) {
	now := metav1.Now()

	ca := newCertificateAuthority("production-ca")
	ca.Status.SigningSecretName = "production-ca-signing-tls"

	cert := newCertificate("web-cert", "production-ca", openvoxv1alpha1.CertificatePhaseSigned)
	cert.DeletionTimestamp = &now
	cert.Finalizers = []string{certificateFinalizer}

	c := setupTestClient(ca, cert)
	r := newCertificateReconciler(c)

	// Signing against a CA that has no reachable service fails, which is the
	// point: the attempt is made and recorded rather than skipped.
	if _, err := r.Reconcile(testCtx(), testRequest("web-cert")); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	got := &openvoxv1alpha1.Certificate{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "web-cert", Namespace: testNamespace}, got); err != nil {
		t.Fatalf("reading Certificate: %v", err)
	}
	if got.Annotations[AnnotationCleanupAttempts] == "" {
		t.Error("a live CA must still be asked to revoke the certificate")
	}
	if !controllerutil.ContainsFinalizer(got, certificateFinalizer) {
		t.Error("the finalizer must be held while cleanup is still being retried")
	}
}
