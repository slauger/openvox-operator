package webhook

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

func TestCertificateAuthorityValidator(t *testing.T) {
	t.Run("valid CA", func(t *testing.T) {
		v := &CertificateAuthorityValidator{}
		ca := &openvoxv1alpha1.CertificateAuthority{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.CertificateAuthoritySpec{
				TTL:                "5y",
				AutoRenewalCertTTL: "90d",
				CRLRefreshInterval: "5m",
				Storage:            openvoxv1alpha1.StorageSpec{Size: "1Gi"},
			},
		}
		_, err := v.ValidateCreate(context.Background(), ca)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("invalid TTL", func(t *testing.T) {
		v := &CertificateAuthorityValidator{}
		ca := &openvoxv1alpha1.CertificateAuthority{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.CertificateAuthoritySpec{
				TTL: "invalid",
			},
		}
		_, err := v.ValidateCreate(context.Background(), ca)
		if err == nil {
			t.Error("expected error for invalid TTL")
		}
	})

	t.Run("invalid autoRenewalCertTTL", func(t *testing.T) {
		v := &CertificateAuthorityValidator{}
		ca := &openvoxv1alpha1.CertificateAuthority{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.CertificateAuthoritySpec{
				TTL:                "5y",
				AutoRenewalCertTTL: "bad",
			},
		}
		_, err := v.ValidateCreate(context.Background(), ca)
		if err == nil {
			t.Error("expected error for invalid autoRenewalCertTTL")
		}
	})

	t.Run("invalid crlRefreshInterval", func(t *testing.T) {
		v := &CertificateAuthorityValidator{}
		ca := &openvoxv1alpha1.CertificateAuthority{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.CertificateAuthoritySpec{
				TTL:                "5y",
				CRLRefreshInterval: "nope",
			},
		}
		_, err := v.ValidateCreate(context.Background(), ca)
		if err == nil {
			t.Error("expected error for invalid crlRefreshInterval")
		}
	})

	t.Run("invalid storage size", func(t *testing.T) {
		v := &CertificateAuthorityValidator{}
		ca := &openvoxv1alpha1.CertificateAuthority{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.CertificateAuthoritySpec{
				TTL:     "5y",
				Storage: openvoxv1alpha1.StorageSpec{Size: "not-a-quantity"},
			},
		}
		_, err := v.ValidateCreate(context.Background(), ca)
		if err == nil {
			t.Error("expected error for invalid storage size")
		}
	})

	t.Run("empty durations are valid", func(t *testing.T) {
		v := &CertificateAuthorityValidator{}
		ca := &openvoxv1alpha1.CertificateAuthority{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec:       openvoxv1alpha1.CertificateAuthoritySpec{},
		}
		_, err := v.ValidateCreate(context.Background(), ca)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("delete always succeeds", func(t *testing.T) {
		v := &CertificateAuthorityValidator{}
		_, err := v.ValidateDelete(context.Background(), &openvoxv1alpha1.CertificateAuthority{})
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})
}
