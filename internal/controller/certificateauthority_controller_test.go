package controller

import (
	"strings"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// caPrereqs returns a Config with authorityRef pointing to the given CA name.
func caPrereqs(caName string) *openvoxv1alpha1.Config {
	return newConfig("production", withAuthorityRef(caName))
}

func TestCAReconcile_NotFound(t *testing.T) {
	c := setupTestClient()
	r := newCertificateAuthorityReconciler(c)

	res, err := r.Reconcile(testCtx(), testRequest("nonexistent"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.RequeueAfter != 0 {
		t.Error("expected no requeue for missing CA")
	}
}

func TestCAReconcile_NoConfig(t *testing.T) {
	ca := newCertificateAuthority("test-ca")
	ca.Status.Phase = "" // reset to trigger initial phase
	c := setupTestClient(ca)
	r := newCertificateAuthorityReconciler(c)

	res, err := r.Reconcile(testCtx(), testRequest("test-ca"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.RequeueAfter != 5*time.Second {
		t.Errorf("expected requeue after 5s, got %v", res.RequeueAfter)
	}
}

func TestCAReconcile_NoConfig_EmitsEvent(t *testing.T) {
	ca := newCertificateAuthority("test-ca")
	ca.Status.Phase = ""
	c := setupTestClient(ca)
	rec := events.NewFakeRecorder(10)
	r := &CertificateAuthorityReconciler{
		Client:   c,
		Scheme:   testScheme(),
		Recorder: rec,
	}

	if _, err := r.Reconcile(testCtx(), testRequest("test-ca")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	select {
	case evt := <-rec.Events:
		if !strings.Contains(evt, EventReasonCAWaitingForConfig) {
			t.Errorf("expected event reason %q, got %q", EventReasonCAWaitingForConfig, evt)
		}
		if !strings.Contains(evt, "Waiting for a Config with authorityRef") {
			t.Errorf("expected event message about waiting for Config, got %q", evt)
		}
	default:
		t.Error("expected WaitingForConfig event, but none was emitted")
	}
}

func TestCAReconcile_PVCCreation(t *testing.T) {
	ca := newCertificateAuthority("test-ca")
	ca.Status.Phase = ""
	cfg := caPrereqs("test-ca")
	c := setupTestClient(ca, cfg)
	r := newCertificateAuthorityReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("test-ca")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	pvc := &corev1.PersistentVolumeClaim{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-ca-data", Namespace: testNamespace}, pvc); err != nil {
		t.Fatalf("PVC not created: %v", err)
	}

	storageQty := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
	if storageQty.Cmp(resource.MustParse("1Gi")) != 0 {
		t.Errorf("expected storage 1Gi, got %s", storageQty.String())
	}

	if pvc.Labels[LabelCertificateAuthority] != "test-ca" {
		t.Errorf("PVC missing CA label, got %v", pvc.Labels)
	}
}

func TestCAReconcile_PVCCustomStorageClass(t *testing.T) {
	ca := newCertificateAuthority("test-ca")
	ca.Status.Phase = ""
	ca.Spec.Storage.StorageClass = "fast-ssd"
	ca.Spec.Storage.Size = "10Gi"
	cfg := caPrereqs("test-ca")
	c := setupTestClient(ca, cfg)
	r := newCertificateAuthorityReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("test-ca")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	pvc := &corev1.PersistentVolumeClaim{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-ca-data", Namespace: testNamespace}, pvc); err != nil {
		t.Fatalf("PVC not created: %v", err)
	}

	if pvc.Spec.StorageClassName == nil || *pvc.Spec.StorageClassName != "fast-ssd" {
		t.Errorf("expected storageClass fast-ssd, got %v", pvc.Spec.StorageClassName)
	}

	storageQty := pvc.Spec.Resources.Requests[corev1.ResourceStorage]
	if storageQty.Cmp(resource.MustParse("10Gi")) != 0 {
		t.Errorf("expected storage 10Gi, got %s", storageQty.String())
	}
}

func TestCAReconcile_RBACCreation(t *testing.T) {
	ca := newCertificateAuthority("test-ca")
	ca.Status.Phase = ""
	cfg := caPrereqs("test-ca")
	c := setupTestClient(ca, cfg)
	r := newCertificateAuthorityReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("test-ca")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	baseName := "test-ca-ca-setup"

	sa := &corev1.ServiceAccount{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: baseName, Namespace: testNamespace}, sa); err != nil {
		t.Fatalf("ServiceAccount not created: %v", err)
	}

	role := &rbacv1.Role{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: baseName, Namespace: testNamespace}, role); err != nil {
		t.Fatalf("Role not created: %v", err)
	}

	rb := &rbacv1.RoleBinding{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: baseName, Namespace: testNamespace}, rb); err != nil {
		t.Fatalf("RoleBinding not created: %v", err)
	}

	if rb.RoleRef.Name != baseName {
		t.Errorf("RoleBinding roleRef name: expected %q, got %q", baseName, rb.RoleRef.Name)
	}
}

func TestCAReconcile_RBACResourceNames(t *testing.T) {
	ca := newCertificateAuthority("test-ca")
	ca.Status.Phase = ""
	cfg := caPrereqs("test-ca")
	cert := newCertificate("my-cert", "test-ca", openvoxv1alpha1.CertificatePhasePending)
	c := setupTestClient(ca, cfg, cert)
	r := newCertificateAuthorityReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("test-ca")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	role := &rbacv1.Role{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-ca-ca-setup", Namespace: testNamespace}, role); err != nil {
		t.Fatalf("Role not created: %v", err)
	}

	// The first rule should have resourceNames containing the CA, key, CRL, and TLS secrets
	if len(role.Rules) < 1 {
		t.Fatal("expected at least 1 policy rule")
	}

	expected := map[string]bool{
		"test-ca-ca":     true,
		"test-ca-ca-key": true,
		"test-ca-ca-crl": true,
		"my-cert-tls":    true,
	}
	for _, rn := range role.Rules[0].ResourceNames {
		delete(expected, rn)
	}
	for missing := range expected {
		t.Errorf("Role resourceNames missing %q", missing)
	}
}

func TestCAReconcile_JobCreation(t *testing.T) {
	ca := newCertificateAuthority("test-ca")
	ca.Status.Phase = ""
	cfg := caPrereqs("test-ca")
	c := setupTestClient(ca, cfg)
	r := newCertificateAuthorityReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("test-ca")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	job := &batchv1.Job{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-ca-ca-setup", Namespace: testNamespace}, job); err != nil {
		t.Fatalf("Job not created: %v", err)
	}

	container := job.Spec.Template.Spec.Containers[0]
	expectedImage := "ghcr.io/slauger/openvox-server:latest"
	if container.Image != expectedImage {
		t.Errorf("expected image %q, got %q", expectedImage, container.Image)
	}

	// Verify security context
	podSC := job.Spec.Template.Spec.SecurityContext
	if podSC == nil || podSC.RunAsUser == nil || *podSC.RunAsUser != CASetupRunAsUser {
		t.Errorf("expected RunAsUser %d", CASetupRunAsUser)
	}

	containerSC := container.SecurityContext
	if containerSC == nil || containerSC.AllowPrivilegeEscalation == nil || *containerSC.AllowPrivilegeEscalation != false {
		t.Error("expected AllowPrivilegeEscalation=false")
	}

	// Verify env vars
	envMap := map[string]string{}
	for _, e := range container.Env {
		envMap[e.Name] = e.Value
	}
	if envMap["CA_SECRET_NAME"] != "test-ca-ca" {
		t.Errorf("expected CA_SECRET_NAME=test-ca-ca, got %q", envMap["CA_SECRET_NAME"])
	}
	if envMap["CA_NAME"] != "test-ca" {
		t.Errorf("expected CA_NAME=test-ca, got %q", envMap["CA_NAME"])
	}

	// Verify resources (defaults)
	if container.Resources.Requests.Cpu().Cmp(resource.MustParse(DefaultCAJobCPURequest)) != 0 {
		t.Errorf("expected CPU request %s, got %s", DefaultCAJobCPURequest, container.Resources.Requests.Cpu().String())
	}
}

func TestCAReconcile_PhasePending(t *testing.T) {
	ca := newCertificateAuthority("test-ca")
	ca.Status.Phase = "" // reset
	cfg := caPrereqs("test-ca")
	c := setupTestClient(ca, cfg)
	r := newCertificateAuthorityReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("test-ca")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	updated := &openvoxv1alpha1.CertificateAuthority{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-ca", Namespace: testNamespace}, updated); err != nil {
		t.Fatalf("failed to get CA: %v", err)
	}
	// Phase should be Initializing (set during job reconciliation) since CA secret doesn't exist
	if updated.Status.Phase != openvoxv1alpha1.CertificateAuthorityPhaseInitializing {
		t.Errorf("expected phase %q, got %q", openvoxv1alpha1.CertificateAuthorityPhaseInitializing, updated.Status.Phase)
	}
}

func TestCAReconcile_PhaseReady(t *testing.T) {
	ca := newCertificateAuthority("test-ca")
	ca.Status.Phase = ""
	cfg := caPrereqs("test-ca")
	caSecret := newSecret("test-ca-ca", map[string][]byte{
		"ca_crt.pem": []byte("ca-cert"),
	})
	c := setupTestClient(ca, cfg, caSecret)
	r := newCertificateAuthorityReconciler(c)

	// The reconcile will find the CA secret exists so the job reconciler returns immediately.
	// Then it sets Ready phase.
	if _, err := r.Reconcile(testCtx(), testRequest("test-ca")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	updated := &openvoxv1alpha1.CertificateAuthority{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-ca", Namespace: testNamespace}, updated); err != nil {
		t.Fatalf("failed to get CA: %v", err)
	}
	if updated.Status.Phase != openvoxv1alpha1.CertificateAuthorityPhaseReady {
		t.Errorf("expected phase %q, got %q", openvoxv1alpha1.CertificateAuthorityPhaseReady, updated.Status.Phase)
	}
	if updated.Status.CASecretName != "test-ca-ca" {
		t.Errorf("expected CASecretName %q, got %q", "test-ca-ca", updated.Status.CASecretName)
	}

	// Verify CAReady condition
	found := false
	for _, cond := range updated.Status.Conditions {
		if cond.Type == openvoxv1alpha1.ConditionCAReady {
			found = true
			if cond.Status != "True" {
				t.Errorf("expected condition status True, got %q", cond.Status)
			}
		}
	}
	if !found {
		t.Error("CAReady condition not set")
	}
}

func TestCAReconcile_NotAfterRequeue(t *testing.T) {
	ca := newCertificateAuthority("test-ca")
	ca.Status.Phase = ""
	cfg := caPrereqs("test-ca")
	// CA secret exists but with non-parseable cert data, so NotAfter will be nil
	caSecret := newSecret("test-ca-ca", map[string][]byte{
		"ca_crt.pem": []byte("not-a-valid-cert"),
	})
	c := setupTestClient(ca, cfg, caSecret)
	r := newCertificateAuthorityReconciler(c)

	res, err := r.Reconcile(testCtx(), testRequest("test-ca"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.RequeueAfter != 5*time.Second {
		t.Errorf("expected requeue after 5s when NotAfter is nil, got %v", res.RequeueAfter)
	}
}

func TestResolveCAJobResources_Default(t *testing.T) {
	ca := newCertificateAuthority("test-ca")
	ca.Spec.Resources = corev1.ResourceRequirements{} // empty

	res := resolveCAJobResources(ca)

	if res.Requests.Cpu().Cmp(resource.MustParse(DefaultCAJobCPURequest)) != 0 {
		t.Errorf("expected CPU request %s, got %s", DefaultCAJobCPURequest, res.Requests.Cpu().String())
	}
	if res.Requests.Memory().Cmp(resource.MustParse(DefaultCAJobMemoryRequest)) != 0 {
		t.Errorf("expected memory request %s, got %s", DefaultCAJobMemoryRequest, res.Requests.Memory().String())
	}
	if res.Limits.Cpu().Cmp(resource.MustParse(DefaultCAJobCPULimit)) != 0 {
		t.Errorf("expected CPU limit %s, got %s", DefaultCAJobCPULimit, res.Limits.Cpu().String())
	}
	if res.Limits.Memory().Cmp(resource.MustParse(DefaultCAJobMemoryLimit)) != 0 {
		t.Errorf("expected memory limit %s, got %s", DefaultCAJobMemoryLimit, res.Limits.Memory().String())
	}
}

func TestResolveCAJobResources_Custom(t *testing.T) {
	ca := newCertificateAuthority("test-ca")
	ca.Spec.Resources = corev1.ResourceRequirements{
		Requests: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("500m"),
			corev1.ResourceMemory: resource.MustParse("1Gi"),
		},
		Limits: corev1.ResourceList{
			corev1.ResourceCPU:    resource.MustParse("2"),
			corev1.ResourceMemory: resource.MustParse("2Gi"),
		},
	}

	res := resolveCAJobResources(ca)

	if res.Requests.Cpu().Cmp(resource.MustParse("500m")) != 0 {
		t.Errorf("expected CPU request 500m, got %s", res.Requests.Cpu().String())
	}
	if res.Limits.Memory().Cmp(resource.MustParse("2Gi")) != 0 {
		t.Errorf("expected memory limit 2Gi, got %s", res.Limits.Memory().String())
	}
}

// --- External CA tests ---

func TestCAReconcile_ExternalCA_Basic(t *testing.T) {
	ca := newCertificateAuthority("ext-ca", withExternal("https://puppet-ca.example.com:8140"))
	ca.Status.Phase = "" // reset to trigger initial phase
	c := setupTestClient(ca)
	r := newCertificateAuthorityReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("ext-ca")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	updated := &openvoxv1alpha1.CertificateAuthority{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "ext-ca", Namespace: testNamespace}, updated); err != nil {
		t.Fatalf("failed to get CA: %v", err)
	}

	if updated.Status.Phase != openvoxv1alpha1.CertificateAuthorityPhaseExternal {
		t.Errorf("expected phase %q, got %q", openvoxv1alpha1.CertificateAuthorityPhaseExternal, updated.Status.Phase)
	}
	if updated.Status.CASecretName != "ext-ca-ca" {
		t.Errorf("expected CASecretName %q, got %q", "ext-ca-ca", updated.Status.CASecretName)
	}

	// Verify CAReady condition with ExternalCA reason
	found := false
	for _, cond := range updated.Status.Conditions {
		if cond.Type == openvoxv1alpha1.ConditionCAReady {
			found = true
			if cond.Status != "True" {
				t.Errorf("expected condition status True, got %q", cond.Status)
			}
			if cond.Reason != "ExternalCA" {
				t.Errorf("expected condition reason ExternalCA, got %q", cond.Reason)
			}
		}
	}
	if !found {
		t.Error("CAReady condition not set")
	}
}

func TestCAReconcile_ExternalCA_WithCASecretRef(t *testing.T) {
	ca := newCertificateAuthority("ext-ca",
		withExternal("https://puppet-ca.example.com:8140"),
		withExternalCASecret("my-custom-ca-secret"),
	)
	ca.Status.Phase = ""
	caSecret := newSecret("my-custom-ca-secret", map[string][]byte{
		"ca_crt.pem": []byte("ca-cert-data"),
	})
	c := setupTestClient(ca, caSecret)
	r := newCertificateAuthorityReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("ext-ca")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	updated := &openvoxv1alpha1.CertificateAuthority{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "ext-ca", Namespace: testNamespace}, updated); err != nil {
		t.Fatalf("failed to get CA: %v", err)
	}

	if updated.Status.CASecretName != "my-custom-ca-secret" {
		t.Errorf("expected CASecretName %q, got %q", "my-custom-ca-secret", updated.Status.CASecretName)
	}
}

func TestCAReconcile_ExternalCA_CASecretNotFound(t *testing.T) {
	ca := newCertificateAuthority("ext-ca",
		withExternal("https://puppet-ca.example.com:8140"),
		withExternalCASecret("missing-secret"),
	)
	ca.Status.Phase = ""
	c := setupTestClient(ca)
	r := newCertificateAuthorityReconciler(c)

	res, err := r.Reconcile(testCtx(), testRequest("ext-ca"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.RequeueAfter != 5*time.Second {
		t.Errorf("expected requeue after 5s, got %v", res.RequeueAfter)
	}
}

func TestCAReconcile_ExternalCA_CASecretMissingKey(t *testing.T) {
	ca := newCertificateAuthority("ext-ca",
		withExternal("https://puppet-ca.example.com:8140"),
		withExternalCASecret("bad-secret"),
	)
	ca.Status.Phase = ""
	// Secret exists but lacks the ca_crt.pem key
	badSecret := newSecret("bad-secret", map[string][]byte{
		"wrong-key": []byte("data"),
	})
	c := setupTestClient(ca, badSecret)
	r := newCertificateAuthorityReconciler(c)

	res, err := r.Reconcile(testCtx(), testRequest("ext-ca"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.RequeueAfter != 5*time.Second {
		t.Errorf("expected requeue after 5s, got %v", res.RequeueAfter)
	}
}

// --- CA Service tests ---

func TestCAReconcile_ServiceCreation(t *testing.T) {
	ca := newCertificateAuthority("test-ca")
	ca.Status.Phase = ""
	cfg := caPrereqs("test-ca")
	c := setupTestClient(ca, cfg)
	r := newCertificateAuthorityReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("test-ca")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	svc := &corev1.Service{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-ca-internal", Namespace: testNamespace}, svc); err != nil {
		t.Fatalf("Service not created: %v", err)
	}

	// Verify name
	if svc.Name != "test-ca-internal" {
		t.Errorf("expected Service name %q, got %q", "test-ca-internal", svc.Name)
	}

	// Verify port
	if len(svc.Spec.Ports) != 1 {
		t.Fatalf("expected 1 port, got %d", len(svc.Spec.Ports))
	}
	if svc.Spec.Ports[0].Port != 8140 {
		t.Errorf("expected port 8140, got %d", svc.Spec.Ports[0].Port)
	}
	if svc.Spec.Ports[0].TargetPort.IntValue() != 8140 {
		t.Errorf("expected targetPort 8140, got %d", svc.Spec.Ports[0].TargetPort.IntValue())
	}

	// Verify selector
	if svc.Spec.Selector[LabelCA] != "true" {
		t.Errorf("expected selector %s=true, got %v", LabelCA, svc.Spec.Selector)
	}

	// Verify labels
	if svc.Labels[LabelCertificateAuthority] != "test-ca" {
		t.Errorf("expected CA label, got %v", svc.Labels)
	}

	// Verify owner reference
	if len(svc.OwnerReferences) == 0 {
		t.Fatal("expected owner reference on Service")
	}
	if svc.OwnerReferences[0].Name != "test-ca" {
		t.Errorf("expected owner ref name %q, got %q", "test-ca", svc.OwnerReferences[0].Name)
	}
}

func TestCAReconcile_JobIncludesServiceFQDN(t *testing.T) {
	ca := newCertificateAuthority("test-ca")
	ca.Status.Phase = ""
	cfg := caPrereqs("test-ca")
	server := newServer("ca-server", withCA(true), withServerRole(true))
	server.Spec.ConfigRef = "production"
	server.Spec.CertificateRef = "ca-cert"
	cert := newCertificate("ca-cert", "test-ca", openvoxv1alpha1.CertificatePhasePending)
	cert.Spec.DNSAltNames = []string{"puppet", "ca.example.com"}
	c := setupTestClient(ca, cfg, server, cert)
	r := newCertificateAuthorityReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("test-ca")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	// Verify the Job's DNS_ALT_NAMES env var includes the CA Service FQDN
	job := &batchv1.Job{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-ca-ca-setup", Namespace: testNamespace}, job); err != nil {
		t.Fatalf("Job not created: %v", err)
	}

	envMap := map[string]string{}
	for _, e := range job.Spec.Template.Spec.Containers[0].Env {
		envMap[e.Name] = e.Value
	}

	dnsAltNames := envMap["DNS_ALT_NAMES"]
	if dnsAltNames == "" {
		t.Fatal("DNS_ALT_NAMES env var not set")
	}

	// Should contain original SANs plus the CA internal Service FQDN
	expectedFQDN := "test-ca-internal.default.svc"
	if !strings.Contains(dnsAltNames, expectedFQDN) {
		t.Errorf("expected DNS_ALT_NAMES to contain %q, got %q", expectedFQDN, dnsAltNames)
	}
	if !strings.Contains(dnsAltNames, "puppet") {
		t.Errorf("expected DNS_ALT_NAMES to contain original SAN 'puppet', got %q", dnsAltNames)
	}

	// Verify Certificate CR was NOT modified
	updatedCert := &openvoxv1alpha1.Certificate{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "ca-cert", Namespace: testNamespace}, updatedCert); err != nil {
		t.Fatalf("failed to get Certificate: %v", err)
	}
	for _, san := range updatedCert.Spec.DNSAltNames {
		if san == expectedFQDN {
			t.Error("CA Service FQDN should NOT be injected into Certificate CR spec")
		}
	}
}

func TestCAReconcile_StatusServiceName(t *testing.T) {
	ca := newCertificateAuthority("test-ca")
	ca.Status.Phase = ""
	cfg := caPrereqs("test-ca")
	caSecret := newSecret("test-ca-ca", map[string][]byte{
		"ca_crt.pem": []byte("ca-cert"),
	})
	c := setupTestClient(ca, cfg, caSecret)
	r := newCertificateAuthorityReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("test-ca")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	updated := &openvoxv1alpha1.CertificateAuthority{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-ca", Namespace: testNamespace}, updated); err != nil {
		t.Fatalf("failed to get CA: %v", err)
	}

	if updated.Status.ServiceName != "test-ca-internal" {
		t.Errorf("expected status.serviceName %q, got %q", "test-ca-internal", updated.Status.ServiceName)
	}
}

func TestCAReconcile_ExternalCA_NoService(t *testing.T) {
	ca := newCertificateAuthority("ext-ca", withExternal("https://puppet-ca.example.com:8140"))
	ca.Status.Phase = ""
	c := setupTestClient(ca)
	r := newCertificateAuthorityReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("ext-ca")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	// No Service should be created for external CA
	svc := &corev1.Service{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "ext-ca-internal", Namespace: testNamespace}, svc); err == nil {
		t.Error("expected no Service for external CA, but one was created")
	}
}

func TestCAReconcile_ExternalCA_SkipsPVCAndJob(t *testing.T) {
	ca := newCertificateAuthority("ext-ca", withExternal("https://puppet-ca.example.com:8140"))
	ca.Status.Phase = ""
	c := setupTestClient(ca)
	r := newCertificateAuthorityReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("ext-ca")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	// No PVC should be created
	pvc := &corev1.PersistentVolumeClaim{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "ext-ca-data", Namespace: testNamespace}, pvc); err == nil {
		t.Error("expected no PVC for external CA, but one was created")
	}

	// No Job should be created
	job := &batchv1.Job{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "ext-ca-ca-setup", Namespace: testNamespace}, job); err == nil {
		t.Error("expected no Job for external CA, but one was created")
	}
}

func TestCAReconcile_StatusSigningSecretName(t *testing.T) {
	ca := newCertificateAuthority("test-ca")
	ca.Status.Phase = ""
	cfg := caPrereqs("test-ca")
	caSecret := newSecret("test-ca-ca", map[string][]byte{
		"ca_crt.pem": []byte("ca-cert"),
	})
	// Create a Server with ca:true and a Certificate for it
	server := newServer("ca-server", withCA(true), withServerRole(true))
	server.Spec.ConfigRef = "production"
	server.Spec.CertificateRef = "ca-cert"
	cert := newCertificate("ca-cert", "test-ca", openvoxv1alpha1.CertificatePhasePending)
	c := setupTestClient(ca, cfg, caSecret, server, cert)
	r := newCertificateAuthorityReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("test-ca")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	updated := &openvoxv1alpha1.CertificateAuthority{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-ca", Namespace: testNamespace}, updated); err != nil {
		t.Fatalf("failed to get CA: %v", err)
	}

	if updated.Status.SigningSecretName != "ca-cert-tls" {
		t.Errorf("expected status.signingSecretName %q, got %q", "ca-cert-tls", updated.Status.SigningSecretName)
	}
}

func TestCAReconcile_StatusSigningSecretName_NoCert(t *testing.T) {
	ca := newCertificateAuthority("test-ca")
	ca.Status.Phase = ""
	cfg := caPrereqs("test-ca")
	caSecret := newSecret("test-ca-ca", map[string][]byte{
		"ca_crt.pem": []byte("ca-cert"),
	})
	// No Certificate or Server objects - findCAServerCert returns nil
	c := setupTestClient(ca, cfg, caSecret)
	r := newCertificateAuthorityReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("test-ca")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	updated := &openvoxv1alpha1.CertificateAuthority{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-ca", Namespace: testNamespace}, updated); err != nil {
		t.Fatalf("failed to get CA: %v", err)
	}

	if updated.Status.SigningSecretName != "" {
		t.Errorf("expected empty status.signingSecretName, got %q", updated.Status.SigningSecretName)
	}
}

func TestCAReconcile_OperatorSigningCertCreated(t *testing.T) {
	ca := newCertificateAuthority("test-ca")
	ca.Status.Phase = ""
	cfg := caPrereqs("test-ca")
	certPEM, _ := generateTestCert(t)
	caSecret := newSecret("test-ca-ca", map[string][]byte{
		"ca_crt.pem": certPEM,
	})
	c := setupTestClient(ca, cfg, caSecret)
	r := newCertificateAuthorityReconciler(c)

	res, err := r.Reconcile(testCtx(), testRequest("test-ca"))
	if err != nil {
		t.Fatalf("reconcile error: %v", err)
	}
	// Should requeue because operator-signing cert was just created
	if res.RequeueAfter == 0 {
		t.Error("expected requeue after creating operator-signing cert")
	}

	// Verify the operator-signing Certificate CR was created
	signingCert := &openvoxv1alpha1.Certificate{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-ca-operator-signing", Namespace: testNamespace}, signingCert); err != nil {
		t.Fatalf("operator-signing Certificate not created: %v", err)
	}

	if signingCert.Spec.AuthorityRef != "test-ca" {
		t.Errorf("expected authorityRef %q, got %q", "test-ca", signingCert.Spec.AuthorityRef)
	}
	if signingCert.Spec.Certname != "test-ca-operator" {
		t.Errorf("expected certname %q, got %q", "test-ca-operator", signingCert.Spec.Certname)
	}
	if signingCert.Spec.CSRExtensions != nil {
		t.Error("expected no csrExtensions on operator-signing cert")
	}
	if len(signingCert.OwnerReferences) == 0 {
		t.Error("expected ownerReference on operator-signing cert")
	}
	if signingCert.OwnerReferences[0].Name != "test-ca" {
		t.Errorf("expected ownerRef to CA %q, got %q", "test-ca", signingCert.OwnerReferences[0].Name)
	}
}

func TestCAReconcile_OperatorSigningCertActivated(t *testing.T) {
	ca := newCertificateAuthority("test-ca")
	ca.Status.Phase = ""
	ca.Status.SigningSecretName = "old-init-job-tls"
	cfg := caPrereqs("test-ca")

	certPEM, keyPEM := generateTestCert(t)
	caSecret := newSecret("test-ca-ca", map[string][]byte{
		"ca_crt.pem": certPEM,
	})

	// Operator-signing cert already exists and is Signed
	signingCert := newCertificate("test-ca-operator-signing", "test-ca", openvoxv1alpha1.CertificatePhaseSigned)
	signingCert.Spec.Certname = "test-ca-operator"
	signingCert.Spec.CSRExtensions = &openvoxv1alpha1.CSRExtensionsSpec{PpCliAuth: true}

	// Need the signing cert TLS secret for CRL refresh to not error
	signingTLSSecret := newSecret("test-ca-operator-signing-tls", map[string][]byte{
		"cert.pem": certPEM,
		"key.pem":  keyPEM,
	})

	c := setupTestClient(ca, cfg, caSecret, signingCert, signingTLSSecret)
	r := newCertificateAuthorityReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("test-ca")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	updated := &openvoxv1alpha1.CertificateAuthority{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-ca", Namespace: testNamespace}, updated); err != nil {
		t.Fatalf("failed to get CA: %v", err)
	}

	if updated.Status.SigningSecretName != "test-ca-operator-signing-tls" {
		t.Errorf("expected signingSecretName %q, got %q", "test-ca-operator-signing-tls", updated.Status.SigningSecretName)
	}

	// Verify OperatorSigningReady condition
	found := false
	for _, cond := range updated.Status.Conditions {
		if cond.Type == openvoxv1alpha1.ConditionOperatorSigningReady {
			found = true
			if cond.Status != "True" {
				t.Errorf("expected condition status True, got %q", cond.Status)
			}
		}
	}
	if !found {
		t.Error("OperatorSigningReady condition not set")
	}
}

func TestCAReconcile_OperatorSigningDoesNotOverwriteInitJobCert(t *testing.T) {
	// When operator-signing cert is not yet active, the Init-Job cert should still be used
	ca := newCertificateAuthority("test-ca")
	ca.Status.Phase = ""
	cfg := caPrereqs("test-ca")
	caSecret := newSecret("test-ca-ca", map[string][]byte{
		"ca_crt.pem": []byte("ca-cert"),
	})
	// Server with ca:true referencing a cert
	server := newServer("ca-server", withCA(true), withServerRole(true))
	server.Spec.ConfigRef = "production"
	server.Spec.CertificateRef = "ca-cert"
	cert := newCertificate("ca-cert", "test-ca", openvoxv1alpha1.CertificatePhasePending)

	// Operator-signing cert exists but is NOT yet signed
	signingCert := newCertificate("test-ca-operator-signing", "test-ca", openvoxv1alpha1.CertificatePhaseRequesting)

	c := setupTestClient(ca, cfg, caSecret, server, cert, signingCert)
	r := newCertificateAuthorityReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("test-ca")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	updated := &openvoxv1alpha1.CertificateAuthority{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-ca", Namespace: testNamespace}, updated); err != nil {
		t.Fatalf("failed to get CA: %v", err)
	}

	// Should still use the Init-Job cert since operator-signing is not yet signed
	if updated.Status.SigningSecretName != "ca-cert-tls" {
		t.Errorf("expected signingSecretName %q (Init-Job cert), got %q", "ca-cert-tls", updated.Status.SigningSecretName)
	}
}

func TestCAReconcile_ExternalCA_SkipsOperatorSigning(t *testing.T) {
	ca := newCertificateAuthority("ext-ca", withExternal("https://puppet-ca.example.com:8140"))
	ca.Status.Phase = ""
	c := setupTestClient(ca)
	r := newCertificateAuthorityReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("ext-ca")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	// No operator-signing cert should be created for external CA
	certList := &openvoxv1alpha1.CertificateList{}
	if err := c.List(testCtx(), certList); err != nil {
		t.Fatalf("failed to list certs: %v", err)
	}
	for _, cert := range certList.Items {
		if strings.Contains(cert.Name, "operator-signing") {
			t.Errorf("unexpected operator-signing cert created for external CA: %s", cert.Name)
		}
	}
}

func TestEnsureCARole_CreateAndUpdate(t *testing.T) {
	ca := newCertificateAuthority("test-ca")
	c := setupTestClient(ca)
	r := newCertificateAuthorityReconciler(c)

	labels := caLabels(ca.Name)
	resourceNames := []string{"test-ca-ca", "test-ca-ca-key", "test-ca-ca-crl"}

	// Create
	if err := r.ensureCARole(testCtx(), "test-ca-ca-setup", testNamespace, labels, resourceNames, ca); err != nil {
		t.Fatalf("ensureCARole create: %v", err)
	}

	role := &rbacv1.Role{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-ca-ca-setup", Namespace: testNamespace}, role); err != nil {
		t.Fatalf("Role not created: %v", err)
	}
	if len(role.Rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(role.Rules))
	}

	// Update with additional resource names
	updatedNames := append(resourceNames, "extra-cert-tls")
	if err := r.ensureCARole(testCtx(), "test-ca-ca-setup", testNamespace, labels, updatedNames, ca); err != nil {
		t.Fatalf("ensureCARole update: %v", err)
	}

	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-ca-ca-setup", Namespace: testNamespace}, role); err != nil {
		t.Fatalf("Role not found after update: %v", err)
	}
	if len(role.Rules[0].ResourceNames) != 4 {
		t.Errorf("expected 4 resource names after update, got %d", len(role.Rules[0].ResourceNames))
	}
}

func TestReconcileCASetupRBAC(t *testing.T) {
	ca := newCertificateAuthority("test-ca")
	cert := newCertificate("my-cert", "test-ca", openvoxv1alpha1.CertificatePhasePending)

	c := setupTestClient(ca)
	r := newCertificateAuthorityReconciler(c)

	certs := []openvoxv1alpha1.Certificate{*cert}
	if err := r.reconcileCASetupRBAC(testCtx(), ca, certs); err != nil {
		t.Fatalf("reconcileCASetupRBAC: %v", err)
	}

	// Check ServiceAccount, Role, and RoleBinding were created
	sa := &corev1.ServiceAccount{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-ca-ca-setup", Namespace: testNamespace}, sa); err != nil {
		t.Fatalf("ServiceAccount not created: %v", err)
	}

	role := &rbacv1.Role{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-ca-ca-setup", Namespace: testNamespace}, role); err != nil {
		t.Fatalf("Role not created: %v", err)
	}

	rb := &rbacv1.RoleBinding{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-ca-ca-setup", Namespace: testNamespace}, rb); err != nil {
		t.Fatalf("RoleBinding not created: %v", err)
	}
}

func TestUpdateCRLSecret(t *testing.T) {
	ca := newCertificateAuthority("test-ca")
	c := setupTestClient(ca)
	r := newCertificateAuthorityReconciler(c)

	crlPEM := []byte("-----BEGIN X509 CRL-----\ntest-crl-data\n-----END X509 CRL-----\n")

	if err := r.updateCRLSecret(testCtx(), ca, "test-ca-ca-crl", crlPEM); err != nil {
		t.Fatalf("updateCRLSecret: %v", err)
	}

	secret := &corev1.Secret{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-ca-ca-crl", Namespace: testNamespace}, secret); err != nil {
		t.Fatalf("CRL Secret not created: %v", err)
	}
	if string(secret.Data["ca_crl.pem"]) != string(crlPEM) {
		t.Errorf("CRL data mismatch")
	}

	// Update
	newCRL := []byte("-----BEGIN X509 CRL-----\nupdated-crl\n-----END X509 CRL-----\n")
	if err := r.updateCRLSecret(testCtx(), ca, "test-ca-ca-crl", newCRL); err != nil {
		t.Fatalf("updateCRLSecret update: %v", err)
	}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-ca-ca-crl", Namespace: testNamespace}, secret); err != nil {
		t.Fatalf("CRL Secret not found after update: %v", err)
	}
	if string(secret.Data["ca_crl.pem"]) != string(newCRL) {
		t.Errorf("CRL data not updated")
	}
}

