package controller

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// TestCertnameOf_DefaultsLikeTheCRD pins the reason this collision is easy to
// hit: an unset certname is not "none", it is "puppet".
func TestCertnameOf_DefaultsLikeTheCRD(t *testing.T) {
	cert := &openvoxv1alpha1.Certificate{}
	if got := certnameOf(cert); got != "puppet" {
		t.Errorf("expected the CRD default puppet, got %q", got)
	}
	cert.Spec.Certname = "web01.example.com"
	if got := certnameOf(cert); got != "web01.example.com" {
		t.Errorf("expected the explicit certname, got %q", got)
	}
}

// TestCertnameConflict_RefusesToSign is the guarantee: the CA keeps one entry
// per certname, so a second Certificate claiming the same name must not sign
// under it. Without this the second resource adopts the first one's
// certificate, and deleting either revokes the entry both rely on.
func TestCertnameConflict_RefusesToSign(t *testing.T) {
	ca := newCertificateAuthority("production-ca")
	meta.SetStatusCondition(&ca.Status.Conditions, metav1.Condition{
		Type: openvoxv1alpha1.ConditionCAReady, Status: metav1.ConditionTrue,
		Reason: "Ready", Message: "ready",
	})
	first := newCertificate("web-a", "production-ca", openvoxv1alpha1.CertificatePhaseSigned)
	first.Spec.Certname = "shared.example.com"
	second := newCertificate("web-b", "production-ca", openvoxv1alpha1.CertificatePhasePending)
	second.Spec.Certname = "shared.example.com"

	c := setupTestClient(ca, first, second)
	r := newCertificateReconciler(c)

	res, err := r.Reconcile(testCtx(), testRequest("web-b"))
	if err != nil {
		t.Fatalf("a certname conflict must not fail the reconcile: %v", err)
	}
	if res.RequeueAfter != 0 {
		t.Errorf("a certname conflict is permanent and must not be polled, got %v", res.RequeueAfter)
	}

	got := &openvoxv1alpha1.Certificate{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "web-b", Namespace: testNamespace}, got); err != nil {
		t.Fatalf("reading the Certificate back: %v", err)
	}
	cond := meta.FindStatusCondition(got.Status.Conditions, openvoxv1alpha1.ConditionCertnameConflict)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Fatalf("expected a CertnameConflict condition, got %+v", cond)
	}
	if got.Status.Phase != openvoxv1alpha1.CertificatePhaseError {
		t.Errorf("expected phase Error, got %q", got.Status.Phase)
	}

	// No TLS Secret may be produced under a name it does not own.
	if err := c.Get(testCtx(), types.NamespacedName{Name: "web-b-tls", Namespace: testNamespace},
		newSecret("web-b-tls", nil)); err == nil {
		t.Error("the conflicting Certificate must not produce a TLS Secret")
	}
}

// TestCertnameConflict_DefaultCertnameCollides is the accidental case: two
// Certificates created without a certname both land on "puppet".
func TestCertnameConflict_DefaultCertnameCollides(t *testing.T) {
	ca := newCertificateAuthority("production-ca")
	meta.SetStatusCondition(&ca.Status.Conditions, metav1.Condition{
		Type: openvoxv1alpha1.ConditionCAReady, Status: metav1.ConditionTrue,
		Reason: "Ready", Message: "ready",
	})
	first := newCertificate("web-a", "production-ca", openvoxv1alpha1.CertificatePhaseSigned)
	first.Spec.Certname = ""
	second := newCertificate("web-b", "production-ca", openvoxv1alpha1.CertificatePhasePending)
	second.Spec.Certname = ""

	c := setupTestClient(ca, first, second)
	r := newCertificateReconciler(c)
	if _, err := r.Reconcile(testCtx(), testRequest("web-b")); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	got := &openvoxv1alpha1.Certificate{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "web-b", Namespace: testNamespace}, got); err != nil {
		t.Fatalf("reading the Certificate back: %v", err)
	}
	if cond := meta.FindStatusCondition(got.Status.Conditions, openvoxv1alpha1.ConditionCertnameConflict); cond == nil {
		t.Error("two Certificates without an explicit certname both use puppet and must conflict")
	}
}

// TestCertnameConflict_DifferentAuthoritiesDoNotCollide bounds the rule: the
// name only has to be unique per CA, since each CA keeps its own entries.
func TestCertnameConflict_DifferentAuthoritiesDoNotCollide(t *testing.T) {
	first := newCertificate("web-a", "ca-one", openvoxv1alpha1.CertificatePhaseSigned)
	second := newCertificate("web-b", "ca-two", openvoxv1alpha1.CertificatePhasePending)

	c := setupTestClient(first, second)
	r := newCertificateReconciler(c)

	other, err := r.otherCertificateUsingCertname(testCtx(), second)
	if err != nil {
		t.Fatalf("looking up claimants: %v", err)
	}
	if other != "" {
		t.Errorf("certificates against different CAs must not conflict, got %q", other)
	}
}

// TestCertnameConflict_TerminatingCertificateReleasesTheName keeps a deletion
// in progress from blocking its replacement forever.
func TestCertnameConflict_TerminatingCertificateReleasesTheName(t *testing.T) {
	now := metav1.Now()
	leaving := newCertificate("web-a", "production-ca", openvoxv1alpha1.CertificatePhaseSigned)
	leaving.DeletionTimestamp = &now
	leaving.Finalizers = []string{certificateFinalizer}
	successor := newCertificate("web-b", "production-ca", openvoxv1alpha1.CertificatePhasePending)

	c := setupTestClient(leaving, successor)
	r := newCertificateReconciler(c)

	other, err := r.otherCertificateUsingCertname(testCtx(), successor)
	if err != nil {
		t.Fatalf("looking up claimants: %v", err)
	}
	if other != "" {
		t.Errorf("a terminating Certificate must release its certname, got %q", other)
	}
}

// --- key matching ---

// selfSignedFor builds a certificate for a freshly generated key, so the pair
// is internally consistent but unrelated to any other key.
func selfSignedFor(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(7),
		Subject:      pkix.Name{CommonName: "shared.example.com"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("creating certificate: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
}

// TestCertMatchesKey_AcceptsItsOwnPair and the rejection case below are the
// last line of defence: with webhooks off and two Certificates racing, the CA
// hands back whatever holds the certname. Pairing that with our own key would
// only fail later, during a TLS handshake.
func TestCertMatchesKey_AcceptsItsOwnPair(t *testing.T) {
	certPEM, keyPEM := selfSignedFor(t)

	ok, err := certMatchesKey(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("verifying a matching pair: %v", err)
	}
	if !ok {
		t.Error("a certificate must match the key it was issued for")
	}
}

func TestCertMatchesKey_RejectsAForeignCertificate(t *testing.T) {
	foreignCert, _ := selfSignedFor(t)
	_, ourKey := selfSignedFor(t)

	ok, err := certMatchesKey(foreignCert, ourKey)
	if err != nil {
		t.Fatalf("verifying a mismatched pair: %v", err)
	}
	if ok {
		t.Error("a certificate issued for another key must be rejected")
	}
}

func TestCertMatchesKey_ReportsUnusableInput(t *testing.T) {
	_, keyPEM := selfSignedFor(t)

	if _, err := certMatchesKey([]byte("not a pem"), keyPEM); err == nil {
		t.Error("expected an error for an undecodable certificate")
	}
}
