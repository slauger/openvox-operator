package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

func (r *ConfigReconciler) reconcileServerServiceAccount(ctx context.Context, cfg *openvoxv1alpha1.Config) error {
	saName := fmt.Sprintf("%s-server", cfg.Name)
	automount := false

	sa := &corev1.ServiceAccount{}
	if err := r.Get(ctx, types.NamespacedName{Name: saName, Namespace: cfg.Namespace}, sa); errors.IsNotFound(err) {
		sa = &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      saName,
				Namespace: cfg.Namespace,
				Labels:    configLabels(cfg.Name),
			},
			AutomountServiceAccountToken: &automount,
		}
		if err := controllerutil.SetControllerReference(cfg, sa, r.Scheme); err != nil {
			return err
		}
		return r.Create(ctx, sa)
	} else {
		return err
	}
}
