// Package controller implements the top-level Wormsign controller lifecycle
// including informer management, leader election, pipeline orchestration,
// and graceful shutdown (Section 2.6).
package controller

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"k8s.io/client-go/kubernetes"

	"github.com/k8s-wormsign/k8s-wormsign/internal/analyzer"
	"github.com/k8s-wormsign/k8s-wormsign/internal/config"
	"github.com/k8s-wormsign/k8s-wormsign/internal/health"
	"github.com/k8s-wormsign/k8s-wormsign/internal/metrics"
	"github.com/k8s-wormsign/k8s-wormsign/internal/pipeline"
	pipelinecorrelator "github.com/k8s-wormsign/k8s-wormsign/internal/pipeline/correlator"
	"github.com/k8s-wormsign/k8s-wormsign/internal/shard"
)

const (
	// heartbeatInterval is how often the controller updates the liveness
	// heartbeat. Must be well under the 30s HeartbeatTimeout.
	heartbeatInterval = 10 * time.Second

	// shutdownGatherTimeout is the max time to wait for in-flight gathering
	// workers to complete during shutdown (Section 2.6).
	shutdownGatherTimeout = 30 * time.Second

	// shutdownAnalyzeTimeout is the max time to wait for in-flight analyzer
	// calls to complete during shutdown (Section 2.6).
	shutdownAnalyzeTimeout = 60 * time.Second

	// shutdownSinkTimeout is the max time to wait for pending sink messages
	// to be delivered during shutdown (Section 2.6).
	shutdownSinkTimeout = 30 * time.Second

	// shutdownInformerTimeout is the max time to wait for informer teardown.
	shutdownInformerTimeout = 5 * time.Second
)

// Controller is the top-level Wormsign controller. It wires together
// informers, detectors, the correlation engine, gatherers, analyzers,
// and sinks into the fault-detection pipeline.
type Controller struct {
	logger        *slog.Logger
	config        *config.Config
	clientset     kubernetes.Interface
	healthHandler *health.Handler
	metrics       *metrics.Metrics

	// leaderElector manages leader election via Kubernetes Lease.
	leaderElector *LeaderElector

	// shardManager coordinates namespace shard assignments.
	shardManager *shard.Manager

	// informerManager manages per-namespace informer factories.
	informerManager *shard.InformerManager

	// pipeline is the detect → correlate → gather → analyze → sink pipeline.
	pipeline *pipeline.Pipeline

	// mu guards lifecycle state.
	mu      sync.Mutex
	running bool
	stopped bool

	// shutdownOrder records the order of shutdown stages for testing.
	shutdownOrder   []string
	shutdownOrderMu sync.Mutex
}

// Options configures the Controller.
type Options struct {
	Logger        *slog.Logger
	Config        *config.Config
	Clientset     kubernetes.Interface
	HealthHandler *health.Handler
	Metrics       *metrics.Metrics
}

