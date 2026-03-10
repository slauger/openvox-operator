package controller

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

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

// findCAServiceName discovers the CA service endpoint by:
// 1. Building the set of Config names that reference this CA
// 2. Finding a running Server with ca:true in one of those Configs
// 3. Finding Pools whose selector matches that CA server
// 4. Returning the first matching Pool name as service name
func findCAServiceName(ctx context.Context, reader client.Reader, ca *openvoxv1alpha1.CertificateAuthority, namespace string) string {
	// Build set of Config names referencing this CA
	cfgList := &openvoxv1alpha1.ConfigList{}
	if err := reader.List(ctx, cfgList, client.InNamespace(namespace)); err != nil {
		return ""
	}
	configNames := map[string]bool{}
	for _, cfg := range cfgList.Items {
		if cfg.Spec.AuthorityRef == ca.Name {
			configNames[cfg.Name] = true
		}
	}

	serverList := &openvoxv1alpha1.ServerList{}
	if err := reader.List(ctx, serverList, client.InNamespace(namespace)); err != nil {
		return ""
	}

	var caServerName string
	var caServerConfigRef string
	for _, server := range serverList.Items {
		if configNames[server.Spec.ConfigRef] && server.Spec.CA {
			if server.Status.Phase == openvoxv1alpha1.ServerPhaseRunning {
				caServerName = server.Name
				caServerConfigRef = server.Spec.ConfigRef
				break
			}
		}
	}

	if caServerName == "" {
		return ""
	}

	poolList := &openvoxv1alpha1.PoolList{}
	if err := reader.List(ctx, poolList, client.InNamespace(namespace)); err != nil {
		return ""
	}

	for _, pool := range poolList.Items {
		if pool.Spec.ConfigRef != caServerConfigRef {
			continue
		}
		if pool.Spec.Selector[LabelServer] == caServerName || pool.Spec.Selector[LabelCA] == "true" {
			return pool.Name
		}
	}

	return ""
}

// resolveCode determines the code source for a Server.
// Priority: Server override > Config default.
func resolveCode(server *openvoxv1alpha1.Server, cfg *openvoxv1alpha1.Config) *openvoxv1alpha1.CodeSpec {
	if server.Spec.Code != nil {
		return server.Spec.Code
	}
	return cfg.Spec.Code
}

// resolveImage determines the container image for a Server.
// Priority: Server override > Config default.
func resolveImage(server *openvoxv1alpha1.Server, cfg *openvoxv1alpha1.Config) string {
	if server.Spec.Image.Tag != "" {
		repo := cfg.Spec.Image.Repository
		if server.Spec.Image.Repository != "" {
			repo = server.Spec.Image.Repository
		}
		return fmt.Sprintf("%s:%s", repo, server.Spec.Image.Tag)
	}
	return fmt.Sprintf("%s:%s", cfg.Spec.Image.Repository, cfg.Spec.Image.Tag)
}
