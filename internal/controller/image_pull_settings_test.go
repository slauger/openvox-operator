package controller

import (
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// serverPrereqsWith returns the standard Server prerequisites with a
// caller-supplied Config, so a test can vary the image settings.
func serverPrereqsWith(cfg *openvoxv1alpha1.Config) []client.Object {
	objs := serverPrereqs()
	for i := range objs {
		if _, ok := objs[i].(*openvoxv1alpha1.Config); ok {
			objs[i] = cfg
			return objs
		}
	}
	panic("serverPrereqs no longer contains a Config")
}

// pullSecretNames flattens the pod's pull secrets for comparison.
func pullSecretNames(refs []corev1.LocalObjectReference) []string {
	out := make([]string, 0, len(refs))
	for _, r := range refs {
		out = append(out, r.Name)
	}
	return out
}

func equalStrings(got []string, want ...string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

// TestPullSecrets_ReachTheServerPod covers a field that was declared on
// ImageSpec but read nowhere: pull secrets configured on the Config never
// reached any pod, so a private registry could not work at all.
func TestPullSecrets_ReachTheServerPod(t *testing.T) {
	cfg := newConfig("production", withAuthorityRef("production-ca"))
	cfg.Spec.Image.PullSecrets = []corev1.LocalObjectReference{{Name: "registry-creds"}}
	server := newServer("test-server")

	c := setupTestClient(append(serverPrereqsWith(cfg), server)...)
	r := newServerReconciler(c)
	if _, err := r.Reconcile(testCtx(), testRequest("test-server")); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	deploy := &appsv1.Deployment{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-server", Namespace: testNamespace}, deploy); err != nil {
		t.Fatalf("reading the Deployment: %v", err)
	}
	if got := pullSecretNames(deploy.Spec.Template.Spec.ImagePullSecrets); !equalStrings(got, "registry-creds") {
		t.Errorf("expected the Config's pull secret on the pod, got %v", got)
	}
}

// TestPullSecrets_ServerOverridesConfig pins the documented precedence: a list
// on the Server replaces the Config's rather than extending it.
func TestPullSecrets_ServerOverridesConfig(t *testing.T) {
	cfg := newConfig("production", withAuthorityRef("production-ca"))
	cfg.Spec.Image.PullSecrets = []corev1.LocalObjectReference{{Name: "config-creds"}}
	server := newServer("test-server")
	server.Spec.Image.PullSecrets = []corev1.LocalObjectReference{{Name: "server-creds"}}

	c := setupTestClient(append(serverPrereqsWith(cfg), server)...)
	r := newServerReconciler(c)
	if _, err := r.Reconcile(testCtx(), testRequest("test-server")); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	deploy := &appsv1.Deployment{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-server", Namespace: testNamespace}, deploy); err != nil {
		t.Fatalf("reading the Deployment: %v", err)
	}
	if got := pullSecretNames(deploy.Spec.Template.Spec.ImagePullSecrets); !equalStrings(got, "server-creds") {
		t.Errorf("expected the Server's list to replace the Config's, got %v", got)
	}
}

// TestPullPolicy_ServerOverridesConfig covers the second half of the same
// problem: the Server's pull policy was read from the Config unconditionally,
// so the field on the Server did nothing.
func TestPullPolicy_ServerOverridesConfig(t *testing.T) {
	cfg := newConfig("production", withAuthorityRef("production-ca"))
	cfg.Spec.Image.PullPolicy = corev1.PullIfNotPresent
	server := newServer("test-server")
	server.Spec.Image.PullPolicy = corev1.PullAlways

	c := setupTestClient(append(serverPrereqsWith(cfg), server)...)
	r := newServerReconciler(c)
	if _, err := r.Reconcile(testCtx(), testRequest("test-server")); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	deploy := &appsv1.Deployment{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-server", Namespace: testNamespace}, deploy); err != nil {
		t.Fatalf("reading the Deployment: %v", err)
	}
	for _, ctr := range deploy.Spec.Template.Spec.Containers {
		if ctr.ImagePullPolicy != corev1.PullAlways {
			t.Errorf("container %s: expected Always from the Server, got %q", ctr.Name, ctr.ImagePullPolicy)
		}
	}
}

// TestPullPolicy_InheritsFromConfig is the counterpart: an unset Server policy
// must still take the Config's value.
func TestPullPolicy_InheritsFromConfig(t *testing.T) {
	cfg := newConfig("production", withAuthorityRef("production-ca"))
	cfg.Spec.Image.PullPolicy = corev1.PullAlways
	server := newServer("test-server") // no pull policy of its own

	c := setupTestClient(append(serverPrereqsWith(cfg), server)...)
	r := newServerReconciler(c)
	if _, err := r.Reconcile(testCtx(), testRequest("test-server")); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	deploy := &appsv1.Deployment{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-server", Namespace: testNamespace}, deploy); err != nil {
		t.Fatalf("reading the Deployment: %v", err)
	}
	for _, ctr := range deploy.Spec.Template.Spec.Containers {
		if ctr.ImagePullPolicy != corev1.PullAlways {
			t.Errorf("container %s: expected Always inherited from the Config, got %q", ctr.Name, ctr.ImagePullPolicy)
		}
	}
}

// TestPullPolicy_DefaultsWhenUnsetEverywhere guards the fallback that replaced
// the CRD default. The default had to go: materialised into every Server, it
// made the field never empty and the override unreachable.
func TestPullPolicy_DefaultsWhenUnsetEverywhere(t *testing.T) {
	cfg := newConfig("production", withAuthorityRef("production-ca"))
	server := newServer("test-server")

	if got := resolveImagePullPolicy(server, cfg); got != corev1.PullIfNotPresent {
		t.Errorf("expected IfNotPresent when nothing is set, got %q", got)
	}
}

// TestPullSecrets_ReachTheDatabasePod and the CA job cover the other two
// workloads that build pods from an ImageSpec.
func TestPullSecrets_ReachTheDatabasePod(t *testing.T) {
	db := newDatabase("puppetdb")
	db.Spec.Image.PullSecrets = []corev1.LocalObjectReference{{Name: "registry-creds"}}

	cert := newCertificate("puppetdb-cert", "production-ca", openvoxv1alpha1.CertificatePhaseSigned)
	ca := newCertificateAuthority("production-ca")
	r := newDatabaseReconciler(setupTestClient(db, cert, ca))
	podSpec := r.buildPodSpec(db, cert, ca, "example/db:1")
	if got := pullSecretNames(podSpec.ImagePullSecrets); !equalStrings(got, "registry-creds") {
		t.Errorf("expected the pull secret on the Database pod, got %v", got)
	}
}

// TestSSLBootstrapped_ReportsAMissingCertificate covers a condition that was
// declared, documented in docs/reference/server.md, and never set. A Server
// waiting for its Certificate left no trace in the status at all.
func TestSSLBootstrapped_ReportsAMissingCertificate(t *testing.T) {
	cfg := newConfig("production", withAuthorityRef("production-ca"))
	server := newServer("test-server") // certificateRef points at a Certificate that does not exist

	c := setupTestClient(cfg, server)
	r := newServerReconciler(c)
	if _, err := r.Reconcile(testCtx(), testRequest("test-server")); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	got := &openvoxv1alpha1.Server{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-server", Namespace: testNamespace}, got); err != nil {
		t.Fatalf("reading the Server back: %v", err)
	}
	cond := meta.FindStatusCondition(got.Status.Conditions, openvoxv1alpha1.ConditionSSLBootstrapped)
	if cond == nil {
		t.Fatal("expected an SSLBootstrapped condition while the Certificate is missing")
	}
	if cond.Status != metav1.ConditionFalse || cond.Reason != "CertificateNotFound" {
		t.Errorf("expected False/CertificateNotFound, got %s/%s", cond.Status, cond.Reason)
	}
}

// TestSSLBootstrapped_ReportsAnUnsignedCertificate is the second waiting state.
func TestSSLBootstrapped_ReportsAnUnsignedCertificate(t *testing.T) {
	cfg := newConfig("production", withAuthorityRef("production-ca"))
	cert := newCertificate("production-cert", "production-ca", openvoxv1alpha1.CertificatePhasePending)
	server := newServer("test-server")

	c := setupTestClient(cfg, cert, server)
	r := newServerReconciler(c)
	if _, err := r.Reconcile(testCtx(), testRequest("test-server")); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	got := &openvoxv1alpha1.Server{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-server", Namespace: testNamespace}, got); err != nil {
		t.Fatalf("reading the Server back: %v", err)
	}
	cond := meta.FindStatusCondition(got.Status.Conditions, openvoxv1alpha1.ConditionSSLBootstrapped)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != "CertificateNotSigned" {
		t.Errorf("expected False/CertificateNotSigned, got %+v", cond)
	}
}

// TestSSLBootstrapped_TrueOnceSigned closes the loop.
func TestSSLBootstrapped_TrueOnceSigned(t *testing.T) {
	server := newServer("test-server")
	c := setupTestClient(append(serverPrereqs(), server)...)
	r := newServerReconciler(c)
	if _, err := r.Reconcile(testCtx(), testRequest("test-server")); err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	got := &openvoxv1alpha1.Server{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "test-server", Namespace: testNamespace}, got); err != nil {
		t.Fatalf("reading the Server back: %v", err)
	}
	cond := meta.FindStatusCondition(got.Status.Conditions, openvoxv1alpha1.ConditionSSLBootstrapped)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Errorf("expected SSLBootstrapped=True once the Certificate is signed, got %+v", cond)
	}
}

// TestPullSecrets_ReachTheCASetupJob covers the third workload.
func TestPullSecrets_ReachTheCASetupJob(t *testing.T) {
	ca := newCertificateAuthority("test-ca")
	cfg := caPrereqs("test-ca")
	cfg.Spec.Image.PullSecrets = []corev1.LocalObjectReference{{Name: "registry-creds"}}

	r := newCertificateAuthorityReconciler(setupTestClient(ca, cfg))
	job := r.buildCASetupJob(testCtx(), ca, cfg, "test-ca-setup", nil)

	if got := pullSecretNames(job.Spec.Template.Spec.ImagePullSecrets); !equalStrings(got, "registry-creds") {
		t.Errorf("expected the pull secret on the CA setup job, got %v", got)
	}
}
