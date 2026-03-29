package webhook

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

func TestSigningPolicyValidator(t *testing.T) {
	ca := &openvoxv1alpha1.CertificateAuthority{
		ObjectMeta: metav1.ObjectMeta{Name: "my-ca", Namespace: "default"},
	}

	t.Run("valid signing policy", func(t *testing.T) {
		c := setupTestClient(ca)
		v := &SigningPolicyValidator{Client: c}
		sp := &openvoxv1alpha1.SigningPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.SigningPolicySpec{
				CertificateAuthorityRef: "my-ca",
				Pattern: &openvoxv1alpha1.PatternSpec{
					Allow: []string{"*.example.com"},
				},
			},
		}
		_, err := v.ValidateCreate(context.Background(), sp)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("missing CA ref", func(t *testing.T) {
		c := setupTestClient()
		v := &SigningPolicyValidator{Client: c}
		sp := &openvoxv1alpha1.SigningPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.SigningPolicySpec{
				CertificateAuthorityRef: "missing-ca",
			},
		}
		_, err := v.ValidateCreate(context.Background(), sp)
		if err == nil {
			t.Error("expected error for missing CA ref")
		}
	})

	t.Run("empty pattern in allow list", func(t *testing.T) {
		c := setupTestClient(ca)
		v := &SigningPolicyValidator{Client: c}
		sp := &openvoxv1alpha1.SigningPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.SigningPolicySpec{
				CertificateAuthorityRef: "my-ca",
				Pattern: &openvoxv1alpha1.PatternSpec{
					Allow: []string{"*.example.com", ""},
				},
			},
		}
		_, err := v.ValidateCreate(context.Background(), sp)
		if err == nil {
			t.Error("expected error for empty pattern")
		}
	})

	t.Run("empty dnsAltNames pattern", func(t *testing.T) {
		c := setupTestClient(ca)
		v := &SigningPolicyValidator{Client: c}
		sp := &openvoxv1alpha1.SigningPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.SigningPolicySpec{
				CertificateAuthorityRef: "my-ca",
				DNSAltNames: &openvoxv1alpha1.PatternSpec{
					Allow: []string{""},
				},
			},
		}
		_, err := v.ValidateCreate(context.Background(), sp)
		if err == nil {
			t.Error("expected error for empty dnsAltNames pattern")
		}
	})

	t.Run("delete always succeeds", func(t *testing.T) {
		v := &SigningPolicyValidator{Client: setupTestClient()}
		_, err := v.ValidateDelete(context.Background(), &openvoxv1alpha1.SigningPolicy{})
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})
}
