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
	"sigs.k8s.io/controller-runtime/pkg/log"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// reconcileCompilerDeployment creates or updates the compiler Deployment.
func (r *OpenVoxServerReconciler) reconcileCompilerDeployment(ctx context.Context, ovs *openvoxv1alpha1.OpenVoxServer) error {
	logger := log.FromContext(ctx)
	deployName := fmt.Sprintf("%s-compiler", ovs.Name)
	image := fmt.Sprintf("%s:%s", ovs.Spec.Image.Repository, ovs.Spec.Image.Tag)

	replicas := int32(1)
	if ovs.Spec.Compilers.Replicas != nil {
		replicas = *ovs.Spec.Compilers.Replicas
	}

	javaArgs := "-Xms512m -Xmx1024m"
	if ovs.Spec.Compilers.JavaArgs != "" {
		javaArgs = ovs.Spec.Compilers.JavaArgs
	}

	caServiceName := fmt.Sprintf("%s-ca", ovs.Name)

	deploy := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: deployName, Namespace: ovs.Namespace}, deploy)
	if errors.IsNotFound(err) {
		logger.Info("creating Compiler Deployment", "name", deployName, "replicas", replicas)
		deploy = &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      deployName,
				Namespace: ovs.Namespace,
				Labels:    compilerLabels(ovs),
			},
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas,
				Selector: &metav1.LabelSelector{
					MatchLabels: compilerLabels(ovs),
				},
				Template: corev1.PodTemplateSpec{
					ObjectMeta: metav1.ObjectMeta{
						Labels: compilerLabels(ovs),
					},
					Spec: corev1.PodSpec{
						SecurityContext: &corev1.PodSecurityContext{
							RunAsUser:    int64Ptr(1001),
							RunAsGroup:   int64Ptr(0),
							RunAsNonRoot: boolPtr(true),
						},
						InitContainers: []corev1.Container{
							{
								Name:    "ssl-bootstrap",
								Image:   image,
								Command: []string{"/bin/bash", "-c"},
								Args: []string{fmt.Sprintf(`set -euo pipefail
SSL_DIR=/etc/puppetlabs/puppet/ssl
CERT_FILE="${SSL_DIR}/certs/$(hostname).pem"

if [ -f "${CERT_FILE}" ]; then
    echo "SSL certificate already exists, skipping bootstrap"
    exit 0
fi

echo "Waiting for CA server..."
until curl --fail --silent --insecure "https://%s:8140/status/v1/simple" | grep -q running; do
    sleep 2
done

echo "Bootstrapping SSL..."
puppetserver ca setup --config /etc/puppetlabs/puppetserver/conf.d || true
puppet ssl bootstrap --server="%s" --serverport=8140
echo "SSL bootstrap complete."
`, caServiceName, caServiceName)},
								VolumeMounts: []corev1.VolumeMount{
									{
										Name:      "ssl",
										MountPath: "/etc/puppetlabs/puppet/ssl",
									},
									{
										Name:      "puppet-conf",
										MountPath: "/etc/puppetlabs/puppet/puppet.conf",
										SubPath:   "puppet.conf",
										ReadOnly:  true,
									},
								},
							},
						},
						Containers: []corev1.Container{
							{
								Name:  "puppetserver",
								Image: image,
								Env: []corev1.EnvVar{
									{Name: "JAVA_ARGS", Value: javaArgs},
								},
								Ports: []corev1.ContainerPort{
									{
										Name:          "https",
										ContainerPort: 8140,
										Protocol:      corev1.ProtocolTCP,
									},
								},
								Resources: ovs.Spec.Compilers.Resources,
								VolumeMounts: []corev1.VolumeMount{
									{
										Name:      "ssl",
										MountPath: "/etc/puppetlabs/puppet/ssl",
									},
									{
										Name:      "puppet-conf",
										MountPath: "/etc/puppetlabs/puppet/puppet.conf",
										SubPath:   "puppet.conf",
										ReadOnly:  true,
									},
									{
										Name:      "puppetdb-conf",
										MountPath: "/etc/puppetlabs/puppet/puppetdb.conf",
										SubPath:   "puppetdb.conf",
										ReadOnly:  true,
									},
									{
										Name:      "puppetserver-conf",
										MountPath: "/etc/puppetlabs/puppetserver/conf.d/puppetserver.conf",
										SubPath:   "puppetserver.conf",
										ReadOnly:  true,
									},
									{
										Name:      "webserver-conf",
										MountPath: "/etc/puppetlabs/puppetserver/conf.d/webserver.conf",
										SubPath:   "webserver.conf",
										ReadOnly:  true,
									},
									{
										Name:      "product-conf",
										MountPath: "/etc/puppetlabs/puppetserver/conf.d/product.conf",
										SubPath:   "product.conf",
										ReadOnly:  true,
									},
									{
										Name:      "ca-disabled",
										MountPath: "/etc/puppetlabs/puppetserver/services.d/ca.cfg",
										SubPath:   "ca.cfg",
										ReadOnly:  true,
									},
								},
								ReadinessProbe: &corev1.Probe{
									ProbeHandler: corev1.ProbeHandler{
										TCPSocket: &corev1.TCPSocketAction{
											Port: intstr.FromInt32(8140),
										},
									},
									InitialDelaySeconds: 60,
									PeriodSeconds:       10,
								},
								LivenessProbe: &corev1.Probe{
									ProbeHandler: corev1.ProbeHandler{
										TCPSocket: &corev1.TCPSocketAction{
											Port: intstr.FromInt32(8140),
										},
									},
									InitialDelaySeconds: 120,
									PeriodSeconds:       30,
								},
							},
						},
						Volumes: r.compilerVolumes(ovs),
					},
				},
			},
		}

		if err := r.setOwnerReference(ovs, deploy); err != nil {
			return err
		}
		return r.Create(ctx, deploy)
	} else if err != nil {
		return err
	}

	// Update existing Deployment
	deploy.Spec.Replicas = &replicas
	deploy.Spec.Template.Spec.Containers[0].Image = image
	deploy.Spec.Template.Spec.Containers[0].Resources = ovs.Spec.Compilers.Resources
	deploy.Spec.Template.Spec.Containers[0].Env = []corev1.EnvVar{
		{Name: "JAVA_ARGS", Value: javaArgs},
	}
	return r.Update(ctx, deploy)
}