func TestCAReconcile_CAServiceDirectUpdate(t *testing.T) {
	ca := newCertificateAuthority("test-ca")
	c := setupTestClient(ca)
	r := newCertificateAuthorityReconciler(c)

	// Create
	if err := r.reconcileCAService(testCtx(), ca); err != nil {
		t.Fatalf("first reconcileCAService: %v", err)
	}

	// Update (should not error, exercises update path)
	if err := r.reconcileCAService(testCtx(), ca); err != nil {
		t.Fatalf("second reconcileCAService: %v", err)
	}

	svc := &corev1.Service{}
	svcName := caInternalServiceName("test-ca")
	if err := c.Get(testCtx(), types.NamespacedName{Name: svcName, Namespace: testNamespace}, svc); err != nil {
		t.Fatalf("Service not found after update: %v", err)
	}
	if svc.Spec.Ports[0].Port != 8140 {
		t.Errorf("expected port 8140 after update, got %d", svc.Spec.Ports[0].Port)
	}
	if svc.Spec.Selector[LabelCA] != "true" {
		t.Error("service selector missing CA label after update")
	}
}

func TestCAReconcile_ExternalCA_NoConfigRequired(t *testing.T) {
	// External CA should work without any Config object (unlike internal CA which requires it)
	ca := newCertificateAuthority("ext-ca", withExternal("https://puppet-ca.example.com:8140"))
	ca.Status.Phase = ""
	c := setupTestClient(ca) // no Config object
	r := newCertificateAuthorityReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("ext-ca")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	updated := &openvoxv1alpha1.CertificateAuthority{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "ext-ca", Namespace: testNamespace}, updated); err != nil {
		t.Fatalf("failed to get CA: %v", err)
	}

	// Should reach External phase without a Config
	if updated.Status.Phase != openvoxv1alpha1.CertificateAuthorityPhaseExternal {
		t.Errorf("expected phase %q, got %q", openvoxv1alpha1.CertificateAuthorityPhaseExternal, updated.Status.Phase)
	}
}
