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

// --- NewAzureOpenAIAnalyzer validation tests ---

func TestNewAzureOpenAIAnalyzer_Validation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     AzureOpenAIConfig
		sr      SecretReader
		prompt  *prompt.Builder
		wantErr string
	}{
		{
			name:    "empty endpoint",
			cfg:     AzureOpenAIConfig{Endpoint: "", DeploymentName: "gpt-4o", APIKeyRef: testSecretRef()},
			sr:      newMockSecretReader("key"),
			prompt:  testPrompter(),
			wantErr: "endpoint must not be empty",
		},
		{
			name:    "empty deploymentName",
			cfg:     AzureOpenAIConfig{Endpoint: "https://mycompany.openai.azure.com", DeploymentName: "", APIKeyRef: testSecretRef()},
			sr:      newMockSecretReader("key"),
			prompt:  testPrompter(),
			wantErr: "deploymentName must not be empty",
		},
		{
			name:    "invalid secretRef",
			cfg:     AzureOpenAIConfig{Endpoint: "https://mycompany.openai.azure.com", DeploymentName: "gpt-4o", APIKeyRef: SecretRef{}},
			sr:      newMockSecretReader("key"),
			prompt:  testPrompter(),
			wantErr: "apiKeyRef",
		},
		{
			name:    "nil secretReader",
			cfg:     AzureOpenAIConfig{Endpoint: "https://mycompany.openai.azure.com", DeploymentName: "gpt-4o", APIKeyRef: testSecretRef()},
			sr:      nil,
			prompt:  testPrompter(),
			wantErr: "secretReader must not be nil",
		},
		{
			name:    "nil prompter",
			cfg:     AzureOpenAIConfig{Endpoint: "https://mycompany.openai.azure.com", DeploymentName: "gpt-4o", APIKeyRef: testSecretRef()},
			sr:      newMockSecretReader("key"),
			prompt:  nil,
			wantErr: "prompter must not be nil",
		},
		{
			name:    "nil logger",
			cfg:     AzureOpenAIConfig{Endpoint: "https://mycompany.openai.azure.com", DeploymentName: "gpt-4o", APIKeyRef: testSecretRef()},
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
			_, err := NewAzureOpenAIAnalyzer(tt.cfg, tt.sr, tt.prompt, logger)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestNewAzureOpenAIAnalyzer_ValidConfig(t *testing.T) {
	cfg := AzureOpenAIConfig{
		Endpoint:       "https://mycompany.openai.azure.com",
		DeploymentName: "gpt-4o",
		APIKeyRef:      testSecretRef(),
	}
	a, err := NewAzureOpenAIAnalyzer(cfg, newMockSecretReader("test-key"), testPrompter(), testLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a == nil {
		t.Fatal("analyzer should not be nil")
	}
}

func TestAzureOpenAIAnalyzer_Name(t *testing.T) {
	cfg := AzureOpenAIConfig{
		Endpoint:       "https://mycompany.openai.azure.com",
		DeploymentName: "gpt-4o",
		APIKeyRef:      testSecretRef(),
	}
	a, _ := NewAzureOpenAIAnalyzer(cfg, newMockSecretReader("test-key"), testPrompter(), testLogger())
	if a.Name() != "azure-openai" {
		t.Errorf("Name() = %q, want %q", a.Name(), "azure-openai")
	}
}

// --- Azure OpenAI URL construction tests ---

func TestAzureOpenAIAnalyzer_BuildURL(t *testing.T) {
	cfg := AzureOpenAIConfig{
		Endpoint:       "https://mycompany.openai.azure.com",
		DeploymentName: "gpt-4o",
		APIKeyRef:      testSecretRef(),
	}
	a, _ := NewAzureOpenAIAnalyzer(cfg, newMockSecretReader("test-key"), testPrompter(), testLogger())

	url := a.buildURL()
	expectedURL := "https://mycompany.openai.azure.com/openai/deployments/gpt-4o/chat/completions?api-version=" + azureOpenAIAPIVersion
	if url != expectedURL {
		t.Errorf("buildURL() = %q, want %q", url, expectedURL)
	}
}

func TestAzureOpenAIAnalyzer_BuildURL_TrailingSlash(t *testing.T) {
	cfg := AzureOpenAIConfig{
		Endpoint:       "https://mycompany.openai.azure.com/",
		DeploymentName: "gpt-4o",
		APIKeyRef:      testSecretRef(),
	}
	a, _ := NewAzureOpenAIAnalyzer(cfg, newMockSecretReader("test-key"), testPrompter(), testLogger())

	url := a.buildURL()
	// Trailing slash should be stripped.
	if strings.Contains(url, ".com//") {
		t.Errorf("buildURL() has double slash: %q", url)
	}
}

func TestAzureOpenAIAnalyzer_BuildURL_Override(t *testing.T) {
	cfg := AzureOpenAIConfig{
		Endpoint:       "https://mycompany.openai.azure.com",
		DeploymentName: "gpt-4o",
		APIKeyRef:      testSecretRef(),
		APIURL:         "http://localhost:8080/custom",
	}
	a, _ := NewAzureOpenAIAnalyzer(cfg, newMockSecretReader("test-key"), testPrompter(), testLogger())

	url := a.buildURL()
	if url != "http://localhost:8080/custom" {
		t.Errorf("buildURL() = %q, want override URL", url)
	}
}

// --- Azure OpenAI Analyze tests with mock HTTP server ---

func TestAzureOpenAIAnalyzer_Analyze_Success(t *testing.T) {
	llmJSON := validLLMResponseJSON()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request headers.
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", r.Header.Get("Content-Type"))
		}
		if r.Header.Get("api-key") != "test-azure-key" {
			t.Errorf("api-key = %q, want test-azure-key", r.Header.Get("api-key"))
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
		// Azure doesn't use model field.
		if reqBody.Model != "" {
			t.Errorf("request model should be empty for Azure, got %q", reqBody.Model)
		}
		if reqBody.MaxTokens != 4096 {
			t.Errorf("request max_tokens = %d, want 4096", reqBody.MaxTokens)
		}
		if reqBody.Temperature != 0.0 {
			t.Errorf("request temperature = %f, want 0.0", reqBody.Temperature)
		}
		// Should have system + user messages.
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
				PromptTokens:     1600,
				CompletionTokens: 550,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := AzureOpenAIConfig{
		Endpoint:       "https://mycompany.openai.azure.com",
		DeploymentName: "gpt-4o",
		APIKeyRef:      testSecretRef(),
		APIURL:         server.URL,
	}
	a, _ := NewAzureOpenAIAnalyzer(cfg, newMockSecretReader("test-azure-key"), testPrompter(), testLogger())

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
	if report.AnalyzerBackend != "azure-openai" {
		t.Errorf("AnalyzerBackend = %q, want azure-openai", report.AnalyzerBackend)
	}
	if report.TokensUsed.Input != 1600 {
		t.Errorf("TokensUsed.Input = %d, want 1600", report.TokensUsed.Input)
	}
	if report.TokensUsed.Output != 550 {
		t.Errorf("TokensUsed.Output = %d, want 550", report.TokensUsed.Output)
	}
	if report.FaultEventID != "fe-test-123" {
		t.Errorf("FaultEventID = %q, want fe-test-123", report.FaultEventID)
	}
}

func TestAzureOpenAIAnalyzer_Analyze_APIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error": {"type": "auth_error", "message": "Invalid API key"}}`))
	}))
	defer server.Close()

	cfg := AzureOpenAIConfig{
		Endpoint:       "https://mycompany.openai.azure.com",
		DeploymentName: "gpt-4o",
		APIKeyRef:      testSecretRef(),
		APIURL:         server.URL,
	}
	a, _ := NewAzureOpenAIAnalyzer(cfg, newMockSecretReader("bad-key"), testPrompter(), testLogger())

	_, err := a.Analyze(context.Background(), testDiagnosticBundle())
	if err == nil {
		t.Fatal("expected error for API error response")
	}
	if !strings.Contains(err.Error(), "status 401") {
		t.Errorf("error = %q, want to contain status 401", err.Error())
	}
}

