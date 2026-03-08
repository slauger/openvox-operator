package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// reconcileCertSetupRBAC ensures the ServiceAccount, Role, and RoleBinding
// for the cert setup job exist.
func (r *ServerReconciler) reconcileCertSetupRBAC(ctx context.Context, server *openvoxv1alpha1.Server, env *openvoxv1alpha1.Environment) error {
	baseName := fmt.Sprintf("%s-cert-setup", server.Name)
	sslSecretName := fmt.Sprintf("%s-ssl", server.Name)
	labels := serverLabels(server.Spec.EnvironmentRef, server.Name, "cert-setup")

	// Determine which Secrets this job may access
	resourceNames := []string{sslSecretName}
	if server.Spec.CA {
		resourceNames = append(resourceNames, fmt.Sprintf("%s-ca", server.Spec.EnvironmentRef))
	}

	// ServiceAccount
	if err := r.ensureServiceAccount(ctx, baseName, server.Namespace, labels, server); err != nil {
		return fmt.Errorf("ensuring ServiceAccount: %w", err)
	}

	// Role
	if err := r.ensureRole(ctx, baseName, server.Namespace, labels, resourceNames, server); err != nil {
		return fmt.Errorf("ensuring Role: %w", err)
	}

	// RoleBinding
	if err := r.ensureRoleBinding(ctx, baseName, server.Namespace, labels, server); err != nil {
		return fmt.Errorf("ensuring RoleBinding: %w", err)
	}

	return nil
}

