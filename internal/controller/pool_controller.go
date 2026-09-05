package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/events"
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
	Recorder            events.EventRecorder
	GatewayAPIAvailable bool
}

// Event reasons for Pool.
const (
	EventReasonPoolError          = "PoolError"
	EventReasonServiceSynced      = "ServiceSynced"
	EventReasonTLSRouteCreated    = "TLSRouteCreated"
	EventReasonTLSRouteUpdated    = "TLSRouteUpdated"
	EventReasonTLSRouteDeleted    = "TLSRouteDeleted"
	EventReasonHostnameConflict   = "HostnameConflict"
	EventReasonDNSAltNameInjected = "DNSAltNameInjected"
)

// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=pools,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=pools/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=pools/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=discovery.k8s.io,resources=endpointslices,verbs=get;list;watch
// +kubebuilder:rbac:groups=gateway.networking.k8s.io,resources=tlsroutes,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=certificates,verbs=get;list;watch;update;patch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=servers,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *PoolReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	pool := &openvoxv1alpha1.Pool{}
	if err := r.Get(ctx, req.NamespacedName, pool); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("getting Pool %s: %w", req.NamespacedName, err)
	}

	// Pausing comes after the deletion path: a paused resource must still be
	// deletable, otherwise the annotation turns into a trap.
	if paused, err := reconcilePauseState(ctx, r.Client, pool, &pool.Status.Conditions); err != nil {
		return ctrl.Result{}, err
	} else if paused {
		logger.Info("reconciliation paused by annotation", "name", pool.Name)
		return ctrl.Result{}, nil
	}

	// Reconcile Service
	if err := r.reconcileService(ctx, pool); err != nil {
		r.Recorder.Eventf(pool, nil, corev1.EventTypeWarning, EventReasonPoolError, "Reconcile", "Failed to reconcile Service: %v", err)
		return ctrl.Result{}, fmt.Errorf("reconciling Service: %w", err)
	}

	// Reconcile TLSRoute. conflictWith names the Pool that owns the requested
	// hostname when it is not this one, so the single status update below can
	// report it. The hostname is captured alongside it because
	// updateStatusWithRetry re-reads the whole object on every attempt: a Pool
	// whose route was removed concurrently comes back with a nil Spec.Route,
	// and the report must not dereference it.
	var conflictWith, claimedHostname string
	if pool.Spec.Route != nil && pool.Spec.Route.Enabled {
		if !r.GatewayAPIAvailable {
			logger.Info("TLSRoute requested but Gateway API CRDs not available, skipping")
		} else {
			owner, err := r.hostnameOwner(ctx, pool)
			if err != nil {
				return ctrl.Result{}, err
			}

			if owner.Name != pool.Name {
				// Losing the hostname is a permanent condition: nothing about
				// it improves by waiting, only a spec change resolves it. It is
				// reported and left alone rather than retried, and the loser
				// gives up any TLSRoute it may still hold from an earlier round.
				conflictWith = owner.Name
				claimedHostname = pool.Spec.Route.Hostname
				logger.Info("hostname already claimed by another Pool, skipping TLSRoute",
					"hostname", pool.Spec.Route.Hostname, "owner", owner.Name)
				if err := r.deleteOwnedTLSRoute(ctx, pool); err != nil {
					return ctrl.Result{}, err
				}
			} else {
				if err := r.reconcileTLSRoute(ctx, pool); err != nil {
					return ctrl.Result{}, fmt.Errorf("reconciling TLSRoute: %w", err)
				}

			}
		}
	} else if r.GatewayAPIAvailable {
		// Cleanup: delete owned TLSRoute if route is disabled
		if err := r.deleteOwnedTLSRoute(ctx, pool); err != nil {
			return ctrl.Result{}, err
		}
	}

	// Update status
	endpoints, err := r.countEndpoints(ctx, pool)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("counting endpoints for Pool %s: %w", pool.Name, err)
	}
	previousConditions := pool.Status.DeepCopy().Conditions
	if err := updateStatusWithRetry(ctx, r.Client, pool, func() {
		pool.Status.ObservedGeneration = pool.Generation
		pool.Status.ServiceName = pool.Name
		pool.Status.Endpoints = endpoints

		// A Pool is only useful once something is behind its Service. Reporting
		// that separately from "the Service exists" is what tells an operator
		// whether traffic can actually reach a server.
		switch {
		case conflictWith != "":
			meta.SetStatusCondition(&pool.Status.Conditions, metav1.Condition{
				Type:               openvoxv1alpha1.ConditionPoolReady,
				Status:             metav1.ConditionFalse,
				Reason:             "HostnameConflict",
				Message:            hostnameConflictMessage(claimedHostname, conflictWith),
				ObservedGeneration: pool.Generation,
			})
		case endpoints > 0:
			meta.SetStatusCondition(&pool.Status.Conditions, metav1.Condition{
				Type:               openvoxv1alpha1.ConditionPoolReady,
				Status:             metav1.ConditionTrue,
				Reason:             "EndpointsAvailable",
				Message:            fmt.Sprintf("%d ready endpoint(s) behind Service %s", endpoints, pool.Name),
				ObservedGeneration: pool.Generation,
			})
		default:
			meta.SetStatusCondition(&pool.Status.Conditions, metav1.Condition{
				Type:               openvoxv1alpha1.ConditionPoolReady,
				Status:             metav1.ConditionFalse,
				Reason:             "NoEndpoints",
				Message:            fmt.Sprintf("No ready Server pods select Service %s", pool.Name),
				ObservedGeneration: pool.Generation,
			})
		}
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating Pool status %s: %w", pool.Name, err)
	}

	// The event follows the condition so it fires once per transition rather
	// than on every reconcile.
	if conflictWith != "" && !wasConflictingWith(previousConditions, claimedHostname, conflictWith) {
		r.Recorder.Eventf(pool, nil, corev1.EventTypeWarning, EventReasonHostnameConflict, "Reconcile",
			"%s", hostnameConflictMessage(claimedHostname, conflictWith))
	}

	return ctrl.Result{}, nil
}

