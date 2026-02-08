// Package filter implements the WormsignPolicy evaluation engine that handles
// all 9 levels of the filter evaluation order defined in the project spec
// (Section 6.2). Filters prevent specific resources, namespaces, or workloads
// from triggering fault detection.
//
// Filter evaluation short-circuits on the first match, following this order:
//
//  1. Global excludeNamespaces (Helm values) — exact match or regex
//  2. Global excludeNamespaceSelector (Helm values) — namespace label match
//  3. WormsignPolicy with action: Suppress + active schedule
//  4. Namespace annotation wormsign.io/suppress
//  5. Resource annotation wormsign.io/exclude: "true"
//  6. Owner annotation wormsign.io/exclude: "true"
//  7. Resource annotation wormsign.io/exclude-detectors (comma-separated)
//  8. Owner annotation wormsign.io/exclude-detectors (comma-separated)
//  9. WormsignPolicy with action: Exclude (resourceSelector, ownerNames globs)
package filter

import (
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	// AnnotationExclude is the annotation key to exclude a resource from all detectors.
	AnnotationExclude = "wormsign.io/exclude"

	// AnnotationExcludeDetectors is the annotation key to exclude a resource
	// from specific detectors (comma-separated list).
	AnnotationExcludeDetectors = "wormsign.io/exclude-detectors"

	// AnnotationSuppress is the annotation key to suppress all detection
	// in a namespace.
	AnnotationSuppress = "wormsign.io/suppress"
)

// FilterReason identifies why a resource was filtered out. These values are
// used as the "reason" label on the wormsign_events_filtered_total metric.
type FilterReason string

const (
	ReasonNamespaceGlobal     FilterReason = "namespace_global"
	ReasonNamespaceLabel      FilterReason = "namespace_label"
	ReasonSuppressPolicy      FilterReason = "suppress_policy"
	ReasonNamespaceAnnotation FilterReason = "namespace_annotation"
	ReasonResourceAnnotation  FilterReason = "resource_annotation"
	ReasonOwnerAnnotation     FilterReason = "owner_annotation"
	ReasonPolicyExclude       FilterReason = "policy_exclude"
)

// FilterResult represents the outcome of filter evaluation.
type FilterResult struct {
	// Excluded is true if the resource should be excluded from detection.
	Excluded bool

	// Reason identifies why the resource was excluded. Empty if not excluded.
	Reason FilterReason

	// DetectorExcluded is true if only specific detectors are excluded
	// (not all detectors). When true, ExcludedDetectors lists which ones.
	DetectorExcluded bool

	// ExcludedDetectors lists detector names that are excluded for this
	// resource. Only populated when DetectorExcluded is true.
	ExcludedDetectors []string
}

// ResourceMeta provides metadata about the resource being evaluated.
// This is an abstraction over Kubernetes resource objects.
type ResourceMeta struct {
	Kind        string
	Namespace   string
	Name        string
	UID         string
	Labels      map[string]string
	Annotations map[string]string
}

// OwnerRef represents an owner reference on a Kubernetes resource.
type OwnerRef struct {
	Kind string
	Name string
	UID  string
}

// NamespaceMeta provides metadata about the namespace containing the resource.
type NamespaceMeta struct {
	Name        string
	Labels      map[string]string
	Annotations map[string]string
}

// PolicyAction defines the action a WormsignPolicy takes.
type PolicyAction string

const (
	PolicyActionExclude  PolicyAction = "Exclude"
	PolicyActionSuppress PolicyAction = "Suppress"
)

// LabelSelector represents a Kubernetes-style label selector with both
// matchLabels and matchExpressions.
type LabelSelector struct {
	MatchLabels      map[string]string
	MatchExpressions []LabelSelectorRequirement
}

// LabelSelectorRequirement represents a single requirement in a label selector.
type LabelSelectorRequirement struct {
	Key      string
	Operator SelectorOperator
	Values   []string
}

// SelectorOperator defines label selector operators.
type SelectorOperator string

