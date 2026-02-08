// Package analyzer — noop.go implements the noop analyzer backend that passes
// DiagnosticBundles through without LLM analysis. See Section 5.3.2 of the
// project spec.
package analyzer

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

// NoopAnalyzer passes diagnostic bundles through without performing LLM
// analysis. It produces a minimal RCA report containing the raw diagnostics.
// This is useful for environments that only want diagnostic data forwarded to
// sinks, or as a fallback when the LLM backend is unavailable.
type NoopAnalyzer struct {
	logger *slog.Logger
}

// NewNoopAnalyzer creates a new noop analyzer.
func NewNoopAnalyzer(logger *slog.Logger) (*NoopAnalyzer, error) {
	if logger == nil {
		return nil, fmt.Errorf("noop: logger must not be nil")
	}
	return &NoopAnalyzer{logger: logger}, nil
}

// Name returns the analyzer backend identifier.
func (n *NoopAnalyzer) Name() string {
	return "noop"
}

// Analyze creates a passthrough RCA report from the diagnostic bundle without
// invoking an LLM. The report preserves the original severity and attaches the
// full diagnostic bundle for downstream sinks.
func (n *NoopAnalyzer) Analyze(ctx context.Context, bundle model.DiagnosticBundle) (*model.RCAReport, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	var faultEventID string
	var severity model.Severity

	if bundle.SuperEvent != nil {
		faultEventID = bundle.SuperEvent.ID
		severity = bundle.SuperEvent.Severity
	} else if bundle.FaultEvent != nil {
		faultEventID = bundle.FaultEvent.ID
		severity = bundle.FaultEvent.Severity
	} else {
		return nil, fmt.Errorf("noop: bundle has neither FaultEvent nor SuperEvent")
	}

	n.logger.Info("noop analyzer: passing through diagnostic bundle",
		"fault_event_id", faultEventID,
		"severity", severity,
	)

	return &model.RCAReport{
		FaultEventID:     faultEventID,
		Timestamp:        time.Now().UTC(),
		RootCause:        "Automated analysis not performed — raw diagnostics attached",
		Severity:         severity,
		Category:         "unknown",
		Systemic:         false,
		BlastRadius:      "",
		Remediation:      []string{},
		RelatedResources: []model.ResourceRef{},
		Confidence:       0.0,
		RawAnalysis:      "",
		DiagnosticBundle: bundle,
		AnalyzerBackend:  "noop",
		TokensUsed:       model.TokenUsage{},
	}, nil
}

// Healthy always returns true since the noop analyzer has no backend dependency.
func (n *NoopAnalyzer) Healthy(ctx context.Context) bool {
	return true
}
