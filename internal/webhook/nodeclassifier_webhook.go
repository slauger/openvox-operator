package webhook

import (
	"context"

	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// NodeClassifierValidator validates NodeClassifier resources.
type NodeClassifierValidator struct{}

func (v *NodeClassifierValidator) ValidateCreate(_ context.Context, nc *openvoxv1alpha1.NodeClassifier) (admission.Warnings, error) {
	return v.validate(nc)
}

func (v *NodeClassifierValidator) ValidateUpdate(_ context.Context, _, nc *openvoxv1alpha1.NodeClassifier) (admission.Warnings, error) {
	return v.validate(nc)
}

func (v *NodeClassifierValidator) ValidateDelete(_ context.Context, _ *openvoxv1alpha1.NodeClassifier) (admission.Warnings, error) {
	return nil, nil
}

func (v *NodeClassifierValidator) validate(nc *openvoxv1alpha1.NodeClassifier) (admission.Warnings, error) {
	var errs field.ErrorList
	specPath := field.NewPath("spec")

	if err := validateURL(nc.Spec.URL, "url"); err != nil {
		errs = append(errs, field.Invalid(specPath.Child("url"), nc.Spec.URL, err.Error()))
	}

	if len(errs) > 0 {
		return nil, errs.ToAggregate()
	}
	return nil, nil
}
