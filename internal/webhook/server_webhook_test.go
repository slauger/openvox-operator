package webhook

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

func TestServerValidator(t *testing.T) {
	cfg := &openvoxv1alpha1.Config{
		ObjectMeta: metav1.ObjectMeta{Name: "production", Namespace: "default"},
	}
	cert := &openvoxv1alpha1.Certificate{
		ObjectMeta: metav1.ObjectMeta{Name: "production-cert", Namespace: "default"},
	}
	pool := &openvoxv1alpha1.Pool{
		ObjectMeta: metav1.ObjectMeta{Name: "default-pool", Namespace: "default"},
	}

	t.Run("valid server", func(t *testing.T) {
		c := setupTestClient(cfg, cert, pool)
		v := &ServerValidator{Client: c}
		replicas := int32(1)
		s := &openvoxv1alpha1.Server{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.ServerSpec{
				ConfigRef:      "production",
				CertificateRef: "production-cert",
				PoolRefs:       []string{"default-pool"},
				Replicas:       &replicas,
			},
		}
		warnings, err := v.ValidateCreate(context.Background(), s)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if len(warnings) > 0 {
			t.Errorf("expected no warnings, got %v", warnings)
		}
	})

	t.Run("missing configRef", func(t *testing.T) {
		c := setupTestClient(cert)
		v := &ServerValidator{Client: c}
		s := &openvoxv1alpha1.Server{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.ServerSpec{
				ConfigRef:      "missing",
				CertificateRef: "production-cert",
			},
		}
		_, err := v.ValidateCreate(context.Background(), s)
		if err == nil {
			t.Error("expected error for missing configRef")
		}
	})

	t.Run("missing certificateRef", func(t *testing.T) {
		c := setupTestClient(cfg)
		v := &ServerValidator{Client: c}
		s := &openvoxv1alpha1.Server{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.ServerSpec{
				ConfigRef:      "production",
				CertificateRef: "missing-cert",
			},
		}
		_, err := v.ValidateCreate(context.Background(), s)
		if err == nil {
			t.Error("expected error for missing certificateRef")
		}
	})

	t.Run("missing poolRef", func(t *testing.T) {
		c := setupTestClient(cfg, cert)
		v := &ServerValidator{Client: c}
		s := &openvoxv1alpha1.Server{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.ServerSpec{
				ConfigRef:      "production",
				CertificateRef: "production-cert",
				PoolRefs:       []string{"missing-pool"},
			},
		}
		_, err := v.ValidateCreate(context.Background(), s)
		if err == nil {
			t.Error("expected error for missing poolRef")
		}
	})

	t.Run("CA with multiple replicas warns", func(t *testing.T) {
		c := setupTestClient(cfg, cert)
		v := &ServerValidator{Client: c}
		replicas := int32(3)
		s := &openvoxv1alpha1.Server{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.ServerSpec{
				ConfigRef:      "production",
				CertificateRef: "production-cert",
				CA:             true,
				Replicas:       &replicas,
			},
		}
		warnings, err := v.ValidateCreate(context.Background(), s)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
		if len(warnings) != 1 {
			t.Errorf("expected 1 warning, got %d", len(warnings))
		}
	})

	t.Run("update validates new object", func(t *testing.T) {
		c := setupTestClient(cfg, cert)
		v := &ServerValidator{Client: c}
		s := &openvoxv1alpha1.Server{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.ServerSpec{
				ConfigRef:      "production",
				CertificateRef: "production-cert",
			},
		}
		_, err := v.ValidateUpdate(context.Background(), s, s)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("delete always succeeds", func(t *testing.T) {
		v := &ServerValidator{Client: setupTestClient()}
		_, err := v.ValidateDelete(context.Background(), &openvoxv1alpha1.Server{})
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})
}
