// Package analyzer — azure_openai.go implements the Azure OpenAI analyzer
// backend for enterprises on Azure. See Section 5.3.2.
package analyzer

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/k8s-wormsign/k8s-wormsign/internal/analyzer/prompt"
	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

const (
	azureOpenAIAPIVersion = "2024-06-01"
	azureHTTPTimeout      = 120 * time.Second
)

// AzureOpenAIAnalyzer uses the Azure OpenAI Service to analyze diagnostic bundles.
type AzureOpenAIAnalyzer struct {
	endpoint       string
	deploymentName string
	maxTokens      int
	temperature    float64
	secretRef      SecretRef
	secretReader   SecretReader
	httpClient     *http.Client
	prompter       *prompt.Builder
	logger         *slog.Logger
	// apiURL overrides the constructed URL (for testing).
	apiURL string
}

// AzureOpenAIConfig holds configuration for the Azure OpenAI analyzer.
type AzureOpenAIConfig struct {
	Endpoint       string
	DeploymentName string
	MaxTokens      int
	Temperature    float64
	APIKeyRef      SecretRef
	// APIURL overrides the constructed Azure endpoint (for testing).
	APIURL string
}

// NewAzureOpenAIAnalyzer creates a new Azure OpenAI-backed analyzer.
func NewAzureOpenAIAnalyzer(cfg AzureOpenAIConfig, secretReader SecretReader, prompter *prompt.Builder, logger *slog.Logger) (*AzureOpenAIAnalyzer, error) {
	if cfg.Endpoint == "" {
		return nil, fmt.Errorf("azure-openai: endpoint must not be empty")
	}
	if cfg.DeploymentName == "" {
		return nil, fmt.Errorf("azure-openai: deploymentName must not be empty")
	}
	if err := cfg.APIKeyRef.Validate(); err != nil {
		return nil, fmt.Errorf("azure-openai: apiKeyRef: %w", err)
	}
	if secretReader == nil {
		return nil, fmt.Errorf("azure-openai: secretReader must not be nil")
	}
	if prompter == nil {
		return nil, fmt.Errorf("azure-openai: prompter must not be nil")
	}
	if logger == nil {
		return nil, fmt.Errorf("azure-openai: logger must not be nil")
	}

	// Default maxTokens to 4096 if not set.
	maxTokens := cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}

	return &AzureOpenAIAnalyzer{
		endpoint:       strings.TrimRight(cfg.Endpoint, "/"),
		deploymentName: cfg.DeploymentName,
		maxTokens:      maxTokens,
		temperature:    cfg.Temperature,
		secretRef:      cfg.APIKeyRef,
		secretReader:   secretReader,
		httpClient:     &http.Client{Timeout: azureHTTPTimeout},
		prompter:       prompter,
		logger:         logger,
		apiURL:         cfg.APIURL,
	}, nil
}

// Name returns the analyzer backend identifier.
func (a *AzureOpenAIAnalyzer) Name() string {
	return "azure-openai"
}

// buildURL constructs the Azure OpenAI chat completions endpoint URL.
func (a *AzureOpenAIAnalyzer) buildURL() string {
	if a.apiURL != "" {
		return a.apiURL
	}
	return fmt.Sprintf("%s/openai/deployments/%s/chat/completions?api-version=%s",
		a.endpoint, a.deploymentName, azureOpenAIAPIVersion)
}

// Analyze sends the diagnostic bundle to the Azure OpenAI Service.
func (a *AzureOpenAIAnalyzer) Analyze(ctx context.Context, bundle model.DiagnosticBundle) (*model.RCAReport, error) {
	apiKey, err := a.secretReader.ReadSecret(ctx, a.secretRef.Namespace, a.secretRef.Name, a.secretRef.Key)
	if err != nil {
		return nil, fmt.Errorf("azure-openai: reading API key: %w", err)
	}

	userPrompt := a.prompter.BuildUserPrompt(bundle)

	// Azure OpenAI uses the same request format as OpenAI, but without the model
	// field (the deployment specifies the model).
	reqBody := openAIRequest{
		Model:       "", // Not used by Azure; deployment determines the model.
		MaxTokens:   a.maxTokens,
		Temperature: a.temperature,
		Messages: []openAIMessage{
			{Role: "system", Content: a.prompter.SystemPrompt()},
			{Role: "user", Content: userPrompt},
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("azure-openai: marshaling request: %w", err)
	}

	url := a.buildURL()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("azure-openai: creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("api-key", apiKey)

	a.logger.Info("sending analysis request to Azure OpenAI",
		"endpoint", a.endpoint,
		"deployment", a.deploymentName,
		"fault_event_id", faultEventID(bundle),
	)

	resp, err := a.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("azure-openai: sending request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1MB limit
	if err != nil {
		return nil, fmt.Errorf("azure-openai: reading response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("azure-openai: API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var openAIResp openAIResponse
	if err := json.Unmarshal(respBody, &openAIResp); err != nil {
		return nil, fmt.Errorf("azure-openai: parsing response JSON: %w", err)
	}

	if openAIResp.Error != nil {
		return nil, fmt.Errorf("azure-openai: API error: %s: %s", openAIResp.Error.Type, openAIResp.Error.Message)
	}

	if len(openAIResp.Choices) == 0 {
		return nil, fmt.Errorf("azure-openai: response contained no choices")
	}

	analysisText := openAIResp.Choices[0].Message.Content

	tokens := model.TokenUsage{
		Input:  openAIResp.Usage.PromptTokens,
		Output: openAIResp.Usage.CompletionTokens,
	}

	a.logger.Info("received Azure OpenAI analysis response",
		"fault_event_id", faultEventID(bundle),
		"input_tokens", tokens.Input,
		"output_tokens", tokens.Output,
	)

	return ParseLLMResponse(analysisText, bundle, "azure-openai", tokens), nil
}

// Healthy checks whether the Azure OpenAI API is reachable by verifying that
// the API key can be read.
func (a *AzureOpenAIAnalyzer) Healthy(ctx context.Context) bool {
	_, err := a.secretReader.ReadSecret(ctx, a.secretRef.Namespace, a.secretRef.Name, a.secretRef.Key)
	if err != nil {
		a.logger.Warn("azure-openai health check failed: cannot read API key",
			"error", err,
		)
		return false
	}
	return true
}
