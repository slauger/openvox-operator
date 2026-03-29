package webhook

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(openvoxv1alpha1.AddToScheme(s))
	return s
}

func setupTestClient(objs ...client.Object) client.Client {
	return fake.NewClientBuilder().
		WithScheme(testScheme()).
		WithObjects(objs...).
		Build()
}

func TestRefExists(t *testing.T) {
	ca := &openvoxv1alpha1.CertificateAuthority{
		ObjectMeta: metav1.ObjectMeta{Name: "my-ca", Namespace: "default"},
	}
	c := setupTestClient(ca)

	t.Run("found", func(t *testing.T) {
		if err := refExists(context.Background(), c, "default", "my-ca", &openvoxv1alpha1.CertificateAuthority{}); err != nil {
			t.Errorf("expected nil error, got %v", err)
		}
	})

	t.Run("not found", func(t *testing.T) {
		if err := refExists(context.Background(), c, "default", "missing", &openvoxv1alpha1.CertificateAuthority{}); err == nil {
			t.Error("expected error for missing ref")
		}
	})
}

func TestValidateURL(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		wantErr bool
	}{
		{"valid https", "https://example.com", false},
		{"valid http", "http://example.com:8080/path", false},
		{"no scheme", "example.com", true},
		{"ftp scheme", "ftp://example.com", true},
		{"empty", "", true},
		{"just scheme", "https://", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateURL(tt.url, "test")
			if (err != nil) != tt.wantErr {
				t.Errorf("validateURL(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}

func TestValidateDuration(t *testing.T) {
	tests := []struct {
		name    string
		dur     string
		wantErr bool
	}{
		{"empty", "", false},
		{"valid years", "5y", false},
		{"valid days", "90d", false},
		{"valid hours", "8760h", false},
		{"valid minutes", "5m", false},
		{"valid seconds", "300s", false},
		{"plain number", "86400", false},
		{"invalid unit", "5x", true},
		{"invalid format", "abc", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateDuration(tt.dur, "test")
			if (err != nil) != tt.wantErr {
				t.Errorf("validateDuration(%q) error = %v, wantErr %v", tt.dur, err, tt.wantErr)
			}
		})
	}
}
