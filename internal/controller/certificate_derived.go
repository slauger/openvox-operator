package controller

import (
	"context"
	"fmt"
	"slices"
	"sort"

	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// effectiveDNSAltNames returns the alt names a Certificate is issued for: its
// own spec, plus the route hostname of every Pool that asks for injection and
// is joined by a Server using this Certificate.
//
// This replaces the Pool writing into Certificate.spec. Deriving keeps the
// Certificate's spec owned by whoever wrote it, which is what makes the result
// stable under a GitOps controller that reverts foreign changes.
func (r *CertificateReconciler) effectiveDNSAltNames(ctx context.Context,
	cert *openvoxv1alpha1.Certificate) ([]string, error) {
	names := append([]string(nil), cert.Spec.DNSAltNames...)

	servers := &openvoxv1alpha1.ServerList{}
	if err := r.List(ctx, servers,
		client.InNamespace(cert.Namespace),
		client.MatchingFields{IndexCertificateRef: cert.Name}); err != nil {
		return nil, fmt.Errorf("listing Servers for Certificate %s: %w", cert.Name, err)
	}

	wanted := map[string]bool{}
	for i := range servers.Items {
		for _, ref := range servers.Items[i].Spec.PoolRefs {
			wanted[ref] = true
		}
	}
	if len(wanted) == 0 {
		return dedupeSorted(names), nil
	}

	pools := &openvoxv1alpha1.PoolList{}
	if err := r.List(ctx, pools, client.InNamespace(cert.Namespace)); err != nil {
		return nil, fmt.Errorf("listing Pools for Certificate %s: %w", cert.Name, err)
	}
	for i := range pools.Items {
		pool := &pools.Items[i]
		if !wanted[pool.Name] || !pool.DeletionTimestamp.IsZero() {
			continue
		}
		route := pool.Spec.Route
		if route == nil || !route.Enabled || !route.InjectDNSAltName || route.Hostname == "" {
			continue
		}
		names = append(names, route.Hostname)
	}

	return dedupeSorted(names), nil
}

// dedupeSorted returns the names in a stable order without duplicates, so the
// signing hash does not change just because a listing order did.
func dedupeSorted(names []string) []string {
	sort.Strings(names)
	return slices.Compact(names)
}

// enqueueCertificatesForPool reaches the Certificates whose effective alt names
// a Pool contributes to. Without it a route hostname added later would not be
// picked up until something else touched the Certificate.
func certificatesForPool(c client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		pool, ok := obj.(*openvoxv1alpha1.Pool)
		if !ok {
			return nil
		}
		servers := &openvoxv1alpha1.ServerList{}
		if err := c.List(ctx, servers, client.InNamespace(pool.Namespace)); err != nil {
			return nil
		}
		seen := map[string]bool{}
		var reqs []reconcile.Request
		for i := range servers.Items {
			s := &servers.Items[i]
			if s.Spec.CertificateRef == "" || seen[s.Spec.CertificateRef] {
				continue
			}
			if !slices.Contains(s.Spec.PoolRefs, pool.Name) {
				continue
			}
			seen[s.Spec.CertificateRef] = true
			reqs = append(reqs, reconcile.Request{NamespacedName: client.ObjectKey{
				Name: s.Spec.CertificateRef, Namespace: s.Namespace}})
		}
		return reqs
	}
}

// enqueueCertificatesForServerPools covers the other direction: a Server that
// joins or leaves a Pool changes which hostnames its Certificate carries.
func certificatesForServerPools() handler.MapFunc {
	return func(_ context.Context, obj client.Object) []reconcile.Request {
		server, ok := obj.(*openvoxv1alpha1.Server)
		if !ok || server.Spec.CertificateRef == "" {
			return nil
		}
		return []reconcile.Request{{NamespacedName: client.ObjectKey{
			Name: server.Spec.CertificateRef, Namespace: server.Namespace}}}
	}
}
