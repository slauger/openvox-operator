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

// enqueueDatabasesForSecret maps Secret changes to Database reconcile requests.
//
// Three kinds of Secret matter to a Database: the PostgreSQL credentials it
// references by name, the TLS Secret of its Certificate, and the CA Secret.
// The latter two feed the pod template hashes, so a rotation has to roll the
// Deployment.
func enqueueDatabasesForSecret(c client.Client) handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(databasesForSecret(c))
}

// databasesForSecret is the map function behind enqueueDatabasesForSecret.
func databasesForSecret(c client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []ctrl.Request {
		namespace := obj.GetNamespace()

		// PostgreSQL credentials, referenced by name rather than labelled.
		if requests := databasesReferencingSecret(ctx, c, namespace, obj.GetName()); len(requests) > 0 {
			return requests
		}

		labels := obj.GetLabels()
		if labels["app.kubernetes.io/managed-by"] != "openvox-operator" {
			return nil
		}

		// TLS Secret of a Certificate the Database mounts.
		if certName := labels["openvox.voxpupuli.org/certificate"]; certName != "" {
			return databasesMatching(ctx, c, namespace, IndexCertificateRef, certName)
		}

		// CA Secret: resolve over the Certificates issued by that CA.
		if caName := labels[LabelCertificateAuthority]; caName != "" {
			return databasesForCA(ctx, c, namespace, caName)
		}

		return nil
	}
}

// enqueueDatabasesForCertificate maps a Certificate change to the Databases
// mounting it, covering both the transition to Signed and later renewals.
func enqueueDatabasesForCertificate(c client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []ctrl.Request {
		return databasesMatching(ctx, c, obj.GetNamespace(), IndexCertificateRef, obj.GetName())
	}
}

// databasesForCA resolves CA -> Certificates -> Databases.
func databasesForCA(ctx context.Context, c client.Client, namespace, caName string) []ctrl.Request {
	certList := &openvoxv1alpha1.CertificateList{}
	if err := c.List(ctx, certList,
		client.InNamespace(namespace),
		client.MatchingFields{IndexAuthorityRef: caName}); err != nil {
		log.FromContext(ctx).Error(err, "failed to list Certificates in watcher", "ca", caName)
		return nil
	}

	seen := map[types.NamespacedName]bool{}
	var requests []ctrl.Request
	for i := range certList.Items {
		for _, req := range databasesMatching(ctx, c, namespace, IndexCertificateRef, certList.Items[i].Name) {
			if seen[req.NamespacedName] {
				continue
			}
			seen[req.NamespacedName] = true
			requests = append(requests, req)
		}
	}
	return requests
}

// databasesReferencingSecret finds Databases whose PostgreSQL credentials
// Secret has the given name.
func databasesReferencingSecret(ctx context.Context, c client.Client, namespace, secretName string) []ctrl.Request {
	dbList := &openvoxv1alpha1.DatabaseList{}
	if err := c.List(ctx, dbList, client.InNamespace(namespace)); err != nil {
		log.FromContext(ctx).Error(err, "failed to list Databases in watcher")
		return nil
	}

	var requests []ctrl.Request
	for _, db := range dbList.Items {
		if db.Spec.Postgres.CredentialsSecretRef == secretName {
			requests = append(requests, ctrl.Request{
				NamespacedName: types.NamespacedName{Name: db.Name, Namespace: db.Namespace},
			})
		}
	}
	return requests
}

// databasesMatching lists Databases through a field index and turns them into
// reconcile requests.
func databasesMatching(ctx context.Context, c client.Client, namespace, field, value string) []ctrl.Request {
	if value == "" {
		return nil
	}
	dbList := &openvoxv1alpha1.DatabaseList{}
	if err := c.List(ctx, dbList,
		client.InNamespace(namespace),
		client.MatchingFields{field: value}); err != nil {
		log.FromContext(ctx).Error(err, "failed to list Databases in watcher", "field", field, "value", value)
		return nil
	}

	requests := make([]ctrl.Request, 0, len(dbList.Items))
	for _, db := range dbList.Items {
		requests = append(requests, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: db.Name, Namespace: db.Namespace},
		})
	}
	return requests
}
