package controller

import (
	"slices"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// TestEffectiveDNSAltNames_AddsTheRouteHostname is the replacement for the
// Pool's former write into the Certificate spec.
func TestEffectiveDNSAltNames_AddsTheRouteHostname(t *testing.T) {
	cert := newCertificate("web-cert", "production-ca", openvoxv1alpha1.CertificatePhaseSigned)
	cert.Spec.DNSAltNames = []string{"web.example.com"}

	server := newServer("web")
	server.Spec.CertificateRef = "web-cert"
	server.Spec.PoolRefs = []string{"puppet"}

	pool := newPool("puppet", withRoute(true, "puppet.example.com", "gw"))
	pool.Spec.Route.InjectDNSAltName = true

	r := newCertificateReconciler(setupTestClient(cert, server, pool))
	names, err := r.effectiveDNSAltNames(testCtx(), cert)
	if err != nil {
		t.Fatalf("deriving alt names: %v", err)
	}
	if !slices.Contains(names, "puppet.example.com") {
		t.Errorf("expected the route hostname among the effective names, got %v", names)
	}
	if !slices.Contains(names, "web.example.com") {
		t.Errorf("the Certificate's own names must survive, got %v", names)
	}
}

// TestEffectiveDNSAltNames_LeavesTheSpecAlone is the point of the redesign: the
// derivation reads, it never writes into a resource it does not own.
func TestEffectiveDNSAltNames_LeavesTheSpecAlone(t *testing.T) {
	cert := newCertificate("web-cert", "production-ca", openvoxv1alpha1.CertificatePhaseSigned)
	server := newServer("web")
	server.Spec.CertificateRef = "web-cert"
	server.Spec.PoolRefs = []string{"puppet"}
	pool := newPool("puppet", withRoute(true, "puppet.example.com", "gw"))
	pool.Spec.Route.InjectDNSAltName = true

	c := setupTestClient(cert, server, pool)
	r := newCertificateReconciler(c)
	if _, err := r.effectiveDNSAltNames(testCtx(), cert); err != nil {
		t.Fatalf("deriving alt names: %v", err)
	}

	got := &openvoxv1alpha1.Certificate{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "web-cert", Namespace: testNamespace}, got); err != nil {
		t.Fatalf("reading the Certificate back: %v", err)
	}
	if len(got.Spec.DNSAltNames) != 0 {
		t.Errorf("the derivation must not write into the spec, got %v", got.Spec.DNSAltNames)
	}
}

// TestEffectiveDNSAltNames_IsIdempotent covers what made the old design churn
// under GitOps: repeating the operation must not change the result.
func TestEffectiveDNSAltNames_IsIdempotent(t *testing.T) {
	cert := newCertificate("web-cert", "production-ca", openvoxv1alpha1.CertificatePhaseSigned)
	cert.Spec.DNSAltNames = []string{"puppet.example.com"} // already present
	server := newServer("web")
	server.Spec.CertificateRef = "web-cert"
	server.Spec.PoolRefs = []string{"puppet"}
	pool := newPool("puppet", withRoute(true, "puppet.example.com", "gw"))
	pool.Spec.Route.InjectDNSAltName = true

	r := newCertificateReconciler(setupTestClient(cert, server, pool))
	first, err := r.effectiveDNSAltNames(testCtx(), cert)
	if err != nil {
		t.Fatalf("deriving alt names: %v", err)
	}
	second, err := r.effectiveDNSAltNames(testCtx(), cert)
	if err != nil {
		t.Fatalf("deriving alt names again: %v", err)
	}
	if !slices.Equal(first, second) {
		t.Errorf("the derivation must be stable, got %v then %v", first, second)
	}
	if len(first) != 1 {
		t.Errorf("a name already in the spec must not be duplicated, got %v", first)
	}
}

// TestEffectiveDNSAltNames_IgnoresPoolsWithoutInjection keeps the API field
// meaningful: it now says "is taken into account" rather than "mutates".
func TestEffectiveDNSAltNames_IgnoresPoolsWithoutInjection(t *testing.T) {
	cert := newCertificate("web-cert", "production-ca", openvoxv1alpha1.CertificatePhaseSigned)
	server := newServer("web")
	server.Spec.CertificateRef = "web-cert"
	server.Spec.PoolRefs = []string{"puppet"}
	pool := newPool("puppet", withRoute(true, "puppet.example.com", "gw")) // InjectDNSAltName stays false

	r := newCertificateReconciler(setupTestClient(cert, server, pool))
	names, err := r.effectiveDNSAltNames(testCtx(), cert)
	if err != nil {
		t.Fatalf("deriving alt names: %v", err)
	}
	if slices.Contains(names, "puppet.example.com") {
		t.Errorf("a Pool that does not ask for injection must be ignored, got %v", names)
	}
}

