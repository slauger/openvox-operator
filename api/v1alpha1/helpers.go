package v1alpha1

// BoolValue returns the value p points at, or def when p is nil.
//
// Optional boolean fields that default to true are declared as pointers: a
// plain bool with omitempty serialises false as absent, so the API server
// would re-apply the default and the field could never be set to false.
// Reading such a field always goes through this helper, which supplies the
// same default the CRD declares.
func BoolValue(p *bool, def bool) bool {
	if p == nil {
		return def
	}
	return *p
}
