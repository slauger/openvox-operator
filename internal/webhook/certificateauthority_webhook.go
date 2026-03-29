package webhook

import (
	"context"

	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// CertificateAuthorityValidator validates CertificateAuthority resources.
type CertificateAuthorityValidator struct{}

func (v *CertificateAuthorityValidator) ValidateCreate(_ context.Context, ca *openvoxv1alpha1.CertificateAuthority) (admission.Warnings, error) {
	return v.validate(ca)
}

func (v *CertificateAuthorityValidator) ValidateUpdate(_ context.Context, _, ca *openvoxv1alpha1.CertificateAuthority) (admission.Warnings, error) {
	return v.validate(ca)
}

func (v *CertificateAuthorityValidator) ValidateDelete(_ context.Context, _ *openvoxv1alpha1.CertificateAuthority) (admission.Warnings, error) {
	return nil, nil
}

func (v *CertificateAuthorityValidator) validate(ca *openvoxv1alpha1.CertificateAuthority) (admission.Warnings, error) {
	var errs field.ErrorList
	specPath := field.NewPath("spec")

	if err := validateDuration(ca.Spec.TTL, "ttl"); err != nil {
		errs = append(errs, field.Invalid(specPath.Child("ttl"), ca.Spec.TTL, err.Error()))
	}

	if err := validateDuration(ca.Spec.AutoRenewalCertTTL, "autoRenewalCertTTL"); err != nil {
		errs = append(errs, field.Invalid(specPath.Child("autoRenewalCertTTL"), ca.Spec.AutoRenewalCertTTL, err.Error()))
	}

	if err := validateDuration(ca.Spec.CRLRefreshInterval, "crlRefreshInterval"); err != nil {
		errs = append(errs, field.Invalid(specPath.Child("crlRefreshInterval"), ca.Spec.CRLRefreshInterval, err.Error()))
	}

	if ca.Spec.Storage.Size != "" {
		if _, err := resource.ParseQuantity(ca.Spec.Storage.Size); err != nil {
			errs = append(errs, field.Invalid(specPath.Child("storage", "size"), ca.Spec.Storage.Size, "must be a valid Kubernetes quantity (e.g. 1Gi, 500Mi)"))
		}
	}

	if len(errs) > 0 {
		return nil, errs.ToAggregate()
	}
	return nil, nil
}
