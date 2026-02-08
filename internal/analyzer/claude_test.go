package analyzer

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/k8s-wormsign/k8s-wormsign/internal/analyzer/prompt"
	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

// mockSecretReader is a test double for SecretReader.
type mockSecretReader struct {
	secrets map[string]string
	err     error
}

func (m *mockSecretReader) ReadSecret(ctx context.Context, namespace, name, key string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	k := namespace + "/" + name + "/" + key
	if v, ok := m.secrets[k]; ok {
		return v, nil
	}
	return "", nil
}

func newMockSecretReader(apiKey string) *mockSecretReader {
	return &mockSecretReader{
		secrets: map[string]string{
			"ns/secret-name/api-key": apiKey,
		},
	}
}

func testSecretRef() SecretRef {
	return SecretRef{
		Namespace: "ns",
		Name:      "secret-name",
		Key:       "api-key",
	}
}

func testPrompter() *prompt.Builder {
	return prompt.NewBuilder("", "")
}

func testDiagnosticBundle() model.DiagnosticBundle {
	return model.DiagnosticBundle{
		FaultEvent: &model.FaultEvent{
			ID:           "fe-test-123",
			DetectorName: "PodCrashLoop",
			Severity:     model.SeverityCritical,
			Timestamp:    time.Now().UTC(),
			Resource: model.ResourceRef{
				Kind:      "Pod",
				Namespace: "default",
				Name:      "test-pod",
			},
			Description: "pod is crash looping",
		},
		Timestamp: time.Now().UTC(),
		Sections: []model.DiagnosticSection{
			{
				GathererName: "PodEvents",
				Title:        "Pod Events",
				Content:      "Warning  BackOff  pod/test-pod  Back-off restarting failed container",
				Format:       "text",
			},
		},
	}
}

func validLLMResponseJSON() string {
	return `{
		"rootCause": "OOMKilled due to insufficient memory limits",
		"severity": "critical",
		"category": "resources",
		"systemic": true,
		"blastRadius": "All pods in deployment web-api",
		"remediation": ["Increase memory limits to 512Mi", "Add HPA for auto-scaling"],
		"relatedResources": [
			{"kind": "Deployment", "namespace": "default", "name": "web-api"}
		],
		"confidence": 0.95
	}`
}

// --- NewClaudeAnalyzer validation tests ---

