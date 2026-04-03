package webhook

import (
	"context"

	"k8s.io/apimachinery/pkg/util/validation/field"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// validSSLModes lists the accepted values for PostgresSpec.SSLMode.
var validSSLModes = map[string]bool{
	"disable":     true,
	"allow":       true,
	"prefer":      true,
	"require":     true,
	"verify-ca":   true,
	"verify-full": true,
}

// DatabaseValidator validates Database resources.
type DatabaseValidator struct {
	Client client.Reader
}

func (v *DatabaseValidator) ValidateCreate(ctx context.Context, d *openvoxv1alpha1.Database) (admission.Warnings, error) {
	return v.validate(ctx, d)
}

func (v *DatabaseValidator) ValidateUpdate(ctx context.Context, _, d *openvoxv1alpha1.Database) (admission.Warnings, error) {
	return v.validate(ctx, d)
}

func (v *DatabaseValidator) ValidateDelete(_ context.Context, _ *openvoxv1alpha1.Database) (admission.Warnings, error) {
	return nil, nil
}

func (v *DatabaseValidator) validate(ctx context.Context, d *openvoxv1alpha1.Database) (admission.Warnings, error) {
	var errs field.ErrorList
	specPath := field.NewPath("spec")

	// certificateRef must not be empty and the referenced Certificate must exist.
	if d.Spec.CertificateRef == "" {
		errs = append(errs, field.Required(specPath.Child("certificateRef"), "must not be empty"))
	} else if err := refExists(ctx, v.Client, d.Namespace, d.Spec.CertificateRef, &openvoxv1alpha1.Certificate{}); err != nil {
		errs = append(errs, field.Invalid(specPath.Child("certificateRef"), d.Spec.CertificateRef, err.Error()))
	}

	pgPath := specPath.Child("postgres")

	// postgres.host must not be empty.
	if d.Spec.Postgres.Host == "" {
		errs = append(errs, field.Required(pgPath.Child("host"), "must not be empty"))
	}

	// postgres.credentialsSecretRef must not be empty.
	if d.Spec.Postgres.CredentialsSecretRef == "" {
		errs = append(errs, field.Required(pgPath.Child("credentialsSecretRef"), "must not be empty"))
	}

	// postgres.sslMode must be a known value when set.
	if d.Spec.Postgres.SSLMode != "" && !validSSLModes[d.Spec.Postgres.SSLMode] {
		errs = append(errs, field.NotSupported(pgPath.Child("sslMode"), d.Spec.Postgres.SSLMode, sslModeKeys()))
	}

	if len(errs) > 0 {
		return nil, errs.ToAggregate()
	}
	return nil, nil
}

func sslModeKeys() []string {
	return []string{"disable", "allow", "prefer", "require", "verify-ca", "verify-full"}
}
