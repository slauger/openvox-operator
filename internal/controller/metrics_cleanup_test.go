package controller

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// TestMetrics_ServerSeriesRemovedOnDeletion covers the series that used to
// outlive its Server: an alert on replicas_ready == 0 kept firing for a
// resource that no longer existed.
func TestMetrics_ServerSeriesRemovedOnDeletion(t *testing.T) {
	serverReplicasDesired.Reset()
	serverReplicasReady.Reset()
	serverReplicasDesired.WithLabelValues("gone", testNamespace).Set(3)
	serverReplicasReady.WithLabelValues("gone", testNamespace).Set(3)

	// The Server is absent from the client, which is what the controller sees
	// once the object has been collected.
	r := newServerReconciler(setupTestClient())
	if _, err := r.Reconcile(testCtx(), testRequest("gone")); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if n := testutil.CollectAndCount(serverReplicasDesired); n != 0 {
		t.Errorf("openvox_server_replicas_desired still reports %d series for a deleted Server", n)
	}
	if n := testutil.CollectAndCount(serverReplicasReady); n != 0 {
		t.Errorf("openvox_server_replicas_ready still reports %d series for a deleted Server", n)
	}
}

// TestMetrics_ServerSeriesOfOtherResourcesSurvive guards the cleanup against
// being too eager: only the deleted Server loses its series.
func TestMetrics_ServerSeriesOfOtherResourcesSurvive(t *testing.T) {
	serverReplicasReady.Reset()
	serverReplicasReady.WithLabelValues("gone", testNamespace).Set(1)
	serverReplicasReady.WithLabelValues("still-there", testNamespace).Set(2)

	r := newServerReconciler(setupTestClient())
	if _, err := r.Reconcile(testCtx(), testRequest("gone")); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if n := testutil.CollectAndCount(serverReplicasReady); n != 1 {
		t.Errorf("expected exactly the surviving Server series, got %d", n)
	}
	if v := testutil.ToFloat64(serverReplicasReady.WithLabelValues("still-there", testNamespace)); v != 2 {
		t.Errorf("the surviving Server lost its value: got %v, want 2", v)
	}
}

// TestMetrics_CertificateExpirySeriesRemovedOnDeletion stops expiry alerts from
// firing for certificates nobody holds any more.
func TestMetrics_CertificateExpirySeriesRemovedOnDeletion(t *testing.T) {
	certificateExpiryTimestamp.Reset()
	certificateExpiryTimestamp.WithLabelValues("gone", testNamespace).Set(1)

	r := newCertificateReconciler(setupTestClient())
	if _, err := r.Reconcile(testCtx(), testRequest("gone")); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if n := testutil.CollectAndCount(certificateExpiryTimestamp); n != 0 {
		t.Errorf("openvox_certificate_expiry_timestamp_seconds still reports %d series for a deleted Certificate", n)
	}
}

// TestMetrics_CARemovesSeriesWhenFinalizerIsReleased takes the path a real
// deletion goes through, rather than the NotFound safety net: the CA is
// terminating, nothing references it, and the finalizer is released.
func TestMetrics_CARemovesSeriesWhenFinalizerIsReleased(t *testing.T) {
	certificateExpiryTimestamp.Reset()
	crlLastRefreshTimestamp.Reset()
	certificateExpiryTimestamp.WithLabelValues("production-ca", testNamespace).Set(1)
	crlLastRefreshTimestamp.WithLabelValues("production-ca", testNamespace).Set(1)

	now := metav1.Now()
	ca := newCertificateAuthority("production-ca")
	ca.DeletionTimestamp = &now
	ca.Finalizers = []string{certificateAuthorityFinalizer}

	r := newCertificateAuthorityReconciler(setupTestClient(ca))
	if _, err := r.Reconcile(testCtx(), testRequest("production-ca")); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if n := testutil.CollectAndCount(certificateExpiryTimestamp); n != 0 {
		t.Errorf("the CA expiry series survived the finalizer release: %d series left", n)
	}
	if n := testutil.CollectAndCount(crlLastRefreshTimestamp); n != 0 {
		t.Errorf("the CRL refresh series survived the finalizer release: %d series left", n)
	}
}

// TestMetrics_CASeriesSurviveBlockedDeletion is the counterpart: while the
// deletion is held back by a Certificate, the CA still exists and its series
// must keep reporting.
func TestMetrics_CASeriesSurviveBlockedDeletion(t *testing.T) {
	certificateExpiryTimestamp.Reset()
	crlLastRefreshTimestamp.Reset()
	certificateExpiryTimestamp.WithLabelValues("production-ca", testNamespace).Set(1)
	crlLastRefreshTimestamp.WithLabelValues("production-ca", testNamespace).Set(1)

	now := metav1.Now()
	ca := newCertificateAuthority("production-ca")
	ca.DeletionTimestamp = &now
	ca.Finalizers = []string{certificateAuthorityFinalizer}
	cert := newCertificate("production-cert", "production-ca", openvoxv1alpha1.CertificatePhaseSigned)

	r := newCertificateAuthorityReconciler(setupTestClient(ca, cert))
	if _, err := r.Reconcile(testCtx(), testRequest("production-ca")); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	if n := testutil.CollectAndCount(certificateExpiryTimestamp); n != 1 {
		t.Errorf("the CA expiry series was dropped while the CA still exists: %d series left", n)
	}
	if n := testutil.CollectAndCount(crlLastRefreshTimestamp); n != 1 {
		t.Errorf("the CRL refresh series was dropped while the CA still exists: %d series left", n)
	}
}