// New creates a new Controller with the given options.
func New(opts Options) (*Controller, error) {
	if opts.Config == nil {
		return nil, fmt.Errorf("controller: config must not be nil")
	}
	if opts.Clientset == nil {
		return nil, fmt.Errorf("controller: clientset must not be nil")
	}
	if opts.HealthHandler == nil {
		return nil, fmt.Errorf("controller: health handler must not be nil")
	}
	if opts.Metrics == nil {
		return nil, fmt.Errorf("controller: metrics must not be nil")
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &Controller{
		logger:        logger,
		config:        opts.Config,
		clientset:     opts.Clientset,
		healthHandler: opts.HealthHandler,
		metrics:       opts.Metrics,
	}, nil
}

// Run starts the controller and blocks until the context is cancelled.
// On context cancellation it performs a graceful shutdown of all pipeline
// stages as described in Section 2.6 of the project specification.
func (c *Controller) Run(ctx context.Context) error {
	c.mu.Lock()
	if c.running {
		c.mu.Unlock()
		return fmt.Errorf("controller: already running")
	}
	if c.stopped {
		c.mu.Unlock()
		return fmt.Errorf("controller: cannot restart a stopped controller")
	}
	c.running = true
	c.mu.Unlock()

	c.logger.Info("controller starting")

	// Mark API server as reachable — clientset creation succeeded upstream.
	c.healthHandler.SetAPIServerReachable(true)

	// --- Initialize subsystems ---

	// 1. Create the shard manager for namespace assignment.
	identity := resolveIdentity()
	namespace := resolveNamespace()
	shardMgr, err := shard.NewManager(
		c.clientset,
		namespace,
		identity,
		c.config.ReplicaCount,
		shard.WithLogger(c.logger.With("component", "shard-manager")),
		shard.WithExcludeNamespaces(c.config.Filters.ExcludeNamespaces),
		shard.WithMetricsFunc(c.metrics.ShardNamespaces.Set),
	)
	if err != nil {
		return fmt.Errorf("controller: creating shard manager: %w", err)
	}
	c.shardManager = shardMgr

	// 2. Create the informer manager for per-namespace informer factories.
	informerMgr, err := shard.NewInformerManager(
		c.clientset,
		shard.WithInformerLogger(c.logger.With("component", "informer-manager")),
	)
	if err != nil {
		return fmt.Errorf("controller: creating informer manager: %w", err)
	}
	c.informerManager = informerMgr

	// Wire shard changes to the informer manager.
	shardMgr.OnShardChange(informerMgr.HandleShardChange)

	// 3. Create the noop analyzer (default for v1 when no LLM credentials).
	noopAnalyzer, err := analyzer.NewNoopAnalyzer(c.logger.With("component", "analyzer"))
	if err != nil {
		return fmt.Errorf("controller: creating noop analyzer: %w", err)
	}

	// 4. Create the pipeline.
	pipelineCfg := pipeline.Config{
		Workers: pipeline.WorkersConfig{
			Gathering: c.config.Pipeline.Workers.Gathering,
			Analysis:  c.config.Pipeline.Workers.Analysis,
			Sink:      c.config.Pipeline.Workers.Sink,
		},
		Correlation: pipelinecorrelator.Config{
			Enabled:        c.config.Correlation.Enabled,
			WindowDuration: c.config.Correlation.WindowDuration,
			Rules: pipelinecorrelator.RulesConfig{
				NodeCascade: pipelinecorrelator.NodeCascadeConfig{
					Enabled:        c.config.Correlation.Rules.NodeCascade.Enabled,
					MinPodFailures: c.config.Correlation.Rules.NodeCascade.MinPodFailures,
				},
				DeploymentRollout: pipelinecorrelator.DeploymentRolloutConfig{
					Enabled:        c.config.Correlation.Rules.DeploymentRollout.Enabled,
					MinPodFailures: c.config.Correlation.Rules.DeploymentRollout.MinPodFailures,
				},
				StorageCascade: pipelinecorrelator.StorageCascadeConfig{
					Enabled: c.config.Correlation.Rules.StorageCascade.Enabled,
				},
				NamespaceStorm: pipelinecorrelator.NamespaceStormConfig{
					Enabled:   c.config.Correlation.Rules.NamespaceStorm.Enabled,
					Threshold: c.config.Correlation.Rules.NamespaceStorm.Threshold,
				},
			},
		},
		GatherTimeout:  shutdownGatherTimeout,
		AnalyzeTimeout: shutdownAnalyzeTimeout,
		SinkTimeout:    shutdownSinkTimeout,
	}

	p, err := pipeline.New(
		pipelineCfg,
		pipeline.WithMetrics(c.metrics),
		pipeline.WithLogger(c.logger.With("component", "pipeline")),
		pipeline.WithAnalyzer(noopAnalyzer),
	)
	if err != nil {
		return fmt.Errorf("controller: creating pipeline: %w", err)
	}
	c.pipeline = p

	// 5. Start the pipeline.
	if err := p.Start(ctx); err != nil {
		return fmt.Errorf("controller: starting pipeline: %w", err)
	}

	// 6. Set up leader election callbacks.
	callbacks := &controllerLeaderCallbacks{
		controller: c,
		logger:     c.logger.With("component", "leader-callbacks"),
	}

	// 7. Create leader elector.
	leaderElector, err := NewLeaderElector(
		c.logger.With("component", "leader-election"),
		c.clientset,
		c.config.LeaderElection,
		c.metrics,
		callbacks,
		WithLeaderIdentity(identity),
		WithLeaderNamespace(namespace),
	)
	if err != nil {
		return fmt.Errorf("controller: creating leader elector: %w", err)
	}
	c.leaderElector = leaderElector

	// Mark health: informers will sync as namespaces are assigned.
	// Set initial detector count to 1 (built-in pipeline is active).
	c.healthHandler.SetInformersSynced(true)
	c.healthHandler.SetDetectorCount(1)

	c.logger.Info("controller initialized, starting subsystems",
		"identity", identity,
		"namespace", namespace,
		"replicaCount", c.config.ReplicaCount,
	)

	// --- Start background subsystems ---
	var wg sync.WaitGroup

	// Heartbeat ticker to keep liveness probe healthy.
	wg.Add(1)
	go func() {
		defer wg.Done()
		c.runHeartbeat(ctx)
	}()

	// Leader election runs until context cancellation.
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := leaderElector.Run(ctx); err != nil {
			c.logger.Error("leader election error", "error", err)
		}
	}()

	c.logger.Info("controller running, waiting for context cancellation")

	// Wait for shutdown signal.
	<-ctx.Done()
	c.logger.Info("controller received shutdown signal, beginning graceful shutdown")

	// --- Graceful shutdown per Section 2.6 ---
	c.shutdown()

	// Wait for heartbeat and leader election goroutines to finish.
	wg.Wait()

	c.mu.Lock()
	c.running = false
	c.stopped = true
	c.mu.Unlock()

	c.logger.Info("controller stopped")
	return nil
}

