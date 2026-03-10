package controller

import (
	"context"
	"fmt"
	"slices"

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
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// PoolReconciler reconciles a Pool object.
type PoolReconciler struct {
	client.Client
	Scheme              *runtime.Scheme
	GatewayAPIAvailable bool
}

// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=pools,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=pools/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=pools/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=discovery.k8s.io,resources=endpointslices,verbs=get;list;watch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=tlsroutes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=certificates,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=servers,verbs=get;list;watch

func (r *PoolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	pool := &openvoxv1alpha1.Pool{}
	if err := r.Get(ctx, req.NamespacedName, pool); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Reconcile Service
	if err := r.reconcileService(ctx, pool); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling Service: %w", err)
	}

	// Reconcile TLSRoute
	if pool.Spec.Route != nil && pool.Spec.Route.Enabled {
		if !r.GatewayAPIAvailable {
			logger.Info("TLSRoute requested but Gateway API CRDs not available, skipping")
		} else {
			// Check for hostname conflicts
			allPools := &openvoxv1alpha1.PoolList{}
			if err := r.List(ctx, allPools, client.InNamespace(pool.Namespace)); err != nil {
				return ctrl.Result{}, fmt.Errorf("listing Pools for hostname conflict check: %w", err)
			}
			for i := range allPools.Items {
				other := &allPools.Items[i]
				if other.Name == pool.Name {
					continue
				}
				if other.Spec.Route != nil && other.Spec.Route.Enabled && other.Spec.Route.Hostname == pool.Spec.Route.Hostname {
					logger.Info("hostname conflict: another Pool uses the same hostname",
						"conflictingPool", other.Name, "hostname", pool.Spec.Route.Hostname)
				}
			}

			if err := r.reconcileTLSRoute(ctx, pool); err != nil {
				return ctrl.Result{}, fmt.Errorf("reconciling TLSRoute: %w", err)
			}

			if pool.Spec.Route.InjectDNSAltName {
				if err := r.injectDNSAltNames(ctx, pool); err != nil {
					return ctrl.Result{}, fmt.Errorf("injecting DNS alt names: %w", err)
				}
			}
		}
	} else if r.GatewayAPIAvailable {
		// Cleanup: delete owned TLSRoute if route is disabled
		existing := &gwapiv1.TLSRoute{}
		if err := r.Get(ctx, types.NamespacedName{Name: pool.Name, Namespace: pool.Namespace}, existing); err == nil {
			if metav1.IsControlledBy(existing, pool) {
				logger.Info("deleting orphaned TLSRoute", "name", pool.Name)
				if err := r.Delete(ctx, existing); err != nil && !errors.IsNotFound(err) {
					return ctrl.Result{}, fmt.Errorf("deleting orphaned TLSRoute: %w", err)
				}
			}
		}
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
	builder := ctrl.NewControllerManagedBy(mgr).
		For(&openvoxv1alpha1.Pool{}).
		Owns(&corev1.Service{}).
		Watches(&openvoxv1alpha1.Server{}, enqueuePoolsForServer(mgr.GetClient()))

	if r.GatewayAPIAvailable {
		builder = builder.Owns(&gwapiv1.TLSRoute{})
	}

	return builder.Complete(r)
}

// enqueuePoolsForServer returns a handler that enqueues all Pools in the
// namespace when a Server changes. This ensures Pool endpoints stay in sync
// when Servers add or remove poolRefs.
func enqueuePoolsForServer(c client.Client) handler.EventHandler {
	return handler.EnqueueRequestsFromMapFunc(func(ctx context.Context, obj client.Object) []ctrl.Request {
		pools := &openvoxv1alpha1.PoolList{}
		if err := c.List(ctx, pools, client.InNamespace(obj.GetNamespace())); err != nil {
			return nil
		}
		var requests []ctrl.Request
		for _, pool := range pools.Items {
			requests = append(requests, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      pool.Name,
					Namespace: pool.Namespace,
				},
			})
		}
		return requests
	})
}

