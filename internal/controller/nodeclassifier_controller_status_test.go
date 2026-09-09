package controller

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// encSecret builds an ENC Secret as the Config controller renders it: the
// endpoint in enc.yaml, and the NodeClassifier it came from in the annotation.
func encSecret(cfgName, url string, sources ...renderSource) *corev1.Secret {
	yaml := "url: " + url + "\nmethod: GET\npath: /node/{certname}\nresponseFormat: yaml\ntimeoutSeconds: 10\n"
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        cfgName + "-enc",
			Namespace:   testNamespace,
			Annotations: renderedFromAnnotation(sources),
		},
		Data: map[string][]byte{"enc.yaml": []byte(yaml)},
	}
}

func TestNodeClassifierReconcile_Status(t *testing.T) {
	const encURL = "https://foreman.example.invalid"
	nc := newNodeClassifier("my-enc", encURL)
	nc.Generation = 2
	current := renderSource{Name: "my-enc", Generation: 2}
	cfg := newConfig("production", withNodeClassifierRef())
	key := types.NamespacedName{Name: "my-enc", Namespace: testNamespace}

	t.Run("active once the ENC config is rendered", func(t *testing.T) {
		c := setupTestClient(nc.DeepCopy(), cfg.DeepCopy(), encSecret("production", encURL, current))
		r := newNodeClassifierReconciler(c)
		if _, err := r.Reconcile(testCtx(), testRequest("my-enc")); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		got := &openvoxv1alpha1.NodeClassifier{}
		if err := c.Get(testCtx(), key, got); err != nil {
			t.Fatalf("reading NodeClassifier: %v", err)
		}
		if got.Status.Phase != openvoxv1alpha1.NodeClassifierPhaseActive {
			t.Errorf("phase = %q, want Active", got.Status.Phase)
		}
		if !meta.IsStatusConditionTrue(got.Status.Conditions, openvoxv1alpha1.ConditionNodeClassifierReady) {
			t.Error("expected a true Ready condition")
		}
		if got.Status.ObservedGeneration != nc.Generation {
			t.Errorf("observedGeneration = %d, want %d", got.Status.ObservedGeneration, nc.Generation)
		}
	})

	t.Run("error when no Config references it", func(t *testing.T) {
		c := setupTestClient(nc.DeepCopy())
		r := newNodeClassifierReconciler(c)
		if _, err := r.Reconcile(testCtx(), testRequest("my-enc")); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		got := &openvoxv1alpha1.NodeClassifier{}
		if err := c.Get(testCtx(), key, got); err != nil {
			t.Fatalf("reading NodeClassifier: %v", err)
		}
		requireErrorCondition(t, got.Status.Conditions, "NotReferenced")
	})

	t.Run("error while the secret has not been rendered", func(t *testing.T) {
		c := setupTestClient(nc.DeepCopy(), cfg.DeepCopy())
		r := newNodeClassifierReconciler(c)
		if _, err := r.Reconcile(testCtx(), testRequest("my-enc")); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		got := &openvoxv1alpha1.NodeClassifier{}
		if err := c.Get(testCtx(), key, got); err != nil {
			t.Fatalf("reading NodeClassifier: %v", err)
		}
		requireErrorCondition(t, got.Status.Conditions, "NotRendered")
	})

	// A Secret left over from a previous nodeClassifierRef would otherwise read
	// as active: it exists, and it is named after the Config.
	t.Run("error when the rendered secret belongs to another classifier", func(t *testing.T) {
		c := setupTestClient(nc.DeepCopy(), cfg.DeepCopy(),
			encSecret("production", "https://someone-else.example.invalid",
				renderSource{Name: "someone-else", Generation: 1}))
		r := newNodeClassifierReconciler(c)
		if _, err := r.Reconcile(testCtx(), testRequest("my-enc")); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		got := &openvoxv1alpha1.NodeClassifier{}
		if err := c.Get(testCtx(), key, got); err != nil {
			t.Fatalf("reading NodeClassifier: %v", err)
		}
		requireErrorCondition(t, got.Status.Conditions, "NotRendered")
		cond := meta.FindStatusCondition(got.Status.Conditions, openvoxv1alpha1.ConditionNodeClassifierReady)
		if cond != nil && !strings.Contains(cond.Message, "different NodeClassifier") {
			t.Errorf("message = %q, want it to say the Secret belongs to another classifier rather than that none exists", cond.Message)
		}
	})

	// A failed re-render leaves the previous Secret in place. Matching on the
	// name alone would report the new spec as active while the servers still run
	// the old one -- including, for an auth rotation, a revoked credential.
	t.Run("error when the rendered secret is from an earlier generation", func(t *testing.T) {
		c := setupTestClient(nc.DeepCopy(), cfg.DeepCopy(),
			encSecret("production", encURL, renderSource{Name: "my-enc", Generation: 1}))
		r := newNodeClassifierReconciler(c)
		if _, err := r.Reconcile(testCtx(), testRequest("my-enc")); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		got := &openvoxv1alpha1.NodeClassifier{}
		if err := c.Get(testCtx(), key, got); err != nil {
			t.Fatalf("reading NodeClassifier: %v", err)
		}
		requireErrorCondition(t, got.Status.Conditions, "RenderedConfigStale")
		if got.Status.ObservedGeneration != nc.Generation {
			t.Errorf("observedGeneration = %d, want the generation that is not in effect (%d)",
				got.Status.ObservedGeneration, nc.Generation)
		}
	})

	// A stale Secret from before the override was set must not read as active:
	// externalNodesCommand replaces the binary that would have consumed it.
	t.Run("error when externalNodesCommand bypasses the classifier", func(t *testing.T) {
		overridden := newConfig("production",
			withNodeClassifierRef(),
			withExternalNodesCommand("/usr/local/bin/custom-enc"))
		c := setupTestClient(nc.DeepCopy(), overridden, encSecret("production", encURL, current))
		r := newNodeClassifierReconciler(c)
		if _, err := r.Reconcile(testCtx(), testRequest("my-enc")); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		got := &openvoxv1alpha1.NodeClassifier{}
		if err := c.Get(testCtx(), key, got); err != nil {
			t.Fatalf("reading NodeClassifier: %v", err)
		}
		requireErrorCondition(t, got.Status.Conditions, "OverriddenByExternalNodesCommand")
	})

	// On upgrade the rendered Secret exists but predates the annotation. Reading
	// that as "this classifier is not in it" would flip every classifier to
	// NotRendered, and for a paused Config it would stay there.
	t.Run("secret from before the annotation reports an unknown source", func(t *testing.T) {
		legacy := encSecret("production", encURL)
		delete(legacy.Annotations, AnnotationRenderedFrom)
		c := setupTestClient(nc.DeepCopy(), cfg.DeepCopy(), legacy)
		r := newNodeClassifierReconciler(c)
		if _, err := r.Reconcile(testCtx(), testRequest("my-enc")); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		got := &openvoxv1alpha1.NodeClassifier{}
		if err := c.Get(testCtx(), key, got); err != nil {
			t.Fatalf("reading NodeClassifier: %v", err)
		}
		requireErrorCondition(t, got.Status.Conditions, "RenderSourceUnknown")
	})

	// A Config that has not rendered anything yet says nothing about the
	// classifier, so it must not pull a Config that did render it out of Active.
	t.Run("active when a second Config has not rendered yet", func(t *testing.T) {
		second := newConfig("staging", withNodeClassifierRef())
		c := setupTestClient(nc.DeepCopy(), cfg.DeepCopy(), second,
			encSecret("production", encURL, current))
		r := newNodeClassifierReconciler(c)
		if _, err := r.Reconcile(testCtx(), testRequest("my-enc")); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		got := &openvoxv1alpha1.NodeClassifier{}
		if err := c.Get(testCtx(), key, got); err != nil {
			t.Fatalf("reading NodeClassifier: %v", err)
		}
		if got.Status.Phase != openvoxv1alpha1.NodeClassifierPhaseActive {
			t.Errorf("phase = %q, want Active", got.Status.Phase)
		}
	})

	// A healthy Config must not mask a broken one. Both cases below are a server
	// demonstrably running something that is not this generation, exactly like a
	// stale render, so neither may be hidden behind a Config that is current.
	t.Run("an unrecorded render in one Config outranks a current one in another", func(t *testing.T) {
		second := newConfig("staging", withNodeClassifierRef())
		legacy := encSecret("staging", encURL)
		delete(legacy.Annotations, AnnotationRenderedFrom)
		c := setupTestClient(nc.DeepCopy(), cfg.DeepCopy(), second,
			encSecret("production", encURL, current), legacy)
		r := newNodeClassifierReconciler(c)
		if _, err := r.Reconcile(testCtx(), testRequest("my-enc")); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		got := &openvoxv1alpha1.NodeClassifier{}
		if err := c.Get(testCtx(), key, got); err != nil {
			t.Fatalf("reading NodeClassifier: %v", err)
		}
		requireErrorCondition(t, got.Status.Conditions, "RenderSourceUnknown")
		cond := meta.FindStatusCondition(got.Status.Conditions, openvoxv1alpha1.ConditionNodeClassifierReady)
		if cond != nil && !strings.Contains(cond.Message, "staging-enc") {
			t.Errorf("message = %q, want it to name the Secret that is holding the classifier back", cond.Message)
		}
	})

	t.Run("a foreign render in one Config outranks a current one in another", func(t *testing.T) {
		second := newConfig("staging", withNodeClassifierRef())
		c := setupTestClient(nc.DeepCopy(), cfg.DeepCopy(), second,
			encSecret("production", encURL, current),
			encSecret("staging", encURL, renderSource{Name: "someone-else", Generation: 1}))
		r := newNodeClassifierReconciler(c)
		if _, err := r.Reconcile(testCtx(), testRequest("my-enc")); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		got := &openvoxv1alpha1.NodeClassifier{}
		if err := c.Get(testCtx(), key, got); err != nil {
			t.Fatalf("reading NodeClassifier: %v", err)
		}
		requireErrorCondition(t, got.Status.Conditions, "NotRendered")
		cond := meta.FindStatusCondition(got.Status.Conditions, openvoxv1alpha1.ConditionNodeClassifierReady)
		if cond != nil && !strings.Contains(cond.Message, "staging-enc") {
			t.Errorf("message = %q, want it to name the Secret that is holding the classifier back", cond.Message)
		}
	})

	// Where several Configs render the same classifier, one still on an earlier
	// generation holds the whole resource back -- the current spec is not in
	// effect everywhere yet. Without that rule the verdict would depend on which
	// Config the listing happened to return first.
	t.Run("stale in one Config outranks a current render in another", func(t *testing.T) {
		second := newConfig("staging", withNodeClassifierRef())
		c := setupTestClient(nc.DeepCopy(), cfg.DeepCopy(), second,
			encSecret("production", encURL, current),
			encSecret("staging", encURL, renderSource{Name: "my-enc", Generation: 1}))
		r := newNodeClassifierReconciler(c)
		if _, err := r.Reconcile(testCtx(), testRequest("my-enc")); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		got := &openvoxv1alpha1.NodeClassifier{}
		if err := c.Get(testCtx(), key, got); err != nil {
			t.Fatalf("reading NodeClassifier: %v", err)
		}
		requireErrorCondition(t, got.Status.Conditions, "RenderedConfigStale")
	})
}

// Repointing nodeClassifierRef has to enqueue the classifier that just lost its
// Config, not only the one that gained it. controller-runtime runs the map
// function against the old object as well, so reading the reference off the
// event object is what makes the old classifier drop out of Active.
func TestNodeClassifiersForConfig(t *testing.T) {
	mapFn := nodeClassifiersForConfig()

	old := newConfig("production", withNodeClassifierRef())
	old.Spec.NodeClassifierRef = "old-enc"
	requests := mapFn(testCtx(), old)
	if len(requests) != 1 || requests[0].Name != "old-enc" {
		t.Errorf("got %v, want a request for old-enc", requests)
	}

	cleared := newConfig("production")
	if got := mapFn(testCtx(), cleared); len(got) != 0 {
		t.Errorf("got %v, want no requests for a Config without a nodeClassifierRef", got)
	}
}
