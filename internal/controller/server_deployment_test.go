package controller

import (
	"fmt"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// testBuildPodSpec is a helper that constructs a PodSpec using buildPodSpec with reasonable defaults.
func testBuildPodSpec(server *openvoxv1alpha1.Server, cfg *openvoxv1alpha1.Config) corev1.PodSpec {
	cert := &openvoxv1alpha1.Certificate{
		Spec: openvoxv1alpha1.CertificateSpec{
			AuthorityRef: "production-ca",
			Certname:     "puppet",
		},
	}
	cert.Status.Phase = openvoxv1alpha1.CertificatePhaseSigned
	cert.Status.SecretName = "production-cert-tls"

	ca := newCertificateAuthority("production-ca")

	image := resolveImage(server, cfg)
	javaArgs := resolveJavaArgs(server)
	maxActive := server.Spec.MaxActiveInstances
	if maxActive <= 0 {
		maxActive = 1
	}
	javaArgs = fmt.Sprintf("%s -Djruby-puppet.max-active-instances=%d", javaArgs, maxActive)
	configMapName := fmt.Sprintf("%s-config", server.Spec.ConfigRef)

	r := &ServerReconciler{Scheme: testScheme()}
	return r.buildPodSpec(server, cfg, cert, ca, image, javaArgs, configMapName)
}

func TestBuildPodSpec_ServerRole(t *testing.T) {
	cfg := newConfig("production", withCodeImage("ghcr.io/slauger/puppet-code:latest"))
	server := newServer("test-server", withServerRole(true), withCA(false))

	podSpec := testBuildPodSpec(server, cfg)

	// Server role should have code volume
	found := false
	for _, v := range podSpec.Volumes {
		if v.Name == "code" {
			found = true
			break
		}
	}
	if !found {
		t.Error("server pod should have code volume")
	}

	// Server role should NOT have CA PVC
	for _, v := range podSpec.Volumes {
		if v.Name == "ca-data" {
			t.Error("server pod should not have CA data PVC")
		}
	}
}

func TestBuildPodSpec_CARole(t *testing.T) {
	cfg := newConfig("production")
	server := newServer("test-server", withCA(true), withServerRole(false))

	podSpec := testBuildPodSpec(server, cfg)

	// CA should have ca-data PVC
	found := false
	for _, v := range podSpec.Volumes {
		if v.Name == "ca-data" {
			found = true
			if v.PersistentVolumeClaim == nil {
				t.Error("ca-data should be a PVC")
			} else if v.PersistentVolumeClaim.ClaimName != "production-ca-data" {
				t.Errorf("expected PVC name %q, got %q", "production-ca-data", v.PersistentVolumeClaim.ClaimName)
			}
			break
		}
	}
	if !found {
		t.Error("CA pod should have ca-data volume")
	}

	// CA should have autosign-policy mount
	hasAutosign := false
	for _, vm := range podSpec.Containers[0].VolumeMounts {
		if vm.Name == "autosign-policy" {
			hasAutosign = true
			break
		}
	}
	if !hasAutosign {
		t.Error("CA pod should have autosign-policy volume mount")
	}

	// CA should use webserver-ca.conf (via ca.conf key mapping)
	hasWebserverCA := false
	for _, v := range podSpec.Volumes {
		if v.Name == "webserver-conf" && v.ConfigMap != nil {
			for _, item := range v.ConfigMap.Items {
				if item.Key == "webserver-ca.conf" {
					hasWebserverCA = true
				}
			}
		}
	}
	if !hasWebserverCA {
		t.Error("CA pod should use webserver-ca.conf")
	}

	// CA should NOT have code volume (server=false)
	for _, v := range podSpec.Volumes {
		if v.Name == "code" {
			t.Error("CA-only pod should not have code volume")
		}
	}
}

func TestBuildPodSpec_CodeVolumeImage(t *testing.T) {
	cfg := newConfig("production", withCodeImage("ghcr.io/slauger/puppet-code:v1.0"))
	server := newServer("test-server", withServerRole(true))

	podSpec := testBuildPodSpec(server, cfg)

	for _, v := range podSpec.Volumes {
		if v.Name == "code" {
			if v.Image == nil {
				t.Fatal("code volume should be an image volume")
			}
			if v.Image.Reference != "ghcr.io/slauger/puppet-code:v1.0" {
				t.Errorf("expected code image %q, got %q", "ghcr.io/slauger/puppet-code:v1.0", v.Image.Reference)
			}
			return
		}
	}
	t.Error("code volume not found")
}

func TestBuildPodSpec_CodeVolumePVC(t *testing.T) {
	cfg := newConfig("production", withCodePVC("puppet-code-pvc"))
	server := newServer("test-server", withServerRole(true))

	podSpec := testBuildPodSpec(server, cfg)

	for _, v := range podSpec.Volumes {
		if v.Name == "code" {
			if v.PersistentVolumeClaim == nil {
				t.Fatal("code volume should be a PVC")
			}
			if v.PersistentVolumeClaim.ClaimName != "puppet-code-pvc" {
				t.Errorf("expected PVC name %q, got %q", "puppet-code-pvc", v.PersistentVolumeClaim.ClaimName)
			}
			if !v.PersistentVolumeClaim.ReadOnly {
				t.Error("code PVC should be read-only")
			}
			return
		}
	}
	t.Error("code volume not found")
}

func TestBuildPodSpec_NoCodeVolume(t *testing.T) {
	cfg := newConfig("production") // no code spec
	server := newServer("test-server", withServerRole(true))

	podSpec := testBuildPodSpec(server, cfg)

	for _, v := range podSpec.Volumes {
		if v.Name == "code" {
			t.Error("pod should not have code volume when no code spec is set")
		}
	}
}

func TestBuildPodSpec_ReadOnlyRootFilesystem(t *testing.T) {
	cfg := newConfig("production", withReadOnlyRootFS(true))
	server := newServer("test-server")

	podSpec := testBuildPodSpec(server, cfg)

	sc := podSpec.Containers[0].SecurityContext
	if sc == nil || sc.ReadOnlyRootFilesystem == nil || !*sc.ReadOnlyRootFilesystem {
		t.Error("readOnlyRootFilesystem should be true")
	}
}

func TestBuildPodSpec_SecurityContext(t *testing.T) {
	cfg := newConfig("production")
	server := newServer("test-server")

	podSpec := testBuildPodSpec(server, cfg)

	// Pod-level security context
	psc := podSpec.SecurityContext
	if psc == nil {
		t.Fatal("pod security context is nil")
	}
	if psc.RunAsUser == nil || *psc.RunAsUser != 1001 {
		t.Errorf("expected RunAsUser=1001, got %v", psc.RunAsUser)
	}
	if psc.RunAsNonRoot == nil || !*psc.RunAsNonRoot {
		t.Error("expected RunAsNonRoot=true")
	}
	if psc.SeccompProfile == nil || psc.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Error("expected Seccomp RuntimeDefault")
	}

	// Container-level security context
	csc := podSpec.Containers[0].SecurityContext
	if csc == nil {
		t.Fatal("container security context is nil")
	}
	if csc.Capabilities == nil || len(csc.Capabilities.Drop) == 0 {
		t.Error("expected capabilities Drop ALL")
	} else if csc.Capabilities.Drop[0] != "ALL" {
		t.Errorf("expected Drop ALL, got %v", csc.Capabilities.Drop)
	}
	if csc.AllowPrivilegeEscalation == nil || *csc.AllowPrivilegeEscalation {
		t.Error("expected AllowPrivilegeEscalation=false")
	}
}

