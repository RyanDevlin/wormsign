// Package detector defines the Detector interface for fault detection in the
// Wormsign pipeline, along with the base detector implementation that provides
// cooldown and deduplication, a registry for managing detectors, and all
// built-in detector implementations.
package detector

import (
	"context"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

// EventCallback is invoked by a detector when a fault event is detected.
// Implementations must be safe for concurrent use.
type EventCallback func(event *model.FaultEvent)

// Detector watches Kubernetes cluster state and emits FaultEvents when
// anomalous conditions are identified. Each detector is independently
// configurable and can be enabled/disabled.
type Detector interface {
	// Name returns the unique name of this detector (e.g., "PodStuckPending").
	Name() string

	// Start begins watching for faults. It blocks until ctx is cancelled.
	// Detected faults are delivered via the callback provided to the detector
	// at construction time. Start must not be called more than once.
	Start(ctx context.Context) error

	// Stop signals the detector to stop watching. It should release resources
	// and return promptly. After Stop returns, no further events will be emitted.
	Stop()

	// Severity returns the default severity level for events emitted by this
	// detector.
	Severity() model.Severity

	// IsLeaderOnly returns true if this detector must only run on the leader
	// replica. Cluster-scoped detectors (e.g., NodeNotReady) return true.
	IsLeaderOnly() bool
}
