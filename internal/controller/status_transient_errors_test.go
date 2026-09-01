package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// apiTimeout mimics the transient failure an overloaded or restarting API
// server returns for any group. It is deliberately not a NotFound.
func apiTimeout(group, resource string) error {
	return apierrors.NewServerTimeout(
		schema.GroupResource{Group: group, Resource: resource}, "get", 1)
}

// newFailingDeploymentClient serves every request normally except Deployment
// reads, which fail with a transient error.
func newFailingDeploymentClient(objs ...client.Object) client.Client {
	return fake.NewClientBuilder().
		WithScheme(testScheme()).
		WithObjects(objs...).
		WithStatusSubresource(&openvoxv1alpha1.Server{}, &openvoxv1alpha1.Database{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Get: func(ctx context.Context, cl client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
				if _, ok := obj.(*appsv1.Deployment); ok {
					return apiTimeout("apps", "deployments")
				}
				return cl.Get(ctx, key, obj, opts...)
			},
		}).
		Build()
}

// newFailingEndpointSliceClient serves every request normally except
// EndpointSlice listings, which fail with a transient error.
func newFailingEndpointSliceClient(objs ...client.Object) client.Client {
	return fake.NewClientBuilder().
		WithScheme(testScheme()).
		WithObjects(objs...).
		WithStatusSubresource(&openvoxv1alpha1.Pool{}).
		WithInterceptorFuncs(interceptor.Funcs{
			List: func(ctx context.Context, cl client.WithWatch, list client.ObjectList, opts ...client.ListOption) error {
				if _, ok := list.(*discoveryv1.EndpointSliceList); ok {
					return apiTimeout("discovery.k8s.io", "endpointslices")
				}
				return cl.List(ctx, list, opts...)
			},
		}).
		Build()
}

// TestServerGetReadyReplicas_MissingDeploymentIsZero pins the one case where
// reporting zero is the honest answer: there is no Deployment, so nothing can
// be ready.
func TestServerGetReadyReplicas_MissingDeploymentIsZero(t *testing.T) {
	server := newServer("puppetserver")
	r := newServerReconciler(setupTestClient(server))

	ready, err := r.getReadyReplicas(testCtx(), server)
	if err != nil {
		t.Fatalf("a missing Deployment must not be an error: %v", err)
	}
	if ready != 0 {
		t.Errorf("expected 0 ready replicas without a Deployment, got %d", ready)
	}
}

// TestServerGetReadyReplicas_TransientFailureIsAnError covers the case the
// helper used to swallow: a failed lookup must not be reported as zero, which
// would be indistinguishable from an idle workload.
func TestServerGetReadyReplicas_TransientFailureIsAnError(t *testing.T) {
	server := newServer("puppetserver")
	r := newServerReconciler(newFailingDeploymentClient(server))

	_, err := r.getReadyReplicas(testCtx(), server)
	if err == nil {
		t.Fatal("expected a transient Deployment lookup failure to be returned")
	}
	if apierrors.IsNotFound(err) {
		t.Errorf("transient error was turned into NotFound: %v", err)
	}
}

func TestDatabaseGetReadyReplicas_MissingDeploymentIsZero(t *testing.T) {
	db := newDatabase("puppetdb")
	r := newDatabaseReconciler(setupTestClient(db))

	ready, err := r.getReadyReplicas(testCtx(), db)
	if err != nil {
		t.Fatalf("a missing Deployment must not be an error: %v", err)
	}
	if ready != 0 {
		t.Errorf("expected 0 ready replicas without a Deployment, got %d", ready)
	}
}

func TestDatabaseGetReadyReplicas_TransientFailureIsAnError(t *testing.T) {
	db := newDatabase("puppetdb")
	r := newDatabaseReconciler(newFailingDeploymentClient(db))

	_, err := r.getReadyReplicas(testCtx(), db)
	if err == nil {
		t.Fatal("expected a transient Deployment lookup failure to be returned")
	}
	if apierrors.IsNotFound(err) {
		t.Errorf("transient error was turned into NotFound: %v", err)
	}
}

