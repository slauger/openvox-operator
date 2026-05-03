package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Type",type=string,JSONPath=`.spec.service.type`
// +kubebuilder:printcolumn:name="Endpoints",type=integer,JSONPath=`.status.endpoints`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Pool is the Schema for the pools API.
// It owns a Kubernetes Service that selects Server Pods whose poolRefs include this Pool.
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
	// Service defines the Kubernetes Service configuration.
	// +optional
	Service PoolServiceSpec `json:"service,omitempty"`

	// Route configures external access via Gateway API TLSRoute.
	// +optional
	Route *PoolRouteSpec `json:"route,omitempty"`
}

// PoolRouteSpec configures a TLSRoute for SNI-based routing via Gateway API.
// +kubebuilder:validation:XValidation:rule="!self.enabled || size(self.hostname) > 0",message="hostname is required when route is enabled"
// +kubebuilder:validation:XValidation:rule="!self.enabled || size(self.gatewayRef.name) > 0",message="gatewayRef.name is required when route is enabled"
type PoolRouteSpec struct {
	// Enabled activates TLSRoute creation for this Pool.
	// +kubebuilder:default=false
	Enabled bool `json:"enabled"`

	// Hostname is the SNI hostname (e.g. "production.puppet.example.com").
	// +optional
	Hostname string `json:"hostname,omitempty"`

	// GatewayRef references the Gateway to attach the TLSRoute to.
	// +optional
	GatewayRef GatewayReference `json:"gatewayRef,omitempty"`

	// InjectDNSAltName automatically adds the hostname to the Certificate's dnsAltNames
	// of all Servers selected by this Pool.
	// +kubebuilder:default=false
	// +optional
	InjectDNSAltName bool `json:"injectDNSAltName,omitempty"`
}

// GatewayReference identifies a Gateway resource.
type GatewayReference struct {
	// Name of the Gateway.
	Name string `json:"name"`

	// SectionName of the Gateway listener.
	// +optional
	SectionName string `json:"sectionName,omitempty"`
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
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(GroupVersion, &Pool{}, &PoolList{})
		return nil
	})
}
