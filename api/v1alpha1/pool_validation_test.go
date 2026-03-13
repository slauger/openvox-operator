package v1alpha1

import (
	"context"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func validPool() *Pool {
	return &Pool{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-pool-",
			Namespace:    "default",
		},
		Spec: PoolSpec{},
	}
}

func TestPoolValidation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping envtest validation tests in short mode")
	}

	tests := []struct {
		name    string
		mutate  func(*Pool)
		wantErr string
	}{
		{
			name:   "pool without route accepted",
			mutate: func(_ *Pool) {},
		},
		{
			name: "route enabled with hostname and gatewayRef accepted",
			mutate: func(p *Pool) {
				p.Spec.Route = &PoolRouteSpec{
					Enabled:    true,
					Hostname:   "puppet.example.com",
					GatewayRef: GatewayReference{Name: "my-gateway"},
				}
			},
		},
		{
			name: "route enabled without hostname rejected",
			mutate: func(p *Pool) {
				p.Spec.Route = &PoolRouteSpec{
					Enabled:    true,
					GatewayRef: GatewayReference{Name: "my-gateway"},
				}
			},
			wantErr: "hostname is required when route is enabled",
		},
		{
			name: "route enabled without gatewayRef name rejected",
			mutate: func(p *Pool) {
				p.Spec.Route = &PoolRouteSpec{
					Enabled:  true,
					Hostname: "puppet.example.com",
				}
			},
			wantErr: "gatewayRef.name is required when route is enabled",
		},
		{
			name: "route disabled without hostname accepted",
			mutate: func(p *Pool) {
				p.Spec.Route = &PoolRouteSpec{
					Enabled: false,
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := validPool()
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
