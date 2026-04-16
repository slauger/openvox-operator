package controller

import (
	"fmt"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"sigs.k8s.io/controller-runtime/pkg/client"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// databasePrereqs returns standard prerequisite objects for Database reconcile tests.
func databasePrereqs() []client.Object {
	return []client.Object{
		newCertificate("production-db-cert", "production-ca", openvoxv1alpha1.CertificatePhaseSigned),
		newCertificateAuthority("production-ca"),
		newSecret("production-db-cert-tls", map[string][]byte{
			"cert.pem": []byte("cert"), "key.pem": []byte("key"),
		}),
		newSecret("production-ca-ca", map[string][]byte{
			"ca_crt.pem": []byte("ca-cert"),
		}),
		newSecret("pg-credentials", map[string][]byte{
			"username": []byte("puppetdb"),
			"password": []byte("secret"),
		}),
	}
}

func TestDatabaseReconcile_NotFound(t *testing.T) {
	c := setupTestClient()
	r := newDatabaseReconciler(c)

	res, err := r.Reconcile(testCtx(), testRequest("nonexistent"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.RequeueAfter != 0 {
		t.Error("expected no requeue for missing Database")
	}
}

func TestDatabaseReconcile_CertificateNotFound(t *testing.T) {
	db := newDatabase("test-db")
	c := setupTestClient(db)
	r := newDatabaseReconciler(c)

	res, err := r.Reconcile(testCtx(), testRequest("test-db"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.RequeueAfter != 5*time.Second {
		t.Errorf("expected requeue after 5s, got %v", res.RequeueAfter)
	}
}

func TestDatabaseReconcile_CertificateNotSigned(t *testing.T) {
	cert := newCertificate("production-db-cert", "production-ca", openvoxv1alpha1.CertificatePhasePending)
	db := newDatabase("test-db")

	c := setupTestClient(cert, db)
	r := newDatabaseReconciler(c)

	res, err := r.Reconcile(testCtx(), testRequest("test-db"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.RequeueAfter != 10*time.Second {
		t.Errorf("expected requeue after 10s, got %v", res.RequeueAfter)
	}

	updated := &openvoxv1alpha1.Database{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-db", Namespace: testNamespace}, updated); err != nil {
		t.Fatalf("failed to get Database: %v", err)
	}
	if updated.Status.Phase != openvoxv1alpha1.DatabasePhaseWaitingForCert {
		t.Errorf("expected phase %q, got %q", openvoxv1alpha1.DatabasePhaseWaitingForCert, updated.Status.Phase)
	}
}

func TestDatabaseReconcile_CredentialsMissing(t *testing.T) {
	cert := newCertificate("production-db-cert", "production-ca", openvoxv1alpha1.CertificatePhaseSigned)
	ca := newCertificateAuthority("production-ca")
	certSecret := newSecret("production-db-cert-tls", map[string][]byte{
		"cert.pem": []byte("cert"), "key.pem": []byte("key"),
	})
	caSecret := newSecret("production-ca-ca", map[string][]byte{
		"ca_crt.pem": []byte("ca-cert"),
	})
	db := newDatabase("test-db")

	c := setupTestClient(cert, ca, certSecret, caSecret, db)
	r := newDatabaseReconciler(c)

	res, err := r.Reconcile(testCtx(), testRequest("test-db"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.RequeueAfter != 5*time.Second {
		t.Errorf("expected requeue after 5s, got %v", res.RequeueAfter)
	}
}

func TestDatabaseReconcile_BasicDeployment(t *testing.T) {
	objs := append(databasePrereqs(), newDatabase("test-db", withDatabaseReplicas(2)))
	c := setupTestClient(objs...)
	r := newDatabaseReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("test-db")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	deploy := &appsv1.Deployment{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-db", Namespace: testNamespace}, deploy); err != nil {
		t.Fatalf("Deployment not created: %v", err)
	}

	if deploy.Spec.Replicas == nil || *deploy.Spec.Replicas != 2 {
		t.Errorf("expected 2 replicas, got %v", deploy.Spec.Replicas)
	}
	if deploy.Spec.Selector == nil || deploy.Spec.Selector.MatchLabels[LabelDatabase] != "test-db" {
		t.Error("deployment selector missing database label")
	}
	if deploy.Spec.Template.Labels[LabelDatabase] != "test-db" {
		t.Error("pod template missing database label")
	}
}

func TestDatabaseReconcile_ServiceCreation(t *testing.T) {
	objs := append(databasePrereqs(), newDatabase("test-db"))
	c := setupTestClient(objs...)
	r := newDatabaseReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("test-db")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	svc := &corev1.Service{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-db", Namespace: testNamespace}, svc); err != nil {
		t.Fatalf("Service not created: %v", err)
	}

	if svc.Spec.Type != corev1.ServiceTypeClusterIP {
		t.Errorf("expected ClusterIP, got %v", svc.Spec.Type)
	}
	if len(svc.Spec.Ports) != 1 || svc.Spec.Ports[0].Port != DatabaseHTTPSPort {
		t.Errorf("expected port %d, got %v", DatabaseHTTPSPort, svc.Spec.Ports)
	}
	if svc.Spec.Selector[LabelDatabase] != "test-db" {
		t.Error("service selector missing database label")
	}
}

func TestDatabaseReconcile_ConfigMapRendering(t *testing.T) {
	objs := append(databasePrereqs(), newDatabase("test-db"))
	c := setupTestClient(objs...)
	r := newDatabaseReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("test-db")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	cm := &corev1.ConfigMap{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-db-config", Namespace: testNamespace}, cm); err != nil {
		t.Fatalf("ConfigMap not created: %v", err)
	}

	if _, ok := cm.Data["jetty.ini"]; !ok {
		t.Error("jetty.ini missing from ConfigMap")
	}
	if _, ok := cm.Data["config.ini"]; !ok {
		t.Error("config.ini missing from ConfigMap")
	}
}

func TestDatabaseReconcile_SecretRendering(t *testing.T) {
	objs := append(databasePrereqs(), newDatabase("test-db"))
	c := setupTestClient(objs...)
	r := newDatabaseReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("test-db")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	secret := &corev1.Secret{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-db-db", Namespace: testNamespace}, secret); err != nil {
		t.Fatalf("database Secret not created: %v", err)
	}

	if _, ok := secret.Data["database.ini"]; !ok {
		t.Error("database.ini missing from Secret")
	}
}

func TestDatabaseReconcile_StatusURL(t *testing.T) {
	objs := append(databasePrereqs(), newDatabase("test-db"))
	c := setupTestClient(objs...)
	r := newDatabaseReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("test-db")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	updated := &openvoxv1alpha1.Database{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-db", Namespace: testNamespace}, updated); err != nil {
		t.Fatalf("failed to get Database: %v", err)
	}

	expected := fmt.Sprintf("https://test-db.%s.svc.cluster.local:8081", testNamespace)
	if updated.Status.URL != expected {
		t.Errorf("expected URL %q, got %q", expected, updated.Status.URL)
	}
}

func TestDatabaseReconcile_AnnotationHashes(t *testing.T) {
	objs := append(databasePrereqs(), newDatabase("test-db"))
	c := setupTestClient(objs...)
	r := newDatabaseReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("test-db")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	deploy := &appsv1.Deployment{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-db", Namespace: testNamespace}, deploy); err != nil {
		t.Fatalf("Deployment not found: %v", err)
	}

	annotations := deploy.Spec.Template.Annotations
	for _, key := range []string{
		"openvox.voxpupuli.org/config-hash",
		"openvox.voxpupuli.org/db-secret-hash",
		"openvox.voxpupuli.org/ssl-secret-hash",
		"openvox.voxpupuli.org/ca-secret-hash",
		"openvox.voxpupuli.org/pg-credentials-hash",
	} {
		if v, ok := annotations[key]; !ok || v == "" {
			t.Errorf("annotation %q missing or empty", key)
		}
	}
}

func TestDatabaseReconcile_JavaArgs(t *testing.T) {
	t.Run("set", func(t *testing.T) {
		objs := append(databasePrereqs(), newDatabase("test-db", withDatabaseJavaArgs("-Xmx2g -Xms1g")))
		c := setupTestClient(objs...)
		r := newDatabaseReconciler(c)

		if _, err := r.Reconcile(testCtx(), testRequest("test-db")); err != nil {
			t.Fatalf("reconcile error: %v", err)
		}

		deploy := &appsv1.Deployment{}
		if err := c.Get(testCtx(), types.NamespacedName{Name: "test-db", Namespace: testNamespace}, deploy); err != nil {
			t.Fatalf("Deployment not found: %v", err)
		}

		container := deploy.Spec.Template.Spec.Containers[0]
		found := false
		for _, env := range container.Env {
			if env.Name == "JAVA_ARGS" {
				found = true
				if env.Value != "-Xmx2g -Xms1g" {
					t.Errorf("expected JAVA_ARGS %q, got %q", "-Xmx2g -Xms1g", env.Value)
				}
				break
			}
		}
		if !found {
			t.Error("JAVA_ARGS env var not found on container")
		}
	})

	t.Run("empty by default", func(t *testing.T) {
		objs := append(databasePrereqs(), newDatabase("test-db"))
		c := setupTestClient(objs...)
		r := newDatabaseReconciler(c)

		if _, err := r.Reconcile(testCtx(), testRequest("test-db")); err != nil {
			t.Fatalf("reconcile error: %v", err)
		}

		deploy := &appsv1.Deployment{}
		if err := c.Get(testCtx(), types.NamespacedName{Name: "test-db", Namespace: testNamespace}, deploy); err != nil {
			t.Fatalf("Deployment not found: %v", err)
		}

		container := deploy.Spec.Template.Spec.Containers[0]
		for _, env := range container.Env {
			if env.Name == "JAVA_ARGS" {
				t.Error("JAVA_ARGS should not be set when javaArgs is empty")
			}
		}
	})
}

func TestDatabaseReconcile_ExecProbes(t *testing.T) {
	objs := append(databasePrereqs(), newDatabase("test-db"))
	c := setupTestClient(objs...)
	r := newDatabaseReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("test-db")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	deploy := &appsv1.Deployment{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-db", Namespace: testNamespace}, deploy); err != nil {
		t.Fatalf("Deployment not created: %v", err)
	}

	container := deploy.Spec.Template.Spec.Containers[0]
	expectedCmd := []string{"curl", "-sf", fmt.Sprintf("http://127.0.0.1:%d/status/v1/simple", DatabaseHTTPPort)}

	probes := map[string]*corev1.Probe{
		"startup":   container.StartupProbe,
		"readiness": container.ReadinessProbe,
		"liveness":  container.LivenessProbe,
	}
	for name, probe := range probes {
		if probe == nil {
			t.Errorf("%s probe is nil", name)
			continue
		}
		if probe.Exec == nil {
			t.Errorf("%s probe: expected Exec handler, got nil", name)
			continue
		}
		if len(probe.Exec.Command) != len(expectedCmd) {
			t.Errorf("%s probe: expected command %v, got %v", name, expectedCmd, probe.Exec.Command)
			continue
		}
		for i, arg := range expectedCmd {
			if probe.Exec.Command[i] != arg {
				t.Errorf("%s probe: command[%d] expected %q, got %q", name, i, arg, probe.Exec.Command[i])
			}
		}
	}
}

func TestDatabaseReconcile_StatusPhase(t *testing.T) {
	tests := []struct {
		name          string
		readyReplicas int32
		wantPhase     openvoxv1alpha1.DatabasePhase
	}{
		{
			name:          "running with ready replicas",
			readyReplicas: 1,
			wantPhase:     openvoxv1alpha1.DatabasePhaseRunning,
		},
		{
			name:          "pending with zero ready",
			readyReplicas: 0,
			wantPhase:     openvoxv1alpha1.DatabasePhasePending,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			objs := append(databasePrereqs(), newDatabase("test-db"))
			c := setupTestClient(objs...)
			r := newDatabaseReconciler(c)

			// First reconcile to create the Deployment
			if _, err := r.Reconcile(testCtx(), testRequest("test-db")); err != nil {
				t.Fatalf("first reconcile error: %v", err)
			}

			// Update Deployment status with ready replicas
			deploy := &appsv1.Deployment{}
			if err := c.Get(testCtx(), types.NamespacedName{Name: "test-db", Namespace: testNamespace}, deploy); err != nil {
				t.Fatalf("Deployment not found: %v", err)
			}
			deploy.Status.ReadyReplicas = tt.readyReplicas
			if err := c.Status().Update(testCtx(), deploy); err != nil {
				t.Fatalf("failed to update Deployment status: %v", err)
			}

			// Reconcile again to pick up status
			if _, err := r.Reconcile(testCtx(), testRequest("test-db")); err != nil {
				t.Fatalf("second reconcile error: %v", err)
			}

			updated := &openvoxv1alpha1.Database{}
			if err := c.Get(testCtx(), types.NamespacedName{Name: "test-db", Namespace: testNamespace}, updated); err != nil {
				t.Fatalf("failed to get Database: %v", err)
			}
			if updated.Status.Phase != tt.wantPhase {
				t.Errorf("expected phase %q, got %q", tt.wantPhase, updated.Status.Phase)
			}
		})
	}
}

func TestDatabaseReconcile_PriorityClassName(t *testing.T) {
	t.Run("set", func(t *testing.T) {
		objs := append(databasePrereqs(), newDatabase("test-db", withDatabasePriorityClassName("high-priority")))
		c := setupTestClient(objs...)
		r := newDatabaseReconciler(c)

		if _, err := r.Reconcile(testCtx(), testRequest("test-db")); err != nil {
			t.Fatalf("reconcile error: %v", err)
		}

		deploy := &appsv1.Deployment{}
		if err := c.Get(testCtx(), types.NamespacedName{Name: "test-db", Namespace: testNamespace}, deploy); err != nil {
			t.Fatalf("Deployment not found: %v", err)
		}
		if deploy.Spec.Template.Spec.PriorityClassName != "high-priority" {
			t.Errorf("expected PriorityClassName %q, got %q", "high-priority", deploy.Spec.Template.Spec.PriorityClassName)
		}
	})

	t.Run("empty by default", func(t *testing.T) {
		objs := append(databasePrereqs(), newDatabase("test-db"))
		c := setupTestClient(objs...)
		r := newDatabaseReconciler(c)

		if _, err := r.Reconcile(testCtx(), testRequest("test-db")); err != nil {
			t.Fatalf("reconcile error: %v", err)
		}

		deploy := &appsv1.Deployment{}
		if err := c.Get(testCtx(), types.NamespacedName{Name: "test-db", Namespace: testNamespace}, deploy); err != nil {
			t.Fatalf("Deployment not found: %v", err)
		}
		if deploy.Spec.Template.Spec.PriorityClassName != "" {
			t.Errorf("expected empty PriorityClassName, got %q", deploy.Spec.Template.Spec.PriorityClassName)
		}
	})
}

func TestDatabaseReconcile_NetworkPolicyCreation(t *testing.T) {
	objs := append(databasePrereqs(), newDatabase("test-db", withDatabaseNetworkPolicy(true)))
	c := setupTestClient(objs...)
	r := newDatabaseReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("test-db")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	np := &networkingv1.NetworkPolicy{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-db-netpol", Namespace: testNamespace}, np); err != nil {
		t.Fatalf("NetworkPolicy not created: %v", err)
	}

	if np.Spec.PodSelector.MatchLabels[LabelDatabase] != "test-db" {
		t.Error("NetworkPolicy pod selector incorrect")
	}
	if len(np.Spec.PolicyTypes) != 1 || np.Spec.PolicyTypes[0] != networkingv1.PolicyTypeIngress {
		t.Errorf("expected PolicyTypes [Ingress], got %v", np.Spec.PolicyTypes)
	}
	if len(np.Spec.Ingress) != 1 {
		t.Fatalf("expected 1 ingress rule, got %d", len(np.Spec.Ingress))
	}
	if len(np.Spec.Ingress[0].Ports) != 1 || np.Spec.Ingress[0].Ports[0].Port.IntVal != DatabaseHTTPSPort {
		t.Error("expected ingress port 8081")
	}
	if len(np.Spec.Ingress[0].From) != 1 {
		t.Fatal("expected 1 from peer")
	}
	if np.Spec.Ingress[0].From[0].PodSelector == nil ||
		np.Spec.Ingress[0].From[0].PodSelector.MatchLabels["app.kubernetes.io/name"] != "openvox" {
		t.Error("expected from selector app.kubernetes.io/name=openvox")
	}
}

func TestDatabaseReconcile_NetworkPolicyDeletion(t *testing.T) {
	existingNP := &networkingv1.NetworkPolicy{}
	existingNP.Name = "test-db-netpol"
	existingNP.Namespace = testNamespace

	objs := append(databasePrereqs(), newDatabase("test-db"), existingNP)
	c := setupTestClient(objs...)
	r := newDatabaseReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("test-db")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	np := &networkingv1.NetworkPolicy{}
	err := c.Get(testCtx(), types.NamespacedName{Name: "test-db-netpol", Namespace: testNamespace}, np)
	if err == nil {
		t.Error("NetworkPolicy should have been deleted")
	}
}

func TestDatabaseReconcile_NetworkPolicyAdditionalIngress(t *testing.T) {
	extraPort := intstr.FromInt32(9090)
	tcp := corev1.ProtocolTCP
	additionalRules := []networkingv1.NetworkPolicyIngressRule{
		{
			Ports: []networkingv1.NetworkPolicyPort{
				{Protocol: &tcp, Port: &extraPort},
			},
		},
	}

	objs := append(databasePrereqs(), newDatabase("test-db",
		withDatabaseNetworkPolicy(true),
		withDatabaseNetworkPolicyAdditionalIngress(additionalRules),
	))
	c := setupTestClient(objs...)
	r := newDatabaseReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("test-db")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	np := &networkingv1.NetworkPolicy{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-db-netpol", Namespace: testNamespace}, np); err != nil {
		t.Fatalf("NetworkPolicy not created: %v", err)
	}

	if len(np.Spec.Ingress) != 2 {
		t.Fatalf("expected 2 ingress rules (default + additional), got %d", len(np.Spec.Ingress))
	}
	if np.Spec.Ingress[1].Ports[0].Port.IntVal != 9090 {
		t.Errorf("expected additional ingress port 9090, got %d", np.Spec.Ingress[1].Ports[0].Port.IntVal)
	}
}
