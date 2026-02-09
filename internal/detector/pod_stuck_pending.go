package detector

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

// PodStuckPendingConfig holds configuration for the PodStuckPending detector.
type PodStuckPendingConfig struct {
	// Threshold is how long a pod must be Pending before a fault is emitted.
	// Default: 15m.
	Threshold time.Duration

	// Cooldown is the minimum time between fault events for the same resource.
	// Default: 30m.
	Cooldown time.Duration

	// Callback receives emitted fault events.
	Callback EventCallback

	// Logger for structured logging.
	Logger *slog.Logger
}

// PodState represents the minimal pod state needed for detection.
// This decouples the detector from Kubernetes API types, allowing
// the detector to be tested without k8s dependencies.
type PodState struct {
	Name      string
	Namespace string
	UID       string
	Phase     string // "Pending", "Running", "Succeeded", "Failed", "Unknown"
	// CreationTimestamp is when the pod was created.
	CreationTimestamp time.Time
	// Labels from the pod metadata.
	Labels map[string]string
	// Annotations from the pod metadata.
	Annotations map[string]string
}

// PodStuckPending detects pods that remain in the Pending phase beyond a
// configurable threshold (default: 15m).
type PodStuckPending struct {
	base      *BaseDetector
	threshold time.Duration

	mu     sync.Mutex
	cancel context.CancelFunc
}

// NewPodStuckPending creates a PodStuckPending detector.
func NewPodStuckPending(cfg PodStuckPendingConfig) (*PodStuckPending, error) {
	if cfg.Threshold <= 0 {
		cfg.Threshold = 15 * time.Minute
	}
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = 30 * time.Minute
	}

	base, err := NewBaseDetector(BaseDetectorConfig{
		Name:     "PodStuckPending",
		Severity: model.SeverityWarning,
		Cooldown: cfg.Cooldown,
		Callback: cfg.Callback,
		Logger:   cfg.Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("creating PodStuckPending detector: %w", err)
	}

	return &PodStuckPending{
		base:      base,
		threshold: cfg.Threshold,
	}, nil
}

func (d *PodStuckPending) Name() string             { return d.base.DetectorName() }
func (d *PodStuckPending) Severity() model.Severity  { return d.base.DetectorSeverity() }
func (d *PodStuckPending) IsLeaderOnly() bool        { return false }

func (d *PodStuckPending) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	d.mu.Lock()
	d.cancel = cancel
	d.mu.Unlock()
	<-ctx.Done()
	return ctx.Err()
}

func (d *PodStuckPending) Stop() {
	d.mu.Lock()
	cancel := d.cancel
	d.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Check evaluates the pod state and emits a fault event if the pod has been
// Pending longer than the threshold. Returns true if an event was emitted.
func (d *PodStuckPending) Check(pod PodState) bool {
	if pod.Phase != "Pending" {
		return false
	}

	elapsed := d.base.Now().Sub(pod.CreationTimestamp)
	if elapsed < d.threshold {
		return false
	}

	ref := model.ResourceRef{
		Kind:      "Pod",
		Namespace: pod.Namespace,
		Name:      pod.Name,
		UID:       pod.UID,
	}

	description := fmt.Sprintf("Pod %s/%s has been Pending for %s (threshold: %s)",
		pod.Namespace, pod.Name, elapsed.Round(time.Second), d.threshold)

	return d.base.Emit(ref, description, pod.Labels, nil)
}

// Threshold returns the configured pending threshold.
func (d *PodStuckPending) Threshold() time.Duration {
	return d.threshold
}
