package controller

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	corev1 "k8s.io/api/core/v1"
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


// fetchCRL retrieves the CRL from the CA HTTP API.
func (r *CertificateAuthorityReconciler) fetchCRL(ctx context.Context, ca *openvoxv1alpha1.CertificateAuthority, caBaseURL, namespace string) ([]byte, error) {
	httpClient, err := caHTTPClientForCA(ctx, r.Client, ca, namespace)
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


// updateCRLSecret creates or updates the CRL secret with fresh CRL data.
func (r *CertificateAuthorityReconciler) updateCRLSecret(ctx context.Context, ca *openvoxv1alpha1.CertificateAuthority, name string, crlPEM []byte) error {
	return createOrUpdateSecret(ctx, r.Client, r.Scheme, ca, name, ca.Namespace, caLabels(ca.Name), map[string][]byte{
		"ca_crl.pem": crlPEM,
	})
}
