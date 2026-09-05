package webhook

import (
	"context"
	"net"

	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
	"github.com/slauger/openvox-operator/internal/puppet"
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

	if sp.Spec.Certnames != nil {
		for i, pattern := range sp.Spec.Certnames.Allow {
			if pattern == "" {
				errs = append(errs, field.Invalid(specPath.Child("certnames", "allow").Index(i), pattern, "certname pattern must not be empty"))
			}
		}
	}

	for _, f := range []struct {
		name string
		spec *openvoxv1alpha1.PatternSpec
	}{
		{"dnsAltNames", sp.Spec.DNSAltNames},
		{"uriAltNames", sp.Spec.URIAltNames},
		{"emailAltNames", sp.Spec.EmailAltNames},
	} {
		if f.spec == nil {
			continue
		}
		for i, pattern := range f.spec.Allow {
			if pattern == "" {
				errs = append(errs, field.Invalid(specPath.Child(f.name, "allow").Index(i), pattern, "pattern must not be empty"))
			}
		}
	}

	if sp.Spec.IPAltNames != nil {
		for i, cidr := range sp.Spec.IPAltNames.Allow {
			if _, _, err := net.ParseCIDR(cidr); err != nil {
				errs = append(errs, field.Invalid(specPath.Child("ipAltNames", "allow").Index(i), cidr, "must be a valid CIDR, e.g. 10.0.0.0/16"))
			}
		}
	}

	// Extensions listed here can only take effect if they are known Puppet OIDs
	// (the autosign guard resolves CSR extensions to names). Reject unknown names
	// early so a typo doesn't silently fail to allow a privileged extension.
	if sp.Spec.Extensions != nil {
		for i, name := range sp.Spec.Extensions.Allow {
			extPath := specPath.Child("extensions", "allow").Index(i)
			switch {
			case name == "":
				errs = append(errs, field.Invalid(extPath, name, "extension name must not be empty"))
			case !puppet.IsKnownOID(name):
				errs = append(errs, field.Invalid(extPath, name, "unknown Puppet extension name"))
			}
		}
	}

	// The autosign binary only matches attributes with a known Puppet OID; an
	// unknown name would never match. Enforcing the allowlist here also blocks
	// YAML-injection payloads via the free-form name field (defense in depth to
	// the %q-quoted renderer).
	for i, attr := range sp.Spec.CSRAttributes {
		namePath := specPath.Child("csrAttributes").Index(i).Child("name")
		switch {
		case attr.Name == "":
			errs = append(errs, field.Invalid(namePath, attr.Name, "name must not be empty"))
		case !puppet.IsKnownOID(attr.Name):
			errs = append(errs, field.Invalid(namePath, attr.Name, "unknown Puppet extension name"))
		}
	}

	if len(errs) > 0 {
		return nil, errs.ToAggregate()
	}
	return nil, nil
}
