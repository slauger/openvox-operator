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

func (r *ServerReconciler) reconcileDeployment(ctx context.Context, server *openvoxv1alpha1.Server, cfg *openvoxv1alpha1.Config, cert *openvoxv1alpha1.Certificate, ca *openvoxv1alpha1.CertificateAuthority) error {
	logger := log.FromContext(ctx)
	deployName := server.Name
	image := resolveImage(server, cfg)

	replicas := int32(1)
	if server.Spec.Replicas != nil {
		replicas = *server.Spec.Replicas
	}

	javaArgs := resolveJavaArgs(server)
	// Override max-active-instances via JVM system property.
	// The ConfigMap sets max-active-instances: 2 as a safe default,
	// but each Server can specify its own value. HOCON's Typesafe Config
	// library resolves -D system properties as overrides.
	maxActive := server.Spec.MaxActiveInstances
	if maxActive <= 0 {
		maxActive = 1
	}
	javaArgs = fmt.Sprintf("%s -Djruby-puppet.max-active-instances=%d", javaArgs, maxActive)

	role := RoleServer
	if server.Spec.CA && !server.Spec.Server {
		role = RoleCA
	}

	labels := serverLabels(server.Spec.ConfigRef, server.Name, role)
	if server.Spec.CA {
		labels[LabelCA] = "true"
	}
	for _, poolRef := range server.Spec.PoolRefs {
		labels[poolLabel(poolRef)] = "true"
	}

	strategy := appsv1.DeploymentStrategy{Type: appsv1.RollingUpdateDeploymentStrategyType}
	if server.Spec.CA {
		strategy = appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType}
	}

	configMapName := fmt.Sprintf("%s-config", server.Spec.ConfigRef)

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

	// Add code image annotation for server:true pods to trigger rollout on image change
	if server.Spec.Server {
		if code := resolveCode(server, cfg); code != nil && code.Image != "" {
			annotations["openvox.voxpupuli.org/code-image"] = code.Image
		}
	}

	// Add ENC secret hash annotation for server pods to trigger rollout on ENC config changes
	if server.Spec.Server && cfg.Spec.NodeClassifierRef != "" {
		encSecretName := fmt.Sprintf("%s-enc", server.Spec.ConfigRef)
		if encHash, err := r.secretHash(ctx, encSecretName, server.Namespace); err == nil {
			annotations["openvox.voxpupuli.org/enc-secret-hash"] = encHash
		}
	}

	// Add report-webhook secret hash annotation for server pods to trigger rollout on report config changes
	if server.Spec.Server {
		reportSecretName := fmt.Sprintf("%s-report-webhook", server.Spec.ConfigRef)
		if reportHash, err := r.secretHash(ctx, reportSecretName, server.Namespace); err == nil {
			annotations["openvox.voxpupuli.org/report-webhook-secret-hash"] = reportHash
		}
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
					Spec: r.buildPodSpec(server, cfg, cert, ca, image, javaArgs, configMapName),
				},
			},
		}

		if !server.Spec.Autoscaling.Enabled {
			deploy.Spec.Replicas = &replicas
		}
		if err := controllerutil.SetControllerReference(server, deploy, r.Scheme); err != nil {
			return err
		}
		return r.Create(ctx, deploy)
	} else if err != nil {
		return err
	}

	// Update existing Deployment
	// Only set replicas when HPA is not managing scaling
	if !server.Spec.Autoscaling.Enabled {
		deploy.Spec.Replicas = &replicas
	}
	deploy.Spec.Strategy = strategy
	deploy.Spec.Template.Labels = labels
	deploy.Spec.Template.Annotations = annotations
	deploy.Spec.Template.Spec = r.buildPodSpec(server, cfg, cert, ca, image, javaArgs, configMapName)
	return r.Update(ctx, deploy)
}

