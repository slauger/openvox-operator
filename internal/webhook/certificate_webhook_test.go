package webhook

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

func TestCertificateValidator(t *testing.T) {
	ca := &openvoxv1alpha1.CertificateAuthority{
		ObjectMeta: metav1.ObjectMeta{Name: "my-ca", Namespace: "default"},
	}

	t.Run("valid certificate", func(t *testing.T) {
		c := setupTestClient(ca)
		v := &CertificateValidator{Client: c}
		cert := &openvoxv1alpha1.Certificate{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.CertificateSpec{
				AuthorityRef: "my-ca",
				Certname:     "puppet",
				DNSAltNames:  []string{"puppet.example.com", "10.0.0.1"},
			},
		}
		_, err := v.ValidateCreate(context.Background(), cert)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("missing authorityRef", func(t *testing.T) {
		c := setupTestClient()
		v := &CertificateValidator{Client: c}
		cert := &openvoxv1alpha1.Certificate{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.CertificateSpec{
				AuthorityRef: "missing-ca",
				Certname:     "puppet",
			},
		}
		_, err := v.ValidateCreate(context.Background(), cert)
		if err == nil {
			t.Error("expected error for missing authorityRef")
		}
	})

	t.Run("invalid certname", func(t *testing.T) {
		c := setupTestClient(ca)
		v := &CertificateValidator{Client: c}
		cert := &openvoxv1alpha1.Certificate{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.CertificateSpec{
				AuthorityRef: "my-ca",
				Certname:     "INVALID HOST NAME!!!",
			},
		}
		_, err := v.ValidateCreate(context.Background(), cert)
		if err == nil {
			t.Error("expected error for invalid certname")
		}
	})

	t.Run("invalid dnsAltName", func(t *testing.T) {
		c := setupTestClient(ca)
		v := &CertificateValidator{Client: c}
		cert := &openvoxv1alpha1.Certificate{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.CertificateSpec{
				AuthorityRef: "my-ca",
				Certname:     "puppet",
				DNSAltNames:  []string{"valid.example.com", "INVALID!!!"},
			},
		}
		_, err := v.ValidateCreate(context.Background(), cert)
		if err == nil {
			t.Error("expected error for invalid dnsAltName")
		}
	})

	t.Run("delete always succeeds", func(t *testing.T) {
		v := &CertificateValidator{Client: setupTestClient()}
		_, err := v.ValidateDelete(context.Background(), &openvoxv1alpha1.Certificate{})
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})
}
