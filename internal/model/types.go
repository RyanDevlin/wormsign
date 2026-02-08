// Package model defines the core data types that flow through the Wormsign
// pipeline: FaultEvent, SuperEvent, DiagnosticBundle, RCAReport, and their
// supporting types.
package model

import (
	"crypto/rand"
	"fmt"
	"time"
)

// Severity represents the severity level of a fault event or RCA report.
type Severity string

const (
	// SeverityCritical indicates a critical fault requiring immediate attention.
	SeverityCritical Severity = "critical"
	// SeverityWarning indicates a warning-level fault that should be investigated.
	SeverityWarning Severity = "warning"
	// SeverityInfo indicates an informational fault for awareness.
	SeverityInfo Severity = "info"
)

// ValidSeverities is the set of all valid Severity values.
var ValidSeverities = map[Severity]bool{
	SeverityCritical: true,
	SeverityWarning:  true,
	SeverityInfo:     true,
}

// IsValid reports whether s is a recognized severity value.
func (s Severity) IsValid() bool {
	return ValidSeverities[s]
}

// ResourceRef identifies a Kubernetes resource by kind, namespace, name, and UID.
type ResourceRef struct {
	// Kind is the Kubernetes resource kind (e.g., "Pod", "Node", "Deployment").
	Kind string
	// Namespace is the namespace of the resource. Empty for cluster-scoped resources.
	Namespace string
	// Name is the name of the resource.
	Name string
	// UID is the Kubernetes UID of the resource.
	UID string
}

// String returns a human-readable representation of the resource reference.
func (r ResourceRef) String() string {
	if r.Namespace != "" {
		return fmt.Sprintf("%s/%s/%s", r.Kind, r.Namespace, r.Name)
	}
	return fmt.Sprintf("%s/%s", r.Kind, r.Name)
}

// FaultEvent represents a detected fault in the cluster. It is the primary
// unit of work flowing through the pipeline.
type FaultEvent struct {
	// ID is a unique identifier for this fault event (UUID v4).
	ID string
	// DetectorName identifies which detector emitted this event (e.g., "PodStuckPending").
	DetectorName string
	// Severity is the severity level assigned by the detector.
	Severity Severity
	// Timestamp is when the fault was detected.
	Timestamp time.Time
	// Resource identifies the Kubernetes resource that triggered the fault.
	Resource ResourceRef
	// Description is a human-readable summary of what was detected.
	Description string
	// Labels are propagated from the triggering Kubernetes resource's labels.
	Labels map[string]string
	// Annotations hold detector-specific metadata.
	Annotations map[string]string
}

// SuperEvent represents a correlated group of FaultEvents that share a common
// root cause. It is produced by the pre-correlation engine before analysis.
type SuperEvent struct {
	// ID is a unique identifier for this super-event (UUID v4).
	ID string
	// CorrelationRule is the name of the rule that produced this grouping
	// (e.g., "NodeCascade", "DeploymentRollout").
	CorrelationRule string
	// PrimaryResource is the root cause resource (e.g., the failing node
	// or the deployment being rolled out).
	PrimaryResource ResourceRef
	// FaultEvents contains all correlated fault events in this group.
	FaultEvents []FaultEvent
	// Timestamp is when the correlation was finalized.
	Timestamp time.Time
	// Severity is the highest severity among constituent fault events.
	Severity Severity
}

// DiagnosticSection holds one piece of diagnostic data collected by a gatherer.
type DiagnosticSection struct {
	// GathererName identifies which gatherer produced this section (e.g., "PodEvents").
	GathererName string
	// Title is a human-readable title for the diagnostic data.
	Title string
	// Content is the collected diagnostic data as structured text.
	Content string
	// Format describes the content encoding: "text", "json", or "yaml".
	Format string
	// Error is non-empty if the gatherer failed to collect this section.
	// A non-empty Error does not block the pipeline; the section is included
	// with whatever partial data was collected.
	Error string
}

// DiagnosticBundle is the structured collection of diagnostic data gathered
// for a fault event or super-event. It is passed to the analyzer.
type DiagnosticBundle struct {
	// FaultEvent is set when the bundle is for a single uncorrelated event.
	// Exactly one of FaultEvent or SuperEvent is non-nil.
	FaultEvent *FaultEvent
	// SuperEvent is set when the bundle is for a correlated group of events.
	// Exactly one of FaultEvent or SuperEvent is non-nil.
	SuperEvent *SuperEvent
	// Timestamp is when gathering was completed.
	Timestamp time.Time
	// Sections contains the diagnostic data collected by all gatherers.
	Sections []DiagnosticSection
}

// TokenUsage tracks the number of input and output tokens consumed by an
// LLM analyzer call.
type TokenUsage struct {
	// Input is the number of input (prompt) tokens consumed.
	Input int
	// Output is the number of output (completion) tokens consumed.
	Output int
}

// Total returns the sum of input and output tokens.
func (t TokenUsage) Total() int {
	return t.Input + t.Output
}

