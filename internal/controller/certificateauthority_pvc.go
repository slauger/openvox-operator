package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// defaultCAStorageQuantity is the parsed form of DefaultCAStorageGi. Parsing it
// once at startup keeps the panic-prone MustParse away from the reconcile path;
// the input is our own constant, so a failure here is a build-time mistake.
var defaultCAStorageQuantity = resource.MustParse(DefaultCAStorageGi)

func (r *CertificateAuthorityReconciler) reconcileCAPVC(ctx context.Context, ca *openvoxv1alpha1.CertificateAuthority) error {
	pvcName := fmt.Sprintf("%s-data", ca.Name)

	pvc := &corev1.PersistentVolumeClaim{}
	err := r.Get(ctx, types.NamespacedName{Name: pvcName, Namespace: ca.Namespace}, pvc)
	if apierrors.IsNotFound(err) {
		storage := resolveCAStorage(ca)
		storageSize := defaultCAStorageQuantity
		if storage.Size != nil {
			storageSize = *storage.Size
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
						corev1.ResourceStorage: storageSize,
					},
				},
			},
		}

		if storage.StorageClass != "" {
			pvc.Spec.StorageClassName = &storage.StorageClass
		}

		if err := controllerutil.SetControllerReference(ca, pvc, r.Scheme); err != nil {
			return fmt.Errorf("setting owner reference on PVC %s: %w", pvcName, err)
		}
		return r.Create(ctx, pvc)
	} else if err != nil {
		return fmt.Errorf("getting PVC %s: %w", pvcName, err)
	}
	return nil
}

// resolveCAStorage returns the configured storage settings, or an empty struct
// when none were given.
//
// The kubebuilder default on StorageSpec.Size does not apply when the whole
// storage object is omitted -- nested defaults need the parent to be present --
// so the fallback lives here, the same way resolveEnvironmentPath handles
// spec.puppet.environmentPath.
func resolveCAStorage(ca *openvoxv1alpha1.CertificateAuthority) openvoxv1alpha1.StorageSpec {
	if ca.Spec.Storage != nil {
		return *ca.Spec.Storage
	}
	return openvoxv1alpha1.StorageSpec{}
}
