package controller

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	openvoxv1alpha1 "github.com/slauger/openvox-operator/api/v1alpha1"
)

// AnnotationRenderedFrom records which resources produced the current content
// of a rendered Secret, as sorted "name=generation" pairs.
//
// The status controllers match on this rather than re-parsing the rendered
// file. Parsing answers a weaker question: enc.yaml carries no resource name at
// all, and a file left behind by a failed re-render still describes the old
// spec, so a resource whose current spec never reached a server would read as
// active. The annotation is written in the same update as the data, so the two
// cannot disagree, and the generation says which spec the content came from.
const AnnotationRenderedFrom = "openvox.voxpupuli.org/rendered-from"

// renderSource identifies one resource a rendered file was built from.
type renderSource struct {
	Name       string
	Generation int64
}

// sourceOf describes an object as the source of a rendered file.
func sourceOf(obj client.Object) renderSource {
	return renderSource{Name: obj.GetName(), Generation: obj.GetGeneration()}
}

// renderedFromAnnotation builds the annotation for a set of sources. An empty
// set still yields the key, so a Secret rendered from nothing overwrites what a
// previous render recorded instead of keeping a stale claim.
func renderedFromAnnotation(sources []renderSource) map[string]string {
	parts := make([]string, 0, len(sources))
	for _, s := range sources {
		parts = append(parts, fmt.Sprintf("%s=%d", s.Name, s.Generation))
	}
	// Sorted so an unchanged render produces an unchanged value and does not
	// rewrite the Secret.
	sort.Strings(parts)
	return map[string]string{AnnotationRenderedFrom: strings.Join(parts, ",")}
}

// renderedGeneration returns the generation the named resource had when the
// file was rendered, and whether it contributed to it at all.
func renderedGeneration(annotations map[string]string, name string) (int64, bool) {
	value, ok := annotations[AnnotationRenderedFrom]
	if !ok || value == "" {
		return 0, false
	}
	for part := range strings.SplitSeq(value, ",") {
		key, gen, found := strings.Cut(part, "=")
		if !found || key != name {
			continue
		}
		parsed, err := strconv.ParseInt(gen, 10, 64)
		if err != nil {
			return 0, false
		}
		return parsed, true
	}
	return 0, false
}

// renderedSourceRequests maps a rendered Secret to the resources it was
// rendered from.
//
// controller-runtime runs a map function against both the old and the new
// object of an update, so a resource dropped from a re-render is enqueued from
// the old annotation just as the one that replaced it is enqueued from the new.
// A Secret the operator did not render carries no annotation and maps to
// nothing, which keeps an unrelated Secret from fanning out over every resource
// in the namespace.
func renderedSourceRequests(obj client.Object) []ctrl.Request {
	value := obj.GetAnnotations()[AnnotationRenderedFrom]
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	requests := make([]ctrl.Request, 0, len(parts))
	for _, part := range parts {
		name, _, found := strings.Cut(part, "=")
		if !found || name == "" {
			continue
		}
		requests = append(requests, ctrl.Request{
			NamespacedName: types.NamespacedName{Name: name, Namespace: obj.GetNamespace()},
		})
	}
	return requests
}

// allOverride reports whether every Config replaces the built-in binary that
// would consume a rendered file, as named by command. A single Config that does
// not still renders the file, so the resource is only bypassed when they all
// opt out.
func allOverride(configs []openvoxv1alpha1.Config, command func(openvoxv1alpha1.Config) string) bool {
	for _, cfg := range configs {
		if command(cfg) == "" {
			return false
		}
	}
	return len(configs) > 0
}

// overrideAutosign and overrideExternalNodes select the command that replaces
// the built-in binary for each kind of rendered file.
func overrideAutosign(cfg openvoxv1alpha1.Config) string {
	return cfg.Spec.Puppet.AutosignCommand
}

func overrideExternalNodes(cfg openvoxv1alpha1.Config) string {
	return cfg.Spec.Puppet.ExternalNodesCommand
}
