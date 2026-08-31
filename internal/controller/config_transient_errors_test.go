package controller

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// serverTimeout mimics the transient failure an overloaded or restarting API
// server returns. It is deliberately not a NotFound.
func serverTimeout(resource string) error {
	return apierrors.NewServerTimeout(
		schema.GroupResource{Group: "openvox.voxpupuli.org", Resource: resource},
		"get", 1)
}

// TestReconcile_TransientCALookupFailure covers the case where the CA lookup
// fails for a reason other than NotFound. Rendering must not fall back to a
// configuration without CA settings, and the Config must not be reported as
// Running.
func TestReconcile_TransientCALookupFailure(t *testing.T) {
	cfg := newConfig("production", withAuthorityRef("production-ca"))
	ca := newCertificateAuthority("production-ca")

	c := fake.NewClientBuilder().
		WithScheme(testScheme()).
		WithObjects(cfg, ca).
		WithStatusSubresource(&openvoxv1alpha1.Config{}, &openvoxv1alpha1.CertificateAuthority{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*openvoxv1alpha1.CertificateAuthority); ok {
					return serverTimeout("certificateauthorities")
				}
				return cl.Get(ctx, key, obj, opts...)
			},
		}).
		Build()

	r := newConfigReconciler(c)
	_, err := r.Reconcile(testCtx(), testRequest(cfg.Name))
	if err == nil {
		t.Fatal("expected the reconcile to fail on a transient CA lookup error")
	}
	if apierrors.IsNotFound(err) {
		t.Fatalf("transient error was turned into NotFound: %v", err)
	}

	cm := &corev1.ConfigMap{}
	cmErr := c.Get(testCtx(), types.NamespacedName{Name: "production-config", Namespace: testNamespace}, cm)
	if cmErr == nil {
		t.Error("ConfigMap was written despite the failed CA lookup")
	} else if !apierrors.IsNotFound(cmErr) {
		t.Fatalf("unexpected error reading the ConfigMap: %v", cmErr)
	}

	got := &openvoxv1alpha1.Config{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: cfg.Name, Namespace: cfg.Namespace}, got); err != nil {
		t.Fatalf("re-reading Config: %v", err)
	}
	if got.Status.Phase == openvoxv1alpha1.ConfigPhaseRunning {
		t.Error("Config was marked Running although rendering failed")
	}
}

// TestReconcileAutosignSecret_TransientListFailure covers the more damaging
// variant: an empty policy set renders as deny-all, so a failed List must not
// be mistaken for "no policies configured" and overwrite a valid Secret.
func TestReconcileAutosignSecret_TransientListFailure(t *testing.T) {
	cfg := newConfig("production", withAuthorityRef("production-ca"))
	ca := newCertificateAuthority("production-ca")
	policy := &openvoxv1alpha1.SigningPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "allow-all", Namespace: testNamespace},
		Spec: openvoxv1alpha1.SigningPolicySpec{
			CertificateAuthorityRef: "production-ca",
			Any:                     true,
		},
	}
	existing := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "production-ca-autosign-policy", Namespace: testNamespace},
		Data:       map[string][]byte{"autosign-policy.yaml": []byte("policies:\n  - any: true\n")},
	}

	c := fake.NewClientBuilder().
		WithScheme(testScheme()).
		WithObjects(cfg, ca, policy, existing).
		WithStatusSubresource(&openvoxv1alpha1.Config{}, &openvoxv1alpha1.SigningPolicy{}).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				if _, ok := list.(*openvoxv1alpha1.SigningPolicyList); ok {
					return serverTimeout("signingpolicies")
				}
				return cl.List(ctx, list, opts...)
			},
		}).
		Build()

	r := newConfigReconciler(c)
	err := r.reconcileAutosignSecrets(testCtx(), cfg)
	if err == nil {
		t.Fatal("expected an error when the SigningPolicy list fails")
	}
	if !strings.Contains(err.Error(), "SigningPolicies") {
		t.Errorf("error should name the failed lookup, got: %v", err)
	}

	got := &corev1.Secret{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: existing.Name, Namespace: testNamespace}, got); err != nil {
		t.Fatalf("reading the policy Secret: %v", err)
	}
	if string(got.Data["autosign-policy.yaml"]) != string(existing.Data["autosign-policy.yaml"]) {
		t.Errorf("the valid policy Secret was overwritten: %s", got.Data["autosign-policy.yaml"])
	}
}

// TestFindCertificateAuthority_NotFoundStaysNil pins the deliberate exception:
// a missing CertificateAuthority is a state the caller renders around, not an
// error.
func TestFindCertificateAuthority_NotFoundStaysNil(t *testing.T) {
	cfg := newConfig("production", withAuthorityRef("missing-ca"))
	r := newConfigReconciler(setupTestClient(cfg))

	ca, err := r.findCertificateAuthority(testCtx(), cfg)
	if err != nil {
		t.Fatalf("a missing CA should not be an error, got: %v", err)
	}
	if ca != nil {
		t.Error("expected nil for a missing CA")
	}
}
