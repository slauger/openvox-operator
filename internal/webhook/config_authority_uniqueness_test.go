package webhook

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

func configWith(name, authorityRef string) *openvoxv1alpha1.Config {
	return &openvoxv1alpha1.Config{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec: openvoxv1alpha1.ConfigSpec{
			AuthorityRef: authorityRef,
			Image:        openvoxv1alpha1.ImageSpec{Repository: "example.invalid/openvox-server", Tag: "latest"},
		},
	}
}

func TestConfigValidator_AuthorityRefIsExclusive(t *testing.T) {
	ca := &openvoxv1alpha1.CertificateAuthority{
		ObjectMeta: metav1.ObjectMeta{Name: "production-ca", Namespace: "default"},
	}

	t.Run("rejects a second Config claiming the same CA", func(t *testing.T) {
		existing := configWith("production", "production-ca")
		v := &ConfigValidator{Client: setupTestClient(ca, existing)}

		_, err := v.ValidateCreate(context.Background(), configWith("staging", "production-ca"))
		if err == nil {
			t.Fatal("expected the second Config to be rejected")
		}
		if !strings.Contains(err.Error(), "production") {
			t.Errorf("the error should name the Config already using the CA, got: %v", err)
		}
	})

	t.Run("accepts the first Config", func(t *testing.T) {
		v := &ConfigValidator{Client: setupTestClient(ca)}
		if _, err := v.ValidateCreate(context.Background(), configWith("production", "production-ca")); err != nil {
			t.Errorf("the first Config must be accepted, got: %v", err)
		}
	})

	t.Run("allows updating the Config that already holds the CA", func(t *testing.T) {
		existing := configWith("production", "production-ca")
		v := &ConfigValidator{Client: setupTestClient(ca, existing)}

		updated := configWith("production", "production-ca")
		updated.Spec.Image.Tag = "9"
		if _, err := v.ValidateUpdate(context.Background(), existing, updated); err != nil {
			t.Errorf("a Config must not conflict with itself, got: %v", err)
		}
	})

	t.Run("allows a different CA", func(t *testing.T) {
		other := &openvoxv1alpha1.CertificateAuthority{
			ObjectMeta: metav1.ObjectMeta{Name: "staging-ca", Namespace: "default"},
		}
		v := &ConfigValidator{Client: setupTestClient(ca, other, configWith("production", "production-ca"))}
		if _, err := v.ValidateCreate(context.Background(), configWith("staging", "staging-ca")); err != nil {
			t.Errorf("a Config pointing at another CA must be accepted, got: %v", err)
		}
	})
}
