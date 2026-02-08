package detector

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

// NodeNotReadyConfig holds configuration for the NodeNotReady detector.
type NodeNotReadyConfig struct {
	// Threshold is how long a node's Ready condition must be False/Unknown
	// before a fault is emitted. Default: 5m.
	Threshold time.Duration

	// Cooldown is the minimum time between fault events for the same resource.
	// Default: 30m.
	Cooldown time.Duration

	// Callback receives emitted fault events.
	Callback EventCallback

	// Logger for structured logging.
	Logger *slog.Logger
}

// NodeConditionStatus represents the status of a node condition.
type NodeConditionStatus string

const (
	NodeConditionTrue    NodeConditionStatus = "True"
	NodeConditionFalse   NodeConditionStatus = "False"
	NodeConditionUnknown NodeConditionStatus = "Unknown"
)

// NodeState represents the minimal node state needed for detection.
type NodeState struct {
	Name string
	UID  string
	// ReadyCondition is the status of the Ready condition.
	ReadyCondition NodeConditionStatus
	// ReadyTransitionTime is when the Ready condition last transitioned.
	ReadyTransitionTime time.Time
	// Labels from the node metadata.
	Labels map[string]string
	// Annotations from the node metadata.
	Annotations map[string]string
}

// NodeNotReady detects nodes whose Ready condition transitions to False or
// Unknown for longer than a configurable threshold (default: 5m).
// This detector runs on the leader replica only.
type NodeNotReady struct {
	base      *BaseDetector
	threshold time.Duration

	cancel context.CancelFunc
}

// NewNodeNotReady creates a NodeNotReady detector.
func NewNodeNotReady(cfg NodeNotReadyConfig) (*NodeNotReady, error) {
	if cfg.Threshold <= 0 {
		cfg.Threshold = 5 * time.Minute
	}
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = 30 * time.Minute
	}

	base, err := NewBaseDetector(BaseDetectorConfig{
		Name:     "NodeNotReady",
		Severity: model.SeverityCritical,
		Cooldown: cfg.Cooldown,
		Callback: cfg.Callback,
		Logger:   cfg.Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("creating NodeNotReady detector: %w", err)
	}

	return &NodeNotReady{
		base:      base,
		threshold: cfg.Threshold,
	}, nil
}

func (d *NodeNotReady) Name() string             { return d.base.DetectorName() }
func (d *NodeNotReady) Severity() model.Severity  { return d.base.DetectorSeverity() }
func (d *NodeNotReady) IsLeaderOnly() bool        { return true }

func (d *NodeNotReady) Start(ctx context.Context) error {
	ctx, d.cancel = context.WithCancel(ctx)
	<-ctx.Done()
	return ctx.Err()
}

func (d *NodeNotReady) Stop() {
	if d.cancel != nil {
		d.cancel()
	}
}

// Check evaluates the node state and emits a fault event if the node's Ready
// condition is False or Unknown for longer than the threshold.
// Returns true if an event was emitted.
func (d *NodeNotReady) Check(node NodeState) bool {
	if node.ReadyCondition == NodeConditionTrue {
		return false
	}

	elapsed := d.base.Now().Sub(node.ReadyTransitionTime)
	if elapsed < d.threshold {
		return false
	}

	ref := model.ResourceRef{
		Kind: "Node",
		Name: node.Name,
		UID:  node.UID,
	}

	description := fmt.Sprintf(
		"Node %s Ready condition is %s for %s (threshold: %s)",
		node.Name, node.ReadyCondition, elapsed.Round(time.Second), d.threshold,
	)

	return d.base.Emit(ref, description, node.Labels, map[string]string{
		"readyCondition": string(node.ReadyCondition),
	})
}

// Threshold returns the configured not-ready threshold.
func (d *NodeNotReady) Threshold() time.Duration {
	return d.threshold
}
