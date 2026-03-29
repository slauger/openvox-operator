package webhook

import (
	"context"
	"fmt"

	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// ServerValidator validates Server resources.
type ServerValidator struct {
	Client client.Reader
}

func (v *ServerValidator) ValidateCreate(ctx context.Context, s *openvoxv1alpha1.Server) (admission.Warnings, error) {
	return v.validate(ctx, s)
}

func (v *ServerValidator) ValidateUpdate(ctx context.Context, _, s *openvoxv1alpha1.Server) (admission.Warnings, error) {
	return v.validate(ctx, s)
}

func (v *ServerValidator) ValidateDelete(_ context.Context, _ *openvoxv1alpha1.Server) (admission.Warnings, error) {
	return nil, nil
}

func (v *ServerValidator) validate(ctx context.Context, s *openvoxv1alpha1.Server) (admission.Warnings, error) {
	var errs field.ErrorList
	var warnings admission.Warnings
	specPath := field.NewPath("spec")

	if err := refExists(ctx, v.Client, s.Namespace, s.Spec.ConfigRef, &openvoxv1alpha1.Config{}); err != nil {
		errs = append(errs, field.Invalid(specPath.Child("configRef"), s.Spec.ConfigRef, err.Error()))
	}

	if err := refExists(ctx, v.Client, s.Namespace, s.Spec.CertificateRef, &openvoxv1alpha1.Certificate{}); err != nil {
		errs = append(errs, field.Invalid(specPath.Child("certificateRef"), s.Spec.CertificateRef, err.Error()))
	}

	for i, ref := range s.Spec.PoolRefs {
		if err := refExists(ctx, v.Client, s.Namespace, ref, &openvoxv1alpha1.Pool{}); err != nil {
			errs = append(errs, field.Invalid(specPath.Child("poolRefs").Index(i), ref, err.Error()))
		}
	}

	if s.Spec.CA && s.Spec.Replicas != nil && *s.Spec.Replicas > 1 {
		warnings = append(warnings, fmt.Sprintf("running CA role with %d replicas; CA data is stored on a PVC and concurrent writes may cause issues", *s.Spec.Replicas))
	}

	if len(errs) > 0 {
		return warnings, errs.ToAggregate()
	}
	return warnings, nil
}