const (
	SelectorOpIn           SelectorOperator = "In"
	SelectorOpNotIn        SelectorOperator = "NotIn"
	SelectorOpExists       SelectorOperator = "Exists"
	SelectorOpDoesNotExist SelectorOperator = "DoesNotExist"
)

// PolicySchedule defines a time window during which a policy is active.
type PolicySchedule struct {
	Start time.Time
	End   time.Time
}

// Policy represents a WormsignPolicy CRD for filter evaluation.
type Policy struct {
	Name      string
	Namespace string

	Action    PolicyAction
	Detectors []string // Empty means all detectors.

	// NamespaceSelector applies when the policy is in wormsign-system.
	NamespaceSelector *LabelSelector

	// ResourceSelector matches resource labels.
	ResourceSelector *LabelSelector

	// OwnerNames matches owner names with glob patterns.
	OwnerNames []string

	// Schedule defines an optional time window for the policy.
	Schedule *PolicySchedule
}

// FilterInput contains all the data needed to evaluate filters for a
// single fault detection event.
type FilterInput struct {
	// DetectorName is the name of the detector that would fire.
	DetectorName string

	// Resource is the metadata of the resource being checked.
	Resource ResourceMeta

	// Namespace is the metadata of the namespace containing the resource.
	Namespace NamespaceMeta

	// Owners are the ownerReferences on the resource, resolved to their
	// metadata. The engine checks annotations on each owner.
	Owners []OwnerMeta
}

// OwnerMeta provides metadata about an owner of the resource being evaluated.
type OwnerMeta struct {
	OwnerRef
	Annotations map[string]string
}

// GlobalFilterConfig holds the global filter settings from Helm values.
type GlobalFilterConfig struct {
	// ExcludeNamespaces is a list of namespace names or regex patterns.
	// Exact strings match exactly; strings containing regex metacharacters
	// are compiled as regex patterns.
	ExcludeNamespaces []string

	// ExcludeNamespaceSelector matches namespaces by label.
	ExcludeNamespaceSelector *LabelSelector
}

// Engine evaluates filter rules against resources to determine whether
// fault detection should be suppressed.
type Engine struct {
	mu               sync.RWMutex
	globalConfig     GlobalFilterConfig
	compiledPatterns []*namespacePattern
	policies         []Policy
	logger           *slog.Logger
	nowFunc          func() time.Time // for testing
}

// namespacePattern holds either an exact namespace name or a compiled regex.
type namespacePattern struct {
	raw   string
	exact bool   // true if this is an exact match (no regex)
	re    *regexp.Regexp // non-nil if this is a regex pattern
}

// NewEngine creates a new filter Engine with the given global configuration.
// It compiles regex patterns from excludeNamespaces and returns an error if
// any pattern is invalid.
func NewEngine(config GlobalFilterConfig, logger *slog.Logger) (*Engine, error) {
	if logger == nil {
		logger = slog.Default()
	}

	patterns, err := compileNamespacePatterns(config.ExcludeNamespaces)
	if err != nil {
		return nil, fmt.Errorf("invalid excludeNamespaces pattern: %w", err)
	}

	return &Engine{
		globalConfig:     config,
		compiledPatterns: patterns,
		logger:           logger,
		nowFunc:          time.Now,
	}, nil
}

// SetPolicies replaces the current set of WormsignPolicy objects.
// This is called when CRD watch events are received.
func (e *Engine) SetPolicies(policies []Policy) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.policies = make([]Policy, len(policies))
	copy(e.policies, policies)
}

