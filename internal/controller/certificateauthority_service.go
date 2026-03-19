package controller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// reconcileCAService creates or updates a ClusterIP Service for internal operator
// communication with the CA (CSR signing, CRL refresh).
func (r *CertificateAuthorityReconciler) reconcileCAService(ctx context.Context, ca *openvoxv1alpha1.CertificateAuthority) error {
	logger := log.FromContext(ctx)
	svcName := caInternalServiceName(ca.Name)

	labels := caLabels(ca.Name)
	selector := map[string]string{
		LabelCA: "true",
	}

	svc := &corev1.Service{}
	err := r.Get(ctx, types.NamespacedName{Name: svcName, Namespace: ca.Namespace}, svc)
	if errors.IsNotFound(err) {
		logger.Info("creating CA Service", "name", svcName)
		svc = &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      svcName,
				Namespace: ca.Namespace,
				Labels:    labels,
			},
			Spec: corev1.ServiceSpec{
				Type:     corev1.ServiceTypeClusterIP,
				Selector: selector,
				Ports: []corev1.ServicePort{
					{
						Name:       "https",
						Port:       8140,
						TargetPort: intstr.FromInt32(8140),
						Protocol:   corev1.ProtocolTCP,
					},
				},
			},
		}
		if err := controllerutil.SetControllerReference(ca, svc, r.Scheme); err != nil {
			return err
		}
		return r.Create(ctx, svc)
	} else if err != nil {
		return err
	}

	// Update existing service
	svc.Labels = labels
	svc.Spec.Type = corev1.ServiceTypeClusterIP
	svc.Spec.Selector = selector
	if len(svc.Spec.Ports) == 0 {
		svc.Spec.Ports = []corev1.ServicePort{{}}
	}
	svc.Spec.Ports[0].Name = "https"
	svc.Spec.Ports[0].Port = 8140
	svc.Spec.Ports[0].TargetPort = intstr.FromInt32(8140)
	svc.Spec.Ports[0].Protocol = corev1.ProtocolTCP
	return r.Update(ctx, svc)
}
