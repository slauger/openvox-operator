package main

import (
	"context"
	"flag"
	"os"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/metrics/filters"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
	ctrlwebhook "sigs.k8s.io/controller-runtime/pkg/webhook"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
	"github.com/slauger/openvox-operator/internal/controller"
	"github.com/slauger/openvox-operator/internal/webhook"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(openvoxv1alpha1.AddToScheme(scheme))
	utilruntime.Must(gwapiv1.Install(scheme))
}

func main() {
	var metricsAddr string
	var secureMetrics bool
	var metricsCertDir string
	var probeAddr string
	var enableLeaderElection bool
	var enableWebhooks bool
	var enableGatewayAPI bool
	var watchNamespace string

	flag.StringVar(&metricsAddr, "metrics-bind-address", ":8080", "The address the metric endpoint binds to.")
	flag.BoolVar(&secureMetrics, "metrics-secure", false,
		"Serve metrics over HTTPS and require an authenticated, authorized client. "+
			"Off by default so existing scrape configurations keep working; this is "+
			"expected to become the default in a later release.")
	flag.StringVar(&metricsCertDir, "metrics-cert-dir", "",
		"Directory holding tls.crt and tls.key for the metrics endpoint. "+
			"Empty means controller-runtime generates a self-signed certificate at startup. "+
			"Only used with --metrics-secure.")
	flag.StringVar(&probeAddr, "health-probe-bind-address", ":8081", "The address the probe endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "leader-elect", false,
		"Enable leader election for controller manager. "+
			"Enabling this will ensure there is only one active controller manager.")
	flag.BoolVar(&enableWebhooks, "enable-webhooks", false, "Enable admission webhooks.")
	flag.BoolVar(&enableGatewayAPI, "enable-gateway-api", true, "Enable Gateway API TLSRoute support. When enabled, the operator auto-detects Gateway API CRDs.")
	flag.StringVar(&watchNamespace, "watch-namespace", "",
		"Namespace to restrict the operator to. If empty, the operator watches all namespaces (cluster-wide).")

	opts := zap.Options{Development: false}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	mgrOptions := ctrl.Options{
		Scheme:                 scheme,
		Metrics:                metricsOptions(metricsAddr, secureMetrics, metricsCertDir),
		HealthProbeBindAddress: probeAddr,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "openvox-operator.voxpupuli.org",
	}

	if enableWebhooks {
		mgrOptions.WebhookServer = ctrlwebhook.NewServer(ctrlwebhook.Options{Port: 9443})
	}

	if watchNamespace != "" {
		setupLog.Info("watching single namespace", "namespace", watchNamespace)
		mgrOptions.Cache = cache.Options{
			DefaultNamespaces: map[string]cache.Config{
				watchNamespace: {},
			},
		}
		mgrOptions.LeaderElectionNamespace = watchNamespace
	} else {
		setupLog.Info("watching all namespaces (cluster-wide)")
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), mgrOptions)
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	// Field indexes must exist before any controller starts using them.
	if err := controller.SetupFieldIndexers(context.Background(), mgr); err != nil {
		setupLog.Error(err, "unable to set up field indexers")
		os.Exit(1)
	}

	if err = (&controller.ConfigReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorder("config-controller"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Config")
		os.Exit(1)
	}

	if err = (&controller.CertificateAuthorityReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorder("certificateauthority-controller"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "CertificateAuthority")
		os.Exit(1)
	}

	if err = (&controller.CertificateReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorder("certificate-controller"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Certificate")
		os.Exit(1)
	}

	gatewayAPIAvailable := false
	if enableGatewayAPI {
		if _, err := mgr.GetRESTMapper().RESTMapping(
			schema.GroupKind{Group: "gateway.networking.k8s.io", Kind: "TLSRoute"},
		); err == nil {
			gatewayAPIAvailable = true
			setupLog.Info("Gateway API detected, TLSRoute support enabled")
		} else {
			setupLog.Info("Gateway API not detected, TLSRoute support disabled")
		}
	} else {
		setupLog.Info("Gateway API disabled via --enable-gateway-api=false")
	}

	if err = (&controller.PoolReconciler{
		Client:              mgr.GetClient(),
		Scheme:              mgr.GetScheme(),
		Recorder:            mgr.GetEventRecorder("pool-controller"),
		GatewayAPIAvailable: gatewayAPIAvailable,
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Pool")
		os.Exit(1)
	}

	if err = (&controller.ServerReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorder("server-controller"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Server")
		os.Exit(1)
	}

	if err = (&controller.DatabaseReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorder("database-controller"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "Database")
		os.Exit(1)
	}

	if err = (&controller.ReportProcessorReconciler{
		Client:   mgr.GetClient(),
		Scheme:   mgr.GetScheme(),
		Recorder: mgr.GetEventRecorder("reportprocessor-controller"),
	}).SetupWithManager(mgr); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "ReportProcessor")
		os.Exit(1)
	}

	if enableWebhooks {
		if err := webhook.SetupWithManager(mgr); err != nil {
			setupLog.Error(err, "unable to set up webhooks")
			os.Exit(1)
		}
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up health check")
		os.Exit(1)
	}
	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		setupLog.Error(err, "unable to set up ready check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}
}

// metricsOptions builds the metrics server configuration.
//
// The endpoint used to be plaintext HTTP reachable by anything in the cluster.
// It carries no key material, but it does list every certificate the operator
// manages and when each expires - a ready-made inventory for picking a moment.
//
// With secure serving the filter authenticates each request against the API
// server (TokenReview) and checks authorization (SubjectAccessReview), so a
// scraper needs an explicit RBAC grant on /metrics.
//
// The certificate is self-signed and regenerated on restart. That is
// deliberate: the protection comes from the filter, not from the certificate,
// and requiring cert-manager for an in-cluster endpoint that exposes no
// secrets would buy little for the dependency. Scrapers verify it with
// insecureSkipVerify, or point at a cert-manager issued one.
func metricsOptions(addr string, secure bool, certDir string) metricsserver.Options {
	if !secure {
		return metricsserver.Options{BindAddress: addr}
	}
	opts := metricsserver.Options{
		BindAddress:    addr,
		SecureServing:  true,
		FilterProvider: filters.WithAuthenticationAndAuthorization,
	}
	// Without a directory controller-runtime generates a self-signed
	// certificate at startup and regenerates it on every restart, which forces
	// scrapers to skip verification. A directory - from cert-manager or
	// supplied by hand - gives them something to verify against.
	if certDir != "" {
		opts.CertDir = certDir
		opts.CertName = "tls.crt"
		opts.KeyName = "tls.key"
	}
	return opts
}
