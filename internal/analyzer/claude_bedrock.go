// Package analyzer — claude_bedrock.go implements the AWS Bedrock analyzer
// backend for enterprises requiring LLM data residency within AWS. See
// Section 5.3.2.
package analyzer

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/bedrockruntime"

	"github.com/k8s-wormsign/k8s-wormsign/internal/analyzer/prompt"
	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

// BedrockAnalyzer uses AWS Bedrock to analyze diagnostic bundles.
type BedrockAnalyzer struct {
	client      BedrockClient
	modelID     string
	maxTokens   int
	temperature float64
	prompter    *prompt.Builder
	logger      *slog.Logger
}

// BedrockClient is the interface for invoking Bedrock models, allowing test
// injection of a mock.
type BedrockClient interface {
	InvokeModel(ctx context.Context, params *bedrockruntime.InvokeModelInput, optFns ...func(*bedrockruntime.Options)) (*bedrockruntime.InvokeModelOutput, error)
}

// BedrockConfig holds configuration for the Bedrock analyzer.
type BedrockConfig struct {
	Region      string
	ModelID     string
	MaxTokens   int
	Temperature float64
}

// NewBedrockAnalyzer creates a new Bedrock-backed analyzer.
// It loads AWS credentials using the default credential chain (IRSA-compatible).
func NewBedrockAnalyzer(ctx context.Context, cfg BedrockConfig, prompter *prompt.Builder, logger *slog.Logger) (*BedrockAnalyzer, error) {
	if cfg.Region == "" {
		return nil, fmt.Errorf("bedrock: region must not be empty")
	}
	if cfg.ModelID == "" {
		return nil, fmt.Errorf("bedrock: modelID must not be empty")
	}
	if cfg.MaxTokens <= 0 {
		return nil, fmt.Errorf("bedrock: maxTokens must be > 0, got %d", cfg.MaxTokens)
	}
	if prompter == nil {
		return nil, fmt.Errorf("bedrock: prompter must not be nil")
	}
	if logger == nil {
		return nil, fmt.Errorf("bedrock: logger must not be nil")
	}

	awsCfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cfg.Region))
	if err != nil {
		return nil, fmt.Errorf("bedrock: loading AWS config: %w", err)
	}

	client := bedrockruntime.NewFromConfig(awsCfg)

	return &BedrockAnalyzer{
		client:      client,
		modelID:     cfg.ModelID,
		maxTokens:   cfg.MaxTokens,
		temperature: cfg.Temperature,
		prompter:    prompter,
		logger:      logger,
	}, nil
}

// newBedrockAnalyzerWithClient creates a BedrockAnalyzer with an injected client
// (for testing).
func newBedrockAnalyzerWithClient(client BedrockClient, cfg BedrockConfig, prompter *prompt.Builder, logger *slog.Logger) (*BedrockAnalyzer, error) {
	if client == nil {
		return nil, fmt.Errorf("bedrock: client must not be nil")
	}
	if cfg.ModelID == "" {
		return nil, fmt.Errorf("bedrock: modelID must not be empty")
	}
	if cfg.MaxTokens <= 0 {
		return nil, fmt.Errorf("bedrock: maxTokens must be > 0, got %d", cfg.MaxTokens)
	}
	if prompter == nil {
		return nil, fmt.Errorf("bedrock: prompter must not be nil")
	}
	if logger == nil {
		return nil, fmt.Errorf("bedrock: logger must not be nil")
	}
	return &BedrockAnalyzer{
		client:      client,
		modelID:     cfg.ModelID,
		maxTokens:   cfg.MaxTokens,
		temperature: cfg.Temperature,
		prompter:    prompter,
		logger:      logger,
	}, nil
}

// Name returns the analyzer backend identifier.
func (b *BedrockAnalyzer) Name() string {
	return "claude-bedrock"
}

// bedrockAnthropicRequest is the request body for Anthropic models via Bedrock
// InvokeModel. Bedrock expects the native Anthropic Messages API format.
type bedrockAnthropicRequest struct {
	AnthropicVersion string           `json:"anthropic_version"`
	MaxTokens        int              `json:"max_tokens"`
	Temperature      float64          `json:"temperature"`
	System           string           `json:"system"`
	Messages         []claudeMessage  `json:"messages"`
}

// bedrockAnthropicResponse mirrors the Claude response format returned by
// Bedrock InvokeModel for Anthropic models.
type bedrockAnthropicResponse struct {
	Content []claudeContentBlock `json:"content"`
	Usage   claudeUsage          `json:"usage"`
}

// Analyze sends the diagnostic bundle to AWS Bedrock for analysis using the
// Anthropic model specified by modelID.
func (b *BedrockAnalyzer) Analyze(ctx context.Context, bundle model.DiagnosticBundle) (*model.RCAReport, error) {
	userPrompt := b.prompter.BuildUserPrompt(bundle)

	reqBody := bedrockAnthropicRequest{
		AnthropicVersion: anthropicVersion,
		MaxTokens:        b.maxTokens,
		Temperature:      b.temperature,
		System:           b.prompter.SystemPrompt(),
		Messages: []claudeMessage{
			{Role: "user", Content: userPrompt},
		},
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("bedrock: marshaling request: %w", err)
	}

	b.logger.Info("sending analysis request to Bedrock",
		"model_id", b.modelID,
		"fault_event_id", faultEventID(bundle),
	)

	output, err := b.client.InvokeModel(ctx, &bedrockruntime.InvokeModelInput{
		ModelId:     aws.String(b.modelID),
		ContentType: aws.String("application/json"),
		Accept:      aws.String("application/json"),
		Body:        bodyBytes,
	})
	if err != nil {
		return nil, fmt.Errorf("bedrock: invoking model: %w", err)
	}

	var bedrockResp bedrockAnthropicResponse
	if err := json.Unmarshal(output.Body, &bedrockResp); err != nil {
		return nil, fmt.Errorf("bedrock: parsing response JSON: %w", err)
	}

	// Extract text from content blocks.
	var analysisText string
	for _, block := range bedrockResp.Content {
		if block.Type == "text" {
			analysisText += block.Text
		}
	}

	tokens := model.TokenUsage{
		Input:  bedrockResp.Usage.InputTokens,
		Output: bedrockResp.Usage.OutputTokens,
	}

	b.logger.Info("received Bedrock analysis response",
		"fault_event_id", faultEventID(bundle),
		"input_tokens", tokens.Input,
		"output_tokens", tokens.Output,
	)

	return ParseLLMResponse(analysisText, bundle, "claude-bedrock", tokens), nil
}

// Healthy checks whether the Bedrock client is configured.
func (b *BedrockAnalyzer) Healthy(ctx context.Context) bool {
	return b.client != nil
}
