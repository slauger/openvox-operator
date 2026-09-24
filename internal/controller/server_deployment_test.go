package controller

import (
	"fmt"
	"strings"
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

func TestBuildPodSpec_RoutesYAMLMount(t *testing.T) {
	routesMount := func(podSpec corev1.PodSpec) *corev1.VolumeMount {
		for i, vm := range podSpec.Containers[0].VolumeMounts {
			if vm.Name == "routes-yaml" {
				return &podSpec.Containers[0].VolumeMounts[i]
			}
		}
		return nil
	}

	// PuppetDB wired up -> routes.yaml mounted at $confdir/routes.yaml.
	cfgWithDB := newConfig("production", withDatabaseRef("production-db"))
	server := newServer("test-server", withServerRole(true))
	podSpec := testBuildPodSpec(server, cfgWithDB)

	vm := routesMount(podSpec)
	if vm == nil {
		t.Fatal("server pod should mount routes-yaml when PuppetDB is the active backend")
	}
	if vm.MountPath != "/etc/puppetlabs/puppet/routes.yaml" || vm.SubPath != "routes.yaml" {
		t.Errorf("unexpected routes.yaml mount: path=%q subPath=%q", vm.MountPath, vm.SubPath)
	}
	hasVol := false
	for _, v := range podSpec.Volumes {
		if v.Name == "routes-yaml" {
			hasVol = true
		}
	}
	if !hasVol {
		t.Error("server pod should have a routes-yaml volume")
	}

	// No PuppetDB wired up -> no routes.yaml mount (SubPath would fail on a missing key).
	cfgNoDB := newConfig("production")
	if routesMount(testBuildPodSpec(server, cfgNoDB)) != nil {
		t.Error("server pod should not mount routes-yaml when PuppetDB is not wired up")
	}
}

func TestBuildPodSpec_MultipleCodeVolumes(t *testing.T) {
	cfg := newConfig("production", withCodeList(
		openvoxv1alpha1.CodeSpec{Image: "ghcr.io/slauger/prod:latest", Environment: "production"},
		openvoxv1alpha1.CodeSpec{ClaimName: "modules", MountPath: "/etc/puppetlabs/code/modules"},
	))
	server := newServer("test-server", withServerRole(true))

	podSpec := testBuildPodSpec(server, cfg)

	mountByName := map[string]string{}
	for _, vm := range podSpec.Containers[0].VolumeMounts {
		mountByName[vm.Name] = vm.MountPath
	}

	// Environment entry -> <environmentpath>/production; mountPath entry -> absolute path.
	envPath := resolveEnvironmentPath(cfg)
	var codeMountPaths []string
	codeVolumes := 0
	for _, v := range podSpec.Volumes {
		if strings.HasPrefix(v.Name, "code") {
			codeVolumes++
			codeMountPaths = append(codeMountPaths, mountByName[v.Name])
		}
	}
	if codeVolumes != 2 {
		t.Fatalf("expected 2 code volumes, got %d (%v)", codeVolumes, codeMountPaths)
	}
	wantPaths := map[string]bool{
		envPath + "/production":        true,
		"/etc/puppetlabs/code/modules": true,
	}
	for _, p := range codeMountPaths {
		if !wantPaths[p] {
			t.Errorf("unexpected code mount path %q", p)
		}
	}
}

func TestBuildPodSpec_AutosignCommandSkipsPolicyMount(t *testing.T) {
	cfg := newConfig("production", withAutosignCommand("/usr/local/bin/custom-autosign"))
	server := newServer("test-ca", withCA(true), withServerRole(false))

	podSpec := testBuildPodSpec(server, cfg)

	for _, vm := range podSpec.Containers[0].VolumeMounts {
		if vm.Name == "autosign-policy" {
			t.Error("autosign-policy mount should be skipped when autosignCommand is set")
		}
	}
	for _, v := range podSpec.Volumes {
		if v.Name == "autosign-policy" {
			t.Error("autosign-policy volume should be skipped when autosignCommand is set")
		}
	}
}

func TestBuildPodSpec_ExternalNodesCommandSkipsENCMount(t *testing.T) {
	cfg := newConfig("production",
		withNodeClassifierRef(),
		withExternalNodesCommand("/usr/local/bin/custom-enc"),
	)
	server := newServer("test-server", withServerRole(true))

	podSpec := testBuildPodSpec(server, cfg)

	for _, vm := range podSpec.Containers[0].VolumeMounts {
		if vm.Name == "enc-config" {
			t.Error("enc-config mount should be skipped when externalNodesCommand is set")
		}
	}
	for _, v := range podSpec.Volumes {
		if v.Name == "enc-config" {
			t.Error("enc-config volume should be skipped when externalNodesCommand is set")
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
				return
			}
			if v.Image.Reference != "ghcr.io/slauger/puppet-code:v1.0" {
				t.Errorf("expected code image %q, got %q", "ghcr.io/slauger/puppet-code:v1.0", v.Image.Reference)
			}
			return
		}
	}
	t.Error("code volume not found")
}

