package v1alpha1

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestImageSpecHasNoDefault pins the reason the default was removed.
//
// ImageSpec is embedded in Config, Server and Database. A default on the shared
// type reached all three: a Database silently inherited the *server* image, and
// a Server always came back with a fully populated image block, so the override
// logic never fell back to its Config.
func TestImageSpecHasNoDefault(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping envtest validation tests in short mode")
	}
	ctx := context.Background()

	t.Run("a Server without an image block keeps it empty", func(t *testing.T) {
		srv := &Server{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "test-server-", Namespace: "default"},
			Spec:       ServerSpec{ConfigRef: "production", CertificateRef: "production-cert"},
		}
		if err := k8sClient.Create(ctx, srv); err != nil {
			t.Fatalf("creating Server: %v", err)
		}
		t.Cleanup(func() { _ = k8sClient.Delete(ctx, srv) })

		if srv.Spec.Image.Repository != "" || srv.Spec.Image.Tag != "" {
			t.Errorf("an unset Server image must stay empty so it can fall back to the Config, got %q:%q",
				srv.Spec.Image.Repository, srv.Spec.Image.Tag)
		}
	})

	t.Run("a Config without an image is rejected", func(t *testing.T) {
		cfg := &Config{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "test-config-", Namespace: "default"},
			Spec:       ConfigSpec{},
		}
		err := k8sClient.Create(ctx, cfg)
		if err == nil {
			_ = k8sClient.Delete(ctx, cfg)
			t.Fatal("expected a Config without an image to be rejected")
		}
		if !strings.Contains(err.Error(), "spec.image.repository and spec.image.tag are required") {
			t.Errorf("expected the required-image message, got: %v", err)
		}
	})

	t.Run("a Database without an image is rejected", func(t *testing.T) {
		db := &Database{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "test-db-", Namespace: "default"},
			Spec: DatabaseSpec{
				CertificateRef: "db-cert",
				Postgres:       PostgresSpec{Host: "pg", CredentialsSecretRef: "creds"},
			},
		}
		err := k8sClient.Create(ctx, db)
		if err == nil {
			_ = k8sClient.Delete(ctx, db)
			t.Fatal("expected a Database without an image to be rejected")
		}
		if !strings.Contains(err.Error(), "spec.image.repository and spec.image.tag are required") {
			t.Errorf("expected the required-image message, got: %v", err)
		}
	})

	t.Run("an explicit image is accepted", func(t *testing.T) {
		cfg := &Config{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "test-config-", Namespace: "default"},
			Spec:       ConfigSpec{Image: ImageSpec{Repository: "example.invalid/openvox-server", Tag: "8"}},
		}
		if err := k8sClient.Create(ctx, cfg); err != nil {
			t.Fatalf("an explicit image must be accepted, got: %v", err)
		}
		t.Cleanup(func() { _ = k8sClient.Delete(ctx, cfg) })
	})
}
