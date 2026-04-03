package webhook

import (
	ctrl "sigs.k8s.io/controller-runtime"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// SetupWithManager registers all validating webhooks with the manager.
func SetupWithManager(mgr ctrl.Manager) error {
	c := mgr.GetClient()

	if err := ctrl.NewWebhookManagedBy(mgr, &openvoxv1alpha1.Server{}).
		WithValidator(&ServerValidator{Client: c}).
		Complete(); err != nil {
		return err
	}

	if err := ctrl.NewWebhookManagedBy(mgr, &openvoxv1alpha1.Certificate{}).
		WithValidator(&CertificateValidator{Client: c}).
		Complete(); err != nil {
		return err
	}

	if err := ctrl.NewWebhookManagedBy(mgr, &openvoxv1alpha1.Config{}).
		WithValidator(&ConfigValidator{Client: c}).
		Complete(); err != nil {
		return err
	}

	if err := ctrl.NewWebhookManagedBy(mgr, &openvoxv1alpha1.SigningPolicy{}).
		WithValidator(&SigningPolicyValidator{Client: c}).
		Complete(); err != nil {
		return err
	}

	if err := ctrl.NewWebhookManagedBy(mgr, &openvoxv1alpha1.ReportProcessor{}).
		WithValidator(&ReportProcessorValidator{Client: c}).
		Complete(); err != nil {
		return err
	}

	if err := ctrl.NewWebhookManagedBy(mgr, &openvoxv1alpha1.NodeClassifier{}).
		WithValidator(&NodeClassifierValidator{}).
		Complete(); err != nil {
		return err
	}

	if err := ctrl.NewWebhookManagedBy(mgr, &openvoxv1alpha1.CertificateAuthority{}).
		WithValidator(&CertificateAuthorityValidator{}).
		Complete(); err != nil {
		return err
	}

	if err := ctrl.NewWebhookManagedBy(mgr, &openvoxv1alpha1.Pool{}).
		WithValidator(&PoolValidator{}).
		Complete(); err != nil {
		return err
	}

	if err := ctrl.NewWebhookManagedBy(mgr, &openvoxv1alpha1.Database{}).
		WithValidator(&DatabaseValidator{Client: c}).
		Complete(); err != nil {
		return err
	}

	return nil
}
