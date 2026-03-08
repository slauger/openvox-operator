package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=env
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="CA Ready",type=boolean,JSONPath=`.status.caReady`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Environment is the Schema for the environments API.
// It manages shared configuration, CA lifecycle, and PuppetDB connection.
type Environment struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   EnvironmentSpec   `json:"spec,omitempty"`
	Status EnvironmentStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// EnvironmentList contains a list of Environment.
type EnvironmentList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Environment `json:"items"`
}

// EnvironmentSpec defines the desired state of Environment.
type EnvironmentSpec struct {
	// Image defines the default container image for all Servers in this Environment.
	Image ImageSpec `json:"image"`

	// CA defines the Certificate Authority configuration.
	// +optional
	CA CASpec `json:"ca,omitempty"`

	// PuppetDB defines the PuppetDB connection settings.
	// +optional
	PuppetDB PuppetDBSpec `json:"puppetdb,omitempty"`

	// Puppet defines shared puppet.conf settings.
	// +optional
	Puppet PuppetSpec `json:"puppet,omitempty"`

	// Code defines the PVC for Puppet code (environments directory).
	// All Servers in this Environment mount this PVC by default.
	// +optional
	Code *CodeSpec `json:"code,omitempty"`
}

// CodeSpec references an existing PVC containing Puppet code.
type CodeSpec struct {
	// ClaimName is the name of an existing PVC containing Puppet code.
	ClaimName string `json:"claimName"`
}

// EnvironmentPhase represents the current lifecycle phase.
// +kubebuilder:validation:Enum=Pending;CASetup;Running;Error
type EnvironmentPhase string

const (
	EnvironmentPhasePending EnvironmentPhase = "Pending"
	EnvironmentPhaseCASetup EnvironmentPhase = "CASetup"
	EnvironmentPhaseRunning EnvironmentPhase = "Running"
	EnvironmentPhaseError   EnvironmentPhase = "Error"
)

// EnvironmentStatus defines the observed state of Environment.
type EnvironmentStatus struct {
	// Phase is the current lifecycle phase.
	// +optional
	Phase EnvironmentPhase `json:"phase,omitempty"`

	// CAReady indicates whether the CA is initialized.
	// +optional
	CAReady bool `json:"caReady,omitempty"`

	// CASecretName is the name of the CA marker Secret.
	// +optional
	CASecretName string `json:"caSecretName,omitempty"`

	// CAServiceName is the name of the CA Service.
	// +optional
	CAServiceName string `json:"caServiceName,omitempty"`

	// Conditions represent the latest available observations.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// Condition types for Environment.
const (
	ConditionCAInitialized = "CAInitialized"
	ConditionConfigReady   = "ConfigReady"
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

// CASpec defines the Certificate Authority configuration.
type CASpec struct {
	// Certname is the CA server's certificate name.
	// +kubebuilder:default="puppet"
	// +optional
	Certname string `json:"certname,omitempty"`

	// DNSAltNames is a list of DNS alternative names for the CA certificate.
	// +optional
	DNSAltNames []string `json:"dnsAltNames,omitempty"`

	// TTL is the CA certificate TTL in seconds.
	// +kubebuilder:default=157680000
	// +optional
	TTL int64 `json:"ttl,omitempty"`

	// AllowSubjectAltNames controls whether SANs are allowed in CSRs.
	// +kubebuilder:default=true
	// +optional
	AllowSubjectAltNames bool `json:"allowSubjectAltNames,omitempty"`

	// Autosign controls certificate autosigning.
	// Can be "true", "false", or a path to an autosign script.
	// +kubebuilder:default="true"
	// +optional
	Autosign string `json:"autosign,omitempty"`

	// Storage defines the PVC settings for CA data.
	// +optional
	Storage StorageSpec `json:"storage,omitempty"`

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

// PuppetSpec defines puppet.conf settings.
type PuppetSpec struct {
	// EnvironmentTimeout controls how long puppet caches environments.
	// +kubebuilder:default="unlimited"
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

	// ExtraConfig is a map of additional puppet.conf entries.
	// +optional
	ExtraConfig map[string]string `json:"extraConfig,omitempty"`
}

// PuppetDBSpec defines the PuppetDB connection.
type PuppetDBSpec struct {
	// ServerURLs is a list of PuppetDB server URLs.
	// +optional
	ServerURLs []string `json:"serverUrls,omitempty"`
}

func init() {
	SchemeBuilder.Register(&Environment{}, &EnvironmentList{})
}
