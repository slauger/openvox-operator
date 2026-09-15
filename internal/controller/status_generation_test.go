package controller

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// The status controllers capture metadata.generation before observing, because
// updateStatusWithRetry re-reads the object and a spec edit landing in between
// would otherwise stamp the new generation onto a verdict derived from the old
// spec. Reading the generation off the re-read object passes an ordinary test,
// where nothing changes in between -- so these tests inject the edit, by
// serving a bumped generation from every read after the first.

// generationBumpingClient serves the given objects, but hands out a newer
// metadata.generation for the watched type from the second read onwards. That
// is exactly the read updateStatusWithRetry performs.
func generationBumpingClient(t *testing.T, bumped int64, isWatched func(client.Object) bool,
	objs ...client.Object) (client.Client, func() int) {
	t.Helper()
	reads := 0
	c := testClientBuilder(objs...).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey,
				obj client.Object, opts ...client.GetOption) error {
				if err := cl.Get(ctx, key, obj, opts...); err != nil {
					return err
				}
				if isWatched(obj) {
					reads++
					if reads > 1 {
						obj.SetGeneration(bumped)
					}
				}
				return nil
			},
		}).
		Build()
	return c, func() int { return reads }
}

func TestNodeClassifierReconcile_ObservedGenerationPredatesTheEdit(t *testing.T) {
	const observed int64 = 2
	nc := newNodeClassifier("my-enc", "https://foreman.example.invalid")
	nc.Generation = observed

	c, reads := generationBumpingClient(t, 7,
		func(o client.Object) bool { _, ok := o.(*openvoxv1alpha1.NodeClassifier); return ok },
		nc, newConfig("production", withNodeClassifierRef()),
		encSecret("production", "https://foreman.example.invalid",
			renderSource{Name: "my-enc", Generation: observed}))

	if _, err := newNodeClassifierReconciler(c).Reconcile(testCtx(), testRequest("my-enc")); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if reads() < 2 {
		t.Fatalf("only %d read(s) of the NodeClassifier, so no re-read happened and this test proves nothing", reads())
	}

	got := &openvoxv1alpha1.NodeClassifier{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "my-enc", Namespace: testNamespace}, got); err != nil {
		t.Fatalf("reading NodeClassifier: %v", err)
	}
	if got.Status.ObservedGeneration != observed {
		t.Errorf("observedGeneration = %d, want %d -- the generation the verdict was derived from, not the one that landed during the status write",
			got.Status.ObservedGeneration, observed)
	}
	cond := meta.FindStatusCondition(got.Status.Conditions, openvoxv1alpha1.ConditionNodeClassifierReady)
	if cond == nil {
		t.Fatal("expected a Ready condition")
	}
	if cond.ObservedGeneration != observed {
		t.Errorf("condition observedGeneration = %d, want %d", cond.ObservedGeneration, observed)
	}
}

func TestSigningPolicyReconcile_ObservedGenerationPredatesTheEdit(t *testing.T) {
	const observed int64 = 2
	sp := newSigningPolicy("test-policy", testCAName)
	sp.Generation = observed

	c, reads := generationBumpingClient(t, 7,
		func(o client.Object) bool { _, ok := o.(*openvoxv1alpha1.SigningPolicy); return ok },
		sp, newCertificateAuthority(testCAName), newConfig("production", withAuthorityRef(testCAName)),
		autosignPolicySecret(renderSource{Name: "test-policy", Generation: observed}))

	if _, err := newSigningPolicyReconciler(c).Reconcile(testCtx(), testRequest("test-policy")); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if reads() < 2 {
		t.Fatalf("only %d read(s) of the SigningPolicy, so no re-read happened and this test proves nothing", reads())
	}

	got := &openvoxv1alpha1.SigningPolicy{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-policy", Namespace: testNamespace}, got); err != nil {
		t.Fatalf("reading SigningPolicy: %v", err)
	}
	if got.Status.ObservedGeneration != observed {
		t.Errorf("observedGeneration = %d, want %d -- the generation the verdict was derived from, not the one that landed during the status write",
			got.Status.ObservedGeneration, observed)
	}
	cond := meta.FindStatusCondition(got.Status.Conditions, openvoxv1alpha1.ConditionSigningPolicyReady)
	if cond == nil {
		t.Fatal("expected a Ready condition")
	}
	if cond.ObservedGeneration != observed {
		t.Errorf("condition observedGeneration = %d, want %d", cond.ObservedGeneration, observed)
	}
}

// ReportProcessor matches by endpoint name rather than by annotation, but the
// generation it stamps has to be captured the same way -- the re-read inside
// updateStatusWithRetry is identical for all three.
func TestReportProcessorReconcile_ObservedGenerationPredatesTheEdit(t *testing.T) {
	const observed int64 = 2
	rp := newReportProcessor("beta", "https://beta.example.invalid/reports")
	rp.Generation = observed

	c, reads := generationBumpingClient(t, 7,
		func(o client.Object) bool { _, ok := o.(*openvoxv1alpha1.ReportProcessor); return ok },
		rp, newConfig("production"),
		webhookSecret("production", renderSource{Name: "beta", Generation: observed}))

	if _, err := newReportProcessorReconciler(c).Reconcile(testCtx(), testRequest("beta")); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if reads() < 2 {
		t.Fatalf("only %d read(s) of the ReportProcessor, so no re-read happened and this test proves nothing", reads())
	}

	got := &openvoxv1alpha1.ReportProcessor{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "beta", Namespace: testNamespace}, got); err != nil {
		t.Fatalf("reading ReportProcessor: %v", err)
	}
	if got.Status.Phase != openvoxv1alpha1.ReportProcessorPhaseActive {
		cond := meta.FindStatusCondition(got.Status.Conditions, openvoxv1alpha1.ConditionReportProcessorReady)
		t.Fatalf("phase = %q, want Active (condition %+v)", got.Status.Phase, cond)
	}
	if got.Status.ObservedGeneration != observed {
		t.Errorf("observedGeneration = %d, want %d -- the generation the verdict was derived from", got.Status.ObservedGeneration, observed)
	}
}
