package controller

import (
	"strings"
	"testing"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

func TestRenderMetricsConf(t *testing.T) {
	r := newConfigReconciler(setupTestClient())

	tests := []struct {
		name     string
		metrics  *openvoxv1alpha1.MetricsSpec
		contains []string
		excludes []string
	}{
		{
			name:    "nil metrics (disabled)",
			metrics: nil,
			contains: []string{
				"metrics: {",
				"server-id: localhost",
			},
			excludes: []string{"registries", "jmx", "graphite"},
		},
		{
			name:    "JMX enabled",
			metrics: &openvoxv1alpha1.MetricsSpec{Enabled: true, JMX: &openvoxv1alpha1.JMXSpec{Enabled: true}},
			contains: []string{
				"registries:",
				"reporters:",
				"jmx:",
				"enabled: true",
			},
			excludes: []string{"graphite"},
		},
		{
			name: "Graphite with custom host and port",
			metrics: &openvoxv1alpha1.MetricsSpec{
				Enabled: true,
				Graphite: &openvoxv1alpha1.GraphiteSpec{
					Enabled:               true,
					Host:                  "graphite.example.com",
					Port:                  9090,
					UpdateIntervalSeconds: 30,
				},
			},
			contains: []string{
				"graphite:",
				`host: "graphite.example.com"`,
				"port: 9090",
				"update-interval-seconds: 30",
			},
			excludes: []string{"jmx"},
		},
		{
			name: "Graphite with default port and interval",
			metrics: &openvoxv1alpha1.MetricsSpec{
				Enabled: true,
				Graphite: &openvoxv1alpha1.GraphiteSpec{
					Enabled: true,
					Host:    "graphite.local",
				},
			},
			contains: []string{
				"port: 2003",
				"update-interval-seconds: 60",
			},
		},
		{
			name: "both JMX and Graphite enabled",
			metrics: &openvoxv1alpha1.MetricsSpec{
				Enabled:  true,
				JMX:      &openvoxv1alpha1.JMXSpec{Enabled: true},
				Graphite: &openvoxv1alpha1.GraphiteSpec{Enabled: true, Host: "g.local"},
			},
			contains: []string{"jmx:", "graphite:"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := newConfig("test")
			cfg.Spec.Metrics = tt.metrics

			out := r.renderMetricsConf(cfg)

			for _, s := range tt.contains {
				if !strings.Contains(out, s) {
					t.Errorf("expected %q in output:\n%s", s, out)
				}
			}
			for _, s := range tt.excludes {
				if strings.Contains(out, s) {
					t.Errorf("unexpected %q in output:\n%s", s, out)
				}
			}
		})
	}
}

func TestRenderWebserverConf(t *testing.T) {
	r := newConfigReconciler(setupTestClient())

	t.Run("default client-auth", func(t *testing.T) {
		cfg := newConfig("test")
		out := r.renderWebserverConf(cfg)

		if !strings.Contains(out, "client-auth: want") {
			t.Errorf("expected default client-auth 'want' in output:\n%s", out)
		}
		if !strings.Contains(out, "ssl-crl-path: /etc/puppetlabs/puppet/crl/ca_crl.pem") {
			t.Errorf("expected non-CA CRL path in output:\n%s", out)
		}
	})

	t.Run("custom client-auth", func(t *testing.T) {
		cfg := newConfig("test", withPuppetServerSpec(openvoxv1alpha1.PuppetServerSpec{
			ClientAuth: "need",
		}))
		out := r.renderWebserverConf(cfg)

		if !strings.Contains(out, "client-auth: need") {
			t.Errorf("expected client-auth 'need' in output:\n%s", out)
		}
	})
}

func TestRenderWebserverConfCA(t *testing.T) {
	r := newConfigReconciler(setupTestClient())
	cfg := newConfig("test")

	out := r.renderWebserverConfCA(cfg)

	if !strings.Contains(out, "ssl-crl-path: /etc/puppetlabs/puppet/ssl/crl.pem") {
		t.Errorf("expected CA CRL path in output:\n%s", out)
	}
	if !strings.Contains(out, "ssl-port: 8140") {
		t.Errorf("expected ssl-port 8140 in output:\n%s", out)
	}
}

func TestRenderCAConf(t *testing.T) {
	r := newConfigReconciler(setupTestClient())

	tests := []struct {
		name     string
		ca       *openvoxv1alpha1.CertificateAuthority
		contains []string
	}{
		{
			name: "nil CA (all defaults true)",
			ca:   nil,
			contains: []string{
				"allow-subject-alt-names: true",
				"allow-authorization-extensions: true",
				"enable-infra-crl: true",
				"allow-auto-renewal: true",
				"auto-renewal-cert-ttl: 90d",
			},
		},
		{
			name: "custom values",
			ca: &openvoxv1alpha1.CertificateAuthority{
				Spec: openvoxv1alpha1.CertificateAuthoritySpec{
					AllowSubjectAltNames:         false,
					AllowAuthorizationExtensions: false,
					EnableInfraCRL:               false,
					AllowAutoRenewal:             false,
					AutoRenewalCertTTL:           "30d",
				},
			},
			contains: []string{
				"allow-subject-alt-names: false",
				"allow-authorization-extensions: false",
				"enable-infra-crl: false",
				"allow-auto-renewal: false",
				"auto-renewal-cert-ttl: 30d",
			},
		},
		{
			name: "empty autoRenewalCertTTL uses default",
			ca: &openvoxv1alpha1.CertificateAuthority{
				Spec: openvoxv1alpha1.CertificateAuthoritySpec{
					AllowSubjectAltNames:         true,
					AllowAuthorizationExtensions: true,
					EnableInfraCRL:               true,
					AllowAutoRenewal:             true,
					AutoRenewalCertTTL:           "",
				},
			},
			contains: []string{
				"auto-renewal-cert-ttl: 90d",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := r.renderCAConf(tt.ca)
			for _, s := range tt.contains {
				if !strings.Contains(out, s) {
					t.Errorf("expected %q in output:\n%s", s, out)
				}
			}
		})
	}
}
