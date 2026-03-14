package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=nc
// +kubebuilder:printcolumn:name="URL",type=string,JSONPath=`.spec.url`
// +kubebuilder:printcolumn:name="Method",type=string,JSONPath=`.spec.request.method`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// NodeClassifier is the Schema for the nodeclassifiers API.
// It defines an External Node Classifier (ENC) endpoint for Puppet Server.
type NodeClassifier struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   NodeClassifierSpec   `json:"spec,omitempty"`
	Status NodeClassifierStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// NodeClassifierList contains a list of NodeClassifier.
type NodeClassifierList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []NodeClassifier `json:"items"`
}

// NodeClassifierSpec defines the desired state of NodeClassifier.
type NodeClassifierSpec struct {
	// URL is the classifier service base URL.
	URL string `json:"url"`

	// Request defines the HTTP request configuration.
	Request NodeClassifierRequest `json:"request"`

	// Response defines how to interpret the classifier response.
	Response NodeClassifierResponse `json:"response"`

	// TimeoutSeconds is the HTTP request timeout.
	// +kubebuilder:default=10
	// +optional
	TimeoutSeconds int32 `json:"timeoutSeconds,omitempty"`

	// Auth defines the authentication method. If omitted, no authentication is used.
	// +optional
	Auth *NodeClassifierAuth `json:"auth,omitempty"`

	// Cache defines optional disk caching for classifier responses.
	// +optional
	Cache *NodeClassifierCache `json:"cache,omitempty"`
}

// NodeClassifierRequest defines the HTTP request to the classifier.
// +kubebuilder:validation:XValidation:rule="self.method == 'GET' || self.method == 'POST'",message="method must be GET or POST"
// +kubebuilder:validation:XValidation:rule="self.method == 'POST' || !has(self.body) || size(self.body) == 0",message="body is only allowed with POST method"
type NodeClassifierRequest struct {
	// Method is the HTTP method (GET or POST).
	// +kubebuilder:default="GET"
	Method string `json:"method"`

	// Path is the URL path template. Use {certname} as placeholder.
	// +kubebuilder:default="/node/{certname}"
	Path string `json:"path"`

	// Body defines what to send as POST body.
	// facts: send Puppet facts as JSON (PE-compatible).
	// certname: send minimal JSON with certname only.
	// Empty or omitted: no body.
	// +kubebuilder:validation:Enum=facts;certname;""
	// +optional
	Body string `json:"body,omitempty"`
}

// NodeClassifierResponse defines how to interpret the classifier response.
type NodeClassifierResponse struct {
	// Format is the expected response format.
	// +kubebuilder:validation:Enum=yaml;json
	// +kubebuilder:default="yaml"
	Format string `json:"format"`
}

// NodeClassifierAuth defines authentication for the classifier service.
// At most one method may be configured.
// +kubebuilder:validation:XValidation:rule="(has(self.mtls) && self.mtls ? 1 : 0) + (has(self.token) ? 1 : 0) + (has(self.bearer) ? 1 : 0) + (has(self.basic) ? 1 : 0) <= 1",message="at most one auth method may be set"
type NodeClassifierAuth struct {
	// MTLS uses the Puppet SSL certificates for mutual TLS authentication.
	// +optional
	MTLS bool `json:"mtls,omitempty"`

	// Token sends a token via a custom HTTP header (e.g. X-Authentication for PE).
	// +optional
	Token *TokenAuth `json:"token,omitempty"`

	// Bearer sends a Bearer token via the Authorization header.
	// +optional
	Bearer *SecretKeySelector `json:"bearer,omitempty"`

	// Basic uses HTTP Basic Authentication.
	// +optional
	Basic *BasicAuth `json:"basic,omitempty"`
}

// TokenAuth sends a token via a custom HTTP header.
type TokenAuth struct {
	// Header is the HTTP header name (e.g. X-Authentication).
	Header string `json:"header"`

	// SecretKeyRef references the Secret key containing the token value.
	SecretKeyRef SecretKeyRef `json:"secretKeyRef"`
}

// BasicAuth defines HTTP Basic Authentication credentials from a Secret.
type BasicAuth struct {
	// SecretRef references the Secret containing username and password.
	SecretRef BasicAuthSecretRef `json:"secretRef"`
}

// BasicAuthSecretRef references username and password keys within a Secret.
type BasicAuthSecretRef struct {
	// Name is the name of the Secret.
	Name string `json:"name"`

	// UsernameKey is the key within the Secret containing the username.
	// +kubebuilder:default="username"
	UsernameKey string `json:"usernameKey"`

	// PasswordKey is the key within the Secret containing the password.
	// +kubebuilder:default="password"
	PasswordKey string `json:"passwordKey"`
}

// NodeClassifierCache defines cache settings for the ENC binary.
type NodeClassifierCache struct {
	// Enabled activates disk caching of classifier responses.
	// +kubebuilder:default=false
	Enabled bool `json:"enabled"`

	// Directory is the cache directory path inside the container.
	// +kubebuilder:default="/var/cache/openvox-enc"
	// +optional
	Directory string `json:"directory,omitempty"`
}

// NodeClassifierPhase represents the current lifecycle phase.
// +kubebuilder:validation:Enum=Active;Error
type NodeClassifierPhase string

const (
	NodeClassifierPhaseActive NodeClassifierPhase = "Active"
	NodeClassifierPhaseError  NodeClassifierPhase = "Error"
)

// NodeClassifierStatus defines the observed state of NodeClassifier.
type NodeClassifierStatus struct {
	// Phase is the current lifecycle phase.
	// +optional
	Phase NodeClassifierPhase `json:"phase,omitempty"`

	// Conditions represent the latest available observations.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// Condition types for NodeClassifier.
const (
	ConditionNodeClassifierReady = "Ready"
)

func init() {
	SchemeBuilder.Register(&NodeClassifier{}, &NodeClassifierList{})
}
