package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Environment",type=string,JSONPath=`.spec.environmentRef`
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.service.type`
// +kubebuilder:printcolumn:name="Endpoints",type=integer,JSONPath=`.status.endpoints`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Pool is the Schema for the pools API.
// It owns a Kubernetes Service that selects Server Pods matching the Pool's Selector.
type Pool struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PoolSpec   `json:"spec,omitempty"`
	Status PoolStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// PoolList contains a list of Pool.
type PoolList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Pool `json:"items"`
}

// PoolSpec defines the desired state of Pool.
type PoolSpec struct {
	// EnvironmentRef references the Environment this Pool belongs to.
	EnvironmentRef string `json:"environmentRef"`

	// Selector is a set of labels used to select Server Pods for this Pool's Service.
	// The environment label is always added automatically.
	// If empty or nil, the Pool selects all Server Pods in the Environment.
	// +optional
	Selector map[string]string `json:"selector,omitempty"`

	// Service defines the Kubernetes Service configuration.
	// +optional
	Service PoolServiceSpec `json:"service,omitempty"`
}

// PoolServiceSpec defines the Service managed by the Pool.
type PoolServiceSpec struct {
	// Type is the Service type (ClusterIP, LoadBalancer, NodePort).
	// +kubebuilder:default="ClusterIP"
	// +optional
	Type corev1.ServiceType `json:"type,omitempty"`

	// Port is the Service port.
	// +kubebuilder:default=8140
	// +optional
	Port int32 `json:"port,omitempty"`

	// NodePort is the port on each node for type NodePort or LoadBalancer.
	// If unset, Kubernetes auto-assigns a port from the node port range.
	// +optional
	NodePort int32 `json:"nodePort,omitempty"`

	// Annotations are additional annotations for the Service.
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`

	// Labels are additional labels for the Service.
	// +optional
	Labels map[string]string `json:"labels,omitempty"`

	// ExternalIPs is a list of IP addresses for which nodes in the cluster
	// will also accept traffic for this service.
	// +optional
	ExternalIPs []string `json:"externalIPs,omitempty"`
}

// PoolStatus defines the observed state of Pool.
type PoolStatus struct {
	// ServiceName is the name of the created Kubernetes Service.
	// +optional
	ServiceName string `json:"serviceName,omitempty"`

	// Endpoints is the number of Pods behind the Service.
	// +optional
	Endpoints int32 `json:"endpoints,omitempty"`
}

func init() {
	SchemeBuilder.Register(&Pool{}, &PoolList{})
}
