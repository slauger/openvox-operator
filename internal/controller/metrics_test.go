package controller

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

func TestMetrics_ServerReplicas(t *testing.T) {
	tests := []struct {
		name          string
		replicas      int32
		readyReplicas int32
	}{
		{
			name:          "all replicas ready",
			replicas:      3,
			readyReplicas: 3,
		},
		{
			name:          "partially ready",
			replicas:      3,
			readyReplicas: 1,
		},
		{
			name:          "none ready",
			replicas:      2,
			readyReplicas: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			serverReplicasDesired.Reset()
			serverReplicasReady.Reset()

			objs := append(serverPrereqs(), newServer("metrics-server", withReplicas(tt.replicas)))
			c := setupTestClient(objs...)
			r := newServerReconciler(c)

			// First reconcile creates the Deployment
			if _, err := r.Reconcile(testCtx(), testRequest("metrics-server")); err != nil {
				t.Fatalf("first reconcile error: %v", err)
			}

			// Update Deployment status
			deploy := &appsv1.Deployment{}
			if err := c.Get(testCtx(), types.NamespacedName{Name: "metrics-server", Namespace: testNamespace}, deploy); err != nil {
				t.Fatalf("Deployment not found: %v", err)
			}
			deploy.Status.ReadyReplicas = tt.readyReplicas
			if err := c.Status().Update(testCtx(), deploy); err != nil {
				t.Fatalf("failed to update Deployment status: %v", err)
			}

			// Second reconcile picks up status and sets metrics
			if _, err := r.Reconcile(testCtx(), testRequest("metrics-server")); err != nil {
				t.Fatalf("second reconcile error: %v", err)
			}

			gotDesired := testutil.ToFloat64(serverReplicasDesired.WithLabelValues("metrics-server", testNamespace))
			if gotDesired != float64(tt.replicas) {
				t.Errorf("openvox_server_replicas_desired = %v, want %v", gotDesired, tt.replicas)
			}

			gotReady := testutil.ToFloat64(serverReplicasReady.WithLabelValues("metrics-server", testNamespace))
			if gotReady != float64(tt.readyReplicas) {
				t.Errorf("openvox_server_replicas_ready = %v, want %v", gotReady, tt.readyReplicas)
			}
		})
	}
}

func TestMetrics_CertificateExpiry(t *testing.T) {
	certificateExpiryTimestamp.Reset()

	validity := 90 * 24 * time.Hour
	certPEM, _ := generateTestCertWithExpiry(t, validity)

	cert := newCertificate("test-cert", "test-ca", openvoxv1alpha1.CertificatePhasePending)
	ca := newCertificateAuthority("test-ca")
	ca.Status.Phase = openvoxv1alpha1.CertificateAuthorityPhaseReady
	ca.Status.CASecretName = "test-ca-ca"
	ca.Status.ServiceName = "test-ca-internal"

	objs := []client.Object{
		cert,
		ca,
		newSecret("test-ca-ca", map[string][]byte{"ca_crt.pem": []byte("ca-cert")}),
		newSecret("test-cert-tls", map[string][]byte{
			"cert.pem": certPEM,
			"key.pem":  []byte("key"),
		}),
	}

	c := setupTestClient(objs...)
	r := newCertificateReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("test-cert")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	got := testutil.ToFloat64(certificateExpiryTimestamp.WithLabelValues("test-cert", testNamespace))
	if got == 0 {
		t.Fatal("openvox_certificate_expiry_timestamp_seconds not set")
	}

	// Verify the timestamp is roughly correct (within 1 minute of expected)
	expectedExpiry := time.Now().Add(validity).Unix()
	diff := got - float64(expectedExpiry)
	if diff < -60 || diff > 60 {
		t.Errorf("openvox_certificate_expiry_timestamp_seconds = %v, expected ~%v (diff: %vs)", got, expectedExpiry, diff)
	}
}

func TestMetrics_CAExpiry(t *testing.T) {
	certificateExpiryTimestamp.Reset()

	validity := 5 * 365 * 24 * time.Hour
	caCertPEM, _ := generateTestCertWithExpiry(t, validity)

	ca := newCertificateAuthority("test-ca")
	// Set phase to Ready so the reconciler reaches the NotAfter extraction path
	ca.Status.Phase = openvoxv1alpha1.CertificateAuthorityPhaseReady
	ca.Status.CASecretName = "test-ca-ca"
	ca.Status.ServiceName = "test-ca-internal"

	objs := []client.Object{
		ca,
		// Config with authorityRef is required for the CA reconciler to proceed
		newConfig("test-config", withAuthorityRef("test-ca")),
		newSecret("test-ca-ca", map[string][]byte{"ca_crt.pem": caCertPEM}),
	}

	c := setupTestClient(objs...)
	r := newCertificateAuthorityReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("test-ca")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	got := testutil.ToFloat64(certificateExpiryTimestamp.WithLabelValues("test-ca", testNamespace))
	if got == 0 {
		t.Fatal("openvox_certificate_expiry_timestamp_seconds not set for CA")
	}

	expectedExpiry := time.Now().Add(validity).Unix()
	diff := got - float64(expectedExpiry)
	if diff < -60 || diff > 60 {
		t.Errorf("openvox_certificate_expiry_timestamp_seconds = %v, expected ~%v (diff: %vs)", got, expectedExpiry, diff)
	}
}

func TestMetrics_ServerReplicasMultipleServers(t *testing.T) {
	serverReplicasDesired.Reset()
	serverReplicasReady.Reset()

	objs := append(serverPrereqs(),
		newServer("server-a", withReplicas(2)),
		newServer("server-b", withReplicas(5)),
	)
	c := setupTestClient(objs...)
	r := newServerReconciler(c)

	for _, name := range []string{"server-a", "server-b"} {
		if _, err := r.Reconcile(testCtx(), testRequest(name)); err != nil {
			t.Fatalf("reconcile %s error: %v", name, err)
		}
	}

	// Set different ready counts
	for _, tc := range []struct {
		name  string
		ready int32
	}{
		{"server-a", 2},
		{"server-b", 3},
	} {
		deploy := &appsv1.Deployment{}
		if err := c.Get(testCtx(), types.NamespacedName{Name: tc.name, Namespace: testNamespace}, deploy); err != nil {
			t.Fatalf("Deployment %s not found: %v", tc.name, err)
		}
		deploy.Status.ReadyReplicas = tc.ready
		if err := c.Status().Update(testCtx(), deploy); err != nil {
			t.Fatalf("failed to update %s status: %v", tc.name, err)
		}
		if _, err := r.Reconcile(testCtx(), testRequest(tc.name)); err != nil {
			t.Fatalf("reconcile %s error: %v", tc.name, err)
		}
	}

	if got := testutil.ToFloat64(serverReplicasDesired.WithLabelValues("server-a", testNamespace)); got != 2 {
		t.Errorf("server-a desired = %v, want 2", got)
	}
	if got := testutil.ToFloat64(serverReplicasReady.WithLabelValues("server-a", testNamespace)); got != 2 {
		t.Errorf("server-a ready = %v, want 2", got)
	}
	if got := testutil.ToFloat64(serverReplicasDesired.WithLabelValues("server-b", testNamespace)); got != 5 {
		t.Errorf("server-b desired = %v, want 5", got)
	}
	if got := testutil.ToFloat64(serverReplicasReady.WithLabelValues("server-b", testNamespace)); got != 3 {
		t.Errorf("server-b ready = %v, want 3", got)
	}
}
