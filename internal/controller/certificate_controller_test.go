package controller

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	clocktesting "k8s.io/utils/clock/testing"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

func TestCertReconcile_NotFound(t *testing.T) {
	c := setupTestClient()
	r := newCertificateReconciler(c)

	res, err := r.Reconcile(testCtx(), testRequest("nonexistent"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.RequeueAfter != 0 {
		t.Error("expected no requeue for missing Certificate")
	}
}

func TestCertReconcile_CANotFound(t *testing.T) {
	cert := newCertificate("my-cert", "missing-ca", "")
	c := setupTestClient(cert)
	r := newCertificateReconciler(c)

	res, err := r.Reconcile(testCtx(), testRequest("my-cert"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// When CA is not found, controller returns empty result (no requeue)
	if res.RequeueAfter != 0 {
		t.Errorf("expected no requeue when CA not found, got %v", res.RequeueAfter)
	}
}

func TestCertReconcile_CANotReady(t *testing.T) {
	cert := newCertificate("my-cert", "test-ca", "")
	ca := newCertificateAuthority("test-ca")
	ca.Status.Phase = openvoxv1alpha1.CertificateAuthorityPhasePending // not ready

	c := setupTestClient(cert, ca)
	r := newCertificateReconciler(c)

	res, err := r.Reconcile(testCtx(), testRequest("my-cert"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.RequeueAfter != 10*time.Second {
		t.Errorf("expected requeue after 10s, got %v", res.RequeueAfter)
	}

	updated := &openvoxv1alpha1.Certificate{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "my-cert", Namespace: testNamespace}, updated); err != nil {
		t.Fatalf("failed to get Certificate: %v", err)
	}
	if updated.Status.Phase != openvoxv1alpha1.CertificatePhasePending {
		t.Errorf("expected phase %q, got %q", openvoxv1alpha1.CertificatePhasePending, updated.Status.Phase)
	}
}

func TestCertReconcile_TLSSecretExists(t *testing.T) {
	cert := newCertificate("my-cert", "test-ca", "")
	ca := newCertificateAuthority("test-ca")
	tlsSecret := newSecret("my-cert-tls", map[string][]byte{
		"cert.pem": []byte("signed-cert"),
		"key.pem":  []byte("private-key"),
	})

	c := setupTestClient(cert, ca, tlsSecret)
	r := newCertificateReconciler(c)

	res, err := r.Reconcile(testCtx(), testRequest("my-cert"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	updated := &openvoxv1alpha1.Certificate{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "my-cert", Namespace: testNamespace}, updated); err != nil {
		t.Fatalf("failed to get Certificate: %v", err)
	}
	if updated.Status.Phase != openvoxv1alpha1.CertificatePhaseSigned {
		t.Errorf("expected phase %q, got %q", openvoxv1alpha1.CertificatePhaseSigned, updated.Status.Phase)
	}
	if updated.Status.SecretName != "my-cert-tls" {
		t.Errorf("expected SecretName %q, got %q", "my-cert-tls", updated.Status.SecretName)
	}

	// Verify CertSigned condition
	found := false
	for _, cond := range updated.Status.Conditions {
		if cond.Type == openvoxv1alpha1.ConditionCertSigned {
			found = true
			if cond.Status != "True" {
				t.Errorf("expected condition status True, got %q", cond.Status)
			}
		}
	}
	if !found {
		t.Error("CertSigned condition not set")
	}

	// Non-parseable cert data means NotAfter is nil, so should requeue
	if res.RequeueAfter != 5*time.Second {
		t.Errorf("expected requeue after 5s when NotAfter is nil, got %v", res.RequeueAfter)
	}
}

func TestCertReconcile_PhasePending(t *testing.T) {
	cert := newCertificate("my-cert", "test-ca", "")
	ca := newCertificateAuthority("test-ca")
	ca.Status.Phase = openvoxv1alpha1.CertificateAuthorityPhasePending

	c := setupTestClient(cert, ca)
	r := newCertificateReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("my-cert")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	updated := &openvoxv1alpha1.Certificate{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "my-cert", Namespace: testNamespace}, updated); err != nil {
		t.Fatalf("failed to get Certificate: %v", err)
	}
	if updated.Status.Phase != openvoxv1alpha1.CertificatePhasePending {
		t.Errorf("expected phase %q, got %q", openvoxv1alpha1.CertificatePhasePending, updated.Status.Phase)
	}
}

func TestCertReconcile_CAExternalPhase_Accepted(t *testing.T) {
	cert := newCertificate("my-cert", "ext-ca", "")
	ca := newCertificateAuthority("ext-ca", withExternal("https://puppet-ca.example.com:8140"))
	ca.Status.Phase = openvoxv1alpha1.CertificateAuthorityPhaseExternal
	// Pre-create the TLS secret so reconcile completes without HTTP calls
	tlsSecret := newSecret("my-cert-tls", map[string][]byte{
		"cert.pem": []byte("signed-cert"),
		"key.pem":  []byte("private-key"),
	})

	c := setupTestClient(cert, ca, tlsSecret)
	r := newCertificateReconciler(c)

	res, err := r.Reconcile(testCtx(), testRequest("my-cert"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should NOT requeue with 10s (that would mean CA was treated as not-ready)
	if res.RequeueAfter == 10*time.Second {
		t.Error("certificate should not wait for CA when phase is External")
	}

	updated := &openvoxv1alpha1.Certificate{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "my-cert", Namespace: testNamespace}, updated); err != nil {
		t.Fatalf("failed to get Certificate: %v", err)
	}
	if updated.Status.Phase != openvoxv1alpha1.CertificatePhaseSigned {
		t.Errorf("expected phase %q, got %q", openvoxv1alpha1.CertificatePhaseSigned, updated.Status.Phase)
	}
}

func TestCertReconcile_RenewalScheduled(t *testing.T) {
	// Cert with NotAfter 90 days out (>60d default renewBefore) should schedule a requeue
	certPEM, keyPEM := generateTestCertWithExpiry(t, 90*24*time.Hour)

	cert := newCertificate("my-cert", "test-ca", "")
	ca := newCertificateAuthority("test-ca")
	tlsSecret := newSecret("my-cert-tls", map[string][]byte{
		"cert.pem": certPEM,
		"key.pem":  keyPEM,
	})

	c := setupTestClient(cert, ca, tlsSecret)
	r := newCertificateReconciler(c)

	res, err := r.Reconcile(testCtx(), testRequest("my-cert"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should have a RequeueAfter > 0 (renewal check scheduled)
	if res.RequeueAfter == 0 {
		t.Error("expected RequeueAfter > 0 for signed cert with NotAfter")
	}
	// Should not trigger renewal yet (90d > 60d renewBefore)
	updated := &openvoxv1alpha1.Certificate{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "my-cert", Namespace: testNamespace}, updated); err != nil {
		t.Fatalf("failed to get Certificate: %v", err)
	}
	if updated.Status.Phase != openvoxv1alpha1.CertificatePhaseSigned {
		t.Errorf("expected phase %q (not yet renewing), got %q", openvoxv1alpha1.CertificatePhaseSigned, updated.Status.Phase)
	}
	// RequeueAfter should be capped at maxRenewalCheckInterval (12h)
	if res.RequeueAfter > maxRenewalCheckInterval {
		t.Errorf("expected RequeueAfter <= %v, got %v", maxRenewalCheckInterval, res.RequeueAfter)
	}
}

func TestCertReconcile_RenewalTriggered(t *testing.T) {
	// Cert with NotAfter 30 days out (<60d default renewBefore) should trigger renewal
	certPEM, keyPEM := generateTestCertWithExpiry(t, 30*24*time.Hour)

	cert := newCertificate("my-cert", "test-ca", "")
	ca := newCertificateAuthority("test-ca")
	tlsSecret := newSecret("my-cert-tls", map[string][]byte{
		"cert.pem": certPEM,
		"key.pem":  keyPEM,
	})

	c := setupTestClient(cert, ca, tlsSecret)
	r := newCertificateReconciler(c)

	res, err := r.Reconcile(testCtx(), testRequest("my-cert"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should trigger immediate requeue (renewal triggered)
	if res.RequeueAfter == 0 {
		t.Error("expected RequeueAfter > 0 when renewal is triggered")
	}

	updated := &openvoxv1alpha1.Certificate{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "my-cert", Namespace: testNamespace}, updated); err != nil {
		t.Fatalf("failed to get Certificate: %v", err)
	}
	if updated.Status.Phase != openvoxv1alpha1.CertificatePhaseRenewing {
		t.Errorf("expected phase %q, got %q", openvoxv1alpha1.CertificatePhaseRenewing, updated.Status.Phase)
	}
}

func TestCertReconcile_SignedCertNoRequeue_BecomesRequeue(t *testing.T) {
	// Verify that a signed cert with valid NotAfter now returns RequeueAfter > 0
	// (the old behavior was ctrl.Result{} with no requeue)
	certPEM, keyPEM := generateTestCertWithExpiry(t, 365*24*time.Hour)

	cert := newCertificate("my-cert", "test-ca", "")
	ca := newCertificateAuthority("test-ca")
	tlsSecret := newSecret("my-cert-tls", map[string][]byte{
		"cert.pem": certPEM,
		"key.pem":  keyPEM,
	})

	c := setupTestClient(cert, ca, tlsSecret)
	r := newCertificateReconciler(c)

	res, err := r.Reconcile(testCtx(), testRequest("my-cert"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Must have a requeue -- no longer returning empty Result
	if res.RequeueAfter == 0 {
		t.Error("expected RequeueAfter > 0 for signed cert with NotAfter set")
	}
}

func TestCertReconcile_ExpiringWarningEvents(t *testing.T) {
	// Cert expiring in 5 days should trigger the 7-day warning but not 1-day
	certPEM, keyPEM := generateTestCertWithExpiry(t, 5*24*time.Hour)

	cert := newCertificate("my-cert", "test-ca", "")
	cert.Spec.RenewBefore = "1d" // only 1 day renewBefore so it doesn't immediately renew
	ca := newCertificateAuthority("test-ca")
	tlsSecret := newSecret("my-cert-tls", map[string][]byte{
		"cert.pem": certPEM,
		"key.pem":  keyPEM,
	})

	c := setupTestClient(cert, ca, tlsSecret)
	r := newCertificateReconciler(c)

	_, err := r.Reconcile(testCtx(), testRequest("my-cert"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify phase is still Signed (within 7d warning but renewBefore=1d hasn't triggered yet)
	updated := &openvoxv1alpha1.Certificate{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "my-cert", Namespace: testNamespace}, updated); err != nil {
		t.Fatalf("failed to get Certificate: %v", err)
	}
	if updated.Status.Phase != openvoxv1alpha1.CertificatePhaseSigned {
		t.Errorf("expected phase %q, got %q", openvoxv1alpha1.CertificatePhaseSigned, updated.Status.Phase)
	}
}

func TestParseCertRenewBefore(t *testing.T) {
	tests := []struct {
		renewBefore string
		expected    time.Duration
	}{
		{"", 60 * 24 * time.Hour},        // default 60 days
		{"30d", 30 * 24 * time.Hour},     // 30 days
		{"720h", 720 * time.Hour},        // 720 hours
		{"invalid", 60 * 24 * time.Hour}, // falls back to default
	}

	for _, tt := range tests {
		cert := &openvoxv1alpha1.Certificate{
			Spec: openvoxv1alpha1.CertificateSpec{
				RenewBefore: tt.renewBefore,
			},
		}
		got := parseCertRenewBefore(cert)
		if got != tt.expected {
			t.Errorf("parseCertRenewBefore(%q) = %v, want %v", tt.renewBefore, got, tt.expected)
		}
	}
}

func TestScheduleRenewalCheck_NotAfterNil(t *testing.T) {
	cert := &openvoxv1alpha1.Certificate{
		ObjectMeta: metav1.ObjectMeta{Name: "test", Namespace: testNamespace},
	}
	c := setupTestClient(cert)
	r := newCertificateReconciler(c)

	res, err := r.scheduleRenewalCheck(testCtx(), cert)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.RequeueAfter != RequeueIntervalShort {
		t.Errorf("expected RequeueAfter %v, got %v", RequeueIntervalShort, res.RequeueAfter)
	}
}

func TestScheduleRenewalCheck_CooldownPreventsLoop(t *testing.T) {
	// Simulate: renewBefore=365d but cert only lives 30d, recently renewed
	certPEM, keyPEM := generateTestCertWithExpiry(t, 30*24*time.Hour)
	_ = keyPEM

	notAfter := parseCertNotAfter(testCtx(), certPEM)
	cert := &openvoxv1alpha1.Certificate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-cert",
			Namespace: testNamespace,
			Annotations: map[string]string{
				AnnotationLastRenewalTime: time.Now().UTC().Format(time.RFC3339),
			},
		},
		Spec: openvoxv1alpha1.CertificateSpec{
			AuthorityRef: "test-ca",
			Certname:     "puppet",
			RenewBefore:  "365d",
		},
	}
	cert.Status.Phase = openvoxv1alpha1.CertificatePhaseSigned
	cert.Status.NotAfter = notAfter

	c := setupTestClient(cert)
	r := newCertificateReconciler(c)

	res, err := r.scheduleRenewalCheck(testCtx(), cert)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should NOT trigger renewal (cooldown active), should requeue after cooldown
	if res.RequeueAfter != minRenewalCooldown {
		t.Errorf("expected RequeueAfter %v (cooldown), got %v", minRenewalCooldown, res.RequeueAfter)
	}

	// Phase should remain Signed (not Renewing)
	updated := &openvoxv1alpha1.Certificate{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "my-cert", Namespace: testNamespace}, updated); err != nil {
		t.Fatalf("failed to get Certificate: %v", err)
	}
	if updated.Status.Phase != openvoxv1alpha1.CertificatePhaseSigned {
		t.Errorf("expected phase %q during cooldown, got %q", openvoxv1alpha1.CertificatePhaseSigned, updated.Status.Phase)
	}
}

func TestIsWithinRenewalCooldown(t *testing.T) {
	now := time.Date(2026, 4, 18, 12, 0, 0, 0, time.UTC)
	fakeClock := clocktesting.NewFakePassiveClock(now)

	c := setupTestClient()
	r := newCertificateReconciler(c)
	r.Clock = fakeClock

	// No annotation -> not in cooldown
	cert := &openvoxv1alpha1.Certificate{}
	if r.isWithinRenewalCooldown(cert) {
		t.Error("expected no cooldown without annotation")
	}

	// Renewed 10 minutes ago -> in cooldown
	cert.Annotations = map[string]string{
		AnnotationLastRenewalTime: now.Add(-10 * time.Minute).Format(time.RFC3339),
	}
	if !r.isWithinRenewalCooldown(cert) {
		t.Error("expected cooldown with recent renewal annotation")
	}

	// Renewed 2 hours ago -> not in cooldown
	cert.Annotations[AnnotationLastRenewalTime] = now.Add(-2 * time.Hour).Format(time.RFC3339)
	if r.isWithinRenewalCooldown(cert) {
		t.Error("expected no cooldown with old renewal annotation")
	}
}
