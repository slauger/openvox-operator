package main

import "testing"

// rulesetJSON mirrors the operator's builtinAuthRules intent for the compile
// endpoints the native server serves, plus the CA CLI rule for cross-checking
// the pp_cli_auth extension matcher.
const rulesetJSON = `{
  "rules": [
    {
      "name": "puppetlabs v3 catalog from agents",
      "path": "^/puppet/v3/catalog/([^/]+)$",
      "type": "regex",
      "method": ["get", "post"],
      "allow": "$1"
    },
    {
      "name": "puppetlabs v3 environments",
      "path": "/puppet/v3/environments",
      "type": "path",
      "method": ["get"],
      "allow": "*"
    },
    {
      "name": "puppetlabs certificate",
      "path": "/puppet-ca/v1/certificate/",
      "type": "path",
      "method": ["get"],
      "allow_unauthenticated": true
    },
    {
      "name": "puppetlabs cert status",
      "path": "/puppet-ca/v1/certificate_status",
      "type": "path",
      "method": ["get", "put", "delete"],
      "allow": [{"extensions": {"pp_cli_auth": "true"}}, "operator.example"]
    },
    {
      "name": "status",
      "path": "/status/",
      "type": "path",
      "allow_unauthenticated": true
    }
  ],
  "default": "deny"
}`

func mustRuleset(t *testing.T) *Ruleset {
	t.Helper()
	rs, err := parseRuleset([]byte(rulesetJSON))
	if err != nil {
		t.Fatalf("parseRuleset: %v", err)
	}
	return rs
}

func agent(cn string) Identity {
	return Identity{Authenticated: true, CN: cn, Extensions: map[string]string{}}
}

func TestAuthorize(t *testing.T) {
	rs := mustRuleset(t)
	cliAuth := Identity{Authenticated: true, CN: "cli.example", Extensions: map[string]string{"pp_cli_auth": "true"}}

	cases := []struct {
		name    string
		method  string
		path    string
		id      Identity
		allowed bool
		status  int
	}{
		{"catalog own node", "GET", "/puppet/v3/catalog/web01.example", agent("web01.example"), true, 200},
		{"catalog other node denied", "GET", "/puppet/v3/catalog/web01.example", agent("intruder.example"), false, 403},
		{"catalog needs cert", "GET", "/puppet/v3/catalog/web01.example", Identity{}, false, 401},
		{"environments any agent", "GET", "/puppet/v3/environments", agent("web01.example"), true, 200},
		{"environments wrong method", "POST", "/puppet/v3/environments", agent("web01.example"), false, 403},
		{"cert fetch unauthenticated", "GET", "/puppet-ca/v1/certificate/web01.example", Identity{}, true, 200},
		{"cert status via pp_cli_auth", "GET", "/puppet-ca/v1/certificate_status/web01", cliAuth, true, 200},
		{"cert status via operator cn", "GET", "/puppet-ca/v1/certificate_status/web01", agent("operator.example"), true, 200},
		{"cert status plain agent denied", "GET", "/puppet-ca/v1/certificate_status/web01", agent("web01.example"), false, 403},
		{"status unauthenticated", "GET", "/status/v1/simple", Identity{}, true, 200},
		{"unknown path default deny", "GET", "/puppet/v3/nope", agent("web01.example"), false, 403},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := rs.Authorize(tc.method, tc.path, tc.id)
			if d.Allowed != tc.allowed || d.Status != tc.status {
				t.Fatalf("got allowed=%v status=%d (%s), want allowed=%v status=%d",
					d.Allowed, d.Status, d.Reason, tc.allowed, tc.status)
			}
		})
	}
}

func TestParseRulesetDefaults(t *testing.T) {
	rs, err := parseRuleset([]byte(`{"rules":[]}`))
	if err != nil {
		t.Fatalf("parseRuleset: %v", err)
	}
	if rs.Default != "deny" {
		t.Fatalf("default = %q, want deny", rs.Default)
	}
	if d := rs.Authorize("GET", "/anything", agent("x")); d.Allowed {
		t.Fatalf("empty ruleset should default-deny, got %+v", d)
	}
}
