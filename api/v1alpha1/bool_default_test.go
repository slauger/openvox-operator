package v1alpha1

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
)

// TestDefaultedBooleansRoundTrip pins the behaviour of the optional boolean
// fields that default to true. Declared as a plain bool with omitempty, false
// would be dropped during serialisation and the API server would re-apply the
// default, making the field impossible to disable. Only a round trip through a
// real API server proves this: a unit test assigns the Go struct directly and
// never exercises defaulting.
func TestDefaultedBooleansRoundTrip(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping envtest validation tests in short mode")
	}
	ctx := context.Background()

	t.Run("server role can be disabled for a CA-only instance", func(t *testing.T) {
		s := &Server{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "test-server-", Namespace: "default"},
			Spec: ServerSpec{
				ConfigRef:      "production",
				CertificateRef: "production-cert",
				CA:             true,
				Server:         ptr.To(false),
			},
		}
		if err := k8sClient.Create(ctx, s); err != nil {
			t.Fatalf("creating Server: %v", err)
		}
		t.Cleanup(func() { _ = k8sClient.Delete(ctx, s) })

		if BoolValue(s.Spec.Server, true) {
			t.Error("spec.server was set to false but came back as true")
		}
	})

	t.Run("unset server role defaults to true", func(t *testing.T) {
		s := &Server{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "test-server-", Namespace: "default"},
			Spec: ServerSpec{
				ConfigRef:      "production",
				CertificateRef: "production-cert",
			},
		}
		if err := k8sClient.Create(ctx, s); err != nil {
			t.Fatalf("creating Server: %v", err)
		}
		t.Cleanup(func() { _ = k8sClient.Delete(ctx, s) })

		if !BoolValue(s.Spec.Server, true) {
			t.Error("unset spec.server should default to true")
		}
	})

	t.Run("config booleans can be disabled", func(t *testing.T) {
		c := &Config{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "test-config-", Namespace: "default"},
			Spec: ConfigSpec{
				Image:                  ImageSpec{Repository: "example.invalid/openvox-server", Tag: "latest"},
				ReadOnlyRootFilesystem: ptr.To(false),
				Puppet:                 PuppetSpec{Storeconfigs: ptr.To(false)},
			},
		}
		if err := k8sClient.Create(ctx, c); err != nil {
			t.Fatalf("creating Config: %v", err)
		}
		t.Cleanup(func() { _ = k8sClient.Delete(ctx, c) })

		if BoolValue(c.Spec.ReadOnlyRootFilesystem, true) {
			t.Error("spec.readOnlyRootFilesystem was set to false but came back as true")
		}
		if BoolValue(c.Spec.Puppet.Storeconfigs, true) {
			t.Error("spec.puppet.storeconfigs was set to false but came back as true")
		}
	})

	t.Run("certificate authority booleans can be disabled", func(t *testing.T) {
		ca := &CertificateAuthority{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "test-ca-", Namespace: "default"},
			Spec: CertificateAuthoritySpec{
				AllowSubjectAltNames:         ptr.To(false),
				AllowAuthorizationExtensions: ptr.To(false),
				EnableInfraCRL:               ptr.To(false),
				AllowAutoRenewal:             ptr.To(false),
			},
		}
		if err := k8sClient.Create(ctx, ca); err != nil {
			t.Fatalf("creating CertificateAuthority: %v", err)
		}
		t.Cleanup(func() { _ = k8sClient.Delete(ctx, ca) })

		for name, got := range map[string]*bool{
			"allowSubjectAltNames":         ca.Spec.AllowSubjectAltNames,
			"allowAuthorizationExtensions": ca.Spec.AllowAuthorizationExtensions,
			"enableInfraCRL":               ca.Spec.EnableInfraCRL,
			"allowAutoRenewal":             ca.Spec.AllowAutoRenewal,
		} {
			if BoolValue(got, true) {
				t.Errorf("spec.%s was set to false but came back as true", name)
			}
		}
	})

	t.Run("unset certificate authority booleans default to true", func(t *testing.T) {
		ca := &CertificateAuthority{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "test-ca-", Namespace: "default"},
			Spec:       CertificateAuthoritySpec{},
		}
		if err := k8sClient.Create(ctx, ca); err != nil {
			t.Fatalf("creating CertificateAuthority: %v", err)
		}
		t.Cleanup(func() { _ = k8sClient.Delete(ctx, ca) })

		if !BoolValue(ca.Spec.AllowSubjectAltNames, false) ||
			!BoolValue(ca.Spec.AllowAuthorizationExtensions, false) ||
			!BoolValue(ca.Spec.EnableInfraCRL, false) ||
			!BoolValue(ca.Spec.AllowAutoRenewal, false) {
			t.Error("unset certificate authority booleans should default to true")
		}
	})
}
