package puppet

import (
	"encoding/asn1"
	"testing"
)

func TestOIDByName(t *testing.T) {
	tests := []struct {
		name    string
		wantOID asn1.ObjectIdentifier
		wantOK  bool
	}{
		{"pp_cli_auth", asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 34380, 1, 3, 39}, true},
		{"pp_role", asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 34380, 1, 1, 13}, true},
		{"pp_environment", asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 34380, 1, 1, 12}, true},
		{"pp_uuid", asn1.ObjectIdentifier{1, 3, 6, 1, 4, 1, 34380, 1, 1, 1}, true},
		{"challengePassword", asn1.ObjectIdentifier{1, 2, 840, 113549, 1, 9, 7}, true},
		{"unknown_extension", nil, false},
		{"", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oid, ok := OIDByName(tt.name)
			if ok != tt.wantOK {
				t.Errorf("OIDByName(%q) ok = %v, want %v", tt.name, ok, tt.wantOK)
			}
			if tt.wantOK && !oid.Equal(tt.wantOID) {
				t.Errorf("OIDByName(%q) = %v, want %v", tt.name, oid, tt.wantOID)
			}
		})
	}
}

func TestIsKnownOID(t *testing.T) {
	if !IsKnownOID("pp_cli_auth") {
		t.Error("expected pp_cli_auth to be known")
	}
	if IsKnownOID("unknown") {
		t.Error("expected unknown to not be known")
	}
}

func TestPuppetOIDsCompleteness(t *testing.T) {
	if len(PuppetOIDs) != 30 {
		t.Errorf("expected 30 OIDs, got %d", len(PuppetOIDs))
	}
}
