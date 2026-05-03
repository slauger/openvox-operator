package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=sp
// +kubebuilder:printcolumn:name="CA",type=string,JSONPath=`.spec.certificateAuthorityRef`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// SigningPolicy is the Schema for the signingpolicies API.
// It defines a policy for automatic CSR signing against a CertificateAuthority.
type SigningPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   SigningPolicySpec   `json:"spec,omitempty"`
	Status SigningPolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// SigningPolicyList contains a list of SigningPolicy.
type SigningPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []SigningPolicy `json:"items"`
}

// SigningPolicySpec defines the desired state of SigningPolicy.
// Between policies: OR (any match signs). Within a policy: AND (all set fields must match).
type SigningPolicySpec struct {
	// CertificateAuthorityRef references the CertificateAuthority this policy applies to.
	CertificateAuthorityRef string `json:"certificateAuthorityRef"`

	// Any when true signs all CSRs unconditionally.
	// +optional
	Any bool `json:"any,omitempty"`

	// Pattern defines certname glob matching rules.
	// +optional
	Pattern *PatternSpec `json:"pattern,omitempty"`

	// DNSAltNames defines allowed DNS subject alternative name patterns.
	// If not set and Any is false, CSRs with SANs are denied by the autosign binary.
	// +optional
	DNSAltNames *PatternSpec `json:"dnsAltNames,omitempty"`

	// CSRAttributes defines CSR extension attributes that must all match (AND logic).
	// Each entry specifies an attribute name and the expected value (inline or from a Secret).
	// +optional
	CSRAttributes []CSRAttributeMatch `json:"csrAttributes,omitempty"`
}

// PatternSpec defines certname glob matching.
type PatternSpec struct {
	// Allow is a list of glob patterns. The certname must match at least one.
	Allow []string `json:"allow"`
}

// CSRAttributeMatch defines a single CSR extension attribute to match.
type CSRAttributeMatch struct {
	// Name is the CSR extension attribute name (e.g. pp_preshared_key, pp_environment).
	Name string `json:"name"`

	// Value is the expected attribute value (inline).
	// +optional
	Value string `json:"value,omitempty"`

	// ValueFrom references a Secret key containing the expected value.
	// +optional
	ValueFrom *SecretKeySelector `json:"valueFrom,omitempty"`
}

// SecretKeySelector references a specific key within a Secret.
type SecretKeySelector struct {
	// SecretKeyRef selects a key from a Secret.
	SecretKeyRef SecretKeyRef `json:"secretKeyRef"`
}

// SecretKeyRef references a specific key within a Secret.
type SecretKeyRef struct {
	// Name is the name of the Secret.
	Name string `json:"name"`

	// Key is the key within the Secret.
	Key string `json:"key"`
}

// SigningPolicyPhase represents the current lifecycle phase of a SigningPolicy.
// +kubebuilder:validation:Enum=Active;Error
type SigningPolicyPhase string

const (
	SigningPolicyPhaseActive SigningPolicyPhase = "Active"
	SigningPolicyPhaseError  SigningPolicyPhase = "Error"
)

// SigningPolicyStatus defines the observed state of SigningPolicy.
type SigningPolicyStatus struct {
	// Phase is the current lifecycle phase.
	// +optional
	Phase SigningPolicyPhase `json:"phase,omitempty"`

	// Conditions represent the latest available observations.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// Condition types for SigningPolicy.
const (
	ConditionSigningPolicyReady = "Ready"
)

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(GroupVersion, &SigningPolicy{}, &SigningPolicyList{})
		return nil
	})
}
