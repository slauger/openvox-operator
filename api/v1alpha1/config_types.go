package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=cfg
// +kubebuilder:printcolumn:name="CA",type=string,JSONPath=`.spec.authorityRef`
// +kubebuilder:printcolumn:name="Image",type=string,JSONPath=`.spec.image.tag`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Config is the Schema for the configs API.
// It manages shared configuration (puppet.conf, auth.conf) and OpenVox DB connection.
type Config struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ConfigSpec   `json:"spec,omitempty"`
	Status ConfigStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ConfigList contains a list of Config.
type ConfigList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Config `json:"items"`
}

// ConfigSpec defines the desired state of Config.
type ConfigSpec struct {
	// Image defines the default container image for all Servers in this Config.
	Image ImageSpec `json:"image"`

	// AuthorityRef references the CertificateAuthority used by this Config.
	// +optional
	AuthorityRef string `json:"authorityRef,omitempty"`

	// DatabaseRef references a Database whose status.URL is used for puppetdb.conf.
	// Mutually exclusive with PuppetDB.ServerURLs.
	// +optional
	DatabaseRef string `json:"databaseRef,omitempty"`

	// PuppetDB defines the OpenVox DB connection settings.
	// Mutually exclusive with DatabaseRef.
	// +optional
	PuppetDB PuppetDBSpec `json:"puppetdb,omitempty"`

	// Puppet defines shared puppet.conf settings.
	// +optional
	Puppet PuppetSpec `json:"puppet,omitempty"`

	// PuppetServer defines puppetserver.conf, webserver.conf and auth.conf settings.
	// +optional
	PuppetServer PuppetServerSpec `json:"puppetserver,omitempty"`

	// Logging defines logback.xml settings for Puppet Server logging.
	// +optional
	Logging *LoggingSpec `json:"logging,omitempty"`

	// Metrics defines metrics.conf settings for Puppet Server metrics.
	// +optional
	Metrics *MetricsSpec `json:"metrics,omitempty"`

	// NodeClassifierRef references the NodeClassifier used by this Config.
	// +optional
	NodeClassifierRef string `json:"nodeClassifierRef,omitempty"`

	// Code defines the Puppet code source for all Servers in this Config.
	// Only applied to Servers with server=true.
	// +optional
	Code *CodeSpec `json:"code,omitempty"`

	// ReadOnlyRootFilesystem enables read-only root filesystem on all Server pods.
	// When true, all writable paths are backed by emptyDir volumes and
	// server-var-dir is redirected to /run/puppetserver.
	// Experimental security hardening feature.
	// +kubebuilder:default=false
	// +optional
	ReadOnlyRootFilesystem bool `json:"readOnlyRootFilesystem,omitempty"`
}

// PuppetServerSpec defines puppetserver.conf, webserver.conf and auth.conf settings.
type PuppetServerSpec struct {
	// MaxRequestsPerInstance is the max number of requests per JRuby instance before it is restarted.
	// 0 means unlimited (no restart).
	// +kubebuilder:default=0
	// +optional
	MaxRequestsPerInstance int32 `json:"maxRequestsPerInstance,omitempty"`

	// BorrowTimeout is the timeout in milliseconds for borrowing a JRuby instance from the pool.
	// +kubebuilder:default=1200000
	// +optional
	BorrowTimeout int32 `json:"borrowTimeout,omitempty"`

	// CompileMode controls JRuby compilation mode.
	// +kubebuilder:validation:Enum=jit;off
	// +kubebuilder:default="off"
	// +optional
	CompileMode string `json:"compileMode,omitempty"`

	// ClientAuth controls SSL client authentication mode for the webserver.
	// +kubebuilder:validation:Enum=want;need;none
	// +kubebuilder:default="want"
	// +optional
	ClientAuth string `json:"clientAuth,omitempty"`

	// HTTPClient defines HTTP client settings for outgoing connections.
	// +optional
	HTTPClient *HTTPClientSpec `json:"httpClient,omitempty"`

	// AuthorizationRules defines custom authorization rules inserted before the deny-all rule.
	// +optional
	AuthorizationRules []AuthorizationRule `json:"authorizationRules,omitempty"`
}

// HTTPClientSpec defines HTTP client settings for Puppet Server.
type HTTPClientSpec struct {
	// ConnectTimeoutMs is the connection timeout in milliseconds.
	// +optional
	ConnectTimeoutMs *int32 `json:"connectTimeoutMs,omitempty"`

	// IdleTimeoutMs is the idle timeout in milliseconds.
	// +optional
	IdleTimeoutMs *int32 `json:"idleTimeoutMs,omitempty"`
}

