package v1alpha1

import (
	"context"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func validNodeClassifier() *NodeClassifier {
	return &NodeClassifier{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-nc-",
			Namespace:    "default",
		},
		Spec: NodeClassifierSpec{
			URL: "https://example.com/enc",
			Request: NodeClassifierRequest{
				Method: "GET",
				Path:   "/node/{certname}",
			},
			Response: NodeClassifierResponse{
				Format: "yaml",
			},
		},
	}
}

func TestNodeClassifierValidation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping envtest validation tests in short mode")
	}

	tests := []struct {
		name    string
		mutate  func(*NodeClassifier)
		wantErr string
	}{
		{
			name:   "valid GET request",
			mutate: func(_ *NodeClassifier) {},
		},
		{
			name: "valid POST with body facts",
			mutate: func(nc *NodeClassifier) {
				nc.Spec.Request.Method = "POST"
				nc.Spec.Request.Body = "facts"
			},
		},
		{
			name: "valid POST with body certname",
			mutate: func(nc *NodeClassifier) {
				nc.Spec.Request.Method = "POST"
				nc.Spec.Request.Body = "certname"
			},
		},
		{
			name: "invalid method PUT rejected",
			mutate: func(nc *NodeClassifier) {
				nc.Spec.Request.Method = "PUT"
			},
			wantErr: "method must be GET or POST",
		},
		{
			name: "GET with body rejected",
			mutate: func(nc *NodeClassifier) {
				nc.Spec.Request.Method = "GET"
				nc.Spec.Request.Body = "facts"
			},
			wantErr: "body is only allowed with POST method",
		},
		{
			name: "POST without body accepted",
			mutate: func(nc *NodeClassifier) {
				nc.Spec.Request.Method = "POST"
			},
		},
		{
			name: "valid response format json",
			mutate: func(nc *NodeClassifier) {
				nc.Spec.Response.Format = "json"
			},
		},
		{
			name: "invalid response format rejected",
			mutate: func(nc *NodeClassifier) {
				nc.Spec.Response.Format = "xml"
			},
			wantErr: "spec.response.format",
		},
		{
			name: "invalid body value rejected",
			mutate: func(nc *NodeClassifier) {
				nc.Spec.Request.Method = "POST"
				nc.Spec.Request.Body = "invalid"
			},
			wantErr: "spec.request.body",
		},
		{
			name: "auth with single method accepted",
			mutate: func(nc *NodeClassifier) {
				nc.Spec.Auth = &NodeClassifierAuth{MTLS: true}
			},
		},
		{
			name: "auth with two methods rejected",
			mutate: func(nc *NodeClassifier) {
				nc.Spec.Auth = &NodeClassifierAuth{
					MTLS: true,
					Token: &TokenAuth{
						Header:       "X-Auth",
						SecretKeyRef: SecretKeyRef{Name: "my-secret", Key: "token"},
					},
				}
			},
			wantErr: "at most one auth method may be set",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := validNodeClassifier()
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
