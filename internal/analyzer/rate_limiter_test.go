package analyzer

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

func TestNewRateLimiter_Validation(t *testing.T) {
	primary := &mockAnalyzer{name: "primary", healthy: true}
	fallback := &mockAnalyzer{name: "fallback", healthy: true}
	logger := testLogger()

	tests := []struct {
		name     string
		primary  Analyzer
		fallback Analyzer
		wantErr  string
	}{
		{
			name:     "nil primary",
			primary:  nil,
			fallback: fallback,
			wantErr:  "primary analyzer must not be nil",
		},
		{
			name:     "nil fallback",
			primary:  primary,
			fallback: nil,
			wantErr:  "fallback analyzer must not be nil",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewRateLimiter(tt.primary, tt.fallback, RateLimiterConfig{}, logger)
			if err == nil {
				t.Fatal("expected error")
			}
			if !containsSubstring(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestNewRateLimiter_NilLogger(t *testing.T) {
	primary := &mockAnalyzer{name: "primary", healthy: true}
	fallback := &mockAnalyzer{name: "fallback", healthy: true}
	_, err := NewRateLimiter(primary, fallback, RateLimiterConfig{}, nil)
	if err == nil {
		t.Fatal("expected error for nil logger")
	}
}

func TestRateLimiter_Name(t *testing.T) {
	primary := &mockAnalyzer{name: "claude-bedrock", healthy: true}
	fallback := &mockAnalyzer{name: "noop", healthy: true}
	rl, err := NewRateLimiter(primary, fallback, RateLimiterConfig{}, testLogger())
	if err != nil {
		t.Fatalf("NewRateLimiter() error = %v", err)
	}
	if rl.Name() != "claude-bedrock" {
		t.Errorf("Name() = %q, want %q", rl.Name(), "claude-bedrock")
	}
}

func TestRateLimiter_Healthy(t *testing.T) {
	primary := &mockAnalyzer{name: "primary", healthy: true}
	fallback := &mockAnalyzer{name: "fallback", healthy: true}
	rl, err := NewRateLimiter(primary, fallback, RateLimiterConfig{}, testLogger())
	if err != nil {
		t.Fatalf("NewRateLimiter() error = %v", err)
	}
	if !rl.Healthy(context.Background()) {
		t.Error("Healthy() = false, want true")
	}
}

func TestRateLimiter_UnlimitedBudget(t *testing.T) {
	// Zero budgets mean unlimited.
	primary := &tokenTrackingAnalyzer{name: "primary", tokensPerCall: 10000}
	fallback := &mockAnalyzer{name: "fallback", healthy: true}
	rl, err := NewRateLimiter(primary, fallback,
		RateLimiterConfig{DailyTokenBudget: 0, HourlyTokenBudget: 0},
		testLogger())
	if err != nil {
		t.Fatalf("NewRateLimiter() error = %v", err)
	}

	bundle := testBundle()

	for i := 0; i < 10; i++ {
		report, err := rl.Analyze(context.Background(), bundle)
		if err != nil {
			t.Fatalf("call %d: unexpected error: %v", i+1, err)
		}
		if report.AnalyzerBackend != "primary" {
			t.Errorf("call %d: AnalyzerBackend = %q, want %q", i+1, report.AnalyzerBackend, "primary")
		}
	}
}

func TestRateLimiter_DailyBudgetExceeded(t *testing.T) {
	primary := &tokenTrackingAnalyzer{name: "primary", tokensPerCall: 500}
	fallback := &mockAnalyzer{name: "fallback", healthy: true}
	rl, err := NewRateLimiter(primary, fallback,
		RateLimiterConfig{DailyTokenBudget: 1000, HourlyTokenBudget: 0},
		testLogger())
	if err != nil {
		t.Fatalf("NewRateLimiter() error = %v", err)
	}

	bundle := testBundle()

	// First call: 500 tokens (under budget).
	report, err := rl.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("call 1 error: %v", err)
	}
	if report.AnalyzerBackend != "primary" {
		t.Errorf("call 1: backend = %q, want primary", report.AnalyzerBackend)
	}

	// Second call: 500 more tokens (at budget = 1000).
	report, err = rl.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("call 2 error: %v", err)
	}
	if report.AnalyzerBackend != "primary" {
		t.Errorf("call 2: backend = %q, want primary", report.AnalyzerBackend)
	}

	// Third call: budget exceeded, should use fallback.
	report, err = rl.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("call 3 error: %v", err)
	}
	if report.AnalyzerBackend != "fallback" {
		t.Errorf("call 3: backend = %q, want fallback", report.AnalyzerBackend)
	}
}

func TestRateLimiter_HourlyBudgetExceeded(t *testing.T) {
	primary := &tokenTrackingAnalyzer{name: "primary", tokensPerCall: 600}
	fallback := &mockAnalyzer{name: "fallback", healthy: true}
	rl, err := NewRateLimiter(primary, fallback,
		RateLimiterConfig{DailyTokenBudget: 100000, HourlyTokenBudget: 1000},
		testLogger())
	if err != nil {
		t.Fatalf("NewRateLimiter() error = %v", err)
	}

	bundle := testBundle()

	// First call: 600 tokens.
	report, err := rl.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("call 1 error: %v", err)
	}
	if report.AnalyzerBackend != "primary" {
		t.Errorf("call 1: backend = %q, want primary", report.AnalyzerBackend)
	}

	// Second call: 600 more = 1200, exceeds hourly budget.
	report, err = rl.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("call 2 error: %v", err)
	}
	if report.AnalyzerBackend != "primary" {
		t.Errorf("call 2: backend = %q, want primary", report.AnalyzerBackend)
	}

	// Third call: over hourly budget.
	report, err = rl.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("call 3 error: %v", err)
	}
	if report.AnalyzerBackend != "fallback" {
		t.Errorf("call 3: backend = %q, want fallback", report.AnalyzerBackend)
	}
}

