package v1alpha1

import (
	"context"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func validConfig() *Config {
	return &Config{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "test-cfg-",
			Namespace:    "default",
		},
		Spec: ConfigSpec{
			Image: ImageSpec{},
		},
	}
}

func TestConfigValidation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping envtest validation tests in short mode")
	}

	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr string
	}{
		{
			name: "code with image only accepted",
			mutate: func(c *Config) {
				c.Spec.Code = []CodeSpec{{Image: "registry.example.com/code:latest"}}
			},
		},
		{
			name: "code with claimName only accepted",
			mutate: func(c *Config) {
				c.Spec.Code = []CodeSpec{{ClaimName: "my-pvc"}}
			},
		},
		{
			name: "code with both image and claimName rejected",
			mutate: func(c *Config) {
				c.Spec.Code = []CodeSpec{{
					Image:     "registry.example.com/code:latest",
					ClaimName: "my-pvc",
				}}
			},
			wantErr: "image and claimName are mutually exclusive",
		},
		{
			name: "code with neither image nor claimName rejected",
			mutate: func(c *Config) {
				c.Spec.Code = []CodeSpec{{}}
			},
			wantErr: "either image or claimName must be set",
		},
		{
			name: "code entry with environment accepted",
			mutate: func(c *Config) {
				c.Spec.Code = []CodeSpec{{Image: "registry.example.com/code:latest", Environment: "production"}}
			},
		},
		{
			name: "code entry with mountPath under codedir accepted",
			mutate: func(c *Config) {
				c.Spec.Code = []CodeSpec{{Image: "registry.example.com/code:latest", MountPath: "/etc/puppetlabs/code/modules"}}
			},
		},
		{
			name: "code entry with environment and mountPath rejected",
			mutate: func(c *Config) {
				c.Spec.Code = []CodeSpec{{
					Image:       "registry.example.com/code:latest",
					Environment: "production",
					MountPath:   "/etc/puppetlabs/code/modules",
				}}
			},
			wantErr: "environment and mountPath are mutually exclusive",
		},
		{
			name: "code entry with mountPath outside codedir rejected",
			mutate: func(c *Config) {
				c.Spec.Code = []CodeSpec{{Image: "registry.example.com/code:latest", MountPath: "/opt/puppet/code"}}
			},
			wantErr: "mountPath must be under the Puppet codedir",
		},
		{
			name: "multiple code entries with targets accepted",
			mutate: func(c *Config) {
				c.Spec.Code = []CodeSpec{
					{Image: "registry.example.com/prod:latest", Environment: "production"},
					{ClaimName: "modules", MountPath: "/etc/puppetlabs/code/modules"},
				}
			},
		},
		{
			name: "multiple code entries without targets rejected",
			mutate: func(c *Config) {
				c.Spec.Code = []CodeSpec{
					{Image: "registry.example.com/a:latest"},
					{Image: "registry.example.com/b:latest"},
				}
			},
			wantErr: "each entry must set either environment or mountPath",
		},
		{
			name: "valid compileMode jit",
			mutate: func(c *Config) {
				c.Spec.PuppetServer.CompileMode = "jit"
			},
		},
		{
			name: "invalid compileMode rejected",
			mutate: func(c *Config) {
				c.Spec.PuppetServer.CompileMode = "aot"
			},
			wantErr: "spec.puppetserver.compileMode",
		},
		{
			name: "valid clientAuth need",
			mutate: func(c *Config) {
				c.Spec.PuppetServer.ClientAuth = "need"
			},
		},
		{
			name: "invalid clientAuth rejected",
			mutate: func(c *Config) {
				c.Spec.PuppetServer.ClientAuth = "require"
			},
			wantErr: "spec.puppetserver.clientAuth",
		},
		{
			name: "valid logging level DEBUG",
			mutate: func(c *Config) {
				c.Spec.Logging = &LoggingSpec{Level: "DEBUG"}
			},
		},
		{
			name: "invalid logging level rejected",
			mutate: func(c *Config) {
				c.Spec.Logging = &LoggingSpec{Level: "VERBOSE"}
			},
			wantErr: "spec.logging.level",
		},
		{
			name: "valid matchRequest type regex",
			mutate: func(c *Config) {
				c.Spec.PuppetServer.AuthorizationRules = []AuthorizationRule{
					{
						Name: "test-rule",
						MatchRequest: AuthorizationMatchRequest{
							Path: "/test",
							Type: "regex",
						},
						Allow: "*",
					},
				}
			},
		},
		{
			name: "invalid matchRequest type rejected",
			mutate: func(c *Config) {
				c.Spec.PuppetServer.AuthorizationRules = []AuthorizationRule{
					{
						Name: "test-rule",
						MatchRequest: AuthorizationMatchRequest{
							Path: "/test",
							Type: "glob",
						},
						Allow: "*",
					},
				}
			},
			wantErr: "type",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			obj := validConfig()
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
