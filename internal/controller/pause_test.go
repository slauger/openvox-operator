package controller

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

func pausedAnnotation() map[string]string {
	return map[string]string{openvoxv1alpha1.AnnotationPaused: "true"}
}

// TestPause_SkipsReconciliation walks every controller that has one, because a
// pause that silently does not apply to one resource type is worse than none.
func TestPause_SkipsReconciliation(t *testing.T) {
	tests := []struct {
		name       string
		build      func() (client.Client, func(client.Client) (ctrl.Result, error), types.NamespacedName)
		conditions func(client.Client, types.NamespacedName) []metav1.Condition
	}{
		{
			name: "Config",
			build: func() (client.Client, func(client.Client) (ctrl.Result, error), types.NamespacedName) {
				cfg := newConfig("production")
				cfg.Annotations = pausedAnnotation()
				c := setupTestClient(cfg)
				return c, func(c client.Client) (ctrl.Result, error) {
					return newConfigReconciler(c).Reconcile(testCtx(), testRequest("production"))
				}, types.NamespacedName{Name: "production", Namespace: testNamespace}
			},
			conditions: func(c client.Client, key types.NamespacedName) []metav1.Condition {
				got := &openvoxv1alpha1.Config{}
				_ = c.Get(testCtx(), key, got)
				return got.Status.Conditions
			},
		},
		{
			name: "Server",
			build: func() (client.Client, func(client.Client) (ctrl.Result, error), types.NamespacedName) {
				srv := newServer("web")
				srv.Annotations = pausedAnnotation()
				objs := append(serverPrereqs(), srv)
				c := setupTestClient(objs...)
				return c, func(c client.Client) (ctrl.Result, error) {
					return newServerReconciler(c).Reconcile(testCtx(), testRequest("web"))
				}, types.NamespacedName{Name: "web", Namespace: testNamespace}
			},
			conditions: func(c client.Client, key types.NamespacedName) []metav1.Condition {
				got := &openvoxv1alpha1.Server{}
				_ = c.Get(testCtx(), key, got)
				return got.Status.Conditions
			},
		},
		{
			name: "Pool",
			build: func() (client.Client, func(client.Client) (ctrl.Result, error), types.NamespacedName) {
				pool := newPool("puppet")
				pool.Annotations = pausedAnnotation()
				c := setupTestClient(pool)
				return c, func(c client.Client) (ctrl.Result, error) {
					return newPoolReconciler(c, false).Reconcile(testCtx(), testRequest("puppet"))
				}, types.NamespacedName{Name: "puppet", Namespace: testNamespace}
			},
			conditions: func(c client.Client, key types.NamespacedName) []metav1.Condition {
				got := &openvoxv1alpha1.Pool{}
				_ = c.Get(testCtx(), key, got)
				return got.Status.Conditions
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, reconcile, key := tt.build()
			res, err := reconcile(c)
			if err != nil {
				t.Fatalf("reconcile: %v", err)
			}
			if res.RequeueAfter != 0 {
				t.Errorf("a paused resource should not be requeued, got %v", res.RequeueAfter)
			}
			if !meta.IsStatusConditionTrue(tt.conditions(c, key), openvoxv1alpha1.ConditionPaused) {
				t.Error("expected a true Paused condition")
			}
		})
	}
}

// TestPause_ProducesNoChildResources checks the actual effect rather than just
// the condition: nothing may be created while paused.
func TestPause_ProducesNoChildResources(t *testing.T) {
	srv := newServer("web")
	srv.Annotations = pausedAnnotation()
	objs := append(serverPrereqs(), srv)
	c := setupTestClient(objs...)

	if _, err := newServerReconciler(c).Reconcile(testCtx(), testRequest("web")); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	err := c.Get(testCtx(), types.NamespacedName{Name: "web", Namespace: testNamespace}, &appsv1.Deployment{})
	if err == nil {
		t.Error("a paused Server must not create its Deployment")
	} else if !apierrors.IsNotFound(err) {
		t.Fatalf("checking for the Deployment: %v", err)
	}
}

// TestPause_ResumesWhenAnnotationIsRemoved makes sure the condition does not
// linger and normal reconciliation comes back.
func TestPause_ResumesWhenAnnotationIsRemoved(t *testing.T) {
	cfg := newConfig("production")
	cfg.Annotations = pausedAnnotation()
	c := setupTestClient(cfg)
	r := newConfigReconciler(c)
	key := types.NamespacedName{Name: "production", Namespace: testNamespace}

	if _, err := r.Reconcile(testCtx(), testRequest("production")); err != nil {
		t.Fatalf("paused reconcile: %v", err)
	}

	current := &openvoxv1alpha1.Config{}
	if err := c.Get(testCtx(), key, current); err != nil {
		t.Fatalf("reading Config: %v", err)
	}
	current.Annotations = nil
	if err := c.Update(testCtx(), current); err != nil {
		t.Fatalf("removing the annotation: %v", err)
	}

	if _, err := r.Reconcile(testCtx(), testRequest("production")); err != nil {
		t.Fatalf("resumed reconcile: %v", err)
	}

	got := &openvoxv1alpha1.Config{}
	if err := c.Get(testCtx(), key, got); err != nil {
		t.Fatalf("reading Config: %v", err)
	}
	if meta.FindStatusCondition(got.Status.Conditions, openvoxv1alpha1.ConditionPaused) != nil {
		t.Error("the Paused condition should be gone once the annotation is removed")
	}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "production-config", Namespace: testNamespace},
		&corev1.ConfigMap{}); err != nil {
		t.Errorf("normal reconciliation should have resumed: %v", err)
	}
}

// TestPause_StillProcessesDeletion is the guard against turning the annotation
// into a trap: a paused resource with a finalizer has to remain deletable.
func TestPause_StillProcessesDeletion(t *testing.T) {
	now := metav1.Now()
	ca := newCertificateAuthority("production-ca")
	ca.Annotations = pausedAnnotation()
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
	case err != nil:
		t.Fatalf("reading CertificateAuthority: %v", err)
	case controllerutil.ContainsFinalizer(got, certificateAuthorityFinalizer):
		t.Error("a paused resource must still release its finalizer on deletion")
	}
}

// TestPause_DoesNotWriteStatusRepeatedly keeps a paused resource quiet.
func TestPause_DoesNotWriteStatusRepeatedly(t *testing.T) {
	cfg := newConfig("production")
	cfg.Annotations = pausedAnnotation()
	c := setupTestClient(cfg)
	r := newConfigReconciler(c)
	key := types.NamespacedName{Name: "production", Namespace: testNamespace}

	if _, err := r.Reconcile(testCtx(), testRequest("production")); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	first := &openvoxv1alpha1.Config{}
	if err := c.Get(testCtx(), key, first); err != nil {
		t.Fatalf("reading Config: %v", err)
	}

	for i := 0; i < 3; i++ {
		if _, err := r.Reconcile(testCtx(), testRequest("production")); err != nil {
			t.Fatalf("reconcile %d: %v", i+2, err)
		}
	}
	later := &openvoxv1alpha1.Config{}
	if err := c.Get(testCtx(), key, later); err != nil {
		t.Fatalf("reading Config: %v", err)
	}
	if later.ResourceVersion != first.ResourceVersion {
		t.Errorf("a paused resource should not be written again, resourceVersion %s -> %s",
			first.ResourceVersion, later.ResourceVersion)
	}
}
