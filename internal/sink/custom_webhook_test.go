package sink

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"text/template"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

func TestNewCustomWebhookSink_Validation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     CustomWebhookConfig
		logger  bool
		wantErr bool
		errMsg  string
	}{
		{
			name:    "nil logger",
			cfg:     CustomWebhookConfig{Name: "test", URL: "https://example.com/hook"},
			logger:  false,
			wantErr: true,
		},
		{
			name:    "empty name",
			cfg:     CustomWebhookConfig{Name: "", URL: "https://example.com/hook"},
			logger:  true,
			wantErr: true,
		},
		{
			name:    "empty URL",
			cfg:     CustomWebhookConfig{Name: "test", URL: ""},
			logger:  true,
			wantErr: true,
		},
		{
			name: "invalid method",
			cfg: CustomWebhookConfig{
				Name:   "test",
				URL:    "https://example.com/hook",
				Method: "DELETE",
			},
			logger:  true,
			wantErr: true,
			errMsg:  "unsupported method",
		},
		{
			name: "SSRF blocked",
			cfg: CustomWebhookConfig{
				Name: "test",
				URL:  "https://evil.com/hook",
				SSRFValidator: func() *SSRFValidator {
					v, _ := NewSSRFValidator([]string{"*.example.com"})
					return v
				}(),
			},
			logger:  true,
			wantErr: true,
		},
		{
			name: "invalid template",
			cfg: CustomWebhookConfig{
				Name:         "test",
				URL:          "https://example.com/hook",
				BodyTemplate: "{{ .InvalidSyntax",
			},
			logger:  true,
			wantErr: true,
		},
		{
			name: "valid with template",
			cfg: CustomWebhookConfig{
				Name:         "test",
				URL:          "https://example.com/hook",
				Method:       "POST",
				BodyTemplate: `{"root_cause": "{{ .RootCause }}"}`,
			},
			logger:  true,
			wantErr: false,
		},
		{
			name: "valid PUT method",
			cfg: CustomWebhookConfig{
				Name:   "test",
				URL:    "https://example.com/hook",
				Method: "put",
			},
			logger:  true,
			wantErr: false,
		},
		{
			name: "valid PATCH method",
			cfg: CustomWebhookConfig{
				Name:   "test",
				URL:    "https://example.com/hook",
				Method: "PATCH",
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
			_, err := NewCustomWebhookSink(tt.cfg, l)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewCustomWebhookSink() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.errMsg != "" && err != nil && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("error %q should contain %q", err.Error(), tt.errMsg)
			}
		})
	}
}

func TestCustomWebhookSink_Name(t *testing.T) {
	s, _ := NewCustomWebhookSink(CustomWebhookConfig{
		Name: "teams-webhook",
		URL:  "https://example.com/hook",
	}, silentLogger())

	want := "custom-webhook:teams-webhook"
	if s.Name() != want {
		t.Errorf("Name() = %q, want %q", s.Name(), want)
	}
}

func TestCustomWebhookSink_Deliver_WithTemplate(t *testing.T) {
	var receivedBody []byte

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedBody = body
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	tmplStr := `{"summary": "{{ .RootCause }}", "severity": "{{ .Severity }}", "resource": "{{ .Resource.Kind }}/{{ .Resource.Name }}"}`
	tmpl, err := template.New("test").Parse(tmplStr)
	if err != nil {
		t.Fatalf("parse template: %v", err)
	}

	s := &CustomWebhookSink{
		client:       server.Client(),
		name:         "custom-webhook:test",
		url:          server.URL,
		method:       http.MethodPost,
		headers:      map[string]string{"Content-Type": "application/json"},
		bodyTemplate: tmpl,
		logger:       silentLogger(),
		retryCfg: retryConfig{
			maxAttempts: 1,
			baseDelay:   0,
			multiplier:  1,
		},
	}

	report := testReport()
	err = s.Deliver(context.Background(), report)
	if err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}

	bodyStr := string(receivedBody)
	if !strings.Contains(bodyStr, "Pod OOMKilled due to memory limit") {
		t.Errorf("body should contain root cause, got %q", bodyStr)
	}
	if !strings.Contains(bodyStr, "critical") {
		t.Errorf("body should contain severity, got %q", bodyStr)
	}
	if !strings.Contains(bodyStr, "Pod/test-pod") {
		t.Errorf("body should contain resource, got %q", bodyStr)
	}
}

func TestCustomWebhookSink_Deliver_WithoutTemplate(t *testing.T) {
	var receivedBody []byte

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedBody = body
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	s := &CustomWebhookSink{
		client:  server.Client(),
		name:    "custom-webhook:test",
		url:     server.URL,
		method:  http.MethodPost,
		headers: make(map[string]string),
		logger:  silentLogger(),
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

	var decoded model.RCAReport
	if err := json.Unmarshal(receivedBody, &decoded); err != nil {
		t.Fatalf("unmarshal body: %v", err)
	}
	if decoded.FaultEventID != "test-event-123" {
		t.Errorf("FaultEventID = %q, want %q", decoded.FaultEventID, "test-event-123")
	}
}

func TestCustomWebhookSink_Deliver_CustomMethod(t *testing.T) {
	var receivedMethod string

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedMethod = r.Method
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	s := &CustomWebhookSink{
		client:  server.Client(),
		name:    "custom-webhook:test",
		url:     server.URL,
		method:  http.MethodPut,
		headers: make(map[string]string),
		logger:  silentLogger(),
		retryCfg: retryConfig{
			maxAttempts: 1,
			baseDelay:   0,
			multiplier:  1,
		},
	}

	err := s.Deliver(context.Background(), testReport())
	if err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}
	if receivedMethod != http.MethodPut {
		t.Errorf("method = %q, want PUT", receivedMethod)
	}
}

