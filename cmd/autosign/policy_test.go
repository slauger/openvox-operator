package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/asn1"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"

	"github.com/slauger/openvox-operator/internal/puppet"
)

// generateCSR creates a test CSR with optional SANs and extensions.
func generateCSR(t *testing.T, cn string, dnsNames []string, extensions []pkix.Extension) *x509.CertificateRequest {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	template := &x509.CertificateRequest{
		Subject:         pkix.Name{CommonName: cn},
		DNSNames:        dnsNames,
		ExtraExtensions: extensions,
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, template, key)
	if err != nil {
		t.Fatalf("creating CSR: %v", err)
	}
	csr, err := x509.ParseCertificateRequest(csrDER)
	if err != nil {
		t.Fatalf("parsing CSR: %v", err)
	}
	return csr
}

// generateCSRPEM creates a PEM-encoded test CSR.
func generateCSRPEM(t *testing.T, cn string) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	template := &x509.CertificateRequest{
		Subject: pkix.Name{CommonName: cn},
	}
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, template, key)
	if err != nil {
		t.Fatalf("creating CSR: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csrDER})
}

// makeExtension creates a Puppet CSR extension with an ASN.1 UTF8String value.
func makeExtension(t *testing.T, oid asn1.ObjectIdentifier, value string) pkix.Extension {
	t.Helper()
	val, err := asn1.Marshal(value)
	if err != nil {
		t.Fatalf("marshalling extension value: %v", err)
	}
	return pkix.Extension{Id: oid, Value: val}
}

func TestLoadPolicyConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "policy.yaml")

	content := `policies:
- name: allow-all
  any: true
- name: pattern-match
  pattern:
    allow:
      - "*.example.com"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadPolicyConfig(path)
	if err != nil {
		t.Fatalf("loadPolicyConfig: %v", err)
	}
	if len(cfg.Policies) != 2 {
		t.Fatalf("expected 2 policies, got %d", len(cfg.Policies))
	}
	if !cfg.Policies[0].Any {
		t.Error("expected first policy to have any=true")
	}
	if cfg.Policies[1].Pattern == nil {
		t.Error("expected second policy to have pattern")
	}
}

func TestLoadPolicyConfig_FileNotFound(t *testing.T) {
	_, err := loadPolicyConfig("/nonexistent/path/policy.yaml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoadPolicyConfig_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte("policies:\n  - name: [invalid\n"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := loadPolicyConfig(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML")
	}
}

func TestParseCSR(t *testing.T) {
	pemData := generateCSRPEM(t, "test-node")
	csr, err := parseCSR(pemData)
	if err != nil {
		t.Fatalf("parseCSR: %v", err)
	}
	if csr.Subject.CommonName != "test-node" {
		t.Errorf("expected CN %q, got %q", "test-node", csr.Subject.CommonName)
	}
}

func TestParseCSR_NoPEM(t *testing.T) {
	_, err := parseCSR([]byte("not a pem block"))
	if err == nil {
		t.Fatal("expected error for non-PEM data")
	}
}

func TestParseCSR_InvalidDER(t *testing.T) {
	block := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: []byte("invalid DER")})
	_, err := parseCSR(block)
	if err == nil {
		t.Fatal("expected error for invalid DER")
	}
}

func TestEvaluatePolicies_AnyTrue(t *testing.T) {
	cfg := &PolicyConfig{
		Policies: []Policy{
			{Name: "allow-all", Any: true},
		},
	}
	csr := generateCSR(t, "node1.example.com", nil, nil)
	if !evaluatePolicies(cfg, "node1.example.com", csr) {
		t.Error("expected any:true to approve")
	}
}

func TestEvaluatePolicies_NoPolicies(t *testing.T) {
	cfg := &PolicyConfig{}
	csr := generateCSR(t, "node1", nil, nil)
	if evaluatePolicies(cfg, "node1", csr) {
		t.Error("expected no policies to deny")
	}
}

func TestEvaluatePolicy_PatternMatch(t *testing.T) {
	policy := Policy{
		Name:    "pattern",
		Pattern: &PatternConf{Allow: []string{"*.example.com"}},
	}
	csr := generateCSR(t, "node1.example.com", nil, nil)

	if !evaluatePolicy(policy, "node1.example.com", csr) {
		t.Error("expected pattern to match node1.example.com")
	}

	if evaluatePolicy(policy, "node1.other.com", csr) {
		t.Error("expected pattern to not match node1.other.com")
	}
}

func TestEvaluatePolicy_PatternMultiple(t *testing.T) {
	policy := Policy{
		Name:    "multi-pattern",
		Pattern: &PatternConf{Allow: []string{"*.prod.com", "*.staging.com"}},
	}
	csr := generateCSR(t, "node1.staging.com", nil, nil)

	if !evaluatePolicy(policy, "node1.staging.com", csr) {
		t.Error("expected second pattern to match")
	}
}

func TestEvaluatePolicy_NoConditions(t *testing.T) {
	policy := Policy{Name: "empty"}
	csr := generateCSR(t, "node1", nil, nil)

	if evaluatePolicy(policy, "node1", csr) {
		t.Error("expected policy with no conditions to deny")
	}
}

func TestEvaluatePolicy_CSRAttributes(t *testing.T) {
	oid := puppet.PuppetOIDs["pp_environment"]
	ext := makeExtension(t, oid, "production")
	csr := generateCSR(t, "node1", nil, []pkix.Extension{ext})

	policy := Policy{
		Name:    "env-check",
		Pattern: &PatternConf{Allow: []string{"*"}},
		CSRAttributes: []CSRAttributeConf{
			{Name: "pp_environment", Value: "production"},
		},
	}

	if !evaluatePolicy(policy, "node1", csr) {
		t.Error("expected CSR attribute match to approve")
	}

	policy.CSRAttributes[0].Value = "staging"
	if evaluatePolicy(policy, "node1", csr) {
		t.Error("expected CSR attribute mismatch to deny")
	}
}

func TestEvaluatePolicy_CSRAttributeNotPresent(t *testing.T) {
	csr := generateCSR(t, "node1", nil, nil)

	policy := Policy{
		Name:    "env-check",
		Pattern: &PatternConf{Allow: []string{"*"}},
		CSRAttributes: []CSRAttributeConf{
			{Name: "pp_environment", Value: "production"},
		},
	}

	if evaluatePolicy(policy, "node1", csr) {
		t.Error("expected missing CSR attribute to deny")
	}
}

func TestEvaluatePolicy_DNSAltNames(t *testing.T) {
	csr := generateCSR(t, "puppet", []string{"puppet.example.com", "puppet.local"}, nil)

	// Policy allows the SANs
	policy := Policy{
		Name:        "with-sans",
		Pattern:     &PatternConf{Allow: []string{"puppet"}},
		DNSAltNames: &PatternConf{Allow: []string{"*.example.com", "*.local"}},
	}
	if !evaluatePolicy(policy, "puppet", csr) {
		t.Error("expected DNS SAN match to approve")
	}

	// Policy doesn't allow one SAN
	policy.DNSAltNames = &PatternConf{Allow: []string{"*.example.com"}}
	if evaluatePolicy(policy, "puppet", csr) {
		t.Error("expected partial DNS SAN match to deny")
	}
}

func TestEvaluatePolicy_DNSAltNamesNotAllowed(t *testing.T) {
	csr := generateCSR(t, "node1", []string{"evil.example.com"}, nil)

	// Policy has no dnsAltNames field but CSR has SANs -> deny
	policy := Policy{
		Name:    "no-sans",
		Pattern: &PatternConf{Allow: []string{"*"}},
	}
	if evaluatePolicy(policy, "node1", csr) {
		t.Error("expected CSR with SANs but no SAN policy to deny")
	}
}

func TestEvaluatePolicy_ANDLogic(t *testing.T) {
	oid := puppet.PuppetOIDs["pp_role"]
	ext := makeExtension(t, oid, "webserver")
	csr := generateCSR(t, "web1.prod.com", nil, []pkix.Extension{ext})

	policy := Policy{
		Name:    "and-logic",
		Pattern: &PatternConf{Allow: []string{"*.prod.com"}},
		CSRAttributes: []CSRAttributeConf{
			{Name: "pp_role", Value: "webserver"},
		},
	}

	// Both match
	if !evaluatePolicy(policy, "web1.prod.com", csr) {
		t.Error("expected both conditions matching to approve")
	}

	// Pattern matches but attribute doesn't
	policy.CSRAttributes[0].Value = "database"
	if evaluatePolicy(policy, "web1.prod.com", csr) {
		t.Error("expected AND logic to deny when attribute mismatches")
	}
}

func TestEvaluatePolicies_ORLogic(t *testing.T) {
	csr := generateCSR(t, "node1.staging.com", nil, nil)

	cfg := &PolicyConfig{
		Policies: []Policy{
			{Name: "prod-only", Pattern: &PatternConf{Allow: []string{"*.prod.com"}}},
			{Name: "staging-only", Pattern: &PatternConf{Allow: []string{"*.staging.com"}}},
		},
	}

	if !evaluatePolicies(cfg, "node1.staging.com", csr) {
		t.Error("expected OR logic: second policy should match")
	}
}

func TestGlobMatch(t *testing.T) {
	tests := []struct {
		pattern string
		name    string
		want    bool
	}{
		{"*.example.com", "node1.example.com", true},
		{"*.example.com", "node1.other.com", false},
		{"node?", "node1", true},
		{"node?", "node12", false},
		{"*", "anything", true},
		{"exact", "exact", true},
		{"exact", "other", false},
	}

	for _, tt := range tests {
		t.Run(tt.pattern+"_"+tt.name, func(t *testing.T) {
			if got := globMatch(tt.pattern, tt.name); got != tt.want {
				t.Errorf("globMatch(%q, %q) = %v, want %v", tt.pattern, tt.name, got, tt.want)
			}
		})
	}
}

func TestExtractCSRAttribute(t *testing.T) {
	oid := puppet.PuppetOIDs["pp_uuid"]
	ext := makeExtension(t, oid, "test-uuid-value")
	csr := generateCSR(t, "node1", nil, []pkix.Extension{ext})

	val, ok := extractCSRAttribute(csr, "pp_uuid")
	if !ok {
		t.Fatal("expected to find pp_uuid")
	}
	if val != "test-uuid-value" {
		t.Errorf("expected %q, got %q", "test-uuid-value", val)
	}

	// Unknown attribute name
	_, ok = extractCSRAttribute(csr, "unknown_attr")
	if ok {
		t.Error("expected unknown attribute to not be found")
	}
}

func TestMatchCSRAttributes_MultipleAND(t *testing.T) {
	oidEnv := puppet.PuppetOIDs["pp_environment"]
	oidRole := puppet.PuppetOIDs["pp_role"]
	csr := generateCSR(t, "node1", nil, []pkix.Extension{
		makeExtension(t, oidEnv, "production"),
		makeExtension(t, oidRole, "webserver"),
	})

	attrs := []CSRAttributeConf{
		{Name: "pp_environment", Value: "production"},
		{Name: "pp_role", Value: "webserver"},
	}
	if !matchCSRAttributes(attrs, csr) {
		t.Error("expected all attributes to match")
	}

	attrs[1].Value = "database"
	if matchCSRAttributes(attrs, csr) {
		t.Error("expected mismatch to deny")
	}
}

func TestMatchDNSAltNames(t *testing.T) {
	p := &PatternConf{Allow: []string{"*.example.com", "*.local"}}

	if !matchDNSAltNames(p, []string{"a.example.com", "b.local"}) {
		t.Error("expected all SANs to match")
	}

	if matchDNSAltNames(p, []string{"a.example.com", "b.other.com"}) {
		t.Error("expected unmatched SAN to deny")
	}

	// Empty SAN list always matches
	if !matchDNSAltNames(p, []string{}) {
		t.Error("expected empty SAN list to match")
	}
}

func TestMatchPattern(t *testing.T) {
	p := &PatternConf{Allow: []string{"web*", "db*"}}

	if !matchPattern(p, "web1") {
		t.Error("expected web1 to match")
	}
	if !matchPattern(p, "db-primary") {
		t.Error("expected db-primary to match")
	}
	if matchPattern(p, "cache1") {
		t.Error("expected cache1 to not match")
	}
}

func TestPuppetOIDs(t *testing.T) {
	// Verify some key OIDs exist
	knownOIDs := []string{
		"pp_uuid", "pp_instance_id", "pp_image_name",
		"pp_preshared_key", "pp_environment", "pp_role",
		"pp_hostname", "pp_owner", "challengePassword",
	}
	for _, name := range knownOIDs {
		if !puppet.IsKnownOID(name) {
			t.Errorf("expected OID %q to exist in PuppetOIDs", name)
		}
	}
}
