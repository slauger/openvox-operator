package controller

import (
	"context"
	"fmt"
	"slices"

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
	svcName := ca.Name

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

// ensureCAServiceDNSAltName injects the CA Service FQDN into the CA server Certificate's
// DNSAltNames so that TLS connections from the operator to the CA service succeed.
func (r *CertificateAuthorityReconciler) ensureCAServiceDNSAltName(ctx context.Context, ca *openvoxv1alpha1.CertificateAuthority, certs []openvoxv1alpha1.Certificate) error {
	logger := log.FromContext(ctx)

	serviceFQDN := fmt.Sprintf("%s.%s.svc", ca.Name, ca.Namespace)

	caCert := r.findCAServerCert(ctx, ca, certs)
	if caCert == nil {
		return nil
	}

	if slices.Contains(caCert.Spec.DNSAltNames, serviceFQDN) {
		return nil
	}

	logger.Info("injecting CA Service FQDN into Certificate DNSAltNames",
		"certificate", caCert.Name, "fqdn", serviceFQDN)
	caCert.Spec.DNSAltNames = append(caCert.Spec.DNSAltNames, serviceFQDN)
	if err := r.Update(ctx, caCert); err != nil {
		return fmt.Errorf("updating Certificate %s with CA Service FQDN: %w", caCert.Name, err)
	}

	// Reset certificate phase to trigger re-signing with the new alt name
	if caCert.Status.Phase == openvoxv1alpha1.CertificatePhaseSigned {
		caCert.Status.Phase = openvoxv1alpha1.CertificatePhasePending
		if err := r.Status().Update(ctx, caCert); err != nil {
			return fmt.Errorf("resetting Certificate %s phase: %w", caCert.Name, err)
		}
		logger.Info("reset Certificate phase to Pending for re-signing",
			"certificate", caCert.Name)
	}

	return nil
}
