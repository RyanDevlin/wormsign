// Package analyzer — claude.go implements the Claude direct API analyzer
// backend using the Anthropic Messages API. See Section 5.3.2.
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
	defaultClaudeAPIURL = "https://api.anthropic.com/v1/messages"
	anthropicVersion    = "2023-06-01"
	claudeHTTPTimeout   = 120 * time.Second
)

// ClaudeAnalyzer uses the Anthropic Messages API to analyze diagnostic bundles.
type ClaudeAnalyzer struct {
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

// ClaudeConfig holds configuration for the direct Claude analyzer.
type ClaudeConfig struct {
	Model       string
	MaxTokens   int
	Temperature float64
	APIKeyRef   SecretRef
	// APIURL overrides the default Anthropic API endpoint (for testing).
	APIURL string
}

// NewClaudeAnalyzer creates a new Claude-backed analyzer.
func NewClaudeAnalyzer(cfg ClaudeConfig, secretReader SecretReader, prompter *prompt.Builder, logger *slog.Logger) (*ClaudeAnalyzer, error) {
	if cfg.Model == "" {
		return nil, fmt.Errorf("claude: model must not be empty")
	}
	if cfg.MaxTokens <= 0 {
		return nil, fmt.Errorf("claude: maxTokens must be > 0, got %d", cfg.MaxTokens)
	}
	if err := cfg.APIKeyRef.Validate(); err != nil {
		return nil, fmt.Errorf("claude: apiKeyRef: %w", err)
	}
	if secretReader == nil {
		return nil, fmt.Errorf("claude: secretReader must not be nil")
	}
	if prompter == nil {
		return nil, fmt.Errorf("claude: prompter must not be nil")
	}
	if logger == nil {
		return nil, fmt.Errorf("claude: logger must not be nil")
	}

	apiURL := cfg.APIURL
	if apiURL == "" {
		apiURL = defaultClaudeAPIURL
	}

	return &ClaudeAnalyzer{
		apiURL:       apiURL,
		model:        cfg.Model,
		maxTokens:    cfg.MaxTokens,
		temperature:  cfg.Temperature,
		secretRef:    cfg.APIKeyRef,
		secretReader: secretReader,
		httpClient:   &http.Client{Timeout: claudeHTTPTimeout},
		prompter:     prompter,
		logger:       logger,
	}, nil
}

// Name returns the analyzer backend identifier.
func (c *ClaudeAnalyzer) Name() string {
	return "claude"
}

// claudeRequest is the Anthropic Messages API request body.
type claudeRequest struct {
	Model       string           `json:"model"`
	MaxTokens   int              `json:"max_tokens"`
	Temperature float64          `json:"temperature"`
	System      string           `json:"system"`
	Messages    []claudeMessage  `json:"messages"`
}

// claudeMessage represents a message in the Anthropic Messages API.
type claudeMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// claudeResponse is the Anthropic Messages API response body.
type claudeResponse struct {
	ID      string               `json:"id"`
	Content []claudeContentBlock `json:"content"`
	Usage   claudeUsage          `json:"usage"`
	Error   *claudeError         `json:"error,omitempty"`
}

// claudeContentBlock is a content block in the Claude response.
type claudeContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// claudeUsage holds token usage from the Claude response.
type claudeUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// claudeError represents an API error from Claude.
type claudeError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

// Analyze sends the diagnostic bundle to the Anthropic Messages API for analysis.
func (c *ClaudeAnalyzer) Analyze(ctx context.Context, bundle model.DiagnosticBundle) (*model.RCAReport, error) {
	apiKey, err := c.secretReader.ReadSecret(ctx, c.secretRef.Namespace, c.secretRef.Name, c.secretRef.Key)
	if err != nil {
		return nil, fmt.Errorf("claude: reading API key: %w", err)
	}

	userPrompt := c.prompter.BuildUserPrompt(bundle)

	reqBody := claudeRequest{
		Model:       c.model,
		MaxTokens:   c.maxTokens,
		Temperature: c.temperature,
		System:      c.prompter.SystemPrompt(),
		Messages: []claudeMessage{
			{Role: "user", Content: userPrompt},
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("claude: marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.apiURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("claude: creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", anthropicVersion)

	c.logger.Info("sending analysis request to Claude",
		"model", c.model,
		"fault_event_id", faultEventID(bundle),
	)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("claude: sending request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB limit
	if err != nil {
		return nil, fmt.Errorf("claude: reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("claude: API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var claudeResp claudeResponse
	if err := json.Unmarshal(respBody, &claudeResp); err != nil {
		return nil, fmt.Errorf("claude: parsing response JSON: %w", err)
	}

	if claudeResp.Error != nil {
		return nil, fmt.Errorf("claude: API error: %s: %s", claudeResp.Error.Type, claudeResp.Error.Message)
	}

	// Extract text from content blocks.
	var analysisText string
	for _, block := range claudeResp.Content {
		if block.Type == "text" {
			analysisText += block.Text
		}
	}

	tokens := model.TokenUsage{
		Input:  claudeResp.Usage.InputTokens,
		Output: claudeResp.Usage.OutputTokens,
	}

	c.logger.Info("received Claude analysis response",
		"fault_event_id", faultEventID(bundle),
		"input_tokens", tokens.Input,
		"output_tokens", tokens.Output,
	)

	return ParseLLMResponse(analysisText, bundle, "claude", tokens), nil
}

// Healthy checks whether the Claude API is reachable by verifying that the
// API key can be read. A full API call is not made to avoid cost.
func (c *ClaudeAnalyzer) Healthy(ctx context.Context) bool {
	_, err := c.secretReader.ReadSecret(ctx, c.secretRef.Namespace, c.secretRef.Name, c.secretRef.Key)
	if err != nil {
		c.logger.Warn("claude health check failed: cannot read API key",
			"error", err,
		)
		return false
	}
	return true
}