func TestRateLimiter_HourlyWindowReset(t *testing.T) {
	primary := &tokenTrackingAnalyzer{name: "primary", tokensPerCall: 600}
	fallback := &mockAnalyzer{name: "fallback", healthy: true}
	rl, err := NewRateLimiter(primary, fallback,
		RateLimiterConfig{DailyTokenBudget: 0, HourlyTokenBudget: 1000},
		testLogger())
	if err != nil {
		t.Fatalf("NewRateLimiter() error = %v", err)
	}

	now := time.Date(2026, 2, 8, 14, 30, 0, 0, time.UTC)
	rl.nowFunc = func() time.Time { return now }

	bundle := testBundle()

	// Two calls: 1200 tokens, exceeds hourly budget.
	_, _ = rl.Analyze(context.Background(), bundle)
	_, _ = rl.Analyze(context.Background(), bundle)

	// Over budget.
	report, _ := rl.Analyze(context.Background(), bundle)
	if report.AnalyzerBackend != "fallback" {
		t.Errorf("expected fallback, got %q", report.AnalyzerBackend)
	}

	// Advance to next hour: window resets.
	rl.nowFunc = func() time.Time { return now.Add(31 * time.Minute) }

	report, err = rl.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("after window reset: error = %v", err)
	}
	if report.AnalyzerBackend != "primary" {
		t.Errorf("after window reset: backend = %q, want primary", report.AnalyzerBackend)
	}
}

