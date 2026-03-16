package controller

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"sort"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// resolveSecretKey reads a specific key from a Secret.
func resolveSecretKey(ctx context.Context, reader client.Reader, namespace, secretName, key string) (string, error) {
	secret := &corev1.Secret{}
	if err := reader.Get(ctx, client.ObjectKey{Name: secretName, Namespace: namespace}, secret); err != nil {
		return "", fmt.Errorf("getting Secret %s: %w", secretName, err)
	}
	val, ok := secret.Data[key]
	if !ok {
		return "", fmt.Errorf("key %q not found in Secret %s", key, secretName)
	}
	return string(val), nil
}

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
// 3. Finding a Pool referenced by the CA server's poolRefs
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

	for _, server := range serverList.Items {
		if configNames[server.Spec.ConfigRef] && server.Spec.CA {
			if server.Status.Phase == openvoxv1alpha1.ServerPhaseRunning {
				// Return the first poolRef as the CA service name
				if len(server.Spec.PoolRefs) > 0 {
					return server.Spec.PoolRefs[0]
				}
				return ""
			}
		}
	}

	return ""
}

// parseCertNotAfter extracts the NotAfter time from a PEM-encoded certificate.
func parseCertNotAfter(certPEM []byte) *metav1.Time {
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil
	}
	t := metav1.NewTime(cert.NotAfter.UTC().Truncate(time.Second))
	return &t
}

// isSecretReady checks if a Secret exists and (optionally) contains the given key.
func isSecretReady(ctx context.Context, reader client.Reader, name, namespace, requiredKey string) bool {
	secret := &corev1.Secret{}
	if err := reader.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, secret); err != nil {
		return false
	}
	if requiredKey != "" {
		_, ok := secret.Data[requiredKey]
		return ok
	}
	return true
}

// createOrUpdateSecret creates or updates a Secret with the given data and owner reference.
func createOrUpdateSecret(ctx context.Context, c client.Client, scheme *runtime.Scheme, owner client.Object,
	name, namespace string, labels map[string]string, data map[string][]byte) error {
	secret := &corev1.Secret{}
	err := c.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, secret)
	if errors.IsNotFound(err) {
		secret = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
				Labels:    labels,
			},
			Data: data,
		}
		if err := controllerutil.SetControllerReference(owner, secret, scheme); err != nil {
			return err
		}
		return c.Create(ctx, secret)
	} else if err != nil {
		return err
	}

	secret.Data = data
	return c.Update(ctx, secret)
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
