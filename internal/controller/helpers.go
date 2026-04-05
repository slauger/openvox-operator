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
	"k8s.io/client-go/util/retry"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// updateStatusWithRetry updates an object's status with automatic retry on conflict.
// It re-fetches the object before each attempt so the latest resourceVersion is used.
func updateStatusWithRetry(ctx context.Context, c client.Client, obj client.Object, mutate func()) error {
	return retry.RetryOnConflict(retry.DefaultRetry, func() error {
		if err := c.Get(ctx, client.ObjectKeyFromObject(obj), obj); err != nil {
			return err
		}
		mutate()
		return c.Status().Update(ctx, obj)
	})
}

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

// parseCertNotAfter extracts the NotAfter time from a PEM-encoded certificate.
func parseCertNotAfter(ctx context.Context, certPEM []byte) *metav1.Time {
	logger := log.FromContext(ctx)

	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		logger.Error(fmt.Errorf("expected PEM block type CERTIFICATE"), "failed to decode certificate PEM")
		return nil
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		logger.Error(err, "failed to parse X.509 certificate")
		return nil
	}
	t := metav1.NewTime(cert.NotAfter.UTC().Truncate(time.Second))
	return &t
}

// isSecretReady checks if a Secret exists and (optionally) contains the given key.
func isSecretReady(ctx context.Context, reader client.Reader, name, namespace, requiredKey string) bool {
	secret := &corev1.Secret{}
	if err := reader.Get(ctx, client.ObjectKey{Name: name, Namespace: namespace}, secret); err != nil {
		if !errors.IsNotFound(err) {
			log.FromContext(ctx).Error(err, "failed to get Secret", "name", name, "namespace", namespace)
		}
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

// getCAPublicCert reads the CA public certificate from the CA Secret.
func getCAPublicCert(ctx context.Context, reader client.Reader, ca *openvoxv1alpha1.CertificateAuthority, namespace string) ([]byte, error) {
	caSecretName := fmt.Sprintf("%s-ca", ca.Name)
	secret := &corev1.Secret{}
	if err := reader.Get(ctx, types.NamespacedName{Name: caSecretName, Namespace: namespace}, secret); err != nil {
		return nil, fmt.Errorf("getting CA Secret %s: %w", caSecretName, err)
	}
	certPEM := secret.Data["ca_crt.pem"]
	if len(certPEM) == 0 {
		return nil, fmt.Errorf("CA Secret %s has no ca_crt.pem data", caSecretName)
	}
	return certPEM, nil
}

// caInternalServiceName returns the name of the internal ClusterIP Service
// created by the CA controller for operator communication (CSR signing, CRL refresh).
func caInternalServiceName(caName string) string {
	return fmt.Sprintf("%s-internal", caName)
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
