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

// NodeClassifierReconciler owns the status of NodeClassifier objects.
//
// The rendered ENC Secret belongs to the Config controller, which has the
// endpoint credentials. What this controller does is report whether a given
// NodeClassifier actually reached a server -- derived from what it can observe,
// not handed over from another controller.
type NodeClassifierReconciler struct {
	client.Client
}

// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=nodeclassifiers,verbs=get;list;watch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=nodeclassifiers/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=configs,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch

func (r *NodeClassifierReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	nc := &openvoxv1alpha1.NodeClassifier{}
	if err := r.Get(ctx, req.NamespacedName, nc); err != nil {
		if apierrors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("getting NodeClassifier %s: %w", req.NamespacedName, err)
	}

	// Pausing comes after the deletion path: a paused resource must still be
	// deletable, otherwise the annotation turns into a trap.
	if paused, err := reconcilePauseState(ctx, r.Client, nc, &nc.Status.Conditions); err != nil {
		return ctrl.Result{}, err
	} else if paused {
		logger.Info("reconciliation paused by annotation", "name", nc.Name)
		return ctrl.Result{}, nil
	}

	// The generation the verdict is about, captured before the observation:
	// updateStatusWithRetry re-reads the object, so a spec edit landing in
	// between would otherwise stamp the new generation onto a verdict derived
	// from the old spec.
	generation := nc.Generation

	phase, reason, message := r.observe(ctx, nc)
	if reason == reasonLookupFailed {
		// A transient lookup failure says nothing about the NodeClassifier.
		// Leave the status alone and let the backoff retry.
		return ctrl.Result{}, fmt.Errorf("%s", message)
	}

	if err := updateStatusWithRetry(ctx, r.Client, nc, func() {
		nc.Status.ObservedGeneration = generation
		nc.Status.Phase = phase
		status := metav1.ConditionFalse
		if phase == openvoxv1alpha1.NodeClassifierPhaseActive {
			status = metav1.ConditionTrue
		}
		meta.SetStatusCondition(&nc.Status.Conditions, metav1.Condition{
			Type:               openvoxv1alpha1.ConditionNodeClassifierReady,
			Status:             status,
			Reason:             reason,
			Message:            message,
			ObservedGeneration: generation,
		})
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating NodeClassifier status %s: %w", nc.Name, err)
	}

	return ctrl.Result{}, nil
}

// observe derives the NodeClassifier state from the Configs referencing it and
// the ENC Secret rendered for them.
func (r *NodeClassifierReconciler) observe(ctx context.Context,
	nc *openvoxv1alpha1.NodeClassifier) (openvoxv1alpha1.NodeClassifierPhase, string, string) {
	configs, err := configsReferencingNodeClassifier(ctx, r.Client, nc.Namespace, nc.Name)
	if err != nil {
		return "", reasonLookupFailed, fmt.Sprintf("listing Configs for NodeClassifier %s: %v", nc.Name, err)
	}
	if len(configs) == 0 {
		return openvoxv1alpha1.NodeClassifierPhaseError, "NotReferenced",
			fmt.Sprintf("no Config sets nodeClassifierRef to %s, so no ENC configuration is rendered", nc.Name)
	}

	// A custom externalNodesCommand replaces the built-in binary entirely, so
	// this NodeClassifier is bypassed. Reported before the Secret is read: a
	// Secret rendered before the override was set still exists, and calling that
	// "active" would claim an effect this NodeClassifier no longer has.
	if allOverride(configs, overrideExternalNodes) {
		return openvoxv1alpha1.NodeClassifierPhaseError, "OverriddenByExternalNodesCommand",
			fmt.Sprintf("spec.puppet.externalNodesCommand is set on every Config referencing NodeClassifier %s, "+
				"which bypasses NodeClassifier resources", nc.Name)
	}

	var rendered, stale []string
	for _, cfg := range configs {
		if cfg.Spec.Puppet.ExternalNodesCommand != "" {
			continue
		}
		secretName := fmt.Sprintf("%s-enc", cfg.Name)
		secret := &corev1.Secret{}
		if err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: nc.Namespace}, secret); err != nil {
			if apierrors.IsNotFound(err) {
				continue
			}
			return "", reasonLookupFailed, fmt.Sprintf("getting Secret %s: %v", secretName, err)
		}
		// The annotation names the NodeClassifier the content was rendered from,
		// which a Secret left over from a previous nodeClassifierRef does not
		// match, and carries the generation it was rendered at.
		generation, ok := renderedGeneration(secret.Annotations, nc.Name)
		switch {
		case !ok:
			continue
		case generation != nc.Generation:
			stale = append(stale, secretName)
		default:
			rendered = append(rendered, secretName)
		}
	}

	// Stale outranks rendered: while any server still runs an earlier spec, the
	// current one is not in effect, and reporting Ready for a generation that
	// has not fully landed is the claim this controller exists to avoid.
	if len(stale) > 0 {
		return openvoxv1alpha1.NodeClassifierPhaseError, "RenderedConfigStale",
			fmt.Sprintf("Secret %s was rendered from an earlier generation of NodeClassifier %s; "+
				"the current spec has not reached a server", strings.Join(stale, ", "), nc.Name)
	}
	if len(rendered) == 0 {
		return openvoxv1alpha1.NodeClassifierPhaseError, "NotRendered",
			fmt.Sprintf("no Secret rendered from NodeClassifier %s exists yet", nc.Name)
	}

	return openvoxv1alpha1.NodeClassifierPhaseActive, "Rendered",
		fmt.Sprintf("Endpoint is present in Secret %s", strings.Join(rendered, ", "))
}

