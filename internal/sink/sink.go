// Package sink defines the Sink interface and provides built-in sink
// implementations for delivering RCA reports to external systems.
// See Section 5.4 of the project spec.
package sink

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

// Sink delivers RCA reports to an external system. Each sink implementation
// is responsible for formatting and transmitting the report to its target.
// Sinks must be safe for concurrent use.
type Sink interface {
	// Name returns the unique name of this sink (e.g., "log", "slack", "s3").
	Name() string

	// Deliver sends the RCA report to the sink's target system.
	// It returns an error if delivery fails after all retries are exhausted.
	Deliver(ctx context.Context, report *model.RCAReport) error

	// SeverityFilter returns the list of severities this sink accepts.
	// If empty, the sink accepts all severities.
	SeverityFilter() []model.Severity
}

// AcceptsSeverity returns true if the sink should process a report with the
// given severity. If the sink's SeverityFilter is empty, all severities are
// accepted.
func AcceptsSeverity(s Sink, severity model.Severity) bool {
	filter := s.SeverityFilter()
	if len(filter) == 0 {
		return true
	}
	for _, f := range filter {
		if f == severity {
			return true
		}
	}
	return false
}

// retryConfig holds parameters for retry with exponential backoff.
type retryConfig struct {
	maxAttempts int
	baseDelay   time.Duration
	multiplier  float64
}

// defaultRetryConfig returns the default retry configuration:
// 3 attempts with backoff 1s, 5s, 25s.
func defaultRetryConfig() retryConfig {
	return retryConfig{
		maxAttempts: 3,
		baseDelay:   1 * time.Second,
		multiplier:  5.0,
	}
}

// deliverWithRetry executes fn with retry logic. It attempts up to
// cfg.maxAttempts times with exponential backoff. The context can cancel
// retries early.
func deliverWithRetry(ctx context.Context, logger *slog.Logger, sinkName string, cfg retryConfig, fn func(ctx context.Context) error) error {
	var lastErr error
	for attempt := 0; attempt < cfg.maxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("sink %s: context cancelled before attempt %d: %w", sinkName, attempt+1, err)
		}

		lastErr = fn(ctx)
		if lastErr == nil {
			return nil
		}

		logger.Warn("sink delivery attempt failed",
			"sink", sinkName,
			"attempt", attempt+1,
			"max_attempts", cfg.maxAttempts,
			"error", lastErr,
		)

		// Don't sleep after the last attempt.
		if attempt < cfg.maxAttempts-1 {
			delay := time.Duration(float64(cfg.baseDelay) * math.Pow(cfg.multiplier, float64(attempt)))
			select {
			case <-ctx.Done():
				return fmt.Errorf("sink %s: context cancelled during backoff: %w", sinkName, ctx.Err())
			case <-time.After(delay):
			}
		}
	}
	return fmt.Errorf("sink %s: delivery failed after %d attempts: %w", sinkName, cfg.maxAttempts, lastErr)
}
