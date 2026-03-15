package controller

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

func TestConfigReconcile_NotFound(t *testing.T) {
	c := setupTestClient()
	r := newConfigReconciler(c)

	res, err := r.Reconcile(testCtx(), testRequest("nonexistent"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.RequeueAfter != 0 {
		t.Error("expected no requeue for missing Config")
	}
}

func TestConfigReconcile_BasicCreation(t *testing.T) {
	cfg := newConfig("production")
	c := setupTestClient(cfg)
	r := newConfigReconciler(c)

	res, err := r.Reconcile(testCtx(), testRequest("production"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.RequeueAfter != 0 {
		t.Error("expected no requeue")
	}

	// Verify ConfigMap
	cm := &corev1.ConfigMap{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "production-config", Namespace: testNamespace}, cm); err != nil {
		t.Fatalf("ConfigMap not created: %v", err)
	}

	expectedKeys := []string{
		"puppet.conf", "puppetdb.conf", "webserver.conf", "webserver-ca.conf",
		"puppetserver.conf", "auth.conf", "ca.conf", "product.conf",
		"logback.xml", "metrics.conf", "ca-enabled.cfg", "ca-disabled.cfg",
	}
	for _, key := range expectedKeys {
		if _, ok := cm.Data[key]; !ok {
			t.Errorf("ConfigMap missing key %q", key)
		}
	}
	if len(cm.Data) != len(expectedKeys) {
		t.Errorf("ConfigMap has %d keys, expected %d", len(cm.Data), len(expectedKeys))
	}

	// Verify ServiceAccount
	sa := &corev1.ServiceAccount{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "production-server", Namespace: testNamespace}, sa); err != nil {
		t.Fatalf("ServiceAccount not created: %v", err)
	}

	// Verify status phase
	updatedCfg := &openvoxv1alpha1.Config{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "production", Namespace: testNamespace}, updatedCfg); err != nil {
		t.Fatalf("failed to get Config: %v", err)
	}
	if updatedCfg.Status.Phase != openvoxv1alpha1.ConfigPhaseRunning {
		t.Errorf("expected phase %q, got %q", openvoxv1alpha1.ConfigPhaseRunning, updatedCfg.Status.Phase)
	}
}

func TestConfigReconcile_PuppetConfRendering(t *testing.T) {
	tests := []struct {
		name     string
		opts     []configOption
		contains []string
		excludes []string
	}{
		{
			name: "storeconfigs enabled",
			opts: []configOption{withPuppetSpec(openvoxv1alpha1.PuppetSpec{
				Storeconfigs: true,
				StoreBackend: "puppetdb",
				Reports:      "puppetdb",
			})},
			contains: []string{"storeconfigs = true", "storeconfigs_backend = puppetdb"},
		},
		{
			name: "storeconfigs disabled",
			opts: []configOption{withPuppetSpec(openvoxv1alpha1.PuppetSpec{
				Storeconfigs: false,
				Reports:      "puppetdb",
			})},
			excludes: []string{"storeconfigs = true"},
		},
		{
			name: "custom environmentPath",
			opts: []configOption{withPuppetSpec(openvoxv1alpha1.PuppetSpec{
				EnvironmentPath: "/custom/code/environments",
				Reports:         "puppetdb",
			})},
			contains: []string{"environmentpath = /custom/code/environments"},
		},
		{
			name: "custom hieraConfig",
			opts: []configOption{withPuppetSpec(openvoxv1alpha1.PuppetSpec{
				HieraConfig: "/etc/puppetlabs/custom/hiera.yaml",
				Reports:     "puppetdb",
			})},
			contains: []string{"hiera_config = /etc/puppetlabs/custom/hiera.yaml"},
		},
		{
			name: "extraConfig sorted",
			opts: []configOption{withPuppetSpec(openvoxv1alpha1.PuppetSpec{
				Reports: "puppetdb",
				ExtraConfig: map[string]string{
					"zz_setting": "zvalue",
					"aa_setting": "avalue",
				},
			})},
			contains: []string{"aa_setting = avalue", "zz_setting = zvalue"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := newConfig("test-cfg", tt.opts...)
			c := setupTestClient(cfg)
			r := newConfigReconciler(c)

			if _, err := r.Reconcile(testCtx(), testRequest("test-cfg")); err != nil {
				t.Fatalf("reconcile error: %v", err)
			}

			cm := &corev1.ConfigMap{}
			if err := c.Get(testCtx(), types.NamespacedName{Name: "test-cfg-config", Namespace: testNamespace}, cm); err != nil {
				t.Fatalf("ConfigMap not found: %v", err)
			}

			puppetConf := cm.Data["puppet.conf"]
			for _, s := range tt.contains {
				if !strings.Contains(puppetConf, s) {
					t.Errorf("puppet.conf missing %q\n---\n%s", s, puppetConf)
				}
			}
			for _, s := range tt.excludes {
				if strings.Contains(puppetConf, s) {
					t.Errorf("puppet.conf should not contain %q\n---\n%s", s, puppetConf)
				}
			}
		})
	}
}

func TestConfigReconcile_PuppetConfWithCA(t *testing.T) {
	cfg := newConfig("production", withAuthorityRef("production-ca"))
	ca := newCertificateAuthority("production-ca")
	c := setupTestClient(cfg, ca)
	r := newConfigReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("production")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	cm := &corev1.ConfigMap{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "production-config", Namespace: testNamespace}, cm); err != nil {
		t.Fatalf("ConfigMap not found: %v", err)
	}

	puppetConf := cm.Data["puppet.conf"]
	if !strings.Contains(puppetConf, "ca_ttl =") {
		t.Errorf("puppet.conf missing ca_ttl\n---\n%s", puppetConf)
	}
	if !strings.Contains(puppetConf, "autosign = ") {
		t.Errorf("puppet.conf missing autosign\n---\n%s", puppetConf)
	}
}

