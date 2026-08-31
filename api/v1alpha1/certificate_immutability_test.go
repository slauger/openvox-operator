package v1alpha1

import (
	"context"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestCertificateCertnameIsImmutable covers the transition rule. A CEL
// transition rule only runs on update, so creation and the unchanged-update
// case have to keep working.
func TestCertificateCertnameIsImmutable(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping envtest validation tests in short mode")
	}
	ctx := context.Background()

	newCert := func() *Certificate {
		return &Certificate{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "test-cert-", Namespace: "default"},
			Spec:       CertificateSpec{AuthorityRef: "production-ca", Certname: "web.example.com"},
		}
	}

	t.Run("creation with an explicit certname is accepted", func(t *testing.T) {
		cert := newCert()
		if err := k8sClient.Create(ctx, cert); err != nil {
			t.Fatalf("creating Certificate: %v", err)
		}
		t.Cleanup(func() { _ = k8sClient.Delete(ctx, cert) })
		if cert.Spec.Certname != "web.example.com" {
			t.Errorf("certname = %q, want web.example.com", cert.Spec.Certname)
		}
	})

	t.Run("changing certname is rejected", func(t *testing.T) {
		cert := newCert()
		if err := k8sClient.Create(ctx, cert); err != nil {
			t.Fatalf("creating Certificate: %v", err)
		}
		t.Cleanup(func() { _ = k8sClient.Delete(ctx, cert) })

		cert.Spec.Certname = "other.example.com"
		err := k8sClient.Update(ctx, cert)
		if err == nil {
			t.Fatal("expected the certname change to be rejected")
		}
		if !apierrors.IsInvalid(err) || !strings.Contains(err.Error(), "certname is immutable") {
			t.Errorf("expected an immutability error, got: %v", err)
		}
	})

	t.Run("other fields stay editable", func(t *testing.T) {
		cert := newCert()
		if err := k8sClient.Create(ctx, cert); err != nil {
			t.Fatalf("creating Certificate: %v", err)
		}
		t.Cleanup(func() { _ = k8sClient.Delete(ctx, cert) })

		cert.Spec.DNSAltNames = append(cert.Spec.DNSAltNames, "alias.example.com")
		if err := k8sClient.Update(ctx, cert); err != nil {
			t.Errorf("changing dnsAltNames must stay possible, got: %v", err)
		}
	})

	t.Run("the defaulted certname survives an unrelated update", func(t *testing.T) {
		cert := &Certificate{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "test-cert-", Namespace: "default"},
			Spec:       CertificateSpec{AuthorityRef: "production-ca"},
		}
		if err := k8sClient.Create(ctx, cert); err != nil {
			t.Fatalf("creating Certificate: %v", err)
		}
		t.Cleanup(func() { _ = k8sClient.Delete(ctx, cert) })
		if cert.Spec.Certname != "puppet" {
			t.Fatalf("expected the default certname, got %q", cert.Spec.Certname)
		}

		cert.Spec.RenewBefore = "30d"
		if err := k8sClient.Update(ctx, cert); err != nil {
			t.Errorf("an update that leaves the defaulted certname alone must pass, got: %v", err)
		}
	})
}
