package controller

import (
	"sort"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// names extracts the sorted request names so assertions do not depend on
// listing order.
func names(reqs []ctrl.Request) []string {
	out := make([]string, 0, len(reqs))
	for _, r := range reqs {
		out = append(out, r.Name)
	}
	sort.Strings(out)
	return out
}

func equalNames(got []ctrl.Request, want ...string) bool {
	g := names(got)
	sort.Strings(want)
	if len(g) != len(want) {
		return false
	}
	for i := range g {
		if g[i] != want[i] {
			return false
		}
	}
	return true
}

// serverIn builds a Server wired to a specific Config and Certificate.
func serverIn(name, configRef, certRef string) *openvoxv1alpha1.Server {
	s := newServer(name)
	s.Spec.ConfigRef = configRef
	s.Spec.CertificateRef = certRef
	return s
}

func TestEnqueueServersForConfigObject(t *testing.T) {
	c := setupTestClient(
		serverIn("web-a", "production", "prod-cert"),
		serverIn("web-b", "production", "prod-cert"),
		serverIn("staging", "staging", "staging-cert"),
	)
	cfg := newConfig("production")

	got := enqueueServersForConfigObject(c)(testCtx(), cfg)
	if !equalNames(got, "web-a", "web-b") {
		t.Errorf("a Config change should reach exactly its own Servers, got %v", names(got))
	}
}

func TestEnqueueServersForCertificate(t *testing.T) {
	c := setupTestClient(
		serverIn("web-a", "production", "prod-cert"),
		serverIn("web-b", "production", "other-cert"),
	)
	cert := newCertificate("prod-cert", "production-ca", openvoxv1alpha1.CertificatePhaseSigned)

	got := enqueueServersForCertificate(c)(testCtx(), cert)
	if !equalNames(got, "web-a") {
		t.Errorf("only Servers mounting the Certificate should be enqueued, got %v", names(got))
	}
}

func TestEnqueueServersForCertificateAuthority(t *testing.T) {
	// Two Certificates from the same CA, and one Server behind each. The CA
	// change has to reach both, without duplicates.
	c := setupTestClient(
		newCertificate("cert-a", "production-ca", openvoxv1alpha1.CertificatePhaseSigned),
		newCertificate("cert-b", "production-ca", openvoxv1alpha1.CertificatePhaseSigned),
		newCertificate("cert-other", "other-ca", openvoxv1alpha1.CertificatePhaseSigned),
		serverIn("web-a", "production", "cert-a"),
		serverIn("web-b", "production", "cert-b"),
		serverIn("unrelated", "production", "cert-other"),
	)
	ca := newCertificateAuthority("production-ca")

	got := enqueueServersForCertificateAuthority(c)(testCtx(), ca)
	if !equalNames(got, "web-a", "web-b") {
		t.Errorf("a CA change should reach the Servers behind its Certificates, got %v", names(got))
	}
}

func TestEnqueueServersForCertificateAuthority_DeduplicatesSharedServer(t *testing.T) {
	// A Server can only reference one Certificate, but two Certificates of the
	// same CA must never produce the same request twice.
	c := setupTestClient(
		newCertificate("cert-a", "production-ca", openvoxv1alpha1.CertificatePhaseSigned),
		newCertificate("cert-b", "production-ca", openvoxv1alpha1.CertificatePhaseSigned),
		serverIn("web", "production", "cert-a"),
	)
	ca := newCertificateAuthority("production-ca")

	got := enqueueServersForCertificateAuthority(c)(testCtx(), ca)
	if len(got) != 1 {
		t.Errorf("expected exactly one request, got %v", names(got))
	}
}

func TestEnqueueConfigsForCertificateAuthority(t *testing.T) {
	c := setupTestClient(
		newConfig("production", withAuthorityRef("production-ca")),
		newConfig("staging", withAuthorityRef("staging-ca")),
	)
	r := newConfigReconciler(c)
	ca := newCertificateAuthority("production-ca")

	got := r.enqueueConfigsForCertificateAuthority(c)(testCtx(), ca)
	if len(got) != 1 || got[0].Name != "production" {
		t.Errorf("only Configs referencing the CA should be enqueued, got %v", got)
	}
}

func TestEnqueueConfigsForCertificateAuthority_CreatedAfterConfig(t *testing.T) {
	// The gap from #508: the CA is created after the Config, so nothing had
	// re-rendered the Config until some unrelated event happened.
	c := setupTestClient(newConfig("production", withAuthorityRef("late-ca")))
	r := newConfigReconciler(c)

	got := r.enqueueConfigsForCertificateAuthority(c)(testCtx(), newCertificateAuthority("late-ca"))
	if len(got) != 1 || got[0].Name != "production" {
		t.Errorf("a CA appearing after its Config must trigger a re-render, got %v", got)
	}
}

func databaseWith(name, certRef, pgSecret string) *openvoxv1alpha1.Database {
	return &openvoxv1alpha1.Database{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: testNamespace},
		Spec: openvoxv1alpha1.DatabaseSpec{
			CertificateRef: certRef,
			Postgres:       openvoxv1alpha1.PostgresSpec{CredentialsSecretRef: pgSecret},
		},
	}
}

