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

func TestRenderedSourceRequests(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "test-ca-autosign-policy",
			Namespace:   testNamespace,
			Annotations: renderedFromAnnotation([]renderSource{{Name: "policy-a", Generation: 1}, {Name: "policy-b", Generation: 1}}),
		},
	}
	requests := renderedSourceRequests(secret)
	if len(requests) != 2 || requests[0].Name != "policy-a" || requests[1].Name != "policy-b" {
		t.Errorf("got %v, want a request per rendered source", requests)
	}
	if requests[0].Namespace != testNamespace {
		t.Errorf("namespace = %q, want %q", requests[0].Namespace, testNamespace)
	}

	// An unrelated Secret that merely happens to match the name suffix carries
	// no annotation, and must not fan out over every resource in the namespace.
	foreign := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "vault-enc", Namespace: testNamespace},
	}
	if got := renderedSourceRequests(foreign); len(got) != 0 {
		t.Errorf("got %v, want no requests for a Secret the operator did not render", got)
	}
}
