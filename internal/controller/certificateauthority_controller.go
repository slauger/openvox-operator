package controller

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// CertificateAuthorityReconciler reconciles a CertificateAuthority object.
type CertificateAuthorityReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=certificateauthorities,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=certificateauthorities/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=certificateauthorities/finalizers,verbs=update
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=certificates,verbs=get;list;watch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=configs,verbs=get;list;watch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=servers,verbs=get;list;watch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=pools,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=persistentvolumeclaims;secrets;serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles;rolebindings,verbs=get;list;watch;create;update;patch;delete

func (r *CertificateAuthorityReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	ca := &openvoxv1alpha1.CertificateAuthority{}
	if err := r.Get(ctx, req.NamespacedName, ca); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Set initial phase
	if ca.Status.Phase == "" {
		ca.Status.Phase = openvoxv1alpha1.CertificateAuthorityPhasePending
		if err := r.Status().Update(ctx, ca); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Resolve Config referencing this CA
	cfg := r.findConfigForCA(ctx, ca)
	if cfg == nil {
		logger.Info("waiting for a Config with authorityRef pointing to this CA", "ca", ca.Name)
		return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
	}

	// Step 1: Ensure CA data PVC exists
	if err := r.reconcileCAPVC(ctx, ca); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling CA PVC: %w", err)
	}

	// Step 2: Discover Certificates referencing this CA
	certs, err := r.findCertificatesForCA(ctx, ca)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("finding certificates for CA: %w", err)
	}

	// Step 3: Ensure RBAC for CA setup job
	if err := r.reconcileCASetupRBAC(ctx, ca, certs); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling CA setup RBAC: %w", err)
	}

	// Step 4: Run CA setup job
	result, err := r.reconcileCASetupJob(ctx, ca, cfg, certs)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling CA setup job: %w", err)
	}
	if result.RequeueAfter > 0 {
		return result, nil
	}

	// CA is ready
	caSecretName := fmt.Sprintf("%s-ca", ca.Name)
	ca.Status.Phase = openvoxv1alpha1.CertificateAuthorityPhaseReady
	ca.Status.CASecretName = caSecretName
	meta.SetStatusCondition(&ca.Status.Conditions, metav1.Condition{
		Type:               openvoxv1alpha1.ConditionCAReady,
		Status:             metav1.ConditionTrue,
		Reason:             "CAInitialized",
		Message:            "CA is initialized and ready",
		LastTransitionTime: metav1.Now(),
	})

	if err := r.Status().Update(ctx, ca); err != nil {
		return ctrl.Result{}, err
	}

	// Periodic CRL refresh: fetch CRL from CA service and update the CRL secret
	crlResult, err := r.reconcileCRLRefresh(ctx, ca)
	if err != nil {
		logger.Error(err, "CRL refresh failed, will retry")
		return ctrl.Result{RequeueAfter: 30 * time.Second}, nil
	}
	return crlResult, nil
}

// findConfigForCA returns the first Config in the same namespace whose authorityRef matches this CA.
func (r *CertificateAuthorityReconciler) findConfigForCA(ctx context.Context, ca *openvoxv1alpha1.CertificateAuthority) *openvoxv1alpha1.Config {
	cfgList := &openvoxv1alpha1.ConfigList{}
	if err := r.List(ctx, cfgList, client.InNamespace(ca.Namespace)); err != nil {
		return nil
	}
	for i := range cfgList.Items {
		if cfgList.Items[i].Spec.AuthorityRef == ca.Name {
			return &cfgList.Items[i]
		}
	}
	return nil
}

