package gatherer

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	v1alpha1 "github.com/k8s-wormsign/k8s-wormsign/api/v1alpha1"
	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

// ResourceFetcher abstracts Kubernetes API interactions for custom gatherers.
// This allows the custom gatherer to be tested without a real API server.
type ResourceFetcher interface {
	// GetResource fetches a single Kubernetes resource by API version, resource
	// type, namespace, and name. Returns the resource as a JSON-encoded byte
	// slice.
	GetResource(ctx context.Context, apiVersion, resource, namespace, name string) ([]byte, error)

	// ListResources lists all resources of the given type in the specified
	// namespace. Returns the list as a JSON-encoded byte slice.
	ListResources(ctx context.Context, apiVersion, resource, namespace string) ([]byte, error)

	// GetLogs fetches container logs for a pod. Returns the log text.
	GetLogs(ctx context.Context, namespace, podName, container string, tailLines *int64, previous bool) (string, error)
}

// CustomGatherer evaluates a WormsignGatherer CRD to collect diagnostic data.
// It implements both the Gatherer and TriggerMatcher interfaces.
type CustomGatherer struct {
	spec    v1alpha1.WormsignGathererSpec
	name    string
	fetcher ResourceFetcher
	logger  *slog.Logger
}

// NewCustomGatherer creates a new CustomGatherer from a WormsignGatherer CRD.
// The name should be the CRD metadata.namespace/metadata.name (or just
// metadata.name for cluster-scoped).
func NewCustomGatherer(name string, spec v1alpha1.WormsignGathererSpec, fetcher ResourceFetcher, logger *slog.Logger) (*CustomGatherer, error) {
	if name == "" {
		return nil, fmt.Errorf("custom gatherer name must not be empty")
	}
	if fetcher == nil {
		return nil, fmt.Errorf("resource fetcher must not be nil")
	}
	if len(spec.Collect) == 0 {
		return nil, fmt.Errorf("custom gatherer %q: at least one collect action is required", name)
	}
	for i, action := range spec.Collect {
		if err := validateCollectAction(action, i); err != nil {
			return nil, fmt.Errorf("custom gatherer %q: %w", name, err)
		}
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &CustomGatherer{
		spec:    spec,
		name:    name,
		fetcher: fetcher,
		logger:  logger,
	}, nil
}

// validateCollectAction checks that a CollectAction has the required fields
// for its type.
func validateCollectAction(action v1alpha1.CollectAction, index int) error {
	switch action.Type {
	case v1alpha1.CollectTypeResource:
		if action.APIVersion == "" {
			return fmt.Errorf("collect[%d]: apiVersion is required for resource type", index)
		}
		if action.Resource == "" {
			return fmt.Errorf("collect[%d]: resource is required for resource type", index)
		}
		if !action.ListAll && action.Name == "" {
			return fmt.Errorf("collect[%d]: name is required for resource type when listAll is false", index)
		}
	case v1alpha1.CollectTypeLogs:
		// Logs type: container is optional (defaults to first container),
		// tailLines is optional.
	default:
		return fmt.Errorf("collect[%d]: unknown collect type %q", index, action.Type)
	}
	return nil
}

// Name returns the custom gatherer's name.
func (cg *CustomGatherer) Name() string {
	return cg.name
}

// ResourceKinds returns the resource kinds this custom gatherer triggers on.
func (cg *CustomGatherer) ResourceKinds() []string {
	return cg.spec.TriggerOn.ResourceKinds
}

// DetectorNames returns the detector names this custom gatherer triggers on.
func (cg *CustomGatherer) DetectorNames() []string {
	return cg.spec.TriggerOn.Detectors
}

// Gather executes all collect actions defined in the CRD and returns a
// single DiagnosticSection with the combined results.
func (cg *CustomGatherer) Gather(ctx context.Context, ref model.ResourceRef) (*model.DiagnosticSection, error) {
	vars := TemplateVars{
		Resource: ref,
	}

	var results []string
	var errors []string

	for i, action := range cg.spec.Collect {
		result, err := cg.executeAction(ctx, action, vars)
		if err != nil {
			cg.logger.Warn("custom gatherer collect action failed",
				"gatherer", cg.name,
				"action_index", i,
				"action_type", string(action.Type),
				"error", err,
			)
			errors = append(errors, fmt.Sprintf("collect[%d] (%s): %s", i, action.Type, err.Error()))
			continue
		}
		if result != "" {
			results = append(results, result)
		}
	}

	section := &model.DiagnosticSection{
		GathererName: cg.name,
		Title:        cg.titleOrName(),
		Content:      strings.Join(results, "\n---\n"),
		Format:       "text",
	}

	if len(errors) > 0 {
		section.Error = strings.Join(errors, "; ")
	}

	return section, nil
}

// titleOrName returns the CRD description as the section title, falling back
// to the gatherer name.
func (cg *CustomGatherer) titleOrName() string {
	if cg.spec.Description != "" {
		return cg.spec.Description
	}
	return cg.name
}

// executeAction runs a single CollectAction and returns the result as text.
func (cg *CustomGatherer) executeAction(ctx context.Context, action v1alpha1.CollectAction, vars TemplateVars) (string, error) {
	switch action.Type {
	case v1alpha1.CollectTypeResource:
		return cg.collectResource(ctx, action, vars)
	case v1alpha1.CollectTypeLogs:
		return cg.collectLogs(ctx, action, vars)
	default:
		return "", fmt.Errorf("unsupported collect type: %q", action.Type)
	}
}

// collectResource fetches a Kubernetes resource or lists resources.
func (cg *CustomGatherer) collectResource(ctx context.Context, action v1alpha1.CollectAction, vars TemplateVars) (string, error) {
	namespace := ExpandTemplate(action.Namespace, vars)
	if namespace == "" {
		namespace = vars.Resource.Namespace
	}

	if action.ListAll {
		data, err := cg.fetcher.ListResources(ctx, action.APIVersion, action.Resource, namespace)
		if err != nil {
			return "", fmt.Errorf("listing %s/%s in %s: %w", action.APIVersion, action.Resource, namespace, err)
		}
		if action.JSONPath != "" {
			return extractJSONPath(data, action.JSONPath)
		}
		return string(data), nil
	}

	name := ExpandTemplate(action.Name, vars)
	data, err := cg.fetcher.GetResource(ctx, action.APIVersion, action.Resource, namespace, name)
	if err != nil {
		return "", fmt.Errorf("getting %s/%s %s/%s: %w", action.APIVersion, action.Resource, namespace, name, err)
	}

	if action.JSONPath != "" {
		return extractJSONPath(data, action.JSONPath)
	}
	return string(data), nil
}

// collectLogs fetches container logs.
func (cg *CustomGatherer) collectLogs(ctx context.Context, action v1alpha1.CollectAction, vars TemplateVars) (string, error) {
	namespace := vars.Resource.Namespace
	podName := vars.Resource.Name

	// Logs only apply to pods.
	if vars.Resource.Kind != "" && vars.Resource.Kind != "Pod" {
		return "", fmt.Errorf("logs collection requires a Pod resource, got %s", vars.Resource.Kind)
	}

	container := ExpandTemplate(action.Container, vars)
	logs, err := cg.fetcher.GetLogs(ctx, namespace, podName, container, action.TailLines, action.Previous)
	if err != nil {
		return "", fmt.Errorf("getting logs for %s/%s container=%s: %w", namespace, podName, container, err)
	}
	return logs, nil
}

// extractJSONPath applies a simple JSONPath-like extraction from JSON data.
// This supports a subset of JSONPath: dot-notation field access and wildcard
// suffixes (e.g., ".metadata.annotations.sidecar\.istio\.io/*").
// For v1, this provides basic field extraction; more complex JSONPath is
// deferred to a library if needed.
func extractJSONPath(data []byte, path string) (string, error) {
	// Parse the JSON into a generic structure.
	var obj interface{}
	if err := json.Unmarshal(data, &obj); err != nil {
		return "", fmt.Errorf("parsing JSON for jsonPath extraction: %w", err)
	}

	// Remove leading dot if present.
	path = strings.TrimPrefix(path, ".")

	// Split path into segments, respecting escaped dots.
	segments := splitJSONPath(path)

	current := obj
	for i, seg := range segments {
		if seg == "*" {
			// Wildcard: return JSON representation of current value.
			result, err := json.MarshalIndent(current, "", "  ")
			if err != nil {
				return "", fmt.Errorf("marshaling wildcard result: %w", err)
			}
			return string(result), nil
		}

		m, ok := current.(map[string]interface{})
		if !ok {
			return "", fmt.Errorf("jsonPath: expected object at segment %d (%q), got %T", i, seg, current)
		}

		val, exists := m[seg]
		if !exists {
			return "", fmt.Errorf("jsonPath: field %q not found at segment %d", seg, i)
		}
		current = val
	}

	// Marshal the final value.
	result, err := json.MarshalIndent(current, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshaling jsonPath result: %w", err)
	}
	return string(result), nil
}

// splitJSONPath splits a JSONPath expression by dots, respecting backslash
// escapes (e.g., "sidecar\.istio\.io" is a single segment).
func splitJSONPath(path string) []string {
	var segments []string
	var current strings.Builder

	escaped := false
	for _, ch := range path {
		if escaped {
			current.WriteRune(ch)
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if ch == '.' {
			if current.Len() > 0 {
				segments = append(segments, current.String())
				current.Reset()
			}
			continue
		}
		current.WriteRune(ch)
	}
	if current.Len() > 0 {
		segments = append(segments, current.String())
	}
	return segments
}

// Compile-time interface checks.
var (
	_ Gatherer       = (*CustomGatherer)(nil)
	_ TriggerMatcher = (*CustomGatherer)(nil)
)
