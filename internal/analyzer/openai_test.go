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

// --- NewOpenAIAnalyzer validation tests ---

func TestNewOpenAIAnalyzer_Validation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     OpenAIConfig
		sr      SecretReader
		prompt  *prompt.Builder
		wantErr string
	}{
		{
			name:    "empty model",
			cfg:     OpenAIConfig{Model: "", MaxTokens: 4096, APIKeyRef: testSecretRef()},
			sr:      newMockSecretReader("key"),
			prompt:  testPrompter(),
			wantErr: "model must not be empty",
		},
		{
			name:    "zero maxTokens",
			cfg:     OpenAIConfig{Model: "gpt-4o", MaxTokens: 0, APIKeyRef: testSecretRef()},
			sr:      newMockSecretReader("key"),
			prompt:  testPrompter(),
			wantErr: "maxTokens must be > 0",
		},
		{
			name:    "negative maxTokens",
			cfg:     OpenAIConfig{Model: "gpt-4o", MaxTokens: -5, APIKeyRef: testSecretRef()},
			sr:      newMockSecretReader("key"),
			prompt:  testPrompter(),
			wantErr: "maxTokens must be > 0",
		},
		{
			name:    "invalid secretRef",
			cfg:     OpenAIConfig{Model: "gpt-4o", MaxTokens: 4096, APIKeyRef: SecretRef{}},
			sr:      newMockSecretReader("key"),
			prompt:  testPrompter(),
			wantErr: "apiKeyRef",
		},
		{
			name:    "nil secretReader",
			cfg:     OpenAIConfig{Model: "gpt-4o", MaxTokens: 4096, APIKeyRef: testSecretRef()},
			sr:      nil,
			prompt:  testPrompter(),
			wantErr: "secretReader must not be nil",
		},
		{
			name:    "nil prompter",
			cfg:     OpenAIConfig{Model: "gpt-4o", MaxTokens: 4096, APIKeyRef: testSecretRef()},
			sr:      newMockSecretReader("key"),
			prompt:  nil,
			wantErr: "prompter must not be nil",
		},
		{
			name:    "nil logger",
			cfg:     OpenAIConfig{Model: "gpt-4o", MaxTokens: 4096, APIKeyRef: testSecretRef()},
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
			_, err := NewOpenAIAnalyzer(tt.cfg, tt.sr, tt.prompt, logger)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestNewOpenAIAnalyzer_ValidConfig(t *testing.T) {
	cfg := OpenAIConfig{
		Model:       "gpt-4o",
		MaxTokens:   4096,
		Temperature: 0.0,
		APIKeyRef:   testSecretRef(),
	}
	a, err := NewOpenAIAnalyzer(cfg, newMockSecretReader("test-key"), testPrompter(), testLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a == nil {
		t.Fatal("analyzer should not be nil")
	}
}

func TestOpenAIAnalyzer_Name(t *testing.T) {
	cfg := OpenAIConfig{
		Model:     "gpt-4o",
		MaxTokens: 4096,
		APIKeyRef: testSecretRef(),
	}
	a, _ := NewOpenAIAnalyzer(cfg, newMockSecretReader("test-key"), testPrompter(), testLogger())
	if a.Name() != "openai" {
		t.Errorf("Name() = %q, want %q", a.Name(), "openai")
	}
}

// --- OpenAI Analyze tests with mock HTTP server ---

func TestOpenAIAnalyzer_Analyze_Success(t *testing.T) {
	llmJSON := validLLMResponseJSON()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request headers.
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", r.Header.Get("Content-Type"))
		}
		if r.Header.Get("Authorization") != "Bearer test-openai-key" {
			t.Errorf("Authorization = %q, want 'Bearer test-openai-key'", r.Header.Get("Authorization"))
		}

		// Verify request body.
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("reading request body: %v", err)
		}
		var reqBody openAIRequest
		if err := json.Unmarshal(body, &reqBody); err != nil {
			t.Fatalf("parsing request body: %v", err)
		}
		if reqBody.Model != "gpt-4o" {
			t.Errorf("request model = %q, want gpt-4o", reqBody.Model)
		}
		if reqBody.MaxTokens != 4096 {
			t.Errorf("request max_tokens = %d", reqBody.MaxTokens)
		}
		// OpenAI uses system + user messages.
		if len(reqBody.Messages) != 2 {
			t.Errorf("expected 2 messages, got %d", len(reqBody.Messages))
		}
		if reqBody.Messages[0].Role != "system" {
			t.Errorf("first message role = %q, want system", reqBody.Messages[0].Role)
		}
		if reqBody.Messages[1].Role != "user" {
			t.Errorf("second message role = %q, want user", reqBody.Messages[1].Role)
		}

		resp := openAIResponse{
			Choices: []openAIChoice{
				{Message: openAIMessage{Role: "assistant", Content: llmJSON}},
			},
			Usage: openAIUsage{
				PromptTokens:     1400,
				CompletionTokens: 450,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := OpenAIConfig{
		Model:       "gpt-4o",
		MaxTokens:   4096,
		Temperature: 0.0,
		APIKeyRef:   testSecretRef(),
		APIURL:      server.URL,
	}
	a, _ := NewOpenAIAnalyzer(cfg, newMockSecretReader("test-openai-key"), testPrompter(), testLogger())

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
	if report.AnalyzerBackend != "openai" {
		t.Errorf("AnalyzerBackend = %q, want openai", report.AnalyzerBackend)
	}
	if report.TokensUsed.Input != 1400 {
		t.Errorf("TokensUsed.Input = %d, want 1400", report.TokensUsed.Input)
	}
	if report.TokensUsed.Output != 450 {
		t.Errorf("TokensUsed.Output = %d, want 450", report.TokensUsed.Output)
	}
	if report.FaultEventID != "fe-test-123" {
		t.Errorf("FaultEventID = %q, want fe-test-123", report.FaultEventID)
	}
	if len(report.Remediation) != 2 {
		t.Errorf("Remediation length = %d, want 2", len(report.Remediation))
	}
}

func TestOpenAIAnalyzer_Analyze_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error": {"type": "rate_limit", "message": "Rate limit reached"}}`))
	}))
	defer server.Close()

	cfg := OpenAIConfig{
		Model:     "gpt-4o",
		MaxTokens: 4096,
		APIKeyRef: testSecretRef(),
		APIURL:    server.URL,
	}
	a, _ := NewOpenAIAnalyzer(cfg, newMockSecretReader("test-key"), testPrompter(), testLogger())

	_, err := a.Analyze(context.Background(), testDiagnosticBundle())
	if err == nil {
		t.Fatal("expected error for API error response")
	}
	if !strings.Contains(err.Error(), "status 429") {
		t.Errorf("error = %q, want to contain status 429", err.Error())
	}
}

