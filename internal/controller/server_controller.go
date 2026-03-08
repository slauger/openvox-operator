package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// ServerReconciler reconciles a Server object.
type ServerReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=servers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=servers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=servers/finalizers,verbs=update
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=environments,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=autoscaling,resources=horizontalpodautoscalers,verbs=get;list;watch;create;update;patch;delete

func (r *ServerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	server := &openvoxv1alpha1.Server{}
	if err := r.Get(ctx, req.NamespacedName, server); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Set initial phase
	if server.Status.Phase == "" {
		server.Status.Phase = openvoxv1alpha1.ServerPhasePending
		if err := r.Status().Update(ctx, server); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Resolve Environment
	env := &openvoxv1alpha1.Environment{}
	if err := r.Get(ctx, types.NamespacedName{Name: server.Spec.EnvironmentRef, Namespace: server.Namespace}, env); err != nil {
		if errors.IsNotFound(err) {
			logger.Error(err, "referenced Environment not found", "environmentRef", server.Spec.EnvironmentRef)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Wait for CA to be ready before proceeding
	if !env.Status.CAReady {
		logger.Info("waiting for CA to be ready")
		server.Status.Phase = openvoxv1alpha1.ServerPhaseWaitingForCA
		if err := r.Status().Update(ctx, server); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{RequeueAfter: 10_000_000_000}, nil // 10s
	}

	// Reconcile Deployment
	if err := r.reconcileDeployment(ctx, server, env); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling Deployment: %w", err)
	}

	// Update status
	replicas := int32(1)
	if server.Spec.Replicas != nil {
		replicas = *server.Spec.Replicas
	}
	server.Status.Desired = replicas
	server.Status.Ready = r.getReadyReplicas(ctx, server)
	server.Status.Phase = openvoxv1alpha1.ServerPhaseRunning

	if err := r.Status().Update(ctx, server); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *ServerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&openvoxv1alpha1.Server{}).
		Owns(&appsv1.Deployment{}).
		Complete(r)
}

func (r *ServerReconciler) reconcileDeployment(ctx context.Context, server *openvoxv1alpha1.Server, env *openvoxv1alpha1.Environment) error {
	logger := log.FromContext(ctx)
	deployName := server.Name

	// Resolve image: Server override > Environment default
	image := fmt.Sprintf("%s:%s", env.Spec.Image.Repository, env.Spec.Image.Tag)
	if server.Spec.Image.Tag != "" {
		repo := env.Spec.Image.Repository
		if server.Spec.Image.Repository != "" {
			repo = server.Spec.Image.Repository
		}
		image = fmt.Sprintf("%s:%s", repo, server.Spec.Image.Tag)
	}

	replicas := int32(1)
	if server.Spec.Replicas != nil {
		replicas = *server.Spec.Replicas
	}

	javaArgs := resolveJavaArgs(server)

	// Determine role: primary role is "server" unless it's a CA-only node
	role := RoleServer
	if server.Spec.CA && !server.Spec.Server {
		role = RoleCA
	}

	// Build labels
	labels := serverLabels(server.Spec.EnvironmentRef, server.Name, role)
	if server.Spec.CA {
		labels[LabelCA] = "true"
	}
	for _, pool := range server.Spec.PoolRefs {
		labels[poolLabel(pool)] = "true"
	}

	// Deployment strategy
	strategy := appsv1.DeploymentStrategy{Type: appsv1.RollingUpdateDeploymentStrategyType}
	if server.Spec.CA {
		strategy = appsv1.DeploymentStrategy{Type: appsv1.RecreateDeploymentStrategyType}
	}

	configMapName := fmt.Sprintf("%s-config", server.Spec.EnvironmentRef)

	deploy := &appsv1.Deployment{}
	err := r.Get(ctx, types.NamespacedName{Name: deployName, Namespace: server.Namespace}, deploy)
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
						Labels: labels,
					},
					Spec: r.buildPodSpec(server, env, image, javaArgs, configMapName, role),
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
	deploy.Spec.Template.Spec = r.buildPodSpec(server, env, image, javaArgs, configMapName, role)
	return r.Update(ctx, deploy)
}

