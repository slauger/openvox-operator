package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
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
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=pools,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=autoscaling,resources=horizontalpodautoscalers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets;serviceaccounts,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=batch,resources=jobs,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=rbac.authorization.k8s.io,resources=roles;rolebindings,verbs=get;list;watch;create;update;patch;delete

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

	// Reconcile cert setup RBAC (SA, Role, RoleBinding)
	if err := r.reconcileCertSetupRBAC(ctx, server, env); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling cert setup RBAC: %w", err)
	}

	// Reconcile SSL certificates (runs CA setup job or cert bootstrap job)
	result, err := r.reconcileCertSetupJob(ctx, server, env)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling cert setup: %w", err)
	}
	if result.Requeue || result.RequeueAfter > 0 {
		return result, nil
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
		Owns(&batchv1.Job{}).
		Owns(&corev1.ServiceAccount{}).
		Owns(&rbacv1.Role{}).
		Owns(&rbacv1.RoleBinding{}).
		Watches(&corev1.Secret{}, enqueueServersForSecret(mgr.GetClient())).
		Complete(r)
}

func (r *ServerReconciler) getReadyReplicas(ctx context.Context, server *openvoxv1alpha1.Server) int32 {
	deploy := &appsv1.Deployment{}
	if err := r.Get(ctx, types.NamespacedName{Name: server.Name, Namespace: server.Namespace}, deploy); err != nil {
		return 0
	}
	return deploy.Status.ReadyReplicas
}
