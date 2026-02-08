// Package correlator implements the pre-LLM event correlation engine.
// It buffers FaultEvents within a configurable time window and applies
// built-in correlation rules to group related events into SuperEvents.
//
// The correlation engine is stateful and single-threaded per replica.
// It sits between the detection and gathering stages:
//
//	Detection → Correlation → Gathering → Analysis → Sinks
//
// Events that match a correlation rule are grouped into SuperEvents.
// Events that do not match any rule are passed through individually.
package correlator

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/k8s-wormsign/k8s-wormsign/internal/metrics"
	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

// Default configuration values.
const (
	DefaultWindowDuration    = 2 * time.Minute
	DefaultMinPodFailures    = 2
	DefaultDeployMinFailures = 3
	DefaultNamespaceStormThr = 20
)

// Config holds the correlation engine configuration.
type Config struct {
	// Enabled controls whether correlation is active.
	// When disabled, all events pass through uncorrelated.
	Enabled bool

	// WindowDuration is how long to buffer events before evaluating rules.
	WindowDuration time.Duration

	// Rules holds per-rule configuration.
	Rules RulesConfig
}

// RulesConfig holds configuration for each built-in correlation rule.
type RulesConfig struct {
	NodeCascade      NodeCascadeConfig
	DeploymentRollout DeploymentRolloutConfig
	StorageCascade   StorageCascadeConfig
	NamespaceStorm   NamespaceStormConfig
}

// NodeCascadeConfig configures the NodeCascade correlation rule.
type NodeCascadeConfig struct {
	Enabled        bool
	MinPodFailures int
}

// DeploymentRolloutConfig configures the DeploymentRollout correlation rule.
type DeploymentRolloutConfig struct {
	Enabled        bool
	MinPodFailures int
}

// StorageCascadeConfig configures the StorageCascade correlation rule.
type StorageCascadeConfig struct {
	Enabled bool
}

// NamespaceStormConfig configures the NamespaceStorm correlation rule.
type NamespaceStormConfig struct {
	Enabled   bool
	Threshold int
}

// DefaultConfig returns a Config with spec-defined defaults.
func DefaultConfig() Config {
	return Config{
		Enabled:        true,
		WindowDuration: DefaultWindowDuration,
		Rules: RulesConfig{
			NodeCascade: NodeCascadeConfig{
				Enabled:        true,
				MinPodFailures: DefaultMinPodFailures,
			},
			DeploymentRollout: DeploymentRolloutConfig{
				Enabled:        true,
				MinPodFailures: DefaultDeployMinFailures,
			},
			StorageCascade: StorageCascadeConfig{
				Enabled: true,
			},
			NamespaceStorm: NamespaceStormConfig{
				Enabled:   true,
				Threshold: DefaultNamespaceStormThr,
			},
		},
	}
}

// ValidateConfig checks that the configuration is semantically valid.
func ValidateConfig(cfg Config) error {
	if cfg.WindowDuration <= 0 {
		return fmt.Errorf("correlation window duration must be positive, got %s", cfg.WindowDuration)
	}
	if cfg.Rules.NodeCascade.Enabled && cfg.Rules.NodeCascade.MinPodFailures < 1 {
		return fmt.Errorf("NodeCascade minPodFailures must be >= 1, got %d", cfg.Rules.NodeCascade.MinPodFailures)
	}
	if cfg.Rules.DeploymentRollout.Enabled && cfg.Rules.DeploymentRollout.MinPodFailures < 1 {
		return fmt.Errorf("DeploymentRollout minPodFailures must be >= 1, got %d", cfg.Rules.DeploymentRollout.MinPodFailures)
	}
	if cfg.Rules.NamespaceStorm.Enabled && cfg.Rules.NamespaceStorm.Threshold < 1 {
		return fmt.Errorf("NamespaceStorm threshold must be >= 1, got %d", cfg.Rules.NamespaceStorm.Threshold)
	}
	return nil
}

// CorrelationResult represents the output of a correlation window evaluation.
// It contains SuperEvents for correlated groups and individual FaultEvents
// for uncorrelated events.
type CorrelationResult struct {
	SuperEvents      []*model.SuperEvent
	UncorrelatedEvents []model.FaultEvent
}

// OutputHandler is a callback invoked when the correlation window produces results.
// Implementations should enqueue the results for downstream processing (gathering).
type OutputHandler func(result CorrelationResult)

// Option configures the Correlator. Used primarily for testing.
type Option func(*Correlator)

// WithNowFunc overrides the time source for testing.
func WithNowFunc(fn func() time.Time) Option {
	return func(c *Correlator) {
		c.nowFunc = fn
	}
}

// Correlator is the pre-LLM event correlation engine. It buffers incoming
// FaultEvents and periodically evaluates correlation rules to group related
// events into SuperEvents.
//
// Thread safety: Submit is safe to call from multiple goroutines.
// The internal evaluation loop is single-threaded per the spec.
type Correlator struct {
	config  Config
	logger  *slog.Logger
	metrics *metrics.Metrics
	handler OutputHandler
	nowFunc func() time.Time

	mu     sync.Mutex
	buffer []model.FaultEvent // events in the current window

	// stopCh signals the evaluation loop to stop.
	stopCh chan struct{}
	// doneCh is closed when the evaluation loop has fully stopped.
	doneCh chan struct{}
}

