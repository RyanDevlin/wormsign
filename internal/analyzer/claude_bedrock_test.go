package analyzer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"

	"github.com/k8s-wormsign/k8s-wormsign/internal/analyzer/prompt"
	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

// mockBedrockClient implements BedrockClient for testing.
type mockBedrockClient struct {
	output *bedrockruntime.InvokeModelOutput
	err    error
	// captures for assertion
	lastInput *bedrockruntime.InvokeModelInput
}

func (m *mockBedrockClient) InvokeModel(ctx context.Context, params *bedrockruntime.InvokeModelInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.InvokeModelOutput, error) {
	m.lastInput = params
	if m.err != nil {
		return nil, m.err
	}
	return m.output, nil
}

func bedrockSuccessOutput(llmJSON string) *bedrockruntime.InvokeModelOutput {
	resp := bedrockAnthropicResponse{
		Content: []claudeContentBlock{
			{Type: "text", Text: llmJSON},
		},
		Usage: claudeUsage{
			InputTokens:  1800,
			OutputTokens: 600,
		},
	}
	body, _ := json.Marshal(resp)
	return &bedrockruntime.InvokeModelOutput{Body: body}
}

func testBedrockConfig() BedrockConfig {
	return BedrockConfig{
		Region:    "us-east-1",
		ModelID:   "anthropic.claude-sonnet-4-5-20250929-v1:0",
		MaxTokens: 4096,
	}
}

// --- NewBedrockAnalyzer via newBedrockAnalyzerWithClient validation ---