// Evaluate runs the filter evaluation chain against the provided input.
// It returns a FilterResult indicating whether the resource should be
// excluded and why. Evaluation short-circuits on the first match.
func (e *Engine) Evaluate(input FilterInput) FilterResult {
	// Level 1: Global excludeNamespaces (exact match and regex)
	if e.matchesGlobalNamespaceExclusion(input.Namespace.Name) {
		e.logger.Debug("resource filtered by global namespace exclusion",
			"namespace", input.Namespace.Name,
			"detector", input.DetectorName,
			"resource", input.Resource.Name,
		)
		return FilterResult{Excluded: true, Reason: ReasonNamespaceGlobal}
	}

	// Level 2: Global excludeNamespaceSelector (namespace label match)
	if e.matchesNamespaceLabelSelector(input.Namespace.Labels) {
		e.logger.Debug("resource filtered by namespace label selector",
			"namespace", input.Namespace.Name,
			"detector", input.DetectorName,
			"resource", input.Resource.Name,
		)
		return FilterResult{Excluded: true, Reason: ReasonNamespaceLabel}
	}

	// Level 3: WormsignPolicy with action: Suppress + active schedule
	if policyName := e.matchesSuppressPolicy(input); policyName != "" {
		e.logger.Debug("resource filtered by suppress policy",
			"namespace", input.Namespace.Name,
			"detector", input.DetectorName,
			"resource", input.Resource.Name,
			"policy", policyName,
		)
		return FilterResult{Excluded: true, Reason: ReasonSuppressPolicy}
	}

	// Level 4: Namespace annotation wormsign.io/suppress
	if isAnnotationTrue(input.Namespace.Annotations, AnnotationSuppress) {
		e.logger.Debug("resource filtered by namespace suppress annotation",
			"namespace", input.Namespace.Name,
			"detector", input.DetectorName,
			"resource", input.Resource.Name,
		)
		return FilterResult{Excluded: true, Reason: ReasonNamespaceAnnotation}
	}

	// Level 5: Resource annotation wormsign.io/exclude: "true"
	if isAnnotationTrue(input.Resource.Annotations, AnnotationExclude) {
		e.logger.Debug("resource filtered by resource exclude annotation",
			"namespace", input.Namespace.Name,
			"detector", input.DetectorName,
			"resource", input.Resource.Name,
		)
		return FilterResult{Excluded: true, Reason: ReasonResourceAnnotation}
	}

	// Level 6: Owner annotation wormsign.io/exclude: "true"
	if ownerHasAnnotationTrue(input.Owners, AnnotationExclude) {
		e.logger.Debug("resource filtered by owner exclude annotation",
			"namespace", input.Namespace.Name,
			"detector", input.DetectorName,
			"resource", input.Resource.Name,
		)
		return FilterResult{Excluded: true, Reason: ReasonOwnerAnnotation}
	}

	// Level 7: Resource annotation wormsign.io/exclude-detectors
	if detectors := getExcludedDetectors(input.Resource.Annotations); len(detectors) > 0 {
		if containsDetector(detectors, input.DetectorName) {
			e.logger.Debug("resource filtered by resource exclude-detectors annotation",
				"namespace", input.Namespace.Name,
				"detector", input.DetectorName,
				"resource", input.Resource.Name,
			)
			return FilterResult{
				Excluded:          true,
				Reason:            ReasonResourceAnnotation,
				DetectorExcluded:  true,
				ExcludedDetectors: detectors,
			}
		}
	}

	// Level 8: Owner annotation wormsign.io/exclude-detectors
	if detectors := getOwnerExcludedDetectors(input.Owners); len(detectors) > 0 {
		if containsDetector(detectors, input.DetectorName) {
			e.logger.Debug("resource filtered by owner exclude-detectors annotation",
				"namespace", input.Namespace.Name,
				"detector", input.DetectorName,
				"resource", input.Resource.Name,
			)
			return FilterResult{
				Excluded:          true,
				Reason:            ReasonOwnerAnnotation,
				DetectorExcluded:  true,
				ExcludedDetectors: detectors,
			}
		}
	}

	// Level 9: WormsignPolicy with action: Exclude
	if policyName := e.matchesExcludePolicy(input); policyName != "" {
		e.logger.Debug("resource filtered by exclude policy",
			"namespace", input.Namespace.Name,
			"detector", input.DetectorName,
			"resource", input.Resource.Name,
			"policy", policyName,
		)
		return FilterResult{Excluded: true, Reason: ReasonPolicyExclude}
	}

	return FilterResult{Excluded: false}
}

