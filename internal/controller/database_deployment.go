package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

func (r *DatabaseReconciler) reconcileDeployment(ctx context.Context, db *openvoxv1alpha1.Database, cert *openvoxv1alpha1.Certificate, ca *openvoxv1alpha1.CertificateAuthority) error {
	logger := log.FromContext(ctx)
	deployName := db.Name
	image := fmt.Sprintf("%s:%s", db.Spec.Image.Repository, db.Spec.Image.Tag)

	replicas := int32(1)
	if db.Spec.Replicas != nil {
		replicas = *db.Spec.Replicas
	}

	labels := databaseLabels(db.Name)

	configMapName := fmt.Sprintf("%s-config", db.Name)
	dbSecretName := fmt.Sprintf("%s-db", db.Name)

	// Compute hashes for automatic rollout on config/secret changes
	configHash, err := r.configMapHash(ctx, configMapName, db.Namespace)
	if err != nil {
		return fmt.Errorf("computing ConfigMap hash: %w", err)
	}

	dbSecretHash, err := r.secretHash(ctx, dbSecretName, db.Namespace)
	if err != nil {
		return fmt.Errorf("computing database Secret hash: %w", err)
	}

	sslSecretName := cert.Status.SecretName
	sslHash, err := r.secretHash(ctx, sslSecretName, db.Namespace)
	if err != nil {
		return fmt.Errorf("computing SSL Secret hash: %w", err)
	}

	caSecretName := ca.Status.CASecretName
	caHash, err := r.secretHash(ctx, caSecretName, db.Namespace)
	if err != nil {
		return fmt.Errorf("computing CA Secret hash: %w", err)
	}

	pgCredsHash, err := r.secretHash(ctx, db.Spec.Postgres.CredentialsSecretRef, db.Namespace)
	if err != nil {
		return fmt.Errorf("computing PG credentials hash: %w", err)
	}

	annotations := map[string]string{
		"openvox.voxpupuli.org/config-hash":         configHash,
		"openvox.voxpupuli.org/db-secret-hash":      dbSecretHash,
		"openvox.voxpupuli.org/ssl-secret-hash":     sslHash,
		"openvox.voxpupuli.org/ca-secret-hash":      caHash,
		"openvox.voxpupuli.org/pg-credentials-hash": pgCredsHash,
	}

	deploy := &appsv1.Deployment{}
	err = r.Get(ctx, types.NamespacedName{Name: deployName, Namespace: db.Namespace}, deploy)
	if errors.IsNotFound(err) {
		logger.Info("creating Database Deployment", "name", deployName, "replicas", replicas)

		deploy = &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      deployName,
				Namespace: db.Namespace,
				Labels:    labels,
			},
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas,
				Strategy: appsv1.DeploymentStrategy{Type: appsv1.RollingUpdateDeploymentStrategyType},
				Selector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						LabelDatabase: db.Name,
					},
				},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels:      labels,
						Annotations: annotations,
					},
					Spec: r.buildPodSpec(db, cert, ca, image),
				},
			},
		}

		if err := controllerutil.SetControllerReference(db, deploy, r.Scheme); err != nil {
			return err
		}
		return r.Create(ctx, deploy)
	} else if err != nil {
		return err
	}

	// Update existing Deployment
	deploy.Spec.Replicas = &replicas
	deploy.Spec.Template.Labels = labels
	deploy.Spec.Template.Annotations = annotations
	deploy.Spec.Template.Spec = r.buildPodSpec(db, cert, ca, image)
	return r.Update(ctx, deploy)
}

