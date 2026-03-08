package controller

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
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

// resolveCertname returns the certname for a Server.
// Defaults to "puppet" if not explicitly set.
func resolveCertname(server *openvoxv1alpha1.Server) string {
	if server.Spec.Certname != "" {
		return server.Spec.Certname
	}
	return "puppet"
}

// resolveDNSAltNames builds the list of dnsAltNames for a Server,
// auto-injecting service names based on roles (CA, server).
// Pool service names are resolved separately via resolvePoolDNSAltNames.
func resolveDNSAltNames(server *openvoxv1alpha1.Server) []string {
	seen := make(map[string]bool)
	var names []string

	add := func(name string) {
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}

	envName := server.Spec.EnvironmentRef
	if server.Spec.CA {
		add(fmt.Sprintf("%s-ca", envName))
	}
	if server.Spec.Server {
		add(fmt.Sprintf("%s-server", envName))
	}
	for _, name := range server.Spec.DNSAltNames {
		add(name)
	}

	return names
}

// resolvePoolDNSAltNames lists all Pools in the same environment and returns
// the service names of those whose Selector matches the Server's Pod labels.
func (r *ServerReconciler) resolvePoolDNSAltNames(ctx context.Context, server *openvoxv1alpha1.Server) ([]string, error) {
	poolList := &openvoxv1alpha1.PoolList{}
	if err := r.List(ctx, poolList, client.InNamespace(server.Namespace)); err != nil {
		return nil, err
	}

	// Determine the labels the Server's Pods will have
	role := RoleServer
	if server.Spec.CA && !server.Spec.Server {
		role = RoleCA
	}
	podLabels := serverLabels(server.Spec.EnvironmentRef, server.Name, role)
	if server.Spec.CA {
		podLabels[LabelCA] = "true"
	}

	var names []string
	for _, pool := range poolList.Items {
		if pool.Spec.EnvironmentRef != server.Spec.EnvironmentRef {
			continue
		}
		sel := poolServiceSelector(&pool)
		if labels.SelectorFromSet(sel).Matches(labels.Set(podLabels)) {
			names = append(names, pool.Name)
		}
	}
	return names, nil
}