func TestCustomWebhookSink_Deliver_NilReport(t *testing.T) {
	s := &CustomWebhookSink{logger: silentLogger(), name: "test"}
	err := s.Deliver(context.Background(), nil)
	if err == nil {
		t.Fatal("Deliver(nil) should return error")
	}
}

func TestCustomWebhookSink_Deliver_ServerError(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	s := &CustomWebhookSink{
		client:  server.Client(),
		name:    "custom-webhook:test",
		url:     server.URL,
		method:  http.MethodPost,
		headers: make(map[string]string),
		logger:  silentLogger(),
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

func TestCustomWebhookSink_Deliver_CustomHeaders(t *testing.T) {
	var receivedHeaders http.Header

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	s := &CustomWebhookSink{
		client: server.Client(),
		name:   "custom-webhook:test",
		url:    server.URL,
		method: http.MethodPost,
		headers: map[string]string{
			"X-Custom": "test-value",
			"Accept":   "application/json",
		},
		logger: silentLogger(),
		retryCfg: retryConfig{
			maxAttempts: 1,
			baseDelay:   0,
			multiplier:  1,
		},
	}

	err := s.Deliver(context.Background(), testReport())
	if err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}

	if got := receivedHeaders.Get("X-Custom"); got != "test-value" {
		t.Errorf("X-Custom = %q, want %q", got, "test-value")
	}
}

func TestCustomWebhookSink_Deliver_DefaultContentType(t *testing.T) {
	var receivedContentType string

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	// No Content-Type in headers — should default to application/json.
	s := &CustomWebhookSink{
		client:  server.Client(),
		name:    "custom-webhook:test",
		url:     server.URL,
		method:  http.MethodPost,
		headers: make(map[string]string),
		logger:  silentLogger(),
		retryCfg: retryConfig{
			maxAttempts: 1,
			baseDelay:   0,
			multiplier:  1,
		},
	}

	err := s.Deliver(context.Background(), testReport())
	if err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}

	if receivedContentType != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", receivedContentType)
	}
}

func TestCustomWebhookSink_SeverityFilter(t *testing.T) {
	s, _ := NewCustomWebhookSink(CustomWebhookConfig{
		Name:           "test",
		URL:            "https://example.com/hook",
		SeverityFilter: []model.Severity{model.SeverityCritical, model.SeverityWarning},
	}, silentLogger())

	f := s.SeverityFilter()
	if len(f) != 2 {
		t.Errorf("SeverityFilter() len = %d, want 2", len(f))
	}
}

func TestExtractResource(t *testing.T) {
	r := testReport()
	res := extractResource(r)
	if res.Kind != "Pod" || res.Name != "test-pod" || res.Namespace != "default" {
		t.Errorf("extractResource(fault) = %+v, want Pod/default/test-pod", res)
	}

	sr := testSuperEventReport()
	res = extractResource(sr)
	if res.Kind != "Node" || res.Name != "node-1" {
		t.Errorf("extractResource(super) = %+v, want Node/node-1", res)
	}

	empty := &model.RCAReport{DiagnosticBundle: model.DiagnosticBundle{}}
	res = extractResource(empty)
	if res.Kind != "" {
		t.Errorf("extractResource(empty) = %+v, want empty", res)
	}
}

func TestCustomWebhookSink_Deliver_SuperEvent(t *testing.T) {
	var receivedBody []byte

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		receivedBody = body
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	tmplStr := `{"resource": "{{ .Resource.Kind }}/{{ .Resource.Name }}", "systemic": {{ .Systemic }}}`
	tmpl, err := template.New("test").Parse(tmplStr)
	if err != nil {
		t.Fatalf("parse template: %v", err)
	}

	s := &CustomWebhookSink{
		client:       server.Client(),
		name:         "custom-webhook:test",
		url:          server.URL,
		method:       http.MethodPost,
		headers:      map[string]string{"Content-Type": "application/json"},
		bodyTemplate: tmpl,
		logger:       silentLogger(),
		retryCfg: retryConfig{
			maxAttempts: 1,
			baseDelay:   0,
			multiplier:  1,
		},
	}

	report := testSuperEventReport()
	err = s.Deliver(context.Background(), report)
	if err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}

	bodyStr := string(receivedBody)
	if !strings.Contains(bodyStr, "Node/node-1") {
		t.Errorf("body should contain primary resource, got %q", bodyStr)
	}
	if !strings.Contains(bodyStr, "true") {
		t.Errorf("body should contain systemic=true, got %q", bodyStr)
	}
}