func TestBuildPodSpec_ENCVolumes(t *testing.T) {
	cfg := newConfig("production", withNodeClassifierRef("my-enc"))
	server := newServer("test-server", withServerRole(true))

	podSpec := testBuildPodSpec(server, cfg)

	hasENCConfig := false
	hasENCCache := false
	for _, v := range podSpec.Volumes {
		switch v.Name {
		case "enc-config":
			hasENCConfig = true
			if v.Secret == nil || v.Secret.SecretName != "production-enc" {
				t.Errorf("enc-config volume should reference Secret %q", "production-enc")
			}
		case "enc-cache":
			hasENCCache = true
			if v.EmptyDir == nil {
				t.Error("enc-cache should be emptyDir")
			}
		}
	}
	if !hasENCConfig {
		t.Error("missing enc-config volume")
	}
	if !hasENCCache {
		t.Error("missing enc-cache volume")
	}
}

func TestBuildPodSpec_PoolLabels(t *testing.T) {
	server := newServer("test-server", withPoolRefs("pool-a", "pool-b"))

	// Pool labels are set at the deployment level via reconcileDeployment,
	// so we test via a full reconcile.
	objs := append(serverPrereqs(), server)
	c := setupTestClient(objs...)
	r := newServerReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("test-server")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	deploy := &appsv1.Deployment{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-server", Namespace: testNamespace}, deploy); err != nil {
		t.Fatalf("Deployment not found: %v", err)
	}

	for _, poolName := range []string{"pool-a", "pool-b"} {
		label := poolLabel(poolName)
		if deploy.Spec.Template.Labels[label] != "true" {
			t.Errorf("pod template missing pool label %q", label)
		}
	}
}