// poolServiceSelector builds the label selector for a Pool's Service.
// It selects pods that declare this pool in their poolRefs.
func poolServiceSelector(pool *openvoxv1alpha1.Pool) map[string]string {
	return map[string]string{
		poolLabel(pool.Name): "true",
	}
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

		labels := map[string]string{
			"app.kubernetes.io/name":       "openvox",
			"app.kubernetes.io/managed-by": "openvox-operator",
			poolLabel(pool.Name):           "true",
		}

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
	labels := map[string]string{
		"app.kubernetes.io/name":       "openvox",
		"app.kubernetes.io/managed-by": "openvox-operator",
		poolLabel(pool.Name):           "true",
	}
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

func (r *PoolReconciler) reconcileTLSRoute(ctx context.Context, pool *openvoxv1alpha1.Pool) error {
	logger := log.FromContext(ctx)

	port := gwapiv1.PortNumber(8140)
	if pool.Spec.Service.Port > 0 {
		port = gwapiv1.PortNumber(pool.Spec.Service.Port)
	}

	hostname := gwapiv1.Hostname(pool.Spec.Route.Hostname)

	parentRef := gwapiv1.ParentReference{
		Name: gwapiv1.ObjectName(pool.Spec.Route.GatewayRef.Name),
	}
	if pool.Spec.Route.GatewayRef.SectionName != "" {
		sectionName := gwapiv1.SectionName(pool.Spec.Route.GatewayRef.SectionName)
		parentRef.SectionName = &sectionName
	}

	desired := &gwapiv1.TLSRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      pool.Name,
			Namespace: pool.Namespace,
		},
		Spec: gwapiv1.TLSRouteSpec{
			CommonRouteSpec: gwapiv1.CommonRouteSpec{
				ParentRefs: []gwapiv1.ParentReference{parentRef},
			},
			Hostnames: []gwapiv1.Hostname{hostname},
			Rules: []gwapiv1.TLSRouteRule{
				{
					BackendRefs: []gwapiv1.BackendRef{
						{
							BackendObjectReference: gwapiv1.BackendObjectReference{
								Name: gwapiv1.ObjectName(pool.Name),
								Port: &port,
							},
						},
					},
				},
			},
		},
	}

	existing := &gwapiv1.TLSRoute{}
	err := r.Get(ctx, types.NamespacedName{Name: pool.Name, Namespace: pool.Namespace}, existing)
	if errors.IsNotFound(err) {
		logger.Info("creating TLSRoute", "name", pool.Name, "hostname", pool.Spec.Route.Hostname)
		if err := controllerutil.SetControllerReference(pool, desired, r.Scheme); err != nil {
			return err
		}
		return r.Create(ctx, desired)
	} else if err != nil {
		return err
	}

	// Update existing TLSRoute
	existing.Spec = desired.Spec
	return r.Update(ctx, existing)
}

func (r *PoolReconciler) injectDNSAltNames(ctx context.Context, pool *openvoxv1alpha1.Pool) error {
	logger := log.FromContext(ctx)

	servers := &openvoxv1alpha1.ServerList{}
	if err := r.List(ctx, servers, client.InNamespace(pool.Namespace)); err != nil {
		return fmt.Errorf("listing Servers: %w", err)
	}

	hostname := pool.Spec.Route.Hostname

	for i := range servers.Items {
		server := &servers.Items[i]

		if !slices.Contains(server.Spec.PoolRefs, pool.Name) {
			continue
		}

		if server.Spec.CertificateRef == "" {
			continue
		}

		cert := &openvoxv1alpha1.Certificate{}
		if err := r.Get(ctx, types.NamespacedName{
			Name:      server.Spec.CertificateRef,
			Namespace: pool.Namespace,
		}, cert); err != nil {
			if errors.IsNotFound(err) {
				continue
			}
			return fmt.Errorf("getting Certificate %s: %w", server.Spec.CertificateRef, err)
		}

		if slices.Contains(cert.Spec.DNSAltNames, hostname) {
			continue
		}

		logger.Info("injecting DNS alt name into Certificate",
			"certificate", cert.Name, "hostname", hostname)
		cert.Spec.DNSAltNames = append(cert.Spec.DNSAltNames, hostname)
		if err := r.Update(ctx, cert); err != nil {
			return fmt.Errorf("updating Certificate %s: %w", cert.Name, err)
		}
	}

	return nil
}
