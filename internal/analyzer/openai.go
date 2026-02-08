// Package analyzer — openai.go implements the OpenAI Chat Completions API
// analyzer backend. See Section 5.3.2.
package analyzer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/k8s-wormsign/k8s-wormsign/internal/analyzer/prompt"
	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

const (
	defaultOpenAIURL      = "https://api.openai.com/v1/chat/completions"
	openAIHTTPTimeout     = 120 * time.Second
)

// OpenAIAnalyzer uses the OpenAI Chat Completions API to analyze diagnostic bundles.
type OpenAIAnalyzer struct {
	apiURL       string
	model        string
	maxTokens    int
	temperature  float64
	secretRef    SecretRef
	secretReader SecretReader
	httpClient   *http.Client
	prompter     *prompt.Builder
	logger       *slog.Logger
}

// OpenAIConfig holds configuration for the OpenAI analyzer.
type OpenAIConfig struct {
	Model       string
	MaxTokens   int
	Temperature float64
	APIKeyRef   SecretRef
	// APIURL overrides the default OpenAI API endpoint (for testing).
	APIURL string
}

// NewOpenAIAnalyzer creates a new OpenAI-backed analyzer.
func NewOpenAIAnalyzer(cfg OpenAIConfig, secretReader SecretReader, prompter *prompt.Builder, logger *slog.Logger) (*OpenAIAnalyzer, error) {
	if cfg.Model == "" {
		return nil, fmt.Errorf("openai: model must not be empty")
	}
	if cfg.MaxTokens <= 0 {
		return nil, fmt.Errorf("openai: maxTokens must be > 0, got %d", cfg.MaxTokens)
	}
	if err := cfg.APIKeyRef.Validate(); err != nil {
		return nil, fmt.Errorf("openai: apiKeyRef: %w", err)
	}
	if secretReader == nil {
		return nil, fmt.Errorf("openai: secretReader must not be nil")
	}
	if prompter == nil {
		return nil, fmt.Errorf("openai: prompter must not be nil")
	}
	if logger == nil {
		return nil, fmt.Errorf("openai: logger must not be nil")
	}

	apiURL := cfg.APIURL
	if apiURL == "" {
		apiURL = defaultOpenAIURL
	}

	return &OpenAIAnalyzer{
		apiURL:       apiURL,
		model:        cfg.Model,
		maxTokens:    cfg.MaxTokens,
		temperature:  cfg.Temperature,
		secretRef:    cfg.APIKeyRef,
		secretReader: secretReader,
		httpClient:   &http.Client{Timeout: openAIHTTPTimeout},
		prompter:     prompter,
		logger:       logger,
	}, nil
}

// Name returns the analyzer backend identifier.
func (o *OpenAIAnalyzer) Name() string {
	return "openai"
}

// openAIRequest is the OpenAI Chat Completions API request body.
type openAIRequest struct {
	Model       string            `json:"model"`
	MaxTokens   int               `json:"max_tokens"`
	Temperature float64           `json:"temperature"`
	Messages    []openAIMessage   `json:"messages"`
}

// openAIMessage represents a message in the OpenAI Chat Completions API.
type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// openAIResponse is the OpenAI Chat Completions API response body.
type openAIResponse struct {
	Choices []openAIChoice `json:"choices"`
	Usage   openAIUsage    `json:"usage"`
	Error   *openAIError   `json:"error,omitempty"`
}

// openAIChoice represents a response choice.
type openAIChoice struct {
	Message openAIMessage `json:"message"`
}

// openAIUsage holds token usage from the OpenAI response.
type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

// openAIError represents an API error from OpenAI.
type openAIError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

// Analyze sends the diagnostic bundle to the OpenAI Chat Completions API.
func (o *OpenAIAnalyzer) Analyze(ctx context.Context, bundle model.DiagnosticBundle) (*model.RCAReport, error) {
	apiKey, err := o.secretReader.ReadSecret(ctx, o.secretRef.Namespace, o.secretRef.Name, o.secretRef.Key)
	if err != nil {
		return nil, fmt.Errorf("openai: reading API key: %w", err)
	}

	userPrompt := o.prompter.BuildUserPrompt(bundle)

	reqBody := openAIRequest{
		Model:       o.model,
		MaxTokens:   o.maxTokens,
		Temperature: o.temperature,
		Messages: []openAIMessage{
			{Role: "system", Content: o.prompter.SystemPrompt()},
			{Role: "user", Content: userPrompt},
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("openai: marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, o.apiURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("openai: creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	o.logger.Info("sending analysis request to OpenAI",
		"model", o.model,
		"fault_event_id", faultEventID(bundle),
	)

	resp, err := o.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("openai: sending request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB limit
	if err != nil {
		return nil, fmt.Errorf("openai: reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("openai: API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var openAIResp openAIResponse
	if err := json.Unmarshal(respBody, &openAIResp); err != nil {
		return nil, fmt.Errorf("openai: parsing response JSON: %w", err)
	}

	if openAIResp.Error != nil {
		return nil, fmt.Errorf("openai: API error: %s: %s", openAIResp.Error.Type, openAIResp.Error.Message)
	}

	if len(openAIResp.Choices) == 0 {
		return nil, fmt.Errorf("openai: response contained no choices")
	}

	analysisText := openAIResp.Choices[0].Message.Content

	tokens := model.TokenUsage{
		Input:  openAIResp.Usage.PromptTokens,
		Output: openAIResp.Usage.CompletionTokens,
	}

	o.logger.Info("received OpenAI analysis response",
		"fault_event_id", faultEventID(bundle),
		"input_tokens", tokens.Input,
		"output_tokens", tokens.Output,
	)

	return ParseLLMResponse(analysisText, bundle, "openai", tokens), nil
}

// Healthy checks whether the OpenAI API is reachable by verifying that the
// API key can be read.
func (o *OpenAIAnalyzer) Healthy(ctx context.Context) bool {
	_, err := o.secretReader.ReadSecret(ctx, o.secretRef.Namespace, o.secretRef.Name, o.secretRef.Key)
	if err != nil {
		o.logger.Warn("openai health check failed: cannot read API key",
			"error", err,
		)
		return false
	}
	return true
}
