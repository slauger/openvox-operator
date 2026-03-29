package webhook

import (
	"context"
	"fmt"
	"net/url"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// refExists checks whether a same-namespace resource of type T exists.
func refExists[T client.Object](ctx context.Context, c client.Reader, ns, name string, obj T) error {
	key := types.NamespacedName{Namespace: ns, Name: name}
	if err := c.Get(ctx, key, obj); err != nil {
		if errors.IsNotFound(err) {
			return fmt.Errorf("referenced %T %q not found", obj, name)
		}
		return fmt.Errorf("looking up %T %q: %w", obj, name, err)
	}
	return nil
}

// validateURL checks whether s is a valid URL with an http or https scheme.
func validateURL(s, fieldName string) error {
	u, err := url.Parse(s)
	if err != nil {
		return fmt.Errorf("%s: invalid URL %q: %w", fieldName, s, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%s: URL %q must use http or https scheme", fieldName, s)
	}
	if u.Host == "" {
		return fmt.Errorf("%s: URL %q must include a host", fieldName, s)
	}
	return nil
}

// validateDuration checks whether s parses as an OpenVox duration string.
func validateDuration(s, fieldName string) error {
	if s == "" {
		return nil
	}
	if _, err := openvoxv1alpha1.ParseDurationToSeconds(s); err != nil {
		return fmt.Errorf("%s: %w", fieldName, err)
	}
	return nil
}
