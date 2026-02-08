// Package analyzer defines the Analyzer interface and provides LLM-backed
// implementations for producing Root Cause Analysis reports from diagnostic
// bundles. See Section 5.3 of the project spec.
package analyzer

import (
	"context"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

// Analyzer processes a DiagnosticBundle and produces an RCAReport.
type Analyzer interface {
	// Name returns the analyzer backend identifier (e.g., "claude", "noop").
	Name() string

	// Analyze processes the diagnostic bundle and returns an RCA report.
	Analyze(ctx context.Context, bundle model.DiagnosticBundle) (*model.RCAReport, error)

	// Healthy reports whether the analyzer backend is reachable and operational.
	Healthy(ctx context.Context) bool
}
