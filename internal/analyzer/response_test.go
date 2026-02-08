package analyzer

import (
	"testing"
	"time"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

func responseTestBundle() model.DiagnosticBundle {
	return model.DiagnosticBundle{
		FaultEvent: &model.FaultEvent{
			ID:           "fe-resp-1",
			DetectorName: "PodCrashLoop",
			Severity:     model.SeverityCritical,
			Timestamp:    time.Now().UTC(),
			Resource: model.ResourceRef{
				Kind:      "Pod",
				Namespace: "default",
				Name:      "test-pod",
			},
		},
		Timestamp: time.Now().UTC(),
		Sections:  []model.DiagnosticSection{},
	}
}

func TestParseLLMResponse_ValidJSON(t *testing.T) {
	raw := `{
		"rootCause": "OOMKilled due to insufficient memory limits",
		"severity": "critical",
		"category": "resources",
		"systemic": true,
		"blastRadius": "All pods in deployment web-api",
		"remediation": ["Increase memory limits to 512Mi", "Add HPA"],
		"relatedResources": [
			{"kind": "Deployment", "namespace": "default", "name": "web-api"}
		],
		"confidence": 0.95
	}`

	bundle := responseTestBundle()
	tokens := model.TokenUsage{Input: 1500, Output: 500}

	report := ParseLLMResponse(raw, bundle, "claude", tokens)

	if report.RootCause != "OOMKilled due to insufficient memory limits" {
		t.Errorf("RootCause = %q", report.RootCause)
	}
	if report.Severity != model.SeverityCritical {
		t.Errorf("Severity = %q, want critical", report.Severity)
	}
	if report.Category != "resources" {
		t.Errorf("Category = %q, want resources", report.Category)
	}
	if !report.Systemic {
		t.Error("Systemic should be true")
	}
	if report.BlastRadius != "All pods in deployment web-api" {
		t.Errorf("BlastRadius = %q", report.BlastRadius)
	}
	if len(report.Remediation) != 2 {
		t.Errorf("Remediation length = %d, want 2", len(report.Remediation))
	}
	if len(report.RelatedResources) != 1 {
		t.Errorf("RelatedResources length = %d, want 1", len(report.RelatedResources))
	}
	if report.RelatedResources[0].Kind != "Deployment" {
		t.Errorf("RelatedResources[0].Kind = %q", report.RelatedResources[0].Kind)
	}
	if report.Confidence != 0.95 {
		t.Errorf("Confidence = %f, want 0.95", report.Confidence)
	}
	if report.FaultEventID != "fe-resp-1" {
		t.Errorf("FaultEventID = %q, want fe-resp-1", report.FaultEventID)
	}
	if report.AnalyzerBackend != "claude" {
		t.Errorf("AnalyzerBackend = %q, want claude", report.AnalyzerBackend)
	}
	if report.TokensUsed.Total() != 2000 {
		t.Errorf("TokensUsed.Total() = %d, want 2000", report.TokensUsed.Total())
	}
	if report.RawAnalysis == "" {
		t.Error("RawAnalysis should contain the original response")
	}
}

func TestParseLLMResponse_JSONInCodeFence(t *testing.T) {
	raw := "Here is my analysis:\n\n```json\n" + `{
		"rootCause": "Image pull error for tag v2.1.0",
		"severity": "warning",
		"category": "application",
		"systemic": false,
		"blastRadius": "Single pod affected",
		"remediation": ["Fix the image tag"],
		"relatedResources": [],
		"confidence": 0.85
	}` + "\n```\n"

	bundle := responseTestBundle()
	report := ParseLLMResponse(raw, bundle, "openai", model.TokenUsage{})

	if report.RootCause != "Image pull error for tag v2.1.0" {
		t.Errorf("RootCause = %q", report.RootCause)
	}
	if report.Severity != model.SeverityWarning {
		t.Errorf("Severity = %q, want warning", report.Severity)
	}
	if report.Category != "application" {
		t.Errorf("Category = %q, want application", report.Category)
	}
	if report.Confidence != 0.85 {
		t.Errorf("Confidence = %f, want 0.85", report.Confidence)
	}
}

func TestParseLLMResponse_PlainCodeFence(t *testing.T) {
	raw := "```\n" + `{
		"rootCause": "Node disk pressure",
		"severity": "critical",
		"category": "node",
		"systemic": true,
		"blastRadius": "All pods on node-1",
		"remediation": ["Expand disk"],
		"relatedResources": [],
		"confidence": 0.9
	}` + "\n```"

	bundle := responseTestBundle()
	report := ParseLLMResponse(raw, bundle, "claude", model.TokenUsage{})

	if report.RootCause != "Node disk pressure" {
		t.Errorf("RootCause = %q", report.RootCause)
	}
}

func TestParseLLMResponse_FallbackOnInvalidJSON(t *testing.T) {
	raw := "This is not JSON at all, just a text response."

	bundle := responseTestBundle()
	report := ParseLLMResponse(raw, bundle, "claude", model.TokenUsage{Input: 100, Output: 50})

	if report.RootCause != "Automated analysis failed — raw diagnostics attached" {
		t.Errorf("RootCause = %q", report.RootCause)
	}
	if report.Severity != model.SeverityCritical {
		t.Errorf("Severity = %q, want %q (preserved from event)", report.Severity, model.SeverityCritical)
	}
	if report.Category != "unknown" {
		t.Errorf("Category = %q, want unknown", report.Category)
	}
	if report.Confidence != 0.0 {
		t.Errorf("Confidence = %f, want 0.0", report.Confidence)
	}
	if report.RawAnalysis != raw {
		t.Error("RawAnalysis should contain the original response")
	}
	if report.AnalyzerBackend != "claude" {
		t.Errorf("AnalyzerBackend = %q, want claude", report.AnalyzerBackend)
	}
	if report.TokensUsed.Total() != 150 {
		t.Errorf("TokensUsed.Total() = %d, want 150", report.TokensUsed.Total())
	}
}

func TestParseLLMResponse_FallbackOnEmptyResponse(t *testing.T) {
	bundle := responseTestBundle()
	report := ParseLLMResponse("", bundle, "claude", model.TokenUsage{})

	if report.RootCause != "Automated analysis failed — raw diagnostics attached" {
		t.Errorf("RootCause = %q", report.RootCause)
	}
	if report.Confidence != 0.0 {
		t.Errorf("Confidence = %f, want 0.0", report.Confidence)
	}
}

func TestParseLLMResponse_InvalidSeverityFallsBackToOriginal(t *testing.T) {
	raw := `{
		"rootCause": "App error",
		"severity": "BOGUS",
		"category": "application",
		"systemic": false,
		"blastRadius": "",
		"remediation": [],
		"relatedResources": [],
		"confidence": 0.5
	}`

	bundle := responseTestBundle()
	report := ParseLLMResponse(raw, bundle, "claude", model.TokenUsage{})

	// Should fall back to the original event severity.
	if report.Severity != model.SeverityCritical {
		t.Errorf("Severity = %q, want %q (preserved from event)", report.Severity, model.SeverityCritical)
	}
}

func TestParseLLMResponse_InvalidCategoryDefaultsToUnknown(t *testing.T) {
	raw := `{
		"rootCause": "Something happened",
		"severity": "warning",
		"category": "bogus_category",
		"systemic": false,
		"blastRadius": "",
		"remediation": [],
		"relatedResources": [],
		"confidence": 0.3
	}`

	bundle := responseTestBundle()
	report := ParseLLMResponse(raw, bundle, "claude", model.TokenUsage{})

	if report.Category != "unknown" {
		t.Errorf("Category = %q, want unknown", report.Category)
	}
}

func TestParseLLMResponse_ConfidenceClamped(t *testing.T) {
	tests := []struct {
		name       string
		confidence string
		want       float64
	}{
		{"negative", "-0.5", 0.0},
		{"over one", "1.5", 1.0},
		{"normal", "0.7", 0.7},
		{"zero", "0.0", 0.0},
		{"one", "1.0", 1.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := `{
				"rootCause": "test",
				"severity": "info",
				"category": "unknown",
				"systemic": false,
				"blastRadius": "",
				"remediation": [],
				"relatedResources": [],
				"confidence": ` + tt.confidence + `
			}`
			bundle := responseTestBundle()
			report := ParseLLMResponse(raw, bundle, "claude", model.TokenUsage{})

			if report.Confidence != tt.want {
				t.Errorf("Confidence = %f, want %f", report.Confidence, tt.want)
			}
		})
	}
}

