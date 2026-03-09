package controller

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
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
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=configs,verbs=get;list;watch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=certificates,verbs=get;list;watch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=certificateauthorities,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=autoscaling,resources=horizontalpodautoscalers,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

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
			logger.Error(err, "referenced Config not found", "configRef", server.Spec.ConfigRef)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Resolve Certificate — wait until phase is Signed
	cert := &openvoxv1alpha1.Certificate{}
	if err := r.Get(ctx, types.NamespacedName{Name: server.Spec.CertificateRef, Namespace: server.Namespace}, cert); err != nil {
		if errors.IsNotFound(err) {
			logger.Error(err, "referenced Certificate not found", "certificateRef", server.Spec.CertificateRef)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	if cert.Status.Phase != openvoxv1alpha1.CertificatePhaseSigned || cert.Status.SecretName == "" {
		logger.Info("waiting for Certificate to be signed", "certificate", cert.Name, "phase", cert.Status.Phase)
		server.Status.Phase = openvoxv1alpha1.ServerPhaseWaitingForCert
		_ = r.Status().Update(ctx, server)
		return ctrl.Result{RequeueAfter: 10 * time.Second}, nil
	}

	// Resolve CertificateAuthority (needed for CA PVC name when ca: true)
	ca := &openvoxv1alpha1.CertificateAuthority{}
	if err := r.Get(ctx, types.NamespacedName{Name: cert.Spec.AuthorityRef, Namespace: server.Namespace}, ca); err != nil {
		if errors.IsNotFound(err) {
			logger.Error(err, "referenced CertificateAuthority not found", "authorityRef", cert.Spec.AuthorityRef)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Reconcile Deployment
	if err := r.reconcileDeployment(ctx, server, cfg, cert, ca); err != nil {
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