func TestConfigReconcile_PuppetConfWithENC(t *testing.T) {
	nc := newNodeClassifier("my-enc", "https://enc.example.com")
	cfg := newConfig("production", withNodeClassifierRef("my-enc"))
	c := setupTestClient(cfg, nc)
	r := newConfigReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("production")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	cm := &corev1.ConfigMap{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "production-config", Namespace: testNamespace}, cm); err != nil {
		t.Fatalf("ConfigMap not found: %v", err)
	}

	puppetConf := cm.Data["puppet.conf"]
	if !strings.Contains(puppetConf, "node_terminus = exec") {
		t.Errorf("puppet.conf missing node_terminus\n---\n%s", puppetConf)
	}
	if !strings.Contains(puppetConf, "external_nodes = ") {
		t.Errorf("puppet.conf missing external_nodes\n---\n%s", puppetConf)
	}
}

func TestConfigReconcile_PuppetConfWithReports(t *testing.T) {
	cfg := newConfig("production")
	rp := newReportProcessor("webhook-rp", "production", "https://reports.example.com")
	c := setupTestClient(cfg, rp)
	r := newConfigReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("production")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	cm := &corev1.ConfigMap{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "production-config", Namespace: testNamespace}, cm); err != nil {
		t.Fatalf("ConfigMap not found: %v", err)
	}

	puppetConf := cm.Data["puppet.conf"]
	if !strings.Contains(puppetConf, "reports = puppetdb,webhook") {
		t.Errorf("puppet.conf missing reports with webhook\n---\n%s", puppetConf)
	}
}

func TestConfigReconcile_PuppetserverConf(t *testing.T) {
	tests := []struct {
		name     string
		ps       openvoxv1alpha1.PuppetServerSpec
		contains []string
	}{
		{
			name: "default server-var-dir",
			ps:   openvoxv1alpha1.PuppetServerSpec{},
			contains: []string{
				"server-var-dir: /run/puppetserver",
			},
		},
		{
			name: "custom compile-mode",
			ps: openvoxv1alpha1.PuppetServerSpec{
				CompileMode: "jit",
			},
			contains: []string{"compile-mode: jit"},
		},
		{
			name: "custom borrow-timeout",
			ps: openvoxv1alpha1.PuppetServerSpec{
				BorrowTimeout: 600000,
			},
			contains: []string{"borrow-timeout: 600000"},
		},
		{
			name: "http-client settings",
			ps: openvoxv1alpha1.PuppetServerSpec{
				HTTPClient: &openvoxv1alpha1.HTTPClientSpec{
					ConnectTimeoutMs: int32Ptr(5000),
					IdleTimeoutMs:    int32Ptr(30000),
				},
			},
			contains: []string{
				"connect-timeout-milliseconds: 5000",
				"idle-timeout-milliseconds: 30000",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := newConfig("test-cfg", withPuppetServerSpec(tt.ps))
			c := setupTestClient(cfg)
			r := newConfigReconciler(c)

			if _, err := r.Reconcile(testCtx(), testRequest("test-cfg")); err != nil {
				t.Fatalf("reconcile error: %v", err)
			}

			cm := &corev1.ConfigMap{}
			if err := c.Get(testCtx(), types.NamespacedName{Name: "test-cfg-config", Namespace: testNamespace}, cm); err != nil {
				t.Fatalf("ConfigMap not found: %v", err)
			}

			psConf := cm.Data["puppetserver.conf"]
			for _, s := range tt.contains {
				if !strings.Contains(psConf, s) {
					t.Errorf("puppetserver.conf missing %q\n---\n%s", s, psConf)
				}
			}
		})
	}
}