func TestAzureOpenAIAnalyzer_Analyze_APIErrorInBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openAIResponse{
			Error: &openAIError{
				Type:    "deployment_not_found",
				Message: "deployment does not exist",
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := AzureOpenAIConfig{
		Endpoint:       "https://mycompany.openai.azure.com",
		DeploymentName: "gpt-4o",
		APIKeyRef:      testSecretRef(),
		APIURL:         server.URL,
	}
	a, _ := NewAzureOpenAIAnalyzer(cfg, newMockSecretReader("test-key"), testPrompter(), testLogger())

	_, err := a.Analyze(context.Background(), testDiagnosticBundle())
	if err == nil {
		t.Fatal("expected error for API error in body")
	}
	if !strings.Contains(err.Error(), "deployment_not_found") {
		t.Errorf("error = %q, want to contain error type", err.Error())
	}
}

func TestAzureOpenAIAnalyzer_Analyze_EmptyChoices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openAIResponse{
			Choices: []openAIChoice{},
			Usage:   openAIUsage{PromptTokens: 100, CompletionTokens: 0},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := AzureOpenAIConfig{
		Endpoint:       "https://mycompany.openai.azure.com",
		DeploymentName: "gpt-4o",
		APIKeyRef:      testSecretRef(),
		APIURL:         server.URL,
	}
	a, _ := NewAzureOpenAIAnalyzer(cfg, newMockSecretReader("test-key"), testPrompter(), testLogger())

	_, err := a.Analyze(context.Background(), testDiagnosticBundle())
	if err == nil {
		t.Fatal("expected error for empty choices")
	}
	if !strings.Contains(err.Error(), "no choices") {
		t.Errorf("error = %q, want to contain 'no choices'", err.Error())
	}
}

func TestAzureOpenAIAnalyzer_Analyze_SecretReadError(t *testing.T) {
	sr := &mockSecretReader{err: errTestSecretRead}

	cfg := AzureOpenAIConfig{
		Endpoint:       "https://mycompany.openai.azure.com",
		DeploymentName: "gpt-4o",
		APIKeyRef:      testSecretRef(),
		APIURL:         "http://localhost:1234",
	}
	a, _ := NewAzureOpenAIAnalyzer(cfg, sr, testPrompter(), testLogger())

	_, err := a.Analyze(context.Background(), testDiagnosticBundle())
	if err == nil {
		t.Fatal("expected error when secret read fails")
	}
	if !strings.Contains(err.Error(), "reading API key") {
		t.Errorf("error = %q, want to contain 'reading API key'", err.Error())
	}
}

func TestAzureOpenAIAnalyzer_Analyze_InvalidJSON_Fallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openAIResponse{
			Choices: []openAIChoice{
				{Message: openAIMessage{Role: "assistant", Content: "Not valid JSON output."}},
			},
			Usage: openAIUsage{PromptTokens: 500, CompletionTokens: 50},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := AzureOpenAIConfig{
		Endpoint:       "https://mycompany.openai.azure.com",
		DeploymentName: "gpt-4o",
		APIKeyRef:      testSecretRef(),
		APIURL:         server.URL,
	}
	a, _ := NewAzureOpenAIAnalyzer(cfg, newMockSecretReader("test-key"), testPrompter(), testLogger())

	report, err := a.Analyze(context.Background(), testDiagnosticBundle())
	if err != nil {
		t.Fatalf("should not error on invalid JSON (fallback): %v", err)
	}
	if report.RootCause != "Automated analysis failed — raw diagnostics attached" {
		t.Errorf("RootCause = %q, expected fallback", report.RootCause)
	}
	if report.AnalyzerBackend != "azure-openai" {
		t.Errorf("AnalyzerBackend = %q, want azure-openai", report.AnalyzerBackend)
	}
}

func TestAzureOpenAIAnalyzer_Analyze_CancelledContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(5 * time.Second)
	}))
	defer server.Close()

	cfg := AzureOpenAIConfig{
		Endpoint:       "https://mycompany.openai.azure.com",
		DeploymentName: "gpt-4o",
		APIKeyRef:      testSecretRef(),
		APIURL:         server.URL,
	}
	a, _ := NewAzureOpenAIAnalyzer(cfg, newMockSecretReader("test-key"), testPrompter(), testLogger())

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := a.Analyze(ctx, testDiagnosticBundle())
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestAzureOpenAIAnalyzer_Analyze_MalformedResponseJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{invalid`))
	}))
	defer server.Close()

	cfg := AzureOpenAIConfig{
		Endpoint:       "https://mycompany.openai.azure.com",
		DeploymentName: "gpt-4o",
		APIKeyRef:      testSecretRef(),
		APIURL:         server.URL,
	}
	a, _ := NewAzureOpenAIAnalyzer(cfg, newMockSecretReader("test-key"), testPrompter(), testLogger())

	_, err := a.Analyze(context.Background(), testDiagnosticBundle())
	if err == nil {
		t.Fatal("expected error for malformed response JSON")
	}
	if !strings.Contains(err.Error(), "parsing response JSON") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestAzureOpenAIAnalyzer_Analyze_SuperEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openAIResponse{
			Choices: []openAIChoice{
				{Message: openAIMessage{Role: "assistant", Content: `{"rootCause": "PVC stuck in Pending causing pod failures", "severity": "critical", "category": "storage", "systemic": true, "blastRadius": "All pods referencing PVC data-vol", "remediation": ["Check storage class", "Verify EBS quota"], "relatedResources": [{"kind": "PersistentVolumeClaim", "namespace": "prod", "name": "data-vol"}], "confidence": 0.92}`}},
			},
			Usage: openAIUsage{PromptTokens: 3000, CompletionTokens: 800},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := AzureOpenAIConfig{
		Endpoint:       "https://mycompany.openai.azure.com",
		DeploymentName: "gpt-4o",
		APIKeyRef:      testSecretRef(),
		APIURL:         server.URL,
	}
	a, _ := NewAzureOpenAIAnalyzer(cfg, newMockSecretReader("test-key"), testPrompter(), testLogger())

	bundle := model.DiagnosticBundle{
		SuperEvent: &model.SuperEvent{
			ID:              "se-azure-1",
			CorrelationRule: "StorageCascade",
			Severity:        model.SeverityCritical,
			PrimaryResource: model.ResourceRef{Kind: "PersistentVolumeClaim", Namespace: "prod", Name: "data-vol"},
			FaultEvents: []model.FaultEvent{
				{ID: "fe-x", Severity: model.SeverityCritical},
			},
		},
		Timestamp: time.Now().UTC(),
	}

	report, err := a.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if report.FaultEventID != "se-azure-1" {
		t.Errorf("FaultEventID = %q, want se-azure-1", report.FaultEventID)
	}
	if report.RootCause != "PVC stuck in Pending causing pod failures" {
		t.Errorf("RootCause = %q", report.RootCause)
	}
	if report.Category != "storage" {
		t.Errorf("Category = %q, want storage", report.Category)
	}
}

func TestAzureOpenAIAnalyzer_Analyze_JSONInCodeFence(t *testing.T) {
	llmJSON := validLLMResponseJSON()
	responseText := "Analysis result:\n```json\n" + llmJSON + "\n```"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := openAIResponse{
			Choices: []openAIChoice{
				{Message: openAIMessage{Role: "assistant", Content: responseText}},
			},
			Usage: openAIUsage{PromptTokens: 900, CompletionTokens: 250},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := AzureOpenAIConfig{
		Endpoint:       "https://mycompany.openai.azure.com",
		DeploymentName: "gpt-4o",
		APIKeyRef:      testSecretRef(),
		APIURL:         server.URL,
	}
	a, _ := NewAzureOpenAIAnalyzer(cfg, newMockSecretReader("test-key"), testPrompter(), testLogger())

	report, err := a.Analyze(context.Background(), testDiagnosticBundle())
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if report.RootCause != "OOMKilled due to insufficient memory limits" {
		t.Errorf("RootCause = %q", report.RootCause)
	}
}

func TestAzureOpenAIAnalyzer_Analyze_ConfigPassedThrough(t *testing.T) {
	var receivedTemp float64
	var receivedMaxTokens int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req openAIRequest
		json.Unmarshal(body, &req)
		receivedTemp = req.Temperature
		receivedMaxTokens = req.MaxTokens

		resp := openAIResponse{
			Choices: []openAIChoice{
				{Message: openAIMessage{Role: "assistant", Content: validLLMResponseJSON()}},
			},
			Usage: openAIUsage{PromptTokens: 100, CompletionTokens: 50},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	cfg := AzureOpenAIConfig{
		Endpoint:       "https://mycompany.openai.azure.com",
		DeploymentName: "gpt-4o",
		MaxTokens:      8192,
		Temperature:    0.7,
		APIKeyRef:      testSecretRef(),
		APIURL:         server.URL,
	}
	a, _ := NewAzureOpenAIAnalyzer(cfg, newMockSecretReader("test-key"), testPrompter(), testLogger())
	a.Analyze(context.Background(), testDiagnosticBundle())

	if receivedTemp != 0.7 {
		t.Errorf("temperature sent = %f, want 0.7", receivedTemp)
	}
	if receivedMaxTokens != 8192 {
		t.Errorf("maxTokens sent = %d, want 8192", receivedMaxTokens)
	}
}

func TestAzureOpenAIAnalyzer_MaxTokensDefault(t *testing.T) {
	var receivedMaxTokens int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req openAIRequest
		json.Unmarshal(body, &req)
		receivedMaxTokens = req.MaxTokens

		resp := openAIResponse{
			Choices: []openAIChoice{
				{Message: openAIMessage{Role: "assistant", Content: validLLMResponseJSON()}},
			},
			Usage: openAIUsage{PromptTokens: 100, CompletionTokens: 50},
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	// MaxTokens not set (zero value) should default to 4096.
	cfg := AzureOpenAIConfig{
		Endpoint:       "https://mycompany.openai.azure.com",
		DeploymentName: "gpt-4o",
		APIKeyRef:      testSecretRef(),
		APIURL:         server.URL,
	}
	a, _ := NewAzureOpenAIAnalyzer(cfg, newMockSecretReader("test-key"), testPrompter(), testLogger())
	a.Analyze(context.Background(), testDiagnosticBundle())

	if receivedMaxTokens != 4096 {
		t.Errorf("maxTokens sent = %d, want 4096 (default)", receivedMaxTokens)
	}
}

// --- Azure OpenAI Healthy tests ---

func TestAzureOpenAIAnalyzer_Healthy_Success(t *testing.T) {
	cfg := AzureOpenAIConfig{
		Endpoint:       "https://mycompany.openai.azure.com",
		DeploymentName: "gpt-4o",
		APIKeyRef:      testSecretRef(),
	}
	a, _ := NewAzureOpenAIAnalyzer(cfg, newMockSecretReader("test-key"), testPrompter(), testLogger())

	if !a.Healthy(context.Background()) {
		t.Error("Healthy() should return true when secret is readable")
	}
}

func TestAzureOpenAIAnalyzer_Healthy_SecretError(t *testing.T) {
	sr := &mockSecretReader{err: errTestSecretRead}

	cfg := AzureOpenAIConfig{
		Endpoint:       "https://mycompany.openai.azure.com",
		DeploymentName: "gpt-4o",
		APIKeyRef:      testSecretRef(),
	}
	a, _ := NewAzureOpenAIAnalyzer(cfg, sr, testPrompter(), testLogger())

	if a.Healthy(context.Background()) {
		t.Error("Healthy() should return false when secret read fails")
	}
}
