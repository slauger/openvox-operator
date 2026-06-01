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
)

func init() {
	metrics.Registry.MustRegister(
		serverReplicasDesired,
		serverReplicasReady,
		certificateExpiryTimestamp,
	)
}
