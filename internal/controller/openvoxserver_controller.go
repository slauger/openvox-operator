package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

const (
	finalizerName = "openvox.voxpupuli.org/finalizer"
)

// OpenVoxServerReconciler reconciles an OpenVoxServer object.
type OpenVoxServerReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=openvoxservers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=openvoxservers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=openvoxservers/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=configmaps;secrets;services;persistentvolumeclaims,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=statefulsets;deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=autoscaling,resources=horizontalpodautoscalers,verbs=get;list;watch;create;update;patch;delete

// Reconcile is the main reconciliation loop for OpenVoxServer resources.
func (r *OpenVoxServerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	// Fetch the OpenVoxServer instance
	ovs := &openvoxv1alpha1.OpenVoxServer{}
	if err := r.Get(ctx, req.NamespacedName, ovs); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Set initial phase
	if ovs.Status.Phase == "" {
		ovs.Status.Phase = openvoxv1alpha1.PhasePending
		if err := r.Status().Update(ctx, ovs); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Step 1: Reconcile ConfigMaps
	logger.Info("reconciling ConfigMaps")
	if err := r.reconcileConfigMaps(ctx, ovs); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling ConfigMaps: %w", err)
	}

	// Step 2: Reconcile CA (if enabled)
	if ovs.Spec.CA.Enabled {
		// Step 2a: Ensure CA PVC exists
		if err := r.reconcileCAPVC(ctx, ovs); err != nil {
			return ctrl.Result{}, fmt.Errorf("reconciling CA PVC: %w", err)
		}

		// Step 2b: Check if CA Secret exists, if not run CA Setup Job
		caSecretName := fmt.Sprintf("%s-ca", ovs.Name)
		caSecret := &corev1.Secret{}
		caSecretExists := true
		if err := r.Get(ctx, types.NamespacedName{Name: caSecretName, Namespace: ovs.Namespace}, caSecret); err != nil {
			if errors.IsNotFound(err) {
				caSecretExists = false
			} else {
				return ctrl.Result{}, err
			}
		}

		if !caSecretExists {
			logger.Info("CA Secret not found, running CA setup")
			ovs.Status.Phase = openvoxv1alpha1.PhaseCASetup
			if err := r.Status().Update(ctx, ovs); err != nil {
				return ctrl.Result{}, err
			}

			result, err := r.reconcileCASetupJob(ctx, ovs)
			if err != nil {
				return ctrl.Result{}, fmt.Errorf("reconciling CA setup job: %w", err)
			}
			if result.Requeue || result.RequeueAfter > 0 {
				return result, nil
			}
		}

		// Step 2c: Reconcile CA StatefulSet
		logger.Info("reconciling CA StatefulSet")
		if err := r.reconcileCAStatefulSet(ctx, ovs); err != nil {
			return ctrl.Result{}, fmt.Errorf("reconciling CA StatefulSet: %w", err)
		}

		// Step 2d: Reconcile CA Service
		if err := r.reconcileCAService(ctx, ovs); err != nil {
			return ctrl.Result{}, fmt.Errorf("reconciling CA Service: %w", err)
		}

		// Update CA status
		ovs.Status.CASecretName = caSecretName
		ovs.Status.CAReady = r.isCAReady(ctx, ovs)
		meta.SetStatusCondition(&ovs.Status.Conditions, metav1.Condition{
			Type:               openvoxv1alpha1.ConditionCAInitialized,
			Status:             metav1.ConditionTrue,
			Reason:             "CASecretExists",
			Message:            "CA certificates are initialized",
			LastTransitionTime: metav1.Now(),
		})
	}

	// Step 3: Reconcile Compilers (if replicas > 0)
	replicas := int32(1)
	if ovs.Spec.Compilers.Replicas != nil {
		replicas = *ovs.Spec.Compilers.Replicas
	}

	if replicas > 0 {
		logger.Info("reconciling Compiler Deployment")
		if err := r.reconcileCompilerDeployment(ctx, ovs); err != nil {
			return ctrl.Result{}, fmt.Errorf("reconciling Compiler Deployment: %w", err)
		}

		if err := r.reconcileCompilerService(ctx, ovs); err != nil {
			return ctrl.Result{}, fmt.Errorf("reconciling Compiler Service: %w", err)
		}

		// Update compiler status
		ovs.Status.CompilersDesired = replicas
		ovs.Status.CompilersReady = r.getReadyCompilers(ctx, ovs)
	}

	// Update overall phase
	if ovs.Spec.CA.Enabled && !ovs.Status.CAReady {
		ovs.Status.Phase = openvoxv1alpha1.PhaseWaitingForCA
	} else {
		ovs.Status.Phase = openvoxv1alpha1.PhaseRunning
	}

	if err := r.Status().Update(ctx, ovs); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *OpenVoxServerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&openvoxv1alpha1.OpenVoxServer{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&corev1.Secret{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.PersistentVolumeClaim{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&appsv1.Deployment{}).
		Owns(&batchv1.Job{}).
		Complete(r)
}

// isCAReady checks if the CA StatefulSet has a ready pod.
func (r *OpenVoxServerReconciler) isCAReady(ctx context.Context, ovs *openvoxv1alpha1.OpenVoxServer) bool {
	sts := &appsv1.StatefulSet{}
	name := fmt.Sprintf("%s-ca", ovs.Name)
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: ovs.Namespace}, sts); err != nil {
		return false
	}
	return sts.Status.ReadyReplicas > 0
}

// getReadyCompilers returns the number of ready compiler pods.
func (r *OpenVoxServerReconciler) getReadyCompilers(ctx context.Context, ovs *openvoxv1alpha1.OpenVoxServer) int32 {
	deploy := &appsv1.Deployment{}
	name := fmt.Sprintf("%s-compiler", ovs.Name)
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: ovs.Namespace}, deploy); err != nil {
		return 0
	}
	return deploy.Status.ReadyReplicas
}

// setOwnerReference sets the owner reference on a child object.
func (r *OpenVoxServerReconciler) setOwnerReference(ovs *openvoxv1alpha1.OpenVoxServer, obj metav1.Object) error {
	return controllerutil.SetControllerReference(ovs, obj, r.Scheme)
}
