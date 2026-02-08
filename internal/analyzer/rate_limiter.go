// Package analyzer — rate_limiter.go implements token budget tracking for
// LLM analyzer backends. See Section 5.3.7 of the project spec.
//
// When daily or hourly token budgets are exceeded, the rate limiter falls
// back to a noop analyzer and surfaces raw diagnostics. Budget tracking is
// in-memory and resets on controller restart (acceptable for v1).
package analyzer

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

// RateLimiterConfig holds configuration for the token budget rate limiter.
type RateLimiterConfig struct {
	// DailyTokenBudget is the maximum total tokens (input + output) allowed
	// per UTC day. Zero or negative means unlimited.
	DailyTokenBudget int

	// HourlyTokenBudget is the maximum total tokens (input + output) allowed
	// per UTC hour. Zero or negative means unlimited.
	HourlyTokenBudget int
}

// tokenWindow tracks token usage within a time window.
type tokenWindow struct {
	tokens    int
	windowKey string // "2026-02-08" for daily, "2026-02-08T14" for hourly
}

// RateLimiter wraps an Analyzer with token budget enforcement. When the
// configured daily or hourly token budget is exceeded, subsequent requests
// are routed to a noop fallback analyzer until the window resets.
type RateLimiter struct {
	primary  Analyzer
	fallback Analyzer
	logger   *slog.Logger
	cfg      RateLimiterConfig

	mu     sync.Mutex
	daily  tokenWindow
	hourly tokenWindow

	// nowFunc allows testing to inject a clock.
	nowFunc func() time.Time
}

// NewRateLimiter creates a new RateLimiter wrapping the given primary analyzer.
func NewRateLimiter(primary Analyzer, fallback Analyzer, cfg RateLimiterConfig, logger *slog.Logger) (*RateLimiter, error) {
	if primary == nil {
		return nil, fmt.Errorf("rate_limiter: primary analyzer must not be nil")
	}
	if fallback == nil {
		return nil, fmt.Errorf("rate_limiter: fallback analyzer must not be nil")
	}
	if logger == nil {
		return nil, fmt.Errorf("rate_limiter: logger must not be nil")
	}

	return &RateLimiter{
		primary:  primary,
		fallback: fallback,
		logger:   logger,
		cfg:      cfg,
		nowFunc:  time.Now,
	}, nil
}

// Name returns the primary analyzer's name.
func (rl *RateLimiter) Name() string {
	return rl.primary.Name()
}

// Analyze processes the diagnostic bundle. If the token budget is exceeded,
// the request is routed to the fallback analyzer.
func (rl *RateLimiter) Analyze(ctx context.Context, bundle model.DiagnosticBundle) (*model.RCAReport, error) {
	if rl.isOverBudget() {
		rl.logger.Warn("token budget exceeded, using fallback analyzer",
			"primary", rl.primary.Name(),
			"fallback", rl.fallback.Name(),
		)
		return rl.fallback.Analyze(ctx, bundle)
	}

	report, err := rl.primary.Analyze(ctx, bundle)
	if err != nil {
		return nil, err
	}

	rl.recordTokens(report.TokensUsed.Total())
	return report, nil
}

// Healthy reports whether the primary analyzer is healthy.
func (rl *RateLimiter) Healthy(ctx context.Context) bool {
	return rl.primary.Healthy(ctx)
}

// isOverBudget checks whether either the daily or hourly token budget has
// been exceeded.
func (rl *RateLimiter) isOverBudget() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.nowFunc().UTC()

	if rl.cfg.DailyTokenBudget > 0 {
		dayKey := now.Format("2006-01-02")
		if rl.daily.windowKey == dayKey && rl.daily.tokens >= rl.cfg.DailyTokenBudget {
			return true
		}
	}

	if rl.cfg.HourlyTokenBudget > 0 {
		hourKey := now.Format("2006-01-02T15")
		if rl.hourly.windowKey == hourKey && rl.hourly.tokens >= rl.cfg.HourlyTokenBudget {
			return true
		}
	}

	return false
}

// recordTokens adds the given token count to both the daily and hourly
// windows, resetting windows that have rolled over.
func (rl *RateLimiter) recordTokens(tokens int) {
	if tokens <= 0 {
		return
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.nowFunc().UTC()

	// Daily window.
	dayKey := now.Format("2006-01-02")
	if rl.daily.windowKey != dayKey {
		rl.daily = tokenWindow{windowKey: dayKey}
	}
	rl.daily.tokens += tokens

	// Hourly window.
	hourKey := now.Format("2006-01-02T15")
	if rl.hourly.windowKey != hourKey {
		rl.hourly = tokenWindow{windowKey: hourKey}
	}
	rl.hourly.tokens += tokens

	rl.logger.Debug("recorded token usage",
		"tokens", tokens,
		"daily_total", rl.daily.tokens,
		"daily_budget", rl.cfg.DailyTokenBudget,
		"hourly_total", rl.hourly.tokens,
		"hourly_budget", rl.cfg.HourlyTokenBudget,
	)
}

// TokenUsage returns the current token usage for both daily and hourly windows.
// Window counters are reset when the window rolls over.
func (rl *RateLimiter) TokenUsage() (dailyUsed, hourlyUsed int) {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.nowFunc().UTC()

	dayKey := now.Format("2006-01-02")
	if rl.daily.windowKey == dayKey {
		dailyUsed = rl.daily.tokens
	}

	hourKey := now.Format("2006-01-02T15")
	if rl.hourly.windowKey == hourKey {
		hourlyUsed = rl.hourly.tokens
	}

	return dailyUsed, hourlyUsed
}