func (r *CertificateAuthorityReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&openvoxv1alpha1.CertificateAuthority{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&batchv1.Job{}).
		Owns(&corev1.ServiceAccount{}).
		Watches(&openvoxv1alpha1.Certificate{}, handler.EnqueueRequestsFromMapFunc(
			func(ctx context.Context, obj client.Object) []ctrl.Request {
				cert, ok := obj.(*openvoxv1alpha1.Certificate)
				if !ok || cert.Spec.AuthorityRef == "" {
					return nil
				}
				return []ctrl.Request{
					{NamespacedName: types.NamespacedName{Name: cert.Spec.AuthorityRef, Namespace: cert.Namespace}},
				}
			},
		)).
		Watches(&openvoxv1alpha1.Config{}, handler.EnqueueRequestsFromMapFunc(
			func(ctx context.Context, obj client.Object) []ctrl.Request {
				cfg, ok := obj.(*openvoxv1alpha1.Config)
				if !ok || cfg.Spec.AuthorityRef == "" {
					return nil
				}
				return []ctrl.Request{
					{NamespacedName: types.NamespacedName{Name: cfg.Spec.AuthorityRef, Namespace: cfg.Namespace}},
				}
			},
		)).
		Complete(r)
}

// --- CRL Refresh ---

// reconcileCRLRefresh fetches the CRL from the CA service and updates the CRL secret.
// Returns a Result with RequeueAfter set to the configured refresh interval.
func (r *CertificateAuthorityReconciler) reconcileCRLRefresh(ctx context.Context, ca *openvoxv1alpha1.CertificateAuthority) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	interval := 5 * time.Minute
	if ca.Spec.CRLRefreshInterval != "" {
		parsed, err := time.ParseDuration(ca.Spec.CRLRefreshInterval)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("parsing crlRefreshInterval %q: %w", ca.Spec.CRLRefreshInterval, err)
		}
		interval = parsed
	}

	caServiceName := findCAServiceName(ctx, r.Client, ca, ca.Namespace)
	if caServiceName == "" {
		logger.Info("CA service not yet available, skipping CRL refresh")
		return ctrl.Result{RequeueAfter: interval}, nil
	}

	crlPEM, err := r.fetchCRL(ctx, caServiceName, ca.Namespace)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("fetching CRL: %w", err)
	}

	crlSecretName := fmt.Sprintf("%s-ca-crl", ca.Name)
	if err := r.updateCRLSecret(ctx, ca, crlSecretName, crlPEM); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating CRL secret: %w", err)
	}

	logger.Info("CRL secret refreshed", "secret", crlSecretName, "nextRefresh", interval)
	return ctrl.Result{RequeueAfter: interval}, nil
}

// fetchCRL retrieves the CRL from the CA HTTP API.
func (r *CertificateAuthorityReconciler) fetchCRL(ctx context.Context, caServiceName, namespace string) ([]byte, error) {
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // internal CA
		},
	}

	crlURL := fmt.Sprintf("https://%s.%s.svc:8140/puppet-ca/v1/certificate_revocation_list/ca?environment=production", caServiceName, namespace)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, crlURL, nil)
	if err != nil {
		return nil, fmt.Errorf("building CRL request: %w", err)
	}
	req.Header.Set("Accept", "text/plain")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("requesting CRL: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading CRL response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("CA returned HTTP %d for CRL: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// updateCRLSecret creates or updates the CRL secret with fresh CRL data.
func (r *CertificateAuthorityReconciler) updateCRLSecret(ctx context.Context, ca *openvoxv1alpha1.CertificateAuthority, name string, crlPEM []byte) error {
	labels := caLabels(ca.Name)

	secret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: ca.Namespace}, secret)
	if errors.IsNotFound(err) {
		secret = &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: ca.Namespace,
				Labels:    labels,
			},
			Data: map[string][]byte{
				"ca_crl.pem": crlPEM,
			},
		}
		if err := controllerutil.SetControllerReference(ca, secret, r.Scheme); err != nil {
			return err
		}
		return r.Create(ctx, secret)
	} else if err != nil {
		return err
	}

	secret.Data["ca_crl.pem"] = crlPEM
	return r.Update(ctx, secret)
}

// --- CA PVC ---

