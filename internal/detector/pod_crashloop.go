package detector

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

// PodCrashLoopConfig holds configuration for the PodCrashLoop detector.
type PodCrashLoopConfig struct {
	// Threshold is the minimum restart count to trigger a fault event.
	// Default: 3.
	Threshold int32

	// Cooldown is the minimum time between fault events for the same resource.
	// Default: 30m.
	Cooldown time.Duration

	// Callback receives emitted fault events.
	Callback EventCallback

	// Logger for structured logging.
	Logger *slog.Logger
}

// ContainerStatus represents the minimal container status needed for detection.
type ContainerStatus struct {
	Name         string
	RestartCount int32
	// Waiting is true if the container is in a waiting state.
	Waiting bool
	// WaitingReason is the reason for waiting (e.g., "CrashLoopBackOff").
	WaitingReason string
}

// PodContainerState represents the pod state with container statuses for
// the CrashLoop detector.
type PodContainerState struct {
	Name               string
	Namespace          string
	UID                string
	Labels             map[string]string
	Annotations        map[string]string
	ContainerStatuses  []ContainerStatus
	InitContainerStatuses []ContainerStatus
}

// PodCrashLoop detects pods that enter CrashLoopBackOff with a restart
// count exceeding a configurable threshold (default: 3).
type PodCrashLoop struct {
	base      *BaseDetector
	threshold int32

	mu     sync.Mutex
	cancel context.CancelFunc
}

// NewPodCrashLoop creates a PodCrashLoop detector.
func NewPodCrashLoop(cfg PodCrashLoopConfig) (*PodCrashLoop, error) {
	if cfg.Threshold <= 0 {
		cfg.Threshold = 3
	}
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = 30 * time.Minute
	}

	base, err := NewBaseDetector(BaseDetectorConfig{
		Name:     "PodCrashLoop",
		Severity: model.SeverityWarning,
		Cooldown: cfg.Cooldown,
		Callback: cfg.Callback,
		Logger:   cfg.Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("creating PodCrashLoop detector: %w", err)
	}

	return &PodCrashLoop{
		base:      base,
		threshold: cfg.Threshold,
	}, nil
}

func (d *PodCrashLoop) Name() string             { return d.base.DetectorName() }
func (d *PodCrashLoop) Severity() model.Severity  { return d.base.DetectorSeverity() }
func (d *PodCrashLoop) IsLeaderOnly() bool        { return false }

func (d *PodCrashLoop) Start(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	d.mu.Lock()
	d.cancel = cancel
	d.mu.Unlock()
	<-ctx.Done()
	return ctx.Err()
}

func (d *PodCrashLoop) Stop() {
	d.mu.Lock()
	cancel := d.cancel
	d.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Check evaluates the pod's container statuses and emits a fault event if
// any container is in CrashLoopBackOff with restarts exceeding the threshold.
// Returns true if an event was emitted.
func (d *PodCrashLoop) Check(pod PodContainerState) bool {
	// Build a combined list without mutating the input slices.
	allStatuses := make([]ContainerStatus, 0, len(pod.ContainerStatuses)+len(pod.InitContainerStatuses))
	allStatuses = append(allStatuses, pod.ContainerStatuses...)
	allStatuses = append(allStatuses, pod.InitContainerStatuses...)

	for _, cs := range allStatuses {
		if !d.isCrashLooping(cs) {
			continue
		}

		ref := model.ResourceRef{
			Kind:      "Pod",
			Namespace: pod.Namespace,
			Name:      pod.Name,
			UID:       pod.UID,
		}

		description := fmt.Sprintf(
			"Pod %s/%s container %q is in CrashLoopBackOff with %d restarts (threshold: %d)",
			pod.Namespace, pod.Name, cs.Name, cs.RestartCount, d.threshold,
		)

		return d.base.Emit(ref, description, pod.Labels, map[string]string{
			"container":    cs.Name,
			"restartCount": fmt.Sprintf("%d", cs.RestartCount),
		})
	}

	return false
}

// isCrashLooping checks if a container meets the crash loop criteria.
func (d *PodCrashLoop) isCrashLooping(cs ContainerStatus) bool {
	if cs.RestartCount < d.threshold {
		return false
	}
	// The container must be in CrashLoopBackOff waiting state.
	return cs.Waiting && cs.WaitingReason == "CrashLoopBackOff"
}

// Threshold returns the configured restart count threshold.
func (d *PodCrashLoop) Threshold() int32 {
	return d.threshold
}
