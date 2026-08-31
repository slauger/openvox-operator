package controller

import (
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// TestCAReconcile_AddsProtectionFinalizer verifies the guard is in place before
// anything can delete the CA.
func TestCAReconcile_AddsProtectionFinalizer(t *testing.T) {
	ca := newCertificateAuthority("production-ca")
	cfg := newConfig("production", withAuthorityRef("production-ca"))
	c := setupTestClient(ca, cfg)
	r := newCertificateAuthorityReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("production-ca")); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	got := &openvoxv1alpha1.CertificateAuthority{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "production-ca", Namespace: testNamespace}, got); err != nil {
		t.Fatalf("reading CertificateAuthority: %v", err)
	}
	if !controllerutil.ContainsFinalizer(got, certificateAuthorityFinalizer) {
		t.Errorf("expected finalizer %s, got %v", certificateAuthorityFinalizer, got.Finalizers)
	}
}

// TestCAReconcile_DeletionBlockedByCertificates is the core guarantee: as long
// as certificates exist, the CA private key must survive.
func TestCAReconcile_DeletionBlockedByCertificates(t *testing.T) {
	now := metav1.Now()
	ca := newCertificateAuthority("production-ca")
	ca.DeletionTimestamp = &now
	ca.Finalizers = []string{certificateAuthorityFinalizer}
	cert := newCertificate("web-cert", "production-ca", openvoxv1alpha1.CertificatePhaseSigned)

	c := setupTestClient(ca, cert)
	r := newCertificateAuthorityReconciler(c)

	res, err := r.Reconcile(testCtx(), testRequest("production-ca"))
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.RequeueAfter == 0 {
		t.Error("expected the controller to keep checking while deletion is blocked")
	}

	got := &openvoxv1alpha1.CertificateAuthority{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "production-ca", Namespace: testNamespace}, got); err != nil {
		t.Fatalf("the CertificateAuthority must still exist: %v", err)
	}
	if !controllerutil.ContainsFinalizer(got, certificateAuthorityFinalizer) {
		t.Fatal("the finalizer must not be released while certificates reference the CA")
	}

	cond := meta.FindStatusCondition(got.Status.Conditions, openvoxv1alpha1.ConditionCADeletionBlocked)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("expected a %s condition, got %+v", openvoxv1alpha1.ConditionCADeletionBlocked, got.Status.Conditions)
	}
	if cond.Message == "" || cond.Reason != "CertificatesExist" {
		t.Errorf("the condition should name the blocking certificates, got reason %q message %q", cond.Reason, cond.Message)
	}
}

// TestCAReconcile_DeletionProceedsWithoutCertificates covers the other half:
// the finalizer must not turn into a trap.
func TestCAReconcile_DeletionProceedsWithoutCertificates(t *testing.T) {
	now := metav1.Now()
	ca := newCertificateAuthority("production-ca")
	ca.DeletionTimestamp = &now
	ca.Finalizers = []string{certificateAuthorityFinalizer}

	c := setupTestClient(ca)
	r := newCertificateAuthorityReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("production-ca")); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	got := &openvoxv1alpha1.CertificateAuthority{}
	err := c.Get(testCtx(), types.NamespacedName{Name: "production-ca", Namespace: testNamespace}, got)
	switch {
	case apierrors.IsNotFound(err):
		// Finalizer released and the object was collected -- the expected outcome.
	case err != nil:
		t.Fatalf("reading CertificateAuthority: %v", err)
	case controllerutil.ContainsFinalizer(got, certificateAuthorityFinalizer):
		t.Error("the finalizer must be released once no certificate references the CA")
	}
}

// TestCAReconcile_DeletionDoesNotRunSetup guards against the finalizer path
// falling through into the normal reconcile, which would recreate the very
// resources deletion is supposed to remove.
func TestCAReconcile_DeletionDoesNotRunSetup(t *testing.T) {
	now := metav1.Now()
	ca := newCertificateAuthority("production-ca")
	ca.DeletionTimestamp = &now
	ca.Finalizers = []string{certificateAuthorityFinalizer}
	cfg := newConfig("production", withAuthorityRef("production-ca"))
	cert := newCertificate("web-cert", "production-ca", openvoxv1alpha1.CertificatePhaseSigned)

	c := setupTestClient(ca, cfg, cert)
	r := newCertificateAuthorityReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("production-ca")); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	// The setup path creates a Job and a PVC. Neither may appear while the CA
	// is being deleted -- recreating them would fight the garbage collector.
	job := &batchv1.Job{}
	err := c.Get(testCtx(), types.NamespacedName{Name: "production-ca-ca-setup", Namespace: testNamespace}, job)
	if err == nil {
		t.Error("a CA being deleted must not create the setup Job")
	} else if !apierrors.IsNotFound(err) {
		t.Fatalf("checking for the setup Job: %v", err)
	}

	pvcList := &corev1.PersistentVolumeClaimList{}
	if err := c.List(testCtx(), pvcList, client.InNamespace(testNamespace)); err != nil {
		t.Fatalf("listing PVCs: %v", err)
	}
	if len(pvcList.Items) > 0 {
		t.Errorf("a CA being deleted must not create the CA data PVC, found %d", len(pvcList.Items))
	}
}