func (r *CertificateAuthorityReconciler) reconcileCAPVC(ctx context.Context, ca *openvoxv1alpha1.CertificateAuthority) error {
	pvcName := fmt.Sprintf("%s-data", ca.Name)

	pvc := &corev1.PersistentVolumeClaim{}
	err := r.Get(ctx, types.NamespacedName{Name: pvcName, Namespace: ca.Namespace}, pvc)
	if errors.IsNotFound(err) {
		storageSize := "1Gi"
		if ca.Spec.Storage.Size != "" {
			storageSize = ca.Spec.Storage.Size
		}

		pvc = &corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      pvcName,
				Namespace: ca.Namespace,
				Labels:    caLabels(ca.Name),
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse(storageSize),
					},
				},
			},
		}

		if ca.Spec.Storage.StorageClass != "" {
			pvc.Spec.StorageClassName = &ca.Spec.Storage.StorageClass
		}

		if err := controllerutil.SetControllerReference(ca, pvc, r.Scheme); err != nil {
			return err
		}
		return r.Create(ctx, pvc)
	}
	return err
}

// --- Certificate discovery ---

// findCertificatesForCA lists Certificates in the namespace that reference this CA.
func (r *CertificateAuthorityReconciler) findCertificatesForCA(ctx context.Context, ca *openvoxv1alpha1.CertificateAuthority) ([]openvoxv1alpha1.Certificate, error) {
	certList := &openvoxv1alpha1.CertificateList{}
	if err := r.List(ctx, certList, client.InNamespace(ca.Namespace)); err != nil {
		return nil, err
	}

	var result []openvoxv1alpha1.Certificate
	for _, cert := range certList.Items {
		if cert.Spec.AuthorityRef == ca.Name {
			result = append(result, cert)
		}
	}
	return result, nil
}

// findCAServerCert finds the Certificate belonging to the Server with ca:true.
// This is the cert that should be signed during CA setup.
func (r *CertificateAuthorityReconciler) findCAServerCert(ctx context.Context, ca *openvoxv1alpha1.CertificateAuthority, certs []openvoxv1alpha1.Certificate) *openvoxv1alpha1.Certificate {
	// Build set of Config names referencing this CA
	cfgList := &openvoxv1alpha1.ConfigList{}
	if err := r.List(ctx, cfgList, client.InNamespace(ca.Namespace)); err != nil {
		return nil
	}
	configNames := map[string]bool{}
	for _, cfg := range cfgList.Items {
		if cfg.Spec.AuthorityRef == ca.Name {
			configNames[cfg.Name] = true
		}
	}

	serverList := &openvoxv1alpha1.ServerList{}
	if err := r.List(ctx, serverList, client.InNamespace(ca.Namespace)); err != nil {
		return nil
	}

	// Find servers with ca:true in a Config referencing this CA
	caServerCertRefs := map[string]bool{}
	for _, server := range serverList.Items {
		if configNames[server.Spec.ConfigRef] && server.Spec.CA {
			caServerCertRefs[server.Spec.CertificateRef] = true
		}
	}

	for i := range certs {
		if caServerCertRefs[certs[i].Name] {
			return &certs[i]
		}
	}

	// Fallback: return first cert if no CA server found
	if len(certs) > 0 {
		return &certs[0]
	}
	return nil
}

// --- RBAC for CA setup job ---

func (r *CertificateAuthorityReconciler) reconcileCASetupRBAC(ctx context.Context, ca *openvoxv1alpha1.CertificateAuthority, certs []openvoxv1alpha1.Certificate) error {
	baseName := fmt.Sprintf("%s-ca-setup", ca.Name)
	caSecretName := fmt.Sprintf("%s-ca", ca.Name)
	labels := caLabels(ca.Name)

	caKeySecretName := fmt.Sprintf("%s-ca-key", ca.Name)
	caCRLSecretName := fmt.Sprintf("%s-ca-crl", ca.Name)
	resourceNames := []string{caSecretName, caKeySecretName, caCRLSecretName}
	for _, cert := range certs {
		resourceNames = append(resourceNames, fmt.Sprintf("%s-tls", cert.Name))
	}

	// ServiceAccount
	if err := r.ensureCAServiceAccount(ctx, baseName, ca.Namespace, labels, ca); err != nil {
		return fmt.Errorf("ensuring ServiceAccount: %w", err)
	}

	// Role
	if err := r.ensureCARole(ctx, baseName, ca.Namespace, labels, resourceNames, ca); err != nil {
		return fmt.Errorf("ensuring Role: %w", err)
	}

	// RoleBinding
	if err := r.ensureCARoleBinding(ctx, baseName, ca.Namespace, labels, ca); err != nil {
		return fmt.Errorf("ensuring RoleBinding: %w", err)
	}

	return nil
}

