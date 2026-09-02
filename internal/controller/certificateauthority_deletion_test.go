package controller

import (
	"strings"
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

// TestCAReconcile_DeletionNotBlockedByTerminatingCertificates is the case that
// broke namespace teardown.
//
// Deleting a namespace marks everything at once. The Certificate finalizers
// then need the CA service to revoke against, while the CA waits for exactly
// those Certificates to disappear -- so the CA has to treat a Certificate that
// is already being deleted as no longer in use.
func TestCAReconcile_DeletionNotBlockedByTerminatingCertificates(t *testing.T) {
	now := metav1.Now()
	ca := newCertificateAuthority("production-ca")
	ca.DeletionTimestamp = &now
	ca.Finalizers = []string{certificateAuthorityFinalizer}

	terminating := newCertificate("web-cert", "production-ca", openvoxv1alpha1.CertificatePhaseSigned)
	terminating.DeletionTimestamp = &now
	terminating.Finalizers = []string{certificateFinalizer}

	c := setupTestClient(ca, terminating)
	r := newCertificateAuthorityReconciler(c)

	res, err := r.Reconcile(testCtx(), testRequest("production-ca"))
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.RequeueAfter != 0 {
		t.Errorf("a CA whose certificates are all terminating should not keep waiting, got %v", res.RequeueAfter)
	}

	got := &openvoxv1alpha1.CertificateAuthority{}
	getErr := c.Get(testCtx(), types.NamespacedName{Name: "production-ca", Namespace: testNamespace}, got)
	switch {
	case apierrors.IsNotFound(getErr):
	case getErr != nil:
		t.Fatalf("reading CertificateAuthority: %v", getErr)
	case controllerutil.ContainsFinalizer(got, certificateAuthorityFinalizer):
		t.Error("the finalizer must be released when every referencing certificate is already terminating")
	}
}

// TestCAReconcile_DeletionBlockedByLiveCertificateAmongTerminating keeps the
// guarantee intact: one certificate nobody asked to delete is enough to hold
// the CA.
func TestCAReconcile_DeletionBlockedByLiveCertificateAmongTerminating(t *testing.T) {
	now := metav1.Now()
	ca := newCertificateAuthority("production-ca")
	ca.DeletionTimestamp = &now
	ca.Finalizers = []string{certificateAuthorityFinalizer}

	terminating := newCertificate("old-cert", "production-ca", openvoxv1alpha1.CertificatePhaseSigned)
	terminating.DeletionTimestamp = &now
	terminating.Finalizers = []string{certificateFinalizer}
	live := newCertificate("web-cert", "production-ca", openvoxv1alpha1.CertificatePhaseSigned)

	c := setupTestClient(ca, terminating, live)
	r := newCertificateAuthorityReconciler(c)

	res, err := r.Reconcile(testCtx(), testRequest("production-ca"))
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.RequeueAfter == 0 {
		t.Error("expected the controller to keep waiting while a live certificate references the CA")
	}

	got := &openvoxv1alpha1.CertificateAuthority{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "production-ca", Namespace: testNamespace}, got); err != nil {
		t.Fatalf("the CertificateAuthority must still exist: %v", err)
	}
	if !controllerutil.ContainsFinalizer(got, certificateAuthorityFinalizer) {
		t.Fatal("the finalizer must not be released while a live certificate exists")
	}

	cond := meta.FindStatusCondition(got.Status.Conditions, openvoxv1alpha1.ConditionCADeletionBlocked)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("expected a DeletionBlocked condition, got %+v", got.Status.Conditions)
	}
	if strings.Contains(cond.Message, "old-cert") {
		t.Errorf("a terminating certificate must not be listed as blocking: %q", cond.Message)
	}
	if !strings.Contains(cond.Message, "web-cert") {
		t.Errorf("the live certificate should be named as blocking: %q", cond.Message)
	}
}

// TestCAReconcile_DeletionNotBlockedByOwnedCertificate is the deadlock that
// made `helm uninstall --wait` hang.
//
// The operator-signing Certificate is created by the CA controller and carries
// a controller reference to the CA, so garbage collection removes it together
// with its owner. Counting it as blocking means the CA waits for a Certificate
// that can only disappear once the CA itself is gone -- and unlike during
// namespace deletion, nothing else marks it for deletion to break the cycle.
func TestCAReconcile_DeletionNotBlockedByOwnedCertificate(t *testing.T) {
	now := metav1.Now()
	ca := newCertificateAuthority("production-ca")
	ca.UID = "ca-uid"
	ca.DeletionTimestamp = &now
	ca.Finalizers = []string{certificateAuthorityFinalizer}

	owned := newCertificate("production-ca-operator-signing", "production-ca", openvoxv1alpha1.CertificatePhaseSigned)
	ownedBy(ca, owned)

	c := setupTestClient(ca, owned)
	r := newCertificateAuthorityReconciler(c)

	res, err := r.Reconcile(testCtx(), testRequest("production-ca"))
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.RequeueAfter != 0 {
		t.Errorf("a CA must not wait for a Certificate it owns, got %v", res.RequeueAfter)
	}

	got := &openvoxv1alpha1.CertificateAuthority{}
	getErr := c.Get(testCtx(), types.NamespacedName{Name: "production-ca", Namespace: testNamespace}, got)
	switch {
	case apierrors.IsNotFound(getErr):
	case getErr != nil:
		t.Fatalf("reading CertificateAuthority: %v", getErr)
	case controllerutil.ContainsFinalizer(got, certificateAuthorityFinalizer):
		t.Error("the finalizer must be released when the only certificate is owned by the CA")
	}
}

// TestCAReconcile_DeletionStillBlockedByIndependentCertificate keeps the
// protection: a Certificate the user created stands on its own and does block.
func TestCAReconcile_DeletionStillBlockedByIndependentCertificate(t *testing.T) {
	now := metav1.Now()
	ca := newCertificateAuthority("production-ca")
	ca.UID = "ca-uid"
	ca.DeletionTimestamp = &now
	ca.Finalizers = []string{certificateAuthorityFinalizer}

	owned := newCertificate("production-ca-operator-signing", "production-ca", openvoxv1alpha1.CertificatePhaseSigned)
	ownedBy(ca, owned)
	independent := newCertificate("web-cert", "production-ca", openvoxv1alpha1.CertificatePhaseSigned)

	c := setupTestClient(ca, owned, independent)
	r := newCertificateAuthorityReconciler(c)

	res, err := r.Reconcile(testCtx(), testRequest("production-ca"))
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if res.RequeueAfter == 0 {
		t.Error("expected the controller to keep waiting for the independent certificate")
	}

	got := &openvoxv1alpha1.CertificateAuthority{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "production-ca", Namespace: testNamespace}, got); err != nil {
		t.Fatalf("the CertificateAuthority must still exist: %v", err)
	}
	if !controllerutil.ContainsFinalizer(got, certificateAuthorityFinalizer) {
		t.Fatal("the finalizer must not be released while an independent certificate exists")
	}

	cond := meta.FindStatusCondition(got.Status.Conditions, openvoxv1alpha1.ConditionCADeletionBlocked)
	if cond == nil {
		t.Fatal("expected a DeletionBlocked condition")
	}
	if strings.Contains(cond.Message, "operator-signing") {
		t.Errorf("a certificate owned by the CA must not be listed as blocking: %q", cond.Message)
	}
	if !strings.Contains(cond.Message, "web-cert") {
		t.Errorf("the independent certificate should be named: %q", cond.Message)
	}
}