// TestEffectiveDNSAltNames_IgnoresUnrelatedServers bounds the lookup to Servers
// that actually use this Certificate and Pools they actually join.
func TestEffectiveDNSAltNames_IgnoresUnrelatedServers(t *testing.T) {
	cert := newCertificate("web-cert", "production-ca", openvoxv1alpha1.CertificatePhaseSigned)

	otherServer := newServer("other")
	otherServer.Spec.CertificateRef = "other-cert"
	otherServer.Spec.PoolRefs = []string{"puppet"}

	notJoined := newServer("web")
	notJoined.Spec.CertificateRef = "web-cert"
	notJoined.Spec.PoolRefs = nil

	pool := newPool("puppet", withRoute(true, "puppet.example.com", "gw"))
	pool.Spec.Route.InjectDNSAltName = true

	r := newCertificateReconciler(setupTestClient(cert, otherServer, notJoined, pool))
	names, err := r.effectiveDNSAltNames(testCtx(), cert)
	if err != nil {
		t.Fatalf("deriving alt names: %v", err)
	}
	if slices.Contains(names, "puppet.example.com") {
		t.Errorf("neither a foreign Certificate nor an unjoined Pool may contribute, got %v", names)
	}
}

// TestEffectiveDNSAltNames_IgnoresTerminatingPool keeps a Pool on its way out
// from holding a name in the certificate.
func TestEffectiveDNSAltNames_IgnoresTerminatingPool(t *testing.T) {
	cert := newCertificate("web-cert", "production-ca", openvoxv1alpha1.CertificatePhaseSigned)
	server := newServer("web")
	server.Spec.CertificateRef = "web-cert"
	server.Spec.PoolRefs = []string{"puppet"}

	pool := newPool("puppet", withRoute(true, "puppet.example.com", "gw"))
	pool.Spec.Route.InjectDNSAltName = true
	now := metav1.Now()
	pool.DeletionTimestamp = &now
	pool.Finalizers = []string{"example.com/keep"}

	r := newCertificateReconciler(setupTestClient(cert, server, pool))
	names, err := r.effectiveDNSAltNames(testCtx(), cert)
	if err != nil {
		t.Fatalf("deriving alt names: %v", err)
	}
	if slices.Contains(names, "puppet.example.com") {
		t.Errorf("a terminating Pool must not contribute, got %v", names)
	}
}

// TestEnqueueCertificatesForPool covers the watch that makes a later route
// hostname reach the Certificate at all.
func TestEnqueueCertificatesForPool(t *testing.T) {
	server := newServer("web")
	server.Spec.CertificateRef = "web-cert"
	server.Spec.PoolRefs = []string{"puppet"}
	unrelated := newServer("other")
	unrelated.Spec.CertificateRef = "other-cert"
	unrelated.Spec.PoolRefs = []string{"different-pool"}
	pool := newPool("puppet", withRoute(true, "puppet.example.com", "gw"))

	c := setupTestClient(server, unrelated, pool)
	got := certificatesForPool(c)(testCtx(), pool)
	if !equalNames(got, "web-cert") {
		t.Errorf("expected only the Certificate of the Server joining this Pool, got %v", names(got))
	}
}

// TestSigningSpecHash_TracksTheEffectiveNames is what makes a newly joined Pool
// actually trigger a re-sign.
func TestSigningSpecHash_TracksTheEffectiveNames(t *testing.T) {
	cert := newCertificate("web-cert", "production-ca", openvoxv1alpha1.CertificatePhaseSigned)

	withoutPool := signingSpecHash(cert, cert.Spec.DNSAltNames)
	withPool := signingSpecHash(cert, []string{"puppet.example.com"})

	if withoutPool == withPool {
		t.Error("a derived alt name must change the signing hash, otherwise no re-sign happens")
	}
}