func (r *ServerReconciler) buildPodSpec(server *openvoxv1alpha1.Server, env *openvoxv1alpha1.Environment, image, javaArgs, configMapName, role string) corev1.PodSpec {
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

	volumes := []corev1.Volume{
		{Name: "ssl", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
		configMapVolume("puppet-conf", configMapName, "puppet.conf"),
		configMapVolume("puppetdb-conf", configMapName, "puppetdb.conf"),
		configMapVolume("puppetserver-conf", configMapName, "puppetserver.conf"),
		configMapVolume("webserver-conf", configMapName, "webserver.conf"),
		configMapVolume("auth-conf", configMapName, "auth.conf"),
		configMapVolume("product-conf", configMapName, "product.conf"),
	}

	// CA-specific: mount CA data PVC, use ca-enabled.cfg
	if server.Spec.CA {
		volumeMounts = append(volumeMounts, corev1.VolumeMount{
			Name:      "ca-data",
			MountPath: "/etc/puppetlabs/puppetserver/ca",
		})
		volumes = append(volumes,
			corev1.Volume{
				Name: "ca-data",
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: fmt.Sprintf("%s-ca-data", server.Spec.EnvironmentRef),
					},
				},
			},
			configMapVolumeWithKey("ca-cfg", configMapName, "ca-enabled.cfg", "ca.cfg"),
		)
	} else {
		// Non-CA server: use ca-disabled.cfg, mount CA Secret for ca_crt.pem + CRL
		caSecretName := fmt.Sprintf("%s-ca", server.Spec.EnvironmentRef)
		volumeMounts = append(volumeMounts,
			corev1.VolumeMount{
				Name:      "ca-certs",
				MountPath: "/etc/puppetlabs/puppet/ssl/certs/ca.pem",
				SubPath:   "ca_crt.pem",
				ReadOnly:  true,
			},
			corev1.VolumeMount{
				Name:      "ca-certs",
				MountPath: "/etc/puppetlabs/puppet/ssl/crl.pem",
				SubPath:   "ca_crl.pem",
				ReadOnly:  true,
			},
		)
		volumes = append(volumes,
			configMapVolumeWithKey("ca-cfg", configMapName, "ca-disabled.cfg", "ca.cfg"),
			corev1.Volume{
				Name: "ca-certs",
				VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{
						SecretName: caSecretName,
					},
				},
			},
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

	podSpec := corev1.PodSpec{
		ServiceAccountName: fmt.Sprintf("%s-server", server.Spec.EnvironmentRef),
		SecurityContext: &corev1.PodSecurityContext{
			RunAsUser:    int64Ptr(1001),
			RunAsGroup:   int64Ptr(0),
			RunAsNonRoot: boolPtr(true),
		},
		Containers: []corev1.Container{container},
		Volumes:    volumes,
	}

	// Non-CA servers need an InitContainer for SSL bootstrap against CA
	if !server.Spec.CA {
		caServiceName := fmt.Sprintf("%s-ca", server.Spec.EnvironmentRef)
		podSpec.InitContainers = []corev1.Container{
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

echo "Waiting for CA server at %s..."
until curl --fail --silent --insecure "https://%s:8140/status/v1/simple" | grep -q running; do
    sleep 2
done

echo "Bootstrapping SSL..."
puppet ssl bootstrap --server="%s" --serverport=8140
echo "SSL bootstrap complete."
`, caServiceName, caServiceName, caServiceName)},
				VolumeMounts: []corev1.VolumeMount{
					{Name: "ssl", MountPath: "/etc/puppetlabs/puppet/ssl"},
					{Name: "puppet-conf", MountPath: "/etc/puppetlabs/puppet/puppet.conf", SubPath: "puppet.conf", ReadOnly: true},
				},
			},
		}
	}

	return podSpec
}

func (r *ServerReconciler) getReadyReplicas(ctx context.Context, server *openvoxv1alpha1.Server) int32 {
	deploy := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: server.Name, Namespace: server.Namespace}, deploy); err != nil {
		return 0
	}
	return deploy.Status.ReadyReplicas
}

// resolveJavaArgs determines JVM arguments for a Server.
// Priority: explicit javaArgs > auto-calculated from memory limits > default.
func resolveJavaArgs(server *openvoxv1alpha1.Server) string {
	if server.Spec.JavaArgs != "" {
		return server.Spec.JavaArgs
	}

	// Auto-calculate from memory limits: 90% for both Xms and Xmx
	if memLimit, ok := server.Spec.Resources.Limits[corev1.ResourceMemory]; ok {
		heapMB := memLimit.Value() * 9 / 10 / (1024 * 1024)
		return fmt.Sprintf("-Xms%dm -Xmx%dm", heapMB, heapMB)
	}

	return "-Xms512m -Xmx1024m"
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
