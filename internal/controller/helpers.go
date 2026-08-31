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
	"k8s.io/apimachinery/pkg/api/meta"
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

// createOrUpdateSecret creates or updates a Secret with the given data, owned by
// the given object.
func createOrUpdateSecret(ctx context.Context, c client.Client, scheme *runtime.Scheme, owner client.Object,
	name, namespace string, labels map[string]string, data map[string][]byte) error {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, c, secret, func() error {
		if err := assertControlledBy(secret, owner, "Secret"); err != nil {
			return err
		}
		secret.Labels = labels
		secret.Data = data
		return controllerutil.SetControllerReference(owner, secret, scheme)
	})
	if err != nil {
		return fmt.Errorf("reconciling Secret %s: %w", name, err)
	}
	return nil
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

// defaultEnvironmentPath is the fallback puppet environmentpath used when
// spec.puppet.environmentPath is unset. It mirrors the kubebuilder default on
// PuppetSpec.EnvironmentPath, which is not applied by the API server when the
// whole spec.puppet object is omitted (nested defaults require the parent object
// to be present). Resolving it here keeps hand-written Configs from rendering an
// empty volume mountPath, which the Kubernetes API rejects.
const defaultEnvironmentPath = "/etc/puppetlabs/code/environments"

// resolveEnvironmentPath returns the configured puppet environmentpath, falling
// back to defaultEnvironmentPath when unset.
func resolveEnvironmentPath(cfg *openvoxv1alpha1.Config) string {
	if cfg.Spec.Puppet.EnvironmentPath != "" {
		return cfg.Spec.Puppet.EnvironmentPath
	}
	return defaultEnvironmentPath
}

// resolveCode determines the code source for a Server.
// Priority: Server override (replace, not merge) > Config default.
func resolveCode(server *openvoxv1alpha1.Server, cfg *openvoxv1alpha1.Config) []openvoxv1alpha1.CodeSpec {
	if len(server.Spec.Code) > 0 {
		return server.Spec.Code
	}
	return cfg.Spec.Code
}

// codeMount is a resolved code source: a unique volume name, the container mount
// path, and the originating CodeSpec.
type codeMount struct {
	VolumeName string
	MountPath  string
	Spec       openvoxv1alpha1.CodeSpec
}

// resolveCodeMounts maps the resolved code sources to concrete volume mounts.
// A single entry without environment/mountPath is mounted as the whole
// environments tree at environmentpath (backwards compatible). Otherwise each
// entry is mounted at its environment subdirectory or its absolute mountPath.
func resolveCodeMounts(server *openvoxv1alpha1.Server, cfg *openvoxv1alpha1.Config) []codeMount {
	entries := resolveCode(server, cfg)
	envPath := resolveEnvironmentPath(cfg)
	mounts := make([]codeMount, 0, len(entries))
	for i, e := range entries {
		switch {
		case e.Environment != "":
			mounts = append(mounts, codeMount{
				VolumeName: fmt.Sprintf("code-%d", i),
				MountPath:  fmt.Sprintf("%s/%s", envPath, e.Environment),
				Spec:       e,
			})
		case e.MountPath != "":
			mounts = append(mounts, codeMount{
				VolumeName: fmt.Sprintf("code-%d", i),
				MountPath:  e.MountPath,
				Spec:       e,
			})
		default:
			// Single whole-tree entry: keep the stable "code" volume name and mount
			// at environmentpath, matching the pre-list behaviour.
			mounts = append(mounts, codeMount{
				VolumeName: "code",
				MountPath:  envPath,
				Spec:       e,
			})
		}
	}
	return mounts
}

// resolveImage determines the container image for a Server.
// Priority: Server override > Config default.
func resolveImage(server *openvoxv1alpha1.Server, cfg *openvoxv1alpha1.Config) string {
	if server.Spec.Image.Tag != "" || server.Spec.Image.Repository != "" {
		repo := cfg.Spec.Image.Repository
		if server.Spec.Image.Repository != "" {
			repo = server.Spec.Image.Repository
		}
		tag := cfg.Spec.Image.Tag
		if server.Spec.Image.Tag != "" {
			tag = server.Spec.Image.Tag
		}
		return fmt.Sprintf("%s:%s", repo, tag)
	}
	return fmt.Sprintf("%s:%s", cfg.Spec.Image.Repository, cfg.Spec.Image.Tag)
}

