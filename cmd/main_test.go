package main

import "testing"

// TestMetricsOptions_InsecureKeepsTheOldShape pins the default: plaintext, no
// filter. Changing it breaks every existing scrape configuration silently, so
// it is a deliberate decision rather than a side effect.
func TestMetricsOptions_InsecureKeepsTheOldShape(t *testing.T) {
	opts := metricsOptions(":8080", false, "")

	if opts.SecureServing {
		t.Error("the default must stay plaintext until the switch is announced")
	}
	if opts.FilterProvider != nil {
		t.Error("no filter without secure serving, it would reject every scrape")
	}
	if opts.BindAddress != ":8080" {
		t.Errorf("bind address = %q, want :8080", opts.BindAddress)
	}
}

// TestMetricsOptions_SecureInstallsTheFilter covers the point of the option.
// Secure serving without the filter would encrypt the transport and still
// serve the metrics to anyone who asks.
func TestMetricsOptions_SecureInstallsTheFilter(t *testing.T) {
	opts := metricsOptions(":8080", true, "")

	if !opts.SecureServing {
		t.Error("expected secure serving")
	}
	if opts.FilterProvider == nil {
		t.Fatal("secure serving without the auth filter still serves to anyone")
	}
	if opts.CertDir != "" {
		t.Errorf("without a cert dir controller-runtime self-signs, got %q", opts.CertDir)
	}
}

// TestMetricsOptions_CertDirIsUsedOnlyWhenSecure keeps a supplied certificate
// from creating the impression of protection while the endpoint is plaintext.
func TestMetricsOptions_CertDirIsUsedOnlyWhenSecure(t *testing.T) {
	secure := metricsOptions(":8080", true, "/tmp/certs")
	if secure.CertDir != "/tmp/certs" {
		t.Errorf("cert dir = %q, want /tmp/certs", secure.CertDir)
	}
	if secure.CertName != "tls.crt" || secure.KeyName != "tls.key" {
		t.Errorf("expected the Secret's key names, got %q/%q", secure.CertName, secure.KeyName)
	}

	insecure := metricsOptions(":8080", false, "/tmp/certs")
	if insecure.CertDir != "" {
		t.Error("a cert dir without secure serving would suggest a protection that is not there")
	}
}
