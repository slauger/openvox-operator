package webhook

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

func TestCertificateAuthorityValidator_ValidateDelete(t *testing.T) {
	ca := &openvoxv1alpha1.CertificateAuthority{
		ObjectMeta: metav1.ObjectMeta{Name: "production-ca", Namespace: "default"},
	}
	certFor := func(name, authority string) *openvoxv1alpha1.Certificate {
		return &openvoxv1alpha1.Certificate{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			Spec:       openvoxv1alpha1.CertificateSpec{AuthorityRef: authority},
		}
	}

	t.Run("rejects deletion while certificates reference the CA", func(t *testing.T) {
		v := &CertificateAuthorityValidator{
			Client: setupTestClient(ca, certFor("web", "production-ca"), certFor("db", "production-ca")),
		}
		_, err := v.ValidateDelete(context.Background(), ca)
		if err == nil {
			t.Fatal("expected deletion to be rejected")
		}
		for _, want := range []string{"web", "db", "production-ca"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error should mention %q, got: %v", want, err)
			}
		}
	})

	t.Run("warns but allows deletion without certificates", func(t *testing.T) {
		v := &CertificateAuthorityValidator{Client: setupTestClient(ca)}
		warnings, err := v.ValidateDelete(context.Background(), ca)
		if err != nil {
			t.Fatalf("deletion should be allowed, got: %v", err)
		}
		if len(warnings) == 0 {
			t.Error("expected a warning about the irreversible key loss")
		}
	})

	t.Run("ignores certificates of another CA", func(t *testing.T) {
		v := &CertificateAuthorityValidator{Client: setupTestClient(ca, certFor("other", "staging-ca"))}
		if _, err := v.ValidateDelete(context.Background(), ca); err != nil {
			t.Errorf("a certificate of a different CA must not block deletion, got: %v", err)
		}
	})
}
