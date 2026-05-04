package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=rp
// +kubebuilder:printcolumn:name="URL",type=string,JSONPath=`.spec.url`
// +kubebuilder:printcolumn:name="Processor",type=string,JSONPath=`.spec.processor`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// ReportProcessor is the Schema for the reportprocessors API.
// It defines a report forwarding endpoint for Puppet Server.
type ReportProcessor struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ReportProcessorSpec   `json:"spec,omitempty"`
	Status ReportProcessorStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ReportProcessorList contains a list of ReportProcessor.
type ReportProcessorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ReportProcessor `json:"items"`
}

// ReportProcessorSpec defines the desired state of ReportProcessor.
type ReportProcessorSpec struct {
	// ConfigRef references the Config this ReportProcessor belongs to.
	// +kubebuilder:validation:MinLength=1
	ConfigRef string `json:"configRef"`

	// Processor is the report processor type.
	// Use "puppetdb" for PuppetDB Wire Format v8 transformation.
	// Empty string means generic (forward report JSON as-is).
	// +kubebuilder:validation:Enum=puppetdb;""
	// +optional
	Processor string `json:"processor,omitempty"`

	// URL is the endpoint to forward reports to.
	URL string `json:"url"`

	// TimeoutSeconds is the HTTP request timeout.
	// +kubebuilder:default=30
	// +optional
	TimeoutSeconds int32 `json:"timeoutSeconds,omitempty"`

	// Auth defines the authentication method. If omitted, no authentication is used.
	// +optional
	Auth *ReportProcessorAuth `json:"auth,omitempty"`

	// Headers defines custom HTTP headers to send with each request.
	// +optional
	Headers []HTTPHeader `json:"headers,omitempty"`
}

// ReportProcessorAuth defines authentication for the report processor endpoint.
// At most one method may be configured.
// +kubebuilder:validation:XValidation:rule="(has(self.mtls) && self.mtls ? 1 : 0) + (has(self.token) ? 1 : 0) + (has(self.bearer) ? 1 : 0) + (has(self.basic) ? 1 : 0) <= 1",message="at most one auth method may be set"
type ReportProcessorAuth struct {
	// MTLS uses the Puppet SSL certificates for mutual TLS authentication.
	// +optional
	MTLS bool `json:"mtls,omitempty"`

	// Token sends a token via a custom HTTP header.
	// +optional
	Token *TokenAuth `json:"token,omitempty"`

	// Bearer sends a Bearer token via the Authorization header.
	// +optional
	Bearer *SecretKeySelector `json:"bearer,omitempty"`

	// Basic uses HTTP Basic Authentication.
	// +optional
	Basic *BasicAuth `json:"basic,omitempty"`
}

// HTTPHeader defines a custom HTTP header.
// Either value or valueFrom may be set, not both.
// +kubebuilder:validation:XValidation:rule="!(has(self.value) && size(self.value) > 0 && has(self.valueFrom))",message="value and valueFrom are mutually exclusive"
type HTTPHeader struct {
	// Name is the HTTP header name.
	Name string `json:"name"`

	// Value is a literal header value.
	// +optional
	Value string `json:"value,omitempty"`

	// ValueFrom references a Secret or ConfigMap for the header value.
	// +optional
	ValueFrom *HTTPHeaderValueFrom `json:"valueFrom,omitempty"`
}

// HTTPHeaderValueFrom references a value from a Secret or ConfigMap.
type HTTPHeaderValueFrom struct {
	// SecretKeyRef references a key in a Secret.
	// +optional
	SecretKeyRef *SecretKeyRef `json:"secretKeyRef,omitempty"`

	// ConfigMapKeyRef references a key in a ConfigMap.
	// +optional
	ConfigMapKeyRef *ConfigMapKeyRef `json:"configMapKeyRef,omitempty"`
}

// ConfigMapKeyRef references a specific key within a ConfigMap.
type ConfigMapKeyRef struct {
	// Name is the name of the ConfigMap.
	Name string `json:"name"`

	// Key is the key within the ConfigMap.
	Key string `json:"key"`
}

// ReportProcessorPhase represents the current lifecycle phase.
// +kubebuilder:validation:Enum=Active;Error
type ReportProcessorPhase string

const (
	ReportProcessorPhaseActive ReportProcessorPhase = "Active"
	ReportProcessorPhaseError  ReportProcessorPhase = "Error"
)

// ReportProcessorStatus defines the observed state of ReportProcessor.
type ReportProcessorStatus struct {
	// Phase is the current lifecycle phase.
	// +optional
	Phase ReportProcessorPhase `json:"phase,omitempty"`

	// Conditions represent the latest available observations.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// Condition types for ReportProcessor.
const (
	ConditionReportProcessorReady = "Ready"
)

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(GroupVersion, &ReportProcessor{}, &ReportProcessorList{})
		return nil
	})
}
