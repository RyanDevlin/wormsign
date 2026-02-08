package detector

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

// dedupKey uniquely identifies a (detector, resource) pair for deduplication.
type dedupKey struct {
	DetectorName string
	ResourceUID  string
}

// BaseDetector provides shared cooldown and deduplication logic for all
// built-in detectors. Detectors embed BaseDetector and call Emit() to
// deliver fault events through the cooldown/dedup gate.
//
// Deduplication is keyed on (DetectorName, Resource.UID) within the cooldown
// window. Cooldown state is in-memory and resets on controller restart.
type BaseDetector struct {
	mu       sync.Mutex
	name     string
	severity model.Severity
	cooldown time.Duration
	callback EventCallback
	logger   *slog.Logger

	// lastFired tracks the last time a fault event was emitted for each
	// (detector, resource UID) pair.
	lastFired map[dedupKey]time.Time

	// nowFunc allows tests to control the clock.
	nowFunc func() time.Time
}

// BaseDetectorConfig holds the configuration for constructing a BaseDetector.
type BaseDetectorConfig struct {
	Name     string
	Severity model.Severity
	Cooldown time.Duration
	Callback EventCallback
	Logger   *slog.Logger
}

// NewBaseDetector creates a new BaseDetector with the given configuration.
// It returns an error if required fields are missing.
func NewBaseDetector(cfg BaseDetectorConfig) (*BaseDetector, error) {
	if cfg.Name == "" {
		return nil, fmt.Errorf("detector name must not be empty")
	}
	if !cfg.Severity.IsValid() {
		return nil, fmt.Errorf("invalid severity %q for detector %s", cfg.Severity, cfg.Name)
	}
	if cfg.Cooldown <= 0 {
		return nil, fmt.Errorf("cooldown must be positive for detector %s", cfg.Name)
	}
	if cfg.Callback == nil {
		return nil, fmt.Errorf("callback must not be nil for detector %s", cfg.Name)
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	return &BaseDetector{
		name:      cfg.Name,
		severity:  cfg.Severity,
		cooldown:  cfg.Cooldown,
		callback:  cfg.Callback,
		logger:    cfg.Logger,
		lastFired: make(map[dedupKey]time.Time),
		nowFunc:   time.Now,
	}, nil
}

// DetectorName returns the name of this detector.
func (b *BaseDetector) DetectorName() string {
	return b.name
}

// DetectorSeverity returns the default severity level.
func (b *BaseDetector) DetectorSeverity() model.Severity {
	return b.severity
}

// Logger returns the detector's logger.
func (b *BaseDetector) Logger() *slog.Logger {
	return b.logger
}

// Now returns the current time, using the test hook if set.
func (b *BaseDetector) Now() time.Time {
	if b.nowFunc != nil {
		return b.nowFunc()
	}
	return time.Now()
}

// SetNowFunc overrides the clock for testing.
func (b *BaseDetector) SetNowFunc(fn func() time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.nowFunc = fn
}

// ShouldFire checks whether a fault event should fire for the given resource.
// It returns true if the cooldown window has elapsed (or no prior event exists)
// for the (DetectorName, Resource.UID) pair, false if the event should be
// suppressed by cooldown/dedup.
func (b *BaseDetector) ShouldFire(resource model.ResourceRef) bool {
	key := dedupKey{
		DetectorName: b.name,
		ResourceUID:  resource.UID,
	}

	now := b.Now()

	b.mu.Lock()
	defer b.mu.Unlock()

	if lastTime, ok := b.lastFired[key]; ok {
		if now.Sub(lastTime) < b.cooldown {
			b.logger.Debug("fault event suppressed by cooldown",
				"detector", b.name,
				"resource", resource.String(),
				"last_fired", lastTime,
				"cooldown", b.cooldown,
			)
			return false
		}
	}
	return true
}

// RecordFire records that a fault event was emitted for the given resource,
// updating the cooldown state for the (DetectorName, Resource.UID) pair.
func (b *BaseDetector) RecordFire(resource model.ResourceRef) {
	key := dedupKey{
		DetectorName: b.name,
		ResourceUID:  resource.UID,
	}

	now := b.Now()

	b.mu.Lock()
	b.lastFired[key] = now
	b.mu.Unlock()
}

// Emit creates and delivers a FaultEvent if the cooldown window has elapsed
// for the given resource. Returns true if the event was emitted, false if it
// was suppressed by cooldown/dedup. This is a convenience method that combines
// ShouldFire, RecordFire, event creation, and callback delivery.
func (b *BaseDetector) Emit(resource model.ResourceRef, description string, labels, annotations map[string]string) bool {
	if !b.ShouldFire(resource) {
		return false
	}
	b.RecordFire(resource)

	event, err := model.NewFaultEvent(b.name, b.severity, resource, description, labels, annotations)
	if err != nil {
		b.logger.Error("failed to create fault event",
			"detector", b.name,
			"resource", resource.String(),
			"error", err,
		)
		return false
	}

	b.logger.Info("fault event emitted",
		"detector", b.name,
		"severity", string(b.severity),
		"resource", resource.String(),
		"fault_event_id", event.ID,
	)

	b.callback(event)
	return true
}

// CleanupExpiredEntries removes dedup entries older than 2x the cooldown
// duration. Call periodically to prevent unbounded memory growth.
func (b *BaseDetector) CleanupExpiredEntries() {
	now := b.Now()
	threshold := 2 * b.cooldown

	b.mu.Lock()
	defer b.mu.Unlock()

	for key, lastTime := range b.lastFired {
		if now.Sub(lastTime) > threshold {
			delete(b.lastFired, key)
		}
	}
}

// DedupEntryCount returns the number of active dedup entries. Useful for
// testing and metrics.
func (b *BaseDetector) DedupEntryCount() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.lastFired)
}
