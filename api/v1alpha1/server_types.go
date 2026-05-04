package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Config",type=string,JSONPath=`.spec.configRef`
// +kubebuilder:printcolumn:name="CA",type=boolean,JSONPath=`.spec.ca`
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
	// ConfigRef references the Config this Server belongs to.
	ConfigRef string `json:"configRef"`

	// CertificateRef references the Certificate whose SSL Secret is mounted into the Server pods.
	CertificateRef string `json:"certificateRef"`

	// PoolRefs lists the Pools this Server joins.
	// The Server controller adds a pool label for each entry, making the pod
	// selectable by the corresponding Pool's Service.
	// +optional
	PoolRefs []string `json:"poolRefs,omitempty"`

	// Image overrides the Config's default image.
	// +optional
	Image ImageSpec `json:"image,omitempty"`

	// CA enables the Certificate Authority role on this Server.
	// When true, pods mount the CA data PVC and run as CA.
	// +kubebuilder:default=false
	// +optional
	CA bool `json:"ca,omitempty"`

	// Server enables the server role (catalog compilation, file serving).
	// A Server with both CA and Server serves as a combined CA+server.
	// +kubebuilder:default=true
	// +optional
	Server bool `json:"server,omitempty"`

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
	// +kubebuilder:default=1
	// +optional
	MaxActiveInstances int32 `json:"maxActiveInstances,omitempty"`

	// Code overrides the Config's code volume for this Server.
	// +optional
	Code *CodeSpec `json:"code,omitempty"`

	// TopologySpreadConstraints controls how pods are spread across topology domains.
	// +optional
	TopologySpreadConstraints []corev1.TopologySpreadConstraint `json:"topologySpreadConstraints,omitempty"`

	// Affinity defines pod affinity/anti-affinity rules.
	// +optional
	Affinity *corev1.Affinity `json:"affinity,omitempty"`

	// PriorityClassName is the name of the PriorityClass for the Server pods.
	// +optional
	PriorityClassName string `json:"priorityClassName,omitempty"`

	// PDB defines PodDisruptionBudget settings.
	// +optional
	PDB *PDBSpec `json:"pdb,omitempty"`

	// NetworkPolicy defines NetworkPolicy settings.
	// +optional
	NetworkPolicy *NetworkPolicySpec `json:"networkPolicy,omitempty"`
}

// PDBSpec defines PodDisruptionBudget settings.
type PDBSpec struct {
	// Enabled activates the PodDisruptionBudget.
	// +kubebuilder:default=false
	Enabled bool `json:"enabled"`

	// MinAvailable is the minimum number of pods that must be available.
	// Mutually exclusive with MaxUnavailable.
	// +optional
	MinAvailable *intstr.IntOrString `json:"minAvailable,omitempty"`

	// MaxUnavailable is the maximum number of pods that can be unavailable.
	// Mutually exclusive with MinAvailable.
	// +optional
	MaxUnavailable *intstr.IntOrString `json:"maxUnavailable,omitempty"`
}

// NetworkPolicySpec defines NetworkPolicy settings.
type NetworkPolicySpec struct {
	// Enabled activates the NetworkPolicy.
	// +kubebuilder:default=false
	Enabled bool `json:"enabled"`

	// AdditionalIngress defines extra ingress rules appended to the defaults.
	// +optional
	AdditionalIngress []networkingv1.NetworkPolicyIngressRule `json:"additionalIngress,omitempty"`
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
// +kubebuilder:validation:Enum=Pending;WaitingForCert;Running;Error
type ServerPhase string

const (
	ServerPhasePending        ServerPhase = "Pending"
	ServerPhaseWaitingForCert ServerPhase = "WaitingForCert"
	ServerPhaseRunning        ServerPhase = "Running"
	ServerPhaseError          ServerPhase = "Error"
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
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(GroupVersion, &Server{}, &ServerList{})
		return nil
	})
}
