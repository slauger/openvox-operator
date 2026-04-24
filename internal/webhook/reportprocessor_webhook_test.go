package webhook

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

func TestReportProcessorValidator_Update(t *testing.T) {
	cfg := &openvoxv1alpha1.Config{
		ObjectMeta: metav1.ObjectMeta{Name: "production", Namespace: "default"},
	}
	c := setupTestClient(cfg)
	v := &ReportProcessorValidator{Client: c}

	valid := &openvoxv1alpha1.ReportProcessor{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec:       openvoxv1alpha1.ReportProcessorSpec{ConfigRef: "production", URL: "https://puppetdb.example.com"},
	}
	invalid := &openvoxv1alpha1.ReportProcessor{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec:       openvoxv1alpha1.ReportProcessorSpec{ConfigRef: "production", URL: "not-a-url"},
	}

	if _, err := v.ValidateUpdate(context.Background(), nil, valid); err != nil {
		t.Errorf("expected no error for valid update, got %v", err)
	}
	if _, err := v.ValidateUpdate(context.Background(), nil, invalid); err == nil {
		t.Error("expected error for invalid URL update")
	}
}

func TestReportProcessorValidator(t *testing.T) {
	cfg := &openvoxv1alpha1.Config{
		ObjectMeta: metav1.ObjectMeta{Name: "production", Namespace: "default"},
	}

	t.Run("valid report processor", func(t *testing.T) {
		c := setupTestClient(cfg)
		v := &ReportProcessorValidator{Client: c}
		rp := &openvoxv1alpha1.ReportProcessor{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.ReportProcessorSpec{
				ConfigRef: "production",
				URL:       "https://puppetdb.example.com:8081",
			},
		}
		_, err := v.ValidateCreate(context.Background(), rp)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("missing configRef", func(t *testing.T) {
		c := setupTestClient()
		v := &ReportProcessorValidator{Client: c}
		rp := &openvoxv1alpha1.ReportProcessor{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.ReportProcessorSpec{
				ConfigRef: "missing",
				URL:       "https://example.com",
			},
		}
		_, err := v.ValidateCreate(context.Background(), rp)
		if err == nil {
			t.Error("expected error for missing configRef")
		}
	})

	t.Run("invalid URL", func(t *testing.T) {
		c := setupTestClient(cfg)
		v := &ReportProcessorValidator{Client: c}
		rp := &openvoxv1alpha1.ReportProcessor{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.ReportProcessorSpec{
				ConfigRef: "production",
				URL:       "not-a-url",
			},
		}
		_, err := v.ValidateCreate(context.Background(), rp)
		if err == nil {
			t.Error("expected error for invalid URL")
		}
	})

	t.Run("delete always succeeds", func(t *testing.T) {
		v := &ReportProcessorValidator{Client: setupTestClient()}
		_, err := v.ValidateDelete(context.Background(), &openvoxv1alpha1.ReportProcessor{})
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})
}
