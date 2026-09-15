package controller

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// SigningPolicyReconciler owns the status of SigningPolicy objects.
//
// The rendered autosign policy Secret belongs to the Config controller, which
// has the CSR-attribute credentials and the full policy list. What this
// controller does is report whether a given SigningPolicy actually reached the
// CA -- derived from what it can observe, not handed over from another
// controller.
type SigningPolicyReconciler struct {
	client.Client
}

// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=signingpolicies,verbs=get;list;watch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=signingpolicies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=certificateauthorities,verbs=get;list;watch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=configs,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *SigningPolicyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	sp := &openvoxv1alpha1.SigningPolicy{}
	if err := r.Get(ctx, req.NamespacedName, sp); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("getting SigningPolicy %s: %w", req.NamespacedName, err)
	}

	// Pausing comes after the deletion path: a paused resource must still be
	// deletable, otherwise the annotation turns into a trap.
	if paused, err := reconcilePauseState(ctx, r.Client, sp, &sp.Status.Conditions); err != nil {
		return ctrl.Result{}, err
	} else if paused {
		logger.Info("reconciliation paused by annotation", "name", sp.Name)
		return ctrl.Result{}, nil
	}

	// The generation the verdict is about, captured before the observation:
	// updateStatusWithRetry re-reads the object, so a spec edit landing in
	// between would otherwise stamp the new generation onto a verdict derived
	// from the old spec.
	generation := sp.Generation

	phase, reason, message := r.observe(ctx, sp)
	if reason == reasonLookupFailed {
		// A transient lookup failure says nothing about the SigningPolicy.
		// Leave the status alone and let the backoff retry.
		return ctrl.Result{}, fmt.Errorf("%s", message)
	}

	if err := updateStatusWithRetry(ctx, r.Client, sp, func() {
		sp.Status.ObservedGeneration = generation
		sp.Status.Phase = phase
		status := metav1.ConditionFalse
		if phase == openvoxv1alpha1.SigningPolicyPhaseActive {
			status = metav1.ConditionTrue
		}
		meta.SetStatusCondition(&sp.Status.Conditions, metav1.Condition{
			Type:               openvoxv1alpha1.ConditionSigningPolicyReady,
			Status:             status,
			Reason:             reason,
			Message:            message,
			ObservedGeneration: generation,
		})
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating SigningPolicy status %s: %w", sp.Name, err)
	}

	return ctrl.Result{}, nil
}

// observe derives the SigningPolicy state from the CA it references and the
// autosign policy Secret rendered for that CA.
func (r *SigningPolicyReconciler) observe(ctx context.Context,
	sp *openvoxv1alpha1.SigningPolicy) (openvoxv1alpha1.SigningPolicyPhase, string, string) {
	if sp.Spec.CertificateAuthorityRef == "" {
		return openvoxv1alpha1.SigningPolicyPhaseError, "CertificateAuthorityRefMissing",
			"spec.certificateAuthorityRef is empty"
	}

	caName := sp.Spec.CertificateAuthorityRef
	ca := &openvoxv1alpha1.CertificateAuthority{}
	if err := r.Get(ctx, types.NamespacedName{Name: caName, Namespace: sp.Namespace}, ca); err != nil {
		if apierrors.IsNotFound(err) {
			return openvoxv1alpha1.SigningPolicyPhaseError, "CertificateAuthorityNotFound",
				fmt.Sprintf("CertificateAuthority %s does not exist", caName)
		}
		return "", reasonLookupFailed, fmt.Sprintf("getting CertificateAuthority %s: %v", caName, err)
	}

	// A policy only reaches the CA through a Config: the Config controller is
	// what renders the Secret. Without one, nothing is rendered no matter how
	// valid the policy is.
	configs, err := configsReferencingAuthority(ctx, r.Client, sp.Namespace, caName)
	if err != nil {
		return "", reasonLookupFailed, fmt.Sprintf("listing Configs for CertificateAuthority %s: %v", caName, err)
	}
	if len(configs) == 0 {
		return openvoxv1alpha1.SigningPolicyPhaseError, "NoConfig",
			fmt.Sprintf("no Config references CertificateAuthority %s, so no policy is rendered", caName)
	}

	// A custom autosignCommand replaces the built-in binary entirely, so the
	// policy is bypassed. Reported before the Secret is read: a Secret rendered
	// before the override was set still exists, and calling that "active" would
	// claim an effect this policy no longer has.
	if allOverride(configs, overrideAutosign) {
		return openvoxv1alpha1.SigningPolicyPhaseDisabled, "OverriddenByAutosignCommand",
			fmt.Sprintf("spec.puppet.autosignCommand is set on every Config referencing CertificateAuthority %s, "+
				"which bypasses SigningPolicy resources", caName)
	}

	secretName := fmt.Sprintf("%s-autosign-policy", caName)
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: sp.Namespace}, secret); err != nil {
		if apierrors.IsNotFound(err) {
			return openvoxv1alpha1.SigningPolicyPhaseError, "NotRendered",
				fmt.Sprintf("Secret %s has not been rendered yet", secretName)
		}
		return "", reasonLookupFailed, fmt.Sprintf("getting Secret %s: %v", secretName, err)
	}

	if !renderedSourceRecorded(secret.Annotations) {
		return openvoxv1alpha1.SigningPolicyPhaseError, "RenderedConfigSourceUnknown",
			fmt.Sprintf("Secret %s does not record which resources it was rendered from, so the Config "+
				"controller has not re-rendered it yet; its contents are unchanged in the meantime", secretName)
	}

	// The annotation names the policies the content was rendered from and the
	// generation each was rendered at. Reading it rather than the rendered
	// policy list is what separates "my current spec is in effect" from "some
	// earlier version of it is", which a name alone cannot tell apart.
	generation, ok := renderedGeneration(secret.Annotations, sp.Name)
	switch {
	case !ok:
		return openvoxv1alpha1.SigningPolicyPhaseError, "NotRendered",
			fmt.Sprintf("Secret %s does not contain a policy for this SigningPolicy", secretName)
	case generation != sp.Generation:
		return openvoxv1alpha1.SigningPolicyPhaseError, "RenderedConfigStale",
			fmt.Sprintf("Secret %s was rendered from an earlier generation of SigningPolicy %s; "+
				"the current spec has not reached the CA", secretName, sp.Name)
	}

	return openvoxv1alpha1.SigningPolicyPhaseActive, "Rendered",
		fmt.Sprintf("Policy is present in Secret %s", secretName)
}