// matchesGlobalNamespaceExclusion checks if the namespace matches any global
// exclude pattern (exact match or regex).
func (e *Engine) matchesGlobalNamespaceExclusion(namespace string) bool {
	for _, p := range e.compiledPatterns {
		if p.exact {
			if p.raw == namespace {
				return true
			}
		} else if p.re != nil {
			if p.re.MatchString(namespace) {
				return true
			}
		}
	}
	return false
}

// matchesNamespaceLabelSelector checks if the namespace labels match the
// global excludeNamespaceSelector.
func (e *Engine) matchesNamespaceLabelSelector(labels map[string]string) bool {
	if e.globalConfig.ExcludeNamespaceSelector == nil {
		return false
	}
	return matchesLabelSelector(labels, e.globalConfig.ExcludeNamespaceSelector)
}

// matchesSuppressPolicy checks if any Suppress policy is active and applies
// to the resource's namespace. Returns the matching policy name or "".
func (e *Engine) matchesSuppressPolicy(input FilterInput) string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	now := e.now()

	for i := range e.policies {
		p := &e.policies[i]
		if p.Action != PolicyActionSuppress {
			continue
		}

		// Check if the schedule is active.
		if p.Schedule != nil {
			if now.Before(p.Schedule.Start) || now.After(p.Schedule.End) {
				continue
			}
		}

		// Check namespace scope.
		if !e.policyAppliesToNamespace(p, input.Namespace) {
			continue
		}

		// Check detector scope.
		if len(p.Detectors) > 0 && !containsDetector(p.Detectors, input.DetectorName) {
			continue
		}

		return p.Name
	}
	return ""
}

// matchesExcludePolicy checks if any Exclude policy matches the resource.
// Returns the matching policy name or "".
func (e *Engine) matchesExcludePolicy(input FilterInput) string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	for i := range e.policies {
		p := &e.policies[i]
		if p.Action != PolicyActionExclude {
			continue
		}

		// Check namespace scope.
		if !e.policyAppliesToNamespace(p, input.Namespace) {
			continue
		}

		// Check detector scope.
		if len(p.Detectors) > 0 && !containsDetector(p.Detectors, input.DetectorName) {
			continue
		}

		// Check resource selector.
		if p.ResourceSelector != nil {
			if !matchesLabelSelector(input.Resource.Labels, p.ResourceSelector) {
				continue
			}
		}

		// Check owner name patterns.
		if len(p.OwnerNames) > 0 {
			if !matchesOwnerNames(input.Owners, p.OwnerNames) {
				continue
			}
		}

		return p.Name
	}
	return ""
}

// policyAppliesToNamespace determines if a policy applies to the given namespace.
// A policy created in the same namespace applies to that namespace.
// A policy with a namespaceSelector (typically from wormsign-system) applies
// to namespaces matching the selector.
func (e *Engine) policyAppliesToNamespace(p *Policy, ns NamespaceMeta) bool {
	// If the policy has a namespace selector, use it.
	if p.NamespaceSelector != nil {
		return matchesLabelSelector(ns.Labels, p.NamespaceSelector)
	}

	// Otherwise, the policy applies only to its own namespace.
	return p.Namespace == ns.Name
}

// now returns the current time, using the test hook if set.
func (e *Engine) now() time.Time {
	if e.nowFunc != nil {
		return e.nowFunc()
	}
	return time.Now()
}

// isAnnotationTrue checks if the given annotation key has a "true" value.
func isAnnotationTrue(annotations map[string]string, key string) bool {
	if annotations == nil {
		return false
	}
	return strings.EqualFold(annotations[key], "true")
}

// ownerHasAnnotationTrue checks if any owner has the given annotation set to "true".
func ownerHasAnnotationTrue(owners []OwnerMeta, key string) bool {
	for _, owner := range owners {
		if isAnnotationTrue(owner.Annotations, key) {
			return true
		}
	}
	return false
}

