package sink

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

const (
	pagerDutySinkName = "pagerduty"
	pagerDutyEventsV2 = "https://events.pagerduty.com/v2/enqueue"
)

// PagerDutyConfig holds configuration for the PagerDuty sink.
type PagerDutyConfig struct {
	// RoutingKey is the PagerDuty Events API v2 routing key.
	RoutingKey string
	// SeverityFilter restricts which severities are delivered.
	SeverityFilter []model.Severity
}

// PagerDutySink creates PagerDuty incidents via the Events API v2.
type PagerDutySink struct {
	client         *http.Client
	routingKey     string
	severityFilter []model.Severity
	logger         *slog.Logger
	retryCfg       retryConfig
	eventsURL      string
}

// NewPagerDutySink creates a new PagerDuty sink.
func NewPagerDutySink(cfg PagerDutyConfig, logger *slog.Logger) (*PagerDutySink, error) {
	if logger == nil {
		return nil, errNilLogger
	}
	if cfg.RoutingKey == "" {
		return nil, fmt.Errorf("pagerduty sink: routing key must not be empty")
	}

	return &PagerDutySink{
		client:         &http.Client{},
		routingKey:     cfg.RoutingKey,
		severityFilter: cfg.SeverityFilter,
		logger:         logger,
		retryCfg:       defaultRetryConfig(),
		eventsURL:      pagerDutyEventsV2,
	}, nil
}

// Name returns "pagerduty".
func (s *PagerDutySink) Name() string {
	return pagerDutySinkName
}

// SeverityFilter returns the configured severity filter.
func (s *PagerDutySink) SeverityFilter() []model.Severity {
	return s.severityFilter
}

// Deliver sends the RCA report to PagerDuty with retry logic.
func (s *PagerDutySink) Deliver(ctx context.Context, report *model.RCAReport) error {
	if report == nil {
		return errNilReport
	}

	return deliverWithRetry(ctx, s.logger, pagerDutySinkName, s.retryCfg, func(ctx context.Context) error {
		return s.deliver(ctx, report)
	})
}

func (s *PagerDutySink) deliver(ctx context.Context, report *model.RCAReport) error {
	payload := s.buildPayload(report)

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("pagerduty sink: marshaling payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.eventsURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("pagerduty sink: creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("pagerduty sink: sending request: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("pagerduty sink: unexpected status %d", resp.StatusCode)
	}

	return nil
}

// pdPayload represents the PagerDuty Events API v2 payload.
type pdPayload struct {
	RoutingKey  string    `json:"routing_key"`
	EventAction string    `json:"event_action"`
	DedupKey    string    `json:"dedup_key"`
	Payload     pdDetails `json:"payload"`
}

type pdDetails struct {
	Summary       string            `json:"summary"`
	Source        string            `json:"source"`
	Severity      string            `json:"severity"`
	Component     string            `json:"component,omitempty"`
	Group         string            `json:"group,omitempty"`
	Class         string            `json:"class,omitempty"`
	CustomDetails map[string]string `json:"custom_details,omitempty"`
}

func (s *PagerDutySink) buildPayload(report *model.RCAReport) pdPayload {
	resource := resourceSummary(report)

	// PagerDuty severity mapping: critical -> critical, warning -> warning,
	// info -> info. PagerDuty also accepts "error" but we map directly.
	pdSeverity := string(report.Severity)

	customDetails := map[string]string{
		"root_cause":    report.RootCause,
		"category":      report.Category,
		"blast_radius":  report.BlastRadius,
		"confidence":    fmt.Sprintf("%.0f%%", report.Confidence*100),
		"systemic":      fmt.Sprintf("%t", report.Systemic),
		"analyzer":      report.AnalyzerBackend,
		"fault_event_id": report.FaultEventID,
	}

	return pdPayload{
		RoutingKey:  s.routingKey,
		EventAction: "trigger",
		DedupKey:    report.FaultEventID,
		Payload: pdDetails{
			Summary:       fmt.Sprintf("K8s Wormsign: %s — %s", resource, report.RootCause),
			Source:        "wormsign",
			Severity:      pdSeverity,
			Component:     resource,
			Class:         report.Category,
			CustomDetails: customDetails,
		},
	}
}
