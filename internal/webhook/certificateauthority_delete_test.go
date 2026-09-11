package webhook

import (
	"context"
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/resource"
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

func TestValidateStorageTransition(t *testing.T) {
	withStorage := func(size, class string) *openvoxv1alpha1.CertificateAuthority {
		ca := &openvoxv1alpha1.CertificateAuthority{
			ObjectMeta: metav1.ObjectMeta{Name: "production-ca", Namespace: "default"},
			Spec:       openvoxv1alpha1.CertificateAuthoritySpec{Storage: &openvoxv1alpha1.StorageSpec{StorageClass: class}},
		}
		if size != "" {
			ca.Spec.Storage.Size = new(resource.MustParse(size))
		}
		return ca
	}

	tests := []struct {
		name    string
		old     *openvoxv1alpha1.CertificateAuthority
		updated *openvoxv1alpha1.CertificateAuthority
		wantErr string
	}{
		{
			name:    "growing is allowed",
			old:     withStorage("1Gi", "fast"),
			updated: withStorage("10Gi", "fast"),
		},
		{
			name:    "unchanged is allowed",
			old:     withStorage("1Gi", "fast"),
			updated: withStorage("1Gi", "fast"),
		},
		{
			name:    "shrinking is rejected",
			old:     withStorage("10Gi", "fast"),
			updated: withStorage("1Gi", "fast"),
			wantErr: "cannot be decreased",
		},
		{
			name:    "changing the class is rejected",
			old:     withStorage("1Gi", "fast"),
			updated: withStorage("1Gi", "slow"),
			wantErr: "storageClass is immutable",
		},
		{
			name:    "equivalent units are not a shrink",
			old:     withStorage("1024Mi", "fast"),
			updated: withStorage("1Gi", "fast"),
		},
		{
			name:    "adding a storage block is allowed",
			old:     &openvoxv1alpha1.CertificateAuthority{ObjectMeta: metav1.ObjectMeta{Name: "production-ca", Namespace: "default"}},
			updated: withStorage("1Gi", "fast"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateStorageTransition(tt.old, tt.updated)
			if tt.wantErr == "" {
				if err != nil {
					t.Errorf("expected no error, got: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected an error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("expected %q, got: %v", tt.wantErr, err)
			}
		})
	}
}
