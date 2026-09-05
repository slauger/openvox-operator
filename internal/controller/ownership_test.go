package controller

import (
	"slices"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// TestReconcileService_DoesNotAdoptUnownedResource is the core guarantee of the
// ownership guard: a Service that happens to share the Pool's name belongs to
// somebody else and must not be overwritten.
func TestReconcileService_DoesNotAdoptUnownedResource(t *testing.T) {
	foreign := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "puppet",
			Namespace: testNamespace,
			Labels:    map[string]string{"owner": "somebody-else"},
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: map[string]string{"app": "unrelated"},
			Ports:    []corev1.ServicePort{{Name: "http", Port: 80}},
		},
	}
	pool := &openvoxv1alpha1.Pool{
		ObjectMeta: metav1.ObjectMeta{Name: "puppet", Namespace: testNamespace},
	}

	c := setupTestClient(foreign, pool)
	r := newPoolReconciler(c, false)

	_, err := r.Reconcile(testCtx(), testRequest("puppet"))
	if err == nil {
		t.Fatal("expected the reconcile to fail rather than take over a foreign Service")
	}
	if !strings.Contains(err.Error(), "not controlled by") {
		t.Errorf("the error should explain the ownership conflict, got: %v", err)
	}

	got := &corev1.Service{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "puppet", Namespace: testNamespace}, got); err != nil {
		t.Fatalf("reading the foreign Service: %v", err)
	}
	if got.Labels["owner"] != "somebody-else" {
		t.Error("the foreign Service was modified")
	}
	if got.Spec.Selector["app"] != "unrelated" {
		t.Errorf("the foreign Service selector was overwritten: %v", got.Spec.Selector)
	}
	if len(got.OwnerReferences) != 0 {
		t.Errorf("an owner reference was added to a foreign Service: %v", got.OwnerReferences)
	}
}

// TestReconcilePDB_NoEventWhenUnchanged pins that repeated reconciles stay
// quiet. Previously every pass wrote the object and emitted an "updated" event.
func TestReconcilePDB_NoEventWhenUnchanged(t *testing.T) {
	enabled := true
	server := newServer("web")
	server.Spec.PDB = &openvoxv1alpha1.PDBSpec{Enabled: enabled}

	c := setupTestClient(server)
	rec := events.NewFakeRecorder(50)
	r := &ServerReconciler{Client: c, Scheme: testScheme(), Recorder: rec}

	if err := r.reconcilePDB(testCtx(), server); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	drain(rec)

	for i := range 3 {
		if err := r.reconcilePDB(testCtx(), server); err != nil {
			t.Fatalf("reconcile %d: %v", i+2, err)
		}
	}
	if n := drain(rec); n != 0 {
		t.Errorf("expected no events when nothing changed, got %d", n)
	}
}

// TestReconcilePDB_RecreatesDeletedChild covers the other direction: a child
// removed out of band has to come back.
func TestReconcilePDB_RecreatesDeletedChild(t *testing.T) {
	server := newServer("web")
	server.Spec.PDB = &openvoxv1alpha1.PDBSpec{Enabled: true}

	c := setupTestClient(server)
	r := newServerReconciler(c)
	key := types.NamespacedName{Name: "web", Namespace: testNamespace}

	if err := r.reconcilePDB(testCtx(), server); err != nil {
		t.Fatalf("first reconcile: %v", err)
	}
	pdb := &policyv1.PodDisruptionBudget{}
	if err := c.Get(testCtx(), key, pdb); err != nil {
		t.Fatalf("the PDB should exist after the first reconcile: %v", err)
	}

	if err := c.Delete(testCtx(), pdb); err != nil {
		t.Fatalf("deleting the PDB: %v", err)
	}
	if err := r.reconcilePDB(testCtx(), server); err != nil {
		t.Fatalf("second reconcile: %v", err)
	}
	if err := c.Get(testCtx(), key, &policyv1.PodDisruptionBudget{}); err != nil {
		t.Errorf("a deleted child must be recreated, got: %v", err)
	}
}

// TestReconcilePDB_DoesNotDeleteUnownedResource guards the disable path: it
// must not remove a foreign object that shares the name.
func TestReconcilePDB_DoesNotDeleteUnownedResource(t *testing.T) {
	foreign := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: testNamespace},
	}
	server := newServer("web") // PDB not enabled

	c := setupTestClient(foreign, server)
	r := newServerReconciler(c)

	if err := r.reconcilePDB(testCtx(), server); err == nil {
		t.Fatal("expected an ownership error instead of deleting a foreign PDB")
	}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "web", Namespace: testNamespace},
		&policyv1.PodDisruptionBudget{}); apierrors.IsNotFound(err) {
		t.Error("the foreign PDB was deleted")
	}
}

// drain empties the recorder channel and reports how many events it held.
func drain(rec *events.FakeRecorder) int {
	n := 0
	for {
		select {
		case <-rec.Events:
			n++
		default:
			return n
		}
	}
}

// TestPoolRouteHostname_IsDerivedNotWritten replaces the old injection test.
// The Pool no longer writes into the Certificate spec: the hostname is derived
// on read, so the Certificate's spec stays owned by whoever wrote it and a
// GitOps controller has nothing to revert.
func TestPoolRouteHostname_IsDerivedNotWritten(t *testing.T) {
	ca := newCertificateAuthority("production-ca")
	cert := newCertificate("web-cert", "production-ca", openvoxv1alpha1.CertificatePhaseSigned)
	cert.Status.SignedSpecHash = specHash(cert)
	notAfter := metav1.NewTime(metav1.Now().Add(365 * 24 * time.Hour))
	cert.Status.NotAfter = &notAfter

	server := newServer("web")
	server.Spec.CertificateRef = "web-cert"
	server.Spec.PoolRefs = []string{"puppet"}

	pool := newPool("puppet", withRoute("puppet.example.com", "gw"))
	pool.Spec.Route.InjectDNSAltName = true

	c := setupTestClient(ca, cert, server, pool)

	if _, err := newPoolReconciler(c, true).Reconcile(testCtx(), testRequest("puppet")); err != nil {
		t.Fatalf("reconciling the Pool: %v", err)
	}

	key := types.NamespacedName{Name: "web-cert", Namespace: testNamespace}
	after := &openvoxv1alpha1.Certificate{}
	if err := c.Get(testCtx(), key, after); err != nil {
		t.Fatalf("reading Certificate: %v", err)
	}
	if slices.Contains(after.Spec.DNSAltNames, "puppet.example.com") {
		t.Error("the Pool must not write into the Certificate spec any more")
	}

	// The Certificate controller derives the same name instead.
	cr := newCertificateReconciler(c)
	names, err := cr.effectiveDNSAltNames(testCtx(), after)
	if err != nil {
		t.Fatalf("deriving alt names: %v", err)
	}
	if !slices.Contains(names, "puppet.example.com") {
		t.Errorf("the route hostname must be part of the effective names, got %v", names)
	}

	// And the drift it causes is what makes the controller re-sign.
	if after.Status.SignedSpecHash == signingSpecHash(after, names) {
		t.Error("the recorded hash should differ once the hostname joins the effective names")
	}
}
