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

const webhookSinkName = "webhook"

// WebhookConfig holds configuration for the generic webhook sink.
type WebhookConfig struct {
	// URL is the target webhook endpoint.
	URL string
	// Headers are additional HTTP headers to include.
	Headers map[string]string
	// SeverityFilter restricts which severities are delivered.
	SeverityFilter []model.Severity
	// AllowedDomains for SSRF protection.
	AllowedDomains []string
}

// WebhookSink delivers RCA reports to an arbitrary HTTP endpoint as JSON.
type WebhookSink struct {
	client         *http.Client
	url            string
	headers        map[string]string
	severityFilter []model.Severity
	logger         *slog.Logger
	retryCfg       retryConfig
}

// NewWebhookSink creates a new webhook sink. The URL is validated against
// the provided allowed domains for SSRF protection.
func NewWebhookSink(cfg WebhookConfig, logger *slog.Logger) (*WebhookSink, error) {
	if logger == nil {
		return nil, errNilLogger
	}
	if cfg.URL == "" {
		return nil, fmt.Errorf("webhook sink: URL must not be empty")
	}

	if len(cfg.AllowedDomains) > 0 {
		validator, err := NewSSRFValidator(cfg.AllowedDomains)
		if err != nil {
			return nil, fmt.Errorf("webhook sink: %w", err)
		}
		if err := validator.ValidateURL(cfg.URL); err != nil {
			return nil, fmt.Errorf("webhook sink: %w", err)
		}
	}

	headers := cfg.Headers
	if headers == nil {
		headers = make(map[string]string)
	}

	return &WebhookSink{
		client:         noRedirectHTTPClient(),
		url:            cfg.URL,
		headers:        headers,
		severityFilter: cfg.SeverityFilter,
		logger:         logger,
		retryCfg:       defaultRetryConfig(),
	}, nil
}

// Name returns "webhook".
func (s *WebhookSink) Name() string {
	return webhookSinkName
}

// SeverityFilter returns the configured severity filter.
func (s *WebhookSink) SeverityFilter() []model.Severity {
	return s.severityFilter
}

// Deliver sends the RCA report to the webhook endpoint with retry logic.
func (s *WebhookSink) Deliver(ctx context.Context, report *model.RCAReport) error {
	if report == nil {
		return errNilReport
	}

	return deliverWithRetry(ctx, s.logger, webhookSinkName, s.retryCfg, func(ctx context.Context) error {
		return s.deliver(ctx, report)
	})
}

func (s *WebhookSink) deliver(ctx context.Context, report *model.RCAReport) error {
	body, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("webhook sink: marshaling report: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("webhook sink: creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	for k, v := range s.headers {
		req.Header.Set(k, v)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("webhook sink: sending request: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook sink: unexpected status %d", resp.StatusCode)
	}

	return nil
}