func (r *CertificateAuthorityReconciler) ensureCAServiceAccount(ctx context.Context, name, namespace string, labels map[string]string, owner *openvoxv1alpha1.CertificateAuthority) error {
	sa := &corev1.ServiceAccount{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, sa); errors.IsNotFound(err) {
		sa = &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
				Labels:    labels,
			},
		}
		if err := controllerutil.SetControllerReference(owner, sa, r.Scheme); err != nil {
			return err
		}
		return r.Create(ctx, sa)
	} else {
		return err
	}
}

func (r *CertificateAuthorityReconciler) ensureCARole(ctx context.Context, name, namespace string, labels map[string]string, resourceNames []string, owner *openvoxv1alpha1.CertificateAuthority) error {
	role := &rbacv1.Role{}
	err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, role)
	if errors.IsNotFound(err) {
		role = &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
				Labels:    labels,
			},
			Rules: []rbacv1.PolicyRule{
				{
					APIGroups:     []string{""},
					Resources:     []string{"secrets"},
					ResourceNames: resourceNames,
					Verbs:         []string{"get", "update", "patch"},
				},
				{
					APIGroups: []string{""},
					Resources: []string{"secrets"},
					Verbs:     []string{"create"},
				},
			},
		}
		if err := controllerutil.SetControllerReference(owner, role, r.Scheme); err != nil {
			return err
		}
		return r.Create(ctx, role)
	} else if err != nil {
		return err
	}

	role.Rules = []rbacv1.PolicyRule{
		{
			APIGroups:     []string{""},
			Resources:     []string{"secrets"},
			ResourceNames: resourceNames,
			Verbs:         []string{"get", "update", "patch"},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"secrets"},
			Verbs:     []string{"create"},
		},
	}
	return r.Update(ctx, role)
}

func (r *CertificateAuthorityReconciler) ensureCARoleBinding(ctx context.Context, name, namespace string, labels map[string]string, owner *openvoxv1alpha1.CertificateAuthority) error {
	rb := &rbacv1.RoleBinding{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, rb); errors.IsNotFound(err) {
		rb = &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
				Labels:    labels,
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "Role",
				Name:     name,
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					Name:      name,
					Namespace: namespace,
				},
			},
		}
		if err := controllerutil.SetControllerReference(owner, rb, r.Scheme); err != nil {
			return err
		}
		return r.Create(ctx, rb)
	} else {
		return err
	}
}

// --- CA Setup Job ---

func (r *CertificateAuthorityReconciler) reconcileCASetupJob(ctx context.Context, ca *openvoxv1alpha1.CertificateAuthority, cfg *openvoxv1alpha1.Config, certs []openvoxv1alpha1.Certificate) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	caSecretName := fmt.Sprintf("%s-ca", ca.Name)

	// Check if CA Secret already exists
	if r.isSecretReady(ctx, caSecretName, ca.Namespace, "ca_crt.pem") {
		logger.Info("CA secret already exists", "secret", caSecretName)
		return ctrl.Result{}, nil
	}

	// CA not ready — run setup job
	ca.Status.Phase = openvoxv1alpha1.CertificateAuthorityPhaseInitializing
	_ = r.Status().Update(ctx, ca)

	jobName := fmt.Sprintf("%s-ca-setup", ca.Name)
	job := r.buildCASetupJob(ctx, ca, cfg, jobName, certs)

	return r.reconcileJob(ctx, ca, jobName, job, caSecretName)
}

