package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// enqueueServersForSecret returns an event handler that maps Secret changes
// to Server reconcile requests. When an openvox-managed Secret changes,
// all Servers in the same environment are reconciled.
func enqueueServersForSecret(c client.Client) handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
		labels := obj.GetLabels()
		if labels["app.kubernetes.io/managed-by"] != "openvox-operator" {
			return nil
		}

		cfgName := labels[LabelConfig]
		if cfgName == "" {
			return nil
		}

		serverList := &openvoxv1alpha1.ServerList{}
		if err := c.List(ctx, serverList, client.InNamespace(obj.GetNamespace())); err != nil {
			return nil
		}

		var requests []ctrl.Request
		for _, server := range serverList.Items {
			if server.Spec.ConfigRef == cfgName {
				requests = append(requests, ctrl.Request{
					NamespacedName: types.NamespacedName{
						Name:      server.Name,
						Namespace: server.Namespace,
					},
				})
			}
		}

		return requests
	})
}
