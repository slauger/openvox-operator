package controller

import (
	"strings"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

func TestRenderReportWebhookConfig(t *testing.T) {
	tests := []struct {
		name       string
		processors []openvoxv1alpha1.ReportProcessor
		objs       []client.Object
		contains   []string
		excludes   []string
	}{
		{
			name: "single endpoint no auth",
			processors: []openvoxv1alpha1.ReportProcessor{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "webhook1", Namespace: testNamespace},
					Spec: openvoxv1alpha1.ReportProcessorSpec{
						ConfigRef:      "production",
						URL:            "https://reports.example.com/webhook",
						TimeoutSeconds: 15,
					},
				},
			},
			contains: []string{
				"endpoints:",
				"name: webhook1",
				"url: https://reports.example.com/webhook",
				"timeoutSeconds: 15",
				"certFile:",
				"keyFile:",
				"caFile:",
			},
			excludes: []string{"auth:"},
		},
		{
			name: "default timeout",
			processors: []openvoxv1alpha1.ReportProcessor{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "webhook1", Namespace: testNamespace},
					Spec: openvoxv1alpha1.ReportProcessorSpec{
						ConfigRef: "production",
						URL:       "https://reports.example.com/webhook",
					},
				},
			},
			contains: []string{"timeoutSeconds: 30"},
		},
		{
			name: "mTLS auth",
			processors: []openvoxv1alpha1.ReportProcessor{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "mtls-ep", Namespace: testNamespace},
					Spec: openvoxv1alpha1.ReportProcessorSpec{
						ConfigRef: "production",
						URL:       "https://reports.example.com",
						Auth:      &openvoxv1alpha1.ReportProcessorAuth{MTLS: true},
					},
				},
			},
			contains: []string{
				"auth:",
				"type: mtls",
			},
		},
		{
			name: "bearer auth",
			processors: []openvoxv1alpha1.ReportProcessor{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "bearer-ep", Namespace: testNamespace},
					Spec: openvoxv1alpha1.ReportProcessorSpec{
						ConfigRef: "production",
						URL:       "https://reports.example.com",
						Auth: &openvoxv1alpha1.ReportProcessorAuth{
							Bearer: &openvoxv1alpha1.SecretKeySelector{
								SecretKeyRef: openvoxv1alpha1.SecretKeyRef{
									Name: "bearer-secret",
									Key:  "token",
								},
							},
						},
					},
				},
			},
			objs: []client.Object{
				newSecret("bearer-secret", map[string][]byte{"token": []byte("bearer-xyz")}),
			},
			contains: []string{
				"type: bearer",
				"token: bearer-xyz",
			},
		},
		{
			name: "processor field set",
			processors: []openvoxv1alpha1.ReportProcessor{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "pdb-ep", Namespace: testNamespace},
					Spec: openvoxv1alpha1.ReportProcessorSpec{
						ConfigRef: "production",
						Processor: "puppetdb",
						URL:       "https://pdb.example.com",
					},
				},
			},
			contains: []string{"processor: puppetdb"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := setupTestClient(tt.objs...)
			r := newConfigReconciler(c)

			out, err := r.renderReportWebhookConfig(testCtx(), testNamespace, tt.processors)
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

func TestRenderReportWebhookConfig_HeaderStaticValue(t *testing.T) {
	c := setupTestClient()
	r := newConfigReconciler(c)

	processors := []openvoxv1alpha1.ReportProcessor{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "hdr-ep", Namespace: testNamespace},
			Spec: openvoxv1alpha1.ReportProcessorSpec{
				ConfigRef: "production",
				URL:       "https://reports.example.com",
				Headers: []openvoxv1alpha1.HTTPHeader{
					{Name: "X-Custom", Value: "static-val"},
				},
			},
		},
	}

	out, err := r.renderReportWebhookConfig(testCtx(), testNamespace, processors)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, s := range []string{
		"headers:",
		"name: X-Custom",
		"value: static-val",
	} {
		if !strings.Contains(out, s) {
			t.Errorf("expected %q in output:\n%s", s, out)
		}
	}
}