func (r *CertificateAuthorityReconciler) buildCASetupJob(ctx context.Context, ca *openvoxv1alpha1.CertificateAuthority, cfg *openvoxv1alpha1.Config, jobName string, certs []openvoxv1alpha1.Certificate) *batchv1.Job {
	image := fmt.Sprintf("%s:%s", cfg.Spec.Image.Repository, cfg.Spec.Image.Tag)
	backoffLimit := int32(3)
	saName := fmt.Sprintf("%s-ca-setup", ca.Name)
	caSecretName := fmt.Sprintf("%s-ca", ca.Name)
	labels := caLabels(ca.Name)

	// Find the CA server's Certificate for the initial setup cert.
	// The CA setup job signs one cert during `puppetserver ca setup`.
	// We must pick the cert belonging to the Server with ca:true.
	certname := "puppet"
	dnsAltNames := ""
	tlsSecretName := ""
	certResourceName := ""
	if caCert := r.findCAServerCert(ctx, ca, certs); caCert != nil {
		if caCert.Spec.Certname != "" {
			certname = caCert.Spec.Certname
		}
		dnsAltNames = strings.Join(caCert.Spec.DNSAltNames, ",")
		tlsSecretName = fmt.Sprintf("%s-tls", caCert.Name)
		certResourceName = caCert.Name
	}

	script := buildCAOnlySetupScript()

	caKeySecretName := fmt.Sprintf("%s-ca-key", ca.Name)
	caCRLSecretName := fmt.Sprintf("%s-ca-crl", ca.Name)

	envVars := []corev1.EnvVar{
		{Name: "CERTNAME", Value: certname},
		{Name: "DNS_ALT_NAMES", Value: dnsAltNames},
		{Name: "CA_SECRET_NAME", Value: caSecretName},
		{Name: "CA_KEY_SECRET_NAME", Value: caKeySecretName},
		{Name: "CA_CRL_SECRET_NAME", Value: caCRLSecretName},
		{Name: "CA_NAME", Value: ca.Name},
		{Name: "CONFIG_NAME", Value: cfg.Name},
		{Name: "SSL_SECRET_NAME", Value: tlsSecretName},
		{Name: "CERT_RESOURCE_NAME", Value: certResourceName},
	}

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: ca.Namespace,
			Labels:    labels,
		},
		Spec: batchv1.JobSpec{
			BackoffLimit: &backoffLimit,
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					ServiceAccountName: saName,
					RestartPolicy:      corev1.RestartPolicyNever,
					SecurityContext: &corev1.PodSecurityContext{
						RunAsUser:    int64Ptr(1001),
						RunAsGroup:   int64Ptr(0),
						RunAsNonRoot: boolPtr(true),
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					Containers: []corev1.Container{
						{
							Name:            "ca-setup",
							Image:           image,
							ImagePullPolicy: cfg.Spec.Image.PullPolicy,
							Command:         []string{"/bin/bash", "-c", script},
							Env:             envVars,
							VolumeMounts: []corev1.VolumeMount{
								{Name: "ca-data", MountPath: "/etc/puppetlabs/puppetserver/ca"},
								{Name: "ssl", MountPath: "/etc/puppetlabs/puppet/ssl"},
								{Name: "puppet-conf", MountPath: "/etc/puppetlabs/puppet/puppet.conf", SubPath: "puppet.conf", ReadOnly: true},
							},
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: boolPtr(false),
								Capabilities: &corev1.Capabilities{
									Drop: []corev1.Capability{"ALL"},
								},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "ca-data",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: fmt.Sprintf("%s-data", ca.Name),
								},
							},
						},
						{Name: "ssl", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
						{
							Name: "puppet-conf",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: fmt.Sprintf("%s-config", cfg.Name),
									},
									Items: []corev1.KeyToPath{{Key: "puppet.conf", Path: "puppet.conf"}},
								},
							},
						},
					},
				},
			},
		},
	}
}

