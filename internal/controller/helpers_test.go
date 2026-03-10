package controller

import (
	"testing"

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
				Repository: "ghcr.io/slauger/openvox-server",
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
			want: "ghcr.io/slauger/openvox-server:latest",
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
			want: "ghcr.io/slauger/openvox-server:v8.12.1",
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
