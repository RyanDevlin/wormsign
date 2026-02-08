package sink

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

func TestNewSlackSink_Validation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     SlackConfig
		wantErr bool
	}{
		{
			name:    "nil logger",
			cfg:     SlackConfig{WebhookURL: "https://hooks.slack.com/services/T00/B00/xxx"},
			wantErr: true,
		},
		{
			name:    "empty URL",
			cfg:     SlackConfig{WebhookURL: ""},
			wantErr: true,
		},
		{
			name: "invalid domain",
			cfg: SlackConfig{
				WebhookURL: "https://evil.com/webhook",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var logger = silentLogger()
			if tt.name == "nil logger" {
				logger = nil
			}
			_, err := NewSlackSink(tt.cfg, logger)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewSlackSink() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSlackSink_Name(t *testing.T) {
	s := &SlackSink{logger: silentLogger()}
	if s.Name() != "slack" {
		t.Errorf("Name() = %q, want %q", s.Name(), "slack")
	}
}

func TestSlackSink_Deliver(t *testing.T) {
	var receivedBody []byte
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		body, _ := io.ReadAll(r.Body)
		receivedBody = body
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	s := &SlackSink{
		client:     server.Client(),
		webhookURL: server.URL,
		channel:    "#test-channel",
		logger:     silentLogger(),
		retryCfg: retryConfig{
			maxAttempts: 1,
			baseDelay:   0,
			multiplier:  1,
		},
	}

	report := testReport()
	err := s.Deliver(context.Background(), report)
	if err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}

	// Verify the payload structure.
	var payload slackPayload
	if err := json.Unmarshal(receivedBody, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	if payload.Channel != "#test-channel" {
		t.Errorf("channel = %q, want %q", payload.Channel, "#test-channel")
	}
	if len(payload.Attachments) != 1 {
		t.Fatalf("expected 1 attachment, got %d", len(payload.Attachments))
	}
	att := payload.Attachments[0]
	if att.Color != "#FF0000" {
		t.Errorf("color = %q, want #FF0000 for critical", att.Color)
	}
}

func TestSlackSink_Deliver_NilReport(t *testing.T) {
	s := &SlackSink{logger: silentLogger()}
	err := s.Deliver(context.Background(), nil)
	if err == nil {
		t.Fatal("Deliver(nil) should return error")
	}
}

func TestSlackSink_Deliver_ServerError(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	s := &SlackSink{
		client:     server.Client(),
		webhookURL: server.URL,
		logger:     silentLogger(),
		retryCfg: retryConfig{
			maxAttempts: 1,
			baseDelay:   0,
			multiplier:  1,
		},
	}

	err := s.Deliver(context.Background(), testReport())
	if err == nil {
		t.Fatal("expected error for server error response")
	}
}

func TestSlackSink_Deliver_Retry(t *testing.T) {
	attempts := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	s := &SlackSink{
		client:     server.Client(),
		webhookURL: server.URL,
		logger:     silentLogger(),
		retryCfg: retryConfig{
			maxAttempts: 3,
			baseDelay:   1,
			multiplier:  1,
		},
	}

	err := s.Deliver(context.Background(), testReport())
	if err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
	if attempts != 2 {
		t.Errorf("expected 2 attempts, got %d", attempts)
	}
}

func TestSlackSink_SeverityFilter(t *testing.T) {
	s := &SlackSink{
		severityFilter: []model.Severity{model.SeverityCritical},
	}
	f := s.SeverityFilter()
	if len(f) != 1 || f[0] != model.SeverityCritical {
		t.Errorf("SeverityFilter() = %v, want [critical]", f)
	}
}

func TestSeverityColor(t *testing.T) {
	tests := []struct {
		severity model.Severity
		want     string
	}{
		{model.SeverityCritical, "#FF0000"},
		{model.SeverityWarning, "#FFA500"},
		{model.SeverityInfo, "#36A64F"},
		{model.Severity("other"), "#808080"},
	}
	for _, tt := range tests {
		got := severityColor(tt.severity)
		if got != tt.want {
			t.Errorf("severityColor(%q) = %q, want %q", tt.severity, got, tt.want)
		}
	}
}

func TestResourceSummary(t *testing.T) {
	// FaultEvent report.
	r := testReport()
	got := resourceSummary(r)
	if got != "Pod/default/test-pod" {
		t.Errorf("resourceSummary(fault) = %q, want %q", got, "Pod/default/test-pod")
	}

	// SuperEvent report.
	sr := testSuperEventReport()
	got = resourceSummary(sr)
	if got != "Node/node-1" {
		t.Errorf("resourceSummary(super) = %q, want %q", got, "Node/node-1")
	}

	// Empty report.
	empty := &model.RCAReport{DiagnosticBundle: model.DiagnosticBundle{}}
	got = resourceSummary(empty)
	if got != "unknown" {
		t.Errorf("resourceSummary(empty) = %q, want %q", got, "unknown")
	}
}
