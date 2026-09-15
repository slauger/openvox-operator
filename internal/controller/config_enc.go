package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"

	"gopkg.in/yaml.v3"
)

const encBinaryPath = "/usr/local/bin/openvox-enc"

// reconcileENCSecret renders the ENC config into a Secret when nodeClassifierRef is set.
func (r *ConfigReconciler) reconcileENCSecret(ctx context.Context, cfg *openvoxv1alpha1.Config) error {
	if cfg.Spec.NodeClassifierRef == "" {
		return nil
	}
	// A custom externalNodesCommand replaces the built-in binary, so the ENC Secret
	// it would read is neither rendered nor mounted.
	if cfg.Spec.Puppet.ExternalNodesCommand != "" {
		return nil
	}

	nc := &openvoxv1alpha1.NodeClassifier{}
	if err := r.Get(ctx, types.NamespacedName{Name: cfg.Spec.NodeClassifierRef, Namespace: cfg.Namespace}, nc); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}
		return fmt.Errorf("getting NodeClassifier %s: %w", cfg.Spec.NodeClassifierRef, err)
	}

	// Rendering failures are reported on the Config, which owns this Secret. The
	// NodeClassifier controller derives its own status from whether its endpoint
	// ends up in the rendered Secret.
	encYAML, renderErr := r.renderENCConfig(ctx, cfg, nc)
	if renderErr != nil {
		r.Recorder.Eventf(cfg, nil, corev1.EventTypeWarning, EventReasonENCRenderFailed, "Reconcile",
			"Rendering the ENC configuration from NodeClassifier %s failed: %v", nc.Name, renderErr)
		return fmt.Errorf("rendering ENC config: %w", renderErr)
	}

	secretName := fmt.Sprintf("%s-enc", cfg.Name)
	data := map[string][]byte{
		"enc.yaml": []byte(encYAML),
	}

	return r.reconcileSecret(ctx, cfg, secretName, data,
		renderedFromAnnotation([]renderSource{sourceOf(nc)}))
}

// encYAMLConfig mirrors the YAML structure read by openvox-enc.
type encYAMLConfig struct {
	URL            string         `yaml:"url"`
	Method         string         `yaml:"method"`
	Path           string         `yaml:"path"`
	Body           string         `yaml:"body,omitempty"`
	ResponseFormat string         `yaml:"responseFormat"`
	TimeoutSeconds int32          `yaml:"timeoutSeconds"`
	Auth           *encAuthConfig `yaml:"auth,omitempty"`
	Cache          *encCache      `yaml:"cache,omitempty"`
	SSL            encSSLConfig   `yaml:"ssl"`
}

type encAuthConfig struct {
	Type     string `yaml:"type"`
	Header   string `yaml:"header,omitempty"`
	Token    string `yaml:"token,omitempty"`
	Username string `yaml:"username,omitempty"`
	Password string `yaml:"password,omitempty"`
}

type encCache struct {
	Enabled   bool   `yaml:"enabled"`
	Directory string `yaml:"directory"`
}

type encSSLConfig struct {
	CertFile string `yaml:"certFile"`
	KeyFile  string `yaml:"keyFile"`
	CAFile   string `yaml:"caFile"`
}

// renderENCConfig renders the enc.yaml that openvox-enc reads.
func (r *ConfigReconciler) renderENCConfig(ctx context.Context, cfg *openvoxv1alpha1.Config, nc *openvoxv1alpha1.NodeClassifier) (string, error) {
	timeout := int32(10)
	if nc.Spec.TimeoutSeconds != 0 {
		timeout = nc.Spec.TimeoutSeconds
	}

	encCfg := encYAMLConfig{
		URL:            nc.Spec.URL,
		Method:         nc.Spec.Request.Method,
		Path:           nc.Spec.Request.Path,
		Body:           nc.Spec.Request.Body,
		ResponseFormat: nc.Spec.Response.Format,
		TimeoutSeconds: timeout,
		SSL: encSSLConfig{
			CertFile: "/etc/puppetlabs/puppet/ssl/certs/puppet.pem",
			KeyFile:  "/etc/puppetlabs/puppet/ssl/private_keys/puppet.pem",
			CAFile:   "/etc/puppetlabs/puppet/ssl/certs/ca.pem",
		},
	}

	// Auth
	if nc.Spec.Auth != nil {
		auth := &encAuthConfig{}
		switch {
		case nc.Spec.Auth.MTLS:
			auth.Type = "mtls"
		case nc.Spec.Auth.Token != nil:
			auth.Type = "token"
			auth.Header = nc.Spec.Auth.Token.Header
			token, err := resolveSecretKey(ctx, r.Client, cfg.Namespace,
				nc.Spec.Auth.Token.SecretKeyRef.Name, nc.Spec.Auth.Token.SecretKeyRef.Key)
			if err != nil {
				return "", fmt.Errorf("resolving token secret: %w", err)
			}
			auth.Token = token
		case nc.Spec.Auth.Bearer != nil:
			auth.Type = "bearer"
			token, err := resolveSecretKey(ctx, r.Client, cfg.Namespace,
				nc.Spec.Auth.Bearer.SecretKeyRef.Name, nc.Spec.Auth.Bearer.SecretKeyRef.Key)
			if err != nil {
				return "", fmt.Errorf("resolving bearer secret: %w", err)
			}
			auth.Token = token
		case nc.Spec.Auth.Basic != nil:
			auth.Type = "basic"
			username, err := resolveSecretKey(ctx, r.Client, cfg.Namespace,
				nc.Spec.Auth.Basic.SecretRef.Name, nc.Spec.Auth.Basic.SecretRef.UsernameKey)
			if err != nil {
				return "", fmt.Errorf("resolving basic auth username: %w", err)
			}
			password, err := resolveSecretKey(ctx, r.Client, cfg.Namespace,
				nc.Spec.Auth.Basic.SecretRef.Name, nc.Spec.Auth.Basic.SecretRef.PasswordKey)
			if err != nil {
				return "", fmt.Errorf("resolving basic auth password: %w", err)
			}
			auth.Username = username
			auth.Password = password
		}
		encCfg.Auth = auth
	}

	// Cache
	if nc.Spec.Cache != nil && nc.Spec.Cache.Enabled {
		dir := "/var/cache/openvox-enc"
		if nc.Spec.Cache.Directory != "" {
			dir = nc.Spec.Cache.Directory
		}
		encCfg.Cache = &encCache{Enabled: true, Directory: dir}
	}

	out, err := yaml.Marshal(encCfg)
	if err != nil {
		return "", fmt.Errorf("marshaling ENC config: %w", err)
	}
	return string(out), nil
}

// enqueueConfigsForNodeClassifier maps NodeClassifier changes to Config reconciles.
func (r *ConfigReconciler) enqueueConfigsForNodeClassifier(c client.Client) handler.MapFunc {
	return func(ctx context.Context, obj client.Object) []reconcile.Request {
		nc, ok := obj.(*openvoxv1alpha1.NodeClassifier)
		if !ok {
			return nil
		}
		configs, err := configsReferencingNodeClassifier(ctx, c, nc.Namespace, nc.Name)
		if err != nil {
			log.FromContext(ctx).Error(err, "failed to list Configs in watcher")
			return nil
		}
		return configRequests(configs)
	}
}
