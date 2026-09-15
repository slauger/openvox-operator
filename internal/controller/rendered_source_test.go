package controller

import (
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestRenderedFromAnnotationRoundTrip(t *testing.T) {
	annotations := renderedFromAnnotation([]renderSource{
		{Name: "policy-b", Generation: 7},
		{Name: "policy-a", Generation: 2},
	})

	// Sorted, so an unchanged render produces an unchanged value and does not
	// rewrite the Secret.
	if got := annotations[AnnotationRenderedFrom]; got != "policy-a=2,policy-b=7" {
		t.Errorf("got %q, want the sources sorted by name", got)
	}

	if gen, ok := renderedGeneration(annotations, "policy-b"); !ok || gen != 7 {
		t.Errorf("got (%d, %v), want (7, true)", gen, ok)
	}
	if _, ok := renderedGeneration(annotations, "policy-c"); ok {
		t.Error("a resource that did not contribute must not report a generation")
	}

	// A CA with no policies still renders a file. The key is written empty
	// rather than left out, so a previous render's claim does not survive.
	empty := renderedFromAnnotation(nil)
	if _, ok := empty[AnnotationRenderedFrom]; !ok {
		t.Error("expected the key to be present for an empty source set")
	}
	if _, ok := renderedGeneration(empty, "policy-a"); ok {
		t.Error("an empty annotation must not report a generation")
	}

	if _, ok := renderedGeneration(nil, "policy-a"); ok {
		t.Error("a Secret the operator did not render must not report a generation")
	}
}

// Anything unparsable has to read as "this resource did not contribute", never
// as generation zero, which a resource could legitimately be at.
func TestRenderedGeneration_MalformedValues(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  int64
		found bool
	}{
		{"no separator", "my-enc", 0, false},
		{"generation is not a number", "my-enc=abc", 0, false},
		{"empty generation", "my-enc=", 0, false},
		{"empty value", "", 0, false},
		{"name only matches a prefix", "my-enc-2=4", 0, false},
		{"second entry matches", "other=1,my-enc=5", 5, true},
		{"trailing separator", "my-enc=5,", 5, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gen, ok := renderedGeneration(map[string]string{AnnotationRenderedFrom: tt.value}, "my-enc")
			if ok != tt.found || gen != tt.want {
				t.Errorf("renderedGeneration(%q) = (%d, %v), want (%d, %v)", tt.value, gen, ok, tt.want, tt.found)
			}
		})
	}
}

func TestRenderedSourceRequests(t *testing.T) {
	// Deliberately not testNamespace, so a hardcoded namespace would show up.
	const ns = "openvox-system"
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-ca-autosign-policy",
			Namespace: ns,
			Annotations: renderedFromAnnotation([]renderSource{
				{Name: "policy-a", Generation: 1},
				{Name: "policy-b", Generation: 1},
			}),
		},
	}
	requests := renderedSourceRequests(secret)
	if len(requests) != 2 || requests[0].Name != "policy-a" || requests[1].Name != "policy-b" {
		t.Fatalf("got %v, want a request per rendered source", requests)
	}
	for _, req := range requests {
		if req.Namespace != ns {
			t.Errorf("namespace = %q, want %q", req.Namespace, ns)
		}
	}

	// A Secret the operator did not render carries no annotation, so it maps to
	// nothing rather than fanning out over every resource in the namespace.
	foreign := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "vault-enc", Namespace: ns},
	}
	if got := renderedSourceRequests(foreign); len(got) != 0 {
		t.Errorf("got %v, want no requests for a Secret the operator did not render", got)
	}
}

// The two Secret watches share the annotation, so the name suffix is what keeps
// each controller to its own rendered file. Without it an autosign policy
// Secret would map its policy names onto NodeClassifier requests.
func TestSecretWatches_SuffixSelectsTheRightController(t *testing.T) {
	annotated := func(name string, sources ...renderSource) *corev1.Secret {
		return &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:        name,
				Namespace:   testNamespace,
				Annotations: renderedFromAnnotation(sources),
			},
		}
	}
	enc := annotated("production-enc", renderSource{Name: "my-enc", Generation: 1})
	autosign := annotated("test-ca-autosign-policy", renderSource{Name: "allow-all", Generation: 1})
	webhook := annotated("production-report-webhook", renderSource{Name: "beta", Generation: 1})

	ncFor := nodeClassifiersForSecret()
	spFor := signingPoliciesForSecret()

	if got := ncFor(testCtx(), enc); len(got) != 1 || got[0].Name != "my-enc" {
		t.Errorf("ENC Secret mapped to %v, want a request for my-enc", got)
	}
	if got := ncFor(testCtx(), autosign); len(got) != 0 {
		t.Errorf("autosign Secret mapped to NodeClassifier requests %v, want none", got)
	}
	if got := ncFor(testCtx(), webhook); len(got) != 0 {
		t.Errorf("report webhook Secret mapped to NodeClassifier requests %v, want none", got)
	}

	if got := spFor(testCtx(), autosign); len(got) != 1 || got[0].Name != "allow-all" {
		t.Errorf("autosign Secret mapped to %v, want a request for allow-all", got)
	}
	if got := spFor(testCtx(), enc); len(got) != 0 {
		t.Errorf("ENC Secret mapped to SigningPolicy requests %v, want none", got)
	}
	if got := spFor(testCtx(), webhook); len(got) != 0 {
		t.Errorf("report webhook Secret mapped to SigningPolicy requests %v, want none", got)
	}
}
