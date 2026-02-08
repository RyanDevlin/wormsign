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
	"text/template"
	"time"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

const customWebhookSinkPrefix = "custom-webhook:"

// templateTimeout is the maximum time allowed for template execution.
// See DECISIONS.md D5: Template execution has a timeout of 5 seconds.
const templateTimeout = 5 * time.Second

// CustomWebhookConfig holds configuration for a custom webhook sink created
// from a WormsignSink CRD.
type CustomWebhookConfig struct {
	// Name is the unique name for this custom sink (typically the CRD name).
	Name string
	// URL is the webhook endpoint URL.
	URL string
	// Method is the HTTP method (POST, PUT, PATCH).
	Method string
	// Headers are additional HTTP headers.
	Headers map[string]string
	// BodyTemplate is a Go text/template for rendering the request body.
	BodyTemplate string
	// SeverityFilter restricts which severities are delivered.
	SeverityFilter []model.Severity
	// SSRFValidator validates the webhook URL against allowed domains.
	SSRFValidator *SSRFValidator
}

// CustomWebhookSink delivers RCA reports via webhook using a Go text/template
// for body rendering. It implements custom sinks defined by WormsignSink CRDs.
type CustomWebhookSink struct {
	client         *http.Client
	name           string
	url            string
	method         string
	headers        map[string]string
	bodyTemplate   *template.Template
	severityFilter []model.Severity
	logger         *slog.Logger
	retryCfg       retryConfig
}

// NewCustomWebhookSink creates a new custom webhook sink from a CRD config.
func NewCustomWebhookSink(cfg CustomWebhookConfig, logger *slog.Logger) (*CustomWebhookSink, error) {
	if logger == nil {
		return nil, errNilLogger
	}
	if cfg.Name == "" {
		return nil, fmt.Errorf("custom webhook sink: name must not be empty")
	}
	if cfg.URL == "" {
		return nil, fmt.Errorf("custom webhook sink %q: URL must not be empty", cfg.Name)
	}

	// Validate URL against SSRF rules if a validator is provided.
	if cfg.SSRFValidator != nil {
		if err := cfg.SSRFValidator.ValidateURL(cfg.URL); err != nil {
			return nil, fmt.Errorf("custom webhook sink %q: %w", cfg.Name, err)
		}
	}

	method := cfg.Method
	if method == "" {
		method = http.MethodPost
	}
	method = strings.ToUpper(method)
	if method != http.MethodPost && method != http.MethodPut && method != http.MethodPatch {
		return nil, fmt.Errorf("custom webhook sink %q: unsupported method %q (must be POST, PUT, or PATCH)", cfg.Name, method)
	}

	headers := cfg.Headers
	if headers == nil {
		headers = make(map[string]string)
	}

	sinkName := customWebhookSinkPrefix + cfg.Name

	var tmpl *template.Template
	if cfg.BodyTemplate != "" {
		var err error
		tmpl, err = template.New(sinkName).Parse(cfg.BodyTemplate)
		if err != nil {
			return nil, fmt.Errorf("custom webhook sink %q: parsing body template: %w", cfg.Name, err)
		}
	}

	return &CustomWebhookSink{
		client:         &http.Client{},
		name:           sinkName,
		url:            cfg.URL,
		method:         method,
		headers:        headers,
		bodyTemplate:   tmpl,
		severityFilter: cfg.SeverityFilter,
		logger:         logger,
		retryCfg:       defaultRetryConfig(),
	}, nil
}

// Name returns "custom-webhook:<crd-name>".
func (s *CustomWebhookSink) Name() string {
	return s.name
}

// SeverityFilter returns the configured severity filter.
func (s *CustomWebhookSink) SeverityFilter() []model.Severity {
	return s.severityFilter
}

// Deliver sends the RCA report to the custom webhook with retry logic.
func (s *CustomWebhookSink) Deliver(ctx context.Context, report *model.RCAReport) error {
	if report == nil {
		return errNilReport
	}

	return deliverWithRetry(ctx, s.logger, s.name, s.retryCfg, func(ctx context.Context) error {
		return s.deliver(ctx, report)
	})
}

func (s *CustomWebhookSink) deliver(ctx context.Context, report *model.RCAReport) error {
	body, err := s.renderBody(report)
	if err != nil {
		return fmt.Errorf("%s: rendering body: %w", s.name, err)
	}

	req, err := http.NewRequestWithContext(ctx, s.method, s.url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%s: creating request: %w", s.name, err)
	}

	for k, v := range s.headers {
		req.Header.Set(k, v)
	}
	// Set Content-Type if not already provided.
	if req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("%s: sending request: %w", s.name, err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("%s: unexpected status %d", s.name, resp.StatusCode)
	}

	return nil
}

// templateData is the data structure passed to the body template.
// Fields are exported for template access.
type templateData struct {
	FaultEventID string
	RootCause    string
	Severity     string
	Category     string
	Systemic     bool
	BlastRadius  string
	Remediation  []string
	Confidence   float64
	Resource     templateResource
}

type templateResource struct {
	Kind      string
	Namespace string
	Name      string
}

func (s *CustomWebhookSink) renderBody(report *model.RCAReport) ([]byte, error) {
	if s.bodyTemplate == nil {
		// No template: send report as JSON.
		return json.Marshal(report)
	}

	data := templateData{
		FaultEventID: report.FaultEventID,
		RootCause:    report.RootCause,
		Severity:     string(report.Severity),
		Category:     report.Category,
		Systemic:     report.Systemic,
		BlastRadius:  report.BlastRadius,
		Remediation:  report.Remediation,
		Confidence:   report.Confidence,
		Resource:     extractResource(report),
	}

	var buf bytes.Buffer

	// Execute with a timeout to prevent runaway templates (D5).
	done := make(chan error, 1)
	go func() {
		done <- s.bodyTemplate.Execute(&buf, data)
	}()

	select {
	case err := <-done:
		if err != nil {
			return nil, fmt.Errorf("executing template: %w", err)
		}
		return buf.Bytes(), nil
	case <-time.After(templateTimeout):
		return nil, fmt.Errorf("template execution timed out after %v", templateTimeout)
	}
}

func extractResource(report *model.RCAReport) templateResource {
	if report.DiagnosticBundle.SuperEvent != nil {
		ref := report.DiagnosticBundle.SuperEvent.PrimaryResource
		return templateResource{Kind: ref.Kind, Namespace: ref.Namespace, Name: ref.Name}
	}
	if report.DiagnosticBundle.FaultEvent != nil {
		ref := report.DiagnosticBundle.FaultEvent.Resource
		return templateResource{Kind: ref.Kind, Namespace: ref.Namespace, Name: ref.Name}
	}
	return templateResource{}
}
