package webhook

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

func TestSigningPolicyValidator_Update(t *testing.T) {
	ca := &openvoxv1alpha1.CertificateAuthority{
		ObjectMeta: metav1.ObjectMeta{Name: "my-ca", Namespace: "default"},
	}
	c := setupTestClient(ca)
	v := &SigningPolicyValidator{Client: c}

	valid := &openvoxv1alpha1.SigningPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec:       openvoxv1alpha1.SigningPolicySpec{CertificateAuthorityRef: "my-ca"},
	}
	invalid := &openvoxv1alpha1.SigningPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec:       openvoxv1alpha1.SigningPolicySpec{CertificateAuthorityRef: "missing-ca"},
	}

	if _, err := v.ValidateUpdate(context.Background(), nil, valid); err != nil {
		t.Errorf("expected no error for valid update, got %v", err)
	}
	if _, err := v.ValidateUpdate(context.Background(), nil, invalid); err == nil {
		t.Error("expected error for missing CA ref update")
	}
}

func TestSigningPolicyValidator(t *testing.T) {
	ca := &openvoxv1alpha1.CertificateAuthority{
		ObjectMeta: metav1.ObjectMeta{Name: "my-ca", Namespace: "default"},
	}

	t.Run("valid signing policy", func(t *testing.T) {
		c := setupTestClient(ca)
		v := &SigningPolicyValidator{Client: c}
		sp := &openvoxv1alpha1.SigningPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.SigningPolicySpec{
				CertificateAuthorityRef: "my-ca",
				Pattern: &openvoxv1alpha1.PatternSpec{
					Allow: []string{"*.example.com"},
				},
			},
		}
		_, err := v.ValidateCreate(context.Background(), sp)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("missing CA ref", func(t *testing.T) {
		c := setupTestClient()
		v := &SigningPolicyValidator{Client: c}
		sp := &openvoxv1alpha1.SigningPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.SigningPolicySpec{
				CertificateAuthorityRef: "missing-ca",
			},
		}
		_, err := v.ValidateCreate(context.Background(), sp)
		if err == nil {
			t.Error("expected error for missing CA ref")
		}
	})

	t.Run("empty pattern in allow list", func(t *testing.T) {
		c := setupTestClient(ca)
		v := &SigningPolicyValidator{Client: c}
		sp := &openvoxv1alpha1.SigningPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.SigningPolicySpec{
				CertificateAuthorityRef: "my-ca",
				Pattern: &openvoxv1alpha1.PatternSpec{
					Allow: []string{"*.example.com", ""},
				},
			},
		}
		_, err := v.ValidateCreate(context.Background(), sp)
		if err == nil {
			t.Error("expected error for empty pattern")
		}
	})

	t.Run("empty dnsAltNames pattern", func(t *testing.T) {
		c := setupTestClient(ca)
		v := &SigningPolicyValidator{Client: c}
		sp := &openvoxv1alpha1.SigningPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.SigningPolicySpec{
				CertificateAuthorityRef: "my-ca",
				DNSAltNames: &openvoxv1alpha1.PatternSpec{
					Allow: []string{""},
				},
			},
		}
		_, err := v.ValidateCreate(context.Background(), sp)
		if err == nil {
			t.Error("expected error for empty dnsAltNames pattern")
		}
	})

	t.Run("valid csrAttribute with known OID", func(t *testing.T) {
		c := setupTestClient(ca)
		v := &SigningPolicyValidator{Client: c}
		sp := &openvoxv1alpha1.SigningPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.SigningPolicySpec{
				CertificateAuthorityRef: "my-ca",
				CSRAttributes: []openvoxv1alpha1.CSRAttributeMatch{
					{Name: "pp_preshared_key", Value: "secret"},
				},
			},
		}
		if _, err := v.ValidateCreate(context.Background(), sp); err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("csrAttribute name with YAML injection is rejected", func(t *testing.T) {
		c := setupTestClient(ca)
		v := &SigningPolicyValidator{Client: c}
		sp := &openvoxv1alpha1.SigningPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.SigningPolicySpec{
				CertificateAuthorityRef: "my-ca",
				CSRAttributes: []openvoxv1alpha1.CSRAttributeMatch{
					{Name: "x\n    any: true", Value: "foo"},
				},
			},
		}
		if _, err := v.ValidateCreate(context.Background(), sp); err == nil {
			t.Error("expected error for injection attribute name")
		}
	})

	t.Run("csrAttribute with empty name is rejected", func(t *testing.T) {
		c := setupTestClient(ca)
		v := &SigningPolicyValidator{Client: c}
		sp := &openvoxv1alpha1.SigningPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.SigningPolicySpec{
				CertificateAuthorityRef: "my-ca",
				CSRAttributes: []openvoxv1alpha1.CSRAttributeMatch{
					{Name: "", Value: "foo"},
				},
			},
		}
		if _, err := v.ValidateCreate(context.Background(), sp); err == nil {
			t.Error("expected error for empty attribute name")
		}
	})

	t.Run("valid ipAltNames CIDR and extensions", func(t *testing.T) {
		c := setupTestClient(ca)
		v := &SigningPolicyValidator{Client: c}
		sp := &openvoxv1alpha1.SigningPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.SigningPolicySpec{
				CertificateAuthorityRef: "my-ca",
				IPAltNames:              &openvoxv1alpha1.PatternSpec{Allow: []string{"10.0.0.0/16", "::1/128"}},
				Extensions:              &openvoxv1alpha1.PatternSpec{Allow: []string{"pp_cli_auth"}},
			},
		}
		if _, err := v.ValidateCreate(context.Background(), sp); err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("invalid ipAltNames CIDR is rejected", func(t *testing.T) {
		c := setupTestClient(ca)
		v := &SigningPolicyValidator{Client: c}
		sp := &openvoxv1alpha1.SigningPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.SigningPolicySpec{
				CertificateAuthorityRef: "my-ca",
				IPAltNames:              &openvoxv1alpha1.PatternSpec{Allow: []string{"10.0.0.5"}}, // not a CIDR
			},
		}
		if _, err := v.ValidateCreate(context.Background(), sp); err == nil {
			t.Error("expected error for non-CIDR ipAltNames entry")
		}
	})

	t.Run("unknown extension name is rejected", func(t *testing.T) {
		c := setupTestClient(ca)
		v := &SigningPolicyValidator{Client: c}
		sp := &openvoxv1alpha1.SigningPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.SigningPolicySpec{
				CertificateAuthorityRef: "my-ca",
				Extensions:              &openvoxv1alpha1.PatternSpec{Allow: []string{"not_a_real_extension"}},
			},
		}
		if _, err := v.ValidateCreate(context.Background(), sp); err == nil {
			t.Error("expected error for unknown extension name")
		}
	})

	t.Run("delete always succeeds", func(t *testing.T) {
		v := &SigningPolicyValidator{Client: setupTestClient()}
		_, err := v.ValidateDelete(context.Background(), &openvoxv1alpha1.SigningPolicy{})
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})
}