// configsReferencingNodeClassifier returns the Configs in a namespace whose
// nodeClassifierRef points at the given NodeClassifier.
func configsReferencingNodeClassifier(ctx context.Context, c client.Client,
	namespace, ncName string) ([]openvoxv1alpha1.Config, error) {
	cfgList := &openvoxv1alpha1.ConfigList{}
	if err := c.List(ctx, cfgList,
		client.InNamespace(namespace),
		client.MatchingFields{IndexNodeClassifierRef: ncName}); err != nil {
		return nil, err
	}
	return cfgList.Items, nil
}

func (r *NodeClassifierReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&openvoxv1alpha1.NodeClassifier{}).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(nodeClassifiersForSecret())).
		Watches(&openvoxv1alpha1.Config{}, handler.EnqueueRequestsFromMapFunc(nodeClassifiersForConfig())).
		Complete(r)
}

// nodeClassifiersForSecret maps a rendered ENC Secret back to the
// NodeClassifier it was rendered from.
func nodeClassifiersForSecret() handler.MapFunc {
	return func(_ context.Context, obj client.Object) []ctrl.Request {
		if !strings.HasSuffix(obj.GetName(), "-enc") {
			return nil
		}
		return renderedSourceRequests(obj)
	}
}

// nodeClassifiersForConfig maps a Config change to the NodeClassifier it
// references. The Config decides whether the ENC configuration is rendered at
// all, so its nodeClassifierRef and externalNodesCommand are both inputs to the
// status.
//
// The reference is read off the event object rather than looked up: an update
// runs this against both the old and the new Config, so repointing or clearing
// nodeClassifierRef enqueues the NodeClassifier that just lost its Config as
// well as the one that gained it. A lookup would resolve both events to the
// current spec and leave the former reporting Active forever.
func nodeClassifiersForConfig() handler.MapFunc {
	return func(_ context.Context, obj client.Object) []ctrl.Request {
		cfg, ok := obj.(*openvoxv1alpha1.Config)
		if !ok || cfg.Spec.NodeClassifierRef == "" {
			return nil
		}
		return []ctrl.Request{{
			NamespacedName: types.NamespacedName{Name: cfg.Spec.NodeClassifierRef, Namespace: cfg.Namespace},
		}}
	}
}