func (r *ServerReconciler) ensureServiceAccount(ctx context.Context, name, namespace string, labels map[string]string, owner *openvoxv1alpha1.Server) error {
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

func (r *ServerReconciler) ensureRole(ctx context.Context, name, namespace string, labels map[string]string, resourceNames []string, owner *openvoxv1alpha1.Server) error {
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

	// Update resourceNames if changed
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

func (r *ServerReconciler) ensureRoleBinding(ctx context.Context, name, namespace string, labels map[string]string, owner *openvoxv1alpha1.Server) error {
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

// reconcileCertSetupJob ensures the SSL Secret exists for the Server.
// For CA servers, it runs the CA setup job that creates both the CA Secret and the server SSL Secret.
// For non-CA servers, it waits for the CA Secret and then runs a cert bootstrap job.
// Returns a non-zero Result if the caller should requeue.
func (r *ServerReconciler) reconcileCertSetupJob(ctx context.Context, server *openvoxv1alpha1.Server, env *openvoxv1alpha1.Environment) (ctrl.Result, error) {
	logger := log.FromContext(ctx)
	sslSecretName := fmt.Sprintf("%s-ssl", server.Name)
	caSecretName := fmt.Sprintf("%s-ca", server.Spec.EnvironmentRef)

	caReady := r.isSecretReady(ctx, caSecretName, server.Namespace, "ca_crt.pem")

	// Non-CA servers must wait for the CA Secret
	if !server.Spec.CA && !caReady {
		logger.Info("waiting for CA to be ready")
		server.Status.Phase = openvoxv1alpha1.ServerPhaseWaitingForCA
		_ = r.Status().Update(ctx, server)
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	// Check if SSL Secret already exists
	if r.isSecretReady(ctx, sslSecretName, server.Namespace, "cert.pem") {
		r.adoptSecret(ctx, sslSecretName, server)
		return ctrl.Result{}, nil
	}

	// SSL not ready — run cert job
	server.Status.Phase = openvoxv1alpha1.ServerPhaseCertSetup
	_ = r.Status().Update(ctx, server)

	// Resolve DNS alt names: role-based + pool-based
	dnsAltNames := resolveDNSAltNames(server)
	poolNames, err := r.resolvePoolDNSAltNames(ctx, server)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("resolving pool DNS alt names: %w", err)
	}
	seen := make(map[string]bool, len(dnsAltNames))
	for _, n := range dnsAltNames {
		seen[n] = true
	}
	for _, n := range poolNames {
		if !seen[n] {
			dnsAltNames = append(dnsAltNames, n)
		}
	}

	var jobName string
	var job *batchv1.Job
	if server.Spec.CA {
		jobName = fmt.Sprintf("%s-ca-setup", server.Name)
		job = r.buildCASetupJob(server, env, jobName, dnsAltNames)
	} else {
		jobName = fmt.Sprintf("%s-cert-setup", server.Name)
		job = r.buildServerCertJob(server, env, jobName, dnsAltNames)
	}

	return r.reconcileJob(ctx, server, jobName, job, sslSecretName)
}

// reconcileJob manages the lifecycle of a cert setup Job: create, check status,
// handle failures, and verify the expected Secret was created.
func (r *ServerReconciler) reconcileJob(ctx context.Context, server *openvoxv1alpha1.Server, jobName string, desiredJob *batchv1.Job, expectedSecretName string) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	existingJob := &batchv1.Job{}
	err := r.Get(ctx, types.NamespacedName{Name: jobName, Namespace: server.Namespace}, existingJob)
	if errors.IsNotFound(err) {
		logger.Info("creating cert setup job", "name", jobName)
		if err := controllerutil.SetControllerReference(server, desiredJob, r.Scheme); err != nil {
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
		// Verify the expected Secret was created
		if !r.isSecretReady(ctx, expectedSecretName, server.Namespace, "cert.pem") {
			logger.Info("job succeeded but secret missing, recreating", "name", jobName)
			return r.deleteAndRequeueJob(ctx, existingJob, "secret missing after success")
		}
		logger.Info("cert setup job completed successfully", "name", jobName)
		return ctrl.Result{}, nil
	}

	// Check permanent failure
	for _, c := range existingJob.Status.Conditions {
		if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
			return r.deleteAndRequeueJob(ctx, existingJob, "permanently failed")
		}
	}

	// Job still running
	return ctrl.Result{RequeueAfter: 15 * time.Second}, nil
}

func (r *ServerReconciler) deleteAndRequeueJob(ctx context.Context, job *batchv1.Job, reason string) (ctrl.Result, error) {
	log.FromContext(ctx).Info("deleting cert setup job", "name", job.Name, "reason", reason)
	propagation := metav1.DeletePropagationForeground
	if err := r.Delete(ctx, job, &client.DeleteOptions{PropagationPolicy: &propagation}); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
}

func (r *ServerReconciler) isSecretReady(ctx context.Context, name, namespace, requiredKey string) bool {
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

func (r *ServerReconciler) adoptSecret(ctx context.Context, name string, server *openvoxv1alpha1.Server) {
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: server.Namespace}, secret); err != nil {
		return
	}
	if !metav1.IsControlledBy(secret, server) {
		if err := controllerutil.SetControllerReference(server, secret, r.Scheme); err == nil {
			_ = r.Update(ctx, secret)
		}
	}
}

// --- Job Builders ---

func (r *ServerReconciler) buildCASetupJob(server *openvoxv1alpha1.Server, env *openvoxv1alpha1.Environment, jobName string, dnsAltNames []string) *batchv1.Job {
	image := resolveImage(server, env)
	backoffLimit := int32(3)
	saName := fmt.Sprintf("%s-cert-setup", server.Name)
	certname := resolveCertname(server)
	sslSecretName := fmt.Sprintf("%s-ssl", server.Name)
	caSecretName := fmt.Sprintf("%s-ca", server.Spec.EnvironmentRef)
	labels := serverLabels(server.Spec.EnvironmentRef, server.Name, "cert-setup")

	script := buildCASetupScript()

	envVars := []corev1.EnvVar{
		{Name: "CERTNAME", Value: certname},
		{Name: "DNS_ALT_NAMES", Value: strings.Join(dnsAltNames, ",")},
		{Name: "SSL_SECRET_NAME", Value: sslSecretName},
		{Name: "CA_SECRET_NAME", Value: caSecretName},
		{Name: "ENV_NAME", Value: server.Spec.EnvironmentRef},
		{Name: "SERVER_NAME", Value: server.Name},
	}

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: server.Namespace,
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
					},
					Containers: []corev1.Container{
						{
							Name:            "cert-setup",
							Image:           image,
							ImagePullPolicy: env.Spec.Image.PullPolicy,
							Command:         []string{"/bin/bash", "-c", script},
							Env:             envVars,
							VolumeMounts: []corev1.VolumeMount{
								{Name: "ca-data", MountPath: "/etc/puppetlabs/puppetserver/ca"},
								{Name: "ssl", MountPath: "/etc/puppetlabs/puppet/ssl"},
								{Name: "puppet-conf", MountPath: "/etc/puppetlabs/puppet/puppet.conf", SubPath: "puppet.conf", ReadOnly: true},
							},
						},
					},
					Volumes: []corev1.Volume{
						{
							Name: "ca-data",
							VolumeSource: corev1.VolumeSource{
								PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
									ClaimName: fmt.Sprintf("%s-ca-data", server.Spec.EnvironmentRef),
								},
							},
						},
						{Name: "ssl", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
						{
							Name: "puppet-conf",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: fmt.Sprintf("%s-config", server.Spec.EnvironmentRef),
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

func (r *ServerReconciler) buildServerCertJob(server *openvoxv1alpha1.Server, env *openvoxv1alpha1.Environment, jobName string, dnsAltNames []string) *batchv1.Job {
	image := resolveImage(server, env)
	backoffLimit := int32(3)
	saName := fmt.Sprintf("%s-cert-setup", server.Name)
	certname := resolveCertname(server)
	sslSecretName := fmt.Sprintf("%s-ssl", server.Name)
	caServiceName := fmt.Sprintf("%s-ca", server.Spec.EnvironmentRef)
	labels := serverLabels(server.Spec.EnvironmentRef, server.Name, "cert-setup")

	script := buildServerCertScript()

	envVars := []corev1.EnvVar{
		{Name: "CERTNAME", Value: certname},
		{Name: "DNS_ALT_NAMES", Value: strings.Join(dnsAltNames, ",")},
		{Name: "SSL_SECRET_NAME", Value: sslSecretName},
		{Name: "CA_SERVICE", Value: caServiceName},
		{Name: "ENV_NAME", Value: server.Spec.EnvironmentRef},
		{Name: "SERVER_NAME", Value: server.Name},
	}

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      jobName,
			Namespace: server.Namespace,
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
					},
					Containers: []corev1.Container{
						{
							Name:            "cert-setup",
							Image:           image,
							ImagePullPolicy: env.Spec.Image.PullPolicy,
							Command:         []string{"/bin/bash", "-c", script},
							Env:             envVars,
							VolumeMounts: []corev1.VolumeMount{
								{Name: "ssl", MountPath: "/etc/puppetlabs/puppet/ssl"},
								{Name: "puppet-conf", MountPath: "/etc/puppetlabs/puppet/puppet.conf", SubPath: "puppet.conf", ReadOnly: true},
							},
						},
					},
					Volumes: []corev1.Volume{
						{Name: "ssl", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
						{
							Name: "puppet-conf",
							VolumeSource: corev1.VolumeSource{
								ConfigMap: &corev1.ConfigMapVolumeSource{
									LocalObjectReference: corev1.LocalObjectReference{
										Name: fmt.Sprintf("%s-config", server.Spec.EnvironmentRef),
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

// --- Shell Scripts ---

func buildCASetupScript() string {
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

# 1. CA public data Secret
CA_CRT=$(base64 -w0 /etc/puppetlabs/puppetserver/ca/ca_crt.pem)
CA_CRL=$(base64 -w0 /etc/puppetlabs/puppetserver/ca/ca_crl.pem)
INFRA_CRL=$(base64 -w0 /etc/puppetlabs/puppetserver/ca/infra_crl.pem)

create_or_update_secret "${CA_SECRET_NAME}" "{
  \"apiVersion\": \"v1\",
  \"kind\": \"Secret\",
  \"metadata\": {
    \"name\": \"${CA_SECRET_NAME}\",
    \"namespace\": \"${NAMESPACE}\",
    \"labels\": {
      \"app.kubernetes.io/managed-by\": \"openvox-operator\",
      \"app.kubernetes.io/name\": \"openvox\",
      \"openvox.voxpupuli.org/environment\": \"${ENV_NAME}\"
    }
  },
  \"data\": {
    \"ca_crt.pem\": \"${CA_CRT}\",
    \"ca_crl.pem\": \"${CA_CRL}\",
    \"infra_crl.pem\": \"${INFRA_CRL}\"
  }
}"

# 2. Server SSL Secret (cert + key)
CERT=$(base64 -w0 /etc/puppetlabs/puppet/ssl/certs/${CERTNAME}.pem)
KEY=$(base64 -w0 /etc/puppetlabs/puppet/ssl/private_keys/${CERTNAME}.pem)

create_or_update_secret "${SSL_SECRET_NAME}" "{
  \"apiVersion\": \"v1\",
  \"kind\": \"Secret\",
  \"metadata\": {
    \"name\": \"${SSL_SECRET_NAME}\",
    \"namespace\": \"${NAMESPACE}\",
    \"labels\": {
      \"app.kubernetes.io/managed-by\": \"openvox-operator\",
      \"app.kubernetes.io/name\": \"openvox\",
      \"openvox.voxpupuli.org/environment\": \"${ENV_NAME}\",
      \"openvox.voxpupuli.org/server\": \"${SERVER_NAME}\"
    }
  },
  \"data\": {
    \"cert.pem\": \"${CERT}\",
    \"key.pem\": \"${KEY}\"
  }
}"

echo "All secrets created successfully."
`
}

func buildServerCertScript() string {
	return `#!/bin/bash
set -euo pipefail

echo "Waiting for CA server at ${CA_SERVICE}..."
until curl --fail --silent --insecure "https://${CA_SERVICE}:8140/status/v1/simple" | grep -q running; do
  sleep 2
done

echo "Bootstrapping SSL..."
ARGS="--server=${CA_SERVICE} --serverport=8140 --certname=${CERTNAME}"
if [ -n "${DNS_ALT_NAMES}" ]; then
  ARGS="${ARGS} --dns_alt_names=${DNS_ALT_NAMES}"
fi
puppet ssl bootstrap ${ARGS}
echo "SSL bootstrap complete."

NAMESPACE=$(cat /var/run/secrets/kubernetes.io/serviceaccount/namespace)
TOKEN=$(cat /var/run/secrets/kubernetes.io/serviceaccount/token)
API="https://kubernetes.default.svc/api/v1/namespaces/${NAMESPACE}/secrets"

CERT=$(base64 -w0 /etc/puppetlabs/puppet/ssl/certs/${CERTNAME}.pem)
KEY=$(base64 -w0 /etc/puppetlabs/puppet/ssl/private_keys/${CERTNAME}.pem)

PAYLOAD="{
  \"apiVersion\": \"v1\",
  \"kind\": \"Secret\",
  \"metadata\": {
    \"name\": \"${SSL_SECRET_NAME}\",
    \"namespace\": \"${NAMESPACE}\",
    \"labels\": {
      \"app.kubernetes.io/managed-by\": \"openvox-operator\",
      \"app.kubernetes.io/name\": \"openvox\",
      \"openvox.voxpupuli.org/environment\": \"${ENV_NAME}\",
      \"openvox.voxpupuli.org/server\": \"${SERVER_NAME}\"
    }
  },
  \"data\": {
    \"cert.pem\": \"${CERT}\",
    \"key.pem\": \"${KEY}\"
  }
}"

HTTP_CODE=$(curl -sk -o /tmp/api-response -w '%{http_code}' -X PUT \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  "${API}/${SSL_SECRET_NAME}" -d "$PAYLOAD")

if [ "$HTTP_CODE" = "404" ]; then
  HTTP_CODE=$(curl -sk -o /tmp/api-response -w '%{http_code}' -X POST \
    -H "Authorization: Bearer $TOKEN" \
    -H "Content-Type: application/json" \
    "${API}" -d "$PAYLOAD")
fi

if [ "${HTTP_CODE:0:1}" != "2" ]; then
  echo "Failed to create/update SSL Secret (HTTP ${HTTP_CODE}):" >&2
  cat /tmp/api-response >&2
  exit 1
fi

echo "SSL Secret '${SSL_SECRET_NAME}' created successfully."
`
}
