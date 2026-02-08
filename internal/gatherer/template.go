package gatherer

import (
	"strings"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

// TemplateVars holds the values available for template variable expansion
// in custom gatherer CRD collect actions.
type TemplateVars struct {
	Resource model.ResourceRef
	NodeName string
}

// ExpandTemplate replaces template variables in the input string with their
// values from the provided TemplateVars. Supported variables:
//
//   - {resource.name}      — the resource name
//   - {resource.namespace} — the resource namespace
//   - {resource.kind}      — the resource kind
//   - {resource.uid}       — the resource UID
//   - {node.name}          — the node name (from gatherer context)
//
// Unrecognized template variables are left unchanged.
func ExpandTemplate(s string, vars TemplateVars) string {
	// Fast path: no template variables present.
	if !strings.Contains(s, "{") {
		return s
	}

	replacements := []string{
		"{resource.name}", vars.Resource.Name,
		"{resource.namespace}", vars.Resource.Namespace,
		"{resource.kind}", vars.Resource.Kind,
		"{resource.uid}", vars.Resource.UID,
		"{node.name}", vars.NodeName,
	}

	r := strings.NewReplacer(replacements...)
	return r.Replace(s)
}
