package controller

import (
	"context"
	"fmt"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// ReportProcessorReconciler reconciles ReportProcessor objects.
type ReportProcessorReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=reportprocessors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=reportprocessors/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=secrets;configmaps,verbs=get;list;watch
// +kubebuilder:rbac:groups="",resources=events,verbs=create;patch

func (r *ReportProcessorReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	rp := &openvoxv1alpha1.ReportProcessor{}
	if err := r.Get(ctx, req.NamespacedName, rp); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	// Find the Config that references this ReportProcessor
	cfgList := &openvoxv1alpha1.ConfigList{}
	if err := r.List(ctx, cfgList, client.InNamespace(rp.Namespace)); err != nil {
		return ctrl.Result{}, err
	}

	// Find Configs that have ReportProcessors with matching configRef
	for _, cfg := range cfgList.Items {
		if rp.Spec.ConfigRef == cfg.Name {
			logger.Info("reconciling report-webhook Secret", "config", cfg.Name)
			if err := r.reconcileReportWebhookSecret(ctx, &cfg); err != nil {
				return ctrl.Result{}, fmt.Errorf("reconciling report-webhook Secret: %w", err)
			}
		}
	}

	return ctrl.Result{}, nil
}

func (r *ReportProcessorReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&openvoxv1alpha1.ReportProcessor{}).
		Watches(&openvoxv1alpha1.Config{}, handler.EnqueueRequestsFromMapFunc(
			r.enqueueReportProcessorsForConfig(mgr.GetClient()),
		)).
		Complete(r)
}

// enqueueReportProcessorsForConfig maps Config changes to ReportProcessor reconciles.
func (r *ReportProcessorReconciler) enqueueReportProcessorsForConfig(c client.Reader) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		cfg, ok := obj.(*openvoxv1alpha1.Config)
		if !ok {
			return nil
		}

		rpList := &openvoxv1alpha1.ReportProcessorList{}
		if err := c.List(ctx, rpList, client.InNamespace(cfg.Namespace)); err != nil {
			return nil
		}

		var requests []reconcile.Request
		for _, rp := range rpList.Items {
			if rp.Spec.ConfigRef == cfg.Name {
				requests = append(requests, reconcile.Request{
					NamespacedName: types.NamespacedName{Name: rp.Name, Namespace: rp.Namespace},
				})
			}
		}
		return requests
	}
}

// reconcileReportWebhookSecret renders the report-webhook.yaml into a Secret.
func (r *ReportProcessorReconciler) reconcileReportWebhookSecret(ctx context.Context, cfg *openvoxv1alpha1.Config) error {
	logger := log.FromContext(ctx)

	// Find all ReportProcessors for this Config
	rpList := &openvoxv1alpha1.ReportProcessorList{}
	if err := r.List(ctx, rpList, client.InNamespace(cfg.Namespace)); err != nil {
		return err
	}

	var processors []openvoxv1alpha1.ReportProcessor
	for _, rp := range rpList.Items {
		if rp.Spec.ConfigRef == cfg.Name {
			processors = append(processors, rp)
		}
	}

	// Sort by name for deterministic output
	sort.Slice(processors, func(i, j int) bool {
		return processors[i].Name < processors[j].Name
	})

	// Render report-webhook.yaml
	webhookYAML, renderErr := r.renderReportWebhookConfig(ctx, cfg.Namespace, processors)
	if renderErr != nil {
		for i := range processors {
			r.updateReportProcessorStatus(ctx, &processors[i], renderErr)
		}
		return renderErr
	}

	// Update status for all processors
	for i := range processors {
		r.updateReportProcessorStatus(ctx, &processors[i], nil)
	}

	secretName := fmt.Sprintf("%s-report-webhook", cfg.Name)
	data := map[string][]byte{
		"report-webhook.yaml": []byte(webhookYAML),
	}

	existing := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: cfg.Namespace}, existing)
	if errors.IsNotFound(err) {
		logger.Info("creating report-webhook Secret", "name", secretName)
		secret := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      secretName,
				Namespace: cfg.Namespace,
				Labels:    configLabels(cfg.Name),
			},
			Data: data,
		}
		if err := controllerutil.SetControllerReference(cfg, secret, r.Scheme); err != nil {
			return err
		}
		return r.Create(ctx, secret)
	} else if err != nil {
		return err
	}

	existing.Data = data
	return r.Update(ctx, existing)
}

