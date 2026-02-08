package sink

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

const slackSinkName = "slack"

// SlackConfig holds configuration for the Slack sink.
type SlackConfig struct {
	// WebhookURL is the Slack incoming webhook URL.
	WebhookURL string
	// Channel overrides the default channel (optional).
	Channel string
	// SeverityFilter restricts which severities are delivered.
	SeverityFilter []model.Severity
	// TemplateOverride replaces the default message format (optional).
	TemplateOverride string
}

// SlackSink delivers RCA reports to Slack via incoming webhooks.
type SlackSink struct {
	client         *http.Client
	webhookURL     string
	channel        string
	severityFilter []model.Severity
	logger         *slog.Logger
	retryCfg       retryConfig
}

// NewSlackSink creates a new Slack sink. The webhookURL is validated against
// the Slack allowed domain (hooks.slack.com).
func NewSlackSink(cfg SlackConfig, logger *slog.Logger) (*SlackSink, error) {
	if logger == nil {
		return nil, errNilLogger
	}
	if cfg.WebhookURL == "" {
		return nil, fmt.Errorf("slack sink: webhook URL must not be empty")
	}
	if err := ValidateBuiltInURL("slack", cfg.WebhookURL); err != nil {
		return nil, fmt.Errorf("slack sink: %w", err)
	}

	return &SlackSink{
		client:         &http.Client{},
		webhookURL:     cfg.WebhookURL,
		channel:        cfg.Channel,
		severityFilter: cfg.SeverityFilter,
		logger:         logger,
		retryCfg:       defaultRetryConfig(),
	}, nil
}

// Name returns "slack".
func (s *SlackSink) Name() string {
	return slackSinkName
}

// SeverityFilter returns the configured severity filter.
func (s *SlackSink) SeverityFilter() []model.Severity {
	return s.severityFilter
}

// Deliver sends the RCA report to Slack with retry logic.
func (s *SlackSink) Deliver(ctx context.Context, report *model.RCAReport) error {
	if report == nil {
		return errNilReport
	}

	return deliverWithRetry(ctx, s.logger, slackSinkName, s.retryCfg, func(ctx context.Context) error {
		return s.deliver(ctx, report)
	})
}

func (s *SlackSink) deliver(ctx context.Context, report *model.RCAReport) error {
	payload := s.buildPayload(report)

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("slack sink: marshaling payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("slack sink: creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("slack sink: sending request: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("slack sink: unexpected status %d", resp.StatusCode)
	}

	return nil
}

// slackPayload represents the Slack incoming webhook payload.
type slackPayload struct {
	Channel     string            `json:"channel,omitempty"`
	Attachments []slackAttachment `json:"attachments"`
}

type slackAttachment struct {
	Color    string       `json:"color"`
	Fallback string       `json:"fallback"`
	Title    string       `json:"title"`
	Text     string       `json:"text"`
	Fields   []slackField `json:"fields"`
}

type slackField struct {
	Title string `json:"title"`
	Value string `json:"value"`
	Short bool   `json:"short"`
}

func (s *SlackSink) buildPayload(report *model.RCAReport) slackPayload {
	color := severityColor(report.Severity)

	resource := resourceSummary(report)
	remediation := strings.Join(report.Remediation, "\n")
	if remediation == "" {
		remediation = "No remediation steps provided."
	}

	return slackPayload{
		Channel: s.channel,
		Attachments: []slackAttachment{
			{
				Color:    color,
				Fallback: fmt.Sprintf("[%s] %s: %s", report.Severity, resource, report.RootCause),
				Title:    fmt.Sprintf("K8s Wormsign: %s", report.RootCause),
				Text:     fmt.Sprintf("*Resource:* %s", resource),
				Fields: []slackField{
					{Title: "Severity", Value: string(report.Severity), Short: true},
					{Title: "Category", Value: report.Category, Short: true},
					{Title: "Systemic", Value: fmt.Sprintf("%t", report.Systemic), Short: true},
					{Title: "Confidence", Value: fmt.Sprintf("%.0f%%", report.Confidence*100), Short: true},
					{Title: "Blast Radius", Value: report.BlastRadius, Short: false},
					{Title: "Remediation", Value: remediation, Short: false},
				},
			},
		},
	}
}

// severityColor maps severity to Slack attachment color codes.
func severityColor(s model.Severity) string {
	switch s {
	case model.SeverityCritical:
		return "#FF0000"
	case model.SeverityWarning:
		return "#FFA500"
	case model.SeverityInfo:
		return "#36A64F"
	default:
		return "#808080"
	}
}

// resourceSummary returns a human-readable resource identifier from the report.
func resourceSummary(report *model.RCAReport) string {
	if report.DiagnosticBundle.SuperEvent != nil {
		ref := report.DiagnosticBundle.SuperEvent.PrimaryResource
		return ref.String()
	}
	if report.DiagnosticBundle.FaultEvent != nil {
		ref := report.DiagnosticBundle.FaultEvent.Resource
		return ref.String()
	}
	return "unknown"
}
