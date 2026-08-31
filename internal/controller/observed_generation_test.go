package controller

import (
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// TestObservedGeneration_RecordsProcessedGeneration verifies that a completed
// reconcile records the generation it processed, in the status and in the
// conditions it sets.
//
// Bumping .metadata.generation is the API server's job, so the fixture sets it
// directly: what belongs to the controller is copying it into the status once
// the reconcile succeeded.
func TestObservedGeneration_RecordsProcessedGeneration(t *testing.T) {
	cfg := newConfig("production")
	cfg.Generation = 3
	c := setupTestClient(cfg)
	r := newConfigReconciler(c)

	key := types.NamespacedName{Name: "production", Namespace: testNamespace}
	if _, err := r.Reconcile(testCtx(), testRequest("production")); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}

	got := &openvoxv1alpha1.Config{}
	if err := c.Get(testCtx(), key, got); err != nil {
		t.Fatalf("reading Config: %v", err)
	}
	if got.Generation != 3 {
		t.Fatalf("fixture generation was not preserved, got %d", got.Generation)
	}
	if got.Status.ObservedGeneration != 3 {
		t.Errorf("status.observedGeneration = %d, want 3", got.Status.ObservedGeneration)
	}
	cond := meta.FindStatusCondition(got.Status.Conditions, openvoxv1alpha1.ConditionConfigReady)
	if cond == nil {
		t.Fatalf("condition %s missing", openvoxv1alpha1.ConditionConfigReady)
	}
	if cond.ObservedGeneration != 3 {
		t.Errorf("condition.observedGeneration = %d, want 3", cond.ObservedGeneration)
	}

	// A new generation is only picked up by the next reconcile.
	got.Generation = 4
	if err := c.Update(testCtx(), got); err != nil {
		t.Fatalf("bumping generation: %v", err)
	}
	stale := &openvoxv1alpha1.Config{}
	if err := c.Get(testCtx(), key, stale); err != nil {
		t.Fatalf("re-reading Config: %v", err)
	}
	if stale.Status.ObservedGeneration != 3 {
		t.Errorf("observedGeneration should still be 3 before the next reconcile, got %d",
			stale.Status.ObservedGeneration)
	}

	if _, err := r.Reconcile(testCtx(), testRequest("production")); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	caught := &openvoxv1alpha1.Config{}
	if err := c.Get(testCtx(), key, caught); err != nil {
		t.Fatalf("reading Config after the second reconcile: %v", err)
	}
	if caught.Status.ObservedGeneration != 4 {
		t.Errorf("observedGeneration should have caught up to 4, got %d", caught.Status.ObservedGeneration)
	}
}

// TestObservedGeneration_NotSetOnFailedReconcile pins the other half of the
// contract: a reconcile that aborts must not claim to have processed the
// generation, otherwise the status looks current while the rendered state is
// not.
func TestObservedGeneration_NotSetOnFailedReconcile(t *testing.T) {
	cfg := newConfig("production", withAuthorityRef("production-ca"))
	cfg.Generation = 7
	ca := newCertificateAuthority("production-ca")

	c := newFailingCALookupClient(cfg, ca)
	r := newConfigReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("production")); err == nil {
		t.Fatal("expected the reconcile to fail")
	}

	got := &openvoxv1alpha1.Config{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "production", Namespace: testNamespace}, got); err != nil {
		t.Fatalf("reading Config: %v", err)
	}
	if got.Status.ObservedGeneration == 7 {
		t.Error("a failed reconcile must not record the generation as observed")
	}
}

// TestCondition_LastTransitionTimeIsStable guards the removal of the explicit
// metav1.Now(). meta.SetStatusCondition only refreshes LastTransitionTime on a
// real status transition; setting it by hand produced a new timestamp on every
// reconcile and made the field useless for spotting when a state last changed.
func TestCondition_LastTransitionTimeIsStable(t *testing.T) {
	cfg := newConfig("production")
	cfg.Generation = 1
	c := setupTestClient(cfg)
	r := newConfigReconciler(c)

	key := types.NamespacedName{Name: "production", Namespace: testNamespace}
	readTransitionTime := func(step string) string {
		got := &openvoxv1alpha1.Config{}
		if err := c.Get(testCtx(), key, got); err != nil {
			t.Fatalf("reading Config after %s: %v", step, err)
		}
		cond := meta.FindStatusCondition(got.Status.Conditions, openvoxv1alpha1.ConditionConfigReady)
		if cond == nil {
			t.Fatalf("condition %s missing after %s", openvoxv1alpha1.ConditionConfigReady, step)
		}
		return cond.LastTransitionTime.String()
	}

	if _, err := r.Reconcile(testCtx(), testRequest("production")); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	first := readTransitionTime("the first reconcile")

	for i := 0; i < 3; i++ {
		if _, err := r.Reconcile(testCtx(), testRequest("production")); err != nil {
			t.Fatalf("reconcile %d: %v", i+2, err)
		}
	}
	if later := readTransitionTime("repeated reconciles"); later != first {
		t.Errorf("LastTransitionTime changed without a status transition: %s -> %s", first, later)
	}
}