func TestOpenAIAnalyzer_Analyze_APIErrorInBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openAIResponse{
			Error: &openAIError{
				Type:    "invalid_request_error",
				Message: "model not found",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := OpenAIConfig{
		Model:     "gpt-4o",
		MaxTokens: 4096,
		APIKeyRef: testSecretRef(),
		APIURL:    server.URL,
	}
	a, _ := NewOpenAIAnalyzer(cfg, newMockSecretReader("test-key"), testPrompter(), testLogger())

	_, err := a.Analyze(context.Background(), testDiagnosticBundle())
	if err == nil {
		t.Fatal("expected error for API error in body")
	}
	if !strings.Contains(err.Error(), "invalid_request_error") {
		t.Errorf("error = %q, want to contain error type", err.Error())
	}
}

func TestOpenAIAnalyzer_Analyze_EmptyChoices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openAIResponse{
			Choices: []openAIChoice{},
			Usage:   openAIUsage{PromptTokens: 100, CompletionTokens: 0},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := OpenAIConfig{
		Model:     "gpt-4o",
		MaxTokens: 4096,
		APIKeyRef: testSecretRef(),
		APIURL:    server.URL,
	}
	a, _ := NewOpenAIAnalyzer(cfg, newMockSecretReader("test-key"), testPrompter(), testLogger())

	_, err := a.Analyze(context.Background(), testDiagnosticBundle())
	if err == nil {
		t.Fatal("expected error for empty choices")
	}
	if !strings.Contains(err.Error(), "no choices") {
		t.Errorf("error = %q, want to contain 'no choices'", err.Error())
	}
}

func TestOpenAIAnalyzer_Analyze_SecretReadError(t *testing.T) {
	sr := &mockSecretReader{err: errTestSecretRead}

	cfg := OpenAIConfig{
		Model:     "gpt-4o",
		MaxTokens: 4096,
		APIKeyRef: testSecretRef(),
		APIURL:    "http://localhost:1234",
	}
	a, _ := NewOpenAIAnalyzer(cfg, sr, testPrompter(), testLogger())

	_, err := a.Analyze(context.Background(), testDiagnosticBundle())
	if err == nil {
		t.Fatal("expected error when secret read fails")
	}
	if !strings.Contains(err.Error(), "reading API key") {
		t.Errorf("error = %q, want to contain 'reading API key'", err.Error())
	}
}

func TestOpenAIAnalyzer_Analyze_InvalidJSON_Fallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openAIResponse{
			Choices: []openAIChoice{
				{Message: openAIMessage{Role: "assistant", Content: "This is not JSON at all."}},
			},
			Usage: openAIUsage{PromptTokens: 800, CompletionTokens: 100},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := OpenAIConfig{
		Model:     "gpt-4o",
		MaxTokens: 4096,
		APIKeyRef: testSecretRef(),
		APIURL:    server.URL,
	}
	a, _ := NewOpenAIAnalyzer(cfg, newMockSecretReader("test-key"), testPrompter(), testLogger())

	report, err := a.Analyze(context.Background(), testDiagnosticBundle())
	if err != nil {
		t.Fatalf("should not error on invalid JSON (fallback): %v", err)
	}
	if report.RootCause != "Automated analysis failed — raw diagnostics attached" {
		t.Errorf("RootCause = %q, expected fallback message", report.RootCause)
	}
	if report.AnalyzerBackend != "openai" {
		t.Errorf("AnalyzerBackend = %q, want openai", report.AnalyzerBackend)
	}
}

func TestOpenAIAnalyzer_Analyze_JSONInCodeFence(t *testing.T) {
	llmJSON := validLLMResponseJSON()
	responseText := "Here is the analysis:\n\n```json\n" + llmJSON + "\n```"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openAIResponse{
			Choices: []openAIChoice{
				{Message: openAIMessage{Role: "assistant", Content: responseText}},
			},
			Usage: openAIUsage{PromptTokens: 1000, CompletionTokens: 300},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := OpenAIConfig{
		Model:     "gpt-4o",
		MaxTokens: 4096,
		APIKeyRef: testSecretRef(),
		APIURL:    server.URL,
	}
	a, _ := NewOpenAIAnalyzer(cfg, newMockSecretReader("test-key"), testPrompter(), testLogger())

	report, err := a.Analyze(context.Background(), testDiagnosticBundle())
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if report.RootCause != "OOMKilled due to insufficient memory limits" {
		t.Errorf("RootCause = %q", report.RootCause)
	}
}

func TestOpenAIAnalyzer_Analyze_CancelledContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
	}))
	defer server.Close()

	cfg := OpenAIConfig{
		Model:     "gpt-4o",
		MaxTokens: 4096,
		APIKeyRef: testSecretRef(),
		APIURL:    server.URL,
	}
	a, _ := NewOpenAIAnalyzer(cfg, newMockSecretReader("test-key"), testPrompter(), testLogger())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := a.Analyze(ctx, testDiagnosticBundle())
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestOpenAIAnalyzer_Analyze_MalformedResponseJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{broken json`))
	}))
	defer server.Close()

	cfg := OpenAIConfig{
		Model:     "gpt-4o",
		MaxTokens: 4096,
		APIKeyRef: testSecretRef(),
		APIURL:    server.URL,
	}
	a, _ := NewOpenAIAnalyzer(cfg, newMockSecretReader("test-key"), testPrompter(), testLogger())

	_, err := a.Analyze(context.Background(), testDiagnosticBundle())
	if err == nil {
		t.Fatal("expected error for malformed response JSON")
	}
	if !strings.Contains(err.Error(), "parsing response JSON") {
		t.Errorf("error = %q, want to contain 'parsing response JSON'", err.Error())
	}
}

func TestOpenAIAnalyzer_Analyze_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("internal server error"))
	}))
	defer server.Close()

	cfg := OpenAIConfig{
		Model:     "gpt-4o",
		MaxTokens: 4096,
		APIKeyRef: testSecretRef(),
		APIURL:    server.URL,
	}
	a, _ := NewOpenAIAnalyzer(cfg, newMockSecretReader("test-key"), testPrompter(), testLogger())

	_, err := a.Analyze(context.Background(), testDiagnosticBundle())
	if err == nil {
		t.Fatal("expected error for server error")
	}
	if !strings.Contains(err.Error(), "status 500") {
		t.Errorf("error = %q, want to contain 'status 500'", err.Error())
	}
}

func TestOpenAIAnalyzer_Analyze_SuperEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openAIResponse{
			Choices: []openAIChoice{
				{Message: openAIMessage{Role: "assistant", Content: `{"rootCause": "Deployment rollout failure", "severity": "warning", "category": "application", "systemic": true, "blastRadius": "All pods in deployment", "remediation": ["Rollback deployment"], "relatedResources": [{"kind": "Deployment", "namespace": "prod", "name": "web-api"}], "confidence": 0.88}`}},
			},
			Usage: openAIUsage{PromptTokens: 2500, CompletionTokens: 700},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := OpenAIConfig{
		Model:     "gpt-4o",
		MaxTokens: 4096,
		APIKeyRef: testSecretRef(),
		APIURL:    server.URL,
	}
	a, _ := NewOpenAIAnalyzer(cfg, newMockSecretReader("test-key"), testPrompter(), testLogger())

	bundle := model.DiagnosticBundle{
		SuperEvent: &model.SuperEvent{
			ID:              "se-openai-1",
			CorrelationRule: "DeploymentRollout",
			Severity:        model.SeverityWarning,
			PrimaryResource: model.ResourceRef{Kind: "Deployment", Namespace: "prod", Name: "web-api"},
			FaultEvents: []model.FaultEvent{
				{ID: "fe-a", Severity: model.SeverityWarning},
				{ID: "fe-b", Severity: model.SeverityWarning},
			},
		},
		Timestamp: time.Now().UTC(),
	}

	report, err := a.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if report.FaultEventID != "se-openai-1" {
		t.Errorf("FaultEventID = %q, want se-openai-1", report.FaultEventID)
	}
	if report.RootCause != "Deployment rollout failure" {
		t.Errorf("RootCause = %q", report.RootCause)
	}
}

func TestOpenAIAnalyzer_Analyze_TemperaturePassedThrough(t *testing.T) {
	var receivedTemp float64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req openAIRequest
		json.Unmarshal(body, &req)
		receivedTemp = req.Temperature

		resp := openAIResponse{
			Choices: []openAIChoice{
				{Message: openAIMessage{Role: "assistant", Content: validLLMResponseJSON()}},
			},
			Usage: openAIUsage{PromptTokens: 100, CompletionTokens: 50},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := OpenAIConfig{
		Model:       "gpt-4o",
		MaxTokens:   4096,
		Temperature: 0.3,
		APIKeyRef:   testSecretRef(),
		APIURL:      server.URL,
	}
	a, _ := NewOpenAIAnalyzer(cfg, newMockSecretReader("test-key"), testPrompter(), testLogger())
	a.Analyze(context.Background(), testDiagnosticBundle())

	if receivedTemp != 0.3 {
		t.Errorf("temperature sent = %f, want 0.3", receivedTemp)
	}
}

// --- OpenAI Healthy tests ---

func TestOpenAIAnalyzer_Healthy_Success(t *testing.T) {
	cfg := OpenAIConfig{
		Model:     "gpt-4o",
		MaxTokens: 4096,
		APIKeyRef: testSecretRef(),
	}
	a, _ := NewOpenAIAnalyzer(cfg, newMockSecretReader("test-key"), testPrompter(), testLogger())

	if !a.Healthy(context.Background()) {
		t.Error("Healthy() should return true when secret is readable")
	}
}

func TestOpenAIAnalyzer_Healthy_SecretError(t *testing.T) {
	sr := &mockSecretReader{err: errTestSecretRead}

	cfg := OpenAIConfig{
		Model:     "gpt-4o",
		MaxTokens: 4096,
		APIKeyRef: testSecretRef(),
	}
	a, _ := NewOpenAIAnalyzer(cfg, sr, testPrompter(), testLogger())

	if a.Healthy(context.Background()) {
		t.Error("Healthy() should return false when secret read fails")
	}
}
