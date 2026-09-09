package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// testCAName is the CertificateAuthority every case in this file binds to.
const testCAName = "test-ca"

// autosignPolicySecret builds a policy Secret as the Config controller renders
// it: the policies in autosign-policy.yaml, and the SigningPolicies they came
// from in the annotation.
func autosignPolicySecret(sources ...renderSource) *corev1.Secret {
	yaml := "reservedCertnames:\n  - \"" + operatorSigningCertname(testCAName) + "\"\npolicies:\n"
	for _, s := range sources {
		yaml += "  - name: \"" + s.Name + "\"\n    any: true\n"
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        testCAName + "-autosign-policy",
			Namespace:   testNamespace,
			Annotations: renderedFromAnnotation(sources),
		},
		Data: map[string][]byte{"autosign-policy.yaml": []byte(yaml)},
	}
}

func TestSigningPolicyReconcile_Status(t *testing.T) {
	sp := newSigningPolicy("test-policy", testCAName, true)
	sp.Generation = 2
	current := renderSource{Name: "test-policy", Generation: 2}
	ca := newCertificateAuthority(testCAName)
	cfg := newConfig("production", withAuthorityRef(testCAName))
	key := types.NamespacedName{Name: "test-policy", Namespace: testNamespace}

	t.Run("active once the policy is rendered", func(t *testing.T) {
		c := setupTestClient(sp.DeepCopy(), ca.DeepCopy(), cfg.DeepCopy(),
			autosignPolicySecret(current))
		r := newSigningPolicyReconciler(c)
		if _, err := r.Reconcile(testCtx(), testRequest("test-policy")); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		got := &openvoxv1alpha1.SigningPolicy{}
		if err := c.Get(testCtx(), key, got); err != nil {
			t.Fatalf("reading SigningPolicy: %v", err)
		}
		if got.Status.Phase != openvoxv1alpha1.SigningPolicyPhaseActive {
			t.Errorf("phase = %q, want Active", got.Status.Phase)
		}
		if !meta.IsStatusConditionTrue(got.Status.Conditions, openvoxv1alpha1.ConditionSigningPolicyReady) {
			t.Error("expected a true Ready condition")
		}
	})

	t.Run("error when the CA is missing", func(t *testing.T) {
		c := setupTestClient(sp.DeepCopy())
		r := newSigningPolicyReconciler(c)
		if _, err := r.Reconcile(testCtx(), testRequest("test-policy")); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		got := &openvoxv1alpha1.SigningPolicy{}
		if err := c.Get(testCtx(), key, got); err != nil {
			t.Fatalf("reading SigningPolicy: %v", err)
		}
		cond := meta.FindStatusCondition(got.Status.Conditions, openvoxv1alpha1.ConditionSigningPolicyReady)
		if cond == nil || cond.Reason != "CertificateAuthorityNotFound" {
			t.Errorf("expected reason CertificateAuthorityNotFound, got %+v", cond)
		}
	})

	// A policy only reaches the CA through a Config. Without one it is inert, and
	// saying so is the whole point of giving the resource its own status.
	t.Run("error when no Config references the CA", func(t *testing.T) {
		c := setupTestClient(sp.DeepCopy(), ca.DeepCopy())
		r := newSigningPolicyReconciler(c)
		if _, err := r.Reconcile(testCtx(), testRequest("test-policy")); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		got := &openvoxv1alpha1.SigningPolicy{}
		if err := c.Get(testCtx(), key, got); err != nil {
			t.Fatalf("reading SigningPolicy: %v", err)
		}
		cond := meta.FindStatusCondition(got.Status.Conditions, openvoxv1alpha1.ConditionSigningPolicyReady)
		if cond == nil || cond.Reason != "NoConfig" {
			t.Errorf("expected reason NoConfig, got %+v", cond)
		}
	})

	t.Run("error while the secret has not been rendered", func(t *testing.T) {
		c := setupTestClient(sp.DeepCopy(), ca.DeepCopy(), cfg.DeepCopy())
		r := newSigningPolicyReconciler(c)
		if _, err := r.Reconcile(testCtx(), testRequest("test-policy")); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		got := &openvoxv1alpha1.SigningPolicy{}
		if err := c.Get(testCtx(), key, got); err != nil {
			t.Fatalf("reading SigningPolicy: %v", err)
		}
		cond := meta.FindStatusCondition(got.Status.Conditions, openvoxv1alpha1.ConditionSigningPolicyReady)
		if cond == nil || cond.Reason != "NotRendered" {
			t.Errorf("expected reason NotRendered, got %+v", cond)
		}
	})

	// A failed re-render -- an unresolvable csrAttributes Secret, say -- leaves
	// the previous Secret in place. Matching on the name alone would report the
	// broken generation as active at the CA.
	t.Run("error when the rendered policy is from an earlier generation", func(t *testing.T) {
		c := setupTestClient(sp.DeepCopy(), ca.DeepCopy(), cfg.DeepCopy(),
			autosignPolicySecret(renderSource{Name: "test-policy", Generation: 1}))
		r := newSigningPolicyReconciler(c)
		if _, err := r.Reconcile(testCtx(), testRequest("test-policy")); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		got := &openvoxv1alpha1.SigningPolicy{}
		if err := c.Get(testCtx(), key, got); err != nil {
			t.Fatalf("reading SigningPolicy: %v", err)
		}
		cond := meta.FindStatusCondition(got.Status.Conditions, openvoxv1alpha1.ConditionSigningPolicyReady)
		if cond == nil || cond.Reason != "RenderedConfigStale" {
			t.Errorf("expected reason RenderedConfigStale, got %+v", cond)
		}
	})

	t.Run("error when certificateAuthorityRef is empty", func(t *testing.T) {
		unbound := sp.DeepCopy()
		unbound.Spec.CertificateAuthorityRef = ""
		c := setupTestClient(unbound)
		r := newSigningPolicyReconciler(c)
		if _, err := r.Reconcile(testCtx(), testRequest("test-policy")); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		got := &openvoxv1alpha1.SigningPolicy{}
		if err := c.Get(testCtx(), key, got); err != nil {
			t.Fatalf("reading SigningPolicy: %v", err)
		}
		cond := meta.FindStatusCondition(got.Status.Conditions, openvoxv1alpha1.ConditionSigningPolicyReady)
		if cond == nil || cond.Reason != "CertificateAuthorityRefMissing" {
			t.Errorf("expected reason CertificateAuthorityRefMissing, got %+v", cond)
		}
	})

	t.Run("error when another policy was rendered but not this one", func(t *testing.T) {
		c := setupTestClient(sp.DeepCopy(), ca.DeepCopy(), cfg.DeepCopy(),
			autosignPolicySecret(renderSource{Name: "someone-else", Generation: 1}))
		r := newSigningPolicyReconciler(c)
		if _, err := r.Reconcile(testCtx(), testRequest("test-policy")); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		got := &openvoxv1alpha1.SigningPolicy{}
		if err := c.Get(testCtx(), key, got); err != nil {
			t.Fatalf("reading SigningPolicy: %v", err)
		}
		if got.Status.Phase != openvoxv1alpha1.SigningPolicyPhaseError {
			t.Errorf("phase = %q, want Error", got.Status.Phase)
		}
	})

	// A stale Secret from before the override was set must not read as active:
	// autosignCommand replaces the binary that would have consumed the policy.
	t.Run("error when autosignCommand bypasses the policy", func(t *testing.T) {
		overridden := newConfig("production",
			withAuthorityRef(testCAName),
			withAutosignCommand())
		c := setupTestClient(sp.DeepCopy(), ca.DeepCopy(), overridden,
			autosignPolicySecret(current))
		r := newSigningPolicyReconciler(c)
		if _, err := r.Reconcile(testCtx(), testRequest("test-policy")); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		got := &openvoxv1alpha1.SigningPolicy{}
		if err := c.Get(testCtx(), key, got); err != nil {
			t.Fatalf("reading SigningPolicy: %v", err)
		}
		cond := meta.FindStatusCondition(got.Status.Conditions, openvoxv1alpha1.ConditionSigningPolicyReady)
		if cond == nil || cond.Reason != "OverriddenByAutosignCommand" {
			t.Errorf("expected reason OverriddenByAutosignCommand, got %+v", cond)
		}
	})

	// One Config opting out does not disable the policy for the Config that did
	// not, so the override only counts when every Config sets it.
	t.Run("active when only one of two Configs overrides", func(t *testing.T) {
		overridden := newConfig("legacy",
			withAuthorityRef(testCAName),
			withAutosignCommand())
		c := setupTestClient(sp.DeepCopy(), ca.DeepCopy(), cfg.DeepCopy(), overridden,
			autosignPolicySecret(current))
		r := newSigningPolicyReconciler(c)
		if _, err := r.Reconcile(testCtx(), testRequest("test-policy")); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		got := &openvoxv1alpha1.SigningPolicy{}
		if err := c.Get(testCtx(), key, got); err != nil {
			t.Fatalf("reading SigningPolicy: %v", err)
		}
		if got.Status.Phase != openvoxv1alpha1.SigningPolicyPhaseActive {
			t.Errorf("phase = %q, want Active", got.Status.Phase)
		}
	})
}
