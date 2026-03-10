package controller

import (
	"testing"
)

func TestConfigLabels(t *testing.T) {
	labels := configLabels("production")

	expected := map[string]string{
		"app.kubernetes.io/name":       "openvox",
		"app.kubernetes.io/managed-by": "openvox-operator",
		LabelConfig:                    "production",
	}

	if len(labels) != len(expected) {
		t.Errorf("expected %d labels, got %d", len(expected), len(labels))
	}
	for k, v := range expected {
		if labels[k] != v {
			t.Errorf("label %q = %q, want %q", k, labels[k], v)
		}
	}
}

func TestCALabels(t *testing.T) {
	labels := caLabels("production-ca")

	if labels[LabelCertificateAuthority] != "production-ca" {
		t.Errorf("expected CA label %q, got %q", "production-ca", labels[LabelCertificateAuthority])
	}
	if labels["app.kubernetes.io/name"] != "openvox" {
		t.Errorf("expected app name label %q, got %q", "openvox", labels["app.kubernetes.io/name"])
	}
	if labels["app.kubernetes.io/managed-by"] != "openvox-operator" {
		t.Errorf("expected managed-by label %q, got %q", "openvox-operator", labels["app.kubernetes.io/managed-by"])
	}
}

func TestServerLabels(t *testing.T) {
	labels := serverLabels("production", "stable", RoleServer)

	if labels[LabelConfig] != "production" {
		t.Errorf("expected config label %q, got %q", "production", labels[LabelConfig])
	}
	if labels[LabelServer] != "stable" {
		t.Errorf("expected server label %q, got %q", "stable", labels[LabelServer])
	}
	if labels[LabelRole] != RoleServer {
		t.Errorf("expected role label %q, got %q", RoleServer, labels[LabelRole])
	}
	if labels["app.kubernetes.io/name"] != "openvox" {
		t.Errorf("expected app name label")
	}
}

func TestServerLabelsCA(t *testing.T) {
	labels := serverLabels("production", "ca", RoleCA)

	if labels[LabelRole] != RoleCA {
		t.Errorf("expected role label %q, got %q", RoleCA, labels[LabelRole])
	}
}

func TestPoolLabel(t *testing.T) {
	label := poolLabel("puppet")
	expected := LabelPoolPrefix + "puppet"
	if label != expected {
		t.Errorf("poolLabel(%q) = %q, want %q", "puppet", label, expected)
	}
}

func TestLabelConstants(t *testing.T) {
	// Verify label constants have expected values
	if LabelConfig != "openvox.voxpupuli.org/config" {
		t.Errorf("LabelConfig = %q", LabelConfig)
	}
	if LabelCertificateAuthority != "openvox.voxpupuli.org/certificateauthority" {
		t.Errorf("LabelCertificateAuthority = %q", LabelCertificateAuthority)
	}
	if LabelPoolPrefix != "openvox.voxpupuli.org/pool-" {
		t.Errorf("LabelPoolPrefix = %q", LabelPoolPrefix)
	}
	if LabelServer != "openvox.voxpupuli.org/server" {
		t.Errorf("LabelServer = %q", LabelServer)
	}
	if LabelRole != "openvox.voxpupuli.org/role" {
		t.Errorf("LabelRole = %q", LabelRole)
	}
	if LabelCA != "openvox.voxpupuli.org/ca" {
		t.Errorf("LabelCA = %q", LabelCA)
	}
}

func TestRoleConstants(t *testing.T) {
	if RoleCA != "ca" {
		t.Errorf("RoleCA = %q, want %q", RoleCA, "ca")
	}
	if RoleServer != "server" {
		t.Errorf("RoleServer = %q, want %q", RoleServer, "server")
	}
}
