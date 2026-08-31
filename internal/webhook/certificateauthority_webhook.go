package webhook

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// CertificateAuthorityValidator validates CertificateAuthority resources.
type CertificateAuthorityValidator struct {
	Client client.Reader
}

func (v *CertificateAuthorityValidator) ValidateCreate(_ context.Context, ca *openvoxv1alpha1.CertificateAuthority) (admission.Warnings, error) {
	return v.validate(ca)
}

func (v *CertificateAuthorityValidator) ValidateUpdate(_ context.Context, _, ca *openvoxv1alpha1.CertificateAuthority) (admission.Warnings, error) {
	return v.validate(ca)
}

// ValidateDelete refuses to delete a CertificateAuthority while Certificates
// still reference it, and warns about the consequences otherwise.
//
// The controller's finalizer is the actual safeguard -- it also holds when the
// webhooks are disabled. This check exists so the user is told immediately
// instead of watching the resource sit in Terminating.
func (v *CertificateAuthorityValidator) ValidateDelete(ctx context.Context, ca *openvoxv1alpha1.CertificateAuthority) (admission.Warnings, error) {
	if v.Client == nil {
		return nil, nil
	}

	certList := &openvoxv1alpha1.CertificateList{}
	if err := v.Client.List(ctx, certList, client.InNamespace(ca.Namespace)); err != nil {
		return nil, fmt.Errorf("listing Certificates for CertificateAuthority %s: %w", ca.Name, err)
	}

	var blocking []string
	for i := range certList.Items {
		if certList.Items[i].Spec.AuthorityRef == ca.Name {
			blocking = append(blocking, certList.Items[i].Name)
		}
	}
	if len(blocking) > 0 {
		sort.Strings(blocking)
		return nil, fmt.Errorf(
			"cannot delete CertificateAuthority %s: %d Certificate(s) still reference it (%s); delete those first",
			ca.Name, len(blocking), strings.Join(blocking, ", "))
	}

	return admission.Warnings{
		"deleting this CertificateAuthority destroys the CA private key and the CA data PVC; this cannot be undone",
	}, nil
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

	if len(errs) > 0 {
		return nil, errs.ToAggregate()
	}
	return nil, nil
}