// buildCAOnlySetupScript returns a script that initializes the CA and creates 3 CA Secrets.
func buildCAOnlySetupScript() string {
	return `#!/bin/bash
set -euo pipefail

# CA setup (idempotent — skips if already initialized on the PVC)
if [ -f /etc/puppetlabs/puppetserver/ca/ca_crt.pem ]; then
  echo "CA already initialized, skipping setup."
else
  echo "Starting CA setup..."
  ARGS="--config /etc/puppetlabs/puppet/puppet.conf --certname ${CERTNAME}"
  if [ -n "${DNS_ALT_NAMES}" ]; then
    ARGS="${ARGS} --subject-alt-names ${DNS_ALT_NAMES}"
  fi
  puppetserver ca setup ${ARGS}
  echo "CA setup complete."
fi

NAMESPACE=$(cat /var/run/secrets/kubernetes.io/serviceaccount/namespace)
TOKEN=$(cat /var/run/secrets/kubernetes.io/serviceaccount/token)
API="https://kubernetes.default.svc/api/v1/namespaces/${NAMESPACE}/secrets"

create_or_update_secret() {
  local SECRET_NAME="$1"
  local PAYLOAD="$2"
  HTTP_CODE=$(curl -sk -o /tmp/api-response -w '%{http_code}' -X PUT \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    "${API}/${SECRET_NAME}" -d "$PAYLOAD")
  if [ "$HTTP_CODE" = "404" ]; then
    HTTP_CODE=$(curl -sk -o /tmp/api-response -w '%{http_code}' -X POST \
      -H "Authorization: Bearer $TOKEN" \
      -H "Content-Type: application/json" \
      "${API}" -d "$PAYLOAD")
  fi
  if [ "${HTTP_CODE:0:1}" != "2" ]; then
    echo "Failed to create/update Secret '${SECRET_NAME}' (HTTP ${HTTP_CODE}):" >&2
    cat /tmp/api-response >&2
    exit 1
  fi
  echo "Secret '${SECRET_NAME}' created/updated successfully."
}

# CA public cert secret (mounted in all pods)
CA_CRT=$(base64 -w0 /etc/puppetlabs/puppetserver/ca/ca_crt.pem)
create_or_update_secret "${CA_SECRET_NAME}" "{
  \"apiVersion\": \"v1\",
  \"kind\": \"Secret\",
  \"metadata\": {
    \"name\": \"${CA_SECRET_NAME}\",
    \"namespace\": \"${NAMESPACE}\",
    \"labels\": {
      \"app.kubernetes.io/managed-by\": \"openvox-operator\",
      \"app.kubernetes.io/name\": \"openvox\",
      \"openvox.voxpupuli.org/certificateauthority\": \"${CA_NAME}\"
    }
  },
  \"data\": {
    \"ca_crt.pem\": \"${CA_CRT}\"
  }
}"

# CA private key secret (never mounted — only accessed by controller via K8s API)
CA_KEY=$(base64 -w0 /etc/puppetlabs/puppetserver/ca/ca_key.pem)
create_or_update_secret "${CA_KEY_SECRET_NAME}" "{
  \"apiVersion\": \"v1\",
  \"kind\": \"Secret\",
  \"metadata\": {
    \"name\": \"${CA_KEY_SECRET_NAME}\",
    \"namespace\": \"${NAMESPACE}\",
    \"labels\": {
      \"app.kubernetes.io/managed-by\": \"openvox-operator\",
      \"app.kubernetes.io/name\": \"openvox\",
      \"openvox.voxpupuli.org/certificateauthority\": \"${CA_NAME}\"
    }
  },
  \"data\": {
    \"ca_key.pem\": \"${CA_KEY}\"
  }
}"

# CA CRL secret (mounted in non-CA pods for kubelet auto-sync)
CA_CRL=$(base64 -w0 /etc/puppetlabs/puppetserver/ca/ca_crl.pem)
INFRA_CRL=$(base64 -w0 /etc/puppetlabs/puppetserver/ca/infra_crl.pem)
create_or_update_secret "${CA_CRL_SECRET_NAME}" "{
  \"apiVersion\": \"v1\",
  \"kind\": \"Secret\",
  \"metadata\": {
    \"name\": \"${CA_CRL_SECRET_NAME}\",
    \"namespace\": \"${NAMESPACE}\",
    \"labels\": {
      \"app.kubernetes.io/managed-by\": \"openvox-operator\",
      \"app.kubernetes.io/name\": \"openvox\",
      \"openvox.voxpupuli.org/certificateauthority\": \"${CA_NAME}\"
    }
  },
  \"data\": {
    \"ca_crl.pem\": \"${CA_CRL}\",
    \"infra_crl.pem\": \"${INFRA_CRL}\"
  }
}"

echo "CA secrets created successfully."

# TLS Secret for the initial server certificate (if a Certificate resource exists)
if [ -n "${SSL_SECRET_NAME}" ] && [ -f "/etc/puppetlabs/puppet/ssl/certs/${CERTNAME}.pem" ]; then
  echo "Exporting TLS Secret for certificate '${CERTNAME}'..."
  CERT=$(base64 -w0 "/etc/puppetlabs/puppet/ssl/certs/${CERTNAME}.pem")
  KEY=$(base64 -w0 "/etc/puppetlabs/puppet/ssl/private_keys/${CERTNAME}.pem")

  create_or_update_secret "${SSL_SECRET_NAME}" "{
    \"apiVersion\": \"v1\",
    \"kind\": \"Secret\",
    \"metadata\": {
      \"name\": \"${SSL_SECRET_NAME}\",
      \"namespace\": \"${NAMESPACE}\",
      \"labels\": {
        \"app.kubernetes.io/managed-by\": \"openvox-operator\",
        \"app.kubernetes.io/name\": \"openvox\",
        \"openvox.voxpupuli.org/certificateauthority\": \"${CA_NAME}\",
        \"openvox.voxpupuli.org/certificate\": \"${CERT_RESOURCE_NAME}\"
      }
    },
    \"data\": {
      \"cert.pem\": \"${CERT}\",
      \"key.pem\": \"${KEY}\"
    }
  }"
  echo "TLS Secret '${SSL_SECRET_NAME}' created successfully."
else
  echo "No TLS Secret to export (SSL_SECRET_NAME='${SSL_SECRET_NAME}')."
fi
`
}

