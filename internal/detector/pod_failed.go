package detector

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

// PodFailedConfig holds configuration for the PodFailed detector.
type PodFailedConfig struct {
	// IgnoreExitCodes is a set of exit codes to ignore when evaluating container
	// terminations. Default: [0] (ignore successful exits).
	IgnoreExitCodes []int32

	// Cooldown is the minimum time between fault events for the same resource.
	// Default: 30m.
	Cooldown time.Duration

	// Callback receives emitted fault events.
	Callback EventCallback

	// Logger for structured logging.
	Logger *slog.Logger
}

// ContainerTermination represents a terminated container state.
type ContainerTermination struct {
	Name     string
	ExitCode int32
	Reason   string // e.g., "OOMKilled", "Error", "Completed"
}

// PodFailedState represents the pod state for the PodFailed detector.
type PodFailedState struct {
	Name        string
	Namespace   string
	UID         string
	Phase       string // "Failed", "Running", etc.
	Labels      map[string]string
	Annotations map[string]string
	// Terminations holds terminated container states for containers that
	// exited non-zero (or zero, if relevant).
	Terminations []ContainerTermination
}

// PodFailed detects pods that transition to the Failed phase or have
// containers that exit non-zero (respecting ignoreExitCodes).
type PodFailed struct {
	base            *BaseDetector
	ignoreExitCodes map[int32]bool

	cancel context.CancelFunc
}

// NewPodFailed creates a PodFailed detector.
func NewPodFailed(cfg PodFailedConfig) (*PodFailed, error) {
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = 30 * time.Minute
	}

	base, err := NewBaseDetector(BaseDetectorConfig{
		Name:     "PodFailed",
		Severity: model.SeverityWarning,
		Cooldown: cfg.Cooldown,
		Callback: cfg.Callback,
		Logger:   cfg.Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("creating PodFailed detector: %w", err)
	}

	ignoreMap := make(map[int32]bool, len(cfg.IgnoreExitCodes))
	for _, code := range cfg.IgnoreExitCodes {
		ignoreMap[code] = true
	}

	return &PodFailed{
		base:            base,
		ignoreExitCodes: ignoreMap,
	}, nil
}

func (d *PodFailed) Name() string             { return d.base.DetectorName() }
func (d *PodFailed) Severity() model.Severity  { return d.base.DetectorSeverity() }
func (d *PodFailed) IsLeaderOnly() bool        { return false }

func (d *PodFailed) Start(ctx context.Context) error {
	ctx, d.cancel = context.WithCancel(ctx)
	<-ctx.Done()
	return ctx.Err()
}

func (d *PodFailed) Stop() {
	if d.cancel != nil {
		d.cancel()
	}
}

// Check evaluates the pod state and emits a fault event if the pod phase is
// Failed or if any container exited with a non-ignored exit code.
// Returns true if an event was emitted.
func (d *PodFailed) Check(pod PodFailedState) bool {
	// Check for pod phase = Failed.
	if pod.Phase == "Failed" {
		return d.emitForPod(pod, "Pod phase is Failed")
	}

	// Check for container terminations with non-ignored exit codes.
	for _, term := range pod.Terminations {
		if d.ignoreExitCodes[term.ExitCode] {
			continue
		}

		reason := term.Reason
		if reason == "" {
			reason = "Unknown"
		}

		desc := fmt.Sprintf(
			"Pod %s/%s container %q exited with code %d (reason: %s)",
			pod.Namespace, pod.Name, term.Name, term.ExitCode, reason,
		)

		ref := model.ResourceRef{
			Kind:      "Pod",
			Namespace: pod.Namespace,
			Name:      pod.Name,
			UID:       pod.UID,
		}

		return d.base.Emit(ref, desc, pod.Labels, map[string]string{
			"container": term.Name,
			"exitCode":  fmt.Sprintf("%d", term.ExitCode),
			"reason":    reason,
		})
	}

	return false
}

// emitForPod emits a fault event for the pod with the given reason.
func (d *PodFailed) emitForPod(pod PodFailedState, reason string) bool {
	ref := model.ResourceRef{
		Kind:      "Pod",
		Namespace: pod.Namespace,
		Name:      pod.Name,
		UID:       pod.UID,
	}

	description := fmt.Sprintf("Pod %s/%s: %s", pod.Namespace, pod.Name, reason)
	return d.base.Emit(ref, description, pod.Labels, nil)
}

// IgnoresExitCode returns true if the given exit code is in the ignore list.
func (d *PodFailed) IgnoresExitCode(code int32) bool {
	return d.ignoreExitCodes[code]
}