// AuthorizationRule defines a custom auth.conf rule.
type AuthorizationRule struct {
	// Name is a descriptive name for the rule.
	Name string `json:"name"`

	// MatchRequest defines the request matching criteria.
	MatchRequest AuthorizationMatchRequest `json:"matchRequest"`

	// Allow specifies which clients are allowed. Use "*" for all authenticated clients.
	// +optional
	Allow string `json:"allow,omitempty"`

	// AllowUnauthenticated allows unauthenticated access when true.
	// +optional
	AllowUnauthenticated bool `json:"allowUnauthenticated,omitempty"`

	// Deny specifies which clients are denied. Use "*" for all clients.
	// +optional
	Deny string `json:"deny,omitempty"`

	// SortOrder controls rule evaluation order (lower = earlier).
	// +kubebuilder:default=500
	// +optional
	SortOrder int32 `json:"sortOrder,omitempty"`
}

// AuthorizationMatchRequest defines request matching criteria for an auth rule.
type AuthorizationMatchRequest struct {
	// Path is the URL path or pattern to match.
	Path string `json:"path"`

	// Type is the match type.
	// +kubebuilder:validation:Enum=path;regex
	// +kubebuilder:default="path"
	// +optional
	Type string `json:"type,omitempty"`

	// Method is the list of HTTP methods to match.
	// +optional
	Method []string `json:"method,omitempty"`
}

// LoggingSpec defines logback.xml settings for Puppet Server.
type LoggingSpec struct {
	// Level is the root log level.
	// +kubebuilder:validation:Enum=TRACE;DEBUG;INFO;WARN;ERROR
	// +kubebuilder:default="INFO"
	// +optional
	Level string `json:"level,omitempty"`

	// Loggers defines per-logger level overrides.
	// Keys are logger names, values are log levels (TRACE, DEBUG, INFO, WARN, ERROR).
	// +optional
	Loggers map[string]string `json:"loggers,omitempty"`
}

// MetricsSpec defines metrics.conf settings for Puppet Server.
type MetricsSpec struct {
	// Enabled activates Puppet Server metrics.
	// +kubebuilder:default=false
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// JMX configures JMX metrics reporting.
	// +optional
	JMX *JMXSpec `json:"jmx,omitempty"`

	// Graphite configures Graphite metrics reporting.
	// +optional
	Graphite *GraphiteSpec `json:"graphite,omitempty"`
}

// JMXSpec defines JMX metrics settings.
type JMXSpec struct {
	// Enabled activates JMX metrics.
	// +kubebuilder:default=false
	// +optional
	Enabled bool `json:"enabled,omitempty"`
}

// GraphiteSpec defines Graphite metrics settings.
type GraphiteSpec struct {
	// Enabled activates Graphite reporting.
	// +kubebuilder:default=false
	// +optional
	Enabled bool `json:"enabled,omitempty"`

	// Host is the Graphite server hostname.
	// +optional
	Host string `json:"host,omitempty"`

	// Port is the Graphite server port.
	// +kubebuilder:default=2003
	// +optional
	Port int32 `json:"port,omitempty"`

	// UpdateIntervalSeconds is the interval between metric reports.
	// +kubebuilder:default=60
	// +optional
	UpdateIntervalSeconds int32 `json:"updateIntervalSeconds,omitempty"`
}

// CodeSpec defines the source of Puppet code to mount into Server pods.
// Either ClaimName (PVC) or Image (OCI image volume) may be set, not both.
// +kubebuilder:validation:XValidation:rule="!(has(self.image) && size(self.image) > 0 && has(self.claimName) && size(self.claimName) > 0)",message="image and claimName are mutually exclusive"
// +kubebuilder:validation:XValidation:rule="(has(self.image) && size(self.image) > 0) || (has(self.claimName) && size(self.claimName) > 0)",message="either image or claimName must be set"
type CodeSpec struct {
	// ClaimName references an existing PVC containing Puppet code.
	// Mutually exclusive with Image.
	// +optional
	ClaimName string `json:"claimName,omitempty"`

	// Image is an OCI image reference containing Puppet code.
	// Mounted as a read-only image volume (Kubernetes 1.35+, or 1.31+ with ImageVolume feature gate).
	// Mutually exclusive with ClaimName.
	// +optional
	Image string `json:"image,omitempty"`

	// ImagePullPolicy defines when to pull the code image.
	// +kubebuilder:default="IfNotPresent"
	// +optional
	ImagePullPolicy corev1.PullPolicy `json:"imagePullPolicy,omitempty"`

	// ImagePullSecret references a Secret for pulling from private registries.
	// +optional
	ImagePullSecret string `json:"imagePullSecret,omitempty"`
}

