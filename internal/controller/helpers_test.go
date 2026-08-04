package controller

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

func TestConfigMapVolume(t *testing.T) {
	vol := configMapVolume("my-vol", "my-cm", "puppet.conf")

	if vol.Name != "my-vol" {
		t.Errorf("expected volume name %q, got %q", "my-vol", vol.Name)
	}
	if vol.ConfigMap == nil {
		t.Fatal("expected ConfigMap volume source, got nil")
	}
	if vol.ConfigMap.Name != "my-cm" {
		t.Errorf("expected ConfigMap name %q, got %q", "my-cm", vol.ConfigMap.Name)
	}
	if len(vol.ConfigMap.Items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(vol.ConfigMap.Items))
	}
	item := vol.ConfigMap.Items[0]
	if item.Key != "puppet.conf" || item.Path != "puppet.conf" {
		t.Errorf("expected key=path=%q, got key=%q path=%q", "puppet.conf", item.Key, item.Path)
	}
}

func TestConfigMapVolumeWithKey(t *testing.T) {
	vol := configMapVolumeWithKey("my-vol", "my-cm", "data-key", "mounted-path")

	if vol.Name != "my-vol" {
		t.Errorf("expected volume name %q, got %q", "my-vol", vol.Name)
	}
	item := vol.ConfigMap.Items[0]
	if item.Key != "data-key" {
		t.Errorf("expected key %q, got %q", "data-key", item.Key)
	}
	if item.Path != "mounted-path" {
		t.Errorf("expected path %q, got %q", "mounted-path", item.Path)
	}
}

func TestHashStringMap(t *testing.T) {
	// Same data should produce same hash
	data := map[string]string{"a": "1", "b": "2"}
	h1 := hashStringMap(data)
	h2 := hashStringMap(data)
	if h1 != h2 {
		t.Errorf("same data produced different hashes: %q vs %q", h1, h2)
	}

	// Different data should produce different hash
	other := map[string]string{"a": "1", "b": "3"}
	h3 := hashStringMap(other)
	if h1 == h3 {
		t.Errorf("different data produced same hash: %q", h1)
	}

	// Order should not matter (deterministic)
	data2 := map[string]string{"b": "2", "a": "1"}
	h4 := hashStringMap(data2)
	if h1 != h4 {
		t.Errorf("same data in different order produced different hashes: %q vs %q", h1, h4)
	}

	// Empty map
	empty := hashStringMap(map[string]string{})
	if empty == "" {
		t.Error("hash of empty map should not be empty string")
	}
}

