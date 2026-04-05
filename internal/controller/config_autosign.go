package controller

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

const autosignBinaryPath = "/usr/local/bin/openvox-autosign"

// findSigningPolicies returns all SigningPolicies referencing the given CA.
func (r *ConfigReconciler) findSigningPolicies(ctx context.Context, ca *openvoxv1alpha1.CertificateAuthority) []openvoxv1alpha1.SigningPolicy {
	list := &openvoxv1alpha1.SigningPolicyList{}
	if err := r.List(ctx, list, client.InNamespace(ca.Namespace)); err != nil {
		return nil
	}
	var result []openvoxv1alpha1.SigningPolicy
	for _, sp := range list.Items {
		if sp.Spec.CertificateAuthorityRef == ca.Name {
			result = append(result, sp)
		}
	}
	return result
}

// reconcileAutosignSecrets reconciles the autosign policy Secret for the CA referenced by this Config.
func (r *ConfigReconciler) reconcileAutosignSecrets(ctx context.Context, cfg *openvoxv1alpha1.Config) error {
	if cfg.Spec.AuthorityRef == "" {
		return nil
	}
	ca := &openvoxv1alpha1.CertificateAuthority{}
	if err := r.Get(ctx, types.NamespacedName{Name: cfg.Spec.AuthorityRef, Namespace: cfg.Namespace}, ca); err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if err := r.reconcileAutosignSecret(ctx, cfg, ca); err != nil {
		return fmt.Errorf("reconciling autosign Secret for CA %s: %w", ca.Name, err)
	}
	return nil
}

// reconcileAutosignSecret renders the autosign policy config YAML into a Secret.
// The Secret is always created -- the binary handles all cases (no policies = deny all,
// any:true = approve all). This keeps puppet.conf static and avoids pod restarts.
func (r *ConfigReconciler) reconcileAutosignSecret(ctx context.Context, cfg *openvoxv1alpha1.Config, ca *openvoxv1alpha1.CertificateAuthority) error {
	secretName := fmt.Sprintf("%s-autosign-policy", ca.Name)

	policies := r.findSigningPolicies(ctx, ca)

	// Render policy config YAML
	policyYAML, renderErr := r.renderAutosignPolicyConfig(ctx, cfg.Namespace, policies)
	if renderErr != nil {
		return fmt.Errorf("rendering autosign policy config: %w", renderErr)
	}

	// Update SigningPolicy status
	for i := range policies {
		r.updateSigningPolicyStatus(ctx, &policies[i], nil)
	}

	data := map[string][]byte{
		"autosign-policy.yaml": []byte(policyYAML),
	}

	return r.reconcileSecret(ctx, cfg, secretName, data)
}

// renderAutosignPolicyConfig renders the policy config YAML that openvox-autosign reads.
func (r *ConfigReconciler) renderAutosignPolicyConfig(ctx context.Context, namespace string, policies []openvoxv1alpha1.SigningPolicy) (string, error) {
	var sb strings.Builder
	sb.WriteString("policies:\n")

	// Sort policies by name for deterministic output
	sort.Slice(policies, func(i, j int) bool {
		return policies[i].Name < policies[j].Name
	})

	for _, p := range policies {
		fmt.Fprintf(&sb, "  - name: %s\n", p.Name)

		if p.Spec.Any {
			sb.WriteString("    any: true\n")
			continue
		}

		if p.Spec.Pattern != nil {
			sb.WriteString("    pattern:\n")
			sb.WriteString("      allow:\n")
			for _, a := range p.Spec.Pattern.Allow {
				fmt.Fprintf(&sb, "        - %q\n", a)
			}
		}

		if p.Spec.DNSAltNames != nil {
			sb.WriteString("    dnsAltNames:\n")
			sb.WriteString("      allow:\n")
			for _, a := range p.Spec.DNSAltNames.Allow {
				fmt.Fprintf(&sb, "        - %q\n", a)
			}
		}

		if len(p.Spec.CSRAttributes) > 0 {
			sb.WriteString("    csrAttributes:\n")
			for _, attr := range p.Spec.CSRAttributes {
				value := attr.Value
				if attr.ValueFrom != nil {
					var err error
					value, err = resolveSecretKey(ctx, r.Client, namespace,
						attr.ValueFrom.SecretKeyRef.Name, attr.ValueFrom.SecretKeyRef.Key)
					if err != nil {
						r.updateSigningPolicyStatus(ctx, &p, err)
						return "", fmt.Errorf("resolving csrAttribute %q for policy %s: %w", attr.Name, p.Name, err)
					}
				}
				fmt.Fprintf(&sb, "      - name: %s\n", attr.Name)
				fmt.Fprintf(&sb, "        value: %q\n", value)
			}
		}
	}

	return sb.String(), nil
}

// updateSigningPolicyStatus sets the phase and condition on a SigningPolicy.
func (r *ConfigReconciler) updateSigningPolicyStatus(ctx context.Context, sp *openvoxv1alpha1.SigningPolicy, err error) {
	var errMsg string
	if err != nil {
		errMsg = err.Error()
	}
	if statusErr := updateStatusWithRetry(ctx, r.Client, sp, func() {
		if err != nil {
			sp.Status.Phase = openvoxv1alpha1.SigningPolicyPhaseError
			meta.SetStatusCondition(&sp.Status.Conditions, metav1.Condition{
				Type:               openvoxv1alpha1.ConditionSigningPolicyReady,
				Status:             metav1.ConditionFalse,
				Reason:             "Error",
				Message:            errMsg,
				LastTransitionTime: metav1.Now(),
			})
		} else {
			sp.Status.Phase = openvoxv1alpha1.SigningPolicyPhaseActive
			meta.SetStatusCondition(&sp.Status.Conditions, metav1.Condition{
				Type:               openvoxv1alpha1.ConditionSigningPolicyReady,
				Status:             metav1.ConditionTrue,
				Reason:             "PolicyRendered",
				Message:            "Signing policy is active",
				LastTransitionTime: metav1.Now(),
			})
		}
	}); statusErr != nil {
		log.FromContext(ctx).Error(statusErr, "failed to update SigningPolicy status", "name", sp.Name)
	}
}

// enqueueConfigsForSigningPolicy maps SigningPolicy changes to Config reconciles.
func (r *ConfigReconciler) enqueueConfigsForSigningPolicy(c client.Reader) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		sp, ok := obj.(*openvoxv1alpha1.SigningPolicy)
		if !ok {
			return nil
		}

		// Find the CA referenced by this SigningPolicy
		ca := &openvoxv1alpha1.CertificateAuthority{}
		if err := c.Get(ctx, types.NamespacedName{Name: sp.Spec.CertificateAuthorityRef, Namespace: sp.Namespace}, ca); err != nil {
			log.FromContext(ctx).Error(err, "failed to get CertificateAuthority in watcher", "name", sp.Spec.CertificateAuthorityRef)
			return nil
		}

		// Enqueue all Configs whose authorityRef points to this CA
		cfgList := &openvoxv1alpha1.ConfigList{}
		if err := c.List(ctx, cfgList, client.InNamespace(ca.Namespace)); err != nil {
			log.FromContext(ctx).Error(err, "failed to list Configs in watcher")
			return nil
		}

		var requests []reconcile.Request
		for _, cfg := range cfgList.Items {
			if cfg.Spec.AuthorityRef == ca.Name {
				requests = append(requests, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: cfg.Name, Namespace: cfg.Namespace},
				})
			}
		}
		return requests
	}
}
