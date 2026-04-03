package controller

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/events"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	gwapiv1 "sigs.k8s.io/gateway-api/apis/v1"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

const testNamespace = "default"

// testScheme returns a runtime.Scheme with all types needed by the controllers.
func testScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(clientgoscheme.AddToScheme(s))
	utilruntime.Must(openvoxv1alpha1.AddToScheme(s))
	utilruntime.Must(gwapiv1.Install(s))
	return s
}

// setupTestClient creates a fake client pre-loaded with the given objects.
// StatusSubresource is enabled for all CRD types that use status updates.
func setupTestClient(objs ...client.Object) client.Client {
	return fake.NewClientBuilder().
		WithScheme(testScheme()).
		WithObjects(objs...).
		WithStatusSubresource(
			&openvoxv1alpha1.Config{},
			&openvoxv1alpha1.Server{},
			&openvoxv1alpha1.Database{},
			&openvoxv1alpha1.Pool{},
			&openvoxv1alpha1.Certificate{},
			&openvoxv1alpha1.CertificateAuthority{},
			&openvoxv1alpha1.SigningPolicy{},
			&openvoxv1alpha1.NodeClassifier{},
			&openvoxv1alpha1.ReportProcessor{},
		).
		Build()
}

// testRecorder returns a fake event recorder.
func testRecorder() events.EventRecorder {
	return events.NewFakeRecorder(100)
}

// testRequest returns a ctrl.Request for the given name in the test namespace.
func testRequest(name string) ctrl.Request {
	return ctrl.Request{
		NamespacedName: client.ObjectKey{Name: name, Namespace: testNamespace},
	}
}

// testCtx returns a background context.
func testCtx() context.Context {
	return context.Background()
}

// --- Object builders ---

type configOption func(*openvoxv1alpha1.Config)

func newConfig(name string, opts ...configOption) *openvoxv1alpha1.Config {
	cfg := &openvoxv1alpha1.Config{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
		},
		Spec: openvoxv1alpha1.ConfigSpec{
			Image: openvoxv1alpha1.ImageSpec{
				Repository: "ghcr.io/slauger/openvox-server",
				Tag:        "latest",
				PullPolicy: corev1.PullIfNotPresent,
			},
			Puppet: openvoxv1alpha1.PuppetSpec{
				EnvironmentTimeout: "unlimited",
				EnvironmentPath:    "/etc/puppetlabs/code/environments",
				HieraConfig:        "$confdir/hiera.yaml",
				Storeconfigs:       true,
				StoreBackend:       "puppetdb",
				Reports:            "puppetdb",
			},
		},
	}
	for _, o := range opts {
		o(cfg)
	}
	return cfg
}

func withAuthorityRef(ref string) configOption {
	return func(c *openvoxv1alpha1.Config) {
		c.Spec.AuthorityRef = ref
	}
}

func withNodeClassifierRef(ref string) configOption {
	return func(c *openvoxv1alpha1.Config) {
		c.Spec.NodeClassifierRef = ref
	}
}

func withReadOnlyRootFS(v bool) configOption {
	return func(c *openvoxv1alpha1.Config) {
		c.Spec.ReadOnlyRootFilesystem = v
	}
}

func withCodeImage(image string) configOption {
	return func(c *openvoxv1alpha1.Config) {
		c.Spec.Code = &openvoxv1alpha1.CodeSpec{Image: image}
	}
}

func withCodePVC(claimName string) configOption {
	return func(c *openvoxv1alpha1.Config) {
		c.Spec.Code = &openvoxv1alpha1.CodeSpec{ClaimName: claimName}
	}
}

func withLogging(level string, loggers map[string]string) configOption {
	return func(c *openvoxv1alpha1.Config) {
		c.Spec.Logging = &openvoxv1alpha1.LoggingSpec{
			Level:   level,
			Loggers: loggers,
		}
	}
}

func withAuthorizationRules(rules []openvoxv1alpha1.AuthorizationRule) configOption {
	return func(c *openvoxv1alpha1.Config) {
		c.Spec.PuppetServer.AuthorizationRules = rules
	}
}

func withPuppetServerSpec(ps openvoxv1alpha1.PuppetServerSpec) configOption {
	return func(c *openvoxv1alpha1.Config) {
		c.Spec.PuppetServer = ps
	}
}

func withDatabaseRef(ref string) configOption {
	return func(c *openvoxv1alpha1.Config) {
		c.Spec.DatabaseRef = ref
	}
}

func withPuppetSpec(p openvoxv1alpha1.PuppetSpec) configOption {
	return func(c *openvoxv1alpha1.Config) {
		c.Spec.Puppet = p
	}
}

type serverOption func(*openvoxv1alpha1.Server)