// hostnameConflictMessage is shared by the condition and the event so the two
// cannot drift apart, which is what makes the transition check below reliable.
func hostnameConflictMessage(hostname, owner string) string {
	return fmt.Sprintf("Hostname %q is already claimed by Pool %s", hostname, owner)
}

// wasConflictingWith reports whether the Pool already carried this exact
// conflict before the status update.
func wasConflictingWith(conditions []metav1.Condition, hostname, owner string) bool {
	cond := meta.FindStatusCondition(conditions, openvoxv1alpha1.ConditionPoolReady)
	return cond != nil &&
		cond.Status == metav1.ConditionFalse &&
		cond.Reason == "HostnameConflict" &&
		cond.Message == hostnameConflictMessage(hostname, owner)
}

// hostnameOwner returns the Pool entitled to the route hostname this Pool asks
// for, which may be the Pool itself.
//
// Listing and comparing is inherently racy: two Pools reconciled concurrently
// can both observe a free hostname and both create a TLSRoute for it. Deciding
// the winner by a property every observer agrees on -- the oldest
// creationTimestamp, with the name as tie-breaker, since the API server stores
// timestamps at second granularity -- makes them converge on the same Pool
// instead of fighting over the route.
func (r *PoolReconciler) hostnameOwner(ctx context.Context, pool *openvoxv1alpha1.Pool) (*openvoxv1alpha1.Pool, error) {
	all := &openvoxv1alpha1.PoolList{}
	if err := r.List(ctx, all, client.InNamespace(pool.Namespace)); err != nil {
		return nil, fmt.Errorf("listing Pools for the hostname conflict check: %w", err)
	}

	owner := pool
	for i := range all.Items {
		other := &all.Items[i]
		switch {
		case other.Name == pool.Name:
			continue
		// A Pool on its way out releases its claim, otherwise a stuck deletion
		// would keep the hostname hostage.
		case !other.DeletionTimestamp.IsZero():
			continue
		case other.Spec.Route == nil || !other.Spec.Route.Enabled:
			continue
		case other.Spec.Route.Hostname != pool.Spec.Route.Hostname:
			continue
		}
		if claimPrecedes(other, owner) {
			owner = other
		}
	}
	return owner, nil
}

