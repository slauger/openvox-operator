package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=ca
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="NotAfter",type=date,JSONPath=`.status.notAfter`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// CertificateAuthority is the Schema for the certificateauthorities API.
// It manages the CA infrastructure: PVC, CA keys, CA public Secret, and signing policy.
type CertificateAuthority struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CertificateAuthoritySpec   `json:"spec,omitempty"`
	Status CertificateAuthorityStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// CertificateAuthorityList contains a list of CertificateAuthority.
type CertificateAuthorityList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CertificateAuthority `json:"items"`
}

// CertificateAuthoritySpec defines the desired state of CertificateAuthority.
type CertificateAuthoritySpec struct {
	// TTL is the CA certificate TTL as a duration string.
	// Supported units: s (seconds), m (minutes), h (hours), d (days), y (years).
	// Examples: "5y", "365d", "8760h", "157680000s"
	// Plain numbers are interpreted as seconds for backwards compatibility.
	// +kubebuilder:default="5y"
	// +optional
	TTL string `json:"ttl,omitempty"`

	// AllowSubjectAltNames controls whether SANs are allowed in CSRs.
	// +kubebuilder:default=true
	// +optional
	AllowSubjectAltNames bool `json:"allowSubjectAltNames,omitempty"`

	// AllowAuthorizationExtensions controls whether authorization extensions
	// (pp_role, pp_environment, etc.) are allowed in CSRs.
	// +kubebuilder:default=true
	// +optional
	AllowAuthorizationExtensions bool `json:"allowAuthorizationExtensions,omitempty"`

	// EnableInfraCRL enables infrastructure CRL for compile server revocation.
	// +kubebuilder:default=true
	// +optional
	EnableInfraCRL bool `json:"enableInfraCRL,omitempty"`

	// AllowAutoRenewal allows agents to automatically renew certificates before expiry.
	// +kubebuilder:default=true
	// +optional
	AllowAutoRenewal bool `json:"allowAutoRenewal,omitempty"`

	// AutoRenewalCertTTL is the TTL threshold for automatic certificate renewal.
	// Uses duration format: "90d", "30d", "2160h".
	// +kubebuilder:default="90d"
	// +optional
	AutoRenewalCertTTL string `json:"autoRenewalCertTTL,omitempty"`

	// Storage defines the PVC settings for CA data.
	// +optional
	Storage StorageSpec `json:"storage,omitempty"`

	// Resources defines the compute resources for the CA setup Job.
	// If not specified, defaults are applied (requests: 200m CPU, 768Mi memory; limits: 1 CPU, 1Gi memory).
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// CRLRefreshInterval defines how often the operator fetches the CRL from the CA
	// and updates the CRL Secret. Only applies to non-CA servers.
	// Uses Go duration format: "5m", "1h", "30s".
	// +kubebuilder:default="5m"
	// +optional
	CRLRefreshInterval string `json:"crlRefreshInterval,omitempty"`

	// IntermediateCA configures an intermediate CA setup.
	// +optional
	IntermediateCA IntermediateCASpec `json:"intermediateCA,omitempty"`
}

// CertificateAuthorityPhase represents the current lifecycle phase of a CertificateAuthority.
// +kubebuilder:validation:Enum=Pending;Initializing;Ready;Error
type CertificateAuthorityPhase string

const (
	CertificateAuthorityPhasePending      CertificateAuthorityPhase = "Pending"
	CertificateAuthorityPhaseInitializing CertificateAuthorityPhase = "Initializing"
	CertificateAuthorityPhaseReady        CertificateAuthorityPhase = "Ready"
	CertificateAuthorityPhaseError        CertificateAuthorityPhase = "Error"
)

// CertificateAuthorityStatus defines the observed state of CertificateAuthority.
type CertificateAuthorityStatus struct {
	// Phase is the current lifecycle phase.
	// +optional
	Phase CertificateAuthorityPhase `json:"phase,omitempty"`

	// CASecretName is the name of the Secret containing ca_crt.pem (public CA certificate).
	// The CA private key is stored separately in {name}-ca-key, and CRLs in {name}-ca-crl.
	// +optional
	CASecretName string `json:"caSecretName,omitempty"`

	// NotAfter is the expiration time of the CA certificate.
	// +optional
	NotAfter *metav1.Time `json:"notAfter,omitempty"`

	// Conditions represent the latest available observations.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// Condition types for CertificateAuthority.
const (
	ConditionCAReady = "CAReady"
)

func init() {
	SchemeBuilder.Register(&CertificateAuthority{}, &CertificateAuthorityList{})
}
