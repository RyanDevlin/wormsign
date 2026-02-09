package detector

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

// PVCStuckBindingConfig holds configuration for the PVCStuckBinding detector.
type PVCStuckBindingConfig struct {
	// Threshold is how long a PVC must remain Pending before a fault is emitted.
	// Default: 10m.
	Threshold time.Duration

	// Cooldown is the minimum time between fault events for the same resource.
	// Default: 30m.
	Cooldown time.Duration

	// Callback receives emitted fault events.
	Callback EventCallback

	// Logger for structured logging.
	Logger *slog.Logger
}

// PVCState represents the minimal PVC state needed for detection.
type PVCState struct {
	Name               string
	Namespace          string
	UID                string
	Phase              string // "Pending", "Bound", "Lost"
	CreationTimestamp   time.Time
	StorageClassName   string
	Labels             map[string]string
	Annotations        map[string]string
}

// PVCStuckBinding detects PersistentVolumeClaims that remain in the Pending
// state beyond a configurable threshold (default: 10m).
type PVCStuckBinding struct {
	base      *BaseDetector
	threshold time.Duration

	mu     sync.Mutex
	cancel context.CancelFunc
}

// NewPVCStuckBinding creates a PVCStuckBinding detector.
func NewPVCStuckBinding(cfg PVCStuckBindingConfig) (*PVCStuckBinding, error) {
	if cfg.Threshold <= 0 {
		cfg.Threshold = 10 * time.Minute
	}
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = 30 * time.Minute
	}

	base, err := NewBaseDetector(BaseDetectorConfig{
		Name:     "PVCStuckBinding",
		Severity: model.SeverityWarning,
		Cooldown: cfg.Cooldown,
		Callback: cfg.Callback,
		Logger:   cfg.Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("creating PVCStuckBinding detector: %w", err)
	}

	return &PVCStuckBinding{
		base:      base,
		threshold: cfg.Threshold,
	}, nil
}

func (d *PVCStuckBinding) Name() string             { return d.base.DetectorName() }
func (d *PVCStuckBinding) Severity() model.Severity  { return d.base.DetectorSeverity() }
func (d *PVCStuckBinding) IsLeaderOnly() bool        { return false }

func (d *PVCStuckBinding) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	d.mu.Lock()
	d.cancel = cancel
	d.mu.Unlock()
	<-ctx.Done()
	return ctx.Err()
}

func (d *PVCStuckBinding) Stop() {
	d.mu.Lock()
	cancel := d.cancel
	d.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Check evaluates the PVC state and emits a fault event if the PVC has been
// Pending longer than the threshold. Returns true if an event was emitted.
func (d *PVCStuckBinding) Check(pvc PVCState) bool {
	if pvc.Phase != "Pending" {
		return false
	}

	elapsed := d.base.Now().Sub(pvc.CreationTimestamp)
	if elapsed < d.threshold {
		return false
	}

	ref := model.ResourceRef{
		Kind:      "PersistentVolumeClaim",
		Namespace: pvc.Namespace,
		Name:      pvc.Name,
		UID:       pvc.UID,
	}

	description := fmt.Sprintf(
		"PVC %s/%s has been Pending for %s (threshold: %s)",
		pvc.Namespace, pvc.Name, elapsed.Round(time.Second), d.threshold,
	)

	annotations := map[string]string{}
	if pvc.StorageClassName != "" {
		annotations["storageClass"] = pvc.StorageClassName
	}

	return d.base.Emit(ref, description, pvc.Labels, annotations)
}

// Threshold returns the configured pending threshold.
func (d *PVCStuckBinding) Threshold() time.Duration {
	return d.threshold
}