func TestParseLLMResponse_MissingRootCauseFallback(t *testing.T) {
	raw := `{
		"rootCause": "",
		"severity": "warning",
		"category": "unknown",
		"systemic": false,
		"blastRadius": "",
		"remediation": [],
		"relatedResources": [],
		"confidence": 0.5
	}`

	bundle := responseTestBundle()
	report := ParseLLMResponse(raw, bundle, "claude", model.TokenUsage{})

	// Empty rootCause should trigger fallback.
	if report.RootCause != "Automated analysis failed — raw diagnostics attached" {
		t.Errorf("RootCause = %q, expected fallback message", report.RootCause)
	}
}

func TestParseLLMResponse_SuperEventBundle(t *testing.T) {
	raw := `{
		"rootCause": "Node went NotReady causing cascade",
		"severity": "critical",
		"category": "node",
		"systemic": true,
		"blastRadius": "All pods on node-1",
		"remediation": ["Check node health"],
		"relatedResources": [{"kind": "Node", "namespace": "", "name": "node-1"}],
		"confidence": 0.9
	}`

	bundle := model.DiagnosticBundle{
		SuperEvent: &model.SuperEvent{
			ID:              "se-789",
			CorrelationRule: "NodeCascade",
			Severity:        model.SeverityCritical,
		},
		Timestamp: time.Now().UTC(),
		Sections:  []model.DiagnosticSection{},
	}

	report := ParseLLMResponse(raw, bundle, "claude-bedrock", model.TokenUsage{Input: 2000, Output: 800})

	if report.FaultEventID != "se-789" {
		t.Errorf("FaultEventID = %q, want se-789", report.FaultEventID)
	}
	if report.AnalyzerBackend != "claude-bedrock" {
		t.Errorf("AnalyzerBackend = %q, want claude-bedrock", report.AnalyzerBackend)
	}
}