// RCAReport is the final output of the analysis pipeline: a structured root
// cause analysis produced by an analyzer for a fault event or super-event.
type RCAReport struct {
	// FaultEventID is the ID of the source FaultEvent or SuperEvent.
	FaultEventID string
	// Timestamp is when the analysis was completed.
	Timestamp time.Time
	// RootCause is a concise 1-2 sentence root cause statement.
	RootCause string
	// Severity is the reassessed severity after analysis.
	Severity Severity
	// Category classifies the fault: scheduling, storage, application,
	// networking, resources, node, configuration, or unknown.
	Category string
	// Systemic is true if the issue affects or will likely affect multiple
	// pods, or if the root cause is at the node/storage/networking layer.
	Systemic bool
	// BlastRadius describes what else is or could be affected.
	BlastRadius string
	// Remediation contains ordered steps from most impactful to least.
	Remediation []string
	// RelatedResources lists Kubernetes resources related to the root cause.
	RelatedResources []ResourceRef
	// Confidence is the analyzer's confidence in the analysis (0.0 to 1.0).
	Confidence float64
	// RawAnalysis is the full LLM response text for audit purposes.
	RawAnalysis string
	// DiagnosticBundle is the full diagnostic data that was analyzed.
	DiagnosticBundle DiagnosticBundle
	// AnalyzerBackend identifies which backend produced this report
	// (e.g., "claude", "claude-bedrock", "openai", "noop").
	AnalyzerBackend string
	// TokensUsed tracks token consumption for the analysis call.
	TokensUsed TokenUsage
}

// ValidCategories is the set of valid RCA report category values.
var ValidCategories = map[string]bool{
	"scheduling":    true,
	"storage":       true,
	"application":   true,
	"networking":    true,
	"resources":     true,
	"node":          true,
	"configuration": true,
	"unknown":       true,
}

// generateUUID produces a version 4 UUID string using crypto/rand.
// It returns an error if the system's cryptographic random source is
// unavailable.
func generateUUID() (string, error) {
	var uuid [16]byte
	_, err := rand.Read(uuid[:])
	if err != nil {
		return "", fmt.Errorf("generating UUID: %w", err)
	}
	// Set version 4 bits.
	uuid[6] = (uuid[6] & 0x0f) | 0x40
	// Set variant bits (RFC 4122).
	uuid[8] = (uuid[8] & 0x3f) | 0x80

	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		uuid[0:4], uuid[4:6], uuid[6:8], uuid[8:10], uuid[10:16]), nil
}

// NewFaultEvent creates a new FaultEvent with a generated UUID and the
// current timestamp. Labels and annotations are initialized to empty maps
// if nil is passed.
func NewFaultEvent(detectorName string, severity Severity, resource ResourceRef, description string, labels, annotations map[string]string) (*FaultEvent, error) {
	if detectorName == "" {
		return nil, fmt.Errorf("detectorName must not be empty")
	}
	if !severity.IsValid() {
		return nil, fmt.Errorf("invalid severity: %q", severity)
	}
	if resource.Kind == "" {
		return nil, fmt.Errorf("resource kind must not be empty")
	}
	if resource.Name == "" {
		return nil, fmt.Errorf("resource name must not be empty")
	}

	id, err := generateUUID()
	if err != nil {
		return nil, err
	}

	if labels == nil {
		labels = make(map[string]string)
	}
	if annotations == nil {
		annotations = make(map[string]string)
	}

	return &FaultEvent{
		ID:           id,
		DetectorName: detectorName,
		Severity:     severity,
		Timestamp:    time.Now().UTC(),
		Resource:     resource,
		Description:  description,
		Labels:       labels,
		Annotations:  annotations,
	}, nil
}

// NewSuperEvent creates a new SuperEvent with a generated UUID, the current
// timestamp, and the severity set to the highest severity among the provided
// fault events.
func NewSuperEvent(correlationRule string, primaryResource ResourceRef, faultEvents []FaultEvent) (*SuperEvent, error) {
	if correlationRule == "" {
		return nil, fmt.Errorf("correlationRule must not be empty")
	}
	if primaryResource.Kind == "" {
		return nil, fmt.Errorf("primary resource kind must not be empty")
	}
	if primaryResource.Name == "" {
		return nil, fmt.Errorf("primary resource name must not be empty")
	}
	if len(faultEvents) == 0 {
		return nil, fmt.Errorf("faultEvents must not be empty")
	}

	id, err := generateUUID()
	if err != nil {
		return nil, err
	}

	severity := highestSeverity(faultEvents)

	return &SuperEvent{
		ID:              id,
		CorrelationRule: correlationRule,
		PrimaryResource: primaryResource,
		FaultEvents:     faultEvents,
		Timestamp:       time.Now().UTC(),
		Severity:        severity,
	}, nil
}

// NewDiagnosticBundle creates a DiagnosticBundle for a single FaultEvent.
func NewDiagnosticBundle(fe *FaultEvent, sections []DiagnosticSection) *DiagnosticBundle {
	if sections == nil {
		sections = []DiagnosticSection{}
	}
	return &DiagnosticBundle{
		FaultEvent: fe,
		Timestamp:  time.Now().UTC(),
		Sections:   sections,
	}
}

// NewSuperEventDiagnosticBundle creates a DiagnosticBundle for a SuperEvent.
func NewSuperEventDiagnosticBundle(se *SuperEvent, sections []DiagnosticSection) *DiagnosticBundle {
	if sections == nil {
		sections = []DiagnosticSection{}
	}
	return &DiagnosticBundle{
		SuperEvent: se,
		Timestamp:  time.Now().UTC(),
		Sections:   sections,
	}
}

// severityRank returns a numeric rank for severity comparison.
// Higher rank means higher severity.
func severityRank(s Severity) int {
	switch s {
	case SeverityCritical:
		return 3
	case SeverityWarning:
		return 2
	case SeverityInfo:
		return 1
	default:
		return 0
	}
}

// highestSeverity returns the highest severity among the given fault events.
// If the slice is empty, it returns SeverityInfo as a safe default.
func highestSeverity(events []FaultEvent) Severity {
	highest := SeverityInfo
	for _, e := range events {
		if severityRank(e.Severity) > severityRank(highest) {
			highest = e.Severity
		}
	}
	return highest
}
