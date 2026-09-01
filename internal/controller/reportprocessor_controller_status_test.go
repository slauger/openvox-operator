package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

func webhookSecret(cfgName string, endpointNames ...string) *corev1.Secret {
	yaml := "endpoints:\n"
	for _, n := range endpointNames {
		yaml += "  - name: " + n + "\n    url: https://example.invalid\n"
	}
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: cfgName + "-report-webhook", Namespace: testNamespace},
		Data:       map[string][]byte{"report-webhook.yaml": []byte(yaml)},
	}
}

func TestReportProcessorReconcile_Status(t *testing.T) {
	rp := newReportProcessor("test-rp", "production", "https://puppetdb.example.invalid")
	cfg := newConfig("production")
	key := types.NamespacedName{Name: "test-rp", Namespace: testNamespace}

	t.Run("active once the endpoint is rendered", func(t *testing.T) {
		c := setupTestClient(rp.DeepCopy(), cfg.DeepCopy(), webhookSecret("production", "test-rp"))
		r := &ReportProcessorReconciler{Client: c, Scheme: testScheme(), Recorder: testRecorder()}
		if _, err := r.Reconcile(testCtx(), testRequest("test-rp")); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		got := &openvoxv1alpha1.ReportProcessor{}
		if err := c.Get(testCtx(), key, got); err != nil {
			t.Fatalf("reading ReportProcessor: %v", err)
		}
		if got.Status.Phase != openvoxv1alpha1.ReportProcessorPhaseActive {
			t.Errorf("phase = %q, want Active", got.Status.Phase)
		}
		if !meta.IsStatusConditionTrue(got.Status.Conditions, openvoxv1alpha1.ConditionReportProcessorReady) {
			t.Error("expected a true Ready condition")
		}
	})

	t.Run("error when the Config is missing", func(t *testing.T) {
		c := setupTestClient(rp.DeepCopy())
		r := &ReportProcessorReconciler{Client: c, Scheme: testScheme(), Recorder: testRecorder()}
		if _, err := r.Reconcile(testCtx(), testRequest("test-rp")); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		got := &openvoxv1alpha1.ReportProcessor{}
		if err := c.Get(testCtx(), key, got); err != nil {
			t.Fatalf("reading ReportProcessor: %v", err)
		}
		cond := meta.FindStatusCondition(got.Status.Conditions, openvoxv1alpha1.ConditionReportProcessorReady)
		if cond == nil || cond.Reason != "ConfigNotFound" {
			t.Errorf("expected reason ConfigNotFound, got %+v", cond)
		}
	})

	t.Run("error while the secret has not been rendered", func(t *testing.T) {
		c := setupTestClient(rp.DeepCopy(), cfg.DeepCopy())
		r := &ReportProcessorReconciler{Client: c, Scheme: testScheme(), Recorder: testRecorder()}
		if _, err := r.Reconcile(testCtx(), testRequest("test-rp")); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		got := &openvoxv1alpha1.ReportProcessor{}
		if err := c.Get(testCtx(), key, got); err != nil {
			t.Fatalf("reading ReportProcessor: %v", err)
		}
		cond := meta.FindStatusCondition(got.Status.Conditions, openvoxv1alpha1.ConditionReportProcessorReady)
		if cond == nil || cond.Reason != "NotRendered" {
			t.Errorf("expected reason NotRendered, got %+v", cond)
		}
	})

	t.Run("error when another processor was rendered but not this one", func(t *testing.T) {
		c := setupTestClient(rp.DeepCopy(), cfg.DeepCopy(), webhookSecret("production", "someone-else"))
		r := &ReportProcessorReconciler{Client: c, Scheme: testScheme(), Recorder: testRecorder()}
		if _, err := r.Reconcile(testCtx(), testRequest("test-rp")); err != nil {
			t.Fatalf("reconcile: %v", err)
		}
		got := &openvoxv1alpha1.ReportProcessor{}
		if err := c.Get(testCtx(), key, got); err != nil {
			t.Fatalf("reading ReportProcessor: %v", err)
		}
		if got.Status.Phase != openvoxv1alpha1.ReportProcessorPhaseError {
			t.Errorf("phase = %q, want Error", got.Status.Phase)
		}
	})
}

func TestRenderedEndpointNames(t *testing.T) {
	names, err := renderedEndpointNames([]byte("endpoints:\n  - name: a\n    url: https://a.invalid\n  - name: b\n    url: https://b.invalid\n"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(names) != 2 || names[0] != "a" || names[1] != "b" {
		t.Errorf("got %v, want [a b]", names)
	}

	if _, err := renderedEndpointNames(nil); err == nil {
		t.Error("empty input should be an error")
	}
}
