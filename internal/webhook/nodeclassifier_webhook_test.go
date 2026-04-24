package webhook

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

func TestNodeClassifierValidator_Update(t *testing.T) {
	v := &NodeClassifierValidator{}
	valid := &openvoxv1alpha1.NodeClassifier{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec:       openvoxv1alpha1.NodeClassifierSpec{URL: "https://foreman.example.com"},
	}
	invalid := &openvoxv1alpha1.NodeClassifier{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec:       openvoxv1alpha1.NodeClassifierSpec{URL: "not-a-url"},
	}

	if _, err := v.ValidateUpdate(context.Background(), nil, valid); err != nil {
		t.Errorf("expected no error for valid update, got %v", err)
	}
	if _, err := v.ValidateUpdate(context.Background(), nil, invalid); err == nil {
		t.Error("expected error for invalid URL update")
	}
}

func TestNodeClassifierValidator(t *testing.T) {
	t.Run("valid URL", func(t *testing.T) {
		v := &NodeClassifierValidator{}
		nc := &openvoxv1alpha1.NodeClassifier{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.NodeClassifierSpec{
				URL: "https://enc.example.com",
			},
		}
		_, err := v.ValidateCreate(context.Background(), nc)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("invalid URL", func(t *testing.T) {
		v := &NodeClassifierValidator{}
		nc := &openvoxv1alpha1.NodeClassifier{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.NodeClassifierSpec{
				URL: "not-a-url",
			},
		}
		_, err := v.ValidateCreate(context.Background(), nc)
		if err == nil {
			t.Error("expected error for invalid URL")
		}
	})

	t.Run("delete always succeeds", func(t *testing.T) {
		v := &NodeClassifierValidator{}
		_, err := v.ValidateDelete(context.Background(), &openvoxv1alpha1.NodeClassifier{})
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})
}
