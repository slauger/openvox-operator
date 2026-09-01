package controller

import (
	"github.com/prometheus/client_golang/prometheus"
	"sigs.k8s.io/controller-runtime/pkg/metrics"
)

var (
	serverReplicasDesired = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "openvox_server_replicas_desired",
			Help: "Desired number of replicas for an OpenVox Server.",
		},
		[]string{"name", "namespace"},
	)

	serverReplicasReady = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "openvox_server_replicas_ready",
			Help: "Number of ready replicas for an OpenVox Server.",
		},
		[]string{"name", "namespace"},
	)

	certificateExpiryTimestamp = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "openvox_certificate_expiry_timestamp_seconds",
			Help: "Unix timestamp when an OpenVox certificate expires.",
		},
		[]string{"name", "namespace"},
	)

	// crlLastRefreshTimestamp makes a stalled CRL refresh alertable. A CRL that
	// is no longer being updated means revoked agents keep being accepted, and
	// nothing else in the status surfaces that.
	crlLastRefreshTimestamp = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "openvox_crl_last_refresh_timestamp_seconds",
			Help: "Unix timestamp of the last successful CRL refresh for a CertificateAuthority.",
		},
		[]string{"name", "namespace"},
	)
)

func init() {
	metrics.Registry.MustRegister(
		serverReplicasDesired,
		serverReplicasReady,
		certificateExpiryTimestamp,
		crlLastRefreshTimestamp,
	)
}

// A gauge series outlives the resource it describes unless it is removed
// explicitly: the client library keeps every label combination it has ever
// seen. Without this an alert on a deleted Server keeps firing, expiry alerts
// fire for certificates nobody holds any more, and a cluster with churn grows
// its series count without bound.
//
// All of these are safe to call for a resource that never had a series.

// forgetServerMetrics drops the replica gauges for a Server that is gone.
func forgetServerMetrics(name, namespace string) {
	serverReplicasDesired.DeleteLabelValues(name, namespace)
	serverReplicasReady.DeleteLabelValues(name, namespace)
}

// forgetCertificateMetrics drops the expiry gauge for a Certificate that is
// gone.
func forgetCertificateMetrics(name, namespace string) {
	certificateExpiryTimestamp.DeleteLabelValues(name, namespace)
}

// forgetCertificateAuthorityMetrics drops the gauges a CertificateAuthority
// reports under its own name.
func forgetCertificateAuthorityMetrics(name, namespace string) {
	certificateExpiryTimestamp.DeleteLabelValues(name, namespace)
	crlLastRefreshTimestamp.DeleteLabelValues(name, namespace)
}