func TestEnqueueDatabasesForCertificate(t *testing.T) {
	c := setupTestClient(
		databaseWith("puppetdb", "db-cert", "pg-creds"),
		databaseWith("other", "other-cert", "pg-creds"),
	)
	cert := newCertificate("db-cert", "production-ca", openvoxv1alpha1.CertificatePhaseSigned)

	got := enqueueDatabasesForCertificate(c)(testCtx(), cert)
	if !equalNames(got, "puppetdb") {
		t.Errorf("only Databases mounting the Certificate should be enqueued, got %v", names(got))
	}
}

func TestEnqueueDatabasesForSecret(t *testing.T) {
	c := setupTestClient(
		databaseWith("puppetdb", "db-cert", "pg-creds"),
		newCertificate("db-cert", "production-ca", openvoxv1alpha1.CertificatePhaseSigned),
	)
	mapFn := databasesForSecret(c)

	managed := func(name string, labels map[string]string) client.Object {
		if labels == nil {
			labels = map[string]string{}
		}
		labels["app.kubernetes.io/managed-by"] = "openvox-operator"
		return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: testNamespace, Labels: labels,
		}}
	}

	t.Run("postgres credentials", func(t *testing.T) {
		got := mapFn(testCtx(), &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
			Name: "pg-creds", Namespace: testNamespace,
		}})
		if !equalNames(got, "puppetdb") {
			t.Errorf("a credentials change must reach the Database, got %v", names(got))
		}
	})

	t.Run("tls secret of the mounted certificate", func(t *testing.T) {
		got := mapFn(testCtx(), managed("db-cert-tls", map[string]string{
			"openvox.voxpupuli.org/certificate": "db-cert",
		}))
		if !equalNames(got, "puppetdb") {
			t.Errorf("a TLS Secret rotation must roll the Database, got %v", names(got))
		}
	})

	t.Run("ca secret resolves over the certificate", func(t *testing.T) {
		got := mapFn(testCtx(), managed("production-ca-ca", map[string]string{
			LabelCertificateAuthority: "production-ca",
		}))
		if !equalNames(got, "puppetdb") {
			t.Errorf("a CA Secret change must reach Databases behind that CA, got %v", names(got))
		}
	})

	t.Run("unrelated secret is ignored", func(t *testing.T) {
		got := mapFn(testCtx(), &corev1.Secret{ObjectMeta: metav1.ObjectMeta{
			Name: "unrelated", Namespace: testNamespace,
		}})
		if len(got) != 0 {
			t.Errorf("an unrelated Secret must not enqueue anything, got %v", names(got))
		}
	})
}
