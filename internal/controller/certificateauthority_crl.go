package controller

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// reconcileCRLRefresh fetches the CRL from the CA service and updates the CRL secret.
// Returns a Result with RequeueAfter set to the configured refresh interval.
func (r *CertificateAuthorityReconciler) reconcileCRLRefresh(ctx context.Context, ca *openvoxv1alpha1.CertificateAuthority) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	interval := DefaultCRLRefreshInterval
	if ca.Spec.CRLRefreshInterval != "" {
		secs, err := openvoxv1alpha1.ParseDurationToSeconds(ca.Spec.CRLRefreshInterval)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("parsing crlRefreshInterval %q: %w", ca.Spec.CRLRefreshInterval, err)
		}
		interval = time.Duration(secs) * time.Second
	}

	// Resolve CA base URL: external URL or internal CA Service
	var caBaseURL string
	if ca.Spec.External != nil {
		caBaseURL = ca.Spec.External.URL
	} else {
		caBaseURL = fmt.Sprintf("https://%s.%s.svc:8140", caInternalServiceName(ca.Name), ca.Namespace)
	}

	crlPEM, err := r.fetchCRL(ctx, ca, caBaseURL, ca.Namespace)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("fetching CRL: %w", err)
	}

	crlSecretName := fmt.Sprintf("%s-ca-crl", ca.Name)
	if err := r.updateCRLSecret(ctx, ca, crlSecretName, crlPEM); err != nil {
		return ctrl.Result{}, fmt.Errorf("updating CRL secret: %w", err)
	}

	logger.Info("CRL secret refreshed", "secret", crlSecretName, "nextRefresh", interval)
	r.Recorder.Eventf(ca, nil, corev1.EventTypeNormal, EventReasonCRLRefreshed, "Reconcile", "CRL refreshed successfully, next refresh in %s", interval)
	return ctrl.Result{RequeueAfter: interval}, nil
}

// getCAPublicCert reads the CA public certificate from the CA Secret.
func (r *CertificateAuthorityReconciler) getCAPublicCert(ctx context.Context, ca *openvoxv1alpha1.CertificateAuthority, namespace string) ([]byte, error) {
	caSecretName := fmt.Sprintf("%s-ca", ca.Name)
	secret := &corev1.Secret{}
	if err := r.Get(ctx, types.NamespacedName{Name: caSecretName, Namespace: namespace}, secret); err != nil {
		return nil, fmt.Errorf("getting CA Secret %s: %w", caSecretName, err)
	}
	certPEM := secret.Data["ca_crt.pem"]
	if len(certPEM) == 0 {
		return nil, fmt.Errorf("CA Secret %s has no ca_crt.pem data", caSecretName)
	}
	return certPEM, nil
}

// fetchCRL retrieves the CRL from the CA HTTP API.
func (r *CertificateAuthorityReconciler) fetchCRL(ctx context.Context, ca *openvoxv1alpha1.CertificateAuthority, caBaseURL, namespace string) ([]byte, error) {
	httpClient, err := r.caHTTPClientForCA(ctx, ca, namespace)
	if err != nil {
		return nil, fmt.Errorf("creating CA HTTP client: %w", err)
	}

	crlURL := fmt.Sprintf("%s/puppet-ca/v1/certificate_revocation_list/ca?environment=production", caBaseURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, crlURL, nil)
	if err != nil {
		return nil, fmt.Errorf("building CRL request: %w", err)
	}
	req.Header.Set("Accept", "text/plain")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("requesting CRL: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, HTTPBodyLimit))
	if err != nil {
		return nil, fmt.Errorf("reading CRL response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("CA returned HTTP %d for CRL: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// caHTTPClientForCA returns an HTTP client configured for the CA.
// For external CAs, it builds an mTLS client from the referenced Secrets.
// For internal CAs, it uses the CA public certificate.
func (r *CertificateAuthorityReconciler) caHTTPClientForCA(ctx context.Context, ca *openvoxv1alpha1.CertificateAuthority, namespace string) (*http.Client, error) {
	if ca.Spec.External != nil {
		return buildExternalCAHTTPClient(ctx, r.Client, ca.Spec.External, namespace)
	}

	caCertPEM, err := r.getCAPublicCert(ctx, ca, namespace)
	if err != nil {
		return nil, fmt.Errorf("loading CA certificate: %w", err)
	}
	return caHTTPClient(caCertPEM)
}

// updateCRLSecret creates or updates the CRL secret with fresh CRL data.
func (r *CertificateAuthorityReconciler) updateCRLSecret(ctx context.Context, ca *openvoxv1alpha1.CertificateAuthority, name string, crlPEM []byte) error {
	return createOrUpdateSecret(ctx, r.Client, r.Scheme, ca, name, ca.Namespace, caLabels(ca.Name), map[string][]byte{
		"ca_crl.pem": crlPEM,
	})
}