// ConfigPhase represents the current lifecycle phase.
// +kubebuilder:validation:Enum=Pending;Running;Error
type ConfigPhase string

const (
	ConfigPhasePending ConfigPhase = "Pending"
	ConfigPhaseRunning ConfigPhase = "Running"
	ConfigPhaseError   ConfigPhase = "Error"
)

// ConfigStatus defines the observed state of Config.
type ConfigStatus struct {
	// Phase is the current lifecycle phase.
	// +optional
	Phase ConfigPhase `json:"phase,omitempty"`

	// Conditions represent the latest available observations.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// Condition types for Config.
const (
	ConditionConfigReady = "ConfigReady"
)

// --- Shared types used by multiple CRDs ---

// ImageSpec defines the container image reference.
type ImageSpec struct {
	// Repository is the container image repository.
	// +kubebuilder:default="ghcr.io/slauger/openvox-server"
	Repository string `json:"repository,omitempty"`

	// Tag is the container image tag.
	// +kubebuilder:default="latest"
	Tag string `json:"tag,omitempty"`

	// PullPolicy defines the image pull policy.
	// +kubebuilder:default="IfNotPresent"
	// +optional
	PullPolicy corev1.PullPolicy `json:"pullPolicy,omitempty"`

	// PullSecrets is a list of image pull secrets.
	// +optional
	PullSecrets []corev1.LocalObjectReference `json:"pullSecrets,omitempty"`
}

// IntermediateCASpec defines an intermediate CA configuration.
type IntermediateCASpec struct {
	// Enabled activates intermediate CA mode.
	// +kubebuilder:default=false
	Enabled bool `json:"enabled"`

	// SecretName is the name of the Secret containing ca.pem, key.pem, crl.pem.
	// +optional
	SecretName string `json:"secretName,omitempty"`
}

// StorageSpec defines PVC settings.
type StorageSpec struct {
	// Size is the requested storage size.
	// +kubebuilder:default="1Gi"
	// +optional
	Size string `json:"size,omitempty"`

	// StorageClass is the storage class name. Empty means default.
	// +optional
	StorageClass string `json:"storageClass,omitempty"`
}

// PuppetSpec defines puppet.conf settings.
type PuppetSpec struct {
	// EnvironmentTimeout controls how long puppet caches environments.
	// When unset, Puppet's default (0 = no caching) is used.
	// +optional
	EnvironmentTimeout string `json:"environmentTimeout,omitempty"`

	// EnvironmentPath is the path to puppet environments.
	// +kubebuilder:default="/etc/puppetlabs/code/environments"
	// +optional
	EnvironmentPath string `json:"environmentPath,omitempty"`

	// HieraConfig is the path to the hiera configuration.
	// +kubebuilder:default="$confdir/hiera.yaml"
	// +optional
	HieraConfig string `json:"hieraConfig,omitempty"`

	// Storeconfigs enables storeconfigs.
	// +kubebuilder:default=true
	// +optional
	Storeconfigs bool `json:"storeconfigs,omitempty"`

	// StoreBackend is the storeconfigs backend.
	// +kubebuilder:default="puppetdb"
	// +optional
	StoreBackend string `json:"storeBackend,omitempty"`

	// Reports defines which report processors to use.
	// +kubebuilder:default="puppetdb"
	// +optional
	Reports string `json:"reports,omitempty"`

	// ExtraConfig adds additional puppet.conf entries to specific INI sections.
	// +optional
	ExtraConfig *PuppetExtraConfig `json:"extraConfig,omitempty"`
}

// PuppetExtraConfig holds additional puppet.conf entries per INI section.
type PuppetExtraConfig struct {
	// Main adds entries to the [main] section.
	// +optional
	Main map[string]string `json:"main,omitempty"`

	// Server adds entries to the [server] section.
	// +optional
	Server map[string]string `json:"server,omitempty"`

	// Agent adds entries to the [agent] section.
	// +optional
	Agent map[string]string `json:"agent,omitempty"`
}

// PuppetDBSpec defines the OpenVox DB connection.
type PuppetDBSpec struct {
	// ServerURLs is a list of OpenVox DB server URLs.
	// +optional
	ServerURLs []string `json:"serverUrls,omitempty"`
}

func init() {
	SchemeBuilder.Register(&Config{}, &ConfigList{})
}
