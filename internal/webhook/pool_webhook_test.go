package webhook

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

func TestPoolValidator_Update(t *testing.T) {
	v := &PoolValidator{}
	valid := &openvoxv1alpha1.Pool{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec:       openvoxv1alpha1.PoolSpec{Service: openvoxv1alpha1.PoolServiceSpec{Port: 8140}},
	}
	invalid := &openvoxv1alpha1.Pool{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
		Spec:       openvoxv1alpha1.PoolSpec{Service: openvoxv1alpha1.PoolServiceSpec{Port: -1}},
	}

	if _, err := v.ValidateUpdate(context.Background(), nil, valid); err != nil {
		t.Errorf("expected no error for valid update, got %v", err)
	}
	if _, err := v.ValidateUpdate(context.Background(), nil, invalid); err == nil {
		t.Error("expected error for invalid port update")
	}
}

func TestPoolValidator(t *testing.T) {
	t.Run("valid port", func(t *testing.T) {
		v := &PoolValidator{}
		p := &openvoxv1alpha1.Pool{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.PoolSpec{
				Service: openvoxv1alpha1.PoolServiceSpec{Port: 8140},
			},
		}
		_, err := v.ValidateCreate(context.Background(), p)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("zero port is valid (default)", func(t *testing.T) {
		v := &PoolValidator{}
		p := &openvoxv1alpha1.Pool{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.PoolSpec{
				Service: openvoxv1alpha1.PoolServiceSpec{Port: 0},
			},
		}
		_, err := v.ValidateCreate(context.Background(), p)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("port too high", func(t *testing.T) {
		v := &PoolValidator{}
		p := &openvoxv1alpha1.Pool{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.PoolSpec{
				Service: openvoxv1alpha1.PoolServiceSpec{Port: 70000},
			},
		}
		_, err := v.ValidateCreate(context.Background(), p)
		if err == nil {
			t.Error("expected error for port > 65535")
		}
	})

	t.Run("negative port", func(t *testing.T) {
		v := &PoolValidator{}
		p := &openvoxv1alpha1.Pool{
			ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: "default"},
			Spec: openvoxv1alpha1.PoolSpec{
				Service: openvoxv1alpha1.PoolServiceSpec{Port: -1},
			},
		}
		_, err := v.ValidateCreate(context.Background(), p)
		if err == nil {
			t.Error("expected error for negative port")
		}
	})

	t.Run("delete always succeeds", func(t *testing.T) {
		v := &PoolValidator{}
		_, err := v.ValidateDelete(context.Background(), &openvoxv1alpha1.Pool{})
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})
}
