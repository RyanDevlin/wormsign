package sink

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

const logSinkName = "log"

// LogSink outputs RCA reports as structured JSON log lines. It is always
// enabled and non-optional, ensuring every report is captured even if all
// external sinks are down.
type LogSink struct {
	logger *slog.Logger
}

// NewLogSink creates a new LogSink. The logger must not be nil.
func NewLogSink(logger *slog.Logger) (*LogSink, error) {
	if logger == nil {
		return nil, errNilLogger
	}
	return &LogSink{
		logger: logger,
	}, nil
}

// Name returns "log".
func (s *LogSink) Name() string {
	return logSinkName
}

// Deliver writes the RCA report as a structured JSON log entry.
// The log sink never retries because it writes to local stdout,
// which is not expected to fail transiently.
func (s *LogSink) Deliver(_ context.Context, report *model.RCAReport) error {
	if report == nil {
		return errNilReport
	}

	// Build a structured representation for the log line. We marshal
	// the remediation and related resources to JSON for readability
	// in structured log output.
	remediationJSON, _ := json.Marshal(report.Remediation)
	relatedJSON, _ := json.Marshal(formatRelatedResources(report.RelatedResources))

	s.logger.Info("rca_report",
		"fault_event_id", report.FaultEventID,
		"root_cause", report.RootCause,
		"severity", string(report.Severity),
		"category", report.Category,
		"systemic", report.Systemic,
		"blast_radius", report.BlastRadius,
		"remediation", string(remediationJSON),
		"related_resources", string(relatedJSON),
		"confidence", report.Confidence,
		"analyzer_backend", report.AnalyzerBackend,
		"tokens_input", report.TokensUsed.Input,
		"tokens_output", report.TokensUsed.Output,
	)
	return nil
}

// SeverityFilter returns nil — the log sink accepts all severities.
func (s *LogSink) SeverityFilter() []model.Severity {
	return nil
}

// formatRelatedResources converts ResourceRef slices to a loggable format.
func formatRelatedResources(refs []model.ResourceRef) []string {
	result := make([]string, len(refs))
	for i, r := range refs {
		result[i] = r.String()
	}
	return result
}
