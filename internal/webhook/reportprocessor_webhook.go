package webhook

import (
	"context"

	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// ReportProcessorValidator validates ReportProcessor resources.
type ReportProcessorValidator struct {
	Client client.Reader
}

func (v *ReportProcessorValidator) ValidateCreate(ctx context.Context, rp *openvoxv1alpha1.ReportProcessor) (admission.Warnings, error) {
	return v.validate(ctx, rp)
}

func (v *ReportProcessorValidator) ValidateUpdate(ctx context.Context, _, rp *openvoxv1alpha1.ReportProcessor) (admission.Warnings, error) {
	return v.validate(ctx, rp)
}

func (v *ReportProcessorValidator) ValidateDelete(_ context.Context, _ *openvoxv1alpha1.ReportProcessor) (admission.Warnings, error) {
	return nil, nil
}

func (v *ReportProcessorValidator) validate(ctx context.Context, rp *openvoxv1alpha1.ReportProcessor) (admission.Warnings, error) {
	var errs field.ErrorList
	specPath := field.NewPath("spec")

	if err := refExists(ctx, v.Client, rp.Namespace, rp.Spec.ConfigRef, &openvoxv1alpha1.Config{}); err != nil {
		errs = append(errs, field.Invalid(specPath.Child("configRef"), rp.Spec.ConfigRef, err.Error()))
	}

	if err := validateURL(rp.Spec.URL, "url"); err != nil {
		errs = append(errs, field.Invalid(specPath.Child("url"), rp.Spec.URL, err.Error()))
	}

	if len(errs) > 0 {
		return nil, errs.ToAggregate()
	}
	return nil, nil
}