// claimPrecedes orders two competing claims on the same hostname.
func claimPrecedes(a, b *openvoxv1alpha1.Pool) bool {
	if !a.CreationTimestamp.Equal(&b.CreationTimestamp) {
		return a.CreationTimestamp.Before(&b.CreationTimestamp)
	}
	return a.Name < b.Name
}

// deleteOwnedTLSRoute removes the TLSRoute this Pool created, if it still owns
// one. A route it does not control is left alone.
func (r *PoolReconciler) deleteOwnedTLSRoute(ctx context.Context, pool *openvoxv1alpha1.Pool) error {
	logger := log.FromContext(ctx)

	existing := &gwapiv1.TLSRoute{}
	if err := r.Get(ctx, types.NamespacedName{Name: pool.Name, Namespace: pool.Namespace}, existing); err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("getting TLSRoute %s: %w", pool.Name, err)
	}
	if !metav1.IsControlledBy(existing, pool) {
		return nil
	}

	logger.Info("deleting orphaned TLSRoute", "name", pool.Name)
	if err := r.Delete(ctx, existing); err != nil && !errors.IsNotFound(err) {
		return fmt.Errorf("deleting orphaned TLSRoute: %w", err)
	}
	r.Recorder.Eventf(pool, nil, corev1.EventTypeNormal, EventReasonTLSRouteDeleted, "Reconcile", "TLSRoute %s deleted", pool.Name)
	return nil
}

func (r *PoolReconciler) SetupWithManager(mgr ctrl.Manager) error {
	builder := ctrl.NewControllerManagedBy(mgr).
		For(&openvoxv1alpha1.Pool{}).
		Owns(&corev1.Service{}).
		Watches(&openvoxv1alpha1.Server{}, enqueuePoolsForServer(mgr.GetClient())).
		Watches(&openvoxv1alpha1.Pool{}, handler.EnqueueRequestsFromMapFunc(poolsSharingHostname(mgr.GetClient())))

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
			log.FromContext(ctx).Error(err, "failed to list Pools in watcher")
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

// enqueuePoolsSharingHostname returns a handler that enqueues the other Pools
// competing for the same route hostname.
//
// Without it a Pool that lost the hostname would stay blocked forever: its own
// spec never changes when the winner is deleted or gives up its route, and the
// controller no longer retries the conflict on a backoff.
func poolsSharingHostname(c client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []ctrl.Request {
		changed, ok := obj.(*openvoxv1alpha1.Pool)
		if !ok || changed.Spec.Route == nil || changed.Spec.Route.Hostname == "" {
			return nil
		}

		pools := &openvoxv1alpha1.PoolList{}
		if err := c.List(ctx, pools, client.InNamespace(changed.Namespace)); err != nil {
			log.FromContext(ctx).Error(err, "failed to list Pools in hostname watcher")
			return nil
		}

		var requests []ctrl.Request
		for i := range pools.Items {
			other := &pools.Items[i]
			if other.Name == changed.Name {
				continue
			}
			if other.Spec.Route == nil || other.Spec.Route.Hostname != changed.Spec.Route.Hostname {
				continue
			}
			requests = append(requests, ctrl.Request{
				NamespacedName: types.NamespacedName{Name: other.Name, Namespace: other.Namespace},
			})
		}
		return requests
	}
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
	for k, v := range pool.Spec.Service.Labels {
		labels[k] = v
	}

	svc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Name: svcName, Namespace: pool.Namespace},
	}
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, svc, func() error {
		if err := assertControlledBy(svc, pool, "Service"); err != nil {
			return err
		}
		svc.Labels = labels
		svc.Annotations = pool.Spec.Service.Annotations

		// Only the fields the Pool owns are written. clusterIP in particular is
		// assigned by Kubernetes and immutable, so the surrounding spec is left
		// untouched.
		svc.Spec.Type = svcType
		svc.Spec.Selector = poolServiceSelector(pool)
		if len(svc.Spec.Ports) == 0 {
			svc.Spec.Ports = []corev1.ServicePort{{}}
		}
		svc.Spec.Ports[0].Name = "https"
		svc.Spec.Ports[0].Port = port
		svc.Spec.Ports[0].TargetPort = intstr.FromInt32(8140)
		svc.Spec.Ports[0].Protocol = corev1.ProtocolTCP
		// An unset nodePort keeps whatever Kubernetes assigned; clearing it here
		// would hand out a new port and break every client using the old one.
		if pool.Spec.Service.NodePort > 0 {
			svc.Spec.Ports[0].NodePort = pool.Spec.Service.NodePort
		}
		svc.Spec.ExternalIPs = pool.Spec.Service.ExternalIPs
		return controllerutil.SetControllerReference(pool, svc, r.Scheme)
	})
	if err != nil {
		return fmt.Errorf("reconciling Service %s: %w", svcName, err)
	}
	switch op {
	case controllerutil.OperationResultCreated:
		logger.Info("created Pool Service", "name", svcName)
		r.Recorder.Eventf(pool, nil, corev1.EventTypeNormal, EventReasonServiceSynced, "Reconcile", "Service %s created", svcName)
	case controllerutil.OperationResultUpdated:
		r.Recorder.Eventf(pool, nil, corev1.EventTypeNormal, EventReasonServiceSynced, "Reconcile", "Service %s updated", svcName)
	}
	return nil
}