// configsReferencingAuthority returns the Configs in a namespace whose
// authorityRef points at the given CertificateAuthority.
func configsReferencingAuthority(ctx context.Context, c client.Client,
	namespace, caName string) ([]openvoxv1alpha1.Config, error) {
	cfgList := &openvoxv1alpha1.ConfigList{}
	if err := c.List(ctx, cfgList,
		client.InNamespace(namespace),
		client.MatchingFields{IndexAuthorityRef: caName}); err != nil {
		return nil, err
	}
	return cfgList.Items, nil
}

// signingPoliciesForAuthority returns the SigningPolicies in a namespace bound
// to the given CertificateAuthority.
//
// A list error is returned rather than swallowed: an empty policy set renders
// as a deny-all policy, so treating a transient failure as "no policies" would
// overwrite a valid policy Secret and lock every agent out.
func signingPoliciesForAuthority(ctx context.Context, c client.Reader,
	namespace, caName string) ([]openvoxv1alpha1.SigningPolicy, error) {
	list := &openvoxv1alpha1.SigningPolicyList{}
	if err := c.List(ctx, list, client.InNamespace(namespace)); err != nil {
		return nil, fmt.Errorf("listing SigningPolicies in namespace %s: %w", namespace, err)
	}
	var result []openvoxv1alpha1.SigningPolicy
	for _, sp := range list.Items {
		if sp.Spec.CertificateAuthorityRef == caName {
			result = append(result, sp)
		}
	}
	return result, nil
}

func (r *SigningPolicyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&openvoxv1alpha1.SigningPolicy{}).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(signingPoliciesForSecret())).
		Watches(&openvoxv1alpha1.Config{}, handler.EnqueueRequestsFromMapFunc(
			signingPoliciesForConfig(mgr.GetClient()),
		)).
		Watches(&openvoxv1alpha1.CertificateAuthority{}, handler.EnqueueRequestsFromMapFunc(
			signingPoliciesForCA(mgr.GetClient()),
		)).
		Complete(r)
}

// signingPoliciesForSecret maps a rendered autosign policy Secret back to the
// SigningPolicies it was rendered from.
func signingPoliciesForSecret() handler.MapFunc {
	return func(_ context.Context, obj client.Object) []ctrl.Request {
		if !strings.HasSuffix(obj.GetName(), "-autosign-policy") {
			return nil
		}
		return renderedSourceRequests(obj)
	}
}

// signingPoliciesForConfig maps a Config change to the SigningPolicies bound to
// the CA it references. The Config decides whether a policy is rendered at all,
// so its authorityRef and autosignCommand are both inputs to the status.
func signingPoliciesForConfig(c client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []ctrl.Request {
		cfg, ok := obj.(*openvoxv1alpha1.Config)
		if !ok || cfg.Spec.AuthorityRef == "" {
			return nil
		}
		return signingPolicyRequests(ctx, c, cfg.Namespace, cfg.Spec.AuthorityRef)
	}
}

// signingPoliciesForCA maps a CertificateAuthority change to the
// SigningPolicies that reference it.
func signingPoliciesForCA(c client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []ctrl.Request {
		return signingPolicyRequests(ctx, c, obj.GetNamespace(), obj.GetName())
	}
}

func signingPolicyRequests(ctx context.Context, c client.Client, namespace, caName string) []ctrl.Request {
	policies, err := signingPoliciesForAuthority(ctx, c, namespace, caName)
	if err != nil {
		log.FromContext(ctx).Error(err, "failed to list SigningPolicies in watcher")
		return nil
	}
	requests := make([]ctrl.Request, 0, len(policies))
	for _, sp := range policies {
		requests = append(requests, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: sp.Name, Namespace: sp.Namespace},
		})
	}
	return requests
}
