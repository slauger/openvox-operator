package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// enqueueCertificatesForSecret returns an event handler that maps Secret changes
// to Certificate reconcile requests.
func enqueueCertificatesForSecret(c client.Client) handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
		labels := obj.GetLabels()
		if labels["app.kubernetes.io/managed-by"] != "openvox-operator" {
			return nil
		}

		// Direct mapping: SSL Secret has certificate label
		certName := labels["openvox.voxpupuli.org/certificate"]
		if certName != "" {
			return []ctrl.Request{
				{NamespacedName: types.NamespacedName{Name: certName, Namespace: obj.GetNamespace()}},
			}
		}

		// CA Secret change → reconcile all Certificates referencing CAs in this environment
		cfgName := labels[LabelConfig]
		if cfgName == "" {
			return nil
		}

		certList := &openvoxv1alpha1.CertificateList{}
		if err := c.List(ctx, certList, client.InNamespace(obj.GetNamespace())); err != nil {
			return nil
		}

		var requests []ctrl.Request
		for _, cert := range certList.Items {
			ca := &openvoxv1alpha1.CertificateAuthority{}
			if err := c.Get(ctx, types.NamespacedName{Name: cert.Spec.AuthorityRef, Namespace: cert.Namespace}, ca); err != nil {
				continue
			}
			if ca.Spec.ConfigRef == cfgName {
				requests = append(requests, ctrl.Request{
					NamespacedName: types.NamespacedName{
						Name:      cert.Name,
						Namespace: cert.Namespace,
					},
				})
			}
		}
		return requests
	})
}
