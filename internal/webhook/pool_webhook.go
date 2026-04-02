package webhook

import (
	"context"

	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// PoolValidator validates Pool resources.
type PoolValidator struct{}

func (v *PoolValidator) ValidateCreate(_ context.Context, p *openvoxv1alpha1.Pool) (admission.Warnings, error) {
	return v.validate(p)
}

func (v *PoolValidator) ValidateUpdate(_ context.Context, _, p *openvoxv1alpha1.Pool) (admission.Warnings, error) {
	return v.validate(p)
}

func (v *PoolValidator) ValidateDelete(_ context.Context, _ *openvoxv1alpha1.Pool) (admission.Warnings, error) {
	return nil, nil
}

func (v *PoolValidator) validate(p *openvoxv1alpha1.Pool) (admission.Warnings, error) {
	var errs field.ErrorList
	specPath := field.NewPath("spec")

	if p.Spec.Service.Port != 0 && (p.Spec.Service.Port < 1 || p.Spec.Service.Port > 65535) {
		errs = append(errs, field.Invalid(specPath.Child("service", "port"), p.Spec.Service.Port, "must be between 1 and 65535"))
	}

	if len(errs) > 0 {
		return nil, errs.ToAggregate()
	}
	return nil, nil
}
