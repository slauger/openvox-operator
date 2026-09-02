package controller

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/yaml"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// ReportProcessorReconciler owns the status of ReportProcessor objects.
//
// The rendered report-webhook Secret belongs to the Config controller, which
// has the credentials and the full processor list. What this controller does is
// report whether a given ReportProcessor actually made it into that Secret --
// derived from what it can observe, not handed over from another controller.
type ReportProcessorReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder events.EventRecorder
}

// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=reportprocessors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=reportprocessors/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=configs,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *ReportProcessorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	rp := &openvoxv1alpha1.ReportProcessor{}
	if err := r.Get(ctx, req.NamespacedName, rp); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, fmt.Errorf("getting ReportProcessor %s: %w", req.NamespacedName, err)
	}

	// Pausing comes after the deletion path: a paused resource must still be
	// deletable, otherwise the annotation turns into a trap.
	if paused, err := reconcilePauseState(ctx, r.Client, rp, &rp.Status.Conditions); err != nil {
		return ctrl.Result{}, err
	} else if paused {
		logger.Info("reconciliation paused by annotation", "name", rp.Name)
		return ctrl.Result{}, nil
	}

	phase, reason, message := r.observe(ctx, rp)
	if reason == reasonLookupFailed {
		// A transient lookup failure says nothing about the ReportProcessor.
		// Leave the status alone and let the backoff retry.
		return ctrl.Result{}, fmt.Errorf("%s", message)
	}

	if err := updateStatusWithRetry(ctx, r.Client, rp, func() {
		rp.Status.ObservedGeneration = rp.Generation
		rp.Status.Phase = phase
		status := metav1.ConditionFalse
		if phase == openvoxv1alpha1.ReportProcessorPhaseActive {
			status = metav1.ConditionTrue
		}
		meta.SetStatusCondition(&rp.Status.Conditions, metav1.Condition{
			Type:               openvoxv1alpha1.ConditionReportProcessorReady,
			Status:             status,
			Reason:             reason,
			Message:            message,
			ObservedGeneration: rp.Generation,
		})
	}); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating ReportProcessor status %s: %w", rp.Name, err)
	}

	return ctrl.Result{}, nil
}

// reasonLookupFailed marks an inconclusive observation: the API call failed, so
// nothing can be said about the ReportProcessor.
const reasonLookupFailed = "LookupFailed"

// observe derives the ReportProcessor state from the Config it references and
// the report-webhook Secret rendered from it.
func (r *ReportProcessorReconciler) observe(ctx context.Context, rp *openvoxv1alpha1.ReportProcessor) (openvoxv1alpha1.ReportProcessorPhase, string, string) {
	if rp.Spec.ConfigRef == "" {
		return openvoxv1alpha1.ReportProcessorPhaseError, "ConfigRefMissing", "spec.configRef is empty"
	}

	cfg := &openvoxv1alpha1.Config{}
	if err := r.Get(ctx, types.NamespacedName{Name: rp.Spec.ConfigRef, Namespace: rp.Namespace}, cfg); err != nil {
		if errors.IsNotFound(err) {
			return openvoxv1alpha1.ReportProcessorPhaseError, "ConfigNotFound",
				fmt.Sprintf("Config %s does not exist", rp.Spec.ConfigRef)
		}
		return "", reasonLookupFailed, fmt.Sprintf("getting Config %s: %v", rp.Spec.ConfigRef, err)
	}

	secretName := fmt.Sprintf("%s-report-webhook", cfg.Name)
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: rp.Namespace}, secret); err != nil {
		if errors.IsNotFound(err) {
			return openvoxv1alpha1.ReportProcessorPhaseError, "NotRendered",
				fmt.Sprintf("Secret %s has not been rendered yet", secretName)
		}
		return "", reasonLookupFailed, fmt.Sprintf("getting Secret %s: %v", secretName, err)
	}

	rendered, err := renderedEndpointNames(secret.Data["report-webhook.yaml"])
	if err != nil {
		return openvoxv1alpha1.ReportProcessorPhaseError, "RenderedConfigUnreadable",
			fmt.Sprintf("Secret %s does not contain a readable report-webhook.yaml: %v", secretName, err)
	}
	for _, name := range rendered {
		if name == rp.Name {
			return openvoxv1alpha1.ReportProcessorPhaseActive, "Rendered",
				fmt.Sprintf("Endpoint is present in Secret %s", secretName)
		}
	}

	return openvoxv1alpha1.ReportProcessorPhaseError, "NotRendered",
		fmt.Sprintf("Secret %s does not contain an endpoint for this ReportProcessor", secretName)
}

// renderedEndpointNames extracts the endpoint names from a rendered
// report-webhook.yaml.
func renderedEndpointNames(data []byte) ([]string, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("report-webhook.yaml is empty")
	}
	var parsed struct {
		Endpoints []struct {
			Name string `json:"name"`
		} `json:"endpoints"`
	}
	if err := yaml.Unmarshal(data, &parsed); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(parsed.Endpoints))
	for _, ep := range parsed.Endpoints {
		names = append(names, ep.Name)
	}
	return names, nil
}

func (r *ReportProcessorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&openvoxv1alpha1.ReportProcessor{}).
		Watches(&corev1.Secret{}, handler.EnqueueRequestsFromMapFunc(
			reportProcessorsForSecret(mgr.GetClient()),
		)).
		Watches(&openvoxv1alpha1.Config{}, handler.EnqueueRequestsFromMapFunc(
			reportProcessorsForConfig(mgr.GetClient()),
		)).
		Complete(r)
}

// reportProcessorsForSecret maps a rendered report-webhook Secret back to the
// ReportProcessors it was rendered from.
func reportProcessorsForSecret(c client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []ctrl.Request {
		name := obj.GetName()
		if !strings.HasSuffix(name, "-report-webhook") {
			return nil
		}
		cfgName := strings.TrimSuffix(name, "-report-webhook")
		return reportProcessorRequests(ctx, c, obj.GetNamespace(), cfgName)
	}
}

// reportProcessorsForConfig maps a Config change to the ReportProcessors that
// reference it.
func reportProcessorsForConfig(c client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []ctrl.Request {
		return reportProcessorRequests(ctx, c, obj.GetNamespace(), obj.GetName())
	}
}

func reportProcessorRequests(ctx context.Context, c client.Client, namespace, cfgName string) []ctrl.Request {
	rpList := &openvoxv1alpha1.ReportProcessorList{}
	if err := c.List(ctx, rpList, client.InNamespace(namespace)); err != nil {
		log.FromContext(ctx).Error(err, "failed to list ReportProcessors in watcher")
		return nil
	}
	var requests []ctrl.Request
	for _, rp := range rpList.Items {
		if rp.Spec.ConfigRef == cfgName {
			requests = append(requests, ctrl.Request{
				NamespacedName: types.NamespacedName{Name: rp.Name, Namespace: rp.Namespace},
			})
		}
	}
	return requests
}