func TestNewBedrockAnalyzerWithClient_Validation(t *testing.T) {
	client := &mockBedrockClient{}
	p := testPrompter()
	l := testLogger()

	tests := []struct {
		name    string
		client  BedrockClient
		cfg     BedrockConfig
		prompt  *prompt.Builder
		wantErr string
	}{
		{
			name:    "nil client",
			client:  nil,
			cfg:     testBedrockConfig(),
			prompt:  p,
			wantErr: "client must not be nil",
		},
		{
			name:   "empty modelID",
			client: client,
			cfg: BedrockConfig{
				Region:    "us-east-1",
				ModelID:   "",
				MaxTokens: 4096,
			},
			prompt:  p,
			wantErr: "modelID must not be empty",
		},
		{
			name:   "zero maxTokens",
			client: client,
			cfg: BedrockConfig{
				Region:    "us-east-1",
				ModelID:   "anthropic.claude-sonnet-4-5-20250929-v1:0",
				MaxTokens: 0,
			},
			prompt:  p,
			wantErr: "maxTokens must be > 0",
		},
		{
			name:    "nil prompter",
			client:  client,
			cfg:     testBedrockConfig(),
			prompt:  nil,
			wantErr: "prompter must not be nil",
		},
		{
			name:    "nil logger",
			client:  client,
			cfg:     testBedrockConfig(),
			prompt:  p,
			wantErr: "logger must not be nil",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := l
			if tt.name == "nil logger" {
				logger = nil
			}
			_, err := newBedrockAnalyzerWithClient(tt.client, tt.cfg, tt.prompt, logger)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestNewBedrockAnalyzer_RegionValidation(t *testing.T) {
	_, err := NewBedrockAnalyzer(context.Background(), BedrockConfig{Region: "", ModelID: "model", MaxTokens: 4096}, testPrompter(), testLogger())
	if err == nil {
		t.Fatal("expected error for empty region")
	}
	if !strings.Contains(err.Error(), "region must not be empty") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestNewBedrockAnalyzer_ModelIDValidation(t *testing.T) {
	_, err := NewBedrockAnalyzer(context.Background(), BedrockConfig{Region: "us-east-1", ModelID: "", MaxTokens: 4096}, testPrompter(), testLogger())
	if err == nil {
		t.Fatal("expected error for empty modelID")
	}
	if !strings.Contains(err.Error(), "modelID must not be empty") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestNewBedrockAnalyzer_MaxTokensValidation(t *testing.T) {
	_, err := NewBedrockAnalyzer(context.Background(), BedrockConfig{Region: "us-east-1", ModelID: "model", MaxTokens: 0}, testPrompter(), testLogger())
	if err == nil {
		t.Fatal("expected error for zero maxTokens")
	}
	if !strings.Contains(err.Error(), "maxTokens must be > 0") {
		t.Errorf("error = %q", err.Error())
	}
}

func TestBedrockAnalyzer_Name(t *testing.T) {
	client := &mockBedrockClient{}
	a, err := newBedrockAnalyzerWithClient(client, testBedrockConfig(), testPrompter(), testLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.Name() != "claude-bedrock" {
		t.Errorf("Name() = %q, want %q", a.Name(), "claude-bedrock")
	}
}

// --- Bedrock Analyze tests ---

func TestBedrockAnalyzer_Analyze_Success(t *testing.T) {
	llmJSON := validLLMResponseJSON()
	client := &mockBedrockClient{output: bedrockSuccessOutput(llmJSON)}

	a, err := newBedrockAnalyzerWithClient(client, testBedrockConfig(), testPrompter(), testLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	bundle := testDiagnosticBundle()
	report, err := a.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}

	// Verify report content.
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
	if report.AnalyzerBackend != "claude-bedrock" {
		t.Errorf("AnalyzerBackend = %q, want claude-bedrock", report.AnalyzerBackend)
	}
	if report.TokensUsed.Input != 1800 {
		t.Errorf("TokensUsed.Input = %d, want 1800", report.TokensUsed.Input)
	}
	if report.TokensUsed.Output != 600 {
		t.Errorf("TokensUsed.Output = %d, want 600", report.TokensUsed.Output)
	}
	if report.FaultEventID != "fe-test-123" {
		t.Errorf("FaultEventID = %q, want fe-test-123", report.FaultEventID)
	}

	// Verify the request was properly formed.
	if client.lastInput == nil {
		t.Fatal("no input captured")
	}
	if *client.lastInput.ModelId != "anthropic.claude-sonnet-4-5-20250929-v1:0" {
		t.Errorf("ModelId = %q", *client.lastInput.ModelId)
	}
	if *client.lastInput.ContentType != "application/json" {
		t.Errorf("ContentType = %q", *client.lastInput.ContentType)
	}

	// Verify request body format.
	var reqBody bedrockAnthropicRequest
	if err := json.Unmarshal(client.lastInput.Body, &reqBody); err != nil {
		t.Fatalf("parsing request body: %v", err)
	}
	if reqBody.AnthropicVersion != anthropicVersion {
		t.Errorf("anthropic_version = %q, want %q", reqBody.AnthropicVersion, anthropicVersion)
	}
	if reqBody.MaxTokens != 4096 {
		t.Errorf("max_tokens = %d, want 4096", reqBody.MaxTokens)
	}
	if reqBody.Temperature != 0.0 {
		t.Errorf("temperature = %f, want 0.0", reqBody.Temperature)
	}
	if reqBody.System == "" {
		t.Error("system prompt should not be empty")
	}
	if len(reqBody.Messages) != 1 || reqBody.Messages[0].Role != "user" {
		t.Errorf("unexpected messages: %+v", reqBody.Messages)
	}
}

func TestBedrockAnalyzer_Analyze_TemperaturePassedThrough(t *testing.T) {
	llmJSON := validLLMResponseJSON()
	client := &mockBedrockClient{output: bedrockSuccessOutput(llmJSON)}

	cfg := BedrockConfig{
		Region:      "us-east-1",
		ModelID:     "anthropic.claude-sonnet-4-5-20250929-v1:0",
		MaxTokens:   8192,
		Temperature: 0.5,
	}
	a, err := newBedrockAnalyzerWithClient(client, cfg, testPrompter(), testLogger())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, err = a.Analyze(context.Background(), testDiagnosticBundle())
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}

	// Verify request body uses configured values.
	var reqBody bedrockAnthropicRequest
	if err := json.Unmarshal(client.lastInput.Body, &reqBody); err != nil {
		t.Fatalf("parsing request body: %v", err)
	}
	if reqBody.MaxTokens != 8192 {
		t.Errorf("max_tokens = %d, want 8192", reqBody.MaxTokens)
	}
	if reqBody.Temperature != 0.5 {
		t.Errorf("temperature = %f, want 0.5", reqBody.Temperature)
	}
}

func TestBedrockAnalyzer_Analyze_InvokeError(t *testing.T) {
	client := &mockBedrockClient{err: fmt.Errorf("access denied: insufficient permissions")}
	a, _ := newBedrockAnalyzerWithClient(client, testBedrockConfig(), testPrompter(), testLogger())

	_, err := a.Analyze(context.Background(), testDiagnosticBundle())
	if err == nil {
		t.Fatal("expected error for invoke failure")
	}
	if !strings.Contains(err.Error(), "invoking model") {
		t.Errorf("error = %q, want to contain 'invoking model'", err.Error())
	}
}

func TestBedrockAnalyzer_Analyze_MalformedResponse(t *testing.T) {
	client := &mockBedrockClient{
		output: &bedrockruntime.InvokeModelOutput{
			Body: []byte(`{not valid json`),
		},
	}
	a, _ := newBedrockAnalyzerWithClient(client, testBedrockConfig(), testPrompter(), testLogger())

	_, err := a.Analyze(context.Background(), testDiagnosticBundle())
	if err == nil {
		t.Fatal("expected error for malformed response")
	}
	if !strings.Contains(err.Error(), "parsing response JSON") {
		t.Errorf("error = %q, want to contain 'parsing response JSON'", err.Error())
	}
}

func TestBedrockAnalyzer_Analyze_InvalidLLMResponse_Fallback(t *testing.T) {
	// Response is valid API response but LLM content is not valid JSON.
	resp := bedrockAnthropicResponse{
		Content: []claudeContentBlock{
			{Type: "text", Text: "I'm sorry, I can't help with that."},
		},
		Usage: claudeUsage{InputTokens: 500, OutputTokens: 100},
	}
	body, _ := json.Marshal(resp)
	client := &mockBedrockClient{
		output: &bedrockruntime.InvokeModelOutput{Body: body},
	}
	a, _ := newBedrockAnalyzerWithClient(client, testBedrockConfig(), testPrompter(), testLogger())

	report, err := a.Analyze(context.Background(), testDiagnosticBundle())
	if err != nil {
		t.Fatalf("should not error on invalid LLM response (fallback): %v", err)
	}
	if report.RootCause != "Automated analysis failed — raw diagnostics attached" {
		t.Errorf("RootCause = %q, expected fallback", report.RootCause)
	}
	if report.AnalyzerBackend != "claude-bedrock" {
		t.Errorf("AnalyzerBackend = %q, want claude-bedrock", report.AnalyzerBackend)
	}
}

func TestBedrockAnalyzer_Analyze_SuperEvent(t *testing.T) {
	llmJSON := `{"rootCause": "Node cascade failure", "severity": "critical", "category": "node", "systemic": true, "blastRadius": "All pods on node", "remediation": ["Investigate node"], "relatedResources": [], "confidence": 0.85}`
	client := &mockBedrockClient{output: bedrockSuccessOutput(llmJSON)}
	a, _ := newBedrockAnalyzerWithClient(client, testBedrockConfig(), testPrompter(), testLogger())

	bundle := model.DiagnosticBundle{
		SuperEvent: &model.SuperEvent{
			ID:              "se-bedrock-1",
			CorrelationRule: "NodeCascade",
			Severity:        model.SeverityCritical,
			PrimaryResource: model.ResourceRef{Kind: "Node", Name: "node-1"},
			FaultEvents: []model.FaultEvent{
				{ID: "fe-1", DetectorName: "PodFailed", Severity: model.SeverityWarning, Resource: model.ResourceRef{Kind: "Pod", Namespace: "default", Name: "pod-1"}},
			},
		},
		Timestamp: time.Now().UTC(),
	}

	report, err := a.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if report.FaultEventID != "se-bedrock-1" {
		t.Errorf("FaultEventID = %q, want se-bedrock-1", report.FaultEventID)
	}
}

func TestBedrockAnalyzer_Analyze_MultipleContentBlocks(t *testing.T) {
	resp := bedrockAnthropicResponse{
		Content: []claudeContentBlock{
			{Type: "text", Text: `{"rootCause": "Taint`},
			{Type: "text", Text: ` mismatch", "severity": "warning", "category": "scheduling", "systemic": false, "blastRadius": "", "remediation": ["Add toleration"], "relatedResources": [], "confidence": 0.8}`},
		},
		Usage: claudeUsage{InputTokens: 400, OutputTokens: 200},
	}
	body, _ := json.Marshal(resp)
	client := &mockBedrockClient{
		output: &bedrockruntime.InvokeModelOutput{Body: body},
	}
	a, _ := newBedrockAnalyzerWithClient(client, testBedrockConfig(), testPrompter(), testLogger())

	report, err := a.Analyze(context.Background(), testDiagnosticBundle())
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if report.RootCause != "Taint mismatch" {
		t.Errorf("RootCause = %q", report.RootCause)
	}
}

// --- Bedrock Healthy tests ---

func TestBedrockAnalyzer_Healthy_WithClient(t *testing.T) {
	client := &mockBedrockClient{}
	a, _ := newBedrockAnalyzerWithClient(client, testBedrockConfig(), testPrompter(), testLogger())

	if !a.Healthy(context.Background()) {
		t.Error("Healthy() should return true when client is configured")
	}
}

func TestBedrockAnalyzer_Healthy_NilClient(t *testing.T) {
	// Directly set client to nil to simulate misconfiguration.
	a := &BedrockAnalyzer{
		client:   nil,
		modelID:  "test",
		prompter: testPrompter(),
		logger:   testLogger(),
	}
	if a.Healthy(context.Background()) {
		t.Error("Healthy() should return false when client is nil")
	}
}
