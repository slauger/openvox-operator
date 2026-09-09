package main

import (
	"crypto/x509"
	"encoding/asn1"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

// Ruleset is the config-driven equivalent of puppetserver's auth.conf. It mirrors
// the operator's builtinAuthRules HOCON model (match-request + allow/deny), so the
// same authorization intent can be rendered to JSON for the native edge instead of
// to trapperkeeper-authorization HOCON for the JVM server.
type Ruleset struct {
	// Rules are evaluated in order; the first whose path+method matches decides.
	Rules []Rule `json:"rules"`
	// Default applies when no rule matches: "deny" (the safe default) or "allow".
	Default string `json:"default"`
}

// Rule is one auth.conf entry. Exactly one of Allow/Deny/AllowUnauthenticated is
// typically set, matching the HOCON rules the operator already emits.
type Rule struct {
	Name                 string          `json:"name"`
	Path                 string          `json:"path"`
	Type                 string          `json:"type"`   // "path" (prefix) or "regex"
	Method               []string        `json:"method"` // empty = any method
	Allow                json.RawMessage `json:"allow"`
	Deny                 json.RawMessage `json:"deny"`
	AllowUnauthenticated bool            `json:"allow_unauthenticated"`

	re    *regexp.Regexp
	allow []matcher
	deny  []matcher
}

// matcher is one entry of an allow/deny list: "*", the "$1" capture, a literal
// certname, or a set of required certificate extensions (e.g. pp_cli_auth: "true").
type matcher struct {
	star       bool
	capture    bool
	certname   string
	extensions map[string]string
}

// Identity is the verified client presented by the TLS layer.
type Identity struct {
	Authenticated bool
	CN            string
	Extensions    map[string]string // by short name (pp_cli_auth) and by raw OID
}

// Decision is the outcome of authorizing a request.
type Decision struct {
	Allowed bool
	Status  int
	Reason  string
}

func parseRuleset(data []byte) (*Ruleset, error) {
	var rs Ruleset
	if err := json.Unmarshal(data, &rs); err != nil {
		return nil, err
	}
	if rs.Default == "" {
		rs.Default = "deny"
	}
	if rs.Default != "deny" && rs.Default != "allow" {
		return nil, fmt.Errorf("default must be \"deny\" or \"allow\", got %q", rs.Default)
	}
	for i := range rs.Rules {
		r := &rs.Rules[i]
		switch r.Type {
		case "regex":
			re, err := regexp.Compile(r.Path)
			if err != nil {
				return nil, fmt.Errorf("rule %q: bad regex %q: %w", r.Name, r.Path, err)
			}
			r.re = re
		case "path", "":
			r.Type = "path"
		default:
			return nil, fmt.Errorf("rule %q: unknown type %q", r.Name, r.Type)
		}
		var err error
		if r.allow, err = parseMatchers(r.Allow); err != nil {
			return nil, fmt.Errorf("rule %q: allow: %w", r.Name, err)
		}
		if r.deny, err = parseMatchers(r.Deny); err != nil {
			return nil, fmt.Errorf("rule %q: deny: %w", r.Name, err)
		}
	}
	return &rs, nil
}

// parseMatchers accepts a bare string ("*", "$1", "<certname>"), a single
// {extensions:{...}} object, or a JSON array mixing the two.
func parseMatchers(raw json.RawMessage) ([]matcher, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		// Not an array: treat as a single entry.
		arr = []json.RawMessage{raw}
	}
	out := make([]matcher, 0, len(arr))
	for _, item := range arr {
		var s string
		if err := json.Unmarshal(item, &s); err == nil {
			switch s {
			case "*":
				out = append(out, matcher{star: true})
			case "$1":
				out = append(out, matcher{capture: true})
			default:
				out = append(out, matcher{certname: s})
			}
			continue
		}
		var obj struct {
			Extensions map[string]string `json:"extensions"`
		}
		if err := json.Unmarshal(item, &obj); err != nil {
			return nil, fmt.Errorf("entry %s: not a string or {extensions}: %w", item, err)
		}
		if len(obj.Extensions) == 0 {
			return nil, fmt.Errorf("entry %s: empty extensions", item)
		}
		out = append(out, matcher{extensions: obj.Extensions})
	}
	return out, nil
}

// Authorize evaluates the ruleset against a request. The first rule whose path and
// method match decides; deny takes precedence over allow within that rule.
func (rs *Ruleset) Authorize(method, path string, id Identity) Decision {
	for i := range rs.Rules {
		r := &rs.Rules[i]
		capture, ok := r.matchPath(path)
		if !ok || !r.matchMethod(method) {
			continue
		}
		if r.AllowUnauthenticated {
			return Decision{Allowed: true, Status: http.StatusOK, Reason: r.Name}
		}
		if !id.Authenticated {
			return Decision{Status: http.StatusUnauthorized, Reason: r.Name + ": no verified client certificate"}
		}
		if anyMatch(r.deny, id, capture) {
			return Decision{Status: http.StatusForbidden, Reason: r.Name + ": denied"}
		}
		if r.allow != nil {
			if anyMatch(r.allow, id, capture) {
				return Decision{Allowed: true, Status: http.StatusOK, Reason: r.Name}
			}
			return Decision{Status: http.StatusForbidden, Reason: r.Name + ": not in allow list"}
		}
		// Only a deny list (that did not match) -> allowed.
		return Decision{Allowed: true, Status: http.StatusOK, Reason: r.Name}
	}
	if rs.Default == "allow" {
		return Decision{Allowed: true, Status: http.StatusOK, Reason: "default allow"}
	}
	return Decision{Status: http.StatusForbidden, Reason: "no matching rule (default deny)"}
}

