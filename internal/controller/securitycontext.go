package controller

import (
	corev1 "k8s.io/api/core/v1"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// buildPodSecurityContext returns a PodSecurityContext seeded with the operator
// defaults for the given workload and overridden by any fields set in override.
//
// The defaults harden the pod (runAsNonRoot + RuntimeDefault seccomp) and set a
// matching fsGroup so the kubelet chowns mounted volumes to the group, which lets
// the non-root user write to CSI-provisioned volumes that are otherwise owned by
// root. Callers pass their workload-specific uid/gid/fsGroup constants; users can
// override individual fields via the CRD (e.g. on OpenShift or PSA-restricted
// namespaces that assign their own UID/GID ranges).
func buildPodSecurityContext(defaultUser, defaultGroup, defaultFSGroup int64, override *openvoxv1alpha1.PodSecurityContextSpec) *corev1.PodSecurityContext {
	psc := &corev1.PodSecurityContext{
		RunAsUser:           int64Ptr(defaultUser),
		RunAsGroup:          int64Ptr(defaultGroup),
		RunAsNonRoot:        boolPtr(true),
		FSGroup:             int64Ptr(defaultFSGroup),
		FSGroupChangePolicy: fsGroupChangePolicyPtr(corev1.FSGroupChangeOnRootMismatch),
		SeccompProfile: &corev1.SeccompProfile{
			Type: corev1.SeccompProfileTypeRuntimeDefault,
		},
	}

	if override == nil {
		return psc
	}
	if override.RunAsUser != nil {
		psc.RunAsUser = override.RunAsUser
	}
	if override.RunAsGroup != nil {
		psc.RunAsGroup = override.RunAsGroup
	}
	if override.RunAsNonRoot != nil {
		psc.RunAsNonRoot = override.RunAsNonRoot
	}
	if override.FSGroup != nil {
		psc.FSGroup = override.FSGroup
	}
	if override.FSGroupChangePolicy != nil {
		psc.FSGroupChangePolicy = override.FSGroupChangePolicy
	}
	return psc
}
