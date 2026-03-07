package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// OpenVoxServerSpec defines the desired state of OpenVoxServer.
type OpenVoxServerSpec struct {
	// Image defines the container image to use.
	Image ImageSpec `json:"image"`

	// CA defines the Certificate Authority configuration.
	// +optional
	CA CASpec `json:"ca,omitempty"`

	// Compilers defines the compiler pool configuration.
	// +optional
	Compilers CompilerSpec `json:"compilers,omitempty"`

	// Puppet defines puppet.conf settings rendered as a ConfigMap.
	// +optional
	Puppet PuppetSpec `json:"puppet,omitempty"`

	// PuppetDB defines the PuppetDB connection settings.
	// +optional
	PuppetDB PuppetDBSpec `json:"puppetdb,omitempty"`

	// Code defines the code deployment strategy (r10k or PVC).
	// +optional
	Code CodeSpec `json:"code,omitempty"`
}

// ImageSpec defines the container image reference.
type ImageSpec struct {
	// Repository is the container image repository.
	// +kubebuilder:default="ghcr.io/slauger/openvoxserver"
	Repository string `json:"repository"`

	// Tag is the container image tag.
	Tag string `json:"tag"`

	// PullPolicy defines the image pull policy.
	// +kubebuilder:default="IfNotPresent"
	// +optional
	PullPolicy corev1.PullPolicy `json:"pullPolicy,omitempty"`

	// PullSecrets is a list of image pull secrets.
	// +optional
	PullSecrets []corev1.LocalObjectReference `json:"pullSecrets,omitempty"`
}

