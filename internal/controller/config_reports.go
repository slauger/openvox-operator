package controller

import (
	"context"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"

	"gopkg.in/yaml.v3"
)

// findReportProcessors returns all ReportProcessors referencing this Config, sorted by name.
func (r *ConfigReconciler) findReportProcessors(ctx context.Context, cfg *openvoxv1alpha1.Config) ([]openvoxv1alpha1.ReportProcessor, error) {
	rpList := &openvoxv1alpha1.ReportProcessorList{}
	if err := r.List(ctx, rpList, client.InNamespace(cfg.Namespace)); err != nil {
		return nil, err
	}
	var result []openvoxv1alpha1.ReportProcessor
	for _, rp := range rpList.Items {
		if rp.Spec.ConfigRef == cfg.Name {
			result = append(result, rp)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result, nil
}

// hasReportProcessors returns true if any ReportProcessor references this Config.
func (r *ConfigReconciler) hasReportProcessors(ctx context.Context, cfg *openvoxv1alpha1.Config) (bool, error) {
	processors, err := r.findReportProcessors(ctx, cfg)
	if err != nil {
		return false, err
	}
	return len(processors) > 0, nil
}

// reconcileReportWebhookSecret renders the report-webhook.yaml into a Secret.
func (r *ConfigReconciler) reconcileReportWebhookSecret(ctx context.Context, cfg *openvoxv1alpha1.Config) error {
	processors, err := r.findReportProcessors(ctx, cfg)
	if err != nil {
		return err
	}

	if len(processors) == 0 {
		return nil
	}

	webhookYAML, renderErr := r.renderReportWebhookConfig(ctx, cfg.Namespace, processors)
	if renderErr != nil {
		for i := range processors {
			r.updateReportProcessorStatus(ctx, &processors[i], renderErr)
		}
		return renderErr
	}

	for i := range processors {
		r.updateReportProcessorStatus(ctx, &processors[i], nil)
	}

	secretName := fmt.Sprintf("%s-report-webhook", cfg.Name)
	data := map[string][]byte{
		"report-webhook.yaml": []byte(webhookYAML),
	}

	return r.reconcileSecret(ctx, cfg, secretName, data)
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
func (r *ConfigReconciler) renderReportWebhookConfig(ctx context.Context, namespace string, processors []openvoxv1alpha1.ReportProcessor) (string, error) {
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
				token, err := resolveSecretKey(ctx, r.Client, namespace,
					rp.Spec.Auth.Token.SecretKeyRef.Name, rp.Spec.Auth.Token.SecretKeyRef.Key)
				if err != nil {
					return "", fmt.Errorf("resolving token secret for %s: %w", rp.Name, err)
				}
				auth.Token = token
			case rp.Spec.Auth.Bearer != nil:
				auth.Type = "bearer"
				token, err := resolveSecretKey(ctx, r.Client, namespace,
					rp.Spec.Auth.Bearer.SecretKeyRef.Name, rp.Spec.Auth.Bearer.SecretKeyRef.Key)
				if err != nil {
					return "", fmt.Errorf("resolving bearer secret for %s: %w", rp.Name, err)
				}
				auth.Token = token
			case rp.Spec.Auth.Basic != nil:
				auth.Type = "basic"
				username, err := resolveSecretKey(ctx, r.Client, namespace,
					rp.Spec.Auth.Basic.SecretRef.Name, rp.Spec.Auth.Basic.SecretRef.UsernameKey)
				if err != nil {
					return "", fmt.Errorf("resolving basic auth username for %s: %w", rp.Name, err)
				}
				password, err := resolveSecretKey(ctx, r.Client, namespace,
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
					value, err = resolveSecretKey(ctx, r.Client, namespace,
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

// resolveConfigMapKey reads a specific key from a ConfigMap.
func (r *ConfigReconciler) resolveConfigMapKey(ctx context.Context, namespace, cmName, key string) (string, error) {
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
func (r *ConfigReconciler) updateReportProcessorStatus(ctx context.Context, rp *openvoxv1alpha1.ReportProcessor, err error) {
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

// enqueueConfigsForReportProcessor maps ReportProcessor changes to Config reconciles.
func (r *ConfigReconciler) enqueueConfigsForReportProcessor(c client.Reader) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		rp, ok := obj.(*openvoxv1alpha1.ReportProcessor)
		if !ok {
			return nil
		}

		// Enqueue the Config that this ReportProcessor references
		if rp.Spec.ConfigRef != "" {
			return []reconcile.Request{{
				NamespacedName: types.NamespacedName{Name: rp.Spec.ConfigRef, Namespace: rp.Namespace},
			}}
		}
		return nil
	}
}
