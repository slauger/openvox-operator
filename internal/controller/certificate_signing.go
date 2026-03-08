package controller

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/log"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// signCertificate generates an RSA key pair, submits a CSR to the Puppet CA HTTP API,
// and polls for the signed certificate. Returns PEM-encoded cert and key.
func (r *CertificateReconciler) signCertificate(ctx context.Context, cert *openvoxv1alpha1.Certificate, caServiceName string) (certPEM, keyPEM []byte, err error) {
	logger := log.FromContext(ctx)

	certname := cert.Spec.Certname
	if certname == "" {
		certname = "puppet"
	}

	// Generate RSA 4096-bit key
	privateKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return nil, nil, fmt.Errorf("generating RSA key: %w", err)
	}

	keyPEM = pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})

	// Build CSR with certname as CN + dnsAltNames as SANs
	csrTemplate := &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: certname},
		DNSNames: cert.Spec.DNSAltNames,
	}

	csrDER, err := x509.CreateCertificateRequest(rand.Reader, csrTemplate, privateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("creating CSR: %w", err)
	}

	csrPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE REQUEST",
		Bytes: csrDER,
	})

	// HTTP client — skip TLS verify (internal CA, not yet trusted)
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // internal CA
		},
	}

	caBaseURL := fmt.Sprintf("https://%s:8140", caServiceName)

	// Submit CSR via PUT
	csrURL := fmt.Sprintf("%s/puppet-ca/v1/certificate_request/%s?environment=production", caBaseURL, certname)
	logger.Info("submitting CSR to CA", "url", csrURL, "certname", certname)

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, csrURL, bytes.NewReader(csrPEM))
	if err != nil {
		return nil, nil, fmt.Errorf("building CSR request: %w", err)
	}
	req.Header.Set("Content-Type", "text/plain")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("submitting CSR: %w", err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("CA rejected CSR (HTTP %d): %s", resp.StatusCode, string(body))
	}
	logger.Info("CSR submitted successfully", "certname", certname)

	// Poll for signed certificate
	certURL := fmt.Sprintf("%s/puppet-ca/v1/certificate/%s?environment=production", caBaseURL, certname)
	logger.Info("polling for signed certificate", "url", certURL)

	var signedCertPEM []byte
	for attempt := 0; attempt < 24; attempt++ {
		select {
		case <-ctx.Done():
			return nil, nil, ctx.Err()
		case <-time.After(5 * time.Second):
		}

		certReq, err := http.NewRequestWithContext(ctx, http.MethodGet, certURL, nil)
		if err != nil {
			return nil, nil, fmt.Errorf("building cert request: %w", err)
		}
		certReq.Header.Set("Accept", "text/plain")

		certResp, err := httpClient.Do(certReq)
		if err != nil {
			logger.Info("polling CA failed, retrying", "attempt", attempt+1, "error", err)
			continue
		}

		certBody, _ := io.ReadAll(certResp.Body)
		certResp.Body.Close()

		if certResp.StatusCode == http.StatusOK && len(certBody) > 0 {
			// Verify it's a valid PEM certificate (not just the CSR echoed back)
			block, _ := pem.Decode(certBody)
			if block != nil && block.Type == "CERTIFICATE" {
				signedCertPEM = certBody
				break
			}
		}

		logger.Info("certificate not yet signed, retrying", "attempt", attempt+1, "status", certResp.StatusCode)
	}

	if signedCertPEM == nil {
		return nil, nil, fmt.Errorf("timed out waiting for signed certificate for %s", certname)
	}

	logger.Info("certificate signed successfully", "certname", certname)
	return signedCertPEM, keyPEM, nil
}
