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

func (v *CertificateAuthorityValidator) ValidateUpdate(_ context.Context, old, ca *openvoxv1alpha1.CertificateAuthority) (admission.Warnings, error) {
	if err := validateStorageTransition(old, ca); err != nil {
		return nil, err
	}
	return v.validate(ca)
}

// validateStorageTransition rejects storage changes a PersistentVolumeClaim
// cannot follow.
//
// A PVC can grow if the storage class allows it, but it can never shrink and
// its class cannot change. Accepting such an edit would leave the reconcile
// failing against the API server on every attempt, with the cause several
// layers away from the field that caused it.
func validateStorageTransition(old, updated *openvoxv1alpha1.CertificateAuthority) error {
	// Admission always supplies the previous object; a nil one means there is
	// nothing to compare against.
	if old == nil || updated == nil {
		return nil
	}
	if old.Spec.Storage == nil || updated.Spec.Storage == nil {
		return nil
	}
	sizePath := field.NewPath("spec", "storage", "size")

	if old.Spec.Storage.Size != nil && updated.Spec.Storage.Size != nil {
		if updated.Spec.Storage.Size.Cmp(*old.Spec.Storage.Size) < 0 {
			return field.Invalid(sizePath, updated.Spec.Storage.Size.String(),
				"storage size cannot be decreased, the PersistentVolumeClaim would reject it")
		}
	}

	if old.Spec.Storage.StorageClass != updated.Spec.Storage.StorageClass {
		return field.Invalid(field.NewPath("spec", "storage", "storageClass"),
			updated.Spec.Storage.StorageClass,
			"storageClass is immutable on an existing PersistentVolumeClaim")
	}
	return nil
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