// matchPath reports whether path matches the rule and returns the first regex
// capture group (empty for path-type rules), used to evaluate the "$1" matcher.
func (r *Rule) matchPath(path string) (capture string, ok bool) {
	if r.Type == "regex" {
		m := r.re.FindStringSubmatch(path)
		if m == nil {
			return "", false
		}
		if len(m) > 1 {
			return m[1], true
		}
		return "", true
	}
	return "", strings.HasPrefix(path, r.Path)
}

func (r *Rule) matchMethod(method string) bool {
	if len(r.Method) == 0 {
		return true
	}
	for _, m := range r.Method {
		if strings.EqualFold(m, method) {
			return true
		}
	}
	return false
}

func anyMatch(ms []matcher, id Identity, capture string) bool {
	for _, m := range ms {
		if m.match(id, capture) {
			return true
		}
	}
	return false
}

func (m matcher) match(id Identity, capture string) bool {
	switch {
	case m.star:
		return true
	case m.capture:
		return capture != "" && id.CN == capture
	case m.certname != "":
		return id.CN == m.certname
	case m.extensions != nil:
		for name, want := range m.extensions {
			if id.Extensions[name] != want {
				return false
			}
		}
		return true
	}
	return false
}

// puppetOIDNames maps Puppet's registered certificate-extension OIDs to their short
// names so rules can reference e.g. "pp_cli_auth" instead of the raw arc. Mirrors
// puppet/ssl/oids.rb (ppRegCertExt 1.3.6.1.4.1.34380.1.1 and ppAuthCertExt .1.3).
var puppetOIDNames = map[string]string{
	"1.3.6.1.4.1.34380.1.1.1":  "pp_uuid",
	"1.3.6.1.4.1.34380.1.1.2":  "pp_instance_id",
	"1.3.6.1.4.1.34380.1.1.3":  "pp_image_name",
	"1.3.6.1.4.1.34380.1.1.4":  "pp_preshared_key",
	"1.3.6.1.4.1.34380.1.1.5":  "pp_cost_center",
	"1.3.6.1.4.1.34380.1.1.6":  "pp_product",
	"1.3.6.1.4.1.34380.1.1.7":  "pp_project",
	"1.3.6.1.4.1.34380.1.1.8":  "pp_application",
	"1.3.6.1.4.1.34380.1.1.9":  "pp_service",
	"1.3.6.1.4.1.34380.1.1.10": "pp_employee",
	"1.3.6.1.4.1.34380.1.1.11": "pp_created_by",
	"1.3.6.1.4.1.34380.1.1.12": "pp_environment",
	"1.3.6.1.4.1.34380.1.1.13": "pp_role",
	"1.3.6.1.4.1.34380.1.1.14": "pp_software_version",
	"1.3.6.1.4.1.34380.1.1.15": "pp_department",
	"1.3.6.1.4.1.34380.1.1.16": "pp_cluster",
	"1.3.6.1.4.1.34380.1.1.17": "pp_provisioner",
	"1.3.6.1.4.1.34380.1.1.18": "pp_region",
	"1.3.6.1.4.1.34380.1.1.19": "pp_datacenter",
	"1.3.6.1.4.1.34380.1.1.20": "pp_zone",
	"1.3.6.1.4.1.34380.1.1.21": "pp_network",
	"1.3.6.1.4.1.34380.1.1.22": "pp_securitypolicy",
	"1.3.6.1.4.1.34380.1.1.23": "pp_cloudplatform",
	"1.3.6.1.4.1.34380.1.1.24": "pp_apptier",
	"1.3.6.1.4.1.34380.1.1.25": "pp_hostname",
	"1.3.6.1.4.1.34380.1.3.1":  "pp_authorization",
	"1.3.6.1.4.1.34380.1.3.39": "pp_cli_auth",
}

// identityFromCert builds an Identity from a verified peer certificate, exposing
// its Puppet extensions by both short name and raw OID.
func identityFromCert(cert *x509.Certificate) Identity {
	id := Identity{Authenticated: true, CN: cert.Subject.CommonName, Extensions: map[string]string{}}
	for _, ext := range cert.Extensions {
		oid := ext.Id.String()
		val := decodeExtensionValue(ext.Value)
		id.Extensions[oid] = val
		if name, ok := puppetOIDNames[oid]; ok {
			id.Extensions[name] = val
		}
	}
	return id
}

// decodeExtensionValue unwraps the ASN.1-encoded string Puppet uses for its
// cert extensions (UTF8String/PrintableString/IA5String). Falls back to the raw
// bytes if the value is not a recognizable ASN.1 string.
func decodeExtensionValue(raw []byte) string {
	var s string
	if _, err := asn1.Unmarshal(raw, &s); err == nil {
		return s
	}
	return strings.TrimRight(string(raw), "\x00")
}
