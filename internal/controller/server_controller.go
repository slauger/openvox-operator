package controller

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// ServerReconciler reconciles a Server object.
type ServerReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder events.EventRecorder
}

// Event reasons for Server.
const (
	EventReasonServerRunning    = "ServerRunning"
	EventReasonServerError      = "ServerError"
	EventReasonPDBCreated       = "PDBCreated"
	EventReasonPDBUpdated       = "PDBUpdated"
	EventReasonPDBDeleted       = "PDBDeleted"
	EventReasonHPACreated       = "HPACreated"
	EventReasonHPAUpdated       = "HPAUpdated"
	EventReasonHPADeleted       = "HPADeleted"
	EventReasonDeploymentSynced = "DeploymentSynced"
)

// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=servers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=servers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=servers/finalizers,verbs=update
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=configs,verbs=get;list;watch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=certificates,verbs=get;list;watch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=certificateauthorities,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=autoscaling,resources=horizontalpodautoscalers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=policy,resources=poddisruptionbudgets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

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

	// Resolve Config
	cfg := &openvoxv1alpha1.Config{}
	if err := r.Get(ctx, types.NamespacedName{Name: server.Spec.ConfigRef, Namespace: server.Namespace}, cfg); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("waiting for Config", "configRef", server.Spec.ConfigRef)
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
		return ctrl.Result{}, err
	}

	// Resolve Certificate -- wait until phase is Signed
	cert := &openvoxv1alpha1.Certificate{}
	if err := r.Get(ctx, types.NamespacedName{Name: server.Spec.CertificateRef, Namespace: server.Namespace}, cert); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("waiting for Certificate", "certificateRef", server.Spec.CertificateRef)
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
		return ctrl.Result{}, err
	}

	if cert.Status.Phase != openvoxv1alpha1.CertificatePhaseSigned || cert.Status.SecretName == "" {
		logger.Info("waiting for Certificate to be signed", "certificate", cert.Name, "phase", cert.Status.Phase)
		server.Status.Phase = openvoxv1alpha1.ServerPhaseWaitingForCert
		if statusErr := r.Status().Update(ctx, server); statusErr != nil {
			logger.Error(statusErr, "failed to update Server status", "name", server.Name)
		}
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	// Resolve CertificateAuthority (needed for CA PVC name when ca: true)
	ca := &openvoxv1alpha1.CertificateAuthority{}
	if err := r.Get(ctx, types.NamespacedName{Name: cert.Spec.AuthorityRef, Namespace: server.Namespace}, ca); err != nil {
		if errors.IsNotFound(err) {
			logger.Info("waiting for CertificateAuthority", "authorityRef", cert.Spec.AuthorityRef)
			return ctrl.Result{RequeueAfter: 5 * time.Second}, nil
		}
		return ctrl.Result{}, err
	}

	// Reconcile Deployment
	if err := r.reconcileDeployment(ctx, server, cfg, cert, ca); err != nil {
		r.Recorder.Eventf(server, nil, corev1.EventTypeWarning, EventReasonServerError, "Reconcile", "Failed to reconcile Deployment: %v", err)
		return ctrl.Result{}, fmt.Errorf("reconciling Deployment: %w", err)
	}

	// Reconcile PodDisruptionBudget
	if err := r.reconcilePDB(ctx, server); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling PDB: %w", err)
	}

	// Reconcile HorizontalPodAutoscaler
	if err := r.reconcileHPA(ctx, server); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling HPA: %w", err)
	}

	// Update status
	replicas := int32(1)
	if server.Spec.Replicas != nil {
		replicas = *server.Spec.Replicas
	}
	ready := r.getReadyReplicas(ctx, server)
	server.Status.Desired = replicas
	server.Status.Ready = ready

	if ready > 0 {
		server.Status.Phase = openvoxv1alpha1.ServerPhaseRunning
	} else {
		server.Status.Phase = openvoxv1alpha1.ServerPhasePending
	}

	if err := r.Status().Update(ctx, server); err != nil {
		return ctrl.Result{}, err
	}

	if ready > 0 {
		r.Recorder.Eventf(server, nil, corev1.EventTypeNormal, EventReasonServerRunning, "Reconcile", "Server reconciled successfully")
	}
	return ctrl.Result{}, nil
}

func (r *ServerReconciler) reconcilePDB(ctx context.Context, server *openvoxv1alpha1.Server) error {
	logger := log.FromContext(ctx)
	pdbName := server.Name
	existing := &policyv1.PodDisruptionBudget{}
	err := r.Get(ctx, types.NamespacedName{Name: pdbName, Namespace: server.Namespace}, existing)

	// If PDB is disabled or not configured, delete if exists
	if server.Spec.PDB == nil || !server.Spec.PDB.Enabled {
		if err == nil {
			logger.Info("deleting PDB (disabled)", "name", pdbName)
			if err := r.Delete(ctx, existing); err != nil && !errors.IsNotFound(err) {
				return err
			}
			r.Recorder.Eventf(server, nil, corev1.EventTypeNormal, EventReasonPDBDeleted, "Reconcile", "PodDisruptionBudget %s deleted", pdbName)
		}
		return nil
	}

	desired, buildErr := r.buildPDB(server)
	if buildErr != nil {
		return buildErr
	}
	if errors.IsNotFound(err) {
		logger.Info("creating PDB", "name", pdbName)
		if err := r.Create(ctx, desired); err != nil {
			return err
		}
		r.Recorder.Eventf(server, nil, corev1.EventTypeNormal, EventReasonPDBCreated, "Reconcile", "PodDisruptionBudget %s created", pdbName)
		return nil
	}
	if err != nil {
		return err
	}

	// Update existing
	existing.Spec = desired.Spec
	if err := r.Update(ctx, existing); err != nil {
		return err
	}
	r.Recorder.Eventf(server, nil, corev1.EventTypeNormal, EventReasonPDBUpdated, "Reconcile", "PodDisruptionBudget %s updated", pdbName)
	return nil
}