// TestBuildPodSpec_CodeVolumeDefaultsMountPath is a regression test for #463:
// when spec.code is set but spec.puppet.environmentPath is empty (e.g. a
// hand-written Config that omits the whole spec.puppet block, so the CRD default
// is never applied), the code volume must still render a valid mountPath rather
// than "", which the Kubernetes API rejects.
func TestBuildPodSpec_CodeVolumeDefaultsMountPath(t *testing.T) {
	cfg := newConfig("production", withCodeImage("ghcr.io/slauger/puppet-code:v1.0"))
	cfg.Spec.Puppet.EnvironmentPath = ""
	server := newServer("test-server", withServerRole(true))

	podSpec := testBuildPodSpec(server, cfg)

	found := false
	for _, vm := range podSpec.Containers[0].VolumeMounts {
		if vm.Name == "code" {
			found = true
			if vm.MountPath != defaultEnvironmentPath {
				t.Errorf("expected code mountPath %q, got %q", defaultEnvironmentPath, vm.MountPath)
			}
		}
	}
	if !found {
		t.Fatal("code volume mount not found")
	}
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

	// Without a code source, a writable emptyDir is mounted at the environment
	// path so Puppetserver can bootstrap its default environment under a
	// read-only root filesystem.
	var codeVol *corev1.Volume
	for i := range podSpec.Volumes {
		if podSpec.Volumes[i].Name == "code" {
			codeVol = &podSpec.Volumes[i]
			break
		}
	}
	if codeVol == nil {
		t.Fatal("code volume not found")
	}
	if codeVol.EmptyDir == nil {
		t.Error("code volume should be an emptyDir when no code spec is set")
	}
	if codeVol.Image != nil || codeVol.PersistentVolumeClaim != nil {
		t.Error("code volume should not reference an image or PVC when no code spec is set")
	}

	var codeMount *corev1.VolumeMount
	for i := range podSpec.Containers[0].VolumeMounts {
		if podSpec.Containers[0].VolumeMounts[i].Name == "code" {
			codeMount = &podSpec.Containers[0].VolumeMounts[i]
			break
		}
	}
	if codeMount == nil {
		t.Fatal("code volume mount not found")
	}
	if codeMount.MountPath != cfg.Spec.Puppet.EnvironmentPath {
		t.Errorf("expected code mount path %q, got %q", cfg.Spec.Puppet.EnvironmentPath, codeMount.MountPath)
	}
	if codeMount.ReadOnly {
		t.Error("code emptyDir mount should be writable (not read-only)")
	}
}

func TestBuildPodSpec_NoCodeVolume_CAOnly(t *testing.T) {
	cfg := newConfig("production") // no code spec
	// CA-only pod (server:false) does not compile catalogs, so no code volume.
	server := newServer("test-server", withServerRole(false), withCA(true))

	podSpec := testBuildPodSpec(server, cfg)

	for _, v := range podSpec.Volumes {
		if v.Name == "code" {
			t.Error("CA-only pod should not have a code volume")
		}
	}
}

func TestBuildPodSpec_ReadOnlyRootFilesystem_Default(t *testing.T) {
	cfg := newConfig("production")
	server := newServer("test-server")

	podSpec := testBuildPodSpec(server, cfg)

	sc := podSpec.Containers[0].SecurityContext
	if sc == nil || sc.ReadOnlyRootFilesystem == nil || !*sc.ReadOnlyRootFilesystem {
		t.Error("readOnlyRootFilesystem should be true by default")
	}
}

