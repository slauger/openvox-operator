package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

func TestPoolReconcile_NotFound(t *testing.T) {
	c := setupTestClient()
	r := newPoolReconciler(c, false)

	res, err := r.Reconcile(testCtx(), testRequest("nonexistent"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.RequeueAfter != 0 {
		t.Error("expected no requeue for missing Pool")
	}
}

func TestPoolReconcile_ServiceCreation(t *testing.T) {
	pool := newPool("puppet")
	c := setupTestClient(pool)
	r := newPoolReconciler(c, false)

	if _, err := r.Reconcile(testCtx(), testRequest("puppet")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	svc := &corev1.Service{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "puppet", Namespace: testNamespace}, svc); err != nil {
		t.Fatalf("Service not created: %v", err)
	}

	// Default port
	if len(svc.Spec.Ports) != 1 || svc.Spec.Ports[0].Port != 8140 {
		t.Errorf("expected port 8140, got %v", svc.Spec.Ports)
	}

	// Default type
	if svc.Spec.Type != corev1.ServiceTypeClusterIP {
		t.Errorf("expected type ClusterIP, got %v", svc.Spec.Type)
	}

	// Selector should use pool label
	expectedSelector := poolServiceSelector(pool)
	for k, v := range expectedSelector {
		if svc.Spec.Selector[k] != v {
			t.Errorf("selector[%q] = %q, want %q", k, svc.Spec.Selector[k], v)
		}
	}

	// Status should be updated
	updated := &openvoxv1alpha1.Pool{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "puppet", Namespace: testNamespace}, updated); err != nil {
		t.Fatalf("failed to get Pool: %v", err)
	}
	if updated.Status.ServiceName != "puppet" {
		t.Errorf("expected status.serviceName %q, got %q", "puppet", updated.Status.ServiceName)
	}
}

func TestPoolReconcile_ServiceCustomPort(t *testing.T) {
	pool := newPool("puppet", withServicePort(9140))
	c := setupTestClient(pool)
	r := newPoolReconciler(c, false)

	if _, err := r.Reconcile(testCtx(), testRequest("puppet")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	svc := &corev1.Service{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "puppet", Namespace: testNamespace}, svc); err != nil {
		t.Fatalf("Service not found: %v", err)
	}

	if svc.Spec.Ports[0].Port != 9140 {
		t.Errorf("expected port 9140, got %d", svc.Spec.Ports[0].Port)
	}
}

func TestPoolReconcile_ServiceLoadBalancer(t *testing.T) {
	pool := newPool("puppet", withServiceType(corev1.ServiceTypeLoadBalancer))
	c := setupTestClient(pool)
	r := newPoolReconciler(c, false)

	if _, err := r.Reconcile(testCtx(), testRequest("puppet")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	svc := &corev1.Service{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "puppet", Namespace: testNamespace}, svc); err != nil {
		t.Fatalf("Service not found: %v", err)
	}

	if svc.Spec.Type != corev1.ServiceTypeLoadBalancer {
		t.Errorf("expected type LoadBalancer, got %v", svc.Spec.Type)
	}
}

func TestPoolReconcile_ServiceAnnotations(t *testing.T) {
	annotations := map[string]string{
		"service.beta.kubernetes.io/aws-load-balancer-type": "nlb",
	}
	pool := newPool("puppet", withServiceAnnotations(annotations))
	c := setupTestClient(pool)
	r := newPoolReconciler(c, false)

	if _, err := r.Reconcile(testCtx(), testRequest("puppet")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	svc := &corev1.Service{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "puppet", Namespace: testNamespace}, svc); err != nil {
		t.Fatalf("Service not found: %v", err)
	}

	for k, v := range annotations {
		if svc.Annotations[k] != v {
			t.Errorf("annotation %q = %q, want %q", k, svc.Annotations[k], v)
		}
	}
}

func TestPoolReconcile_EndpointCount(t *testing.T) {
	pool := newPool("puppet")
	eps := newEndpointSlice("puppet-abc", "puppet", 3)
	c := setupTestClient(pool, eps)
	r := newPoolReconciler(c, false)

	if _, err := r.Reconcile(testCtx(), testRequest("puppet")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	updated := &openvoxv1alpha1.Pool{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "puppet", Namespace: testNamespace}, updated); err != nil {
		t.Fatalf("failed to get Pool: %v", err)
	}

	if updated.Status.Endpoints != 3 {
		t.Errorf("expected 3 endpoints, got %d", updated.Status.Endpoints)
	}
}

func TestPoolReconcile_TLSRouteCreation(t *testing.T) {
	pool := newPool("puppet", withRoute(true, "puppet.example.com", "my-gateway"))
	c := setupTestClient(pool)
	r := newPoolReconciler(c, true)

	if _, err := r.Reconcile(testCtx(), testRequest("puppet")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	route := &gwapiv1.TLSRoute{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "puppet", Namespace: testNamespace}, route); err != nil {
		t.Fatalf("TLSRoute not created: %v", err)
	}

	if len(route.Spec.Hostnames) != 1 || string(route.Spec.Hostnames[0]) != "puppet.example.com" {
		t.Errorf("unexpected hostnames: %v", route.Spec.Hostnames)
	}
	if len(route.Spec.ParentRefs) != 1 || string(route.Spec.ParentRefs[0].Name) != "my-gateway" {
		t.Errorf("unexpected parentRefs: %v", route.Spec.ParentRefs)
	}
}

func TestPoolReconcile_TLSRouteDisabled(t *testing.T) {
	pool := newPool("puppet") // no route spec
	c := setupTestClient(pool)
	r := newPoolReconciler(c, true)

	if _, err := r.Reconcile(testCtx(), testRequest("puppet")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	route := &gwapiv1.TLSRoute{}
	err := c.Get(testCtx(), types.NamespacedName{Name: "puppet", Namespace: testNamespace}, route)
	if err == nil {
		t.Error("TLSRoute should not be created when route is disabled")
	}
}

func TestPoolReconcile_TLSRouteHostnameConflict(t *testing.T) {
	// Create pool-a first and reconcile it
	pool1 := newPool("puppet-a", withRoute(true, "puppet.example.com", "gw"))
	c := setupTestClient(pool1)
	r := newPoolReconciler(c, true)

	if _, err := r.Reconcile(testCtx(), testRequest("puppet-a")); err != nil {
		t.Fatalf("reconcile pool-a error: %v", err)
	}

	// Now add pool-b with the same hostname
	pool2 := newPool("puppet-b", withRoute(true, "puppet.example.com", "gw"))
	if err := c.Create(testCtx(), pool2); err != nil {
		t.Fatalf("failed to create pool-b: %v", err)
	}

	// Reconcile pool-b should fail with hostname conflict
	_, err := r.Reconcile(testCtx(), testRequest("puppet-b"))
	if err == nil {
		t.Fatal("expected hostname conflict error")
	}
}

func TestPoolReconcile_UpdateExistingService(t *testing.T) {
	pool := newPool("puppet", withServicePort(9140))
	existingSvc := &corev1.Service{}
	existingSvc.Name = "puppet"
	existingSvc.Namespace = testNamespace
	existingSvc.Spec.Ports = []corev1.ServicePort{
		{Name: "https", Port: 8140},
	}

	c := setupTestClient(pool, existingSvc)
	r := newPoolReconciler(c, false)

	if _, err := r.Reconcile(testCtx(), testRequest("puppet")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	svc := &corev1.Service{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "puppet", Namespace: testNamespace}, svc); err != nil {
		t.Fatalf("Service not found: %v", err)
	}

	if svc.Spec.Ports[0].Port != 9140 {
		t.Errorf("Service was not updated: port = %d, want 9140", svc.Spec.Ports[0].Port)
	}
}
