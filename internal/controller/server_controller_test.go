package controller

import (
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	networkingv1 "k8s.io/api/networking/v1"
	policyv1 "k8s.io/api/policy/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// serverPrereqs returns standard prerequisite objects for Server reconcile tests.
func serverPrereqs() []client.Object {
	return []client.Object{
		newConfig("production", withAuthorityRef("production-ca")),
		newConfigMap("production-config", map[string]string{
			"puppet.conf": "[main]\n", "puppetdb.conf": "", "webserver.conf": "",
			"webserver-ca.conf": "", "puppetserver.conf": "", "auth.conf": "",
			"ca.conf": "", "product.conf": "", "logback.xml": "", "metrics.conf": "",
			"ca-enabled.cfg": "", "ca-disabled.cfg": "",
		}),
		newCertificate("production-cert", "production-ca", openvoxv1alpha1.CertificatePhaseSigned),
		newCertificateAuthority("production-ca"),
		newSecret("production-cert-tls", map[string][]byte{
			"cert.pem": []byte("cert"), "key.pem": []byte("key"),
		}),
		newSecret("production-ca-ca", map[string][]byte{
			"ca_crt.pem": []byte("ca-cert"),
		}),
	}
}

func TestServerReconcile_NotFound(t *testing.T) {
	c := setupTestClient()
	r := newServerReconciler(c)

	res, err := r.Reconcile(testCtx(), testRequest("nonexistent"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.RequeueAfter != 0 {
		t.Error("expected no requeue for missing Server")
	}
}

func TestServerReconcile_ConfigNotFound(t *testing.T) {
	server := newServer("test-server")
	c := setupTestClient(server)
	r := newServerReconciler(c)

	res, err := r.Reconcile(testCtx(), testRequest("test-server"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.RequeueAfter != 5*time.Second {
		t.Errorf("expected requeue after 5s, got %v", res.RequeueAfter)
	}
}

func TestServerReconcile_CertificateNotSigned(t *testing.T) {
	cfg := newConfig("production")
	cert := newCertificate("production-cert", "production-ca", openvoxv1alpha1.CertificatePhasePending)
	server := newServer("test-server")

	c := setupTestClient(cfg, cert, server)
	r := newServerReconciler(c)

	res, err := r.Reconcile(testCtx(), testRequest("test-server"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.RequeueAfter != 10*time.Second {
		t.Errorf("expected requeue after 10s, got %v", res.RequeueAfter)
	}

	updated := &openvoxv1alpha1.Server{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-server", Namespace: testNamespace}, updated); err != nil {
		t.Fatalf("failed to get Server: %v", err)
	}
	if updated.Status.Phase != openvoxv1alpha1.ServerPhaseWaitingForCert {
		t.Errorf("expected phase %q, got %q", openvoxv1alpha1.ServerPhaseWaitingForCert, updated.Status.Phase)
	}
}

func TestServerReconcile_BasicDeployment(t *testing.T) {
	objs := append(serverPrereqs(), newServer("test-server", withReplicas(3)))
	c := setupTestClient(objs...)
	r := newServerReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("test-server")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	deploy := &appsv1.Deployment{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-server", Namespace: testNamespace}, deploy); err != nil {
		t.Fatalf("Deployment not created: %v", err)
	}

	if deploy.Spec.Replicas == nil || *deploy.Spec.Replicas != 3 {
		t.Errorf("expected 3 replicas, got %v", deploy.Spec.Replicas)
	}
	if deploy.Spec.Selector == nil || deploy.Spec.Selector.MatchLabels[LabelServer] != "test-server" {
		t.Error("deployment selector missing server label")
	}
	if deploy.Spec.Template.Labels[LabelServer] != "test-server" {
		t.Error("pod template missing server label")
	}
	if deploy.Spec.Template.Labels[LabelConfig] != "production" {
		t.Error("pod template missing config label")
	}
}

func TestServerReconcile_DeploymentStrategy(t *testing.T) {
	tests := []struct {
		name     string
		opts     []serverOption
		strategy appsv1.DeploymentStrategyType
	}{
		{
			name:     "server uses RollingUpdate",
			opts:     []serverOption{withServerRole(true), withCA(false)},
			strategy: appsv1.RollingUpdateDeploymentStrategyType,
		},
		{
			name:     "CA uses Recreate",
			opts:     []serverOption{withServerRole(false), withCA(true)},
			strategy: appsv1.RecreateDeploymentStrategyType,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objs := append(serverPrereqs(), newServer("test-server", tt.opts...))
			c := setupTestClient(objs...)
			r := newServerReconciler(c)

			if _, err := r.Reconcile(testCtx(), testRequest("test-server")); err != nil {
				t.Fatalf("reconcile error: %v", err)
			}

			deploy := &appsv1.Deployment{}
			if err := c.Get(testCtx(), types.NamespacedName{Name: "test-server", Namespace: testNamespace}, deploy); err != nil {
				t.Fatalf("Deployment not found: %v", err)
			}
			if deploy.Spec.Strategy.Type != tt.strategy {
				t.Errorf("expected strategy %q, got %q", tt.strategy, deploy.Spec.Strategy.Type)
			}
		})
	}
}

func TestServerReconcile_AnnotationHashes(t *testing.T) {
	objs := append(serverPrereqs(), newServer("test-server"))
	c := setupTestClient(objs...)
	r := newServerReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("test-server")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	deploy := &appsv1.Deployment{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-server", Namespace: testNamespace}, deploy); err != nil {
		t.Fatalf("Deployment not found: %v", err)
	}

	annotations := deploy.Spec.Template.Annotations
	for _, key := range []string{
		"openvox.voxpupuli.org/config-hash",
		"openvox.voxpupuli.org/ssl-secret-hash",
		"openvox.voxpupuli.org/ca-secret-hash",
	} {
		if v, ok := annotations[key]; !ok || v == "" {
			t.Errorf("annotation %q missing or empty", key)
		}
	}
}

func TestServerReconcile_PDBCreation(t *testing.T) {
	objs := append(serverPrereqs(), newServer("test-server", withPDBEnabled(true)))
	c := setupTestClient(objs...)
	r := newServerReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("test-server")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	pdb := &policyv1.PodDisruptionBudget{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-server", Namespace: testNamespace}, pdb); err != nil {
		t.Fatalf("PDB not created: %v", err)
	}

	if pdb.Spec.MinAvailable == nil {
		t.Fatal("PDB minAvailable not set (expected default)")
	}
	if pdb.Spec.Selector == nil || pdb.Spec.Selector.MatchLabels[LabelServer] != "test-server" {
		t.Error("PDB selector incorrect")
	}
}

func TestServerReconcile_PDBDeletion(t *testing.T) {
	existingPDB := &policyv1.PodDisruptionBudget{}
	existingPDB.Name = "test-server"
	existingPDB.Namespace = testNamespace

	objs := append(serverPrereqs(), newServer("test-server"), existingPDB)
	c := setupTestClient(objs...)
	r := newServerReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("test-server")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	pdb := &policyv1.PodDisruptionBudget{}
	err := c.Get(testCtx(), types.NamespacedName{Name: "test-server", Namespace: testNamespace}, pdb)
	if err == nil {
		t.Error("PDB should have been deleted")
	}
}

func TestServerReconcile_HPACreation(t *testing.T) {
	objs := append(serverPrereqs(), newServer("test-server", withAutoscaling(true)))
	c := setupTestClient(objs...)
	r := newServerReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("test-server")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	hpa := &autoscalingv2.HorizontalPodAutoscaler{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-server", Namespace: testNamespace}, hpa); err != nil {
		t.Fatalf("HPA not created: %v", err)
	}

	if hpa.Spec.MaxReplicas != 5 {
		t.Errorf("expected maxReplicas=5 (default), got %d", hpa.Spec.MaxReplicas)
	}
	if len(hpa.Spec.Metrics) != 1 {
		t.Fatalf("expected 1 metric, got %d", len(hpa.Spec.Metrics))
	}
	if hpa.Spec.Metrics[0].Resource == nil || hpa.Spec.Metrics[0].Resource.Target.AverageUtilization == nil {
		t.Fatal("expected CPU utilization metric")
	}
	if *hpa.Spec.Metrics[0].Resource.Target.AverageUtilization != 75 {
		t.Errorf("expected targetCPU=75 (default), got %d", *hpa.Spec.Metrics[0].Resource.Target.AverageUtilization)
	}
}

func TestServerReconcile_HPADeletion(t *testing.T) {
	existingHPA := &autoscalingv2.HorizontalPodAutoscaler{}
	existingHPA.Name = "test-server"
	existingHPA.Namespace = testNamespace

	objs := append(serverPrereqs(), newServer("test-server"), existingHPA)
	c := setupTestClient(objs...)
	r := newServerReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("test-server")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	hpa := &autoscalingv2.HorizontalPodAutoscaler{}
	err := c.Get(testCtx(), types.NamespacedName{Name: "test-server", Namespace: testNamespace}, hpa)
	if err == nil {
		t.Error("HPA should have been deleted")
	}
}

func TestServerReconcile_AutoscalingNoReplicas(t *testing.T) {
	objs := append(serverPrereqs(), newServer("test-server", withAutoscaling(true), withReplicas(3)))
	c := setupTestClient(objs...)
	r := newServerReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("test-server")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	deploy := &appsv1.Deployment{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-server", Namespace: testNamespace}, deploy); err != nil {
		t.Fatalf("Deployment not found: %v", err)
	}

	if deploy.Spec.Replicas != nil {
		t.Errorf("when HPA is enabled, replicas should not be set, got %d", *deploy.Spec.Replicas)
	}
}

func TestServerReconcile_StatusPhase(t *testing.T) {
	tests := []struct {
		name          string
		readyReplicas int32
		wantPhase     openvoxv1alpha1.ServerPhase
	}{
		{
			name:          "running with ready replicas",
			readyReplicas: 2,
			wantPhase:     openvoxv1alpha1.ServerPhaseRunning,
		},
		{
			name:          "pending with zero ready",
			readyReplicas: 0,
			wantPhase:     openvoxv1alpha1.ServerPhasePending,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objs := append(serverPrereqs(), newServer("test-server"))
			c := setupTestClient(objs...)
			r := newServerReconciler(c)

			// First reconcile to create the Deployment
			if _, err := r.Reconcile(testCtx(), testRequest("test-server")); err != nil {
				t.Fatalf("first reconcile error: %v", err)
			}

			// Update Deployment status with ready replicas
			deploy := &appsv1.Deployment{}
			if err := c.Get(testCtx(), types.NamespacedName{Name: "test-server", Namespace: testNamespace}, deploy); err != nil {
				t.Fatalf("Deployment not found: %v", err)
			}
			deploy.Status.ReadyReplicas = tt.readyReplicas
			if err := c.Status().Update(testCtx(), deploy); err != nil {
				t.Fatalf("failed to update Deployment status: %v", err)
			}

			// Reconcile again to pick up status
			if _, err := r.Reconcile(testCtx(), testRequest("test-server")); err != nil {
				t.Fatalf("second reconcile error: %v", err)
			}

			updated := &openvoxv1alpha1.Server{}
			if err := c.Get(testCtx(), types.NamespacedName{Name: "test-server", Namespace: testNamespace}, updated); err != nil {
				t.Fatalf("failed to get Server: %v", err)
			}
			if updated.Status.Phase != tt.wantPhase {
				t.Errorf("expected phase %q, got %q", tt.wantPhase, updated.Status.Phase)
			}
		})
	}
}

func TestServerReconcile_NetworkPolicyCreation(t *testing.T) {
	objs := append(serverPrereqs(), newServer("test-server", withServerNetworkPolicy(true)))
	c := setupTestClient(objs...)
	r := newServerReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("test-server")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	np := &networkingv1.NetworkPolicy{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-server-netpol", Namespace: testNamespace}, np); err != nil {
		t.Fatalf("NetworkPolicy not created: %v", err)
	}

	if np.Spec.PodSelector.MatchLabels[LabelServer] != "test-server" {
		t.Error("NetworkPolicy pod selector incorrect")
	}
	if len(np.Spec.PolicyTypes) != 1 || np.Spec.PolicyTypes[0] != networkingv1.PolicyTypeIngress {
		t.Errorf("expected PolicyTypes [Ingress], got %v", np.Spec.PolicyTypes)
	}
	if len(np.Spec.Ingress) != 1 {
		t.Fatalf("expected 1 ingress rule, got %d", len(np.Spec.Ingress))
	}
	if len(np.Spec.Ingress[0].Ports) != 1 || np.Spec.Ingress[0].Ports[0].Port.IntVal != 8140 {
		t.Error("expected ingress port 8140")
	}
	if len(np.Spec.Ingress[0].From) != 0 {
		t.Error("server default ingress should have no from restriction")
	}
}

func TestServerReconcile_NetworkPolicyDeletion(t *testing.T) {
	existingNP := &networkingv1.NetworkPolicy{}
	existingNP.Name = "test-server-netpol"
	existingNP.Namespace = testNamespace

	objs := append(serverPrereqs(), newServer("test-server"), existingNP)
	c := setupTestClient(objs...)
	r := newServerReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("test-server")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	np := &networkingv1.NetworkPolicy{}
	err := c.Get(testCtx(), types.NamespacedName{Name: "test-server-netpol", Namespace: testNamespace}, np)
	if err == nil {
		t.Error("NetworkPolicy should have been deleted")
	}
}
