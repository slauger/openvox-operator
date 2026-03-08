package controller

const (
	// Label keys used across all resources.
	LabelEnvironment = "openvox.voxpupuli.org/environment"
	LabelPool        = "openvox.voxpupuli.org/pool"
	LabelServer      = "openvox.voxpupuli.org/server"
	LabelRole        = "openvox.voxpupuli.org/role"

	// Role values.
	RoleCA       = "ca"
	RoleCompiler = "compiler"
)

// environmentLabels returns labels for resources owned by an Environment.
func environmentLabels(envName string) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "openvox",
		"app.kubernetes.io/managed-by": "openvox-operator",
		LabelEnvironment:               envName,
	}
}

// serverLabels returns labels for Pods/resources owned by a Server.
func serverLabels(envName, serverName, role string) map[string]string {
	labels := map[string]string{
		"app.kubernetes.io/name":       "openvox",
		"app.kubernetes.io/managed-by": "openvox-operator",
		LabelEnvironment:               envName,
		LabelServer:                    serverName,
		LabelRole:                      role,
	}
	return labels
}

// poolSelector returns the label selector for a Pool's Service.
func poolSelector(poolName string) map[string]string {
	return map[string]string{
		LabelPool: poolName,
	}
}

func int64Ptr(i int64) *int64 { return &i }
func boolPtr(b bool) *bool    { return &b }
