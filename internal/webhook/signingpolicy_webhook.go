package webhook

import (
	"context"

	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// SigningPolicyValidator validates SigningPolicy resources.
type SigningPolicyValidator struct {
	Client client.Reader
}

func (v *SigningPolicyValidator) ValidateCreate(ctx context.Context, sp *openvoxv1alpha1.SigningPolicy) (admission.Warnings, error) {
	return v.validate(ctx, sp)
}

func (v *SigningPolicyValidator) ValidateUpdate(ctx context.Context, _, sp *openvoxv1alpha1.SigningPolicy) (admission.Warnings, error) {
	return v.validate(ctx, sp)
}

func (v *SigningPolicyValidator) ValidateDelete(_ context.Context, _ *openvoxv1alpha1.SigningPolicy) (admission.Warnings, error) {
	return nil, nil
}

func (v *SigningPolicyValidator) validate(ctx context.Context, sp *openvoxv1alpha1.SigningPolicy) (admission.Warnings, error) {
	var errs field.ErrorList
	specPath := field.NewPath("spec")

	if err := refExists(ctx, v.Client, sp.Namespace, sp.Spec.CertificateAuthorityRef, &openvoxv1alpha1.CertificateAuthority{}); err != nil {
		errs = append(errs, field.Invalid(specPath.Child("certificateAuthorityRef"), sp.Spec.CertificateAuthorityRef, err.Error()))
	}

	if sp.Spec.Pattern != nil {
		for i, pattern := range sp.Spec.Pattern.Allow {
			if pattern == "" {
				errs = append(errs, field.Invalid(specPath.Child("pattern", "allow").Index(i), pattern, "pattern must not be empty"))
			}
		}
	}

	if sp.Spec.DNSAltNames != nil {
		for i, pattern := range sp.Spec.DNSAltNames.Allow {
			if pattern == "" {
				errs = append(errs, field.Invalid(specPath.Child("dnsAltNames", "allow").Index(i), pattern, "pattern must not be empty"))
			}
		}
	}

	if len(errs) > 0 {
		return nil, errs.ToAggregate()
	}
	return nil, nil
}