func (r *ServerReconciler) buildPodSpec(server *openvoxv1alpha1.Server, cfg *openvoxv1alpha1.Config, cert *openvoxv1alpha1.Certificate, ca *openvoxv1alpha1.CertificateAuthority, image, javaArgs, configMapName string) corev1.PodSpec {
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
		{Name: "ca-conf", MountPath: "/etc/puppetlabs/puppetserver/conf.d/ca.conf", SubPath: "ca.conf", ReadOnly: true},
		{Name: "ca-cfg", MountPath: "/etc/puppetlabs/puppetserver/services.d/ca.cfg", SubPath: "ca.cfg", ReadOnly: true},
		{Name: "logback-xml", MountPath: "/etc/puppetlabs/puppetserver/logback.xml", SubPath: "logback.xml", ReadOnly: true},
		{Name: "metrics-conf", MountPath: "/etc/puppetlabs/puppetserver/conf.d/metrics.conf", SubPath: "metrics.conf", ReadOnly: true},
		{Name: "puppetserver-yaml", MountPath: "/opt/puppetlabs/server/data/puppetserver/yaml"},
		{Name: "puppetserver-state", MountPath: "/opt/puppetlabs/server/data/puppetserver/state"},
		{Name: "puppetserver-bucket", MountPath: "/opt/puppetlabs/server/data/puppetserver/bucket"},
		{Name: "puppetserver-reports", MountPath: "/opt/puppetlabs/server/data/puppetserver/reports"},
		{Name: "tmp", MountPath: "/tmp"},
		{Name: "var-log", MountPath: "/var/log/puppetlabs"},
		{Name: "var-run", MountPath: "/var/run"},
	}

	// SSL: emptyDir populated by init container from secret volumes.
	// OpenVox needs full read-write on the ssl directory (creates subdirs, syncs CRL, sets permissions).
	defaultMode := int32(0640)
	crlSecretName := fmt.Sprintf("%s-ca-crl", ca.Name)

	volumes := []corev1.Volume{
		{Name: "ssl", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		{Name: "puppetserver-yaml", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		{Name: "puppetserver-state", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		{Name: "puppetserver-bucket", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		{Name: "puppetserver-reports", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		{Name: "var-log", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		{Name: "var-run", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
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
		configMapVolume("puppet-conf", configMapName, "puppet.conf"),
		configMapVolume("puppetdb-conf", configMapName, "puppetdb.conf"),
		configMapVolume("puppetserver-conf", configMapName, "puppetserver.conf"),
		configMapVolume("auth-conf", configMapName, "auth.conf"),
		configMapVolume("ca-conf", configMapName, "ca.conf"),
		configMapVolume("product-conf", configMapName, "product.conf"),
		configMapVolume("logback-xml", configMapName, "logback.xml"),
		configMapVolume("metrics-conf", configMapName, "metrics.conf"),
	}

	// Non-CA pods: mount CRL secret as directory (NOT SubPath) for kubelet auto-sync,
	// and use the standard webserver.conf pointing to the CRL secret mount.
	// CA pods: no CRL volume (Puppetserver manages CRL from PVC), use webserver-ca.conf.
	if server.Spec.CA {
		volumes = append(volumes,
			configMapVolumeWithKey("webserver-conf", configMapName, "webserver-ca.conf", "webserver.conf"),
		)
	} else {
		volumes = append(volumes,
			configMapVolume("webserver-conf", configMapName, "webserver.conf"),
			corev1.Volume{
				Name: "ssl-ca-crl",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName:  crlSecretName,
						DefaultMode: &defaultMode,
					},
				},
			},
		)
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      "ssl-ca-crl",
			MountPath: "/etc/puppetlabs/puppet/crl",
			ReadOnly:  true,
		})
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

		// Mount autosign policy Secret (always present, managed by Config controller)
		autosignSecretName := fmt.Sprintf("%s-autosign-policy", ca.Name)
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      "autosign-policy",
			MountPath: "/etc/puppetlabs/puppet/autosign-policy.yaml",
			SubPath:   "autosign-policy.yaml",
			ReadOnly:  true,
		})
		volumes = append(volumes, corev1.Volume{
			Name: "autosign-policy",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName: autosignSecretName,
				},
			},
		})
	} else {
		volumes = append(volumes,
			configMapVolumeWithKey("ca-cfg", configMapName, "ca-disabled.cfg", "ca.cfg"),
		)
	}

	// Code volume: only mounted for server:true pods (CA-only pods don't compile catalogs)
	if server.Spec.Server {
		if code := resolveCode(server, cfg); code != nil {
			volumeMounts = append(volumeMounts, corev1.VolumeMount{
				Name:      "code",
				MountPath: cfg.Spec.Puppet.EnvironmentPath,
				ReadOnly:  true,
			})
			switch {
			case code.Image != "":
				pullPolicy := code.ImagePullPolicy
				if pullPolicy == "" {
					pullPolicy = corev1.PullIfNotPresent
				}
				volumes = append(volumes, corev1.Volume{
					Name: "code",
					VolumeSource: corev1.VolumeSource{
						Image: &corev1.ImageVolumeSource{
							Reference:  code.Image,
							PullPolicy: pullPolicy,
						},
					},
				})
			case code.ClaimName != "":
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
		}
	}

	// ENC: mount config Secret and cache emptyDir for server:true pods with nodeClassifierRef
	if server.Spec.Server && cfg.Spec.NodeClassifierRef != "" {
		encSecretName := fmt.Sprintf("%s-enc", server.Spec.ConfigRef)
		volumeMounts = append(volumeMounts,
			corev1.VolumeMount{
				Name:      "enc-config",
				MountPath: "/etc/puppetlabs/puppet/enc.yaml",
				SubPath:   "enc.yaml",
				ReadOnly:  true,
			},
			corev1.VolumeMount{
				Name:      "enc-cache",
				MountPath: "/var/cache/openvox-enc",
			},
		)
		volumes = append(volumes,
			corev1.Volume{
				Name: "enc-config",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: encSecretName,
					},
				},
			},
			corev1.Volume{
				Name:         "enc-cache",
				VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
			},
		)
	}

	// ReportProcessor: mount config Secret for server:true pods when report-webhook Secret exists
	// The Secret is created by ReportProcessorReconciler when any ReportProcessor references this Config.
	// We mount it if it exists; the Secret presence indicates webhook reports are configured.
	if server.Spec.Server {
		reportSecretName := fmt.Sprintf("%s-report-webhook", server.Spec.ConfigRef)
		volumeMounts = append(volumeMounts,
			corev1.VolumeMount{
				Name:      "report-webhook-config",
				MountPath: "/etc/puppetlabs/puppet/report-webhook.yaml",
				SubPath:   "report-webhook.yaml",
				ReadOnly:  true,
			},
		)
		volumes = append(volumes,
			corev1.Volume{
				Name: "report-webhook-config",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: reportSecretName,
						Optional:   boolPtr(true),
					},
				},
			},
		)
	}

	container := corev1.Container{
		Name:            "openvox-server",
		Image:           image,
		ImagePullPolicy: cfg.Spec.Image.PullPolicy,
		Env: []corev1.EnvVar{
			{Name: "JAVA_ARGS", Value: javaArgs},
		},
		Ports: []corev1.ContainerPort{
			{Name: "https", ContainerPort: 8140, Protocol: corev1.ProtocolTCP},
		},
		Resources:    server.Spec.Resources,
		VolumeMounts: volumeMounts,
		StartupProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path:   "/status/v1/simple",
					Port:   intstr.FromInt32(8140),
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
					Port:   intstr.FromInt32(8140),
					Scheme: corev1.URISchemeHTTPS,
				},
			},
			PeriodSeconds: 10,
		},
		LivenessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				HTTPGet: &corev1.HTTPGetAction{
					Path:   "/status/v1/simple",
					Port:   intstr.FromInt32(8140),
					Scheme: corev1.URISchemeHTTPS,
				},
			},
			PeriodSeconds: 30,
		},
	}

	// Init container populates the writable ssl emptyDir from secret volumes.
	// CRL is NOT copied here -- non-CA pods get it from a kubelet-synced secret mount,
	// CA pods get it from the PVC at startup.
	sslInitScript := `mkdir -p /ssl/certs /ssl/private_keys /ssl/public_keys /ssl/certificate_requests /ssl/private
cp /ssl-cert/cert.pem /ssl/certs/puppet.pem
cp /ssl-cert/key.pem /ssl/private_keys/puppet.pem
cp /ssl-ca/ca_crt.pem /ssl/certs/ca.pem
chmod 640 /ssl/private_keys/puppet.pem`
	initContainer := corev1.Container{
		Name:            "tls-init",
		Image:           image,
		ImagePullPolicy: cfg.Spec.Image.PullPolicy,
		Command:         []string{"sh", "-c", sslInitScript},
		VolumeMounts: []corev1.VolumeMount{
			{Name: "ssl", MountPath: "/ssl"},
			{Name: "ssl-cert", MountPath: "/ssl-cert", ReadOnly: true},
			{Name: "ssl-ca", MountPath: "/ssl-ca", ReadOnly: true},
		},
	}

	readOnlyRootFilesystem := true
	containerSecurityContext := &corev1.SecurityContext{
		AllowPrivilegeEscalation: boolPtr(false),
		ReadOnlyRootFilesystem:   &readOnlyRootFilesystem,
		Capabilities: &corev1.Capabilities{
			Drop: []corev1.Capability{"ALL"},
		},
	}
	container.SecurityContext = containerSecurityContext
	initContainer.SecurityContext = containerSecurityContext

	automountServiceAccountToken := false
	podSpec := corev1.PodSpec{
		ServiceAccountName:           fmt.Sprintf("%s-server", server.Spec.ConfigRef),
		AutomountServiceAccountToken: &automountServiceAccountToken,
		SecurityContext: &corev1.PodSecurityContext{
			RunAsUser:    int64Ptr(1001),
			RunAsGroup:   int64Ptr(0),
			RunAsNonRoot: boolPtr(true),
			SeccompProfile: &corev1.SeccompProfile{
				Type: corev1.SeccompProfileTypeRuntimeDefault,
			},
		},
		TopologySpreadConstraints: server.Spec.TopologySpreadConstraints,
		Affinity:                  server.Spec.Affinity,
		InitContainers:            []corev1.Container{initContainer},
		Containers:                []corev1.Container{container},
		Volumes:                   volumes,
	}

	// Add imagePullSecrets for code image if configured
	if server.Spec.Server {
		if code := resolveCode(server, cfg); code != nil && code.ImagePullSecret != "" {
			podSpec.ImagePullSecrets = append(podSpec.ImagePullSecrets, corev1.LocalObjectReference{
				Name: code.ImagePullSecret,
			})
		}
	}

	return podSpec
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
