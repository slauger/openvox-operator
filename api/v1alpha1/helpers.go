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

// AnnotationPaused suspends reconciliation for a single resource.
//
// It is an annotation rather than a spec field on purpose: pausing is a
// temporary operational measure, not a declared desired state, and it has no
// business being committed to a GitOps repository.
const AnnotationPaused = "openvox.voxpupuli.org/paused"

// ConditionPaused reports that reconciliation is suspended by AnnotationPaused.
const ConditionPaused = "Paused"
