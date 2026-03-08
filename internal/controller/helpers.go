package controller

import (
	"crypto/sha256"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// configMapVolume creates a Volume from a ConfigMap key where key name == path.
func configMapVolume(volumeName, cmName, key string) corev1.Volume {
	return corev1.Volume{
		Name: volumeName,
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: cmName},
				Items:                []corev1.KeyToPath{{Key: key, Path: key}},
			},
		},
	}
}

// configMapVolumeWithKey creates a Volume from a ConfigMap key with a different path.
func configMapVolumeWithKey(volumeName, cmName, key, path string) corev1.Volume {
	return corev1.Volume{
		Name: volumeName,
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: cmName},
				Items:                []corev1.KeyToPath{{Key: key, Path: path}},
			},
		},
	}
}

// hashStringMap computes a deterministic SHA256 hash of a map[string]string.
func hashStringMap(data map[string]string) string {
	keys := make([]string, 0, len(data))
	for k := range data {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	h := sha256.New()
	for _, k := range keys {
		h.Write([]byte(k))
		h.Write([]byte(data[k]))
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// resolveImage determines the container image for a Server.
// Priority: Server override > Environment default.
func resolveImage(server *openvoxv1alpha1.Server, env *openvoxv1alpha1.Environment) string {
	if server.Spec.Image.Tag != "" {
		repo := env.Spec.Image.Repository
		if server.Spec.Image.Repository != "" {
			repo = server.Spec.Image.Repository
		}
		return fmt.Sprintf("%s:%s", repo, server.Spec.Image.Tag)
	}
	return fmt.Sprintf("%s:%s", env.Spec.Image.Repository, env.Spec.Image.Tag)
}
