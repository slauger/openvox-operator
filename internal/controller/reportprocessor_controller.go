package controller

import (
	"context"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"

	"gopkg.in/yaml.v3"
)

// ReportProcessorReconciler reconciles ReportProcessor objects.
type ReportProcessorReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder events.EventRecorder
}

// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=reportprocessors,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=openvox.voxpupuli.org,resources=reportprocessors/status,verbs=get;update;patch
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch;create;update;patch
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch
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

	// Look up the Config referenced by this ReportProcessor
	cfg := &openvoxv1alpha1.Config{}
	if err := r.Get(ctx, types.NamespacedName{Name: rp.Spec.ConfigRef, Namespace: rp.Namespace}, cfg); err != nil {
		if errors.IsNotFound(err) {
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}

	logger.Info("reconciling report-webhook Secret", "config", cfg.Name)
	if err := r.reconcileReportWebhookSecret(ctx, cfg); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconciling report-webhook Secret: %w", err)
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

// reportWebhookConfig mirrors the YAML structure read by openvox-report.
type reportWebhookConfig struct {
	Endpoints []reportEndpointConfig `yaml:"endpoints"`
}

type reportEndpointConfig struct {
	Name           string               `yaml:"name"`
	Processor      string               `yaml:"processor,omitempty"`
	URL            string               `yaml:"url"`
	TimeoutSeconds int32                `yaml:"timeoutSeconds"`
	Auth           *reportAuthConfig    `yaml:"auth,omitempty"`
	SSL            reportSSLConfig      `yaml:"ssl"`
	Headers        []reportHeaderConfig `yaml:"headers,omitempty"`
}

type reportAuthConfig struct {
	Type     string `yaml:"type"`
	Header   string `yaml:"header,omitempty"`
	Token    string `yaml:"token,omitempty"`
	Username string `yaml:"username,omitempty"`
	Password string `yaml:"password,omitempty"`
}

type reportSSLConfig struct {
	CertFile string `yaml:"certFile"`
	KeyFile  string `yaml:"keyFile"`
	CAFile   string `yaml:"caFile"`
}

type reportHeaderConfig struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

// renderReportWebhookConfig renders the report-webhook.yaml that openvox-report reads.
func (r *ReportProcessorReconciler) renderReportWebhookConfig(ctx context.Context, namespace string, processors []openvoxv1alpha1.ReportProcessor) (string, error) {
	var endpoints []reportEndpointConfig

	for _, rp := range processors {
		timeout := int32(30)
		if rp.Spec.TimeoutSeconds != 0 {
			timeout = rp.Spec.TimeoutSeconds
		}

		ep := reportEndpointConfig{
			Name:           rp.Name,
			Processor:      rp.Spec.Processor,
			URL:            rp.Spec.URL,
			TimeoutSeconds: timeout,
			SSL: reportSSLConfig{
				CertFile: "/etc/puppetlabs/puppet/ssl/certs/puppet.pem",
				KeyFile:  "/etc/puppetlabs/puppet/ssl/private_keys/puppet.pem",
				CAFile:   "/etc/puppetlabs/puppet/ssl/certs/ca.pem",
			},
		}

		// Auth
		if rp.Spec.Auth != nil {
			auth := &reportAuthConfig{}
			switch {
			case rp.Spec.Auth.MTLS:
				auth.Type = "mtls"
			case rp.Spec.Auth.Token != nil:
				auth.Type = "token"
				auth.Header = rp.Spec.Auth.Token.Header
				token, err := r.resolveSecretKey(ctx, namespace,
					rp.Spec.Auth.Token.SecretKeyRef.Name, rp.Spec.Auth.Token.SecretKeyRef.Key)
				if err != nil {
					return "", fmt.Errorf("resolving token secret for %s: %w", rp.Name, err)
				}
				auth.Token = token
			case rp.Spec.Auth.Bearer != nil:
				auth.Type = "bearer"
				token, err := r.resolveSecretKey(ctx, namespace,
					rp.Spec.Auth.Bearer.SecretKeyRef.Name, rp.Spec.Auth.Bearer.SecretKeyRef.Key)
				if err != nil {
					return "", fmt.Errorf("resolving bearer secret for %s: %w", rp.Name, err)
				}
				auth.Token = token
			case rp.Spec.Auth.Basic != nil:
				auth.Type = "basic"
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
				auth.Username = username
				auth.Password = password
			}
			ep.Auth = auth
		}

		// Headers
		for _, h := range rp.Spec.Headers {
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
			ep.Headers = append(ep.Headers, reportHeaderConfig{Name: h.Name, Value: value})
		}

		endpoints = append(endpoints, ep)
	}

	out, err := yaml.Marshal(reportWebhookConfig{Endpoints: endpoints})
	if err != nil {
		return "", fmt.Errorf("marshaling report-webhook config: %w", err)
	}
	return string(out), nil
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
	if statusErr := r.Status().Update(ctx, rp); statusErr != nil {
		log.FromContext(ctx).Error(statusErr, "failed to update ReportProcessor status", "name", rp.Name)
	}
}