func TestRateLimiter_DailyWindowReset(t *testing.T) {
	primary := &tokenTrackingAnalyzer{name: "primary", tokensPerCall: 600}
	fallback := &mockAnalyzer{name: "fallback", healthy: true}
	rl, err := NewRateLimiter(primary, fallback,
		RateLimiterConfig{DailyTokenBudget: 1000, HourlyTokenBudget: 0},
		testLogger())
	if err != nil {
		t.Fatalf("NewRateLimiter() error = %v", err)
	}

	now := time.Date(2026, 2, 8, 23, 30, 0, 0, time.UTC)
	rl.nowFunc = func() time.Time { return now }

	bundle := testBundle()

	// Two calls: 1200 tokens, exceeds daily budget.
	_, _ = rl.Analyze(context.Background(), bundle)
	_, _ = rl.Analyze(context.Background(), bundle)

	report, _ := rl.Analyze(context.Background(), bundle)
	if report.AnalyzerBackend != "fallback" {
		t.Errorf("expected fallback, got %q", report.AnalyzerBackend)
	}

	// Advance to next day.
	rl.nowFunc = func() time.Time { return now.Add(time.Hour) }

	report, err = rl.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("after day reset: error = %v", err)
	}
	if report.AnalyzerBackend != "primary" {
		t.Errorf("after day reset: backend = %q, want primary", report.AnalyzerBackend)
	}
}

func TestRateLimiter_TokenUsage(t *testing.T) {
	primary := &tokenTrackingAnalyzer{name: "primary", tokensPerCall: 100}
	fallback := &mockAnalyzer{name: "fallback", healthy: true}
	rl, err := NewRateLimiter(primary, fallback,
		RateLimiterConfig{DailyTokenBudget: 10000, HourlyTokenBudget: 5000},
		testLogger())
	if err != nil {
		t.Fatalf("NewRateLimiter() error = %v", err)
	}

	bundle := testBundle()

	_, err = rl.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}

	daily, hourly := rl.TokenUsage()
	if daily != 100 {
		t.Errorf("daily usage = %d, want 100", daily)
	}
	if hourly != 100 {
		t.Errorf("hourly usage = %d, want 100", hourly)
	}

	_, err = rl.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}

	daily, hourly = rl.TokenUsage()
	if daily != 200 {
		t.Errorf("daily usage = %d, want 200", daily)
	}
	if hourly != 200 {
		t.Errorf("hourly usage = %d, want 200", hourly)
	}
}

func TestRateLimiter_PrimaryError_NotRecorded(t *testing.T) {
	primary := &mockAnalyzer{name: "primary", healthy: true, err: fmt.Errorf("LLM fail")}
	fallback := &mockAnalyzer{name: "fallback", healthy: true}
	rl, err := NewRateLimiter(primary, fallback,
		RateLimiterConfig{DailyTokenBudget: 1000, HourlyTokenBudget: 500},
		testLogger())
	if err != nil {
		t.Fatalf("NewRateLimiter() error = %v", err)
	}

	bundle := testBundle()

	_, err = rl.Analyze(context.Background(), bundle)
	if err == nil {
		t.Fatal("expected error from primary")
	}

	// No tokens should be recorded on failure.
	daily, hourly := rl.TokenUsage()
	if daily != 0 {
		t.Errorf("daily usage = %d, want 0", daily)
	}
	if hourly != 0 {
		t.Errorf("hourly usage = %d, want 0", hourly)
	}
}

// tokenTrackingAnalyzer returns reports with a fixed token count.
type tokenTrackingAnalyzer struct {
	name          string
	tokensPerCall int
	callCount     int
}

func (a *tokenTrackingAnalyzer) Name() string { return a.name }

func (a *tokenTrackingAnalyzer) Analyze(ctx context.Context, bundle model.DiagnosticBundle) (*model.RCAReport, error) {
	a.callCount++
	return &model.RCAReport{
		FaultEventID:    faultEventID(bundle),
		AnalyzerBackend: a.name,
		Severity:        model.SeverityInfo,
		Category:        "unknown",
		Remediation:     []string{},
		TokensUsed:      model.TokenUsage{Input: a.tokensPerCall / 2, Output: a.tokensPerCall / 2},
	}, nil
}

func (a *tokenTrackingAnalyzer) Healthy(ctx context.Context) bool { return true }
