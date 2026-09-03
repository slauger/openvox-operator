package controller

import (
	"context"
	"fmt"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// Field index names for cross-resource lookups.
//
// Watch map functions have to answer "which resources reference this object?"
// on every event. Without an index that means listing every object in the
// namespace and filtering in Go; with one the cache answers directly.
// Indexes are scoped per type, so the same field name is reused where the
// reference means the same thing on different resources.
const (
	IndexConfigRef      = "spec.configRef"
	IndexCertificateRef = "spec.certificateRef"
	IndexAuthorityRef   = "spec.authorityRef"

	// IndexCertname makes the certname collision check a lookup rather than a
	// full listing. A certname identifies exactly one entry on the CA, so two
	// Certificates sharing one against the same CA are indistinguishable to it.
	IndexCertname = "spec.certname"
)

// SetupFieldIndexers registers every field index the controllers rely on.
//
// It must be called once before the controllers are started: registering the
// same (type, field) pair twice makes the manager fail at startup.
func SetupFieldIndexers(ctx context.Context, mgr ctrl.Manager) error {
	for _, idx := range fieldIndexes() {
		if err := mgr.GetFieldIndexer().IndexField(ctx, idx.obj, idx.field, idx.extract); err != nil {
			return fmt.Errorf("indexing %T on %s: %w", idx.obj, idx.field, err)
		}
	}
	return nil
}

// fieldIndex describes one index so production setup and the test client can
// register the exact same set.
type fieldIndex struct {
	obj     client.Object
	field   string
	extract client.IndexerFunc
}

func fieldIndexes() []fieldIndex {
	return []fieldIndex{
		{&openvoxv1alpha1.Server{}, IndexConfigRef, func(o client.Object) []string {
			return nonEmpty(o.(*openvoxv1alpha1.Server).Spec.ConfigRef)
		}},
		{&openvoxv1alpha1.Server{}, IndexCertificateRef, func(o client.Object) []string {
			return nonEmpty(o.(*openvoxv1alpha1.Server).Spec.CertificateRef)
		}},
		{&openvoxv1alpha1.Config{}, IndexAuthorityRef, func(o client.Object) []string {
			return nonEmpty(o.(*openvoxv1alpha1.Config).Spec.AuthorityRef)
		}},
		{&openvoxv1alpha1.Certificate{}, IndexAuthorityRef, func(o client.Object) []string {
			return nonEmpty(o.(*openvoxv1alpha1.Certificate).Spec.AuthorityRef)
		}},
		{&openvoxv1alpha1.Certificate{}, IndexCertname, func(o client.Object) []string {
			c := o.(*openvoxv1alpha1.Certificate)
			return nonEmpty(certnameOf(c))
		}},
		{&openvoxv1alpha1.Database{}, IndexCertificateRef, func(o client.Object) []string {
			return nonEmpty(o.(*openvoxv1alpha1.Database).Spec.CertificateRef)
		}},
	}
}

// nonEmpty drops empty reference values so unset fields do not end up in the
// index under the empty key.
func nonEmpty(v string) []string {
	if v == "" {
		return nil
	}
	return []string{v}
}
