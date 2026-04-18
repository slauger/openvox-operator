package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=cert
// +kubebuilder:printcolumn:name="Authority",type=string,JSONPath=`.spec.authorityRef`
// +kubebuilder:printcolumn:name="Certname",type=string,JSONPath=`.spec.certname`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="NotAfter",type=string,JSONPath=`.status.notAfter`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Certificate is the Schema for the certificates API.
// It manages the lifecycle of a single certificate signed by a CertificateAuthority.
type Certificate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CertificateSpec   `json:"spec,omitempty"`
	Status CertificateStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// CertificateList contains a list of Certificate.
type CertificateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Certificate `json:"items"`
}

// CSRExtensionsSpec defines Puppet CSR extension attributes to embed in the CSR.
type CSRExtensionsSpec struct {
	// PpCliAuth adds the pp_cli_auth extension, granting CA API signing rights.
	// +optional
	PpCliAuth bool `json:"ppCliAuth,omitempty"`

	// PpRole sets the pp_role extension value.
	// +optional
	PpRole string `json:"ppRole,omitempty"`

	// PpEnvironment sets the pp_environment extension value.
	// +optional
	PpEnvironment string `json:"ppEnvironment,omitempty"`

	// CustomExtensions allows arbitrary pp_* extensions as name/value pairs.
	// Keys must be known Puppet extension names (e.g. pp_cost_center, pp_department).
	// +optional
	CustomExtensions map[string]string `json:"customExtensions,omitempty"`
}

// CertificateSpec defines the desired state of Certificate.
type CertificateSpec struct {
	// AuthorityRef references the CertificateAuthority that signs this certificate.
	AuthorityRef string `json:"authorityRef"`

	// Certname is the certificate common name.
	// +kubebuilder:default="puppet"
	// +optional
	Certname string `json:"certname,omitempty"`

	// DNSAltNames is a list of DNS subject alternative names for the certificate.
	// +optional
	DNSAltNames []string `json:"dnsAltNames,omitempty"`

	// CSRExtensions defines Puppet CSR extension attributes to embed in the CSR.
	// +optional
	CSRExtensions *CSRExtensionsSpec `json:"csrExtensions,omitempty"`

	// RenewBefore is the duration before expiration when the certificate should be renewed.
	// Uses duration format: "60d", "30d", "720h".
	// +kubebuilder:default="60d"
	// +optional
	RenewBefore string `json:"renewBefore,omitempty"`
}

// CertificatePhase represents the current lifecycle phase of a Certificate.
// +kubebuilder:validation:Enum=Pending;Requesting;WaitingForSigning;Signed;Renewing;Error
type CertificatePhase string

const (
	CertificatePhasePending           CertificatePhase = "Pending"
	CertificatePhaseRequesting        CertificatePhase = "Requesting"
	CertificatePhaseWaitingForSigning CertificatePhase = "WaitingForSigning"
	CertificatePhaseSigned            CertificatePhase = "Signed"
	CertificatePhaseRenewing          CertificatePhase = "Renewing"
	CertificatePhaseError             CertificatePhase = "Error"
)

// CertificateStatus defines the observed state of Certificate.
type CertificateStatus struct {
	// Phase is the current lifecycle phase.
	// +optional
	Phase CertificatePhase `json:"phase,omitempty"`

	// SecretName is the name of the Secret containing cert.pem and key.pem.
	// +optional
	SecretName string `json:"secretName,omitempty"`

	// NotAfter is the expiration time of the signed certificate.
	// +optional
	NotAfter *metav1.Time `json:"notAfter,omitempty"`

	// Conditions represent the latest available observations.
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// Condition types for Certificate.
const (
	ConditionCertSigned = "CertSigned"
)

func init() {
	SchemeBuilder.Register(&Certificate{}, &CertificateList{})
}