func TestNewClaudeAnalyzer_Validation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     ClaudeConfig
		sr      SecretReader
		prompt  *prompt.Builder
		wantErr string
	}{
		{
			name:    "empty model",
			cfg:     ClaudeConfig{Model: "", MaxTokens: 4096, APIKeyRef: testSecretRef()},
			sr:      newMockSecretReader("key"),
			prompt:  testPrompter(),
			wantErr: "model must not be empty",
		},
		{
			name:    "zero maxTokens",
			cfg:     ClaudeConfig{Model: "claude-sonnet-4-5-20250929", MaxTokens: 0, APIKeyRef: testSecretRef()},
			sr:      newMockSecretReader("key"),
			prompt:  testPrompter(),
			wantErr: "maxTokens must be > 0",
		},
		{
			name:    "negative maxTokens",
			cfg:     ClaudeConfig{Model: "claude-sonnet-4-5-20250929", MaxTokens: -1, APIKeyRef: testSecretRef()},
			sr:      newMockSecretReader("key"),
			prompt:  testPrompter(),
			wantErr: "maxTokens must be > 0",
		},
		{
			name:    "invalid secretRef",
			cfg:     ClaudeConfig{Model: "claude-sonnet-4-5-20250929", MaxTokens: 4096, APIKeyRef: SecretRef{}},
			sr:      newMockSecretReader("key"),
			prompt:  testPrompter(),
			wantErr: "apiKeyRef",
		},
		{
			name:    "nil secretReader",
			cfg:     ClaudeConfig{Model: "claude-sonnet-4-5-20250929", MaxTokens: 4096, APIKeyRef: testSecretRef()},
			sr:      nil,
			prompt:  testPrompter(),
			wantErr: "secretReader must not be nil",
		},
		{
			name:    "nil prompter",
			cfg:     ClaudeConfig{Model: "claude-sonnet-4-5-20250929", MaxTokens: 4096, APIKeyRef: testSecretRef()},
			sr:      newMockSecretReader("key"),
			prompt:  nil,
			wantErr: "prompter must not be nil",
		},
		{
			name:    "nil logger",
			cfg:     ClaudeConfig{Model: "claude-sonnet-4-5-20250929", MaxTokens: 4096, APIKeyRef: testSecretRef()},
			sr:      newMockSecretReader("key"),
			prompt:  testPrompter(),
			wantErr: "logger must not be nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := testLogger()
			if tt.name == "nil logger" {
				logger = nil
			}
			_, err := NewClaudeAnalyzer(tt.cfg, tt.sr, tt.prompt, logger)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestNewClaudeAnalyzer_ValidConfig(t *testing.T) {
	cfg := ClaudeConfig{
		Model:       "claude-sonnet-4-5-20250929",
		MaxTokens:   4096,
		Temperature: 0.0,
		APIKeyRef:   testSecretRef(),
	}
	a, err := NewClaudeAnalyzer(cfg, newMockSecretReader("test-key"), testPrompter(), testLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a == nil {
		t.Fatal("analyzer should not be nil")
	}
}

func TestClaudeAnalyzer_Name(t *testing.T) {
	cfg := ClaudeConfig{
		Model:     "claude-sonnet-4-5-20250929",
		MaxTokens: 4096,
		APIKeyRef: testSecretRef(),
	}
	a, err := NewClaudeAnalyzer(cfg, newMockSecretReader("test-key"), testPrompter(), testLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.Name() != "claude" {
		t.Errorf("Name() = %q, want %q", a.Name(), "claude")
	}
}

// --- Claude Analyze tests with mock HTTP server ---

func TestClaudeAnalyzer_Analyze_Success(t *testing.T) {
	llmJSON := validLLMResponseJSON()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request headers.
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", r.Header.Get("Content-Type"))
		}
		if r.Header.Get("x-api-key") != "test-api-key" {
			t.Errorf("x-api-key = %q, want test-api-key", r.Header.Get("x-api-key"))
		}
		if r.Header.Get("anthropic-version") != anthropicVersion {
			t.Errorf("anthropic-version = %q, want %q", r.Header.Get("anthropic-version"), anthropicVersion)
		}

		// Verify request body.
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("reading request body: %v", err)
		}
		var reqBody claudeRequest
		if err := json.Unmarshal(body, &reqBody); err != nil {
			t.Fatalf("parsing request body: %v", err)
		}
		if reqBody.Model != "claude-sonnet-4-5-20250929" {
			t.Errorf("request model = %q", reqBody.Model)
		}
		if reqBody.MaxTokens != 4096 {
			t.Errorf("request max_tokens = %d", reqBody.MaxTokens)
		}
		if len(reqBody.Messages) != 1 || reqBody.Messages[0].Role != "user" {
			t.Errorf("unexpected messages: %+v", reqBody.Messages)
		}
		if reqBody.System == "" {
			t.Error("system prompt should not be empty")
		}

		resp := claudeResponse{
			ID: "msg_123",
			Content: []claudeContentBlock{
				{Type: "text", Text: llmJSON},
			},
			Usage: claudeUsage{
				InputTokens:  1500,
				OutputTokens: 500,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := ClaudeConfig{
		Model:       "claude-sonnet-4-5-20250929",
		MaxTokens:   4096,
		Temperature: 0.0,
		APIKeyRef:   testSecretRef(),
		APIURL:      server.URL,
	}

	a, err := NewClaudeAnalyzer(cfg, newMockSecretReader("test-api-key"), testPrompter(), testLogger())
	if err != nil {
		t.Fatalf("NewClaudeAnalyzer() error = %v", err)
	}

	bundle := testDiagnosticBundle()
	report, err := a.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}

	if report.RootCause != "OOMKilled due to insufficient memory limits" {
		t.Errorf("RootCause = %q", report.RootCause)
	}
	if report.Severity != model.SeverityCritical {
		t.Errorf("Severity = %q, want critical", report.Severity)
	}
	if report.Category != "resources" {
		t.Errorf("Category = %q, want resources", report.Category)
	}
	if !report.Systemic {
		t.Error("Systemic should be true")
	}
	if report.Confidence != 0.95 {
		t.Errorf("Confidence = %f, want 0.95", report.Confidence)
	}
	if report.AnalyzerBackend != "claude" {
		t.Errorf("AnalyzerBackend = %q, want claude", report.AnalyzerBackend)
	}
	if report.TokensUsed.Input != 1500 {
		t.Errorf("TokensUsed.Input = %d, want 1500", report.TokensUsed.Input)
	}
	if report.TokensUsed.Output != 500 {
		t.Errorf("TokensUsed.Output = %d, want 500", report.TokensUsed.Output)
	}
	if report.FaultEventID != "fe-test-123" {
		t.Errorf("FaultEventID = %q, want fe-test-123", report.FaultEventID)
	}
	if report.RawAnalysis == "" {
		t.Error("RawAnalysis should contain the original response")
	}
	if len(report.Remediation) != 2 {
		t.Errorf("Remediation length = %d, want 2", len(report.Remediation))
	}
	if len(report.RelatedResources) != 1 {
		t.Errorf("RelatedResources length = %d, want 1", len(report.RelatedResources))
	}
}

func TestClaudeAnalyzer_Analyze_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error": {"type": "rate_limit_error", "message": "Rate limit exceeded"}}`))
	}))
	defer server.Close()

	cfg := ClaudeConfig{
		Model:     "claude-sonnet-4-5-20250929",
		MaxTokens: 4096,
		APIKeyRef: testSecretRef(),
		APIURL:    server.URL,
	}
	a, _ := NewClaudeAnalyzer(cfg, newMockSecretReader("test-key"), testPrompter(), testLogger())

	_, err := a.Analyze(context.Background(), testDiagnosticBundle())
	if err == nil {
		t.Fatal("expected error for API error response")
	}
	if !strings.Contains(err.Error(), "status 429") {
		t.Errorf("error = %q, want to contain status 429", err.Error())
	}
}

