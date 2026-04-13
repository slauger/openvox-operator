package puppet

import "encoding/asn1"

// PuppetOIDs maps Puppet CSR extension names to their ASN.1 OIDs.
var PuppetOIDs = map[string]asn1.ObjectIdentifier{
	"pp_uuid":             {1, 3, 6, 1, 4, 1, 34380, 1, 1, 1},
	"pp_instance_id":      {1, 3, 6, 1, 4, 1, 34380, 1, 1, 2},
	"pp_image_name":       {1, 3, 6, 1, 4, 1, 34380, 1, 1, 3},
	"pp_preshared_key":    {1, 3, 6, 1, 4, 1, 34380, 1, 1, 4},
	"pp_cost_center":      {1, 3, 6, 1, 4, 1, 34380, 1, 1, 5},
	"pp_product":          {1, 3, 6, 1, 4, 1, 34380, 1, 1, 6},
	"pp_project":          {1, 3, 6, 1, 4, 1, 34380, 1, 1, 7},
	"pp_application":      {1, 3, 6, 1, 4, 1, 34380, 1, 1, 8},
	"pp_service":          {1, 3, 6, 1, 4, 1, 34380, 1, 1, 9},
	"pp_employee":         {1, 3, 6, 1, 4, 1, 34380, 1, 1, 10},
	"pp_created_by":       {1, 3, 6, 1, 4, 1, 34380, 1, 1, 11},
	"pp_environment":      {1, 3, 6, 1, 4, 1, 34380, 1, 1, 12},
	"pp_role":             {1, 3, 6, 1, 4, 1, 34380, 1, 1, 13},
	"pp_software_version": {1, 3, 6, 1, 4, 1, 34380, 1, 1, 14},
	"pp_department":       {1, 3, 6, 1, 4, 1, 34380, 1, 1, 15},
	"pp_cluster":          {1, 3, 6, 1, 4, 1, 34380, 1, 1, 16},
	"pp_provisioner":      {1, 3, 6, 1, 4, 1, 34380, 1, 1, 17},
	"pp_region":           {1, 3, 6, 1, 4, 1, 34380, 1, 1, 18},
	"pp_datacenter":       {1, 3, 6, 1, 4, 1, 34380, 1, 1, 19},
	"pp_zone":             {1, 3, 6, 1, 4, 1, 34380, 1, 1, 20},
	"pp_network":          {1, 3, 6, 1, 4, 1, 34380, 1, 1, 21},
	"pp_securitypolicy":   {1, 3, 6, 1, 4, 1, 34380, 1, 1, 22},
	"pp_cloudplatform":    {1, 3, 6, 1, 4, 1, 34380, 1, 1, 23},
	"pp_apptier":          {1, 3, 6, 1, 4, 1, 34380, 1, 1, 24},
	"pp_hostname":         {1, 3, 6, 1, 4, 1, 34380, 1, 1, 25},
	"pp_owner":            {1, 3, 6, 1, 4, 1, 34380, 1, 1, 26},
	"pp_authorization":    {1, 3, 6, 1, 4, 1, 34380, 1, 3, 1},
	"pp_auth_token":       {1, 3, 6, 1, 4, 1, 34380, 1, 3, 2},
	"pp_cli_auth":         {1, 3, 6, 1, 4, 1, 34380, 1, 3, 39},
	"challengePassword":   {1, 2, 840, 113549, 1, 9, 7},
}

// OIDByName returns the ASN.1 OID for a known Puppet extension name.
func OIDByName(name string) (asn1.ObjectIdentifier, bool) {
	oid, ok := PuppetOIDs[name]
	return oid, ok
}

// IsKnownOID reports whether name is a recognized Puppet extension name.
func IsKnownOID(name string) bool {
	_, ok := PuppetOIDs[name]
	return ok
}
