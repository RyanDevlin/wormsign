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

func TestNewWebhookSink_Validation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     WebhookConfig
		logger  bool
		wantErr bool
	}{
		{
			name:    "nil logger",
			cfg:     WebhookConfig{URL: "https://example.com/webhook"},
			logger:  false,
			wantErr: true,
		},
		{
			name:    "empty URL",
			cfg:     WebhookConfig{URL: ""},
			logger:  true,
			wantErr: true,
		},
		{
			name: "SSRF blocked URL",
			cfg: WebhookConfig{
				URL:            "https://evil.com/webhook",
				AllowedDomains: []string{"*.example.com"},
			},
			logger:  true,
			wantErr: true,
		},
		{
			name: "SSRF allowed URL",
			cfg: WebhookConfig{
				URL:            "https://api.example.com/webhook",
				AllowedDomains: []string{"*.example.com"},
			},
			logger:  true,
			wantErr: false,
		},
		{
			name: "no SSRF validation when no domains configured",
			cfg: WebhookConfig{
				URL:            "https://anything.com/webhook",
				AllowedDomains: nil,
			},
			logger:  true,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var l = silentLogger()
			if !tt.logger {
				l = nil
			}
			_, err := NewWebhookSink(tt.cfg, l)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewWebhookSink() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestWebhookSink_Name(t *testing.T) {
	s, _ := NewWebhookSink(WebhookConfig{URL: "https://example.com/hook"}, silentLogger())
	if s.Name() != "webhook" {
		t.Errorf("Name() = %q, want %q", s.Name(), "webhook")
	}
}

func TestWebhookSink_Deliver(t *testing.T) {
	var receivedBody []byte
	var receivedHeaders http.Header

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		receivedHeaders = r.Header
		body, _ := io.ReadAll(r.Body)
		receivedBody = body
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	s := &WebhookSink{
		client: server.Client(),
		url:    server.URL,
		headers: map[string]string{
			"X-Custom-Header": "test-value",
		},
		logger: silentLogger(),
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

	// Verify body is valid JSON containing the report.
	var decoded model.RCAReport
	if err := json.Unmarshal(receivedBody, &decoded); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if decoded.FaultEventID != "test-event-123" {
		t.Errorf("FaultEventID = %q, want %q", decoded.FaultEventID, "test-event-123")
	}

	// Verify custom header.
	if got := receivedHeaders.Get("X-Custom-Header"); got != "test-value" {
		t.Errorf("X-Custom-Header = %q, want %q", got, "test-value")
	}

	// Verify Content-Type.
	if got := receivedHeaders.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want %q", got, "application/json")
	}
}

func TestWebhookSink_Deliver_NilReport(t *testing.T) {
	s := &WebhookSink{logger: silentLogger()}
	err := s.Deliver(context.Background(), nil)
	if err == nil {
		t.Fatal("Deliver(nil) should return error")
	}
}

func TestWebhookSink_Deliver_ServerError(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	s := &WebhookSink{
		client: server.Client(),
		url:    server.URL,
		logger: silentLogger(),
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

func TestWebhookSink_SeverityFilter(t *testing.T) {
	s, _ := NewWebhookSink(WebhookConfig{
		URL:            "https://example.com/hook",
		SeverityFilter: []model.Severity{model.SeverityCritical, model.SeverityWarning},
	}, silentLogger())

	f := s.SeverityFilter()
	if len(f) != 2 {
		t.Errorf("SeverityFilter() len = %d, want 2", len(f))
	}
}