func newServer(name string, opts ...serverOption) *openvoxv1alpha1.Server {
	replicas := int32(1)
	s := &openvoxv1alpha1.Server{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
		},
		Spec: openvoxv1alpha1.ServerSpec{
			ConfigRef:      "production",
			CertificateRef: "production-cert",
			Server:         true,
			CA:             false,
			Replicas:       &replicas,
		},
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

func withCA(ca bool) serverOption {
	return func(s *openvoxv1alpha1.Server) {
		s.Spec.CA = ca
		if ca && !s.Spec.Server {
			s.Spec.Server = false
		}
	}
}

func withServerRole(server bool) serverOption {
	return func(s *openvoxv1alpha1.Server) {
		s.Spec.Server = server
	}
}

func withReplicas(r int32) serverOption {
	return func(s *openvoxv1alpha1.Server) {
		s.Spec.Replicas = &r
	}
}

func withPoolRefs(refs ...string) serverOption {
	return func(s *openvoxv1alpha1.Server) {
		s.Spec.PoolRefs = refs
	}
}

func withPDBEnabled(enabled bool) serverOption {
	return func(s *openvoxv1alpha1.Server) {
		s.Spec.PDB = &openvoxv1alpha1.PDBSpec{Enabled: enabled}
	}
}

func withServerNetworkPolicy(enabled bool) serverOption {
	return func(s *openvoxv1alpha1.Server) {
		s.Spec.NetworkPolicy = &openvoxv1alpha1.NetworkPolicySpec{Enabled: enabled}
	}
}

func withAutoscaling(enabled bool) serverOption {
	return func(s *openvoxv1alpha1.Server) {
		s.Spec.Autoscaling = openvoxv1alpha1.AutoscalingSpec{Enabled: enabled}
	}
}

type poolOption func(*openvoxv1alpha1.Pool)

func newPool(name string, opts ...poolOption) *openvoxv1alpha1.Pool {
	p := &openvoxv1alpha1.Pool{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
		},
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

func withServicePort(port int32) poolOption {
	return func(p *openvoxv1alpha1.Pool) {
		p.Spec.Service.Port = port
	}
}

func withServiceType(t corev1.ServiceType) poolOption {
	return func(p *openvoxv1alpha1.Pool) {
		p.Spec.Service.Type = t
	}
}

func withServiceAnnotations(a map[string]string) poolOption {
	return func(p *openvoxv1alpha1.Pool) {
		p.Spec.Service.Annotations = a
	}
}

func withRoute(enabled bool, hostname, gwName string) poolOption {
	return func(p *openvoxv1alpha1.Pool) {
		p.Spec.Route = &openvoxv1alpha1.PoolRouteSpec{
			Enabled:  enabled,
			Hostname: hostname,
			GatewayRef: openvoxv1alpha1.GatewayReference{
				Name: gwName,
			},
		}
	}
}

func newCertificate(name, authorityRef string, phase openvoxv1alpha1.CertificatePhase) *openvoxv1alpha1.Certificate {
	cert := &openvoxv1alpha1.Certificate{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
		},
		Spec: openvoxv1alpha1.CertificateSpec{
			AuthorityRef: authorityRef,
			Certname:     "puppet",
		},
	}
	cert.Status.Phase = phase
	if phase == openvoxv1alpha1.CertificatePhaseSigned {
		cert.Status.SecretName = name + "-tls"
	}
	return cert
}

type caOption func(*openvoxv1alpha1.CertificateAuthority)

func withExternal(url string) caOption {
	return func(ca *openvoxv1alpha1.CertificateAuthority) {
		if ca.Spec.External == nil {
			ca.Spec.External = &openvoxv1alpha1.ExternalCASpec{}
		}
		ca.Spec.External.URL = url
	}
}

func withExternalCASecret(ref string) caOption {
	return func(ca *openvoxv1alpha1.CertificateAuthority) {
		if ca.Spec.External == nil {
			ca.Spec.External = &openvoxv1alpha1.ExternalCASpec{}
		}
		ca.Spec.External.CASecretRef = ref
	}
}

func newCertificateAuthority(name string, opts ...caOption) *openvoxv1alpha1.CertificateAuthority {
	ca := &openvoxv1alpha1.CertificateAuthority{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
		},
		Spec: openvoxv1alpha1.CertificateAuthoritySpec{
			TTL:                          "5y",
			AllowSubjectAltNames:         true,
			AllowAuthorizationExtensions: true,
			EnableInfraCRL:               true,
			AllowAutoRenewal:             true,
			AutoRenewalCertTTL:           "90d",
		},
	}
	ca.Status.Phase = openvoxv1alpha1.CertificateAuthorityPhaseReady
	ca.Status.CASecretName = name + "-ca"
	for _, o := range opts {
		o(ca)
	}
	return ca
}

func newSigningPolicy(name, caRef string, any bool) *openvoxv1alpha1.SigningPolicy {
	return &openvoxv1alpha1.SigningPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
		},
		Spec: openvoxv1alpha1.SigningPolicySpec{
			CertificateAuthorityRef: caRef,
			Any:                     any,
		},
	}
}