func TestRenderReportWebhookConfig_HeaderFromSecret(t *testing.T) {
	secret := newSecret("header-secret", map[string][]byte{"api-key": []byte("secret-key-val")})
	c := setupTestClient(secret)
	r := newConfigReconciler(c)

	processors := []openvoxv1alpha1.ReportProcessor{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "hdr-ep", Namespace: testNamespace},
			Spec: openvoxv1alpha1.ReportProcessorSpec{
				ConfigRef: "production",
				URL:       "https://reports.example.com",
				Headers: []openvoxv1alpha1.HTTPHeader{
					{
						Name: "X-API-Key",
						ValueFrom: &openvoxv1alpha1.HTTPHeaderValueFrom{
							SecretKeyRef: &openvoxv1alpha1.SecretKeyRef{
								Name: "header-secret",
								Key:  "api-key",
							},
						},
					},
				},
			},
		},
	}

	out, err := r.renderReportWebhookConfig(testCtx(), testNamespace, processors)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, s := range []string{
		"name: X-API-Key",
		"value: secret-key-val",
	} {
		if !strings.Contains(out, s) {
			t.Errorf("expected %q in output:\n%s", s, out)
		}
	}
}

func TestRenderReportWebhookConfig_HeaderFromConfigMap(t *testing.T) {
	cm := newConfigMap("header-cm", map[string]string{"env": "production"})
	c := setupTestClient(cm)
	r := newConfigReconciler(c)

	processors := []openvoxv1alpha1.ReportProcessor{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "hdr-ep", Namespace: testNamespace},
			Spec: openvoxv1alpha1.ReportProcessorSpec{
				ConfigRef: "production",
				URL:       "https://reports.example.com",
				Headers: []openvoxv1alpha1.HTTPHeader{
					{
						Name: "X-Environment",
						ValueFrom: &openvoxv1alpha1.HTTPHeaderValueFrom{
							ConfigMapKeyRef: &openvoxv1alpha1.ConfigMapKeyRef{
								Name: "header-cm",
								Key:  "env",
							},
						},
					},
				},
			},
		},
	}

	out, err := r.renderReportWebhookConfig(testCtx(), testNamespace, processors)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, s := range []string{
		"name: X-Environment",
		"value: production",
	} {
		if !strings.Contains(out, s) {
			t.Errorf("expected %q in output:\n%s", s, out)
		}
	}
}

func TestRenderReportWebhookConfig_MultipleEndpoints(t *testing.T) {
	bearerSecret := newSecret("bearer-secret", map[string][]byte{"token": []byte("tok123")})
	c := setupTestClient(bearerSecret)
	r := newConfigReconciler(c)

	processors := []openvoxv1alpha1.ReportProcessor{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "ep-mtls", Namespace: testNamespace},
			Spec: openvoxv1alpha1.ReportProcessorSpec{
				ConfigRef: "production",
				URL:       "https://mtls.example.com",
				Auth:      &openvoxv1alpha1.ReportProcessorAuth{MTLS: true},
			},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "ep-bearer", Namespace: testNamespace},
			Spec: openvoxv1alpha1.ReportProcessorSpec{
				ConfigRef: "production",
				URL:       "https://bearer.example.com",
				Auth: &openvoxv1alpha1.ReportProcessorAuth{
					Bearer: &openvoxv1alpha1.SecretKeySelector{
						SecretKeyRef: openvoxv1alpha1.SecretKeyRef{
							Name: "bearer-secret",
							Key:  "token",
						},
					},
				},
			},
		},
	}

	out, err := r.renderReportWebhookConfig(testCtx(), testNamespace, processors)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, s := range []string{
		"name: ep-mtls",
		"type: mtls",
		"name: ep-bearer",
		"type: bearer",
		"token: tok123",
	} {
		if !strings.Contains(out, s) {
			t.Errorf("expected %q in output:\n%s", s, out)
		}
	}
}