// serverRoleEnabled reports whether the Server runs the catalog server role.
// The spec field defaults to true, so an unset value enables the role.
func serverRoleEnabled(server *openvoxv1alpha1.Server) bool {
	return openvoxv1alpha1.BoolValue(server.Spec.Server, true)
}

// assertControlledBy fails when obj already exists and is not controlled by the
// expected owner.
//
// Managed child resources are addressed by a name derived from the owner, so a
// pre-existing resource that happens to share that name would otherwise be
// silently taken over and overwritten. Refusing is the safer answer: the
// operator does not own it, and the reconcile error makes that visible instead
// of destroying someone else's object.
func assertControlledBy(obj, owner metav1.Object, kind string) error {
	// An empty resourceVersion means the object was constructed locally and does
	// not exist yet, so there is nothing to conflict with. It is the one field
	// the API server always populates on read.
	if obj.GetResourceVersion() == "" {
		return nil
	}
	if !metav1.IsControlledBy(obj, owner) {
		return fmt.Errorf("%s %s already exists and is not controlled by %s %s",
			kind, obj.GetName(), ownerKind(owner), owner.GetName())
	}
	return nil
}

// ownerKind renders a readable type name for the error above.
func ownerKind(owner metav1.Object) string {
	switch owner.(type) {
	case *openvoxv1alpha1.Server:
		return "Server"
	case *openvoxv1alpha1.Pool:
		return "Pool"
	case *openvoxv1alpha1.Database:
		return "Database"
	case *openvoxv1alpha1.Config:
		return "Config"
	case *openvoxv1alpha1.CertificateAuthority:
		return "CertificateAuthority"
	case *openvoxv1alpha1.Certificate:
		return "Certificate"
	default:
		return "owner"
	}
}

// isPaused reports whether reconciliation is suspended for this object.
func isPaused(obj client.Object) bool {
	return obj.GetAnnotations()[openvoxv1alpha1.AnnotationPaused] == "true"
}

// reconcilePauseState reports whether reconciliation is suspended and keeps the
// Paused condition in sync with the annotation. Callers return early -- without
// requeueing -- when it reports true; removing the annotation produces an event
// that starts a fresh reconcile.
//
// The status is only written when the condition actually has to change, so a
// paused resource does not generate traffic while it sits there.
func reconcilePauseState(ctx context.Context, c client.Client, obj client.Object, conditions *[]metav1.Condition) (bool, error) {
	paused := isPaused(obj)
	generation := obj.GetGeneration()

	if paused {
		if meta.IsStatusConditionTrue(*conditions, openvoxv1alpha1.ConditionPaused) {
			return true, nil
		}
		err := updateStatusWithRetry(ctx, c, obj, func() {
			meta.SetStatusCondition(conditions, metav1.Condition{
				Type:               openvoxv1alpha1.ConditionPaused,
				Status:             metav1.ConditionTrue,
				Reason:             "Paused",
				Message:            "Reconciliation is suspended by the " + openvoxv1alpha1.AnnotationPaused + " annotation",
				ObservedGeneration: generation,
			})
		})
		if err != nil {
			return true, fmt.Errorf("recording the paused condition: %w", err)
		}
		return true, nil
	}

	if meta.FindStatusCondition(*conditions, openvoxv1alpha1.ConditionPaused) == nil {
		return false, nil
	}
	if err := updateStatusWithRetry(ctx, c, obj, func() {
		meta.RemoveStatusCondition(conditions, openvoxv1alpha1.ConditionPaused)
	}); err != nil {
		return false, fmt.Errorf("clearing the paused condition: %w", err)
	}
	return false, nil
}