// --- Job lifecycle management ---

func (r *CertificateAuthorityReconciler) reconcileJob(ctx context.Context, ca *openvoxv1alpha1.CertificateAuthority, jobName string, desiredJob *batchv1.Job, expectedSecretName string) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	existingJob := &batchv1.Job{}
	err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: ca.Namespace}, existingJob)
	if errors.IsNotFound(err) {
		logger.Info("creating CA setup job", "name", jobName)
		if err := controllerutil.SetControllerReference(ca, desiredJob, r.Scheme); err != nil {
			return ctrl.Result{}, err
		}
		if err := r.Create(ctx, desiredJob); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	} else if err != nil {
		return ctrl.Result{}, err
	}

	// Check if image changed
	desiredImage := desiredJob.Spec.Template.Spec.Containers[0].Image
	currentImage := ""
	if len(existingJob.Spec.Template.Spec.Containers) > 0 {
		currentImage = existingJob.Spec.Template.Spec.Containers[0].Image
	}
	if currentImage != desiredImage {
		return r.deleteAndRequeueJob(ctx, existingJob, "image changed")
	}

	if existingJob.Status.Succeeded > 0 {
		if !r.isSecretReady(ctx, expectedSecretName, ca.Namespace, "ca_crt.pem") {
			logger.Info("job succeeded but CA secret missing, recreating", "name", jobName)
			return r.deleteAndRequeueJob(ctx, existingJob, "secret missing after success")
		}
		logger.Info("CA setup job completed successfully", "name", jobName)
		return ctrl.Result{}, nil
	}

	// Check permanent failure
	for _, c := range existingJob.Status.Conditions {
		if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
			return r.deleteAndRequeueJob(ctx, existingJob, "permanently failed")
		}
	}

	return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
}

func (r *CertificateAuthorityReconciler) deleteAndRequeueJob(ctx context.Context, job *batchv1.Job, reason string) (ctrl.Result, error) {
	log.FromContext(ctx).Info("deleting CA setup job", "name", job.Name, "reason", reason)
	propagation := metav1.DeletePropagationForeground
	if err := r.Delete(ctx, job, &client.DeleteOptions{PropagationPolicy: &propagation}); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

func (r *CertificateAuthorityReconciler) isSecretReady(ctx context.Context, name, namespace, requiredKey string) bool {
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, secret); err != nil {
		return false
	}
	if requiredKey != "" {
		_, ok := secret.Data[requiredKey]
		return ok
	}
	return true
}
