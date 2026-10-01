package v1alpha1

import (
	"context"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestServerJavaArgsHasNoDefault is the test that was missing while the bug
// existed. The unit tests around resolveJavaArgs all call the function
// directly and therefore pass whether or not the field can ever be empty; only
// a round-trip through the API server shows that.
//
// A default here is not cosmetic: the controller derives the heap from the
// pod's memory limit exactly when javaArgs is empty, so a default silently
// pins every Server to the same heap.
func TestServerJavaArgsHasNoDefault(t *testing.T) {
	ctx := context.Background()

	server := &Server{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "test-server-", Namespace: "default"},
		Spec: ServerSpec{
			ConfigRef:      "production",
			CertificateRef: "production-cert",
		},
	}
	if err := k8sClient.Create(ctx, server); err != nil {
		t.Fatalf("creating Server: %v", err)
	}
	t.Cleanup(func() { _ = k8sClient.Delete(ctx, server) })

	if server.Spec.JavaArgs != "" {
		t.Errorf("javaArgs must come back empty so the heap can be derived, got %q", server.Spec.JavaArgs)
	}
}