func (r *ServerReconciler) buildPDB(server *openvoxv1alpha1.Server) (*policyv1.PodDisruptionBudget, error) {
	pdb := &policyv1.PodDisruptionBudget{
		ObjectMeta: metav1.ObjectMeta{
			Name:      server.Name,
			Namespace: server.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "openvox-operator",
				LabelServer:                    server.Name,
			},
		},
		Spec: policyv1.PodDisruptionBudgetSpec{
			Selector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					LabelServer: server.Name,
				},
			},
		},
	}
	if server.Spec.PDB.MinAvailable != nil {
		pdb.Spec.MinAvailable = server.Spec.PDB.MinAvailable
	} else if server.Spec.PDB.MaxUnavailable != nil {
		pdb.Spec.MaxUnavailable = server.Spec.PDB.MaxUnavailable
	} else {
		// Default: minAvailable: 1
		minAvailable := intstrInt(1)
		pdb.Spec.MinAvailable = &minAvailable
	}
	if err := controllerutil.SetControllerReference(server, pdb, r.Scheme); err != nil {
		return nil, fmt.Errorf("setting controller reference on PDB: %w", err)
	}
	return pdb, nil
}

func (r *ServerReconciler) reconcileHPA(ctx context.Context, server *openvoxv1alpha1.Server) error {
	logger := log.FromContext(ctx)
	hpaName := server.Name
	existing := &autoscalingv2.HorizontalPodAutoscaler{}
	err := r.Get(ctx, types.NamespacedName{Name: hpaName, Namespace: server.Namespace}, existing)

	// If HPA is disabled, delete if exists
	if !server.Spec.Autoscaling.Enabled {
		if err == nil {
			logger.Info("deleting HPA (disabled)", "name", hpaName)
			if err := r.Delete(ctx, existing); err != nil && !errors.IsNotFound(err) {
				return err
			}
			r.Recorder.Eventf(server, nil, corev1.EventTypeNormal, EventReasonHPADeleted, "Reconcile", "HorizontalPodAutoscaler %s deleted", hpaName)
		}
		return nil
	}

	desired, buildErr := r.buildHPA(server)
	if buildErr != nil {
		return buildErr
	}
	if errors.IsNotFound(err) {
		logger.Info("creating HPA", "name", hpaName)
		if err := r.Create(ctx, desired); err != nil {
			return err
		}
		r.Recorder.Eventf(server, nil, corev1.EventTypeNormal, EventReasonHPACreated, "Reconcile", "HorizontalPodAutoscaler %s created", hpaName)
		return nil
	}
	if err != nil {
		return err
	}

	// Update existing
	existing.Spec = desired.Spec
	if err := r.Update(ctx, existing); err != nil {
		return err
	}
	r.Recorder.Eventf(server, nil, corev1.EventTypeNormal, EventReasonHPAUpdated, "Reconcile", "HorizontalPodAutoscaler %s updated", hpaName)
	return nil
}

func (r *ServerReconciler) buildHPA(server *openvoxv1alpha1.Server) (*autoscalingv2.HorizontalPodAutoscaler, error) {
	as := server.Spec.Autoscaling
	targetCPU := as.TargetCPU
	if targetCPU == 0 {
		targetCPU = 75
	}
	maxReplicas := as.MaxReplicas
	if maxReplicas == 0 {
		maxReplicas = 5
	}

	hpa := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{
			Name:      server.Name,
			Namespace: server.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "openvox-operator",
				LabelServer:                    server.Name,
			},
		},
		Spec: autoscalingv2.HorizontalPodAutoscalerSpec{
			ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{
				APIVersion: "apps/v1",
				Kind:       "Deployment",
				Name:       server.Name,
			},
			MinReplicas: as.MinReplicas,
			MaxReplicas: maxReplicas,
			Metrics: []autoscalingv2.MetricSpec{
				{
					Type: autoscalingv2.ResourceMetricSourceType,
					Resource: &autoscalingv2.ResourceMetricSource{
						Name: corev1.ResourceCPU,
						Target: autoscalingv2.MetricTarget{
							Type:               autoscalingv2.UtilizationMetricType,
							AverageUtilization: &targetCPU,
						},
					},
				},
			},
		},
	}

	if err := controllerutil.SetControllerReference(server, hpa, r.Scheme); err != nil {
		return nil, fmt.Errorf("setting controller reference on HPA: %w", err)
	}
	return hpa, nil
}

func (r *ServerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&openvoxv1alpha1.Server{}).
		Owns(&appsv1.Deployment{}).
		Owns(&policyv1.PodDisruptionBudget{}).
		Owns(&autoscalingv2.HorizontalPodAutoscaler{}).
		Watches(&corev1.Secret{}, enqueueServersForSecret(mgr.GetClient())).
		Complete(r)
}

func intstrInt(val int) intstr.IntOrString {
	return intstr.FromInt32(int32(val))
}

func (r *ServerReconciler) getReadyReplicas(ctx context.Context, server *openvoxv1alpha1.Server) int32 {
	deploy := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: server.Name, Namespace: server.Namespace}, deploy); err != nil {
		return 0
	}
	return deploy.Status.ReadyReplicas
}