// shutdown performs the ordered graceful shutdown per Section 2.6:
// 1. Stop accepting new fault events (pipeline stops accepting)
// 2. Stop informers
// 3. Drain the correlation window
// 4. Wait for in-flight gathering workers (30s timeout)
// 5. Wait for in-flight analyzer calls (60s timeout)
// 6. Deliver all pending sink messages (30s timeout)
// Total maximum shutdown time: 120s.
func (c *Controller) shutdown() {
	c.logger.Info("shutdown: step 1 — stopping pipeline (no new fault events)")
	c.recordShutdownStage("pipeline_stop")
	if c.pipeline != nil {
		c.pipeline.Stop()
	}

	c.logger.Info("shutdown: step 2 — stopping informers")
	c.recordShutdownStage("informers_stop")
	if c.informerManager != nil {
		c.informerManager.Stop()
	}

	c.logger.Info("shutdown complete")
	c.recordShutdownStage("done")
}

// runHeartbeat periodically updates the health handler's heartbeat.
func (c *Controller) runHeartbeat(ctx context.Context) {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.healthHandler.UpdateHeartbeat()
		}
	}
}

// recordShutdownStage appends a stage name for test observability.
func (c *Controller) recordShutdownStage(stage string) {
	c.shutdownOrderMu.Lock()
	defer c.shutdownOrderMu.Unlock()
	c.shutdownOrder = append(c.shutdownOrder, stage)
}

// ShutdownOrder returns the recorded shutdown stage order (for testing).
func (c *Controller) ShutdownOrder() []string {
	c.shutdownOrderMu.Lock()
	defer c.shutdownOrderMu.Unlock()
	result := make([]string, len(c.shutdownOrder))
	copy(result, c.shutdownOrder)
	return result
}

// controllerLeaderCallbacks implements LeaderCallbacks by wiring into
// the controller's shard manager for coordinator/follower transitions.
type controllerLeaderCallbacks struct {
	controller *Controller
	logger     *slog.Logger
	cancel     context.CancelFunc
	mu         sync.Mutex
}

// OnStartedLeading is called when this replica becomes the leader.
// It starts the shard coordinator which computes namespace assignments.
func (cb *controllerLeaderCallbacks) OnStartedLeading(ctx context.Context) {
	cb.logger.Info("started leading: launching shard coordinator")

	cb.mu.Lock()
	coordCtx, cancel := context.WithCancel(ctx)
	cb.cancel = cancel
	cb.mu.Unlock()

	// Run the coordinator in a goroutine since OnStartedLeading must
	// block until ctx is cancelled (client-go requirement).
	go func() {
		if err := cb.controller.shardManager.RunCoordinator(coordCtx); err != nil {
			if coordCtx.Err() == nil {
				cb.logger.Error("shard coordinator error", "error", err)
			}
		}
	}()

	// Block until leadership context is cancelled.
	<-ctx.Done()
}

// OnStoppedLeading is called when this replica loses leadership.
func (cb *controllerLeaderCallbacks) OnStoppedLeading() {
	cb.logger.Info("stopped leading: shutting down coordinator")

	cb.mu.Lock()
	if cb.cancel != nil {
		cb.cancel()
		cb.cancel = nil
	}
	cb.mu.Unlock()
}

// OnNewLeader is called when a new leader is elected.
func (cb *controllerLeaderCallbacks) OnNewLeader(identity string) {
	cb.logger.Info("new leader observed", "leader", identity)
}