func newNodeClassifier(name, url string) *openvoxv1alpha1.NodeClassifier {
	return &openvoxv1alpha1.NodeClassifier{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
		},
		Spec: openvoxv1alpha1.NodeClassifierSpec{
			URL: url,
			Request: openvoxv1alpha1.NodeClassifierRequest{
				Method: "GET",
				Path:   "/node/{certname}",
			},
			Response: openvoxv1alpha1.NodeClassifierResponse{
				Format: "yaml",
			},
			TimeoutSeconds: 10,
		},
	}
}

func newReportProcessor(name, configRef, url string) *openvoxv1alpha1.ReportProcessor {
	return &openvoxv1alpha1.ReportProcessor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
		},
		Spec: openvoxv1alpha1.ReportProcessorSpec{
			ConfigRef:      configRef,
			URL:            url,
			TimeoutSeconds: 30,
		},
	}
}

func newSecret(name string, data map[string][]byte) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
		},
		Data: data,
	}
}

func newConfigMap(name string, data map[string]string) *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
		},
		Data: data,
	}
}

func newEndpointSlice(name, serviceName string, readyCount int) *discoveryv1.EndpointSlice {
	eps := &discoveryv1.EndpointSlice{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
			Labels: map[string]string{
				"kubernetes.io/service-name": serviceName,
			},
		},
		AddressType: discoveryv1.AddressTypeIPv4,
	}
	ready := true
	for i := 0; i < readyCount; i++ {
		eps.Endpoints = append(eps.Endpoints, discoveryv1.Endpoint{
			Conditions: discoveryv1.EndpointConditions{
				Ready: &ready,
			},
		})
	}
	return eps
}

// --- Convenience for reconcilers ---

func newConfigReconciler(c client.Client) *ConfigReconciler {
	return &ConfigReconciler{
		Client:   c,
		Scheme:   testScheme(),
		Recorder: testRecorder(),
	}
}

func newServerReconciler(c client.Client) *ServerReconciler {
	return &ServerReconciler{
		Client:   c,
		Scheme:   testScheme(),
		Recorder: testRecorder(),
	}
}

func newPoolReconciler(c client.Client, gatewayAPI bool) *PoolReconciler {
	return &PoolReconciler{
		Client:              c,
		Scheme:              testScheme(),
		Recorder:            testRecorder(),
		GatewayAPIAvailable: gatewayAPI,
	}
}

func newCertificateReconciler(c client.Client) *CertificateReconciler {
	return &CertificateReconciler{
		Client:   c,
		Scheme:   testScheme(),
		Recorder: testRecorder(),
	}
}

func newCertificateAuthorityReconciler(c client.Client) *CertificateAuthorityReconciler {
	return &CertificateAuthorityReconciler{
		Client:   c,
		Scheme:   testScheme(),
		Recorder: testRecorder(),
	}
}

func newReportProcessorReconciler(c client.Client) *ReportProcessorReconciler {
	return &ReportProcessorReconciler{
		Client:   c,
		Scheme:   testScheme(),
		Recorder: testRecorder(),
	}
}

type databaseOption func(*openvoxv1alpha1.Database)

func newDatabase(name string, opts ...databaseOption) *openvoxv1alpha1.Database {
	replicas := int32(1)
	db := &openvoxv1alpha1.Database{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: testNamespace,
		},
		Spec: openvoxv1alpha1.DatabaseSpec{
			CertificateRef: "production-db-cert",
			Image: openvoxv1alpha1.ImageSpec{
				Repository: "ghcr.io/slauger/openvox-db",
				Tag:        "latest",
				PullPolicy: corev1.PullIfNotPresent,
			},
			Postgres: openvoxv1alpha1.PostgresSpec{
				Host:                 "pg-rw.openvox.svc",
				Port:                 5432,
				Database:             "openvoxdb",
				CredentialsSecretRef: "pg-credentials",
				SSLMode:              "require",
			},
			Replicas: &replicas,
		},
	}
	for _, o := range opts {
		o(db)
	}
	return db
}

func withDatabaseReplicas(r int32) databaseOption {
	return func(db *openvoxv1alpha1.Database) {
		db.Spec.Replicas = &r
	}
}

func withDatabaseNetworkPolicy(enabled bool) databaseOption {
	return func(db *openvoxv1alpha1.Database) {
		db.Spec.NetworkPolicy = &openvoxv1alpha1.NetworkPolicySpec{Enabled: enabled}
	}
}

func withDatabaseNetworkPolicyAdditionalIngress(rules []networkingv1.NetworkPolicyIngressRule) databaseOption {
	return func(db *openvoxv1alpha1.Database) {
		if db.Spec.NetworkPolicy == nil {
			db.Spec.NetworkPolicy = &openvoxv1alpha1.NetworkPolicySpec{Enabled: true}
		}
		db.Spec.NetworkPolicy.AdditionalIngress = rules
	}
}

func withDatabaseStatusURL(url string) databaseOption {
	return func(db *openvoxv1alpha1.Database) {
		db.Status.URL = url
	}
}

func newDatabaseReconciler(c client.Client) *DatabaseReconciler {
	return &DatabaseReconciler{
		Client:   c,
		Scheme:   testScheme(),
		Recorder: testRecorder(),
	}
}