// reconcileCompilerService creates the compiler Service.
func (r *OpenVoxServerReconciler) reconcileCompilerService(ctx context.Context, ovs *openvoxv1alpha1.OpenVoxServer) error {
	svcName := fmt.Sprintf("%s-compiler", ovs.Name)

	svc := &corev1.Service{}
	err := r.Get(ctx, types.NamespacedName{Name: svcName, Namespace: ovs.Namespace}, svc)
	if errors.IsNotFound(err) {
		port := int32(8140)
		if ovs.Spec.Puppet.ServerPort != 0 {
			port = ovs.Spec.Puppet.ServerPort
		}

		svc = &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      svcName,
				Namespace: ovs.Namespace,
				Labels:    compilerLabels(ovs),
			},
			Spec: corev1.ServiceSpec{
				Selector: compilerLabels(ovs),
				Ports: []corev1.ServicePort{
					{
						Name:       "https",
						Port:       port,
						TargetPort: intstr.FromInt32(8140),
						Protocol:   corev1.ProtocolTCP,
					},
				},
			},
		}

		if err := r.setOwnerReference(ovs, svc); err != nil {
			return err
		}
		return r.Create(ctx, svc)
	}
	return err
}

// compilerVolumes returns the volume list for compiler pods.
func (r *OpenVoxServerReconciler) compilerVolumes(ovs *openvoxv1alpha1.OpenVoxServer) []corev1.Volume {
	configMapName := fmt.Sprintf("%s-config", ovs.Name)

	// CA disabled config for compilers
	caDisabledContent := `puppetlabs.services.ca.certificate-authority-disabled-service/certificate-authority-disabled-service
puppetlabs.trapperkeeper.services.watcher.filesystem-watch-service/filesystem-watch-service
`

	volumes := []corev1.Volume{
		{
			Name: "ssl",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{},
			},
		},
		{
			Name: "puppet-conf",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: configMapName},
					Items: []corev1.KeyToPath{
						{Key: "puppet.conf", Path: "puppet.conf"},
					},
				},
			},
		},
		{
			Name: "puppetdb-conf",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: configMapName},
					Items: []corev1.KeyToPath{
						{Key: "puppetdb.conf", Path: "puppetdb.conf"},
					},
				},
			},
		},
		{
			Name: "puppetserver-conf",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: configMapName},
					Items: []corev1.KeyToPath{
						{Key: "puppetserver.conf", Path: "puppetserver.conf"},
					},
				},
			},
		},
		{
			Name: "webserver-conf",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: configMapName},
					Items: []corev1.KeyToPath{
						{Key: "webserver.conf", Path: "webserver.conf"},
					},
				},
			},
		},
		{
			Name: "product-conf",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: configMapName},
					Items: []corev1.KeyToPath{
						{Key: "product.conf", Path: "product.conf"},
					},
				},
			},
		},
	}

	// CA disabled ConfigMap — we need to create this as a separate ConfigMap
	// For now, use a projected volume with inline content via a ConfigMap
	_ = caDisabledContent
	caDisabledCMName := fmt.Sprintf("%s-ca-disabled", ovs.Name)
	volumes = append(volumes, corev1.Volume{
		Name: "ca-disabled",
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: caDisabledCMName},
				Items: []corev1.KeyToPath{
					{Key: "ca.cfg", Path: "ca.cfg"},
				},
			},
		},
	})

	// Code volume
	if ovs.Spec.Code.Volume.ExistingClaim != "" {
		volumes = append(volumes, corev1.Volume{
			Name: "code",
			VolumeSource: corev1.VolumeSource{
				PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
					ClaimName: ovs.Spec.Code.Volume.ExistingClaim,
				},
			},
		})
	}

	return volumes
}

// compilerLabels returns labels for compiler-specific resources.
func compilerLabels(ovs *openvoxv1alpha1.OpenVoxServer) map[string]string {
	labels := commonLabels(ovs)
	labels["app.kubernetes.io/component"] = "compiler"
	return labels
}
