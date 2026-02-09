package analyzer

import (
	"context"
	"testing"
	"time"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

// --- response.go: faultEventID and originalSeverity edge cases ---

// TestParseLLMResponse_BothNilBundle tests the fallback behavior when
// both FaultEvent and SuperEvent are nil in the bundle.
func TestParseLLMResponse_BothNilBundle(t *testing.T) {
	bundle := model.DiagnosticBundle{
		// Both FaultEvent and SuperEvent are nil.
		Timestamp: time.Now().UTC(),
	}
	tokens := model.TokenUsage{Input: 100, Output: 50}

	report := ParseLLMResponse(`{"rootCause":"test","severity":"warning","category":"unknown","confidence":0.5}`, bundle, "test", tokens)

	// Should still produce a valid report.
	if report.RootCause != "test" {
		t.Errorf("RootCause = %q, want %q", report.RootCause, "test")
	}
	// FaultEventID should be empty since both are nil.
	if report.FaultEventID != "" {
		t.Errorf("FaultEventID = %q, want empty", report.FaultEventID)
	}
}

// TestParseLLMResponse_FallbackSeverity tests that when both FaultEvent
// and SuperEvent are nil, severity defaults to warning.
func TestParseLLMResponse_FallbackSeverityBothNil(t *testing.T) {
	bundle := model.DiagnosticBundle{
		Timestamp: time.Now().UTC(),
	}
	tokens := model.TokenUsage{Input: 100, Output: 50}

	// Invalid severity in LLM response should fall back to original.
	report := ParseLLMResponse(`{"rootCause":"test","severity":"bogus","category":"unknown","confidence":0.5}`, bundle, "test", tokens)

	// Original severity from both-nil bundle defaults to "warning".
	if report.Severity != model.SeverityWarning {
		t.Errorf("Severity = %q, want %q", report.Severity, model.SeverityWarning)
	}
}

// TestParseLLMResponse_SuperEventPriority tests that when both SuperEvent
// and FaultEvent are present, SuperEvent takes priority for ID and severity.
func TestParseLLMResponse_SuperEventPriority(t *testing.T) {
	bundle := model.DiagnosticBundle{
		FaultEvent: &model.FaultEvent{
			ID:       "fault-id",
			Severity: model.SeverityInfo,
		},
		SuperEvent: &model.SuperEvent{
			ID:       "super-id",
			Severity: model.SeverityCritical,
		},
		Timestamp: time.Now().UTC(),
	}
	tokens := model.TokenUsage{Input: 100, Output: 50}

	report := ParseLLMResponse(`{"rootCause":"test","severity":"warning","category":"unknown","confidence":0.5}`, bundle, "test", tokens)

	// SuperEvent ID should take priority.
	if report.FaultEventID != "super-id" {
		t.Errorf("FaultEventID = %q, want %q", report.FaultEventID, "super-id")
	}
}

// TestParseLLMResponse_EmptyStringFallback tests fallback on completely empty
// string input.
func TestParseLLMResponse_EmptyStringFallback(t *testing.T) {
	bundle := responseTestBundle()
	tokens := model.TokenUsage{Input: 0, Output: 0}

	report := ParseLLMResponse("", bundle, "test", tokens)
	// Should create a fallback report.
	if report.Confidence != 0.0 {
		t.Errorf("Confidence = %f, want 0.0 for fallback", report.Confidence)
	}
	if report.Category != "unknown" {
		t.Errorf("Category = %q, want unknown for fallback", report.Category)
	}
}

// --- secret.go: Validate edge cases ---

func TestSecretRef_Validate(t *testing.T) {
	tests := []struct {
		name    string
		ref     SecretRef
		wantErr bool
	}{
		{
			name:    "valid with all fields",
			ref:     SecretRef{Namespace: "default", Name: "my-secret", Key: "api-key"},
			wantErr: false,
		},
		{
			name:    "valid without namespace",
			ref:     SecretRef{Name: "my-secret", Key: "api-key"},
			wantErr: false,
		},
		{
			name:    "empty name",
			ref:     SecretRef{Namespace: "default", Name: "", Key: "api-key"},
			wantErr: true,
		},
		{
			name:    "empty key",
			ref:     SecretRef{Namespace: "default", Name: "my-secret", Key: ""},
			wantErr: true,
		},
		{
			name:    "both empty",
			ref:     SecretRef{},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.ref.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// --- rate_limiter.go: recordTokens edge cases ---

func TestRateLimiter_RecordZeroTokens(t *testing.T) {
	primary := &tokenTrackingAnalyzer{name: "primary", tokensPerCall: 0}
	fallback := &mockAnalyzer{name: "fallback", healthy: true}

	rl, err := NewRateLimiter(primary, fallback, RateLimiterConfig{
		DailyTokenBudget:  1000,
		HourlyTokenBudget: 100,
	}, testLogger())
	if err != nil {
		t.Fatalf("NewRateLimiter: %v", err)
	}

	bundle := testBundle()
	// With zero tokens per call, should not affect budget.
	_, err = rl.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}

	dailyUsed, hourlyUsed := rl.TokenUsage()
	if dailyUsed != 0 {
		t.Errorf("DailyTokens = %d, want 0", dailyUsed)
	}
	if hourlyUsed != 0 {
		t.Errorf("HourlyTokens = %d, want 0", hourlyUsed)
	}
}

// TestRateLimiter_NegativeTokens tests that negative token values don't
// corrupt budget tracking (recordTokens early return).
func TestRateLimiter_NegativeTokens(t *testing.T) {
	primary := &tokenTrackingAnalyzer{name: "primary", tokensPerCall: -10}
	fallback := &mockAnalyzer{name: "fallback", healthy: true}

	rl, err := NewRateLimiter(primary, fallback, RateLimiterConfig{
		DailyTokenBudget:  1000,
		HourlyTokenBudget: 100,
	}, testLogger())
	if err != nil {
		t.Fatalf("NewRateLimiter: %v", err)
	}

	bundle := testBundle()
	_, err = rl.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}

	dailyUsed, hourlyUsed := rl.TokenUsage()
	if dailyUsed != 0 {
		t.Errorf("DailyTokens = %d, want 0 for negative tokens", dailyUsed)
	}
	if hourlyUsed != 0 {
		t.Errorf("HourlyTokens = %d, want 0 for negative tokens", hourlyUsed)
	}
}
