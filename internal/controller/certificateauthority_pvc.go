package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

func (r *CertificateAuthorityReconciler) reconcileCAPVC(ctx context.Context, ca *openvoxv1alpha1.CertificateAuthority) error {
	pvcName := fmt.Sprintf("%s-data", ca.Name)

	pvc := &corev1.PersistentVolumeClaim{}
	err := r.Get(ctx, types.NamespacedName{Name: pvcName, Namespace: ca.Namespace}, pvc)
	if errors.IsNotFound(err) {
		storageSize := DefaultCAStorageGi
		if ca.Spec.Storage.Size != "" {
			storageSize = ca.Spec.Storage.Size
		}

		pvc = &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      pvcName,
				Namespace: ca.Namespace,
				Labels:    caLabels(ca.Name),
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse(storageSize),
					},
				},
			},
		}

		if ca.Spec.Storage.StorageClass != "" {
			pvc.Spec.StorageClassName = &ca.Spec.Storage.StorageClass
		}

		if err := controllerutil.SetControllerReference(ca, pvc, r.Scheme); err != nil {
			return err
		}
		return r.Create(ctx, pvc)
	}
	return err
}