func TestParseLLMResponse_NilRemediationInitialized(t *testing.T) {
	raw := `{
		"rootCause": "test cause",
		"severity": "info",
		"category": "unknown",
		"systemic": false,
		"blastRadius": "",
		"confidence": 0.5
	}`

	bundle := responseTestBundle()
	report := ParseLLMResponse(raw, bundle, "claude", model.TokenUsage{})

	if report.Remediation == nil {
		t.Error("Remediation should be initialized to empty slice, not nil")
	}
}

func TestParseLLMResponse_SkipsInvalidRelatedResources(t *testing.T) {
	raw := `{
		"rootCause": "test",
		"severity": "info",
		"category": "unknown",
		"systemic": false,
		"blastRadius": "",
		"remediation": [],
		"relatedResources": [
			{"kind": "Pod", "namespace": "ns", "name": "valid-pod"},
			{"kind": "", "namespace": "ns", "name": "missing-kind"},
			{"kind": "Pod", "namespace": "ns", "name": ""},
			{"kind": "Node", "namespace": "", "name": "node-1"}
		],
		"confidence": 0.5
	}`

	bundle := responseTestBundle()
	report := ParseLLMResponse(raw, bundle, "claude", model.TokenUsage{})

	if len(report.RelatedResources) != 2 {
		t.Errorf("RelatedResources length = %d, want 2 (skipping invalid entries)", len(report.RelatedResources))
	}
}

func TestParseLLMResponse_WhitespaceAroundJSON(t *testing.T) {
	raw := `
	{
		"rootCause": "test",
		"severity": "info",
		"category": "unknown",
		"systemic": false,
		"blastRadius": "",
		"remediation": [],
		"relatedResources": [],
		"confidence": 0.5
	}
	  `

	bundle := responseTestBundle()
	report := ParseLLMResponse(raw, bundle, "claude", model.TokenUsage{})

	if report.RootCause != "test" {
		t.Errorf("RootCause = %q, want test", report.RootCause)
	}
}

func TestExtractJSONFromCodeFence(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "json fence",
			input: "text\n```json\n{\"key\": \"value\"}\n```\nmore text",
			want:  `{"key": "value"}`,
		},
		{
			name:  "plain fence",
			input: "```\n{\"key\": \"value\"}\n```",
			want:  `{"key": "value"}`,
		},
		{
			name:  "no fence",
			input: `{"key": "value"}`,
			want:  "",
		},
		{
			name:  "empty fence",
			input: "```json\n\n```",
			want:  "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractJSONFromCodeFence(tt.input)
			if got != tt.want {
				t.Errorf("extractJSONFromCodeFence() = %q, want %q", got, tt.want)
			}
		})
	}
}