func TestConfigReconcile_AuthConf(t *testing.T) {
	cfg := newConfig("production")
	c := setupTestClient(cfg)
	r := newConfigReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("production")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	cm := &corev1.ConfigMap{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "production-config", Namespace: testNamespace}, cm); err != nil {
		t.Fatalf("ConfigMap not found: %v", err)
	}

	authConf := cm.Data["auth.conf"]
	// Must contain default rules
	if !strings.Contains(authConf, "puppetlabs v3 catalog from agents") {
		t.Errorf("auth.conf missing default catalog rule")
	}
	if !strings.Contains(authConf, "puppetlabs status service - simple") {
		t.Errorf("auth.conf missing default status rule")
	}
	// Must end with deny-all
	if !strings.Contains(authConf, `deny: "*"`) {
		t.Errorf("auth.conf missing deny-all rule")
	}
	if !strings.Contains(authConf, `name: "puppetlabs deny all"`) {
		t.Errorf("auth.conf missing deny-all rule name")
	}
}

func TestConfigReconcile_AuthConfCustomRules(t *testing.T) {
	rules := []openvoxv1alpha1.AuthorizationRule{
		{
			Name: "custom api access",
			MatchRequest: openvoxv1alpha1.AuthorizationMatchRequest{
				Path:   "/custom/api",
				Type:   "path",
				Method: []string{"get"},
			},
			Allow:     "*",
			SortOrder: 100,
		},
	}

	cfg := newConfig("production", withAuthorizationRules(rules))
	c := setupTestClient(cfg)
	r := newConfigReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("production")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	cm := &corev1.ConfigMap{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "production-config", Namespace: testNamespace}, cm); err != nil {
		t.Fatalf("ConfigMap not found: %v", err)
	}

	authConf := cm.Data["auth.conf"]
	// Custom rule must appear
	if !strings.Contains(authConf, `"custom api access"`) {
		t.Errorf("auth.conf missing custom rule\n---\n%s", authConf)
	}
	// Custom rule must appear before deny-all
	customIdx := strings.Index(authConf, "custom api access")
	denyIdx := strings.Index(authConf, "puppetlabs deny all")
	if customIdx >= denyIdx {
		t.Errorf("custom rule should appear before deny-all (custom=%d, deny=%d)", customIdx, denyIdx)
	}
}

func TestConfigReconcile_LogbackXML(t *testing.T) {
	tests := []struct {
		name     string
		opts     []configOption
		contains []string
	}{
		{
			name: "default INFO level",
			opts: nil,
			contains: []string{
				`<root level="INFO">`,
			},
		},
		{
			name: "custom root level",
			opts: []configOption{withLogging("DEBUG", nil)},
			contains: []string{
				`<root level="DEBUG">`,
			},
		},
		{
			name: "per-logger overrides",
			opts: []configOption{withLogging("INFO", map[string]string{
				"puppetlabs.puppetserver": "DEBUG",
			})},
			contains: []string{
				`<logger name="puppetlabs.puppetserver" level="DEBUG" />`,
				`<root level="INFO">`,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := newConfig("test-cfg", tt.opts...)
			c := setupTestClient(cfg)
			r := newConfigReconciler(c)

			if _, err := r.Reconcile(testCtx(), testRequest("test-cfg")); err != nil {
				t.Fatalf("reconcile error: %v", err)
			}

			cm := &corev1.ConfigMap{}
			if err := c.Get(testCtx(), types.NamespacedName{Name: "test-cfg-config", Namespace: testNamespace}, cm); err != nil {
				t.Fatalf("ConfigMap not found: %v", err)
			}

			logbackXML := cm.Data["logback.xml"]
			for _, s := range tt.contains {
				if !strings.Contains(logbackXML, s) {
					t.Errorf("logback.xml missing %q\n---\n%s", s, logbackXML)
				}
			}
		})
	}
}

