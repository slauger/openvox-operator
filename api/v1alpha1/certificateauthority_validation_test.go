package v1alpha1

import (
	"context"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func validCA() *CertificateAuthority {
	return &CertificateAuthority{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "test-ca-", Namespace: "default"},
		Spec:       CertificateAuthoritySpec{},
	}
}

// TestCertificateAuthorityStorageExclusivity covers the rule that an external
// CA cannot also request cluster storage.
//
// The previous rule compared storage.size against the literal default '1Gi',
// which let exactly that value through alongside external -- the case this
// suite pins down.
func TestCertificateAuthorityStorageExclusivity(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping envtest validation tests in short mode")
	}
	ctx := context.Background()
	external := &ExternalCASpec{URL: "https://puppet-ca.example.com:8140"}

	tests := []struct {
		name    string
		mutate  func(*CertificateAuthority)
		wantErr string
	}{
		{
			name:   "no external and no storage accepted",
			mutate: func(_ *CertificateAuthority) {},
		},
		{
			name: "storage without external accepted",
			mutate: func(ca *CertificateAuthority) {
				ca.Spec.Storage = &StorageSpec{Size: "10Gi"}
			},
		},
		{
			name: "external without storage accepted",
			mutate: func(ca *CertificateAuthority) {
				ca.Spec.External = external
			},
		},
		{
			name: "external with custom storage rejected",
			mutate: func(ca *CertificateAuthority) {
				ca.Spec.External = external
				ca.Spec.Storage = &StorageSpec{Size: "10Gi"}
			},
			wantErr: "external and storage are mutually exclusive",
		},
		{
			name: "external with storage at the default size rejected",
			mutate: func(ca *CertificateAuthority) {
				ca.Spec.External = external
				ca.Spec.Storage = &StorageSpec{Size: "1Gi"}
			},
			wantErr: "external and storage are mutually exclusive",
		},
		{
			name: "external with a storage block carrying only a class rejected",
			mutate: func(ca *CertificateAuthority) {
				ca.Spec.External = external
				ca.Spec.Storage = &StorageSpec{StorageClass: "fast-ssd"}
			},
			wantErr: "external and storage are mutually exclusive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ca := validCA()
			tt.mutate(ca)

			err := k8sClient.Create(ctx, ca)
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("expected the CertificateAuthority to be accepted, got: %v", err)
				}
				t.Cleanup(func() { _ = k8sClient.Delete(ctx, ca) })
				return
			}
			if err == nil {
				t.Fatal("expected the CertificateAuthority to be rejected")
			}
			if !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("expected an invalid error containing %q, got: %v", tt.wantErr, err)
			}
		})
	}
}

// TestCertificateAuthorityStorageDefault documents that omitting the storage
// block leaves it unset rather than materialising a default -- the reason the
// size fallback lives in the controller.
func TestCertificateAuthorityStorageDefault(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping envtest validation tests in short mode")
	}
	ctx := context.Background()

	ca := validCA()
	if err := k8sClient.Create(ctx, ca); err != nil {
		t.Fatalf("creating CertificateAuthority: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(ctx, ca) })

	if ca.Spec.Storage != nil {
		t.Errorf("an omitted storage block should stay nil, got %+v", ca.Spec.Storage)
	}
}
