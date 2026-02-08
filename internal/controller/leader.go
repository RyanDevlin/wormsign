// Package controller implements the top-level Wormsign controller lifecycle.
package controller

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"

	"github.com/k8s-wormsign/k8s-wormsign/internal/config"
	"github.com/k8s-wormsign/k8s-wormsign/internal/metrics"
)

const (
	// DefaultLeaseName is the well-known name of the Lease object used for
	// leader election per Section 3.6 of the project specification.
	DefaultLeaseName = "wormsign-leader"

	// DefaultControllerNamespace is the fallback namespace when the
	// POD_NAMESPACE environment variable is not set.
	DefaultControllerNamespace = "wormsign-system"
)

// LeaderCallbacks defines the callbacks invoked during leader election
// lifecycle transitions. This interface allows the controller to plug in
// actions for starting/stopping cluster-scoped detectors and the shard
// coordinator without leader.go knowing those implementations.
type LeaderCallbacks interface {
	// OnStartedLeading is called when this replica becomes the leader.
	// The provided context is cancelled when leadership is lost.
	OnStartedLeading(ctx context.Context)

	// OnStoppedLeading is called when this replica loses leadership.
	OnStoppedLeading()

	// OnNewLeader is called when a new leader is elected. The identity
	// parameter is the leader's identity string.
	OnNewLeader(identity string)
}

// LeaderElector manages leader election using a Kubernetes Lease object.
type LeaderElector struct {
	logger    *slog.Logger
	clientset kubernetes.Interface
	config    config.LeaderElectionConfig
	metrics   *metrics.Metrics
	callbacks LeaderCallbacks

	// identity is the unique identifier for this replica. Typically the
	// pod name, sourced from the POD_NAME environment variable.
	identity string

	// namespace is the namespace where the Lease object is created.
	namespace string

	// leaseName is the name of the Lease object.
	leaseName string

	// isLeader tracks whether this instance currently holds the lease.
	isLeader bool
}

// LeaderElectorOption configures a LeaderElector.
type LeaderElectorOption func(*LeaderElector)

// WithLeaderIdentity overrides the identity string. If not set, defaults to
// the POD_NAME environment variable or the hostname.
func WithLeaderIdentity(identity string) LeaderElectorOption {
	return func(le *LeaderElector) {
		le.identity = identity
	}
}

// WithLeaderNamespace overrides the namespace for the Lease object.
func WithLeaderNamespace(namespace string) LeaderElectorOption {
	return func(le *LeaderElector) {
		le.namespace = namespace
	}
}

// WithLeaseName overrides the Lease object name.
func WithLeaseName(name string) LeaderElectorOption {
	return func(le *LeaderElector) {
		le.leaseName = name
	}
}

// NewLeaderElector creates a new LeaderElector. The clientset, config,
// metrics, and callbacks must not be nil.
func NewLeaderElector(
	logger *slog.Logger,
	clientset kubernetes.Interface,
	cfg config.LeaderElectionConfig,
	m *metrics.Metrics,
	callbacks LeaderCallbacks,
	opts ...LeaderElectorOption,
) (*LeaderElector, error) {
	if clientset == nil {
		return nil, fmt.Errorf("leader: clientset must not be nil")
	}
	if m == nil {
		return nil, fmt.Errorf("leader: metrics must not be nil")
	}
	if callbacks == nil {
		return nil, fmt.Errorf("leader: callbacks must not be nil")
	}
	if logger == nil {
		logger = slog.Default()
	}

	le := &LeaderElector{
		logger:    logger,
		clientset: clientset,
		config:    cfg,
		metrics:   m,
		callbacks: callbacks,
		leaseName: DefaultLeaseName,
		namespace: resolveNamespace(),
	}

	for _, opt := range opts {
		opt(le)
	}

	if le.identity == "" {
		le.identity = resolveIdentity()
	}

	// Ensure metric starts at 0 (not leader).
	le.metrics.LeaderIsLeader.Set(0)

	return le, nil
}

// Run starts the leader election loop. It blocks until the context is
// cancelled or an unrecoverable error occurs. When this replica wins the
// election, it calls OnStartedLeading. When it loses, OnStoppedLeading.
func (le *LeaderElector) Run(ctx context.Context) error {
	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      le.leaseName,
			Namespace: le.namespace,
		},
		Client: le.clientset.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: le.identity,
		},
	}

	le.logger.Info("starting leader election",
		"identity", le.identity,
		"namespace", le.namespace,
		"leaseName", le.leaseName,
		"leaseDuration", le.config.LeaseDuration,
		"renewDeadline", le.config.RenewDeadline,
		"retryPeriod", le.config.RetryPeriod,
	)

	elector, err := leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{
		Lock:            lock,
		LeaseDuration:   le.config.LeaseDuration,
		RenewDeadline:   le.config.RenewDeadline,
		RetryPeriod:     le.config.RetryPeriod,
		ReleaseOnCancel: true,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				le.onStartedLeading(ctx)
			},
			OnStoppedLeading: func() {
				le.onStoppedLeading()
			},
			OnNewLeader: func(identity string) {
				le.onNewLeader(identity)
			},
		},
	})
	if err != nil {
		return fmt.Errorf("creating leader elector: %w", err)
	}

	// Run blocks until ctx is cancelled or the elector exits.
	elector.Run(ctx)
	return nil
}

// Identity returns the identity string used for leader election.
func (le *LeaderElector) Identity() string {
	return le.identity
}

// IsLeader returns whether this replica currently holds the leader lease.
func (le *LeaderElector) IsLeader() bool {
	return le.isLeader
}

func (le *LeaderElector) onStartedLeading(ctx context.Context) {
	le.isLeader = true
	le.metrics.LeaderIsLeader.Set(1)
	le.logger.Info("this replica is now the leader",
		"identity", le.identity,
	)
	le.callbacks.OnStartedLeading(ctx)
}

func (le *LeaderElector) onStoppedLeading() {
	le.isLeader = false
	le.metrics.LeaderIsLeader.Set(0)
	le.logger.Warn("this replica lost leadership",
		"identity", le.identity,
	)
	le.callbacks.OnStoppedLeading()
}

func (le *LeaderElector) onNewLeader(identity string) {
	if identity == le.identity {
		le.logger.Info("this replica was elected leader",
			"identity", identity,
		)
	} else {
		le.logger.Info("new leader elected",
			"leader", identity,
			"self", le.identity,
		)
	}
	le.callbacks.OnNewLeader(identity)
}

// resolveIdentity determines the identity string for this replica.
// It prefers the POD_NAME environment variable (set by the downward API in
// Kubernetes), falling back to the hostname.
func resolveIdentity() string {
	if name := os.Getenv("POD_NAME"); name != "" {
		return name
	}
	hostname, err := os.Hostname()
	if err != nil {
		return fmt.Sprintf("wormsign-%d", time.Now().UnixNano())
	}
	return hostname
}

// resolveNamespace determines the namespace for the Lease object.
// It prefers the POD_NAMESPACE environment variable (set by the downward API),
// falling back to DefaultControllerNamespace.
func resolveNamespace() string {
	if ns := os.Getenv("POD_NAMESPACE"); ns != "" {
		return ns
	}
	return DefaultControllerNamespace
}
