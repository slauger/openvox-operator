package webhook

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

func TestConfigValidator_Update(t *testing.T) {
	ca := &openvoxv1alpha1.CertificateAuthority{
		ObjectMeta: metav1.ObjectMeta{Name: "my-ca", Namespace: "default"},
	}
	c := setupTestClient(ca)
	v := &ConfigValidator{Client: c}

	valid := &openvoxv1alpha1.Config{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec:       openvoxv1alpha1.ConfigSpec{AuthorityRef: "my-ca"},
	}
	invalid := &openvoxv1alpha1.Config{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec:       openvoxv1alpha1.ConfigSpec{AuthorityRef: "missing-ca"},
	}

	if _, err := v.ValidateUpdate(context.Background(), nil, valid); err != nil {
		t.Errorf("expected no error for valid update, got %v", err)
	}
	if _, err := v.ValidateUpdate(context.Background(), nil, invalid); err == nil {
		t.Error("expected error for missing CA ref update")
	}
}

func TestConfigValidator(t *testing.T) {
	ca := &openvoxv1alpha1.CertificateAuthority{
		ObjectMeta: metav1.ObjectMeta{Name: "my-ca", Namespace: "default"},
	}
	nc := &openvoxv1alpha1.NodeClassifier{
		ObjectMeta: metav1.ObjectMeta{Name: "my-nc", Namespace: "default"},
	}

	t.Run("valid with refs", func(t *testing.T) {
		c := setupTestClient(ca, nc)
		v := &ConfigValidator{Client: c}
		cfg := &openvoxv1alpha1.Config{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.ConfigSpec{
				AuthorityRef:      "my-ca",
				NodeClassifierRef: "my-nc",
			},
		}
		_, err := v.ValidateCreate(context.Background(), cfg)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("valid without optional refs", func(t *testing.T) {
		c := setupTestClient()
		v := &ConfigValidator{Client: c}
		cfg := &openvoxv1alpha1.Config{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec:       openvoxv1alpha1.ConfigSpec{},
		}
		_, err := v.ValidateCreate(context.Background(), cfg)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("missing authorityRef", func(t *testing.T) {
		c := setupTestClient()
		v := &ConfigValidator{Client: c}
		cfg := &openvoxv1alpha1.Config{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.ConfigSpec{
				AuthorityRef: "missing-ca",
			},
		}
		_, err := v.ValidateCreate(context.Background(), cfg)
		if err == nil {
			t.Error("expected error for missing authorityRef")
		}
	})

	t.Run("missing nodeClassifierRef", func(t *testing.T) {
		c := setupTestClient()
		v := &ConfigValidator{Client: c}
		cfg := &openvoxv1alpha1.Config{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.ConfigSpec{
				NodeClassifierRef: "missing-nc",
			},
		}
		_, err := v.ValidateCreate(context.Background(), cfg)
		if err == nil {
			t.Error("expected error for missing nodeClassifierRef")
		}
	})

	t.Run("valid databaseRef", func(t *testing.T) {
		db := &openvoxv1alpha1.Database{
			ObjectMeta: metav1.ObjectMeta{Name: "my-db", Namespace: "default"},
		}
		c := setupTestClient(db)
		v := &ConfigValidator{Client: c}
		cfg := &openvoxv1alpha1.Config{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.ConfigSpec{
				DatabaseRef: "my-db",
			},
		}
		_, err := v.ValidateCreate(context.Background(), cfg)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("missing databaseRef", func(t *testing.T) {
		c := setupTestClient()
		v := &ConfigValidator{Client: c}
		cfg := &openvoxv1alpha1.Config{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.ConfigSpec{
				DatabaseRef: "missing-db",
			},
		}
		_, err := v.ValidateCreate(context.Background(), cfg)
		if err == nil {
			t.Error("expected error for missing databaseRef")
		}
	})

	t.Run("databaseRef and serverUrls mutually exclusive", func(t *testing.T) {
		db := &openvoxv1alpha1.Database{
			ObjectMeta: metav1.ObjectMeta{Name: "my-db", Namespace: "default"},
		}
		c := setupTestClient(db)
		v := &ConfigValidator{Client: c}
		cfg := &openvoxv1alpha1.Config{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.ConfigSpec{
				DatabaseRef: "my-db",
				PuppetDB: openvoxv1alpha1.PuppetDBSpec{
					ServerURLs: []string{"https://external:8081"},
				},
			},
		}
		_, err := v.ValidateCreate(context.Background(), cfg)
		if err == nil {
			t.Error("expected error for databaseRef + serverUrls")
		}
	})

	t.Run("delete always succeeds", func(t *testing.T) {
		v := &ConfigValidator{Client: setupTestClient()}
		_, err := v.ValidateDelete(context.Background(), &openvoxv1alpha1.Config{})
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})
}
