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

	// Certnames defines allowed certname glob patterns. The certname must match
	// at least one.
	// +optional
	Certnames *PatternSpec `json:"certnames,omitempty"`

	// DNSAltNames defines allowed DNS subject alternative name patterns (glob).
	// If a CSR carries DNS SANs and this is not set, the CSR is denied.
	// +optional
	DNSAltNames *PatternSpec `json:"dnsAltNames,omitempty"`

	// IPAltNames defines allowed IP subject alternative names as CIDR ranges
	// (e.g. "10.0.0.0/16", "::1/128"). If a CSR carries IP SANs and this is not
	// set, the CSR is denied.
	// +optional
	IPAltNames *PatternSpec `json:"ipAltNames,omitempty"`

	// URIAltNames defines allowed URI subject alternative name patterns. A "*"
	// wildcard matches any run of characters, including "/". If a CSR carries URI
	// SANs and this is not set, the CSR is denied.
	// +optional
	URIAltNames *PatternSpec `json:"uriAltNames,omitempty"`

	// EmailAltNames defines allowed email subject alternative name patterns. A "*"
	// wildcard matches any run of characters, including "@". If a CSR carries email
	// SANs and this is not set, the CSR is denied.
	// +optional
	EmailAltNames *PatternSpec `json:"emailAltNames,omitempty"`

	// Extensions lists Puppet CSR extension names (e.g. pp_cli_auth) that a CSR
	// matched by this policy is permitted to carry. Privileged authorization
	// extensions (the 1.3.6.1.4.1.34380.1.3 arc: pp_cli_auth, pp_authorization,
	// pp_auth_token) are denied unless listed here; this gate applies to every
	// policy, including one with any=true. Trusted-fact extensions are unaffected.
	// +optional
	Extensions *PatternSpec `json:"extensions,omitempty"`

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
// Either value or valueFrom may be set, not both.
// +kubebuilder:validation:XValidation:rule="!(has(self.value) && size(self.value) > 0 && has(self.valueFrom))",message="value and valueFrom are mutually exclusive"
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
