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

// enqueueServersForSecret returns an event handler that maps Secret changes
// to Server reconcile requests. When an openvox-managed Secret changes,
// all Servers in the same environment are reconciled.
func enqueueServersForSecret(c client.Client) handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(serversForSecret(c))
}

// serversForSecret is the map function behind enqueueServersForSecret.
func serversForSecret(c client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []ctrl.Request {
		labels := obj.GetLabels()
		if labels["app.kubernetes.io/managed-by"] != "openvox-operator" {
			return nil
		}

		// Config-owned secrets: match directly by LabelConfig
		cfgName := labels[LabelConfig]
		if cfgName != "" {
			return enqueueServersForConfig(c, ctx, obj.GetNamespace(), cfgName)
		}

		// CA-owned secrets: find Configs referencing this CA, then their Servers
		caName := labels[LabelCertificateAuthority]
		if caName != "" {
			return serversForCA(ctx, c, obj.GetNamespace(), caName)
		}

		return nil
	}
}

// enqueueServersForConfigObject maps a Config change to every Server that
// references it. Image, resources, code and the config hash are all derived
// from the Config, so a change there has to reach the Servers.
func enqueueServersForConfigObject(c client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []ctrl.Request {
		return enqueueServersForConfig(c, ctx, obj.GetNamespace(), obj.GetName())
	}
}

// enqueueServersForCertificate maps a Certificate change to the Servers that
// mount it. This covers the transition to Signed as well as a renewal, which
// changes the mounted Secret and therefore the pod template hash.
func enqueueServersForCertificate(c client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []ctrl.Request {
		return serversMatching(ctx, c, obj.GetNamespace(), IndexCertificateRef, obj.GetName())
	}
}

// enqueueServersForCertificateAuthority maps a CertificateAuthority change to
// the Servers that ultimately depend on it, resolved over the Certificates
// issued by that CA.
func enqueueServersForCertificateAuthority(c client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []ctrl.Request {
		return serversForCA(ctx, c, obj.GetNamespace(), obj.GetName())
	}
}

// serversForCA resolves CA -> Certificates -> Servers.
func serversForCA(ctx context.Context, c client.Client, namespace, caName string) []ctrl.Request {
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
		for _, req := range serversMatching(ctx, c, namespace, IndexCertificateRef, certList.Items[i].Name) {
			if seen[req.NamespacedName] {
				continue
			}
			seen[req.NamespacedName] = true
			requests = append(requests, req)
		}
	}
	return requests
}

// enqueueServersForConfig returns reconcile requests for every Server in the
// namespace that references the given Config.
func enqueueServersForConfig(c client.Client, ctx context.Context, namespace, cfgName string) []ctrl.Request {
	return serversMatching(ctx, c, namespace, IndexConfigRef, cfgName)
}

// serversMatching lists Servers through a field index and turns them into
// reconcile requests.
func serversMatching(ctx context.Context, c client.Client, namespace, field, value string) []ctrl.Request {
	if value == "" {
		return nil
	}
	serverList := &openvoxv1alpha1.ServerList{}
	if err := c.List(ctx, serverList,
		client.InNamespace(namespace),
		client.MatchingFields{field: value}); err != nil {
		log.FromContext(ctx).Error(err, "failed to list Servers in watcher", "field", field, "value", value)
		return nil
	}

	requests := make([]ctrl.Request, 0, len(serverList.Items))
	for _, server := range serverList.Items {
		requests = append(requests, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: server.Name, Namespace: server.Namespace},
		})
	}
	return requests
}
