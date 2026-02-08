package model

import (
	"regexp"
	"testing"
	"time"
)

// uuidRegex matches a UUID v4 string.
var uuidRegex = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func TestSeverity_IsValid(t *testing.T) {
	tests := []struct {
		severity Severity
		want     bool
	}{
		{SeverityCritical, true},
		{SeverityWarning, true},
		{SeverityInfo, true},
		{"", false},
		{"unknown", false},
		{"CRITICAL", false}, // case-sensitive
	}
	for _, tt := range tests {
		t.Run(string(tt.severity), func(t *testing.T) {
			got := tt.severity.IsValid()
			if got != tt.want {
				t.Errorf("Severity(%q).IsValid() = %v, want %v", tt.severity, got, tt.want)
			}
		})
	}
}

func TestResourceRef_String(t *testing.T) {
	tests := []struct {
		name string
		ref  ResourceRef
		want string
	}{
		{
			name: "namespaced resource",
			ref:  ResourceRef{Kind: "Pod", Namespace: "default", Name: "my-pod", UID: "uid-1"},
			want: "Pod/default/my-pod",
		},
		{
			name: "cluster-scoped resource",
			ref:  ResourceRef{Kind: "Node", Name: "node-1", UID: "uid-2"},
			want: "Node/node-1",
		},
		{
			name: "empty namespace treated as cluster-scoped",
			ref:  ResourceRef{Kind: "PersistentVolume", Namespace: "", Name: "pv-1"},
			want: "PersistentVolume/pv-1",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.ref.String()
			if got != tt.want {
				t.Errorf("ResourceRef.String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewFaultEvent_Success(t *testing.T) {
	ref := ResourceRef{Kind: "Pod", Namespace: "default", Name: "test-pod", UID: "uid-123"}
	labels := map[string]string{"app": "test"}
	annotations := map[string]string{"detector.wormsign.io/reason": "crash"}

	before := time.Now().UTC()
	fe, err := NewFaultEvent("PodCrashLoop", SeverityCritical, ref, "pod is crash looping", labels, annotations)
	after := time.Now().UTC()

	if err != nil {
		t.Fatalf("NewFaultEvent() error = %v", err)
	}
	if fe == nil {
		t.Fatal("NewFaultEvent() returned nil")
	}
	if !uuidRegex.MatchString(fe.ID) {
		t.Errorf("ID %q does not match UUID v4 format", fe.ID)
	}
	if fe.DetectorName != "PodCrashLoop" {
		t.Errorf("DetectorName = %q, want %q", fe.DetectorName, "PodCrashLoop")
	}
	if fe.Severity != SeverityCritical {
		t.Errorf("Severity = %q, want %q", fe.Severity, SeverityCritical)
	}
	if fe.Timestamp.Before(before) || fe.Timestamp.After(after) {
		t.Errorf("Timestamp %v not in range [%v, %v]", fe.Timestamp, before, after)
	}
	if fe.Resource != ref {
		t.Errorf("Resource = %+v, want %+v", fe.Resource, ref)
	}
	if fe.Description != "pod is crash looping" {
		t.Errorf("Description = %q, want %q", fe.Description, "pod is crash looping")
	}
	if fe.Labels["app"] != "test" {
		t.Errorf("Labels[app] = %q, want %q", fe.Labels["app"], "test")
	}
	if fe.Annotations["detector.wormsign.io/reason"] != "crash" {
		t.Errorf("Annotations mismatch")
	}
}

func TestNewFaultEvent_NilMapsInitialized(t *testing.T) {
	ref := ResourceRef{Kind: "Pod", Namespace: "ns", Name: "p", UID: "u"}
	fe, err := NewFaultEvent("PodFailed", SeverityWarning, ref, "failed", nil, nil)
	if err != nil {
		t.Fatalf("NewFaultEvent() error = %v", err)
	}
	if fe.Labels == nil {
		t.Error("Labels should be initialized, got nil")
	}
	if fe.Annotations == nil {
		t.Error("Annotations should be initialized, got nil")
	}
}

func TestNewFaultEvent_UniqueIDs(t *testing.T) {
	ref := ResourceRef{Kind: "Pod", Namespace: "ns", Name: "p", UID: "u"}
	ids := make(map[string]bool)

	for i := 0; i < 100; i++ {
		fe, err := NewFaultEvent("PodFailed", SeverityWarning, ref, "failed", nil, nil)
		if err != nil {
			t.Fatalf("NewFaultEvent() error = %v", err)
		}
		if ids[fe.ID] {
			t.Fatalf("duplicate ID generated: %s", fe.ID)
		}
		ids[fe.ID] = true
	}
}

func TestNewFaultEvent_ValidationErrors(t *testing.T) {
	validRef := ResourceRef{Kind: "Pod", Namespace: "default", Name: "test-pod", UID: "uid-123"}

	tests := []struct {
		name         string
		detectorName string
		severity     Severity
		resource     ResourceRef
		wantErr      string
	}{
		{
			name:         "empty detector name",
			detectorName: "",
			severity:     SeverityCritical,
			resource:     validRef,
			wantErr:      "detectorName must not be empty",
		},
		{
			name:         "invalid severity",
			detectorName: "PodFailed",
			severity:     "bogus",
			resource:     validRef,
			wantErr:      "invalid severity",
		},
		{
			name:         "empty severity",
			detectorName: "PodFailed",
			severity:     "",
			resource:     validRef,
			wantErr:      "invalid severity",
		},
		{
			name:         "empty resource kind",
			detectorName: "PodFailed",
			severity:     SeverityCritical,
			resource:     ResourceRef{Kind: "", Namespace: "ns", Name: "p", UID: "u"},
			wantErr:      "resource kind must not be empty",
		},
		{
			name:         "empty resource name",
			detectorName: "PodFailed",
			severity:     SeverityCritical,
			resource:     ResourceRef{Kind: "Pod", Namespace: "ns", Name: "", UID: "u"},
			wantErr:      "resource name must not be empty",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fe, err := NewFaultEvent(tt.detectorName, tt.severity, tt.resource, "desc", nil, nil)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if fe != nil {
				t.Error("expected nil FaultEvent on error")
			}
			if !containsSubstring(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestNewSuperEvent_Success(t *testing.T) {
	ref := ResourceRef{Kind: "Node", Name: "node-1", UID: "node-uid"}
	events := []FaultEvent{
		{ID: "e1", DetectorName: "PodFailed", Severity: SeverityWarning, Resource: ResourceRef{Kind: "Pod", Namespace: "ns", Name: "p1"}},
		{ID: "e2", DetectorName: "PodFailed", Severity: SeverityCritical, Resource: ResourceRef{Kind: "Pod", Namespace: "ns", Name: "p2"}},
		{ID: "e3", DetectorName: "PodFailed", Severity: SeverityInfo, Resource: ResourceRef{Kind: "Pod", Namespace: "ns", Name: "p3"}},
	}

	before := time.Now().UTC()
	se, err := NewSuperEvent("NodeCascade", ref, events)
	after := time.Now().UTC()

	if err != nil {
		t.Fatalf("NewSuperEvent() error = %v", err)
	}
	if !uuidRegex.MatchString(se.ID) {
		t.Errorf("ID %q does not match UUID v4 format", se.ID)
	}
	if se.CorrelationRule != "NodeCascade" {
		t.Errorf("CorrelationRule = %q, want %q", se.CorrelationRule, "NodeCascade")
	}
	if se.PrimaryResource != ref {
		t.Errorf("PrimaryResource = %+v, want %+v", se.PrimaryResource, ref)
	}
	if len(se.FaultEvents) != 3 {
		t.Errorf("FaultEvents length = %d, want 3", len(se.FaultEvents))
	}
	if se.Severity != SeverityCritical {
		t.Errorf("Severity = %q, want %q (highest among events)", se.Severity, SeverityCritical)
	}
	if se.Timestamp.Before(before) || se.Timestamp.After(after) {
		t.Errorf("Timestamp %v not in range [%v, %v]", se.Timestamp, before, after)
	}
}

func TestNewSuperEvent_SeverityPromotion(t *testing.T) {
	ref := ResourceRef{Kind: "Deployment", Name: "deploy-1", Namespace: "ns", UID: "d-uid"}
	tests := []struct {
		name       string
		severities []Severity
		want       Severity
	}{
		{
			name:       "all info",
			severities: []Severity{SeverityInfo, SeverityInfo},
			want:       SeverityInfo,
		},
		{
			name:       "info and warning",
			severities: []Severity{SeverityInfo, SeverityWarning},
			want:       SeverityWarning,
		},
		{
			name:       "warning and critical",
			severities: []Severity{SeverityWarning, SeverityCritical},
			want:       SeverityCritical,
		},
		{
			name:       "single critical",
			severities: []Severity{SeverityCritical},
			want:       SeverityCritical,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			events := make([]FaultEvent, len(tt.severities))
			for i, s := range tt.severities {
				events[i] = FaultEvent{ID: "e", DetectorName: "D", Severity: s, Resource: ref}
			}
			se, err := NewSuperEvent("TestRule", ref, events)
			if err != nil {
				t.Fatalf("NewSuperEvent() error = %v", err)
			}
			if se.Severity != tt.want {
				t.Errorf("Severity = %q, want %q", se.Severity, tt.want)
			}
		})
	}
}

func TestNewSuperEvent_ValidationErrors(t *testing.T) {
	validRef := ResourceRef{Kind: "Node", Name: "node-1", UID: "uid"}
	validEvents := []FaultEvent{{ID: "e1", DetectorName: "D", Severity: SeverityWarning, Resource: validRef}}

	tests := []struct {
		name    string
		rule    string
		primary ResourceRef
		events  []FaultEvent
		wantErr string
	}{
		{
			name:    "empty correlation rule",
			rule:    "",
			primary: validRef,
			events:  validEvents,
			wantErr: "correlationRule must not be empty",
		},
		{
			name:    "empty primary resource kind",
			rule:    "NodeCascade",
			primary: ResourceRef{Kind: "", Name: "n"},
			events:  validEvents,
			wantErr: "primary resource kind must not be empty",
		},
		{
			name:    "empty primary resource name",
			rule:    "NodeCascade",
			primary: ResourceRef{Kind: "Node", Name: ""},
			events:  validEvents,
			wantErr: "primary resource name must not be empty",
		},
		{
			name:    "empty fault events",
			rule:    "NodeCascade",
			primary: validRef,
			events:  []FaultEvent{},
			wantErr: "faultEvents must not be empty",
		},
		{
			name:    "nil fault events",
			rule:    "NodeCascade",
			primary: validRef,
			events:  nil,
			wantErr: "faultEvents must not be empty",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			se, err := NewSuperEvent(tt.rule, tt.primary, tt.events)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if se != nil {
				t.Error("expected nil SuperEvent on error")
			}
			if !containsSubstring(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestNewDiagnosticBundle_FaultEvent(t *testing.T) {
	fe := &FaultEvent{ID: "fe-1", DetectorName: "PodFailed", Severity: SeverityCritical}
	sections := []DiagnosticSection{
		{GathererName: "PodEvents", Title: "Pod Events", Content: "event data", Format: "text"},
	}

	before := time.Now().UTC()
	db := NewDiagnosticBundle(fe, sections)
	after := time.Now().UTC()

	if db.FaultEvent != fe {
		t.Error("FaultEvent pointer mismatch")
	}
	if db.SuperEvent != nil {
		t.Error("SuperEvent should be nil for FaultEvent bundle")
	}
	if len(db.Sections) != 1 {
		t.Errorf("Sections length = %d, want 1", len(db.Sections))
	}
	if db.Timestamp.Before(before) || db.Timestamp.After(after) {
		t.Errorf("Timestamp %v not in expected range", db.Timestamp)
	}
}

func TestNewDiagnosticBundle_NilSections(t *testing.T) {
	fe := &FaultEvent{ID: "fe-1"}
	db := NewDiagnosticBundle(fe, nil)
	if db.Sections == nil {
		t.Error("Sections should be initialized to empty slice, got nil")
	}
	if len(db.Sections) != 0 {
		t.Errorf("Sections length = %d, want 0", len(db.Sections))
	}
}

func TestNewSuperEventDiagnosticBundle(t *testing.T) {
	se := &SuperEvent{ID: "se-1", CorrelationRule: "NodeCascade", Severity: SeverityCritical}
	sections := []DiagnosticSection{
		{GathererName: "NodeConditions", Title: "Node Conditions", Content: "node data", Format: "json"},
		{GathererName: "PodEvents", Title: "Pod Events", Content: "events", Format: "text"},
	}

	db := NewSuperEventDiagnosticBundle(se, sections)

	if db.SuperEvent != se {
		t.Error("SuperEvent pointer mismatch")
	}
	if db.FaultEvent != nil {
		t.Error("FaultEvent should be nil for SuperEvent bundle")
	}
	if len(db.Sections) != 2 {
		t.Errorf("Sections length = %d, want 2", len(db.Sections))
	}
}

func TestNewSuperEventDiagnosticBundle_NilSections(t *testing.T) {
	se := &SuperEvent{ID: "se-1"}
	db := NewSuperEventDiagnosticBundle(se, nil)
	if db.Sections == nil {
		t.Error("Sections should be initialized to empty slice, got nil")
	}
}

func TestTokenUsage_Total(t *testing.T) {
	tests := []struct {
		name   string
		usage  TokenUsage
		want   int
	}{
		{"zero values", TokenUsage{}, 0},
		{"input only", TokenUsage{Input: 100}, 100},
		{"output only", TokenUsage{Output: 50}, 50},
		{"both", TokenUsage{Input: 1500, Output: 500}, 2000},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.usage.Total()
			if got != tt.want {
				t.Errorf("TokenUsage.Total() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestGenerateUUID_Format(t *testing.T) {
	for i := 0; i < 50; i++ {
		id, err := generateUUID()
		if err != nil {
			t.Fatalf("generateUUID() error = %v", err)
		}
		if !uuidRegex.MatchString(id) {
			t.Errorf("generateUUID() = %q, does not match UUID v4 format", id)
		}
	}
}

func TestGenerateUUID_Uniqueness(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 1000; i++ {
		id, err := generateUUID()
		if err != nil {
			t.Fatalf("generateUUID() error = %v", err)
		}
		if seen[id] {
			t.Fatalf("duplicate UUID generated: %s", id)
		}
		seen[id] = true
	}
}

func TestHighestSeverity(t *testing.T) {
	tests := []struct {
		name   string
		events []FaultEvent
		want   Severity
	}{
		{
			name:   "empty returns info",
			events: nil,
			want:   SeverityInfo,
		},
		{
			name:   "single warning",
			events: []FaultEvent{{Severity: SeverityWarning}},
			want:   SeverityWarning,
		},
		{
			name:   "mixed severities",
			events: []FaultEvent{{Severity: SeverityInfo}, {Severity: SeverityCritical}, {Severity: SeverityWarning}},
			want:   SeverityCritical,
		},
		{
			name:   "all same",
			events: []FaultEvent{{Severity: SeverityWarning}, {Severity: SeverityWarning}},
			want:   SeverityWarning,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := highestSeverity(tt.events)
			if got != tt.want {
				t.Errorf("highestSeverity() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSeverityRank(t *testing.T) {
	if severityRank(SeverityCritical) <= severityRank(SeverityWarning) {
		t.Error("critical should rank higher than warning")
	}
	if severityRank(SeverityWarning) <= severityRank(SeverityInfo) {
		t.Error("warning should rank higher than info")
	}
	if severityRank(SeverityInfo) <= severityRank("") {
		t.Error("info should rank higher than empty")
	}
}

func TestValidCategories(t *testing.T) {
	expected := []string{
		"scheduling", "storage", "application", "networking",
		"resources", "node", "configuration", "unknown",
	}
	for _, c := range expected {
		if !ValidCategories[c] {
			t.Errorf("category %q should be valid", c)
		}
	}
	if ValidCategories["bogus"] {
		t.Error("bogus category should not be valid")
	}
	if ValidCategories[""] {
		t.Error("empty category should not be valid")
	}
}

func TestRCAReport_StructLayout(t *testing.T) {
	// Verify that RCAReport can be constructed with all fields.
	// This is a compile-time check wrapped in a test for documentation.
	report := RCAReport{
		FaultEventID: "fe-1",
		Timestamp:    time.Now().UTC(),
		RootCause:    "OOMKilled due to insufficient memory limits",
		Severity:     SeverityCritical,
		Category:     "resources",
		Systemic:     true,
		BlastRadius:  "All pods in deployment web-api",
		Remediation:  []string{"Increase memory limits to 512Mi", "Add HPA"},
		RelatedResources: []ResourceRef{
			{Kind: "Deployment", Namespace: "default", Name: "web-api"},
		},
		Confidence:  0.95,
		RawAnalysis: `{"rootCause":"..."}`,
		DiagnosticBundle: DiagnosticBundle{
			FaultEvent: &FaultEvent{ID: "fe-1"},
			Sections:   []DiagnosticSection{},
		},
		AnalyzerBackend: "claude",
		TokensUsed:      TokenUsage{Input: 1500, Output: 500},
	}

	if report.FaultEventID != "fe-1" {
		t.Errorf("FaultEventID = %q, want %q", report.FaultEventID, "fe-1")
	}
	if report.Severity != SeverityCritical {
		t.Errorf("Severity = %q, want %q", report.Severity, SeverityCritical)
	}
	if !report.Systemic {
		t.Error("Systemic should be true")
	}
	if report.Confidence != 0.95 {
		t.Errorf("Confidence = %f, want 0.95", report.Confidence)
	}
	if report.TokensUsed.Total() != 2000 {
		t.Errorf("TokensUsed.Total() = %d, want 2000", report.TokensUsed.Total())
	}
	if len(report.Remediation) != 2 {
		t.Errorf("Remediation length = %d, want 2", len(report.Remediation))
	}
	if len(report.RelatedResources) != 1 {
		t.Errorf("RelatedResources length = %d, want 1", len(report.RelatedResources))
	}
}

func TestDiagnosticSection_ErrorField(t *testing.T) {
	// Sections with errors should still be part of the bundle.
	section := DiagnosticSection{
		GathererName: "PodLogs",
		Title:        "Container Logs",
		Content:      "",
		Format:       "text",
		Error:        "container not found: istio-proxy",
	}

	if section.Error == "" {
		t.Error("Error field should be set")
	}
	if section.GathererName != "PodLogs" {
		t.Errorf("GathererName = %q, want %q", section.GathererName, "PodLogs")
	}
}

// containsSubstring checks if s contains substr.
func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && searchSubstring(s, substr)))
}

func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
