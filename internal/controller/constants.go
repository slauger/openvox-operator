package controller

import "time"

// Requeue intervals for controller reconciliation loops.
const (
	RequeueIntervalShort  = 5 * time.Second
	RequeueIntervalMedium = 10 * time.Second
	RequeueIntervalLong   = 15 * time.Second
	RequeueIntervalCRL    = 30 * time.Second
)

// HTTP client configuration.
const (
	HTTPClientTimeout = 30 * time.Second
	HTTPBodyLimit     = 10 * 1024 * 1024 // 10 MB
)

// Default CRL refresh interval when not configured on the CertificateAuthority.
const DefaultCRLRefreshInterval = 5 * time.Minute

// RSA key size for certificate signing requests.
const RSAKeySize = 4096

// CA setup Job defaults.
const (
	CAJobBackoffLimit  = int32(3)
	DefaultCAStorageGi = "1Gi"
	CASetupRunAsUser   = int64(1001)
	CASetupRunAsGroup  = int64(0)
	ServerRunAsUser    = int64(1001)
	ServerRunAsGroup   = int64(0)
)

// CA setup Job resource defaults (JRuby/JVM workload).
const (
	DefaultCAJobCPURequest    = "200m"
	DefaultCAJobMemoryRequest = "768Mi"
	DefaultCAJobCPULimit      = "1"
	DefaultCAJobMemoryLimit   = "1Gi"
)

// HPA and PDB defaults for Server resources.
const (
	DefaultHPAMaxReplicas  = int32(5)
	DefaultHPATargetCPU    = int32(75)
	DefaultPDBMinAvailable = 1
)
