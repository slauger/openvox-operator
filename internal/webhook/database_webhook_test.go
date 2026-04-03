package webhook

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

func TestDatabaseValidator(t *testing.T) {
	cert := &openvoxv1alpha1.Certificate{
		ObjectMeta: metav1.ObjectMeta{Name: "db-cert", Namespace: "default"},
	}

	validDB := func() *openvoxv1alpha1.Database {
		return &openvoxv1alpha1.Database{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.DatabaseSpec{
				CertificateRef: "db-cert",
				Postgres: openvoxv1alpha1.PostgresSpec{
					Host:                 "postgres.example.com",
					CredentialsSecretRef: "db-credentials",
					SSLMode:              "require",
				},
			},
		}
	}

	t.Run("valid database", func(t *testing.T) {
		c := setupTestClient(cert)
		v := &DatabaseValidator{Client: c}
		_, err := v.ValidateCreate(context.Background(), validDB())
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("empty certificateRef", func(t *testing.T) {
		c := setupTestClient(cert)
		v := &DatabaseValidator{Client: c}
		d := validDB()
		d.Spec.CertificateRef = ""
		_, err := v.ValidateCreate(context.Background(), d)
		if err == nil {
			t.Error("expected error for empty certificateRef")
		}
	})

	t.Run("non-existent certificate", func(t *testing.T) {
		c := setupTestClient() // no certificate in cluster
		v := &DatabaseValidator{Client: c}
		_, err := v.ValidateCreate(context.Background(), validDB())
		if err == nil {
			t.Error("expected error for non-existent certificate")
		}
	})

	t.Run("empty postgres.host", func(t *testing.T) {
		c := setupTestClient(cert)
		v := &DatabaseValidator{Client: c}
		d := validDB()
		d.Spec.Postgres.Host = ""
		_, err := v.ValidateCreate(context.Background(), d)
		if err == nil {
			t.Error("expected error for empty postgres.host")
		}
	})

	t.Run("empty postgres.credentialsSecretRef", func(t *testing.T) {
		c := setupTestClient(cert)
		v := &DatabaseValidator{Client: c}
		d := validDB()
		d.Spec.Postgres.CredentialsSecretRef = ""
		_, err := v.ValidateCreate(context.Background(), d)
		if err == nil {
			t.Error("expected error for empty postgres.credentialsSecretRef")
		}
	})

	t.Run("invalid postgres.sslMode", func(t *testing.T) {
		c := setupTestClient(cert)
		v := &DatabaseValidator{Client: c}
		d := validDB()
		d.Spec.Postgres.SSLMode = "invalid"
		_, err := v.ValidateCreate(context.Background(), d)
		if err == nil {
			t.Error("expected error for invalid postgres.sslMode")
		}
	})

	t.Run("valid sslMode values", func(t *testing.T) {
		for _, mode := range []string{"disable", "allow", "prefer", "require", "verify-ca", "verify-full"} {
			t.Run(mode, func(t *testing.T) {
				c := setupTestClient(cert)
				v := &DatabaseValidator{Client: c}
				d := validDB()
				d.Spec.Postgres.SSLMode = mode
				_, err := v.ValidateCreate(context.Background(), d)
				if err != nil {
					t.Errorf("expected no error for sslMode %q, got %v", mode, err)
				}
			})
		}
	})

	t.Run("empty sslMode is valid", func(t *testing.T) {
		c := setupTestClient(cert)
		v := &DatabaseValidator{Client: c}
		d := validDB()
		d.Spec.Postgres.SSLMode = ""
		_, err := v.ValidateCreate(context.Background(), d)
		if err != nil {
			t.Errorf("expected no error for empty sslMode, got %v", err)
		}
	})

	t.Run("update validates new object", func(t *testing.T) {
		c := setupTestClient(cert)
		v := &DatabaseValidator{Client: c}
		d := validDB()
		_, err := v.ValidateUpdate(context.Background(), d, d)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("delete always succeeds", func(t *testing.T) {
		v := &DatabaseValidator{Client: setupTestClient()}
		_, err := v.ValidateDelete(context.Background(), &openvoxv1alpha1.Database{})
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})
}