// countEndpoints reports how many ready endpoints back the Pool Service.
//
// An empty list is a real answer: nothing is behind the Service yet. A failed
// list is not, so it is returned rather than rounded down to zero, which would
// flip ConditionPoolReady to False on every API hiccup.
func (r *PoolReconciler) countEndpoints(ctx context.Context, pool *openvoxv1alpha1.Pool) (int32, error) {
	sliceList := &discoveryv1.EndpointSliceList{}
	if err := r.List(ctx, sliceList, client.InNamespace(pool.Namespace),
		client.MatchingLabels{"kubernetes.io/service-name": pool.Name}); err != nil {
		return 0, fmt.Errorf("listing EndpointSlices for Service %s: %w", pool.Name, err)
	}
	var count int32
	for _, slice := range sliceList.Items {
		for _, ep := range slice.Endpoints {
			if ep.Conditions.Ready != nil && *ep.Conditions.Ready {
				count++
			}
		}
	}
	return count, nil
}

func (r *PoolReconciler) reconcileTLSRoute(ctx context.Context, pool *openvoxv1alpha1.Pool) error {
	logger := log.FromContext(ctx)

	port := gwapiv1.PortNumber(8140)
	if pool.Spec.Service.Port > 0 {
		port = pool.Spec.Service.Port
	}

	parentRef := gwapiv1.ParentReference{
		Name: gwapiv1.ObjectName(pool.Spec.Route.GatewayRef.Name),
	}
	if pool.Spec.Route.GatewayRef.SectionName != "" {
		sectionName := gwapiv1.SectionName(pool.Spec.Route.GatewayRef.SectionName)
		parentRef.SectionName = &sectionName
	}

	route := &gwapiv1.TLSRoute{
		ObjectMeta: metav1.ObjectMeta{Name: pool.Name, Namespace: pool.Namespace},
	}
	op, err := controllerutil.CreateOrUpdate(ctx, r.Client, route, func() error {
		if err := assertControlledBy(route, pool, "TLSRoute"); err != nil {
			return err
		}
		route.Spec = gwapiv1.TLSRouteSpec{
			CommonRouteSpec: gwapiv1.CommonRouteSpec{
				ParentRefs: []gwapiv1.ParentReference{parentRef},
			},
			Hostnames: []gwapiv1.Hostname{gwapiv1.Hostname(pool.Spec.Route.Hostname)},
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
		}
		return controllerutil.SetControllerReference(pool, route, r.Scheme)
	})
	if err != nil {
		return fmt.Errorf("reconciling TLSRoute %s: %w", pool.Name, err)
	}
	switch op {
	case controllerutil.OperationResultCreated:
		logger.Info("created TLSRoute", "name", pool.Name, "hostname", pool.Spec.Route.Hostname)
		r.Recorder.Eventf(pool, nil, corev1.EventTypeNormal, EventReasonTLSRouteCreated, "Reconcile", "TLSRoute %s created for hostname %s", pool.Name, pool.Spec.Route.Hostname)
	case controllerutil.OperationResultUpdated:
		r.Recorder.Eventf(pool, nil, corev1.EventTypeNormal, EventReasonTLSRouteUpdated, "Reconcile", "TLSRoute %s updated", pool.Name)
	}
	return nil
}