// TestReadyConditions_DatabaseAndPool covers the two resources that declared a
// readiness contract without ever fulfilling it: Database had the constant but
// never set the condition, and Pool had no conditions at all.
func TestReadyConditions_DatabaseAndPool(t *testing.T) {
	t.Run("Pool reports no endpoints", func(t *testing.T) {
		pool := newPool("puppet")
		c := setupTestClient(pool)
		r := newPoolReconciler(c, false)

		if _, err := r.Reconcile(testCtx(), testRequest("puppet")); err != nil {
			t.Fatalf("reconcile: %v", err)
		}

		got := &openvoxv1alpha1.Pool{}
		if err := c.Get(testCtx(), types.NamespacedName{Name: "puppet", Namespace: testNamespace}, got); err != nil {
			t.Fatalf("reading Pool: %v", err)
		}
		cond := meta.FindStatusCondition(got.Status.Conditions, openvoxv1alpha1.ConditionPoolReady)
		if cond == nil {
			t.Fatal("expected a Ready condition on the Pool")
		}
		if cond.Status != metav1.ConditionFalse || cond.Reason != "NoEndpoints" {
			t.Errorf("expected Ready=False/NoEndpoints, got %s/%s", cond.Status, cond.Reason)
		}
	})

	t.Run("Database reports its replicas", func(t *testing.T) {
		objs := append(databasePrereqs(), newDatabase("puppetdb"))
		c := setupTestClient(objs...)
		r := newDatabaseReconciler(c)

		if _, err := r.Reconcile(testCtx(), testRequest("puppetdb")); err != nil {
			t.Fatalf("reconcile: %v", err)
		}

		got := &openvoxv1alpha1.Database{}
		if err := c.Get(testCtx(), types.NamespacedName{Name: "puppetdb", Namespace: testNamespace}, got); err != nil {
			t.Fatalf("reading Database: %v", err)
		}
		cond := meta.FindStatusCondition(got.Status.Conditions, openvoxv1alpha1.ConditionDatabaseReady)
		if cond == nil {
			t.Fatal("expected a Ready condition on the Database")
		}
		if cond.ObservedGeneration != got.Generation {
			t.Errorf("condition should carry the observed generation, got %d want %d",
				cond.ObservedGeneration, got.Generation)
		}
	})
}

// TestCertificateUsable_PrefersConditionOverPhase pins the readiness contract:
// consumers look at the condition, not at the phase.
func TestCertificateUsable_PrefersConditionOverPhase(t *testing.T) {
	t.Run("signed condition and secret name are enough", func(t *testing.T) {
		cert := newCertificate("web", "production-ca", openvoxv1alpha1.CertificatePhaseSigned)
		cert.Status.Phase = "" // phase deliberately cleared
		if !certificateUsable(cert) {
			t.Error("a certificate with a true CertSigned condition must be usable regardless of phase")
		}
	})

	t.Run("phase alone is not enough", func(t *testing.T) {
		cert := newCertificate("web", "production-ca", openvoxv1alpha1.CertificatePhaseSigned)
		meta.RemoveStatusCondition(&cert.Status.Conditions, openvoxv1alpha1.ConditionCertSigned)
		if certificateUsable(cert) {
			t.Error("a phase without the condition must not count as usable")
		}
	})

	t.Run("condition without a secret name is not enough", func(t *testing.T) {
		cert := newCertificate("web", "production-ca", openvoxv1alpha1.CertificatePhaseSigned)
		cert.Status.SecretName = ""
		if certificateUsable(cert) {
			t.Error("without a secret name there is nothing to mount")
		}
	})
}
