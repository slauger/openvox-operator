package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

func (r *CertificateAuthorityReconciler) reconcileCASetupRBAC(ctx context.Context, ca *openvoxv1alpha1.CertificateAuthority, certs []openvoxv1alpha1.Certificate) error {
	baseName := fmt.Sprintf("%s-ca-setup", ca.Name)
	caSecretName := fmt.Sprintf("%s-ca", ca.Name)
	labels := caLabels(ca.Name)

	caKeySecretName := fmt.Sprintf("%s-ca-key", ca.Name)
	caCRLSecretName := fmt.Sprintf("%s-ca-crl", ca.Name)
	resourceNames := []string{caSecretName, caKeySecretName, caCRLSecretName}
	for _, cert := range certs {
		resourceNames = append(resourceNames, fmt.Sprintf("%s-tls", cert.Name))
	}

	// ServiceAccount
	if err := r.ensureCAServiceAccount(ctx, baseName, ca.Namespace, labels, ca); err != nil {
		return fmt.Errorf("ensuring ServiceAccount: %w", err)
	}

	// Role
	if err := r.ensureCARole(ctx, baseName, ca.Namespace, labels, resourceNames, ca); err != nil {
		return fmt.Errorf("ensuring Role: %w", err)
	}

	// RoleBinding
	if err := r.ensureCARoleBinding(ctx, baseName, ca.Namespace, labels, ca); err != nil {
		return fmt.Errorf("ensuring RoleBinding: %w", err)
	}

	return nil
}

func (r *CertificateAuthorityReconciler) ensureCAServiceAccount(ctx context.Context, name, namespace string, labels map[string]string, owner *openvoxv1alpha1.CertificateAuthority) error {
	sa := &corev1.ServiceAccount{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, sa); errors.IsNotFound(err) {
		sa = &corev1.ServiceAccount{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
				Labels:    labels,
			},
		}
		if err := controllerutil.SetControllerReference(owner, sa, r.Scheme); err != nil {
			return err
		}
		return r.Create(ctx, sa)
	} else {
		return err
	}
}

func (r *CertificateAuthorityReconciler) ensureCARole(ctx context.Context, name, namespace string, labels map[string]string, resourceNames []string, owner *openvoxv1alpha1.CertificateAuthority) error {
	role := &rbacv1.Role{}
	err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, role)
	if errors.IsNotFound(err) {
		role = &rbacv1.Role{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
				Labels:    labels,
			},
			Rules: []rbacv1.PolicyRule{
				{
					APIGroups:     []string{""},
					Resources:     []string{"secrets"},
					ResourceNames: resourceNames,
					Verbs:         []string{"get", "update", "patch"},
				},
				{
					APIGroups: []string{""},
					Resources: []string{"secrets"},
					Verbs:     []string{"create"},
				},
			},
		}
		if err := controllerutil.SetControllerReference(owner, role, r.Scheme); err != nil {
			return err
		}
		return r.Create(ctx, role)
	} else if err != nil {
		return err
	}

	role.Rules = []rbacv1.PolicyRule{
		{
			APIGroups:     []string{""},
			Resources:     []string{"secrets"},
			ResourceNames: resourceNames,
			Verbs:         []string{"get", "update", "patch"},
		},
		{
			APIGroups: []string{""},
			Resources: []string{"secrets"},
			Verbs:     []string{"create"},
		},
	}
	return r.Update(ctx, role)
}

func (r *CertificateAuthorityReconciler) ensureCARoleBinding(ctx context.Context, name, namespace string, labels map[string]string, owner *openvoxv1alpha1.CertificateAuthority) error {
	rb := &rbacv1.RoleBinding{}
	if err := r.Get(ctx, types.NamespacedName{Name: name, Namespace: namespace}, rb); errors.IsNotFound(err) {
		rb = &rbacv1.RoleBinding{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
				Labels:    labels,
			},
			RoleRef: rbacv1.RoleRef{
				APIGroup: "rbac.authorization.k8s.io",
				Kind:     "Role",
				Name:     name,
			},
			Subjects: []rbacv1.Subject{
				{
					Kind:      "ServiceAccount",
					Name:      name,
					Namespace: namespace,
				},
			},
		}
		if err := controllerutil.SetControllerReference(owner, rb, r.Scheme); err != nil {
			return err
		}
		return r.Create(ctx, rb)
	} else {
		return err
	}
}
