package webhook

import (
	"context"

	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// ConfigValidator validates Config resources.
type ConfigValidator struct {
	Client client.Reader
}

func (v *ConfigValidator) ValidateCreate(ctx context.Context, c *openvoxv1alpha1.Config) (admission.Warnings, error) {
	return v.validate(ctx, c)
}

func (v *ConfigValidator) ValidateUpdate(ctx context.Context, _, c *openvoxv1alpha1.Config) (admission.Warnings, error) {
	return v.validate(ctx, c)
}

func (v *ConfigValidator) ValidateDelete(_ context.Context, _ *openvoxv1alpha1.Config) (admission.Warnings, error) {
	return nil, nil
}

func (v *ConfigValidator) validate(ctx context.Context, c *openvoxv1alpha1.Config) (admission.Warnings, error) {
	var errs field.ErrorList
	specPath := field.NewPath("spec")

	if c.Spec.AuthorityRef != "" {
		if err := refExists(ctx, v.Client, c.Namespace, c.Spec.AuthorityRef, &openvoxv1alpha1.CertificateAuthority{}); err != nil {
			errs = append(errs, field.Invalid(specPath.Child("authorityRef"), c.Spec.AuthorityRef, err.Error()))
		}
	}

	if c.Spec.NodeClassifierRef != "" {
		if err := refExists(ctx, v.Client, c.Namespace, c.Spec.NodeClassifierRef, &openvoxv1alpha1.NodeClassifier{}); err != nil {
			errs = append(errs, field.Invalid(specPath.Child("nodeClassifierRef"), c.Spec.NodeClassifierRef, err.Error()))
		}
	}

	if len(errs) > 0 {
		return nil, errs.ToAggregate()
	}
	return nil, nil
}
