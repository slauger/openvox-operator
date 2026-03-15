package controller

const (
	// Label keys used across all resources.
	LabelConfig               = "openvox.voxpupuli.org/config"
	LabelCertificateAuthority = "openvox.voxpupuli.org/certificateauthority"
	LabelPoolPrefix           = "openvox.voxpupuli.org/pool-"
	LabelServer               = "openvox.voxpupuli.org/server"
	LabelRole                 = "openvox.voxpupuli.org/role"
	LabelCA                   = "openvox.voxpupuli.org/ca"

	// Annotation keys.
	AnnotationConfigSpecHash = "openvox.voxpupuli.org/config-spec-hash"

	// Role values.
	RoleCA     = "ca"
	RoleServer = "server"
)

// configLabels returns labels for resources owned by a Config.
func configLabels(cfgName string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "openvox",
		"app.kubernetes.io/managed-by": "openvox-operator",
		LabelConfig:                    cfgName,
	}
}

// caLabels returns labels for resources owned by a CertificateAuthority.
func caLabels(caName string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "openvox",
		"app.kubernetes.io/managed-by": "openvox-operator",
		LabelCertificateAuthority:      caName,
	}
}

// serverLabels returns labels for Pods/resources owned by a Server.
func serverLabels(cfgName, serverName, role string) map[string]string {
	labels := map[string]string{
		"app.kubernetes.io/name":       "openvox",
		"app.kubernetes.io/managed-by": "openvox-operator",
		LabelConfig:                    cfgName,
		LabelServer:                    serverName,
		LabelRole:                      role,
	}
	return labels
}

// poolLabel returns the label key for a specific Pool.
func poolLabel(poolName string) string {
	return LabelPoolPrefix + poolName
}

func int64Ptr(i int64) *int64 { return &i }
func boolPtr(b bool) *bool    { return &b }
