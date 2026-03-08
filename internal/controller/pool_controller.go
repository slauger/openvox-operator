package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
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

// PoolReconciler reconciles a Pool object.
type PoolReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=pools,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=pools/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=pools/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=discovery.k8s.io,resources=endpointslices,verbs=get;list;watch

func (r *PoolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	pool := &openvoxv1alpha1.Pool{}
	if err := r.Get(ctx, req.NamespacedName, pool); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Validate environmentRef
	env := &openvoxv1alpha1.Environment{}
	if err := r.Get(ctx, types.NamespacedName{Name: pool.Spec.EnvironmentRef, Namespace: pool.Namespace}, env); err != nil {
		if errors.IsNotFound(err) {
			logger.Error(err, "referenced Environment not found", "environmentRef", pool.Spec.EnvironmentRef)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Reconcile Service
	if err := r.reconcileService(ctx, pool); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling Service: %w", err)
	}

	// Update status
	pool.Status.ServiceName = pool.Name
	pool.Status.Endpoints = r.countEndpoints(ctx, pool)
	if err := r.Status().Update(ctx, pool); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *PoolReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&openvoxv1alpha1.Pool{}).
		Owns(&corev1.Service{}).
		Complete(r)
}

// poolServiceSelector builds the label selector for a Pool's Service.
// It merges the user-defined Selector with the auto-injected environment label.
// If no Selector is defined, only the environment label is used (selects all Pods in the Environment).
func poolServiceSelector(pool *openvoxv1alpha1.Pool) map[string]string {
	sel := map[string]string{
		LabelEnvironment: pool.Spec.EnvironmentRef,
	}
	for k, v := range pool.Spec.Selector {
		sel[k] = v
	}
	return sel
}

func (r *PoolReconciler) reconcileService(ctx context.Context, pool *openvoxv1alpha1.Pool) error {
	logger := log.FromContext(ctx)
	svcName := pool.Name

	svc := &corev1.Service{}
	err := r.Get(ctx, types.NamespacedName{Name: svcName, Namespace: pool.Namespace}, svc)
	if errors.IsNotFound(err) {
		logger.Info("creating Pool Service", "name", svcName)

		port := int32(8140)
		if pool.Spec.Service.Port > 0 {
			port = pool.Spec.Service.Port
		}

		svcType := corev1.ServiceTypeClusterIP
		if pool.Spec.Service.Type != "" {
			svcType = pool.Spec.Service.Type
		}

		labels := environmentLabels(pool.Spec.EnvironmentRef)
		labels[poolLabel(pool.Name)] = "true"

		// Merge additional labels
		for k, v := range pool.Spec.Service.Labels {
			labels[k] = v
		}

		svcPort := corev1.ServicePort{
			Name:       "https",
			Port:       port,
			TargetPort: intstr.FromInt32(8140),
			Protocol:   corev1.ProtocolTCP,
		}
		if pool.Spec.Service.NodePort > 0 {
			svcPort.NodePort = pool.Spec.Service.NodePort
		}

		svc = &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:        svcName,
				Namespace:   pool.Namespace,
				Labels:      labels,
				Annotations: pool.Spec.Service.Annotations,
			},
			Spec: corev1.ServiceSpec{
				Type:        svcType,
				Selector:    poolServiceSelector(pool),
				Ports:       []corev1.ServicePort{svcPort},
				ExternalIPs: pool.Spec.Service.ExternalIPs,
			},
		}

		if err := controllerutil.SetControllerReference(pool, svc, r.Scheme); err != nil {
			return err
		}
		return r.Create(ctx, svc)
	} else if err != nil {
		return err
	}

	// Update existing service
	port := int32(8140)
	if pool.Spec.Service.Port > 0 {
		port = pool.Spec.Service.Port
	}
	svcType := corev1.ServiceTypeClusterIP
	if pool.Spec.Service.Type != "" {
		svcType = pool.Spec.Service.Type
	}

	// Update labels
	labels := environmentLabels(pool.Spec.EnvironmentRef)
	labels[poolLabel(pool.Name)] = "true"
	for k, v := range pool.Spec.Service.Labels {
		labels[k] = v
	}
	svc.Labels = labels
	svc.Annotations = pool.Spec.Service.Annotations

	svc.Spec.Type = svcType
	svc.Spec.Selector = poolServiceSelector(pool)
	if len(svc.Spec.Ports) == 0 {
		svc.Spec.Ports = []corev1.ServicePort{{}}
	}
	svc.Spec.Ports[0].Name = "https"
	svc.Spec.Ports[0].Port = port
	svc.Spec.Ports[0].TargetPort = intstr.FromInt32(8140)
	svc.Spec.Ports[0].Protocol = corev1.ProtocolTCP
	if pool.Spec.Service.NodePort > 0 {
		svc.Spec.Ports[0].NodePort = pool.Spec.Service.NodePort
	}
	svc.Spec.ExternalIPs = pool.Spec.Service.ExternalIPs
	return r.Update(ctx, svc)
}

func (r *PoolReconciler) countEndpoints(ctx context.Context, pool *openvoxv1alpha1.Pool) int32 {
	sliceList := &discoveryv1.EndpointSliceList{}
	if err := r.List(ctx, sliceList, client.InNamespace(pool.Namespace),
		client.MatchingLabels{"kubernetes.io/service-name": pool.Name}); err != nil {
		return 0
	}
	var count int32
	for _, slice := range sliceList.Items {
		for _, ep := range slice.Endpoints {
			if ep.Conditions.Ready != nil && *ep.Conditions.Ready {
				count++
			}
		}
	}
	return count
}