// CASpec defines the Certificate Authority configuration.
type CASpec struct {
	// Enabled controls whether the built-in CA is used.
	// Set to false to use an external CA.
	// +kubebuilder:default=true
	Enabled bool `json:"enabled"`

	// Autosign controls certificate autosigning.
	// Can be "true", "false", or a path to an autosign script.
	// +kubebuilder:default="true"
	// +optional
	Autosign string `json:"autosign,omitempty"`

	// TTL is the CA certificate TTL in seconds.
	// +kubebuilder:default=157680000
	// +optional
	TTL int64 `json:"ttl,omitempty"`

	// AllowSubjectAltNames controls whether SANs are allowed in CSRs.
	// +kubebuilder:default=true
	// +optional
	AllowSubjectAltNames bool `json:"allowSubjectAltNames,omitempty"`

	// Certname is the CA server's certificate name.
	// +kubebuilder:default="puppet"
	// +optional
	Certname string `json:"certname,omitempty"`

	// DNSAltNames is a list of DNS alternative names for the CA certificate.
	// +optional
	DNSAltNames []string `json:"dnsAltNames,omitempty"`

	// Storage defines the PVC settings for CA data.
	// +optional
	Storage StorageSpec `json:"storage,omitempty"`

	// Resources defines the compute resources for the CA server.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// JavaArgs defines the JVM arguments for the CA server.
	// +kubebuilder:default="-Xms512m -Xmx1024m"
	// +optional
	JavaArgs string `json:"javaArgs,omitempty"`

	// IntermediateCA configures an intermediate CA setup.
	// +optional
	IntermediateCA IntermediateCASpec `json:"intermediateCA,omitempty"`
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

// CompilerSpec defines the compiler pool configuration.
type CompilerSpec struct {
	// Replicas is the number of compiler instances.
	// Set to 0 to only run a CA server without separate compilers.
	// +kubebuilder:default=1
	// +optional
	Replicas *int32 `json:"replicas,omitempty"`

	// Autoscaling defines HPA settings for compilers.
	// +optional
	Autoscaling AutoscalingSpec `json:"autoscaling,omitempty"`

	// DNSAltNames is a list of DNS alternative names for compiler certificates.
	// +optional
	DNSAltNames []string `json:"dnsAltNames,omitempty"`

	// Resources defines the compute resources for compilers.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// JavaArgs defines the JVM arguments for compilers.
	// +kubebuilder:default="-Xms512m -Xmx1024m"
	// +optional
	JavaArgs string `json:"javaArgs,omitempty"`

	// MaxActiveInstances is the number of JRuby instances per compiler.
	// +kubebuilder:default=2
	// +optional
	MaxActiveInstances int32 `json:"maxActiveInstances,omitempty"`
}

// AutoscalingSpec defines HPA settings.
type AutoscalingSpec struct {
	// Enabled activates horizontal pod autoscaling.
	// +kubebuilder:default=false
	Enabled bool `json:"enabled"`

	// MinReplicas is the minimum number of replicas.
	// +kubebuilder:default=1
	// +optional
	MinReplicas *int32 `json:"minReplicas,omitempty"`

	// MaxReplicas is the maximum number of replicas.
	// +kubebuilder:default=5
	// +optional
	MaxReplicas int32 `json:"maxReplicas,omitempty"`

	// TargetCPU is the target CPU utilization percentage.
	// +kubebuilder:default=75
	// +optional
	TargetCPU int32 `json:"targetCPU,omitempty"`
}

// PuppetSpec defines puppet.conf settings.
type PuppetSpec struct {
	// ServerPort is the puppet server port.
	// +kubebuilder:default=8140
	// +optional
	ServerPort int32 `json:"serverPort,omitempty"`

	// EnvironmentPath is the path to puppet environments.
	// +kubebuilder:default="/etc/puppetlabs/code/environments"
	// +optional
	EnvironmentPath string `json:"environmentPath,omitempty"`

	// EnvironmentTimeout controls how long puppet caches environments.
	// +kubebuilder:default="unlimited"
	// +optional
	EnvironmentTimeout string `json:"environmentTimeout,omitempty"`

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

	// ExtraConfig is a map of additional puppet.conf entries.
	// +optional
	ExtraConfig map[string]string `json:"extraConfig,omitempty"`
}

// PuppetDBSpec defines the PuppetDB connection.
type PuppetDBSpec struct {
	// Enabled controls whether PuppetDB integration is active.
	// +kubebuilder:default=true
	Enabled bool `json:"enabled"`

	// ServerURLs is a list of PuppetDB server URLs.
	// +optional
	ServerURLs []string `json:"serverUrls,omitempty"`
}

// CodeSpec defines the code deployment strategy.
type CodeSpec struct {
	// R10k configures r10k-based code deployment.
	// +optional
	R10k R10kSpec `json:"r10k,omitempty"`

	// Volume configures PVC-based code storage.
	// +optional
	Volume CodeVolumeSpec `json:"volume,omitempty"`
}

// R10kSpec defines r10k deployment settings.
type R10kSpec struct {
	// Enabled activates r10k code deployment.
	// +kubebuilder:default=false
	Enabled bool `json:"enabled"`

	// Image defines the r10k container image.
	// +optional
	Image ImageSpec `json:"image,omitempty"`

	// Repository is the Git repository URL for r10k.
	// +optional
	Repository string `json:"repository,omitempty"`

	// Schedule is the CronJob schedule for periodic code sync.
	// +optional
	Schedule string `json:"schedule,omitempty"`
}

// CodeVolumeSpec defines PVC settings for code storage.
type CodeVolumeSpec struct {
	// ExistingClaim is the name of an existing PVC to use.
	// +optional
	ExistingClaim string `json:"existingClaim,omitempty"`

	// Size is the requested storage size for a new PVC.
	// +kubebuilder:default="5Gi"
	// +optional
	Size string `json:"size,omitempty"`
}

// OpenVoxServerPhase represents the current lifecycle phase.
// +kubebuilder:validation:Enum=Pending;CASetup;WaitingForCA;Running;Error
type OpenVoxServerPhase string

const (
	PhasePending      OpenVoxServerPhase = "Pending"
	PhaseCASetup      OpenVoxServerPhase = "CASetup"
	PhaseWaitingForCA OpenVoxServerPhase = "WaitingForCA"
	PhaseRunning      OpenVoxServerPhase = "Running"
	PhaseError        OpenVoxServerPhase = "Error"
)

// OpenVoxServerStatus defines the observed state of OpenVoxServer.
type OpenVoxServerStatus struct {
	// Phase is the current lifecycle phase.
	// +optional
	Phase OpenVoxServerPhase `json:"phase,omitempty"`

	// CAReady indicates whether the CA is initialized and running.
	// +optional
	CAReady bool `json:"caReady,omitempty"`

	// CASecretName is the name of the Secret containing the CA certificates.
	// +optional
	CASecretName string `json:"caSecretName,omitempty"`

	// CompilersReady is the number of ready compiler replicas.
	// +optional
	CompilersReady int32 `json:"compilersReady,omitempty"`

	// CompilersDesired is the desired number of compiler replicas.
	// +optional
	CompilersDesired int32 `json:"compilersDesired,omitempty"`

	// Conditions represent the latest available observations.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// Condition types for OpenVoxServer.
const (
	ConditionCAInitialized = "CAInitialized"
	ConditionCAServerReady = "CAServerReady"
	ConditionCompilersReady = "CompilersReady"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=ovs
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="CA Ready",type=boolean,JSONPath=`.status.caReady`
// +kubebuilder:printcolumn:name="Compilers",type=string,JSONPath=`.status.compilersReady`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// OpenVoxServer is the Schema for the openvoxservers API.
type OpenVoxServer struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   OpenVoxServerSpec   `json:"spec,omitempty"`
	Status OpenVoxServerStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// OpenVoxServerList contains a list of OpenVoxServer.
type OpenVoxServerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []OpenVoxServer `json:"items"`
}

func init() {
	SchemeBuilder.Register(&OpenVoxServer{}, &OpenVoxServerList{})
}
