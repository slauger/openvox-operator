package v1alpha1

import (
	"context"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func validReportProcessor() *ReportProcessor {
	return &ReportProcessor{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-rp-",
			Namespace:    "default",
		},
		Spec: ReportProcessorSpec{
			ConfigRef: "my-config",
			URL:       "https://example.com/report",
		},
	}
}

func TestReportProcessorValidation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping envtest validation tests in short mode")
	}

	tests := []struct {
		name    string
		mutate  func(*ReportProcessor)
		wantErr string
	}{
		{
			name:   "valid minimal spec",
			mutate: func(_ *ReportProcessor) {},
		},
		{
			name: "valid with processor puppetdb",
			mutate: func(rp *ReportProcessor) {
				rp.Spec.Processor = "puppetdb"
			},
		},
		{
			name: "empty configRef rejected",
			mutate: func(rp *ReportProcessor) {
				rp.Spec.ConfigRef = ""
			},
			wantErr: "configRef",
		},
		{
			name: "invalid processor rejected",
			mutate: func(rp *ReportProcessor) {
				rp.Spec.Processor = "invalid"
			},
			wantErr: "spec.processor",
		},
		{
			name: "auth with mtls accepted",
			mutate: func(rp *ReportProcessor) {
				rp.Spec.Auth = &ReportProcessorAuth{MTLS: true}
			},
		},
		{
			name: "auth with token accepted",
			mutate: func(rp *ReportProcessor) {
				rp.Spec.Auth = &ReportProcessorAuth{
					Token: &TokenAuth{
						Header:       "X-Auth",
						SecretKeyRef: SecretKeyRef{Name: "my-secret", Key: "token"},
					},
				}
			},
		},
		{
			name: "auth with bearer accepted",
			mutate: func(rp *ReportProcessor) {
				rp.Spec.Auth = &ReportProcessorAuth{
					Bearer: &SecretKeySelector{
						SecretKeyRef: SecretKeyRef{Name: "my-secret", Key: "token"},
					},
				}
			},
		},
		{
			name: "auth with basic accepted",
			mutate: func(rp *ReportProcessor) {
				rp.Spec.Auth = &ReportProcessorAuth{
					Basic: &BasicAuth{
						SecretRef: BasicAuthSecretRef{Name: "my-secret"},
					},
				}
			},
		},
		{
			name: "auth with two methods rejected",
			mutate: func(rp *ReportProcessor) {
				rp.Spec.Auth = &ReportProcessorAuth{
					MTLS: true,
					Token: &TokenAuth{
						Header:       "X-Auth",
						SecretKeyRef: SecretKeyRef{Name: "my-secret", Key: "token"},
					},
				}
			},
			wantErr: "at most one auth method may be set",
		},
		{
			name: "header with value only accepted",
			mutate: func(rp *ReportProcessor) {
				rp.Spec.Headers = []HTTPHeader{
					{Name: "X-Custom", Value: "val"},
				}
			},
		},
		{
			name: "header with valueFrom only accepted",
			mutate: func(rp *ReportProcessor) {
				rp.Spec.Headers = []HTTPHeader{
					{
						Name: "X-Custom",
						ValueFrom: &HTTPHeaderValueFrom{
							SecretKeyRef: &SecretKeyRef{Name: "my-secret", Key: "val"},
						},
					},
				}
			},
		},
		{
			name: "header with both value and valueFrom rejected",
			mutate: func(rp *ReportProcessor) {
				rp.Spec.Headers = []HTTPHeader{
					{
						Name:  "X-Custom",
						Value: "literal",
						ValueFrom: &HTTPHeaderValueFrom{
							SecretKeyRef: &SecretKeyRef{Name: "my-secret", Key: "val"},
						},
					},
				}
			},
			wantErr: "value and valueFrom are mutually exclusive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := validReportProcessor()
			tt.mutate(obj)
			ctx := context.Background()
			err := k8sClient.Create(ctx, obj)
			if err == nil {
				defer func() { _ = k8sClient.Delete(ctx, obj) }()
			}

			if tt.wantErr != "" {
				if err == nil {
					t.Fatal("expected error but create succeeded")
				}
				if !apierrors.IsInvalid(err) {
					t.Fatalf("expected Invalid error, got: %v", err)
				}
				if !strings.Contains(err.Error(), tt.wantErr) {
					t.Errorf("expected error containing %q, got: %v", tt.wantErr, err)
				}
			} else if err != nil {
				t.Fatalf("expected success, got: %v", err)
			}
		})
	}
}
