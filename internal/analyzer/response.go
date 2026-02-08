// Package analyzer — response.go implements LLM response validation and
// parsing. It handles JSON extraction, markdown fence stripping, schema
// validation, and fallback report generation. See Section 5.3.5 of the
// project spec.
package analyzer

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

// llmResponse is the JSON schema expected from the LLM response, matching
// the output format defined in the system prompt.
type llmResponse struct {
	RootCause        string              `json:"rootCause"`
	Severity         string              `json:"severity"`
	Category         string              `json:"category"`
	Systemic         bool                `json:"systemic"`
	BlastRadius      string              `json:"blastRadius"`
	Remediation      []string            `json:"remediation"`
	RelatedResources []llmResourceRef    `json:"relatedResources"`
	Confidence       float64             `json:"confidence"`
}

// llmResourceRef is a resource reference in the LLM response.
type llmResourceRef struct {
	Kind      string `json:"kind"`
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// codeFenceRegex matches JSON content within markdown code fences.
// It handles ```json, ``` (plain), and variations with whitespace.
var codeFenceRegex = regexp.MustCompile("(?s)```(?:json)?\\s*\n?(\\{.*?\\})\\s*\n?```")

// ParseLLMResponse parses the raw LLM response text and converts it into an
// RCAReport. It follows this strategy:
//  1. Try to parse the response as JSON directly.
//  2. If that fails, try to extract JSON from markdown code fences.
//  3. If that also fails, return a fallback report with the raw response
//     attached for debugging.
//
// The bundle, backend name, and token usage are attached to the resulting
// report regardless of parse success.
func ParseLLMResponse(rawResponse string, bundle model.DiagnosticBundle, backend string, tokens model.TokenUsage) *model.RCAReport {
	trimmed := strings.TrimSpace(rawResponse)

	// Attempt 1: Parse as JSON directly.
	if report, err := parseLLMJSON(trimmed, bundle, backend, tokens); err == nil {
		report.RawAnalysis = rawResponse
		return report
	}

	// Attempt 2: Extract JSON from markdown code fences.
	extracted := extractJSONFromCodeFence(trimmed)
	if extracted != "" {
		if report, err := parseLLMJSON(extracted, bundle, backend, tokens); err == nil {
			report.RawAnalysis = rawResponse
			return report
		}
	}

	// Attempt 3: Return fallback report.
	return buildFallbackReport(rawResponse, bundle, backend, tokens)
}

// parseLLMJSON attempts to parse JSON text into an RCAReport.
func parseLLMJSON(jsonText string, bundle model.DiagnosticBundle, backend string, tokens model.TokenUsage) (*model.RCAReport, error) {
	var resp llmResponse
	if err := json.Unmarshal([]byte(jsonText), &resp); err != nil {
		return nil, fmt.Errorf("json unmarshal: %w", err)
	}

	// Validate required fields.
	if resp.RootCause == "" {
		return nil, fmt.Errorf("missing required field: rootCause")
	}

	// Validate severity.
	severity := model.Severity(resp.Severity)
	if !severity.IsValid() {
		severity = originalSeverity(bundle)
	}

	// Validate category.
	category := resp.Category
	if !model.ValidCategories[category] {
		category = "unknown"
	}

	// Clamp confidence to [0.0, 1.0].
	confidence := resp.Confidence
	if confidence < 0.0 {
		confidence = 0.0
	}
	if confidence > 1.0 {
		confidence = 1.0
	}

	// Convert related resources.
	related := make([]model.ResourceRef, 0, len(resp.RelatedResources))
	for _, rr := range resp.RelatedResources {
		if rr.Kind != "" && rr.Name != "" {
			related = append(related, model.ResourceRef{
				Kind:      rr.Kind,
				Namespace: rr.Namespace,
				Name:      rr.Name,
			})
		}
	}

	// Ensure remediation is never nil.
	remediation := resp.Remediation
	if remediation == nil {
		remediation = []string{}
	}

	return &model.RCAReport{
		FaultEventID:     faultEventID(bundle),
		Timestamp:        time.Now().UTC(),
		RootCause:        resp.RootCause,
		Severity:         severity,
		Category:         category,
		Systemic:         resp.Systemic,
		BlastRadius:      resp.BlastRadius,
		Remediation:      remediation,
		RelatedResources: related,
		Confidence:       confidence,
		DiagnosticBundle: bundle,
		AnalyzerBackend:  backend,
		TokensUsed:       tokens,
	}, nil
}

// extractJSONFromCodeFence attempts to extract a JSON object from markdown
// code fences (```json ... ``` or ``` ... ```).
func extractJSONFromCodeFence(text string) string {
	matches := codeFenceRegex.FindStringSubmatch(text)
	if len(matches) >= 2 {
		return strings.TrimSpace(matches[1])
	}
	return ""
}

// buildFallbackReport creates a fallback RCA report when the LLM response
// cannot be parsed. It preserves the original severity and attaches the raw
// response for debugging.
func buildFallbackReport(rawResponse string, bundle model.DiagnosticBundle, backend string, tokens model.TokenUsage) *model.RCAReport {
	return &model.RCAReport{
		FaultEventID:     faultEventID(bundle),
		Timestamp:        time.Now().UTC(),
		RootCause:        "Automated analysis failed — raw diagnostics attached",
		Severity:         originalSeverity(bundle),
		Category:         "unknown",
		Systemic:         false,
		BlastRadius:      "",
		Remediation:      []string{},
		RelatedResources: []model.ResourceRef{},
		Confidence:       0.0,
		RawAnalysis:      rawResponse,
		DiagnosticBundle: bundle,
		AnalyzerBackend:  backend,
		TokensUsed:       tokens,
	}
}

// faultEventID extracts the fault event or super-event ID from the bundle.
func faultEventID(bundle model.DiagnosticBundle) string {
	if bundle.SuperEvent != nil {
		return bundle.SuperEvent.ID
	}
	if bundle.FaultEvent != nil {
		return bundle.FaultEvent.ID
	}
	return ""
}

// originalSeverity extracts the severity from the bundle's source event.
func originalSeverity(bundle model.DiagnosticBundle) model.Severity {
	if bundle.SuperEvent != nil {
		return bundle.SuperEvent.Severity
	}
	if bundle.FaultEvent != nil {
		return bundle.FaultEvent.Severity
	}
	return model.SeverityWarning
}
