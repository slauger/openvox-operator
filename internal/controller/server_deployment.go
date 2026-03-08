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

func (r *ServerReconciler) reconcileDeployment(ctx context.Context, server *openvoxv1alpha1.Server, env *openvoxv1alpha1.Environment, cert *openvoxv1alpha1.Certificate, ca *openvoxv1alpha1.CertificateAuthority) error {
	logger := log.FromContext(ctx)
	deployName := server.Name
	image := resolveImage(server, env)

	replicas := int32(1)
	if server.Spec.Replicas != nil {
		replicas = *server.Spec.Replicas
	}

	javaArgs := resolveJavaArgs(server)

	role := RoleServer
	if server.Spec.CA && !server.Spec.Server {
		role = RoleCA
	}

	labels := serverLabels(server.Spec.EnvironmentRef, server.Name, role)
	if server.Spec.CA {
		labels[LabelCA] = "true"
	}

	strategy := appsv1.DeploymentStrategy{Type: appsv1.RollingUpdateDeploymentStrategyType}
	if server.Spec.CA {
		strategy = appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType}
	}

	configMapName := fmt.Sprintf("%s-config", server.Spec.EnvironmentRef)

	// Compute hashes for automatic rollout on config/secret changes
	configHash, err := r.configMapHash(ctx, configMapName, server.Namespace)
	if err != nil {
		return fmt.Errorf("computing ConfigMap hash: %w", err)
	}

	sslSecretName := cert.Status.SecretName
	sslHash, err := r.secretHash(ctx, sslSecretName, server.Namespace)
	if err != nil {
		return fmt.Errorf("computing SSL Secret hash: %w", err)
	}

	caSecretName := ca.Status.CASecretName
	caHash, err := r.secretHash(ctx, caSecretName, server.Namespace)
	if err != nil {
		return fmt.Errorf("computing CA Secret hash: %w", err)
	}

	annotations := map[string]string{
		"openvox.voxpupuli.org/config-hash":     configHash,
		"openvox.voxpupuli.org/ssl-secret-hash": sslHash,
		"openvox.voxpupuli.org/ca-secret-hash":  caHash,
	}

	deploy := &appsv1.Deployment{}
	err = r.Get(ctx, types.NamespacedName{Name: deployName, Namespace: server.Namespace}, deploy)
	if errors.IsNotFound(err) {
		logger.Info("creating Server Deployment", "name", deployName, "role", role, "replicas", replicas)

		deploy = &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      deployName,
				Namespace: server.Namespace,
				Labels:    labels,
			},
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas,
				Strategy: strategy,
				Selector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						LabelServer: server.Name,
					},
				},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels:      labels,
						Annotations: annotations,
					},
					Spec: r.buildPodSpec(server, env, cert, ca, image, javaArgs, configMapName),
				},
			},
		}

		if err := controllerutil.SetControllerReference(server, deploy, r.Scheme); err != nil {
			return err
		}
		return r.Create(ctx, deploy)
	} else if err != nil {
		return err
	}

	// Update existing Deployment
	deploy.Spec.Replicas = &replicas
	deploy.Spec.Strategy = strategy
	deploy.Spec.Template.Labels = labels
	deploy.Spec.Template.Annotations = annotations
	deploy.Spec.Template.Spec = r.buildPodSpec(server, env, cert, ca, image, javaArgs, configMapName)
	return r.Update(ctx, deploy)
}