func TestClaudeAnalyzer_Analyze_APIErrorInBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := claudeResponse{
			Error: &claudeError{
				Type:    "invalid_request_error",
				Message: "max_tokens too large",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := ClaudeConfig{
		Model:     "claude-sonnet-4-5-20250929",
		MaxTokens: 4096,
		APIKeyRef: testSecretRef(),
		APIURL:    server.URL,
	}
	a, _ := NewClaudeAnalyzer(cfg, newMockSecretReader("test-key"), testPrompter(), testLogger())

	_, err := a.Analyze(context.Background(), testDiagnosticBundle())
	if err == nil {
		t.Fatal("expected error for API error in response body")
	}
	if !strings.Contains(err.Error(), "invalid_request_error") {
		t.Errorf("error = %q, want to contain error type", err.Error())
	}
}

func TestClaudeAnalyzer_Analyze_SecretReadError(t *testing.T) {
	sr := &mockSecretReader{err: errTestSecretRead}

	cfg := ClaudeConfig{
		Model:     "claude-sonnet-4-5-20250929",
		MaxTokens: 4096,
		APIKeyRef: testSecretRef(),
		APIURL:    "http://localhost:1234",
	}
	a, _ := NewClaudeAnalyzer(cfg, sr, testPrompter(), testLogger())

	_, err := a.Analyze(context.Background(), testDiagnosticBundle())
	if err == nil {
		t.Fatal("expected error when secret read fails")
	}
	if !strings.Contains(err.Error(), "reading API key") {
		t.Errorf("error = %q, want to contain 'reading API key'", err.Error())
	}
}

func TestClaudeAnalyzer_Analyze_InvalidJSON_Fallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := claudeResponse{
			ID: "msg_456",
			Content: []claudeContentBlock{
				{Type: "text", Text: "I cannot provide a valid JSON response at this time."},
			},
			Usage: claudeUsage{InputTokens: 1000, OutputTokens: 200},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := ClaudeConfig{
		Model:     "claude-sonnet-4-5-20250929",
		MaxTokens: 4096,
		APIKeyRef: testSecretRef(),
		APIURL:    server.URL,
	}
	a, _ := NewClaudeAnalyzer(cfg, newMockSecretReader("test-key"), testPrompter(), testLogger())

	report, err := a.Analyze(context.Background(), testDiagnosticBundle())
	if err != nil {
		t.Fatalf("Analyze() should not error on invalid JSON (fallback): %v", err)
	}
	if report.RootCause != "Automated analysis failed — raw diagnostics attached" {
		t.Errorf("RootCause = %q, expected fallback message", report.RootCause)
	}
	if report.Confidence != 0.0 {
		t.Errorf("Confidence = %f, want 0.0", report.Confidence)
	}
	if report.AnalyzerBackend != "claude" {
		t.Errorf("AnalyzerBackend = %q, want claude", report.AnalyzerBackend)
	}
}

func TestClaudeAnalyzer_Analyze_JSONInCodeFence(t *testing.T) {
	llmJSON := validLLMResponseJSON()
	responseText := "Here is my analysis:\n\n```json\n" + llmJSON + "\n```"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := claudeResponse{
			ID: "msg_789",
			Content: []claudeContentBlock{
				{Type: "text", Text: responseText},
			},
			Usage: claudeUsage{InputTokens: 1200, OutputTokens: 400},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := ClaudeConfig{
		Model:     "claude-sonnet-4-5-20250929",
		MaxTokens: 4096,
		APIKeyRef: testSecretRef(),
		APIURL:    server.URL,
	}
	a, _ := NewClaudeAnalyzer(cfg, newMockSecretReader("test-key"), testPrompter(), testLogger())

	report, err := a.Analyze(context.Background(), testDiagnosticBundle())
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if report.RootCause != "OOMKilled due to insufficient memory limits" {
		t.Errorf("RootCause = %q", report.RootCause)
	}
}

func TestClaudeAnalyzer_Analyze_MultipleContentBlocks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := claudeResponse{
			ID: "msg_multi",
			Content: []claudeContentBlock{
				{Type: "text", Text: `{"rootCause": "Node disk`},
				{Type: "text", Text: ` pressure", "severity": "critical", "category": "node", "systemic": true, "blastRadius": "All pods on node", "remediation": ["Expand disk"], "relatedResources": [], "confidence": 0.9}`},
			},
			Usage: claudeUsage{InputTokens: 800, OutputTokens: 300},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := ClaudeConfig{
		Model:     "claude-sonnet-4-5-20250929",
		MaxTokens: 4096,
		APIKeyRef: testSecretRef(),
		APIURL:    server.URL,
	}
	a, _ := NewClaudeAnalyzer(cfg, newMockSecretReader("test-key"), testPrompter(), testLogger())

	report, err := a.Analyze(context.Background(), testDiagnosticBundle())
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if report.RootCause != "Node disk pressure" {
		t.Errorf("RootCause = %q", report.RootCause)
	}
}

func TestClaudeAnalyzer_Analyze_CancelledContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
	}))
	defer server.Close()

	cfg := ClaudeConfig{
		Model:     "claude-sonnet-4-5-20250929",
		MaxTokens: 4096,
		APIKeyRef: testSecretRef(),
		APIURL:    server.URL,
	}
	a, _ := NewClaudeAnalyzer(cfg, newMockSecretReader("test-key"), testPrompter(), testLogger())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := a.Analyze(ctx, testDiagnosticBundle())
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestClaudeAnalyzer_Analyze_MalformedResponseJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{not valid json}`))
	}))
	defer server.Close()

	cfg := ClaudeConfig{
		Model:     "claude-sonnet-4-5-20250929",
		MaxTokens: 4096,
		APIKeyRef: testSecretRef(),
		APIURL:    server.URL,
	}
	a, _ := NewClaudeAnalyzer(cfg, newMockSecretReader("test-key"), testPrompter(), testLogger())

	_, err := a.Analyze(context.Background(), testDiagnosticBundle())
	if err == nil {
		t.Fatal("expected error for malformed response JSON")
	}
	if !strings.Contains(err.Error(), "parsing response JSON") {
		t.Errorf("error = %q, want to contain 'parsing response JSON'", err.Error())
	}
}

func TestClaudeAnalyzer_Analyze_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal error"))
	}))
	defer server.Close()

	cfg := ClaudeConfig{
		Model:     "claude-sonnet-4-5-20250929",
		MaxTokens: 4096,
		APIKeyRef: testSecretRef(),
		APIURL:    server.URL,
	}
	a, _ := NewClaudeAnalyzer(cfg, newMockSecretReader("test-key"), testPrompter(), testLogger())

	_, err := a.Analyze(context.Background(), testDiagnosticBundle())
	if err == nil {
		t.Fatal("expected error for server error response")
	}
	if !strings.Contains(err.Error(), "status 500") {
		t.Errorf("error = %q, want to contain 'status 500'", err.Error())
	}
}

// --- Claude Healthy tests ---

func TestClaudeAnalyzer_Healthy_Success(t *testing.T) {
	cfg := ClaudeConfig{
		Model:     "claude-sonnet-4-5-20250929",
		MaxTokens: 4096,
		APIKeyRef: testSecretRef(),
	}
	a, _ := NewClaudeAnalyzer(cfg, newMockSecretReader("test-key"), testPrompter(), testLogger())

	if !a.Healthy(context.Background()) {
		t.Error("Healthy() should return true when secret is readable")
	}
}

func TestClaudeAnalyzer_Healthy_SecretError(t *testing.T) {
	sr := &mockSecretReader{err: errTestSecretRead}

	cfg := ClaudeConfig{
		Model:     "claude-sonnet-4-5-20250929",
		MaxTokens: 4096,
		APIKeyRef: testSecretRef(),
	}
	a, _ := NewClaudeAnalyzer(cfg, sr, testPrompter(), testLogger())

	if a.Healthy(context.Background()) {
		t.Error("Healthy() should return false when secret read fails")
	}
}

func TestClaudeAnalyzer_CustomAPIURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := claudeResponse{
			ID: "msg_custom",
			Content: []claudeContentBlock{
				{Type: "text", Text: validLLMResponseJSON()},
			},
			Usage: claudeUsage{InputTokens: 100, OutputTokens: 50},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := ClaudeConfig{
		Model:     "claude-sonnet-4-5-20250929",
		MaxTokens: 4096,
		APIKeyRef: testSecretRef(),
		APIURL:    server.URL,
	}
	a, err := NewClaudeAnalyzer(cfg, newMockSecretReader("test-key"), testPrompter(), testLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	report, err := a.Analyze(context.Background(), testDiagnosticBundle())
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if report.RootCause == "" {
		t.Error("RootCause should not be empty")
	}
}

func TestClaudeAnalyzer_Analyze_SuperEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := claudeResponse{
			ID: "msg_se",
			Content: []claudeContentBlock{
				{Type: "text", Text: `{"rootCause": "Node went NotReady causing cascade", "severity": "critical", "category": "node", "systemic": true, "blastRadius": "All pods on node-1", "remediation": ["Check node health"], "relatedResources": [{"kind": "Node", "namespace": "", "name": "node-1"}], "confidence": 0.9}`},
			},
			Usage: claudeUsage{InputTokens: 2000, OutputTokens: 600},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := ClaudeConfig{
		Model:     "claude-sonnet-4-5-20250929",
		MaxTokens: 4096,
		APIKeyRef: testSecretRef(),
		APIURL:    server.URL,
	}
	a, _ := NewClaudeAnalyzer(cfg, newMockSecretReader("test-key"), testPrompter(), testLogger())

	bundle := model.DiagnosticBundle{
		SuperEvent: &model.SuperEvent{
			ID:              "se-test-456",
			CorrelationRule: "NodeCascade",
			Severity:        model.SeverityCritical,
			PrimaryResource: model.ResourceRef{Kind: "Node", Name: "node-1"},
			FaultEvents: []model.FaultEvent{
				{ID: "fe-1", DetectorName: "PodFailed", Severity: model.SeverityWarning, Resource: model.ResourceRef{Kind: "Pod", Namespace: "default", Name: "pod-1"}},
				{ID: "fe-2", DetectorName: "PodFailed", Severity: model.SeverityCritical, Resource: model.ResourceRef{Kind: "Pod", Namespace: "default", Name: "pod-2"}},
			},
		},
		Timestamp: time.Now().UTC(),
		Sections:  []model.DiagnosticSection{},
	}

	report, err := a.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if report.FaultEventID != "se-test-456" {
		t.Errorf("FaultEventID = %q, want se-test-456", report.FaultEventID)
	}
	if report.RootCause != "Node went NotReady causing cascade" {
		t.Errorf("RootCause = %q", report.RootCause)
	}
}

func TestClaudeAnalyzer_Analyze_TemperaturePassedThrough(t *testing.T) {
	var receivedTemp float64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req claudeRequest
		json.Unmarshal(body, &req)
		receivedTemp = req.Temperature

		resp := claudeResponse{
			ID:      "msg_temp",
			Content: []claudeContentBlock{{Type: "text", Text: validLLMResponseJSON()}},
			Usage:   claudeUsage{InputTokens: 100, OutputTokens: 50},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := ClaudeConfig{
		Model:       "claude-sonnet-4-5-20250929",
		MaxTokens:   4096,
		Temperature: 0.7,
		APIKeyRef:   testSecretRef(),
		APIURL:      server.URL,
	}
	a, _ := NewClaudeAnalyzer(cfg, newMockSecretReader("test-key"), testPrompter(), testLogger())
	a.Analyze(context.Background(), testDiagnosticBundle())

	if receivedTemp != 0.7 {
		t.Errorf("temperature sent = %f, want 0.7", receivedTemp)
	}
}
