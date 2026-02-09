package detector

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

// HighPodCountConfig holds configuration for the HighPodCount detector.
type HighPodCountConfig struct {
	// Threshold is the maximum number of pods in a namespace before a fault
	// is emitted. Default: 200.
	Threshold int

	// Cooldown is the minimum time between fault events for the same namespace.
	// Default: 1h.
	Cooldown time.Duration

	// Callback receives emitted fault events.
	Callback EventCallback

	// Logger for structured logging.
	Logger *slog.Logger
}

// NamespacePodCount represents a namespace with its current pod count.
type NamespacePodCount struct {
	Name     string
	UID      string
	PodCount int
	Labels   map[string]string
}

// HighPodCount detects namespaces where the number of pods exceeds a
// configurable threshold (default: 200).
type HighPodCount struct {
	base      *BaseDetector
	threshold int

	mu     sync.Mutex
	cancel context.CancelFunc
}

// NewHighPodCount creates a HighPodCount detector.
func NewHighPodCount(cfg HighPodCountConfig) (*HighPodCount, error) {
	if cfg.Threshold <= 0 {
		cfg.Threshold = 200
	}
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = 1 * time.Hour
	}

	base, err := NewBaseDetector(BaseDetectorConfig{
		Name:     "HighPodCount",
		Severity: model.SeverityInfo,
		Cooldown: cfg.Cooldown,
		Callback: cfg.Callback,
		Logger:   cfg.Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("creating HighPodCount detector: %w", err)
	}

	return &HighPodCount{
		base:      base,
		threshold: cfg.Threshold,
	}, nil
}

func (d *HighPodCount) Name() string             { return d.base.DetectorName() }
func (d *HighPodCount) Severity() model.Severity  { return d.base.DetectorSeverity() }
func (d *HighPodCount) IsLeaderOnly() bool        { return false }

func (d *HighPodCount) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	d.mu.Lock()
	d.cancel = cancel
	d.mu.Unlock()
	<-ctx.Done()
	return ctx.Err()
}

func (d *HighPodCount) Stop() {
	d.mu.Lock()
	cancel := d.cancel
	d.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Check evaluates whether the namespace pod count exceeds the threshold.
// Returns true if an event was emitted.
func (d *HighPodCount) Check(ns NamespacePodCount) bool {
	if ns.PodCount <= d.threshold {
		return false
	}

	ref := model.ResourceRef{
		Kind: "Namespace",
		Name: ns.Name,
		UID:  ns.UID,
	}

	description := fmt.Sprintf(
		"Namespace %s has %d pods (threshold: %d)",
		ns.Name, ns.PodCount, d.threshold,
	)

	return d.base.Emit(ref, description, ns.Labels, map[string]string{
		"podCount":  fmt.Sprintf("%d", ns.PodCount),
		"threshold": fmt.Sprintf("%d", d.threshold),
	})
}

// Threshold returns the configured pod count threshold.
func (d *HighPodCount) Threshold() int {
	return d.threshold
}