func (r *ServerReconciler) buildPodSpec(server *openvoxv1alpha1.Server, env *openvoxv1alpha1.Environment, cert *openvoxv1alpha1.Certificate, ca *openvoxv1alpha1.CertificateAuthority, image, javaArgs, configMapName string) corev1.PodSpec {
	sslSecretName := cert.Status.SecretName
	caSecretName := ca.Status.CASecretName

	volumeMounts := []corev1.VolumeMount{
		{Name: "ssl", MountPath: "/etc/puppetlabs/puppet/ssl"},
		{Name: "puppet-conf", MountPath: "/etc/puppetlabs/puppet/puppet.conf", SubPath: "puppet.conf", ReadOnly: true},
		{Name: "puppetdb-conf", MountPath: "/etc/puppetlabs/puppet/puppetdb.conf", SubPath: "puppetdb.conf", ReadOnly: true},
		{Name: "puppetserver-conf", MountPath: "/etc/puppetlabs/puppetserver/conf.d/puppetserver.conf", SubPath: "puppetserver.conf", ReadOnly: true},
		{Name: "webserver-conf", MountPath: "/etc/puppetlabs/puppetserver/conf.d/webserver.conf", SubPath: "webserver.conf", ReadOnly: true},
		{Name: "product-conf", MountPath: "/etc/puppetlabs/puppetserver/conf.d/product.conf", SubPath: "product.conf", ReadOnly: true},
		{Name: "auth-conf", MountPath: "/etc/puppetlabs/puppetserver/conf.d/auth.conf", SubPath: "auth.conf", ReadOnly: true},
		{Name: "ca-cfg", MountPath: "/etc/puppetlabs/puppetserver/services.d/ca.cfg", SubPath: "ca.cfg", ReadOnly: true},
	}

	// SSL volume: projected from server SSL Secret + CA Secret
	defaultMode := int32(0640)
	sslVolume := corev1.Volume{
		Name: "ssl",
		VolumeSource: corev1.VolumeSource{
			Projected: &corev1.ProjectedVolumeSource{
				DefaultMode: &defaultMode,
				Sources: []corev1.VolumeProjection{
					{
						Secret: &corev1.SecretProjection{
							LocalObjectReference: corev1.LocalObjectReference{Name: sslSecretName},
							Items: []corev1.KeyToPath{
								{Key: "cert.pem", Path: "certs/puppet.pem"},
								{Key: "key.pem", Path: "private_keys/puppet.pem"},
							},
						},
					},
					{
						Secret: &corev1.SecretProjection{
							LocalObjectReference: corev1.LocalObjectReference{Name: caSecretName},
							Items: []corev1.KeyToPath{
								{Key: "ca_crt.pem", Path: "certs/ca.pem"},
								{Key: "ca_crl.pem", Path: "crl.pem"},
							},
						},
					},
				},
			},
		},
	}

	volumes := []corev1.Volume{
		sslVolume,
		configMapVolume("puppet-conf", configMapName, "puppet.conf"),
		configMapVolume("puppetdb-conf", configMapName, "puppetdb.conf"),
		configMapVolume("puppetserver-conf", configMapName, "puppetserver.conf"),
		configMapVolume("webserver-conf", configMapName, "webserver.conf"),
		configMapVolume("auth-conf", configMapName, "auth.conf"),
		configMapVolume("product-conf", configMapName, "product.conf"),
	}

	// CA-specific: mount CA data PVC, use ca-enabled.cfg
	if server.Spec.CA {
		caPVCName := fmt.Sprintf("%s-data", ca.Name)
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      "ca-data",
			MountPath: "/etc/puppetlabs/puppetserver/ca",
		})
		volumes = append(volumes,
			corev1.Volume{
				Name: "ca-data",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: caPVCName,
					},
				},
			},
			configMapVolumeWithKey("ca-cfg", configMapName, "ca-enabled.cfg", "ca.cfg"),
		)
	} else {
		volumes = append(volumes,
			configMapVolumeWithKey("ca-cfg", configMapName, "ca-disabled.cfg", "ca.cfg"),
		)
	}

	// Code volume: Server override > Environment default
	code := env.Spec.Code
	if server.Spec.Code != nil {
		code = server.Spec.Code
	}
	if code != nil {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      "code",
			MountPath: env.Spec.Puppet.EnvironmentPath,
			ReadOnly:  true,
		})
		volumes = append(volumes, corev1.Volume{
			Name: "code",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: code.ClaimName,
					ReadOnly:  true,
				},
			},
		})
	}

	container := corev1.Container{
		Name:            "openvox-server",
		Image:           image,
		ImagePullPolicy: env.Spec.Image.PullPolicy,
		Env: []corev1.EnvVar{
			{Name: "JAVA_ARGS", Value: javaArgs},
		},
		Ports: []corev1.ContainerPort{
			{Name: "https", ContainerPort: 8140, Protocol: corev1.ProtocolTCP},
		},
		Resources:    server.Spec.Resources,
		VolumeMounts: volumeMounts,
		ReadinessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(8140)},
			},
			InitialDelaySeconds: 60,
			PeriodSeconds:       10,
		},
		LivenessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				TCPSocket: &corev1.TCPSocketAction{Port: intstr.FromInt32(8140)},
			},
			InitialDelaySeconds: 120,
			PeriodSeconds:       30,
		},
	}

	return corev1.PodSpec{
		ServiceAccountName: fmt.Sprintf("%s-server", server.Spec.EnvironmentRef),
		SecurityContext: &corev1.PodSecurityContext{
			RunAsUser:    int64Ptr(1001),
			RunAsGroup:   int64Ptr(0),
			RunAsNonRoot: boolPtr(true),
		},
		Containers: []corev1.Container{container},
		Volumes:    volumes,
	}
}

// resolveJavaArgs determines JVM arguments for a Server.
func resolveJavaArgs(server *openvoxv1alpha1.Server) string {
	if server.Spec.JavaArgs != "" {
		return server.Spec.JavaArgs
	}
	if memLimit, ok := server.Spec.Resources.Limits[corev1.ResourceMemory]; ok {
		heapMB := memLimit.Value() * 9 / 10 / (1024 * 1024)
		return fmt.Sprintf("-Xms%dm -Xmx%dm", heapMB, heapMB)
	}
	return "-Xms512m -Xmx1024m"
}

// configMapHash computes a deterministic SHA256 hash of a ConfigMap's data.
func (r *ServerReconciler) configMapHash(ctx context.Context, name, namespace string) (string, error) {
	cm := &corev1.ConfigMap{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, cm); err != nil {
		return "", err
	}
	return hashStringMap(cm.Data), nil
}

// secretHash computes a deterministic SHA256 hash of a Secret's data.
func (r *ServerReconciler) secretHash(ctx context.Context, name, namespace string) (string, error) {
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