// TestPoolCountEndpoints_TransientFailureIsAnError distinguishes an empty
// EndpointSlice list, which is a real answer, from a failed one, which is not.
func TestPoolCountEndpoints_TransientFailureIsAnError(t *testing.T) {
	pool := newPool("puppet")

	r := newPoolReconciler(setupTestClient(pool), false)
	count, err := r.countEndpoints(testCtx(), pool)
	if err != nil {
		t.Fatalf("an empty EndpointSlice list must not be an error: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 endpoints, got %d", count)
	}

	failing := newPoolReconciler(newFailingEndpointSliceClient(pool), false)
	if _, err := failing.countEndpoints(testCtx(), pool); err == nil {
		t.Fatal("expected a failed EndpointSlice list to be returned")
	}
}

// TestServerReconcile_TransientDeploymentFailureKeepsStatus is the regression
// guard for the flapping this fixes: while Deployment reads fail, a Server that
// was reported ready keeps that status instead of dropping to Pending and
// emitting a transition event.
func TestServerReconcile_TransientDeploymentFailureKeepsStatus(t *testing.T) {
	server := newServer("test-server")
	server.Status.Desired = 3
	server.Status.Ready = 3
	server.Status.Phase = openvoxv1alpha1.ServerPhaseRunning
	meta.SetStatusCondition(&server.Status.Conditions, metav1.Condition{
		Type:    openvoxv1alpha1.ConditionServerReady,
		Status:  metav1.ConditionTrue,
		Reason:  "ReplicasReady",
		Message: "3/3 replicas ready",
	})

	c := newFailingDeploymentClient(append(serverPrereqs(), server)...)
	r := newServerReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest(server.Name)); err == nil {
		t.Fatal("expected the reconcile to fail while the Deployment cannot be read")
	}

	got := &openvoxv1alpha1.Server{}
	if err := c.Get(testCtx(), client.ObjectKeyFromObject(server), got); err != nil {
		t.Fatalf("getting the Server back: %v", err)
	}
	if got.Status.Ready != 3 {
		t.Errorf("ready replicas were overwritten on a transient failure: got %d, want 3", got.Status.Ready)
	}
	if got.Status.Phase != openvoxv1alpha1.ServerPhaseRunning {
		t.Errorf("phase flipped on a transient failure: got %q, want %q", got.Status.Phase, openvoxv1alpha1.ServerPhaseRunning)
	}
	if cond := meta.FindStatusCondition(got.Status.Conditions, openvoxv1alpha1.ConditionServerReady); cond == nil || cond.Status != metav1.ConditionTrue {
		t.Errorf("Ready condition flipped on a transient failure: %+v", cond)
	}
}

// TestPoolReconcile_TransientEndpointFailureKeepsCondition is the same guard
// for the Pool condition added in #549.
func TestPoolReconcile_TransientEndpointFailureKeepsCondition(t *testing.T) {
	pool := newPool("puppet")
	pool.Status.Endpoints = 2
	meta.SetStatusCondition(&pool.Status.Conditions, metav1.Condition{
		Type:    openvoxv1alpha1.ConditionPoolReady,
		Status:  metav1.ConditionTrue,
		Reason:  "EndpointsAvailable",
		Message: "2 ready endpoint(s) behind Service puppet",
	})

	c := newFailingEndpointSliceClient(pool)
	r := newPoolReconciler(c, false)

	if _, err := r.Reconcile(testCtx(), testRequest(pool.Name)); err == nil {
		t.Fatal("expected the reconcile to fail while EndpointSlices cannot be listed")
	}

	got := &openvoxv1alpha1.Pool{}
	if err := c.Get(testCtx(), client.ObjectKeyFromObject(pool), got); err != nil {
		t.Fatalf("getting the Pool back: %v", err)
	}
	if got.Status.Endpoints != 2 {
		t.Errorf("endpoint count was overwritten on a transient failure: got %d, want 2", got.Status.Endpoints)
	}
	if cond := meta.FindStatusCondition(got.Status.Conditions, openvoxv1alpha1.ConditionPoolReady); cond == nil || cond.Status != metav1.ConditionTrue {
		t.Errorf("PoolReady condition flipped on a transient failure: %+v", cond)
	}
}
