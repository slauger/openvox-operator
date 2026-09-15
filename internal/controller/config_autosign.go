package controller

import (
	"context"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

const autosignBinaryPath = "/usr/local/bin/openvox-autosign"

// autosignPolicyDir is where the rendered policy Secret is mounted. It is a
// directory so the kubelet keeps it in sync; see the mount in
// server_deployment.go.
const autosignPolicyDir = "/etc/puppetlabs/puppet/autosign-policy"

// autosignPolicyPath is the file inside that directory, passed to the binary
// with --config.
const autosignPolicyPath = autosignPolicyDir + "/autosign-policy.yaml"

// reconcileAutosignSecrets reconciles the autosign policy Secret for the CA referenced by this Config.
func (r *ConfigReconciler) reconcileAutosignSecrets(ctx context.Context, cfg *openvoxv1alpha1.Config) error {
	if cfg.Spec.AuthorityRef == "" {
		return nil
	}
	// A custom autosignCommand replaces the built-in binary, so the policy Secret
	// it would read is neither rendered nor mounted.
	if cfg.Spec.Puppet.AutosignCommand != "" {
		return nil
	}
	ca := &openvoxv1alpha1.CertificateAuthority{}
	if err := r.Get(ctx, types.NamespacedName{Name: cfg.Spec.AuthorityRef, Namespace: cfg.Namespace}, ca); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("getting CertificateAuthority %s: %w", cfg.Spec.AuthorityRef, err)
	}
	if err := r.reconcileAutosignSecret(ctx, cfg, ca); err != nil {
		return fmt.Errorf("reconciling autosign Secret for CA %s: %w", ca.Name, err)
	}
	return nil
}

// reconcileAutosignSecret renders the autosign policy config YAML into a Secret.
// The Secret is always created -- the binary handles all cases (no policies = deny all,
// any:true = approve all). puppet.conf stays static; a policy change rewrites this
// Secret and the server controller rolls the CA pod via the autosign-policy-secret-hash
// annotation to apply it.
func (r *ConfigReconciler) reconcileAutosignSecret(ctx context.Context, cfg *openvoxv1alpha1.Config, ca *openvoxv1alpha1.CertificateAuthority) error {
	secretName := fmt.Sprintf("%s-autosign-policy", ca.Name)

	policies, err := signingPoliciesForAuthority(ctx, r.Client, ca.Namespace, ca.Name)
	if err != nil {
		return err
	}

	// Rendering failures are reported on the Config, which owns this Secret. The
	// SigningPolicy controller derives its own status from whether its policy
	// ends up in the rendered Secret.
	policyYAML, renderErr := r.renderAutosignPolicyConfig(ctx, cfg.Namespace, ca, policies)
	if renderErr != nil {
		r.Recorder.Eventf(cfg, nil, corev1.EventTypeWarning, EventReasonAutosignPolicyRenderFailed, "Reconcile",
			"Rendering the autosign policy for CertificateAuthority %s failed: %v", ca.Name, renderErr)
		return fmt.Errorf("rendering autosign policy config: %w", renderErr)
	}

	data := map[string][]byte{
		"autosign-policy.yaml": []byte(policyYAML),
	}

	sources := make([]renderSource, 0, len(policies))
	for i := range policies {
		sources = append(sources, sourceOf(&policies[i]))
	}

	return r.reconcileSecret(ctx, cfg, secretName, data, renderedFromAnnotation(sources))
}

// renderAutosignPolicyConfig renders the policy config YAML that openvox-autosign reads.
func (r *ConfigReconciler) renderAutosignPolicyConfig(ctx context.Context, namespace string,
	ca *openvoxv1alpha1.CertificateAuthority, policies []openvoxv1alpha1.SigningPolicy) (string, error) {
	var sb strings.Builder

	// The CA auth.conf grants admin rights to this certname as well as to the
	// pp_cli_auth extension (see builtinAuthRules). An agent holding a
	// certificate under it would be a CA admin, so the name is refused before
	// any policy runs - including any: true, which no downstream guard can undo.
	sb.WriteString("reservedCertnames:\n")
	fmt.Fprintf(&sb, "  - %q\n", operatorSigningCertname(ca.Name))

	sb.WriteString("policies:\n")

	// Sort policies by name for deterministic output
	sort.Slice(policies, func(i, j int) bool {
		return policies[i].Name < policies[j].Name
	})

	for _, p := range policies {
		fmt.Fprintf(&sb, "  - name: %q\n", p.Name)

		if p.Spec.Any {
			sb.WriteString("    any: true\n")
		}

		// Guard fields (SAN allowlists and extensions) are rendered for every
		// policy, including any:true, so the autosign binary enforces them and no
		// policy can implicitly waive escalation protection.
		if p.Spec.Certnames != nil {
			renderAllowList(&sb, "certnames", p.Spec.Certnames.Allow)
		}
		if p.Spec.DNSAltNames != nil {
			renderAllowList(&sb, "dnsAltNames", p.Spec.DNSAltNames.Allow)
		}
		if p.Spec.IPAltNames != nil {
			renderAllowList(&sb, "ipAltNames", p.Spec.IPAltNames.Allow)
		}
		if p.Spec.URIAltNames != nil {
			renderAllowList(&sb, "uriAltNames", p.Spec.URIAltNames.Allow)
		}
		if p.Spec.EmailAltNames != nil {
			renderAllowList(&sb, "emailAltNames", p.Spec.EmailAltNames.Allow)
		}
		if p.Spec.Extensions != nil {
			renderAllowList(&sb, "extensions", p.Spec.Extensions.Allow)
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
						return "", fmt.Errorf("resolving csrAttribute %q for policy %s: %w", attr.Name, p.Name, err)
					}
				}
				fmt.Fprintf(&sb, "      - name: %q\n", attr.Name)
				fmt.Fprintf(&sb, "        value: %q\n", value)
			}
		}
	}

	return sb.String(), nil
}

// renderAllowList writes an "{field}: { allow: [...] }" block with quoted entries.
func renderAllowList(sb *strings.Builder, field string, allow []string) {
	fmt.Fprintf(sb, "    %s:\n", field)
	sb.WriteString("      allow:\n")
	for _, a := range allow {
		fmt.Fprintf(sb, "        - %q\n", a)
	}
}

// enqueueConfigsForSigningPolicy maps SigningPolicy changes to Config reconciles.
func (r *ConfigReconciler) enqueueConfigsForSigningPolicy(c client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		sp, ok := obj.(*openvoxv1alpha1.SigningPolicy)
		if !ok || sp.Spec.CertificateAuthorityRef == "" {
			return nil
		}
		configs, err := configsReferencingAuthority(ctx, c, sp.Namespace, sp.Spec.CertificateAuthorityRef)
		if err != nil {
			log.FromContext(ctx).Error(err, "failed to list Configs in watcher")
			return nil
		}
		return configRequests(configs)
	}
}

// configRequests turns a set of Configs into reconcile requests.
func configRequests(configs []openvoxv1alpha1.Config) []reconcile.Request {
	requests := make([]reconcile.Request, 0, len(configs))
	for _, cfg := range configs {
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{Name: cfg.Name, Namespace: cfg.Namespace},
		})
	}
	return requests
}