// getExcludedDetectors parses the comma-separated detector names from the
// exclude-detectors annotation.
func getExcludedDetectors(annotations map[string]string) []string {
	if annotations == nil {
		return nil
	}
	val, ok := annotations[AnnotationExcludeDetectors]
	if !ok || val == "" {
		return nil
	}
	parts := strings.Split(val, ",")
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		trimmed := strings.TrimSpace(p)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// getOwnerExcludedDetectors collects excluded detectors from all owners.
// Returns the union of all owners' exclude-detectors annotations.
func getOwnerExcludedDetectors(owners []OwnerMeta) []string {
	var all []string
	seen := make(map[string]struct{})
	for _, owner := range owners {
		detectors := getExcludedDetectors(owner.Annotations)
		for _, d := range detectors {
			if _, ok := seen[d]; !ok {
				seen[d] = struct{}{}
				all = append(all, d)
			}
		}
	}
	return all
}

// containsDetector checks if the detector name is in the list.
func containsDetector(detectors []string, name string) bool {
	for _, d := range detectors {
		if d == name {
			return true
		}
	}
	return false
}

// matchesLabelSelector evaluates a label selector against a set of labels.
func matchesLabelSelector(labels map[string]string, selector *LabelSelector) bool {
	if selector == nil {
		return true
	}

	// Check matchLabels: all must be present and equal.
	for key, val := range selector.MatchLabels {
		if labels == nil {
			return false
		}
		if labels[key] != val {
			return false
		}
	}

	// Check matchExpressions: all must be satisfied.
	for _, req := range selector.MatchExpressions {
		if !matchesLabelRequirement(labels, req) {
			return false
		}
	}

	return true
}

// matchesLabelRequirement evaluates a single label selector requirement.
func matchesLabelRequirement(labels map[string]string, req LabelSelectorRequirement) bool {
	val, exists := labels[req.Key]

	switch req.Operator {
	case SelectorOpIn:
		if !exists {
			return false
		}
		for _, v := range req.Values {
			if v == val {
				return true
			}
		}
		return false

	case SelectorOpNotIn:
		if !exists {
			return true
		}
		for _, v := range req.Values {
			if v == val {
				return false
			}
		}
		return true

	case SelectorOpExists:
		return exists

	case SelectorOpDoesNotExist:
		return !exists

	default:
		return false
	}
}

// matchesOwnerNames checks if any owner's name matches any of the glob patterns.
func matchesOwnerNames(owners []OwnerMeta, patterns []string) bool {
	for _, owner := range owners {
		for _, pattern := range patterns {
			matched, err := filepath.Match(pattern, owner.Name)
			if err != nil {
				// Invalid pattern — skip it. This should be caught during
				// policy validation, not at evaluation time.
				continue
			}
			if matched {
				return true
			}
		}
	}
	return false
}

// hasRegexMeta returns true if the string contains regex metacharacters,
// indicating it should be treated as a regex rather than an exact match.
func hasRegexMeta(s string) bool {
	for _, ch := range s {
		switch ch {
		case '.', '*', '+', '?', '(', ')', '[', ']', '{', '}', '^', '$', '|', '\\':
			return true
		}
	}
	return false
}

// compileNamespacePatterns compiles the excludeNamespaces list into
// namespacePattern objects. Strings without regex metacharacters are treated
// as exact matches; strings with metacharacters are compiled as regex.
func compileNamespacePatterns(patterns []string) ([]*namespacePattern, error) {
	result := make([]*namespacePattern, 0, len(patterns))
	for _, p := range patterns {
		if p == "" {
			continue
		}
		np := &namespacePattern{raw: p}
		if hasRegexMeta(p) {
			// Anchor the regex to match the full namespace name.
			anchored := p
			if !strings.HasPrefix(anchored, "^") {
				anchored = "^" + anchored
			}
			if !strings.HasSuffix(anchored, "$") {
				anchored = anchored + "$"
			}
			re, err := regexp.Compile(anchored)
			if err != nil {
				return nil, fmt.Errorf("pattern %q: %w", p, err)
			}
			np.re = re
		} else {
			np.exact = true
		}
		result = append(result, np)
	}
	return result, nil
}
