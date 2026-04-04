package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func TestReconcileServerServiceAccount(t *testing.T) {
	t.Run("creates SA when not found", func(t *testing.T) {
		cfg := newConfig("production")
		c := setupTestClient(cfg)
		r := newConfigReconciler(c)

		if err := r.reconcileServerServiceAccount(testCtx(), cfg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		sa := &corev1.ServiceAccount{}
		if err := c.Get(testCtx(), types.NamespacedName{Name: "production-server", Namespace: testNamespace}, sa); err != nil {
			t.Fatalf("ServiceAccount not created: %v", err)
		}

		if sa.AutomountServiceAccountToken == nil || *sa.AutomountServiceAccountToken {
			t.Error("expected AutomountServiceAccountToken to be false")
		}

		// Verify labels
		if sa.Labels["app.kubernetes.io/name"] != "openvox" {
			t.Errorf("expected label app.kubernetes.io/name=openvox, got %q", sa.Labels["app.kubernetes.io/name"])
		}
	})

	t.Run("no-op when SA already exists", func(t *testing.T) {
		cfg := newConfig("production")
		automount := false
		existingSA := &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "production-server",
				Namespace: testNamespace,
			},
			AutomountServiceAccountToken: &automount,
		}
		c := setupTestClient(cfg, existingSA)
		r := newConfigReconciler(c)

		if err := r.reconcileServerServiceAccount(testCtx(), cfg); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		// Should still exist and be the same
		sa := &corev1.ServiceAccount{}
		if err := c.Get(testCtx(), types.NamespacedName{Name: "production-server", Namespace: testNamespace}, sa); err != nil {
			t.Fatalf("ServiceAccount should still exist: %v", err)
		}
	})
}