// renderReportWebhookConfig renders the report-webhook.yaml that openvox-report reads.
func (r *ReportProcessorReconciler) renderReportWebhookConfig(ctx context.Context, namespace string, processors []openvoxv1alpha1.ReportProcessor) (string, error) {
	var sb strings.Builder
	sb.WriteString("endpoints:\n")

	for _, rp := range processors {
		fmt.Fprintf(&sb, "  - name: %s\n", rp.Name)
		if rp.Spec.Processor != "" {
			fmt.Fprintf(&sb, "    processor: %s\n", rp.Spec.Processor)
		}
		fmt.Fprintf(&sb, "    url: %s\n", rp.Spec.URL)

		timeout := int32(30)
		if rp.Spec.TimeoutSeconds != 0 {
			timeout = rp.Spec.TimeoutSeconds
		}
		fmt.Fprintf(&sb, "    timeoutSeconds: %d\n", timeout)

		// Auth
		if rp.Spec.Auth != nil {
			sb.WriteString("    auth:\n")
			switch {
			case rp.Spec.Auth.MTLS:
				sb.WriteString("      type: mtls\n")
			case rp.Spec.Auth.Token != nil:
				sb.WriteString("      type: token\n")
				fmt.Fprintf(&sb, "      header: %s\n", rp.Spec.Auth.Token.Header)
				token, err := r.resolveSecretKey(ctx, namespace,
					rp.Spec.Auth.Token.SecretKeyRef.Name, rp.Spec.Auth.Token.SecretKeyRef.Key)
				if err != nil {
					return "", fmt.Errorf("resolving token secret for %s: %w", rp.Name, err)
				}
				fmt.Fprintf(&sb, "      token: %s\n", token)
			case rp.Spec.Auth.Bearer != nil:
				sb.WriteString("      type: bearer\n")
				token, err := r.resolveSecretKey(ctx, namespace,
					rp.Spec.Auth.Bearer.SecretKeyRef.Name, rp.Spec.Auth.Bearer.SecretKeyRef.Key)
				if err != nil {
					return "", fmt.Errorf("resolving bearer secret for %s: %w", rp.Name, err)
				}
				fmt.Fprintf(&sb, "      token: %s\n", token)
			case rp.Spec.Auth.Basic != nil:
				sb.WriteString("      type: basic\n")
				username, err := r.resolveSecretKey(ctx, namespace,
					rp.Spec.Auth.Basic.SecretRef.Name, rp.Spec.Auth.Basic.SecretRef.UsernameKey)
				if err != nil {
					return "", fmt.Errorf("resolving basic auth username for %s: %w", rp.Name, err)
				}
				password, err := r.resolveSecretKey(ctx, namespace,
					rp.Spec.Auth.Basic.SecretRef.Name, rp.Spec.Auth.Basic.SecretRef.PasswordKey)
				if err != nil {
					return "", fmt.Errorf("resolving basic auth password for %s: %w", rp.Name, err)
				}
				fmt.Fprintf(&sb, "      username: %s\n", username)
				fmt.Fprintf(&sb, "      password: %s\n", password)
			}
		}

		// SSL paths (for mTLS or server verification)
		sb.WriteString("    ssl:\n")
		sb.WriteString("      certFile: /etc/puppetlabs/puppet/ssl/certs/puppet.pem\n")
		sb.WriteString("      keyFile: /etc/puppetlabs/puppet/ssl/private_keys/puppet.pem\n")
		sb.WriteString("      caFile: /etc/puppetlabs/puppet/ssl/certs/ca.pem\n")

		// Headers
		if len(rp.Spec.Headers) > 0 {
			sb.WriteString("    headers:\n")
			for _, h := range rp.Spec.Headers {
				fmt.Fprintf(&sb, "      - name: %s\n", h.Name)
				value := h.Value
				if h.ValueFrom != nil {
					var err error
					if h.ValueFrom.SecretKeyRef != nil {
						value, err = r.resolveSecretKey(ctx, namespace,
							h.ValueFrom.SecretKeyRef.Name, h.ValueFrom.SecretKeyRef.Key)
						if err != nil {
							return "", fmt.Errorf("resolving header secret for %s: %w", rp.Name, err)
						}
					} else if h.ValueFrom.ConfigMapKeyRef != nil {
						value, err = r.resolveConfigMapKey(ctx, namespace,
							h.ValueFrom.ConfigMapKeyRef.Name, h.ValueFrom.ConfigMapKeyRef.Key)
						if err != nil {
							return "", fmt.Errorf("resolving header configmap for %s: %w", rp.Name, err)
						}
					}
				}
				fmt.Fprintf(&sb, "        value: %s\n", value)
			}
		}
	}

	return sb.String(), nil
}

// resolveSecretKey reads a specific key from a Secret.
func (r *ReportProcessorReconciler) resolveSecretKey(ctx context.Context, namespace, secretName, key string) (string, error) {
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: namespace}, secret); err != nil {
		return "", fmt.Errorf("getting Secret %s: %w", secretName, err)
	}
	val, ok := secret.Data[key]
	if !ok {
		return "", fmt.Errorf("key %q not found in Secret %s", key, secretName)
	}
	return string(val), nil
}

// resolveConfigMapKey reads a specific key from a ConfigMap.
func (r *ReportProcessorReconciler) resolveConfigMapKey(ctx context.Context, namespace, cmName, key string) (string, error) {
	cm := &corev1.ConfigMap{}
	if err := r.Get(ctx, types.NamespacedName{Name: cmName, Namespace: namespace}, cm); err != nil {
		return "", fmt.Errorf("getting ConfigMap %s: %w", cmName, err)
	}
	val, ok := cm.Data[key]
	if !ok {
		return "", fmt.Errorf("key %q not found in ConfigMap %s", key, cmName)
	}
	return val, nil
}

// updateReportProcessorStatus sets the phase and condition on a ReportProcessor.
func (r *ReportProcessorReconciler) updateReportProcessorStatus(ctx context.Context, rp *openvoxv1alpha1.ReportProcessor, err error) {
	if err != nil {
		rp.Status.Phase = openvoxv1alpha1.ReportProcessorPhaseError
		meta.SetStatusCondition(&rp.Status.Conditions, metav1.Condition{
			Type:               openvoxv1alpha1.ConditionReportProcessorReady,
			Status:             metav1.ConditionFalse,
			Reason:             "Error",
			Message:            err.Error(),
			LastTransitionTime: metav1.Now(),
		})
	} else {
		rp.Status.Phase = openvoxv1alpha1.ReportProcessorPhaseActive
		meta.SetStatusCondition(&rp.Status.Conditions, metav1.Condition{
			Type:               openvoxv1alpha1.ConditionReportProcessorReady,
			Status:             metav1.ConditionTrue,
			Reason:             "ConfigRendered",
			Message:            "Report processor configuration is active",
			LastTransitionTime: metav1.Now(),
		})
	}
	_ = r.Status().Update(ctx, rp)
}
