package shard

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	// leasePrefix is the prefix for Lease objects managed by wormsign
	// controller replicas.
	leasePrefix = "wormsign-"

	// leaseExpirationThreshold defines how long after a Lease's
	// last renewal time a replica is considered dead. This should be
	// comfortably larger than the leader election lease duration.
	leaseExpirationThreshold = 45 * time.Second

	// defaultWatchInterval is how often the replica watcher polls for
	// active Leases.
	defaultWatchInterval = 10 * time.Second
)

// ReplicaWatcher monitors Lease objects to discover active controller replicas.
// The coordinator uses this to dynamically adjust the replica count for shard
// assignment. When replicas are added or removed, it updates the shard Manager's
// replica count, triggering rebalancing on the next reconciliation cycle.
type ReplicaWatcher struct {
	logger              *slog.Logger
	clientset           kubernetes.Interface
	controllerNamespace string
	manager             *Manager
	watchInterval       time.Duration
	nowFunc             func() time.Time
}

// ReplicaWatcherOption configures a ReplicaWatcher.
type ReplicaWatcherOption func(*ReplicaWatcher)

// WithReplicaWatcherLogger sets the logger for the ReplicaWatcher.
func WithReplicaWatcherLogger(logger *slog.Logger) ReplicaWatcherOption {
	return func(rw *ReplicaWatcher) {
		rw.logger = logger
	}
}

// WithWatchInterval sets how often the watcher checks for active replicas.
func WithWatchInterval(d time.Duration) ReplicaWatcherOption {
	return func(rw *ReplicaWatcher) {
		rw.watchInterval = d
	}
}

// WithReplicaWatcherNowFunc overrides the time source. Intended for testing.
func WithReplicaWatcherNowFunc(fn func() time.Time) ReplicaWatcherOption {
	return func(rw *ReplicaWatcher) {
		rw.nowFunc = fn
	}
}

// NewReplicaWatcher creates a new ReplicaWatcher that monitors Lease objects
// in the controller namespace and updates the Manager's replica count.
func NewReplicaWatcher(
	clientset kubernetes.Interface,
	controllerNamespace string,
	manager *Manager,
	opts ...ReplicaWatcherOption,
) (*ReplicaWatcher, error) {
	if clientset == nil {
		return nil, fmt.Errorf("shard: ReplicaWatcher clientset must not be nil")
	}
	if controllerNamespace == "" {
		return nil, fmt.Errorf("shard: ReplicaWatcher controllerNamespace must not be empty")
	}
	if manager == nil {
		return nil, fmt.Errorf("shard: ReplicaWatcher manager must not be nil")
	}

	rw := &ReplicaWatcher{
		logger:              slog.Default(),
		clientset:           clientset,
		controllerNamespace: controllerNamespace,
		manager:             manager,
		watchInterval:       defaultWatchInterval,
		nowFunc:             time.Now,
	}

	for _, opt := range opts {
		opt(rw)
	}

	return rw, nil
}

// Run starts the replica watcher loop. It periodically lists Lease objects
// in the controller namespace, counts active replicas, and updates the
// Manager's replica count if it changed. It blocks until ctx is cancelled.
func (rw *ReplicaWatcher) Run(ctx context.Context) error {
	rw.logger.Info("replica watcher starting",
		"controllerNamespace", rw.controllerNamespace,
		"watchInterval", rw.watchInterval,
	)

	// Perform an initial check immediately.
	if err := rw.checkReplicas(ctx); err != nil {
		rw.logger.Error("initial replica check failed", "error", err)
	}

	ticker := time.NewTicker(rw.watchInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			rw.logger.Info("replica watcher stopping")
			return ctx.Err()
		case <-ticker.C:
			if err := rw.checkReplicas(ctx); err != nil {
				rw.logger.Error("replica check failed", "error", err)
			}
		}
	}
}

// checkReplicas lists Lease objects in the controller namespace, filters
// for wormsign-owned Leases that are still active, and updates the Manager
// if the replica count changed.
func (rw *ReplicaWatcher) checkReplicas(ctx context.Context) error {
	leases, err := rw.clientset.CoordinationV1().Leases(rw.controllerNamespace).List(
		ctx, metav1.ListOptions{},
	)
	if err != nil {
		return fmt.Errorf("listing leases: %w", err)
	}

	activeCount := rw.countActiveReplicas(leases.Items)

	// Ensure at least 1 replica (this replica is running).
	if activeCount < 1 {
		activeCount = 1
	}

	rw.manager.mu.RLock()
	current := rw.manager.numReplicas
	rw.manager.mu.RUnlock()

	if activeCount != current {
		rw.logger.Info("replica count changed",
			"previous", current,
			"current", activeCount,
		)
		rw.manager.SetNumReplicas(activeCount)
	}

	return nil
}

// countActiveReplicas counts the number of Lease objects that belong to
// wormsign controller replicas and are still considered active (renewed
// within the expiration threshold).
func (rw *ReplicaWatcher) countActiveReplicas(leases []coordinationv1.Lease) int {
	now := rw.nowFunc()
	active := 0

	for _, lease := range leases {
		if !strings.HasPrefix(lease.Name, leasePrefix) {
			continue
		}

		// Skip the leader election lease itself (wormsign-leader).
		if lease.Name == "wormsign-leader" {
			continue
		}

		// Check if the Lease has been renewed recently.
		if lease.Spec.RenewTime == nil {
			continue
		}

		elapsed := now.Sub(lease.Spec.RenewTime.Time)
		if elapsed <= leaseExpirationThreshold {
			active++
		} else {
			rw.logger.Debug("replica lease expired",
				"lease", lease.Name,
				"lastRenew", lease.Spec.RenewTime.Time,
				"elapsed", elapsed,
			)
		}
	}

	return active
}
