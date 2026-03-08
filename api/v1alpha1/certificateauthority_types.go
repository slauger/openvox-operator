package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=ca
// +kubebuilder:printcolumn:name="Environment",type=string,JSONPath=`.spec.environmentRef`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
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
	// EnvironmentRef references the Environment this CA belongs to.
	EnvironmentRef string `json:"environmentRef"`

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

	// CASecretName is the name of the Secret containing ca_crt.pem, ca_crl.pem, infra_crl.pem.
	// +optional
	CASecretName string `json:"caSecretName,omitempty"`

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
