package controller

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

func TestRenderAutosignPolicyConfig(t *testing.T) {
	tests := []struct {
		name     string
		policies []openvoxv1alpha1.SigningPolicy
		objs     []client.Object
		contains []string
		excludes []string
	}{
		{
			name:     "empty policies",
			policies: nil,
			contains: []string{"policies:\n"},
		},
		{
			name: "any: true",
			policies: []openvoxv1alpha1.SigningPolicy{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "allow-all", Namespace: testNamespace},
					Spec: openvoxv1alpha1.SigningPolicySpec{
						CertificateAuthorityRef: "ca",
						Any:                     true,
					},
				},
			},
			contains: []string{
				"- name: allow-all",
				"any: true",
			},
		},
		{
			name: "CSR attribute with inline value",
			policies: []openvoxv1alpha1.SigningPolicy{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "psk-policy", Namespace: testNamespace},
					Spec: openvoxv1alpha1.SigningPolicySpec{
						CertificateAuthorityRef: "ca",
						CSRAttributes: []openvoxv1alpha1.CSRAttributeMatch{
							{Name: "pp_preshared_key", Value: "my-key-value"},
						},
					},
				},
			},
			contains: []string{
				"- name: psk-policy",
				"csrAttributes:",
				"- name: pp_preshared_key",
				`value: "my-key-value"`,
			},
		},
		{
			name: "CSR attribute with secretKeyRef",
			policies: []openvoxv1alpha1.SigningPolicy{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "secret-policy", Namespace: testNamespace},
					Spec: openvoxv1alpha1.SigningPolicySpec{
						CertificateAuthorityRef: "ca",
						CSRAttributes: []openvoxv1alpha1.CSRAttributeMatch{
							{
								Name: "pp_preshared_key",
								ValueFrom: &openvoxv1alpha1.SecretKeySelector{
									SecretKeyRef: openvoxv1alpha1.SecretKeyRef{
										Name: "psk-secret",
										Key:  "key",
									},
								},
							},
						},
					},
				},
			},
			objs: []client.Object{
				newSecret("psk-secret", map[string][]byte{"key": []byte("secret-value")}),
			},
			contains: []string{
				"- name: pp_preshared_key",
				`value: "secret-value"`,
			},
		},
		{
			name: "multiple policies sorted by name",
			policies: []openvoxv1alpha1.SigningPolicy{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "zebra", Namespace: testNamespace},
					Spec: openvoxv1alpha1.SigningPolicySpec{
						CertificateAuthorityRef: "ca",
						Any:                     true,
					},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: testNamespace},
					Spec: openvoxv1alpha1.SigningPolicySpec{
						CertificateAuthorityRef: "ca",
						Any:                     true,
					},
				},
			},
			contains: []string{
				"- name: alpha",
				"- name: zebra",
			},
		},
		{
			name: "pattern spec",
			policies: []openvoxv1alpha1.SigningPolicy{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "pattern-policy", Namespace: testNamespace},
					Spec: openvoxv1alpha1.SigningPolicySpec{
						CertificateAuthorityRef: "ca",
						Pattern: &openvoxv1alpha1.PatternSpec{
							Allow: []string{"*.example.com", "web-*"},
						},
					},
				},
			},
			contains: []string{
				"pattern:",
				"allow:",
				`"*.example.com"`,
				`"web-*"`,
			},
		},
		{
			name: "dnsAltNames spec",
			policies: []openvoxv1alpha1.SigningPolicy{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "dns-policy", Namespace: testNamespace},
					Spec: openvoxv1alpha1.SigningPolicySpec{
						CertificateAuthorityRef: "ca",
						DNSAltNames: &openvoxv1alpha1.PatternSpec{
							Allow: []string{"puppet", "puppet.local"},
						},
					},
				},
			},
			contains: []string{
				"dnsAltNames:",
				"allow:",
				`"puppet"`,
				`"puppet.local"`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := setupTestClient(tt.objs...)
			r := newConfigReconciler(c)

			out, err := r.renderAutosignPolicyConfig(testCtx(), testNamespace, tt.policies)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			for _, s := range tt.contains {
				if !strings.Contains(out, s) {
					t.Errorf("expected %q in output:\n%s", s, out)
				}
			}
			for _, s := range tt.excludes {
				if strings.Contains(out, s) {
					t.Errorf("unexpected %q in output:\n%s", s, out)
				}
			}
		})
	}
}

func TestRenderAutosignPolicyConfig_SortOrder(t *testing.T) {
	c := setupTestClient()
	r := newConfigReconciler(c)

	policies := []openvoxv1alpha1.SigningPolicy{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "charlie", Namespace: testNamespace},
			Spec:       openvoxv1alpha1.SigningPolicySpec{CertificateAuthorityRef: "ca", Any: true},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "alpha", Namespace: testNamespace},
			Spec:       openvoxv1alpha1.SigningPolicySpec{CertificateAuthorityRef: "ca", Any: true},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "bravo", Namespace: testNamespace},
			Spec:       openvoxv1alpha1.SigningPolicySpec{CertificateAuthorityRef: "ca", Any: true},
		},
	}

	out, err := r.renderAutosignPolicyConfig(testCtx(), testNamespace, policies)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	alphaIdx := strings.Index(out, "- name: alpha")
	bravoIdx := strings.Index(out, "- name: bravo")
	charlieIdx := strings.Index(out, "- name: charlie")

	if alphaIdx < 0 || bravoIdx < 0 || charlieIdx < 0 {
		t.Fatalf("not all policies found in output:\n%s", out)
	}

	if alphaIdx >= bravoIdx || bravoIdx >= charlieIdx {
		t.Errorf("policies not sorted by name; alpha=%d, bravo=%d, charlie=%d", alphaIdx, bravoIdx, charlieIdx)
	}
}