func TestResolveImage(t *testing.T) {
	cfg := &openvoxv1alpha1.Config{
		Spec: openvoxv1alpha1.ConfigSpec{
			Image: openvoxv1alpha1.ImageSpec{
				Repository: "ghcr.io/slauger/openvox-server-8",
				Tag:        "latest",
			},
		},
	}

	tests := []struct {
		name   string
		server *openvoxv1alpha1.Server
		want   string
	}{
		{
			name: "config defaults",
			server: &openvoxv1alpha1.Server{
				Spec: openvoxv1alpha1.ServerSpec{},
			},
			want: "ghcr.io/slauger/openvox-server-8:latest",
		},
		{
			name: "server tag override",
			server: &openvoxv1alpha1.Server{
				Spec: openvoxv1alpha1.ServerSpec{
					Image: openvoxv1alpha1.ImageSpec{
						Tag: "v8.12.1",
					},
				},
			},
			want: "ghcr.io/slauger/openvox-server-8:v8.12.1",
		},
		{
			name: "server repository override only",
			server: &openvoxv1alpha1.Server{
				Spec: openvoxv1alpha1.ServerSpec{
					Image: openvoxv1alpha1.ImageSpec{
						Repository: "custom-registry/server",
					},
				},
			},
			want: "custom-registry/server:latest",
		},
		{
			name: "server full override",
			server: &openvoxv1alpha1.Server{
				Spec: openvoxv1alpha1.ServerSpec{
					Image: openvoxv1alpha1.ImageSpec{
						Repository: "custom-registry/server",
						Tag:        "v9.0.0",
					},
				},
			},
			want: "custom-registry/server:v9.0.0",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveImage(tt.server, cfg)
			if got != tt.want {
				t.Errorf("resolveImage() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestResolveCode(t *testing.T) {
	cfgCode := &openvoxv1alpha1.CodeSpec{
		Image: "ghcr.io/slauger/puppet-code:latest",
	}
	cfg := &openvoxv1alpha1.Config{
		Spec: openvoxv1alpha1.ConfigSpec{
			Code: cfgCode,
		},
	}

	// Server without code override -> use config default
	server := &openvoxv1alpha1.Server{
		Spec: openvoxv1alpha1.ServerSpec{},
	}
	got := resolveCode(server, cfg)
	if got != cfgCode {
		t.Error("expected config code spec when server has no override")
	}

	// Server with code override -> use server's
	serverCode := &openvoxv1alpha1.CodeSpec{
		Image: "custom/code:v2",
	}
	server.Spec.Code = serverCode
	got = resolveCode(server, cfg)
	if got != serverCode {
		t.Error("expected server code spec when override is set")
	}
}

func TestInt64Ptr(t *testing.T) {
	val := int64Ptr(42)
	if val == nil || *val != 42 {
		t.Errorf("int64Ptr(42) = %v, want pointer to 42", val)
	}
}

func TestBoolPtr(t *testing.T) {
	val := boolPtr(true)
	if val == nil || !*val {
		t.Errorf("boolPtr(true) = %v, want pointer to true", val)
	}

	val = boolPtr(false)
	if val == nil || *val {
		t.Errorf("boolPtr(false) = %v, want pointer to false", val)
	}
}

func TestUpdateStatusWithRetry(t *testing.T) {
	cfg := &openvoxv1alpha1.Config{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-config",
			Namespace: testNamespace,
		},
	}
	c := setupTestClient(cfg)

	err := updateStatusWithRetry(testCtx(), c, cfg, func() {
		cfg.Status.Phase = openvoxv1alpha1.ConfigPhaseRunning
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify the mutation was applied
	got := &openvoxv1alpha1.Config{}
	if err := c.Get(testCtx(), client.ObjectKeyFromObject(cfg), got); err != nil {
		t.Fatalf("failed to get config: %v", err)
	}
	if got.Status.Phase != openvoxv1alpha1.ConfigPhaseRunning {
		t.Errorf("expected phase %q, got %q", openvoxv1alpha1.ConfigPhaseRunning, got.Status.Phase)
	}
}

func TestUpdateStatusWithRetry_ConflictRetry(t *testing.T) {
	cfg := &openvoxv1alpha1.Config{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-config",
			Namespace: testNamespace,
		},
	}

	var calls atomic.Int32
	c := fake.NewClientBuilder().
		WithScheme(testScheme()).
		WithObjects(cfg).
		WithStatusSubresource(&openvoxv1alpha1.Config{}).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourceUpdate: func(ctx context.Context, client client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
				if calls.Add(1) == 1 {
					return errors.NewConflict(schema.GroupResource{Group: "openvox.voxpupuli.org", Resource: "configs"}, obj.GetName(), fmt.Errorf("conflict"))
				}
				return client.SubResource(subResourceName).Update(ctx, obj, opts...)
			},
		}).
		Build()

	err := updateStatusWithRetry(testCtx(), c, cfg, func() {
		cfg.Status.Phase = openvoxv1alpha1.ConfigPhaseRunning
	})
	if err != nil {
		t.Fatalf("unexpected error after retry: %v", err)
	}
	if calls.Load() < 2 {
		t.Errorf("expected at least 2 calls, got %d", calls.Load())
	}
}

func TestUpdateStatusWithRetry_NonConflictError(t *testing.T) {
	cfg := &openvoxv1alpha1.Config{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-config",
			Namespace: testNamespace,
		},
	}

	c := fake.NewClientBuilder().
		WithScheme(testScheme()).
		WithObjects(cfg).
		WithStatusSubresource(&openvoxv1alpha1.Config{}).
		WithInterceptorFuncs(interceptor.Funcs{
			SubResourceUpdate: func(ctx context.Context, client client.Client, subResourceName string, obj client.Object, opts ...client.SubResourceUpdateOption) error {
				return fmt.Errorf("internal server error")
			},
		}).
		Build()

	err := updateStatusWithRetry(testCtx(), c, cfg, func() {
		cfg.Status.Phase = openvoxv1alpha1.ConfigPhaseRunning
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// Verify configMapVolume returns correct type
func TestConfigMapVolumeType(t *testing.T) {
	vol := configMapVolume("test", "cm", "key")
	if vol.ConfigMap == nil {
		t.Fatal("expected ConfigMap volume source")
	}
	// Verify no other volume sources are set
	if vol.Secret != nil || vol.EmptyDir != nil || vol.PersistentVolumeClaim != nil {
		t.Error("unexpected additional volume sources set")
	}
}

func TestResolveSecretKey(t *testing.T) {
	secret := newSecret("db-creds", map[string][]byte{
		"password": []byte("supersecret"),
		"username": []byte("admin"),
	})
	c := setupTestClient(secret)

	tests := []struct {
		name       string
		secretName string
		key        string
		want       string
		wantErr    bool
	}{
		{
			name:       "existing key",
			secretName: "db-creds",
			key:        "password",
			want:       "supersecret",
			wantErr:    false,
		},
		{
			name:       "missing key",
			secretName: "db-creds",
			key:        "missing",
			want:       "",
			wantErr:    true,
		},
		{
			name:       "missing secret",
			secretName: "not-exists",
			key:        "password",
			want:       "",
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveSecretKey(testCtx(), c, testNamespace, tt.secretName, tt.key)
			if (err != nil) != tt.wantErr {
				t.Errorf("resolveSecretKey() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("resolveSecretKey() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsSecretReady(t *testing.T) {
	secret := newSecret("my-secret", map[string][]byte{
		"tls.crt": []byte("cert-data"),
		"tls.key": []byte("key-data"),
	})
	c := setupTestClient(secret)

	tests := []struct {
		name        string
		secretName  string
		requiredKey string
		want        bool
	}{
		{
			name:        "exists without key requirement",
			secretName:  "my-secret",
			requiredKey: "",
			want:        true,
		},
		{
			name:        "exists with present key",
			secretName:  "my-secret",
			requiredKey: "tls.crt",
			want:        true,
		},
		{
			name:        "exists with missing key",
			secretName:  "my-secret",
			requiredKey: "ca.crt",
			want:        false,
		},
		{
			name:        "secret does not exist",
			secretName:  "not-exists",
			requiredKey: "",
			want:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isSecretReady(testCtx(), c, tt.secretName, testNamespace, tt.requiredKey)
			if got != tt.want {
				t.Errorf("isSecretReady() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCAInternalServiceName(t *testing.T) {
	tests := []struct {
		caName string
		want   string
	}{
		{caName: "production", want: "production-internal"},
		{caName: "my-ca", want: "my-ca-internal"},
		{caName: "", want: "-internal"},
	}

	for _, tt := range tests {
		t.Run(tt.caName, func(t *testing.T) {
			got := caInternalServiceName(tt.caName)
			if got != tt.want {
				t.Errorf("caInternalServiceName(%q) = %q, want %q", tt.caName, got, tt.want)
			}
		})
	}
}

func TestParseCertNotAfter(t *testing.T) {
	// Valid PEM certificate (self-signed, expires 2030-01-01)
	validCertPEM := []byte(`-----BEGIN CERTIFICATE-----
MIICujCCAaKgAwIBAgIBATANBgkqhkiG9w0BAQsFADAPMQ0wCwYDVQQDEwR0ZXN0
MB4XDTIwMDEwMTAwMDAwMFoXDTMwMDEwMTAwMDAwMFowDzENMAsGA1UEAxMEdGVz
dDCCASIwDQYJKoZIhvcNAQEBBQADggEPADCCAQoCggEBAPO26OVqqye9Z3SF7VEs
8uiKA8VNZmRg1LPsn+IYY6oc025pnAG8WyIuWg6MMYGTpAbKWVnI1nwW8EBv8Qg3
Cl5Y1LGbnnZnu7tdMkcnLZJv5LWcIaMQw0PvAOSqPBca66/wN/I3CBMCwQGvuBAY
UK7A9hN29X0EX9Hcj/cMgGRvn2pk61ae2pOiLkxgH6OQT8QJW71TwBRgKwwtfY5G
Q5wLMcssT8tO1MzWCkjN1akL9Xd+E4dnH00qsej7NFXb+lpobI7YdcmBVbIv0JtK
YoRuiyBB+0MoEt7Rr0VEjbSeVvbiFthVY4Gkq64wlVBZsjECswxMjk55Lyps8zgT
zkUCAwEAAaMhMB8wHQYDVR0OBBYEFOoDmk6u9EfFFp6Lq4wPkcMxHAMMMA0GCSqG
SIb3DQEBCwUAA4IBAQAOXzQYERe2tA+CT4jzRwy3Fr6UTCPdoMD0vXbFMPWwiPDe
N1C9MHRxAoxc1MEdU6/WOFWQRoVaPNHVp1C7RfARekh7aPKM8Pnv2iCQMV4OLx6l
UPUciFsDEJzh0x0ZViP1/b31wrMAZqiqJldJo0x3TojEEXnkCBL9KVGg+4hQ7HeT
Cry0P1mb4Y1tLgR1iDN2oAtLno6yURFlZ5QiF8urp1TGdz5F3LWOAb4Hum6ClliM
8yULVmDNZPUTKNwCAv/5ebwxN3ZidYthXuO014lxRMfVNa4IQWXPZDl64UnMLNzp
liqwHJVNUueDArq7d4MOYP/zXLbF7OJ8ordjCPzl
-----END CERTIFICATE-----`)

	// Invalid PEM
	invalidPEM := []byte("not a valid PEM")

	// Wrong PEM type
	wrongTypePEM := []byte(`-----BEGIN PRIVATE KEY-----
MIIBVgIBADANBgkqhkiG9w0BAQEFAASCAUAwggE8AgEAAkEAu6PehQqeA2Jz/Vrw
-----END PRIVATE KEY-----`)

	t.Run("valid certificate", func(t *testing.T) {
		result := parseCertNotAfter(testCtx(), validCertPEM)
		if result == nil {
			t.Fatal("expected non-nil result for valid certificate")
			return
		}
		// Check year is 2030
		if result.Year() != 2030 {
			t.Errorf("expected year 2030, got %d", result.Year())
		}
	})

	t.Run("invalid PEM", func(t *testing.T) {
		result := parseCertNotAfter(testCtx(), invalidPEM)
		if result != nil {
			t.Errorf("expected nil result for invalid PEM, got %v", result)
		}
	})

	t.Run("wrong PEM type", func(t *testing.T) {
		result := parseCertNotAfter(testCtx(), wrongTypePEM)
		if result != nil {
			t.Errorf("expected nil result for wrong PEM type, got %v", result)
		}
	})

	t.Run("empty input", func(t *testing.T) {
		result := parseCertNotAfter(testCtx(), []byte{})
		if result != nil {
			t.Errorf("expected nil result for empty input, got %v", result)
		}
	})
}