// New creates a new Correlator with the given configuration.
// The handler is called when correlation results are ready.
// The correlator must be started with Run().
func New(cfg Config, handler OutputHandler, m *metrics.Metrics, logger *slog.Logger, opts ...Option) (*Correlator, error) {
	if err := ValidateConfig(cfg); err != nil {
		return nil, fmt.Errorf("invalid correlator config: %w", err)
	}
	if handler == nil {
		return nil, fmt.Errorf("output handler must not be nil")
	}
	if m == nil {
		return nil, fmt.Errorf("metrics must not be nil")
	}
	if logger == nil {
		logger = slog.Default()
	}

	c := &Correlator{
		config:  cfg,
		logger:  logger,
		metrics: m,
		handler: handler,
		nowFunc: time.Now,
		buffer:  make([]model.FaultEvent, 0),
		stopCh:  make(chan struct{}),
		doneCh:  make(chan struct{}),
	}

	for _, opt := range opts {
		opt(c)
	}

	return c, nil
}

// Submit adds a FaultEvent to the correlation buffer.
// If correlation is disabled, the event is immediately passed through.
func (c *Correlator) Submit(event model.FaultEvent) {
	if !c.config.Enabled {
		c.handler(CorrelationResult{
			UncorrelatedEvents: []model.FaultEvent{event},
		})
		return
	}

	c.mu.Lock()
	c.buffer = append(c.buffer, event)
	c.mu.Unlock()

	c.logger.Debug("event buffered for correlation",
		"detector", event.DetectorName,
		"resource", event.Resource.String(),
		"severity", event.Severity,
		"fault_event_id", event.ID,
	)
}

// Run starts the correlation evaluation loop. It blocks until the context
// is canceled or Flush is called during shutdown. The loop evaluates the
// buffer at each window interval.
func (c *Correlator) Run(ctx context.Context) {
	defer close(c.doneCh)

	if !c.config.Enabled {
		c.logger.Info("correlator disabled, running in pass-through mode")
		<-ctx.Done()
		return
	}

	c.logger.Info("correlator started",
		"window_duration", c.config.WindowDuration.String(),
	)

	ticker := time.NewTicker(c.config.WindowDuration)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			c.logger.Info("correlator shutting down, flushing remaining events")
			c.evaluateAndEmit()
			return
		case <-c.stopCh:
			c.logger.Info("correlator stop requested, flushing remaining events")
			c.evaluateAndEmit()
			return
		case <-ticker.C:
			c.evaluateAndEmit()
		}
	}
}

// Flush drains all pending events in the buffer by evaluating rules
// immediately and passing results to the handler. This is called during
// graceful shutdown.
func (c *Correlator) Flush() {
	close(c.stopCh)
	<-c.doneCh
}

// evaluateAndEmit takes all buffered events, applies correlation rules,
// and delivers results to the output handler.
func (c *Correlator) evaluateAndEmit() {
	c.mu.Lock()
	events := c.buffer
	c.buffer = make([]model.FaultEvent, 0)
	c.mu.Unlock()

	if len(events) == 0 {
		return
	}

	c.logger.Info("evaluating correlation window",
		"event_count", len(events),
	)

	result := c.evaluate(events)

	// Record metrics.
	for _, se := range result.SuperEvents {
		c.metrics.SuperEventsTotal.WithLabelValues(
			se.CorrelationRule,
			string(se.Severity),
		).Inc()
	}

	if len(result.SuperEvents) > 0 || len(result.UncorrelatedEvents) > 0 {
		c.handler(result)
	}

	c.logger.Info("correlation window complete",
		"super_events", len(result.SuperEvents),
		"uncorrelated_events", len(result.UncorrelatedEvents),
	)
}

// evaluate applies all enabled correlation rules to the given events.
// Events consumed by a rule are not available to subsequent rules.
// The NamespaceStorm rule runs last as a catch-all.
func (c *Correlator) evaluate(events []model.FaultEvent) CorrelationResult {
	var result CorrelationResult

	// Track which events have been consumed by correlation rules.
	consumed := make(map[string]bool, len(events))

	// Rule 1: NodeCascade
	if c.config.Rules.NodeCascade.Enabled {
		superEvents := c.evaluateNodeCascade(events, consumed)
		result.SuperEvents = append(result.SuperEvents, superEvents...)
	}

	// Rule 2: DeploymentRollout
	if c.config.Rules.DeploymentRollout.Enabled {
		superEvents := c.evaluateDeploymentRollout(events, consumed)
		result.SuperEvents = append(result.SuperEvents, superEvents...)
	}

	// Rule 3: StorageCascade
	if c.config.Rules.StorageCascade.Enabled {
		superEvents := c.evaluateStorageCascade(events, consumed)
		result.SuperEvents = append(result.SuperEvents, superEvents...)
	}

	// Rule 4: NamespaceStorm (catch-all, runs last)
	if c.config.Rules.NamespaceStorm.Enabled {
		superEvents := c.evaluateNamespaceStorm(events, consumed)
		result.SuperEvents = append(result.SuperEvents, superEvents...)
	}

	// Remaining uncorrelated events pass through.
	for i := range events {
		if !consumed[events[i].ID] {
			result.UncorrelatedEvents = append(result.UncorrelatedEvents, events[i])
		}
	}

	return result
}