func TestBuildPodSpec_InitContainer(t *testing.T) {
	cfg := newConfig("production")
	server := newServer("test-server")

	podSpec := testBuildPodSpec(server, cfg)

	if len(podSpec.InitContainers) == 0 {
		t.Fatal("expected at least one init container")
	}

	initC := podSpec.InitContainers[0]
	if initC.Name != "tls-init" {
		t.Errorf("expected init container name %q, got %q", "tls-init", initC.Name)
	}

	// Check that tls-init mounts ssl, ssl-cert, ssl-ca
	mountNames := make(map[string]bool)
	for _, vm := range initC.VolumeMounts {
		mountNames[vm.Name] = true
	}
	for _, name := range []string{"ssl", "ssl-cert", "ssl-ca"} {
		if !mountNames[name] {
			t.Errorf("tls-init missing volume mount %q", name)
		}
	}
}

func TestBuildPodSpec_Probes(t *testing.T) {
	cfg := newConfig("production")
	server := newServer("test-server")

	podSpec := testBuildPodSpec(server, cfg)

	container := podSpec.Containers[0]
	if container.StartupProbe == nil {
		t.Fatal("startup probe is nil")
	}
	if container.StartupProbe.PeriodSeconds != 5 {
		t.Errorf("startup probe period = %d, want 5", container.StartupProbe.PeriodSeconds)
	}
	if container.StartupProbe.FailureThreshold != 60 {
		t.Errorf("startup probe failure threshold = %d, want 60", container.StartupProbe.FailureThreshold)
	}

	if container.ReadinessProbe == nil {
		t.Fatal("readiness probe is nil")
	}
	if container.ReadinessProbe.PeriodSeconds != 10 {
		t.Errorf("readiness probe period = %d, want 10", container.ReadinessProbe.PeriodSeconds)
	}

	if container.LivenessProbe == nil {
		t.Fatal("liveness probe is nil")
	}
	if container.LivenessProbe.PeriodSeconds != 30 {
		t.Errorf("liveness probe period = %d, want 30", container.LivenessProbe.PeriodSeconds)
	}

	// All probes should check /status/v1/simple via HTTPS
	for name, probe := range map[string]*corev1.Probe{
		"startup":   container.StartupProbe,
		"readiness": container.ReadinessProbe,
		"liveness":  container.LivenessProbe,
	} {
		if probe.HTTPGet == nil {
			t.Errorf("%s probe missing HTTPGet", name)
			continue
		}
		if probe.HTTPGet.Path != "/status/v1/simple" {
			t.Errorf("%s probe path = %q, want /status/v1/simple", name, probe.HTTPGet.Path)
		}
		if probe.HTTPGet.Scheme != corev1.URISchemeHTTPS {
			t.Errorf("%s probe scheme = %q, want HTTPS", name, probe.HTTPGet.Scheme)
		}
	}
}

func TestResolveJavaArgs_Default(t *testing.T) {
	server := &openvoxv1alpha1.Server{
		Spec: openvoxv1alpha1.ServerSpec{},
	}
	got := resolveJavaArgs(server)
	if got != "-Xms512m -Xmx1024m" {
		t.Errorf("resolveJavaArgs() = %q, want %q", got, "-Xms512m -Xmx1024m")
	}
}

func TestResolveJavaArgs_Explicit(t *testing.T) {
	server := &openvoxv1alpha1.Server{
		Spec: openvoxv1alpha1.ServerSpec{
			JavaArgs: "-Xms1g -Xmx2g",
		},
	}
	got := resolveJavaArgs(server)
	if got != "-Xms1g -Xmx2g" {
		t.Errorf("resolveJavaArgs() = %q, want %q", got, "-Xms1g -Xmx2g")
	}
}

func TestResolveJavaArgs_FromMemoryLimit(t *testing.T) {
	server := &openvoxv1alpha1.Server{
		Spec: openvoxv1alpha1.ServerSpec{
			Resources: corev1.ResourceRequirements{
				Limits: corev1.ResourceList{
					corev1.ResourceMemory: resource.MustParse("4Gi"),
				},
			},
		},
	}
	got := resolveJavaArgs(server)
	// 4Gi = 4294967296 bytes, 90% = 3865470566 bytes, /1024/1024 = 3686 MB
	expected := fmt.Sprintf("-Xms%dm -Xmx%dm", 3686, 3686)
	if got != expected {
		t.Errorf("resolveJavaArgs() = %q, want %q", got, expected)
	}
}
