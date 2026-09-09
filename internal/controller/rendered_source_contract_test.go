package controller

import (
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// The status controllers read the rendered-from annotation; the Config
// controller writes it. The status tests build their Secret fixtures by calling
// renderedFromAnnotation themselves, so reader and writer agree there by
// construction -- the Config controller could stop writing the annotation
// altogether and those tests would still pass. These tests observe what the
// Config controller actually produces, and then run both halves against each
// other.

func renderedFrom(t *testing.T, c client.Client, secretName string) string {
	t.Helper()
	secret := &corev1.Secret{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: secretName, Namespace: testNamespace}, secret); err != nil {
		t.Fatalf("reading Secret %s: %v", secretName, err)
	}
	value, ok := secret.Annotations[AnnotationRenderedFrom]
	if !ok {
		t.Fatalf("Secret %s carries no %s annotation, so nothing can tell which resource it was rendered from",
			secretName, AnnotationRenderedFrom)
	}
	return value
}

func TestConfigReconcile_ENCSecretRecordsRenderSource(t *testing.T) {
	nc := newNodeClassifier("my-enc", "https://foreman.example.invalid")
	nc.Generation = 3
	c := setupTestClient(newConfig("production", withNodeClassifierRef()), nc)

	if _, err := newConfigReconciler(c).Reconcile(testCtx(), testRequest("production")); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if got := renderedFrom(t, c, "production-enc"); got != "my-enc=3" {
		t.Errorf("rendered-from = %q, want the classifier at the generation it was rendered from", got)
	}
}

func TestConfigReconcile_AutosignSecretRecordsRenderSources(t *testing.T) {
	ca := newCertificateAuthority(testCAName)

	alpha := newSigningPolicy("alpha", testCAName)
	alpha.Generation = 5
	beta := newSigningPolicy("beta", testCAName)
	beta.Generation = 2
	// Bound to a different CA, so it must not appear in this Secret's sources.
	foreign := newSigningPolicy("gamma", "other-ca")
	foreign.Generation = 9

	c := setupTestClient(newConfig("production", withAuthorityRef(testCAName)), ca, alpha, beta, foreign)

	if _, err := newConfigReconciler(c).Reconcile(testCtx(), testRequest("production")); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if got := renderedFrom(t, c, testCAName+"-autosign-policy"); got != "alpha=5,beta=2" {
		t.Errorf("rendered-from = %q, want both policies of this CA, sorted, and no other CA's policy", got)
	}
}

// A re-render has to refresh the annotation. If it were only written when the
// Secret is created, the recorded generation would freeze and every later
// generation would read as RenderedConfigStale forever.
func TestConfigReconcile_RenderSourceFollowsGeneration(t *testing.T) {
	nc := newNodeClassifier("my-enc", "https://foreman.example.invalid")
	nc.Generation = 3
	c := setupTestClient(newConfig("production", withNodeClassifierRef()), nc)
	r := newConfigReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("production")); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	first := renderedFrom(t, c, "production-enc")

	stored := &openvoxv1alpha1.NodeClassifier{}
	key := types.NamespacedName{Name: "my-enc", Namespace: testNamespace}
	if err := c.Get(testCtx(), key, stored); err != nil {
		t.Fatalf("reading NodeClassifier: %v", err)
	}
	stored.Generation = 4
	stored.Spec.URL = "https://foreman-2.example.invalid"
	if err := c.Update(testCtx(), stored); err != nil {
		t.Fatalf("updating NodeClassifier: %v", err)
	}
	if err := c.Get(testCtx(), key, stored); err != nil {
		t.Fatalf("re-reading NodeClassifier: %v", err)
	}
	if stored.Generation == 3 {
		t.Fatalf("generation did not advance, so this test cannot tell a refreshed annotation from a frozen one")
	}

	if _, err := r.Reconcile(testCtx(), testRequest("production")); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}

	want := fmt.Sprintf("my-enc=%d", stored.Generation)
	got := renderedFrom(t, c, "production-enc")
	if got == first {
		t.Errorf("rendered-from is still %q after a re-render at a new generation", got)
	}
	if got != want {
		t.Errorf("rendered-from = %q, want %q", got, want)
	}
}

// The two halves run against each other rather than against a shared fixture
// helper: the Config controller renders, then the status controller reads what
// it wrote.
func TestRenderedFromRoundTrip_NodeClassifier(t *testing.T) {
	nc := newNodeClassifier("my-enc", "https://foreman.example.invalid")
	nc.Generation = 3
	c := setupTestClient(newConfig("production", withNodeClassifierRef()), nc)

	if _, err := newConfigReconciler(c).Reconcile(testCtx(), testRequest("production")); err != nil {
		t.Fatalf("config reconcile: %v", err)
	}
	if _, err := newNodeClassifierReconciler(c).Reconcile(testCtx(), testRequest("my-enc")); err != nil {
		t.Fatalf("nodeclassifier reconcile: %v", err)
	}

	got := &openvoxv1alpha1.NodeClassifier{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "my-enc", Namespace: testNamespace}, got); err != nil {
		t.Fatalf("reading NodeClassifier: %v", err)
	}
	if got.Status.Phase != openvoxv1alpha1.NodeClassifierPhaseActive {
		cond := meta.FindStatusCondition(got.Status.Conditions, openvoxv1alpha1.ConditionNodeClassifierReady)
		t.Fatalf("phase = %q, want Active (condition %+v)", got.Status.Phase, cond)
	}
	if got.Status.ObservedGeneration != 3 {
		t.Errorf("observedGeneration = %d, want 3", got.Status.ObservedGeneration)
	}
}

func TestRenderedFromRoundTrip_SigningPolicy(t *testing.T) {
	sp := newSigningPolicy("allow-all", testCAName)
	sp.Generation = 4
	c := setupTestClient(newConfig("production", withAuthorityRef(testCAName)),
		newCertificateAuthority(testCAName), sp)

	if _, err := newConfigReconciler(c).Reconcile(testCtx(), testRequest("production")); err != nil {
		t.Fatalf("config reconcile: %v", err)
	}
	if _, err := newSigningPolicyReconciler(c).Reconcile(testCtx(), testRequest("allow-all")); err != nil {
		t.Fatalf("signingpolicy reconcile: %v", err)
	}

	got := &openvoxv1alpha1.SigningPolicy{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "allow-all", Namespace: testNamespace}, got); err != nil {
		t.Fatalf("reading SigningPolicy: %v", err)
	}
	if got.Status.Phase != openvoxv1alpha1.SigningPolicyPhaseActive {
		cond := meta.FindStatusCondition(got.Status.Conditions, openvoxv1alpha1.ConditionSigningPolicyReady)
		t.Fatalf("phase = %q, want Active (condition %+v)", got.Status.Phase, cond)
	}
	if got.Status.ObservedGeneration != 4 {
		t.Errorf("observedGeneration = %d, want 4", got.Status.ObservedGeneration)
	}
}
