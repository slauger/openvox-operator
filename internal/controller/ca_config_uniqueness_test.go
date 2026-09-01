package controller

import (
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	"context"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// TestFindConfigForCA_Deterministic pins the tie-break. Which Config wins
// decides the image of the CA setup Job, so it must not depend on listing order.
func TestFindConfigForCA_Deterministic(t *testing.T) {
	ca := newCertificateAuthority("production-ca")

	// Insertion order deliberately reversed against alphabetical order.
	c := setupTestClient(
		ca,
		newConfig("zulu", withAuthorityRef("production-ca")),
		newConfig("alpha", withAuthorityRef("production-ca")),
		newConfig("mike", withAuthorityRef("production-ca")),
		newConfig("unrelated", withAuthorityRef("other-ca")),
	)
	r := newCertificateAuthorityReconciler(c)

	for i := 0; i < 5; i++ {
		cfg, err := r.findConfigForCA(testCtx(), ca)
		if err != nil {
			t.Fatalf("lookup %d: %v", i, err)
		}
		if cfg == nil {
			t.Fatalf("lookup %d returned no Config", i)
		}
		if cfg.Name != "alpha" {
			t.Fatalf("lookup %d picked %q, want the alphabetically first name", i, cfg.Name)
		}
	}
}

// TestFindConfigForCA_NoneIsNotAnError covers bring-up: a CA created before its
// Config is a normal state, not a failure.
func TestFindConfigForCA_NoneIsNotAnError(t *testing.T) {
	ca := newCertificateAuthority("production-ca")
	r := newCertificateAuthorityReconciler(setupTestClient(ca))

	cfg, err := r.findConfigForCA(testCtx(), ca)
	if err != nil {
		t.Fatalf("a missing Config must not be an error, got: %v", err)
	}
	if cfg != nil {
		t.Errorf("expected nil, got %q", cfg.Name)
	}
}

// TestFindConfigForCA_ListErrorPropagates keeps the rule from #509: a transient
// failure must not look like "no Config yet", which would make the controller
// wait forever instead of retrying.
func TestFindConfigForCA_ListErrorPropagates(t *testing.T) {
	ca := newCertificateAuthority("production-ca")

	b := fake.NewClientBuilder().
		WithScheme(testScheme()).
		WithObjects(ca, newConfig("production", withAuthorityRef("production-ca"))).
		WithStatusSubresource(&openvoxv1alpha1.CertificateAuthority{}).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				if _, ok := list.(*openvoxv1alpha1.ConfigList); ok {
					return serverTimeout("configs")
				}
				return cl.List(ctx, list, opts...)
			},
		})
	for _, idx := range fieldIndexes() {
		b = b.WithIndex(idx.obj, idx.field, idx.extract)
	}
	r := newCertificateAuthorityReconciler(b.Build())

	if _, err := r.findConfigForCA(testCtx(), ca); err == nil {
		t.Fatal("expected a transient list error to be propagated")
	}
}
