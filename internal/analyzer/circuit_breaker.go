// Package analyzer — circuit_breaker.go implements a simple state-machine
// circuit breaker for the LLM analyzer backend. See Section 5.3.7 and
// DECISIONS.md D13.
//
// State transitions:
//
//	closed  → open       (after consecutiveFailures threshold is reached)
//	open    → half-open  (after openDuration elapses)
//	half-open → closed   (on successful probe request)
//	half-open → open     (on failed probe request)
package analyzer

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

// CircuitState represents the current state of the circuit breaker.
type CircuitState int

const (
	// CircuitClosed means the analyzer is functioning normally.
	CircuitClosed CircuitState = 0
	// CircuitOpen means the analyzer has failed too many times and requests
	// are short-circuited to the fallback.
	CircuitOpen CircuitState = 1
	// CircuitHalfOpen means the circuit breaker is probing with a single
	// request to determine if the backend has recovered.
	CircuitHalfOpen CircuitState = 2
)

// String returns a human-readable representation of the circuit state.
func (s CircuitState) String() string {
	switch s {
	case CircuitClosed:
		return "closed"
	case CircuitOpen:
		return "open"
	case CircuitHalfOpen:
		return "half-open"
	default:
		return fmt.Sprintf("unknown(%d)", int(s))
	}
}

// CircuitBreakerConfig holds configuration for the circuit breaker.
type CircuitBreakerConfig struct {
	// ConsecutiveFailures is the number of consecutive failures before the
	// circuit opens. Must be >= 1.
	ConsecutiveFailures int

	// OpenDuration is how long the circuit stays open before transitioning
	// to half-open for a probe request.
	OpenDuration time.Duration
}

// CircuitBreaker wraps an Analyzer with circuit breaker logic. When the
// underlying analyzer fails consecutively beyond the configured threshold,
// the circuit opens and requests are routed to a noop fallback analyzer.
// After the open duration elapses, a single probe request is allowed through
// to test recovery.
type CircuitBreaker struct {
	primary  Analyzer
	fallback Analyzer
	logger   *slog.Logger
	cfg      CircuitBreakerConfig

	mu                  sync.Mutex
	state               CircuitState
	consecutiveFailures int
	openedAt            time.Time

	// nowFunc allows testing to inject a clock.
	nowFunc func() time.Time
}

// NewCircuitBreaker creates a new CircuitBreaker wrapping the given primary
// analyzer. The fallback analyzer is used when the circuit is open.
func NewCircuitBreaker(primary Analyzer, fallback Analyzer, cfg CircuitBreakerConfig, logger *slog.Logger) (*CircuitBreaker, error) {
	if primary == nil {
		return nil, fmt.Errorf("circuit_breaker: primary analyzer must not be nil")
	}
	if fallback == nil {
		return nil, fmt.Errorf("circuit_breaker: fallback analyzer must not be nil")
	}
	if logger == nil {
		return nil, fmt.Errorf("circuit_breaker: logger must not be nil")
	}
	if cfg.ConsecutiveFailures < 1 {
		return nil, fmt.Errorf("circuit_breaker: consecutiveFailures must be >= 1, got %d", cfg.ConsecutiveFailures)
	}
	if cfg.OpenDuration <= 0 {
		return nil, fmt.Errorf("circuit_breaker: openDuration must be > 0, got %v", cfg.OpenDuration)
	}

	return &CircuitBreaker{
		primary:  primary,
		fallback: fallback,
		logger:   logger,
		cfg:      cfg,
		state:    CircuitClosed,
		nowFunc:  time.Now,
	}, nil
}

// Name returns the primary analyzer's name.
func (cb *CircuitBreaker) Name() string {
	return cb.primary.Name()
}

// Analyze processes the diagnostic bundle. If the circuit is closed or
// half-open, the request goes to the primary analyzer. If the circuit is
// open, the request is routed to the fallback analyzer.
func (cb *CircuitBreaker) Analyze(ctx context.Context, bundle model.DiagnosticBundle) (*model.RCAReport, error) {
	cb.mu.Lock()
	state := cb.currentStateLocked()
	cb.mu.Unlock()

	switch state {
	case CircuitOpen:
		cb.logger.Warn("circuit breaker open, using fallback analyzer",
			"primary", cb.primary.Name(),
			"fallback", cb.fallback.Name(),
		)
		return cb.fallback.Analyze(ctx, bundle)

	case CircuitHalfOpen:
		cb.logger.Info("circuit breaker half-open, sending probe request",
			"primary", cb.primary.Name(),
		)
		report, err := cb.primary.Analyze(ctx, bundle)
		if err != nil {
			cb.recordFailure()
			return cb.fallback.Analyze(ctx, bundle)
		}
		cb.recordSuccess()
		return report, nil

	default: // CircuitClosed
		report, err := cb.primary.Analyze(ctx, bundle)
		if err != nil {
			cb.recordFailure()
			return nil, err
		}
		cb.recordSuccess()
		return report, nil
	}
}

// Healthy reports whether the primary analyzer is healthy and the circuit
// is not open.
func (cb *CircuitBreaker) Healthy(ctx context.Context) bool {
	cb.mu.Lock()
	state := cb.currentStateLocked()
	cb.mu.Unlock()

	if state == CircuitOpen {
		return false
	}
	return cb.primary.Healthy(ctx)
}

// State returns the current circuit breaker state.
func (cb *CircuitBreaker) State() CircuitState {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.currentStateLocked()
}

// currentStateLocked evaluates the current state, handling the time-based
// transition from open to half-open. Caller must hold cb.mu.
func (cb *CircuitBreaker) currentStateLocked() CircuitState {
	if cb.state == CircuitOpen {
		if cb.nowFunc().Sub(cb.openedAt) >= cb.cfg.OpenDuration {
			cb.state = CircuitHalfOpen
			cb.logger.Info("circuit breaker transitioning to half-open",
				"primary", cb.primary.Name(),
				"open_duration", cb.cfg.OpenDuration,
			)
		}
	}
	return cb.state
}

// recordSuccess resets the failure count and closes the circuit.
func (cb *CircuitBreaker) recordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	wasOpen := cb.state != CircuitClosed
	cb.consecutiveFailures = 0
	cb.state = CircuitClosed

	if wasOpen {
		cb.logger.Info("circuit breaker closed after successful probe",
			"primary", cb.primary.Name(),
		)
	}
}

// recordFailure increments the consecutive failure count and opens the
// circuit if the threshold is reached.
func (cb *CircuitBreaker) recordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.consecutiveFailures++

	if cb.state == CircuitHalfOpen {
		// Probe failed, reopen the circuit.
		cb.state = CircuitOpen
		cb.openedAt = cb.nowFunc()
		cb.logger.Warn("circuit breaker reopened after failed probe",
			"primary", cb.primary.Name(),
			"consecutive_failures", cb.consecutiveFailures,
		)
		return
	}

	if cb.consecutiveFailures >= cb.cfg.ConsecutiveFailures && cb.state == CircuitClosed {
		cb.state = CircuitOpen
		cb.openedAt = cb.nowFunc()
		cb.logger.Warn("circuit breaker opened",
			"primary", cb.primary.Name(),
			"consecutive_failures", cb.consecutiveFailures,
			"open_duration", cb.cfg.OpenDuration,
		)
	}
}
