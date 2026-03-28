package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// enqueueDatabasesForSecret returns an event handler that maps Secret changes
// to Database reconcile requests. When a PG credentials Secret changes,
// all Databases referencing it are reconciled.
func enqueueDatabasesForSecret(c client.Client) handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
		dbList := &openvoxv1alpha1.DatabaseList{}
		if err := c.List(ctx, dbList, client.InNamespace(obj.GetNamespace())); err != nil {
			log.FromContext(ctx).Error(err, "failed to list Databases in watcher")
			return nil
		}

		var requests []ctrl.Request
		for _, db := range dbList.Items {
			if db.Spec.Postgres.CredentialsSecretRef == obj.GetName() {
				requests = append(requests, ctrl.Request{
					NamespacedName: types.NamespacedName{
						Name:      db.Name,
						Namespace: db.Namespace,
					},
				})
			}
		}
		return requests
	})
}
