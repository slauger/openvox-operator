package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Environment",type=string,JSONPath=`.spec.environmentRef`
// +kubebuilder:printcolumn:name="Pools",type=string,JSONPath=`.spec.poolRefs`
// +kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=`.spec.replicas`
// +kubebuilder:printcolumn:name="Ready",type=integer,JSONPath=`.status.ready`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Server is the Schema for the servers API.
// It manages a pool of OpenVox Server instances as a Deployment.
type Server struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ServerSpec   `json:"spec,omitempty"`
	Status ServerStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ServerList contains a list of Server.
type ServerList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Server `json:"items"`
}

// ServerSpec defines the desired state of Server.
type ServerSpec struct {
	// EnvironmentRef references the Environment this Server belongs to.
	EnvironmentRef string `json:"environmentRef"`

	// PoolRefs references one or more Pools this Server joins.
	// For each Pool, Pods get a pool label and are selected by that Pool's Service.
	// +optional
	PoolRefs []string `json:"poolRefs,omitempty"`

	// Image overrides the Environment's default image.
	// +optional
	Image ImageSpec `json:"image,omitempty"`

	// CA enables the Certificate Authority role on this Server.
	// When true, pods mount the CA data PVC and run as CA.
	// +kubebuilder:default=false
	// +optional
	CA bool `json:"ca,omitempty"`

	// Compiler enables the compiler role on this Server.
	// Servers are compilers by default.
	// A Server with both CA and Compiler serves as a combined CA+compiler.
	// +kubebuilder:default=true
	// +optional
	Compiler bool `json:"compiler,omitempty"`

	// Replicas is the number of Server instances.
	// +kubebuilder:default=1
	// +optional
	Replicas *int32 `json:"replicas,omitempty"`

	// Autoscaling defines HPA settings.
	// +optional
	Autoscaling AutoscalingSpec `json:"autoscaling,omitempty"`

	// Resources defines the compute resources for Server Pods.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// JavaArgs defines the JVM arguments.
	// +kubebuilder:default="-Xms512m -Xmx1024m"
	// +optional
	JavaArgs string `json:"javaArgs,omitempty"`

	// MaxActiveInstances is the number of JRuby instances per Pod.
	// +kubebuilder:default=2
	// +optional
	MaxActiveInstances int32 `json:"maxActiveInstances,omitempty"`

	// Code overrides the Environment's code volume for this Server.
	// +optional
	Code *CodeSpec `json:"code,omitempty"`

	// DNSAltNames is a list of DNS alternative names for the server certificate.
	// +optional
	DNSAltNames []string `json:"dnsAltNames,omitempty"`
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

// ServerPhase represents the current lifecycle phase of a Server.
// +kubebuilder:validation:Enum=Pending;WaitingForCA;Running;Error
type ServerPhase string

const (
	ServerPhasePending      ServerPhase = "Pending"
	ServerPhaseWaitingForCA ServerPhase = "WaitingForCA"
	ServerPhaseRunning      ServerPhase = "Running"
	ServerPhaseError        ServerPhase = "Error"
)

// ServerStatus defines the observed state of Server.
type ServerStatus struct {
	// Phase is the current lifecycle phase.
	// +optional
	Phase ServerPhase `json:"phase,omitempty"`

	// Ready is the number of ready replicas.
	// +optional
	Ready int32 `json:"ready,omitempty"`

	// Desired is the desired number of replicas.
	// +optional
	Desired int32 `json:"desired,omitempty"`

	// Conditions represent the latest available observations.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// Condition types for Server.
const (
	ConditionSSLBootstrapped = "SSLBootstrapped"
	ConditionServerReady     = "Ready"
)

func init() {
	SchemeBuilder.Register(&Server{}, &ServerList{})
}