func TestBuildPodSpec_ReadOnlyRootFilesystem_Disabled(t *testing.T) {
	cfg := newConfig("production", withReadOnlyRootFS(false))
	server := newServer("test-server")

	podSpec := testBuildPodSpec(server, cfg)

	sc := podSpec.Containers[0].SecurityContext
	if sc == nil || sc.ReadOnlyRootFilesystem == nil || *sc.ReadOnlyRootFilesystem {
		t.Error("readOnlyRootFilesystem should be false when explicitly disabled")
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
		return
	}
	if psc.RunAsUser == nil || *psc.RunAsUser != 1001 {
		t.Errorf("expected RunAsUser=1001, got %v", psc.RunAsUser)
	}
	if psc.RunAsNonRoot == nil || !*psc.RunAsNonRoot {
		t.Error("expected RunAsNonRoot=true")
	}
	if psc.FSGroup == nil || *psc.FSGroup != 1001 {
		t.Errorf("expected FSGroup=1001, got %v", psc.FSGroup)
	}
	if psc.FSGroupChangePolicy == nil || *psc.FSGroupChangePolicy != corev1.FSGroupChangeOnRootMismatch {
		t.Errorf("expected FSGroupChangePolicy=OnRootMismatch, got %v", psc.FSGroupChangePolicy)
	}
	if psc.SeccompProfile == nil || psc.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Error("expected Seccomp RuntimeDefault")
	}

	// Container-level security context
	csc := podSpec.Containers[0].SecurityContext
	if csc == nil {
		t.Fatal("container security context is nil")
		return
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

func TestBuildPodSpec_SecurityContextOverride(t *testing.T) {
	cfg := newConfig("production")
	server := newServer("test-server")

	user := int64(2000)
	fsGroup := int64(2000)
	server.Spec.SecurityContext = &openvoxv1alpha1.PodSecurityContextSpec{
		RunAsUser: &user,
		FSGroup:   &fsGroup,
	}

	podSpec := testBuildPodSpec(server, cfg)
	psc := podSpec.SecurityContext
	if psc == nil {
		t.Fatal("pod security context is nil")
		return
	}
	if psc.RunAsUser == nil || *psc.RunAsUser != 2000 {
		t.Errorf("expected overridden RunAsUser=2000, got %v", psc.RunAsUser)
	}
	if psc.FSGroup == nil || *psc.FSGroup != 2000 {
		t.Errorf("expected overridden FSGroup=2000, got %v", psc.FSGroup)
	}
	// Unset fields keep the defaults.
	if psc.RunAsGroup == nil || *psc.RunAsGroup != ServerRunAsGroup {
		t.Errorf("expected default RunAsGroup=%d, got %v", ServerRunAsGroup, psc.RunAsGroup)
	}
}

func TestBuildPodSpec_ENCVolumes(t *testing.T) {
	cfg := newConfig("production", withNodeClassifierRef())
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

func TestBuildPodSpec_PriorityClassName(t *testing.T) {
	cfg := newConfig("production")

	t.Run("set", func(t *testing.T) {
		server := newServer("test-server", withPriorityClassName("high-priority"))
		podSpec := testBuildPodSpec(server, cfg)
		if podSpec.PriorityClassName != "high-priority" {
			t.Errorf("expected PriorityClassName %q, got %q", "high-priority", podSpec.PriorityClassName)
		}
	})

	t.Run("empty by default", func(t *testing.T) {
		server := newServer("test-server")
		podSpec := testBuildPodSpec(server, cfg)
		if podSpec.PriorityClassName != "" {
			t.Errorf("expected empty PriorityClassName, got %q", podSpec.PriorityClassName)
		}
	})
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

func TestBuildPodSpec_ExtraEnvAndEnvFrom(t *testing.T) {
	cfg := newConfig("production")
	server := newServer("test-server",
		withExtraEnv(corev1.EnvVar{Name: "INVENTORY_API_URL", Value: "https://inventory.internal"}),
		withEnvFrom(corev1.EnvFromSource{
			SecretRef: &corev1.SecretEnvSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: "server-extra-env"},
			},
		}),
	)

	podSpec := testBuildPodSpec(server, cfg)
	container := podSpec.Containers[0]

	// JAVA_ARGS stays first; extra variables are appended after it.
	if len(container.Env) != 2 {
		t.Fatalf("container should have 2 env vars, got %d", len(container.Env))
	}
	if container.Env[0].Name != "JAVA_ARGS" {
		t.Errorf("first env var = %q, want JAVA_ARGS", container.Env[0].Name)
	}
	if container.Env[1].Name != "INVENTORY_API_URL" || container.Env[1].Value != "https://inventory.internal" {
		t.Errorf("extra env var not appended, got %+v", container.Env[1])
	}

	if len(container.EnvFrom) != 1 {
		t.Fatalf("container should have 1 envFrom source, got %d", len(container.EnvFrom))
	}
	if container.EnvFrom[0].SecretRef == nil || container.EnvFrom[0].SecretRef.Name != "server-extra-env" {
		t.Errorf("envFrom source not passed through, got %+v", container.EnvFrom[0])
	}
}

func TestBuildPodSpec_NoExtraEnv(t *testing.T) {
	cfg := newConfig("production")
	server := newServer("test-server")

	podSpec := testBuildPodSpec(server, cfg)
	container := podSpec.Containers[0]

	if len(container.Env) != 1 || container.Env[0].Name != "JAVA_ARGS" {
		t.Errorf("container env = %+v, want only JAVA_ARGS", container.Env)
	}
	if container.EnvFrom != nil {
		t.Errorf("container envFrom = %+v, want nil", container.EnvFrom)
	}
}

func TestBuildPodSpec_ExtraVolumesAndMounts(t *testing.T) {
	cfg := newConfig("production")
	server := newServer("test-server",
		withExtraVolumes(corev1.Volume{
			Name: "autosign-client",
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{SecretName: "autosign-client"},
			},
		}),
		withExtraVolumeMounts(corev1.VolumeMount{
			Name:      "autosign-client",
			MountPath: "/etc/puppetlabs/autosign-client",
			ReadOnly:  true,
		}),
	)

	podSpec := testBuildPodSpec(server, cfg)

	// The extra volume is appended last, so operator-managed volumes keep their position.
	lastVolume := podSpec.Volumes[len(podSpec.Volumes)-1]
	if lastVolume.Name != "autosign-client" {
		t.Errorf("last volume = %q, want autosign-client", lastVolume.Name)
	}
	if lastVolume.Secret == nil || lastVolume.Secret.SecretName != "autosign-client" {
		t.Errorf("extra volume source not passed through, got %+v", lastVolume.VolumeSource)
	}

	mounts := podSpec.Containers[0].VolumeMounts
	lastMount := mounts[len(mounts)-1]
	if lastMount.Name != "autosign-client" || lastMount.MountPath != "/etc/puppetlabs/autosign-client" {
		t.Errorf("last volume mount = %+v, want the autosign-client mount", lastMount)
	}
	if !lastMount.ReadOnly {
		t.Error("extra volume mount should keep readOnly: true")
	}

	// Extra mounts go to the main container only, not the tls-init container.
	for _, m := range podSpec.InitContainers[0].VolumeMounts {
		if m.Name == "autosign-client" {
			t.Error("extra volume mount leaked into the tls-init container")
		}
	}
}

func TestBuildPodSpec_ExtraVolumesOnCARole(t *testing.T) {
	cfg := newConfig("production")
	server := newServer("test-ca", withCA(true), withServerRole(false),
		withExtraVolumes(corev1.Volume{
			Name:         "scratch",
			VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}},
		}),
		withExtraVolumeMounts(corev1.VolumeMount{Name: "scratch", MountPath: "/scratch"}),
	)

	podSpec := testBuildPodSpec(server, cfg)

	// The CA pod runs the autosign binary, so it needs the same passthrough.
	found := false
	for _, v := range podSpec.Volumes {
		if v.Name == "scratch" {
			found = true
		}
	}
	if !found {
		t.Error("CA pod should have the extra volume")
	}
	found = false
	for _, m := range podSpec.Containers[0].VolumeMounts {
		if m.MountPath == "/scratch" {
			found = true
		}
	}
	if !found {
		t.Error("CA pod should have the extra volume mount")
	}
}
