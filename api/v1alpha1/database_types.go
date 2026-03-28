package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=db
// +kubebuilder:printcolumn:name="CertificateRef",type=string,JSONPath=`.spec.certificateRef`
// +kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=`.spec.replicas`
// +kubebuilder:printcolumn:name="Ready",type=integer,JSONPath=`.status.ready`
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=`.status.phase`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Database is the Schema for the databases API.
// It manages a pool of OpenVox DB instances as a Deployment.
type Database struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   DatabaseSpec   `json:"spec,omitempty"`
	Status DatabaseStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// DatabaseList contains a list of Database.
type DatabaseList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Database `json:"items"`
}

// DatabaseSpec defines the desired state of Database.
type DatabaseSpec struct {
	// CertificateRef references the Certificate whose SSL Secret is mounted into the Database pods.
	CertificateRef string `json:"certificateRef"`

	// Image defines the container image for the Database.
	Image ImageSpec `json:"image"`

	// Postgres defines the external PostgreSQL connection settings.
	Postgres PostgresSpec `json:"postgres"`

	// Resources defines the compute resources for Database Pods.
	// +optional
	Resources corev1.ResourceRequirements `json:"resources,omitempty"`

	// Replicas is the number of Database instances.
	// +kubebuilder:default=1
	// +optional
	Replicas *int32 `json:"replicas,omitempty"`

	// JavaArgs defines the JVM arguments.
	// +optional
	JavaArgs string `json:"javaArgs,omitempty"`

	// Service defines the Service configuration for the Database.
	// +optional
	Service DatabaseServiceSpec `json:"service,omitempty"`
}

// PostgresSpec defines the external PostgreSQL connection settings.
type PostgresSpec struct {
	// Host is the PostgreSQL hostname.
	Host string `json:"host"`

	// Port is the PostgreSQL port.
	// +kubebuilder:default=5432
	// +optional
	Port int32 `json:"port,omitempty"`

	// Database is the PostgreSQL database name.
	// +kubebuilder:default="openvoxdb"
	// +optional
	Database string `json:"database,omitempty"`

	// CredentialsSecretRef references a Secret containing username and password keys.
	CredentialsSecretRef string `json:"credentialsSecretRef"`

	// SSLMode defines the PostgreSQL SSL connection mode.
	// +kubebuilder:validation:Enum=disable;allow;prefer;require;verify-ca;verify-full
	// +kubebuilder:default="require"
	// +optional
	SSLMode string `json:"sslMode,omitempty"`
}

// DatabaseServiceSpec defines the Service configuration for Database.
type DatabaseServiceSpec struct {
	// Type is the Service type.
	// +kubebuilder:default="ClusterIP"
	// +optional
	Type corev1.ServiceType `json:"type,omitempty"`

	// Port is the Service port.
	// +kubebuilder:default=8081
	// +optional
	Port int32 `json:"port,omitempty"`

	// Annotations defines additional annotations on the Service.
	// +optional
	Annotations map[string]string `json:"annotations,omitempty"`
}

// DatabasePhase represents the current lifecycle phase of a Database.
// +kubebuilder:validation:Enum=Pending;WaitingForCert;Running;Error
type DatabasePhase string

const (
	DatabasePhasePending        DatabasePhase = "Pending"
	DatabasePhaseWaitingForCert DatabasePhase = "WaitingForCert"
	DatabasePhaseRunning        DatabasePhase = "Running"
	DatabasePhaseError          DatabasePhase = "Error"
)

// DatabaseStatus defines the observed state of Database.
type DatabaseStatus struct {
	// Phase is the current lifecycle phase.
	// +optional
	Phase DatabasePhase `json:"phase,omitempty"`

	// URL is the HTTPS endpoint of the Database Service.
	// +optional
	URL string `json:"url,omitempty"`

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

// Condition types for Database.
const (
	ConditionDatabaseReady = "Ready"
)

func init() {
	SchemeBuilder.Register(&Database{}, &DatabaseList{})
}