func TestConfigReconcile_AutosignPolicy(t *testing.T) {
	cfg := newConfig("production", withAuthorityRef("production-ca"))
	ca := newCertificateAuthority("production-ca")
	sp := newSigningPolicy("allow-all", "production-ca", true)

	c := setupTestClient(cfg, ca, sp)
	r := newConfigReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("production")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	// Verify autosign-policy Secret
	secret := &corev1.Secret{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "production-ca-autosign-policy", Namespace: testNamespace}, secret); err != nil {
		t.Fatalf("autosign Secret not created: %v", err)
	}

	policyYAML := string(secret.Data["autosign-policy.yaml"])
	if !strings.Contains(policyYAML, "any: true") {
		t.Errorf("autosign policy missing any:true\n---\n%s", policyYAML)
	}
	if !strings.Contains(policyYAML, "name: allow-all") {
		t.Errorf("autosign policy missing policy name\n---\n%s", policyYAML)
	}
}

func TestConfigReconcile_ENCSecret(t *testing.T) {
	tests := []struct {
		name     string
		nc       *openvoxv1alpha1.NodeClassifier
		contains []string
	}{
		{
			name: "basic enc config",
			nc:   newNodeClassifier("my-enc", "https://enc.example.com"),
			contains: []string{
				"url: https://enc.example.com",
				"method: GET",
				"path: /node/{certname}",
				"responseFormat: yaml",
			},
		},
		{
			name: "mtls auth",
			nc: func() *openvoxv1alpha1.NodeClassifier {
				nc := newNodeClassifier("my-enc", "https://enc.example.com")
				nc.Spec.Auth = &openvoxv1alpha1.NodeClassifierAuth{MTLS: true}
				return nc
			}(),
			contains: []string{
				"type: mtls",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := newConfig("production", withNodeClassifierRef("my-enc"))
			c := setupTestClient(cfg, tt.nc)
			r := newConfigReconciler(c)

			if _, err := r.Reconcile(testCtx(), testRequest("production")); err != nil {
				t.Fatalf("reconcile error: %v", err)
			}

			secret := &corev1.Secret{}
			if err := c.Get(testCtx(), types.NamespacedName{Name: "production-enc", Namespace: testNamespace}, secret); err != nil {
				t.Fatalf("ENC Secret not created: %v", err)
			}

			encYAML := string(secret.Data["enc.yaml"])
			for _, s := range tt.contains {
				if !strings.Contains(encYAML, s) {
					t.Errorf("enc.yaml missing %q\n---\n%s", s, encYAML)
				}
			}
		})
	}
}

func TestConfigReconcile_ReportWebhookSecret(t *testing.T) {
	cfg := newConfig("production")
	rp1 := newReportProcessor("beta-webhook", "production", "https://beta.example.com/reports")
	rp2 := newReportProcessor("alpha-webhook", "production", "https://alpha.example.com/reports")

	c := setupTestClient(cfg, rp1, rp2)
	r := newConfigReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("production")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	secret := &corev1.Secret{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "production-report-webhook", Namespace: testNamespace}, secret); err != nil {
		t.Fatalf("report-webhook Secret not created: %v", err)
	}

	webhookYAML := string(secret.Data["report-webhook.yaml"])
	// Processors should be sorted by name
	alphaIdx := strings.Index(webhookYAML, "alpha-webhook")
	betaIdx := strings.Index(webhookYAML, "beta-webhook")
	if alphaIdx < 0 || betaIdx < 0 {
		t.Fatalf("report-webhook.yaml missing processor entries\n---\n%s", webhookYAML)
	}
	if alphaIdx > betaIdx {
		t.Errorf("processors should be sorted by name (alpha=%d, beta=%d)", alphaIdx, betaIdx)
	}
}

func TestConfigReconcile_UpdateExistingConfigMap(t *testing.T) {
	cfg := newConfig("production")
	existingCM := newConfigMap("production-config", map[string]string{
		"puppet.conf": "old content",
	})

	c := setupTestClient(cfg, existingCM)
	r := newConfigReconciler(c)

	if _, err := r.Reconcile(testCtx(), testRequest("production")); err != nil {
		t.Fatalf("reconcile error: %v", err)
	}

	cm := &corev1.ConfigMap{}
	if err := c.Get(testCtx(), types.NamespacedName{Name: "production-config", Namespace: testNamespace}, cm); err != nil {
		t.Fatalf("ConfigMap not found: %v", err)
	}

	if cm.Data["puppet.conf"] == "old content" {
		t.Error("ConfigMap was not updated")
	}
	if !strings.Contains(cm.Data["puppet.conf"], "[main]") {
		t.Error("ConfigMap puppet.conf missing expected content")
	}
}

func int32Ptr(v int32) *int32 {
	return &v
}
