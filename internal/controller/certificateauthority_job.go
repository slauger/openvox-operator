package controller

import (
	"context"
	"fmt"
	"slices"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

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
	logger := log.FromContext(ctx)

	// Build set of Config names referencing this CA
	cfgList := &openvoxv1alpha1.ConfigList{}
	if err := r.List(ctx, cfgList, client.InNamespace(ca.Namespace)); err != nil {
		logger.Error(err, "failed to list Configs for CA server cert discovery", "namespace", ca.Namespace)
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
		logger.Error(err, "failed to list Servers for CA server cert discovery", "namespace", ca.Namespace)
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

// --- CA Setup Job ---

func (r *CertificateAuthorityReconciler) reconcileCASetupJob(ctx context.Context, ca *openvoxv1alpha1.CertificateAuthority, cfg *openvoxv1alpha1.Config, certs []openvoxv1alpha1.Certificate) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	caSecretName := fmt.Sprintf("%s-ca", ca.Name)

	// Check if CA Secret already exists
	if isSecretReady(ctx, r.Client, caSecretName, ca.Namespace, "ca_crt.pem") {
		logger.Info("CA secret already exists", "secret", caSecretName)
		return ctrl.Result{}, nil
	}

	// CA not ready -- run setup job
	ca.Status.Phase = openvoxv1alpha1.CertificateAuthorityPhaseInitializing
	if statusErr := r.Status().Update(ctx, ca); statusErr != nil {
		logger.Error(statusErr, "failed to update CertificateAuthority status", "name", ca.Name)
	}

	jobName := fmt.Sprintf("%s-ca-setup", ca.Name)
	job := r.buildCASetupJob(ctx, ca, cfg, jobName, certs)

	return r.reconcileJob(ctx, ca, jobName, job, caSecretName)
}

func (r *CertificateAuthorityReconciler) buildCASetupJob(ctx context.Context, ca *openvoxv1alpha1.CertificateAuthority, cfg *openvoxv1alpha1.Config, jobName string, certs []openvoxv1alpha1.Certificate) *batchv1.Job {
	image := fmt.Sprintf("%s:%s", cfg.Spec.Image.Repository, cfg.Spec.Image.Tag)
	backoffLimit := CAJobBackoffLimit
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
		// Append CA Service FQDN to DNS alt names so the CA server cert
		// is valid for internal operator communication (CSR signing, CRL refresh).
		// This is done here transparently without modifying the Certificate CR.
		serviceFQDN := fmt.Sprintf("%s.%s.svc", caInternalServiceName(ca.Name), ca.Namespace)
		altNames := make([]string, len(caCert.Spec.DNSAltNames))
		copy(altNames, caCert.Spec.DNSAltNames)
		if !slices.Contains(altNames, serviceFQDN) {
			altNames = append(altNames, serviceFQDN)
		}
		dnsAltNames = strings.Join(altNames, ",")
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
		{Name: "CA_UID", Value: string(ca.UID)},
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
						RunAsUser:    int64Ptr(CASetupRunAsUser),
						RunAsGroup:   int64Ptr(CASetupRunAsGroup),
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
							Resources:       resolveCAJobResources(ca),
							VolumeMounts: []corev1.VolumeMount{
								{Name: "ca-data", MountPath: "/etc/puppetlabs/puppetserver/ca"},
								{Name: "ssl", MountPath: "/etc/puppetlabs/puppet/ssl"},
								{Name: "puppet-conf", MountPath: "/etc/puppetlabs/puppet/puppet.conf", SubPath: "puppet.conf", ReadOnly: true},
								{Name: "puppetserver-conf", MountPath: "/etc/puppetlabs/puppetserver/conf.d/puppetserver.conf", SubPath: "puppetserver.conf", ReadOnly: true},
								{Name: "puppetserver-data", MountPath: "/run/puppetserver"},
								{Name: "tmp", MountPath: "/tmp"},
							},
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: boolPtr(false),
								ReadOnlyRootFilesystem:   boolPtr(true),
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
						{Name: "puppetserver-data", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
						{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
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
						{
							Name: "puppetserver-conf",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: fmt.Sprintf("%s-config", cfg.Name),
									},
									Items: []corev1.KeyToPath{{Key: "puppetserver.conf", Path: "puppetserver.conf"}},
								},
							},
						},
					},
				},
			},
		},
	}
}

// resolveCAJobResources returns the user-specified resources or sensible defaults for the JVM-based CA setup Job.
func resolveCAJobResources(ca *openvoxv1alpha1.CertificateAuthority) corev1.ResourceRequirements {
	if len(ca.Spec.Resources.Requests) > 0 || len(ca.Spec.Resources.Limits) > 0 {
		return ca.Spec.Resources
	}
	return corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(DefaultCAJobCPURequest),
			corev1.ResourceMemory: resource.MustParse(DefaultCAJobMemoryRequest),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse(DefaultCAJobCPULimit),
			corev1.ResourceMemory: resource.MustParse(DefaultCAJobMemoryLimit),
		},
	}
}

// buildCAOnlySetupScript returns a script that initializes the CA and creates 3 CA Secrets.
func buildCAOnlySetupScript() string {
	return `#!/bin/bash
set -euo pipefail

# CA setup (idempotent -- skips if already initialized on the PVC)
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
CACERT=/var/run/secrets/kubernetes.io/serviceaccount/ca.crt
API="https://kubernetes.default.svc/api/v1/namespaces/${NAMESPACE}/secrets"

create_or_update_secret() {
  local SECRET_NAME="$1"
  local PAYLOAD="$2"
  HTTP_CODE=$(curl -s --cacert "$CACERT" -o /tmp/api-response -w '%{http_code}' -X PUT \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    "${API}/${SECRET_NAME}" -d "$PAYLOAD")
  if [ "$HTTP_CODE" = "404" ]; then
    HTTP_CODE=$(curl -s --cacert "$CACERT" -o /tmp/api-response -w '%{http_code}' -X POST \
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

# ownerReference block for all Secrets (GC deletes them when the CA is deleted)
OWNER_REF="\"ownerReferences\": [{
      \"apiVersion\": \"openvox.voxpupuli.org/v1alpha1\",
      \"kind\": \"CertificateAuthority\",
      \"name\": \"${CA_NAME}\",
      \"uid\": \"${CA_UID}\",
      \"controller\": true,
      \"blockOwnerDeletion\": true
    }]"

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
    },
    ${OWNER_REF}
  },
  \"data\": {
    \"ca_crt.pem\": \"${CA_CRT}\"
  }
}"

# CA private key secret (never mounted -- only accessed by controller via K8s API)
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
    },
    ${OWNER_REF}
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
    },
    ${OWNER_REF}
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
      },
      ${OWNER_REF}
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
		return ctrl.Result{RequeueAfter: RequeueIntervalMedium}, nil
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
		if !isSecretReady(ctx, r.Client, expectedSecretName, ca.Namespace, "ca_crt.pem") {
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

	return ctrl.Result{RequeueAfter: RequeueIntervalLong}, nil
}

func (r *CertificateAuthorityReconciler) deleteAndRequeueJob(ctx context.Context, job *batchv1.Job, reason string) (ctrl.Result, error) {
	log.FromContext(ctx).Info("deleting CA setup job", "name", job.Name, "reason", reason)
	propagation := metav1.DeletePropagationForeground
	if err := r.Delete(ctx, job, &client.DeleteOptions{PropagationPolicy: &propagation}); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: RequeueIntervalMedium}, nil
}
