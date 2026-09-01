package v1alpha1

import (
	"context"
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestListSemantics pins the declared list types. They are invisible in Go but
// change what the API server accepts, so only a round trip proves them.
func TestListSemantics(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping envtest validation tests in short mode")
	}
	ctx := context.Background()

	t.Run("duplicate dnsAltNames are rejected", func(t *testing.T) {
		cert := &Certificate{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "test-cert-", Namespace: "default"},
			Spec: CertificateSpec{
				AuthorityRef: "production-ca",
				DNSAltNames:  []string{"puppet.example.com", "puppet.example.com"},
			},
		}
		err := k8sClient.Create(ctx, cert)
		if err == nil {
			_ = k8sClient.Delete(ctx, cert)
			t.Fatal("expected duplicate alt names to be rejected")
		}
		if !strings.Contains(err.Error(), "Duplicate value") {
			t.Errorf("expected a duplicate-entry error, got: %v", err)
		}
	})

	t.Run("distinct dnsAltNames are accepted", func(t *testing.T) {
		cert := &Certificate{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "test-cert-", Namespace: "default"},
			Spec: CertificateSpec{
				AuthorityRef: "production-ca",
				DNSAltNames:  []string{"a.example.com", "b.example.com"},
			},
		}
		if err := k8sClient.Create(ctx, cert); err != nil {
			t.Fatalf("distinct alt names must be accepted, got: %v", err)
		}
		t.Cleanup(func() { _ = k8sClient.Delete(ctx, cert) })
	})

	t.Run("duplicate authorizationRules names are rejected", func(t *testing.T) {
		cfg := &Config{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "test-config-", Namespace: "default"},
			Spec: ConfigSpec{
				Image: ImageSpec{Repository: "example.invalid/openvox-server", Tag: "latest"},
				PuppetServer: PuppetServerSpec{
					AuthorizationRules: []AuthorizationRule{
						{Name: "same", MatchRequest: AuthorizationMatchRequest{Path: "/a"}},
						{Name: "same", MatchRequest: AuthorizationMatchRequest{Path: "/b"}},
					},
				},
			},
		}
		err := k8sClient.Create(ctx, cfg)
		if err == nil {
			_ = k8sClient.Delete(ctx, cfg)
			t.Fatal("expected duplicate rule names to be rejected")
		}
		if !strings.Contains(err.Error(), "Duplicate value") {
			t.Errorf("expected a duplicate-key error, got: %v", err)
		}
	})

	t.Run("serverUrls keep their order", func(t *testing.T) {
		// Puppet treats the order as failover priority, which is why this list
		// stays atomic rather than becoming a set.
		urls := []string{"https://db-c:8081", "https://db-a:8081", "https://db-b:8081"}
		cfg := &Config{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "test-config-", Namespace: "default"},
			Spec: ConfigSpec{
				Image:    ImageSpec{Repository: "example.invalid/openvox-server", Tag: "latest"},
				PuppetDB: PuppetDBSpec{ServerURLs: urls},
			},
		}
		if err := k8sClient.Create(ctx, cfg); err != nil {
			t.Fatalf("creating Config: %v", err)
		}
		t.Cleanup(func() { _ = k8sClient.Delete(ctx, cfg) })

		for i := range urls {
			if cfg.Spec.PuppetDB.ServerURLs[i] != urls[i] {
				t.Fatalf("serverUrls order changed: %v", cfg.Spec.PuppetDB.ServerURLs)
			}
		}
	})

	t.Run("a multi-entry code list is still accepted", func(t *testing.T) {
		// code[] stays atomic on purpose; the CEL rule keeps guarding it.
		cfg := &Config{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "test-config-", Namespace: "default"},
			Spec: ConfigSpec{
				Image: ImageSpec{Repository: "example.invalid/openvox-server", Tag: "latest"},
				Code: []CodeSpec{
					{Image: "example.invalid/code:prod", Environment: "production"},
					{Image: "example.invalid/code:stage", Environment: "staging"},
				},
			},
		}
		if err := k8sClient.Create(ctx, cfg); err != nil {
			t.Fatalf("a multi-entry code list must be accepted, got: %v", err)
		}
		t.Cleanup(func() { _ = k8sClient.Delete(ctx, cfg) })
	})

	t.Run("duplicate poolRefs are rejected", func(t *testing.T) {
		srv := &Server{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "test-server-", Namespace: "default"},
			Spec: ServerSpec{
				ConfigRef:      "production",
				CertificateRef: "production-cert",
				PoolRefs:       []string{"puppet", "puppet"},
			},
		}
		err := k8sClient.Create(ctx, srv)
		if err == nil {
			_ = k8sClient.Delete(ctx, srv)
			t.Fatal("expected duplicate pool references to be rejected")
		}
		if !strings.Contains(err.Error(), "Duplicate value") {
			t.Errorf("expected a duplicate-entry error, got: %v", err)
		}
	})
}
