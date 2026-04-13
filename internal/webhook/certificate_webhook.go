package webhook

import (
	"context"
	"net"
	"strings"

	"k8s.io/apimachinery/pkg/util/validation"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
	"github.com/slauger/openvox-operator/internal/puppet"
)

// CertificateValidator validates Certificate resources.
type CertificateValidator struct {
	Client client.Reader
}

func (v *CertificateValidator) ValidateCreate(ctx context.Context, c *openvoxv1alpha1.Certificate) (admission.Warnings, error) {
	return v.validate(ctx, c)
}

func (v *CertificateValidator) ValidateUpdate(ctx context.Context, _, c *openvoxv1alpha1.Certificate) (admission.Warnings, error) {
	return v.validate(ctx, c)
}

func (v *CertificateValidator) ValidateDelete(_ context.Context, _ *openvoxv1alpha1.Certificate) (admission.Warnings, error) {
	return nil, nil
}

func (v *CertificateValidator) validate(ctx context.Context, c *openvoxv1alpha1.Certificate) (admission.Warnings, error) {
	var errs field.ErrorList
	specPath := field.NewPath("spec")

	if err := refExists(ctx, v.Client, c.Namespace, c.Spec.AuthorityRef, &openvoxv1alpha1.CertificateAuthority{}); err != nil {
		errs = append(errs, field.Invalid(specPath.Child("authorityRef"), c.Spec.AuthorityRef, err.Error()))
	}

	if c.Spec.Certname != "" {
		if msgs := validation.IsDNS1123Subdomain(strings.ToLower(c.Spec.Certname)); len(msgs) > 0 {
			errs = append(errs, field.Invalid(specPath.Child("certname"), c.Spec.Certname, "must be a valid hostname: "+strings.Join(msgs, "; ")))
		}
	}

	for i, san := range c.Spec.DNSAltNames {
		if net.ParseIP(san) != nil {
			continue // IP SANs are valid
		}
		if msgs := validation.IsDNS1123Subdomain(strings.ToLower(san)); len(msgs) > 0 {
			errs = append(errs, field.Invalid(specPath.Child("dnsAltNames").Index(i), san, "must be a valid DNS name or IP: "+strings.Join(msgs, "; ")))
		}
	}

	if ext := c.Spec.CSRExtensions; ext != nil {
		extPath := specPath.Child("csrExtensions")
		conflicting := map[string]bool{
			"pp_cli_auth":    true,
			"pp_role":        true,
			"pp_environment": true,
		}
		for key := range ext.CustomExtensions {
			p := extPath.Child("customExtensions").Key(key)
			if conflicting[key] {
				errs = append(errs, field.Invalid(p, key, "use the dedicated field instead of customExtensions"))
			} else if !puppet.IsKnownOID(key) {
				errs = append(errs, field.Invalid(p, key, "unknown Puppet extension name"))
			}
		}
	}

	if len(errs) > 0 {
		return nil, errs.ToAggregate()
	}
	return nil, nil
}