func (r *DatabaseReconciler) buildPodSpec(db *openvoxv1alpha1.Database, cert *openvoxv1alpha1.Certificate, ca *openvoxv1alpha1.CertificateAuthority, image string) corev1.PodSpec {
	sslSecretName := cert.Status.SecretName
	caSecretName := ca.Status.CASecretName
	certname := cert.Spec.Certname
	configMapName := fmt.Sprintf("%s-config", db.Name)
	dbSecretName := fmt.Sprintf("%s-db", db.Name)

	defaultMode := int32(0640)

	volumeMounts := []corev1.VolumeMount{
		{Name: "ssl", MountPath: "/etc/puppetlabs/puppetdb/ssl"},
		{Name: "config", MountPath: "/etc/puppetlabs/puppetdb/conf.d/config.ini", SubPath: "config.ini", ReadOnly: true},
		{Name: "config", MountPath: "/etc/puppetlabs/puppetdb/conf.d/jetty.ini", SubPath: "jetty.ini", ReadOnly: true},
		{Name: "db-config", MountPath: "/etc/puppetlabs/puppetdb/conf.d/database.ini", SubPath: "database.ini", ReadOnly: true},
		{Name: "tmp", MountPath: "/tmp"},
		{Name: "var-log", MountPath: "/var/log/puppetlabs"},
	}

	volumes := []corev1.Volume{
		{Name: "ssl", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		{Name: "var-log", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		{
			Name: "ssl-cert",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  sslSecretName,
					DefaultMode: &defaultMode,
				},
			},
		},
		{
			Name: "ssl-ca",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  caSecretName,
					DefaultMode: &defaultMode,
				},
			},
		},
		{
			Name: "config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: configMapName},
				},
			},
		},
		{
			Name: "db-config",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  dbSecretName,
					DefaultMode: &defaultMode,
				},
			},
		},
	}

	env := []corev1.EnvVar{}
	if db.Spec.JavaArgs != "" {
		env = append(env, corev1.EnvVar{Name: "JAVA_ARGS", Value: db.Spec.JavaArgs})
	}

	container := corev1.Container{
		Name:            "openvox-db",
		Image:           image,
		ImagePullPolicy: db.Spec.Image.PullPolicy,
		Env:             env,
		Ports: []corev1.ContainerPort{
			{Name: "https", ContainerPort: DatabaseHTTPSPort, Protocol: corev1.ProtocolTCP},
		},
		Resources:    db.Spec.Resources,
		VolumeMounts: volumeMounts,
		StartupProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path:   "/status/v1/simple",
					Port:   intstr.FromInt32(DatabaseHTTPSPort),
					Scheme: corev1.URISchemeHTTPS,
				},
			},
			PeriodSeconds:    5,
			FailureThreshold: 60,
		},
		ReadinessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path:   "/status/v1/simple",
					Port:   intstr.FromInt32(DatabaseHTTPSPort),
					Scheme: corev1.URISchemeHTTPS,
				},
			},
			PeriodSeconds: 10,
		},
		LivenessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path:   "/status/v1/simple",
					Port:   intstr.FromInt32(DatabaseHTTPSPort),
					Scheme: corev1.URISchemeHTTPS,
				},
			},
			PeriodSeconds: 30,
		},
	}

	// Init container populates the writable ssl emptyDir from secret volumes.
	sslInitScript := fmt.Sprintf(`mkdir -p /ssl/certs /ssl/private_keys
cp /ssl-cert/cert.pem /ssl/certs/%s.pem
cp /ssl-cert/key.pem /ssl/private_keys/%s.pem
cp /ssl-ca/ca_crt.pem /ssl/certs/ca.pem
chmod 640 /ssl/private_keys/%s.pem`, certname, certname, certname)

	initContainer := corev1.Container{
		Name:            "tls-init",
		Image:           image,
		ImagePullPolicy: db.Spec.Image.PullPolicy,
		Command:         []string{"sh", "-c", sslInitScript},
		VolumeMounts: []corev1.VolumeMount{
			{Name: "ssl", MountPath: "/ssl"},
			{Name: "ssl-cert", MountPath: "/ssl-cert", ReadOnly: true},
			{Name: "ssl-ca", MountPath: "/ssl-ca", ReadOnly: true},
		},
	}

	containerSecurityContext := &corev1.SecurityContext{
		AllowPrivilegeEscalation: boolPtr(false),
		ReadOnlyRootFilesystem:   boolPtr(true),
		Capabilities: &corev1.Capabilities{
			Drop: []corev1.Capability{"ALL"},
		},
	}
	container.SecurityContext = containerSecurityContext
	initContainer.SecurityContext = containerSecurityContext

	automountServiceAccountToken := false
	podSpec := corev1.PodSpec{
		AutomountServiceAccountToken: &automountServiceAccountToken,
		SecurityContext: &corev1.PodSecurityContext{
			RunAsUser:    int64Ptr(DatabaseRunAsUser),
			RunAsGroup:   int64Ptr(DatabaseRunAsGroup),
			RunAsNonRoot: boolPtr(true),
			SeccompProfile: &corev1.SeccompProfile{
				Type: corev1.SeccompProfileTypeRuntimeDefault,
			},
		},
		InitContainers: []corev1.Container{initContainer},
		Containers:     []corev1.Container{container},
		Volumes:        volumes,
	}

	return podSpec
}

// configMapHash computes a deterministic SHA256 hash of a ConfigMap's data.
func (r *DatabaseReconciler) configMapHash(ctx context.Context, name, namespace string) (string, error) {
	cm := &corev1.ConfigMap{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, cm); err != nil {
		return "", err
	}
	return hashStringMap(cm.Data), nil
}

// secretHash computes a deterministic SHA256 hash of a Secret's data.
func (r *DatabaseReconciler) secretHash(ctx context.Context, name, namespace string) (string, error) {
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, secret); err != nil {
		return "", err
	}
	data := make(map[string]string, len(secret.Data))
	for k, v := range secret.Data {
		data[k] = string(v)
	}
	return hashStringMap(data), nil
}
