package controller

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	testclock "k8s.io/utils/clock/testing"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// signedCert builds a Certificate that is already signed, with the spec hash
// and expiry a freshly issued certificate would have.
func signedCert(notAfter time.Time) *openvoxv1alpha1.Certificate {
	cert := newCertificate("web", "production-ca", openvoxv1alpha1.CertificatePhaseSigned)
	cert.Spec.DNSAltNames = []string{"puppet.example.com"}
	cert.Status.SignedSpecHash = specHash(cert)
	t := metav1.NewTime(notAfter)
	cert.Status.NotAfter = &t
	return cert
}

func TestSigningSpecHash(t *testing.T) {
	base := signedCert(time.Now().Add(365 * 24 * time.Hour))

	t.Run("stable across equal specs", func(t *testing.T) {
		if specHash(base.DeepCopy()) != specHash(base) {
			t.Error("two certificates with the same spec must hash the same")
		}
	})

	t.Run("independent of dnsAltNames order", func(t *testing.T) {
		a := base.DeepCopy()
		a.Spec.DNSAltNames = []string{"a.example.com", "b.example.com"}
		b := base.DeepCopy()
		b.Spec.DNSAltNames = []string{"b.example.com", "a.example.com"}
		if specHash(a) != specHash(b) {
			t.Error("reordering dnsAltNames must not change the hash")
		}
	})

	t.Run("changes with certname", func(t *testing.T) {
		other := base.DeepCopy()
		other.Spec.Certname = "different"
		if specHash(other) == specHash(base) {
			t.Error("certname must affect the hash")
		}
	})

	t.Run("changes with a new alt name", func(t *testing.T) {
		other := base.DeepCopy()
		other.Spec.DNSAltNames = append(other.Spec.DNSAltNames, "extra.example.com")
		if specHash(other) == specHash(base) {
			t.Error("an added alt name must affect the hash")
		}
	})

	t.Run("changes with csr extensions", func(t *testing.T) {
		other := base.DeepCopy()
		other.Spec.CSRExtensions = &openvoxv1alpha1.CSRExtensionsSpec{PpRole: "compiler"}
		if specHash(other) == specHash(base) {
			t.Error("csrExtensions must affect the hash")
		}
	})

	t.Run("ignores renewBefore", func(t *testing.T) {
		other := base.DeepCopy()
		other.Spec.RenewBefore = "10d"
		if specHash(other) != specHash(base) {
			t.Error("renewBefore only moves the renewal point and must not affect the hash")
		}
	})
}

// TestRenewalDue_DerivedFromObservedState pins that the renewal decision comes
// from notAfter, renewBefore and the cooldown annotation -- never from
// status.phase.
func TestRenewalDue_DerivedFromObservedState(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	r := &CertificateReconciler{Clock: testclock.NewFakePassiveClock(now)}

	t.Run("not due outside the window", func(t *testing.T) {
		cert := signedCert(now.Add(200 * 24 * time.Hour))
		cert.Spec.RenewBefore = "60d"
		if r.renewalDue(cert) {
			t.Error("renewal should not be due 200 days before expiry with renewBefore 60d")
		}
	})

	t.Run("due inside the window", func(t *testing.T) {
		cert := signedCert(now.Add(30 * 24 * time.Hour))
		cert.Spec.RenewBefore = "60d"
		if !r.renewalDue(cert) {
			t.Error("renewal should be due 30 days before expiry with renewBefore 60d")
		}
	})

	t.Run("suppressed by the cooldown annotation", func(t *testing.T) {
		cert := signedCert(now.Add(30 * 24 * time.Hour))
		cert.Spec.RenewBefore = "60d"
		cert.Annotations = map[string]string{
			AnnotationLastRenewalTime: now.Add(-1 * time.Minute).Format(time.RFC3339),
		}
		if r.renewalDue(cert) {
			t.Error("a recent renewal must suppress the next one")
		}
	})

	t.Run("phase is irrelevant", func(t *testing.T) {
		cert := signedCert(now.Add(30 * 24 * time.Hour))
		cert.Spec.RenewBefore = "60d"
		cert.Status.Phase = ""
		if !r.renewalDue(cert) {
			t.Error("clearing status.phase must not change the renewal decision")
		}
	})
}

// TestReconcile_ResignsOnSpecDrift is the behaviour the Pool controller used to
// force by resetting a foreign status: changing dnsAltNames on a signed
// certificate has to take effect without anyone touching the status.
func TestReconcile_ResignsOnSpecDrift(t *testing.T) {
	ca := newCertificateAuthority("production-ca")
	ca.Status.Phase = openvoxv1alpha1.CertificateAuthorityPhaseReady
	cert := signedCert(time.Now().Add(365 * 24 * time.Hour))

	c := setupTestClient(ca, cert)
	r := newCertificateReconciler(c)

	key := types.NamespacedName{Name: "web", Namespace: testNamespace}

	// No drift: the phase stays Signed.
	if _, err := r.Reconcile(testCtx(), testRequest("web")); err != nil {
		t.Fatalf("reconcile without drift: %v", err)
	}
	got := &openvoxv1alpha1.Certificate{}
	if err := c.Get(testCtx(), key, got); err != nil {
		t.Fatalf("reading Certificate: %v", err)
	}
	if got.Status.Phase != openvoxv1alpha1.CertificatePhaseSigned {
		t.Fatalf("phase should stay Signed without drift, got %s", got.Status.Phase)
	}

	// Drift: a new alt name must pull the certificate out of the Signed state.
	got.Spec.DNSAltNames = append(got.Spec.DNSAltNames, "extra.example.com")
	if err := c.Update(testCtx(), got); err != nil {
		t.Fatalf("updating dnsAltNames: %v", err)
	}
	if _, err := r.Reconcile(testCtx(), testRequest("web")); err != nil {
		t.Logf("reconcile after drift returned: %v (signing against a fake CA cannot succeed)", err)
	}

	after := &openvoxv1alpha1.Certificate{}
	if err := c.Get(testCtx(), key, after); err != nil {
		t.Fatalf("reading Certificate after drift: %v", err)
	}
	if after.Status.Phase == openvoxv1alpha1.CertificatePhaseSigned {
		t.Errorf("a signing-relevant spec change must leave the Signed state, phase is still %s", after.Status.Phase)
	}
}

// TestReconcile_AdoptsMissingHashWithoutResigning covers the upgrade path:
// certificates issued before the hash existed must not all be re-signed on the
// first reconcile after an operator upgrade.
func TestReconcile_AdoptsMissingHashWithoutResigning(t *testing.T) {
	ca := newCertificateAuthority("production-ca")
	ca.Status.Phase = openvoxv1alpha1.CertificateAuthorityPhaseReady
	cert := signedCert(time.Now().Add(365 * 24 * time.Hour))
	cert.Status.SignedSpecHash = "" // pre-upgrade state

	c := setupTestClient(ca, cert)
	r := newCertificateReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("web")); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	got := &openvoxv1alpha1.Certificate{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "web", Namespace: testNamespace}, got); err != nil {
		t.Fatalf("reading Certificate: %v", err)
	}
	if got.Status.Phase != openvoxv1alpha1.CertificatePhaseSigned {
		t.Errorf("a missing hash must be adopted, not re-signed; phase is %s", got.Status.Phase)
	}
	if got.Status.SignedSpecHash != specHash(got) {
		t.Errorf("the current spec hash should have been recorded, got %q", got.Status.SignedSpecHash)
	}
}
