package controller

import (
	"strings"
	"testing"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

func TestRenderENCConfig(t *testing.T) {
	tests := []struct {
		name     string
		nc       *openvoxv1alpha1.NodeClassifier
		contains []string
		excludes []string
	}{
		{
			name: "no auth",
			nc: &openvoxv1alpha1.NodeClassifier{
				Spec: openvoxv1alpha1.NodeClassifierSpec{
					URL:            "https://enc.example.com",
					Request:        openvoxv1alpha1.NodeClassifierRequest{Method: "GET", Path: "/node/{certname}"},
					Response:       openvoxv1alpha1.NodeClassifierResponse{Format: "yaml"},
					TimeoutSeconds: 15,
				},
			},
			contains: []string{
				"url: https://enc.example.com",
				"method: GET",
				"path: /node/{certname}",
				"responseFormat: yaml",
				"timeoutSeconds: 15",
				"certFile:",
				"keyFile:",
				"caFile:",
			},
			excludes: []string{"auth:"},
		},
		{
			name: "default timeout",
			nc: &openvoxv1alpha1.NodeClassifier{
				Spec: openvoxv1alpha1.NodeClassifierSpec{
					URL:      "https://enc.example.com",
					Request:  openvoxv1alpha1.NodeClassifierRequest{Method: "GET", Path: "/node/{certname}"},
					Response: openvoxv1alpha1.NodeClassifierResponse{Format: "yaml"},
				},
			},
			contains: []string{"timeoutSeconds: 10"},
		},
		{
			name: "mTLS auth",
			nc: func() *openvoxv1alpha1.NodeClassifier {
				nc := newNodeClassifier("enc", "https://enc.example.com")
				nc.Spec.Auth = &openvoxv1alpha1.NodeClassifierAuth{MTLS: true}
				return nc
			}(),
			contains: []string{
				"auth:",
				"type: mtls",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := setupTestClient()
			r := newConfigReconciler(c)
			cfg := newConfig("test")

			// Use the provided NC or default
			nc := tt.nc
			if nc.Name == "" {
				nc.Name = "enc"
				nc.Namespace = testNamespace
			}

			out, err := r.renderENCConfig(testCtx(), cfg, nc)
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

func TestRenderENCConfig_TokenAuth(t *testing.T) {
	tokenSecret := newSecret("enc-token", map[string][]byte{
		"token": []byte("my-secret-token"),
	})

	c := setupTestClient(tokenSecret)
	r := newConfigReconciler(c)
	cfg := newConfig("test")

	nc := newNodeClassifier("enc", "https://enc.example.com")
	nc.Spec.Auth = &openvoxv1alpha1.NodeClassifierAuth{
		Token: &openvoxv1alpha1.TokenAuth{
			Header: "X-Auth-Token",
			SecretKeyRef: openvoxv1alpha1.SecretKeyRef{
				Name: "enc-token",
				Key:  "token",
			},
		},
	}

	out, err := r.renderENCConfig(testCtx(), cfg, nc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, s := range []string{
		"type: token",
		"header: X-Auth-Token",
		"token: my-secret-token",
	} {
		if !strings.Contains(out, s) {
			t.Errorf("expected %q in output:\n%s", s, out)
		}
	}
}

func TestRenderENCConfig_BearerAuth(t *testing.T) {
	bearerSecret := newSecret("bearer-secret", map[string][]byte{
		"token": []byte("bearer-value"),
	})

	c := setupTestClient(bearerSecret)
	r := newConfigReconciler(c)
	cfg := newConfig("test")

	nc := newNodeClassifier("enc", "https://enc.example.com")
	nc.Spec.Auth = &openvoxv1alpha1.NodeClassifierAuth{
		Bearer: &openvoxv1alpha1.SecretKeySelector{
			SecretKeyRef: openvoxv1alpha1.SecretKeyRef{
				Name: "bearer-secret",
				Key:  "token",
			},
		},
	}

	out, err := r.renderENCConfig(testCtx(), cfg, nc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, s := range []string{
		"type: bearer",
		"token: bearer-value",
	} {
		if !strings.Contains(out, s) {
			t.Errorf("expected %q in output:\n%s", s, out)
		}
	}
}

func TestRenderENCConfig_BasicAuth(t *testing.T) {
	basicSecret := newSecret("basic-creds", map[string][]byte{
		"user": []byte("admin"),
		"pass": []byte("s3cret"),
	})

	c := setupTestClient(basicSecret)
	r := newConfigReconciler(c)
	cfg := newConfig("test")

	nc := newNodeClassifier("enc", "https://enc.example.com")
	nc.Spec.Auth = &openvoxv1alpha1.NodeClassifierAuth{
		Basic: &openvoxv1alpha1.BasicAuth{
			SecretRef: openvoxv1alpha1.BasicAuthSecretRef{
				Name:        "basic-creds",
				UsernameKey: "user",
				PasswordKey: "pass",
			},
		},
	}

	out, err := r.renderENCConfig(testCtx(), cfg, nc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, s := range []string{
		"type: basic",
		"username: admin",
		"password: s3cret",
	} {
		if !strings.Contains(out, s) {
			t.Errorf("expected %q in output:\n%s", s, out)
		}
	}
}

func TestRenderENCConfig_CacheEnabled(t *testing.T) {
	c := setupTestClient()
	r := newConfigReconciler(c)
	cfg := newConfig("test")

	nc := newNodeClassifier("enc", "https://enc.example.com")
	nc.Spec.Cache = &openvoxv1alpha1.NodeClassifierCache{
		Enabled: true,
	}

	out, err := r.renderENCConfig(testCtx(), cfg, nc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, s := range []string{
		"cache:",
		"enabled: true",
		"directory: /var/cache/openvox-enc",
	} {
		if !strings.Contains(out, s) {
			t.Errorf("expected %q in output:\n%s", s, out)
		}
	}
}

func TestRenderENCConfig_CacheDisabled(t *testing.T) {
	c := setupTestClient()
	r := newConfigReconciler(c)
	cfg := newConfig("test")

	nc := newNodeClassifier("enc", "https://enc.example.com")
	// Cache nil = no cache section

	out, err := r.renderENCConfig(testCtx(), cfg, nc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(out, "cache:") {
		t.Errorf("unexpected cache section in output:\n%s", out)
	}
}

func TestRenderENCConfig_CacheCustomDirectory(t *testing.T) {
	c := setupTestClient()
	r := newConfigReconciler(c)
	cfg := newConfig("test")

	nc := newNodeClassifier("enc", "https://enc.example.com")
	nc.Spec.Cache = &openvoxv1alpha1.NodeClassifierCache{
		Enabled:   true,
		Directory: "/custom/cache/dir",
	}

	out, err := r.renderENCConfig(testCtx(), cfg, nc)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(out, "directory: /custom/cache/dir") {
		t.Errorf("expected custom cache directory in output:\n%s", out)
	}
}
