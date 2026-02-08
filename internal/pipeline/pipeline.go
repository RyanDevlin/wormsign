// Package pipeline orchestrates the Wormsign fault-detection pipeline:
//
//	Detect → Correlate → Gather → Analyze → Sink
//
// It owns all per-stage work queues and workers, wires the filter engine
// into the detection stage, and manages the full lifecycle including
// graceful shutdown with per-stage drain timeouts.
package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/k8s-wormsign/k8s-wormsign/internal/analyzer"
	"github.com/k8s-wormsign/k8s-wormsign/internal/filter"
	"github.com/k8s-wormsign/k8s-wormsign/internal/gatherer"
	"github.com/k8s-wormsign/k8s-wormsign/internal/metrics"
	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
	"github.com/k8s-wormsign/k8s-wormsign/internal/pipeline/correlator"
	"github.com/k8s-wormsign/k8s-wormsign/internal/pipeline/queue"
)

// Sink delivers RCA reports to external systems (Slack, PagerDuty, S3, etc.).
// The pipeline calls Deliver for each active sink.
type Sink interface {
	// Name returns the sink identifier (e.g., "slack", "pagerduty", "log").
	Name() string

	// Deliver sends the RCA report to the external system.
	// Implementations should handle retries internally if needed.
	Deliver(ctx context.Context, report *model.RCAReport) error
}

// Config holds the pipeline configuration.
type Config struct {
	// Workers configures concurrency per stage.
	Workers WorkersConfig

	// Correlation configures the pre-LLM event correlation engine.
	Correlation correlator.Config

	// GatherTimeout is the maximum time to wait for gathering workers
	// to complete during graceful shutdown.
	GatherTimeout time.Duration

	// AnalyzeTimeout is the maximum time to wait for analysis workers
	// to complete during graceful shutdown.
	AnalyzeTimeout time.Duration

	// SinkTimeout is the maximum time to wait for sink delivery workers
	// to complete during graceful shutdown.
	SinkTimeout time.Duration
}

// WorkersConfig specifies worker concurrency per pipeline stage.
type WorkersConfig struct {
	Gathering int
	Analysis  int
	Sink      int
}

// DefaultConfig returns a Config with spec-defined defaults.
func DefaultConfig() Config {
	return Config{
		Workers: WorkersConfig{
			Gathering: 5,
			Analysis:  2,
			Sink:      3,
		},
		Correlation:    correlator.DefaultConfig(),
		GatherTimeout:  30 * time.Second,
		AnalyzeTimeout: 60 * time.Second,
		SinkTimeout:    30 * time.Second,
	}
}

// ValidateConfig checks that the pipeline configuration is valid.
func ValidateConfig(cfg Config) error {
	if cfg.Workers.Gathering < 1 {
		return fmt.Errorf("pipeline: workers.gathering must be >= 1, got %d", cfg.Workers.Gathering)
	}
	if cfg.Workers.Analysis < 1 {
		return fmt.Errorf("pipeline: workers.analysis must be >= 1, got %d", cfg.Workers.Analysis)
	}
	if cfg.Workers.Sink < 1 {
		return fmt.Errorf("pipeline: workers.sink must be >= 1, got %d", cfg.Workers.Sink)
	}
	if cfg.GatherTimeout <= 0 {
		return fmt.Errorf("pipeline: gatherTimeout must be positive")
	}
	if cfg.AnalyzeTimeout <= 0 {
		return fmt.Errorf("pipeline: analyzeTimeout must be positive")
	}
	if cfg.SinkTimeout <= 0 {
		return fmt.Errorf("pipeline: sinkTimeout must be positive")
	}
	return correlator.ValidateConfig(cfg.Correlation)
}

// Pipeline orchestrates the detect → correlate → gather → analyze → sink
// flow. It owns per-stage work queues and workers.
type Pipeline struct {
	config     Config
	queues     *queue.Queues
	correlator *correlator.Correlator
	filter     *filter.Engine
	gatherers  []gatherer.Gatherer
	analyzer   analyzer.Analyzer
	sinks      []Sink
	metrics    *metrics.Metrics
	logger     *slog.Logger

	// cancel is called to stop the pipeline context.
	cancel context.CancelFunc
	// wg tracks all worker goroutines for clean shutdown.
	wg sync.WaitGroup
	// stopped signals that Stop() was called.
	stopped chan struct{}
	once    sync.Once
}

// New creates a new Pipeline. Call Start to begin processing.
func New(cfg Config, opts ...Option) (*Pipeline, error) {
	if err := ValidateConfig(cfg); err != nil {
		return nil, fmt.Errorf("invalid pipeline config: %w", err)
	}

	p := &Pipeline{
		config:  cfg,
		stopped: make(chan struct{}),
	}

	for _, opt := range opts {
		opt(p)
	}

	// Validate required dependencies.
	if p.metrics == nil {
		return nil, fmt.Errorf("pipeline: metrics must be set (use WithMetrics)")
	}
	if p.logger == nil {
		p.logger = slog.Default()
	}
	if p.analyzer == nil {
		return nil, fmt.Errorf("pipeline: analyzer must be set (use WithAnalyzer)")
	}

	// Create work queues.
	q, err := queue.NewQueues(p.metrics, p.logger)
	if err != nil {
		return nil, fmt.Errorf("pipeline: creating queues: %w", err)
	}
	p.queues = q

	// Create correlator with handler that enqueues results for gathering.
	corr, err := correlator.New(
		cfg.Correlation,
		p.handleCorrelationResult,
		p.metrics,
		p.logger,
	)
	if err != nil {
		return nil, fmt.Errorf("pipeline: creating correlator: %w", err)
	}
	p.correlator = corr

	return p, nil
}

// Option configures the Pipeline.
type Option func(*Pipeline)

// WithMetrics sets the Prometheus metrics instance.
func WithMetrics(m *metrics.Metrics) Option {
	return func(p *Pipeline) {
		p.metrics = m
	}
}

// WithLogger sets the structured logger.
func WithLogger(l *slog.Logger) Option {
	return func(p *Pipeline) {
		p.logger = l
	}
}

// WithFilter sets the filter engine for detection-time filtering.
func WithFilter(f *filter.Engine) Option {
	return func(p *Pipeline) {
		p.filter = f
	}
}

// WithGatherers sets the diagnostic data gatherers.
func WithGatherers(g []gatherer.Gatherer) Option {
	return func(p *Pipeline) {
		p.gatherers = g
	}
}

// WithAnalyzer sets the LLM analysis backend.
func WithAnalyzer(a analyzer.Analyzer) Option {
	return func(p *Pipeline) {
		p.analyzer = a
	}
}

// WithSinks sets the sink delivery targets.
func WithSinks(s []Sink) Option {
	return func(p *Pipeline) {
		p.sinks = s
	}
}

// SubmitFaultEvent is the entry point for the pipeline. Detectors call this
// when a fault is detected. It evaluates filters and, if the event is not
// excluded, enqueues it for correlation.
func (p *Pipeline) SubmitFaultEvent(event *model.FaultEvent, input filter.FilterInput) {
	if event == nil {
		p.logger.Error("nil fault event submitted to pipeline")
		return
	}

	// Evaluate filters if the filter engine is configured.
	if p.filter != nil {
		result := p.filter.Evaluate(input)
		if result.Excluded {
			p.metrics.EventsFilteredTotal.WithLabelValues(
				event.DetectorName,
				event.Resource.Namespace,
				string(result.Reason),
			).Inc()
			p.logger.Debug("fault event filtered",
				"detector", event.DetectorName,
				"resource", event.Resource.String(),
				"reason", result.Reason,
				"fault_event_id", event.ID,
			)
			return
		}
	}

	// Record the fault event metric.
	p.metrics.FaultEventsTotal.WithLabelValues(
		event.DetectorName,
		string(event.Severity),
		event.Resource.Namespace,
	).Inc()

	p.logger.Info("fault event accepted into pipeline",
		"detector", event.DetectorName,
		"resource", event.Resource.String(),
		"severity", event.Severity,
		"fault_event_id", event.ID,
	)

	// Enqueue for correlation.
	p.correlator.Submit(*event)
}

// handleCorrelationResult is the correlator's output handler. It enqueues
// correlated (SuperEvent) and uncorrelated (FaultEvent) results into the
// gathering queue.
func (p *Pipeline) handleCorrelationResult(result correlator.CorrelationResult) {
	for _, se := range result.SuperEvents {
		p.queues.Gathering.Add(queue.GatherItem{SuperEvent: se})
		p.logger.Info("super-event enqueued for gathering",
			"correlation_rule", se.CorrelationRule,
			"primary_resource", se.PrimaryResource.String(),
			"sub_events", len(se.FaultEvents),
			"severity", se.Severity,
		)
	}
	for i := range result.UncorrelatedEvents {
		fe := result.UncorrelatedEvents[i]
		p.queues.Gathering.Add(queue.GatherItem{FaultEvent: &fe})
		p.logger.Debug("uncorrelated event enqueued for gathering",
			"detector", fe.DetectorName,
			"resource", fe.Resource.String(),
			"fault_event_id", fe.ID,
		)
	}
}

// Start launches all workers and the correlator. It returns immediately.
// Call Stop to gracefully shut down the pipeline.
func (p *Pipeline) Start(ctx context.Context) error {
	ctx, p.cancel = context.WithCancel(ctx)

	// Start queue metrics reporter.
	p.queues.StartMetricsReporter()

	// Start correlator.
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.correlator.Run(ctx)
	}()

	// Start gathering workers.
	for i := 0; i < p.config.Workers.Gathering; i++ {
		p.wg.Add(1)
		go func(workerID int) {
			defer p.wg.Done()
			p.runGatheringWorker(ctx, workerID)
		}(i)
	}

	// Start analysis workers.
	for i := 0; i < p.config.Workers.Analysis; i++ {
		p.wg.Add(1)
		go func(workerID int) {
			defer p.wg.Done()
			p.runAnalysisWorker(ctx, workerID)
		}(i)
	}

	// Start sink workers.
	for i := 0; i < p.config.Workers.Sink; i++ {
		p.wg.Add(1)
		go func(workerID int) {
			defer p.wg.Done()
			p.runSinkWorker(ctx, workerID)
		}(i)
	}

	p.logger.Info("pipeline started",
		"gathering_workers", p.config.Workers.Gathering,
		"analysis_workers", p.config.Workers.Analysis,
		"sink_workers", p.config.Workers.Sink,
	)

	return nil
}

// StopAccepting cancels the pipeline context, preventing new fault events
// from being accepted. This is the first step of the graceful shutdown
// sequence per Section 2.6. After calling StopAccepting, the caller should
// stop informers, then call DrainAndShutdown to drain remaining work.
func (p *Pipeline) StopAccepting() {
	p.logger.Info("pipeline: stopping acceptance of new fault events")
	if p.cancel != nil {
		p.cancel()
	}
}

// DrainAndShutdown drains the remaining pipeline stages in order:
//  1. Flush the correlation window (pending correlated events).
//  2. Wait for in-flight gathering workers (with GatherTimeout).
//  3. Wait for in-flight analysis workers (with AnalyzeTimeout).
//  4. Deliver all pending sink messages (with SinkTimeout).
//  5. Shut down all queues and wait for workers.
//
// This should be called after StopAccepting and after informers are stopped.
func (p *Pipeline) DrainAndShutdown() {
	p.logger.Info("pipeline: flushing correlation window")
	p.correlator.Flush()
	p.logger.Info("pipeline: correlation window flushed")

	p.logger.Info("pipeline: waiting for gathering workers to drain",
		"timeout", p.config.GatherTimeout,
	)
	p.drainStageWithTimeout(p.queues.Gathering, "gathering", p.config.GatherTimeout)

	p.logger.Info("pipeline: waiting for analysis workers to drain",
		"timeout", p.config.AnalyzeTimeout,
	)
	p.drainAnalysisWithTimeout(p.config.AnalyzeTimeout)

	p.logger.Info("pipeline: waiting for sink delivery workers to drain",
		"timeout", p.config.SinkTimeout,
	)
	p.drainSinkWithTimeout(p.config.SinkTimeout)

	p.queues.ShutDown()
	p.wg.Wait()
	p.logger.Info("pipeline: shutdown complete")
}

// Stop performs a graceful shutdown of the pipeline following the spec's
// shutdown order:
//  1. Stop accepting new fault events (close detection queue).
//  2. Drain the correlation window (flush pending correlated events).
//  3. Wait for in-flight gathering workers (with GatherTimeout).
//  4. Wait for in-flight analysis workers (with AnalyzeTimeout).
//  5. Deliver all pending sink messages (with SinkTimeout).
//  6. Shut down all queues.
func (p *Pipeline) Stop() {
	p.once.Do(func() {
		close(p.stopped)
		p.logger.Info("pipeline stopping, beginning graceful shutdown")

		// Step 1: Stop accepting new events.
		p.StopAccepting()

		// Steps 2-6: Drain all stages and shut down.
		p.DrainAndShutdown()
	})
}

// drainStageWithTimeout waits for a rate-limiting queue to be drained
// within the given timeout, then shuts it down.
func (p *Pipeline) drainStageWithTimeout(q interface{ Len() int; ShutDown() }, stage string, timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			if q.Len() == 0 {
				close(done)
				return
			}
			<-ticker.C
		}
	}()

	select {
	case <-done:
		p.logger.Info("stage drained successfully", "stage", stage)
	case <-time.After(timeout):
		p.logger.Warn("stage drain timed out, shutting down with items remaining",
			"stage", stage,
			"remaining", q.Len(),
		)
	}
}

// drainAnalysisWithTimeout waits for the priority analysis queue to drain
// all pending and in-flight items.
func (p *Pipeline) drainAnalysisWithTimeout(timeout time.Duration) {
	if p.queues.Analysis.WaitForDrain(timeout) {
		p.logger.Info("analysis stage drained successfully")
	} else {
		p.logger.Warn("analysis drain timed out, shutting down with items remaining",
			"pending", p.queues.Analysis.Len(),
			"in_flight", p.queues.Analysis.InFlight(),
		)
	}
}

// drainSinkWithTimeout waits for the sink queue to drain.
func (p *Pipeline) drainSinkWithTimeout(timeout time.Duration) {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			if p.queues.Sink.Len() == 0 {
				close(done)
				return
			}
			<-ticker.C
		}
	}()

	select {
	case <-done:
		p.logger.Info("sink stage drained successfully")
	case <-time.After(timeout):
		p.logger.Warn("sink drain timed out, shutting down with items remaining",
			"remaining", p.queues.Sink.Len(),
		)
	}
}

// runGatheringWorker processes items from the gathering queue. It runs
// all configured gatherers concurrently for each item and enqueues the
// resulting DiagnosticBundle into the analysis queue.
func (p *Pipeline) runGatheringWorker(ctx context.Context, workerID int) {
	logger := p.logger.With("worker", "gathering", "worker_id", workerID)
	logger.Debug("gathering worker started")

	for {
		obj, shutdown := p.queues.Gathering.Get()
		if shutdown {
			logger.Debug("gathering worker shutting down")
			return
		}

		item, ok := obj.(queue.GatherItem)
		if !ok {
			logger.Error("unexpected item type in gathering queue",
				"type", fmt.Sprintf("%T", obj),
			)
			p.queues.Gathering.Done(obj)
			continue
		}

		p.processGatherItem(ctx, logger, item)
		p.queues.Gathering.Done(obj)
	}
}

// processGatherItem collects diagnostics for a single fault event or super-event.
func (p *Pipeline) processGatherItem(ctx context.Context, logger *slog.Logger, item queue.GatherItem) {
	start := time.Now()

	var bundle *model.DiagnosticBundle
	var severity model.Severity
	var eventID string

	if item.SuperEvent != nil {
		se := item.SuperEvent
		eventID = se.ID
		severity = se.Severity
		logger = logger.With(
			"super_event_id", se.ID,
			"correlation_rule", se.CorrelationRule,
			"primary_resource", se.PrimaryResource.String(),
		)

		// Gather for the primary resource.
		sections := gatherer.RunAll(ctx, p.logger, p.gatherers, se.PrimaryResource)
		bundle = model.NewSuperEventDiagnosticBundle(se, sections)
	} else if item.FaultEvent != nil {
		fe := item.FaultEvent
		eventID = fe.ID
		severity = fe.Severity
		logger = logger.With(
			"fault_event_id", fe.ID,
			"detector", fe.DetectorName,
			"resource", fe.Resource.String(),
		)

		sections := gatherer.RunAll(ctx, p.logger, p.gatherers, fe.Resource)
		bundle = model.NewDiagnosticBundle(fe, sections)
	} else {
		logger.Error("gather item has neither FaultEvent nor SuperEvent")
		return
	}

	duration := time.Since(start)
	logger.Info("diagnostic gathering complete",
		"sections", len(bundle.Sections),
		"duration", duration,
		"fault_event_id", eventID,
	)

	// Record per-gatherer duration metrics.
	for _, s := range bundle.Sections {
		if s.GathererName != "" {
			p.metrics.DiagnosticGatherDuration.WithLabelValues(s.GathererName).Observe(duration.Seconds())
		}
	}

	// Enqueue for analysis.
	p.queues.Analysis.Add(queue.AnalysisItem{
		Bundle:   *bundle,
		Severity: severity,
	})
}

// runAnalysisWorker processes items from the analysis priority queue.
// Critical items are processed before warning and info.
func (p *Pipeline) runAnalysisWorker(ctx context.Context, workerID int) {
	logger := p.logger.With("worker", "analysis", "worker_id", workerID)
	logger.Debug("analysis worker started")

	for {
		item, shutdown := p.queues.Analysis.Get()
		if shutdown {
			logger.Debug("analysis worker shutting down")
			return
		}

		p.processAnalysisItem(ctx, logger, item)
		p.queues.Analysis.Done()
	}
}

// processAnalysisItem sends a DiagnosticBundle to the analyzer and enqueues
// the resulting RCAReport for sink delivery.
func (p *Pipeline) processAnalysisItem(ctx context.Context, logger *slog.Logger, item queue.AnalysisItem) {
	eventID := ""
	if item.Bundle.FaultEvent != nil {
		eventID = item.Bundle.FaultEvent.ID
	} else if item.Bundle.SuperEvent != nil {
		eventID = item.Bundle.SuperEvent.ID
	}

	logger = logger.With(
		"severity", item.Severity,
		"fault_event_id", eventID,
		"backend", p.analyzer.Name(),
	)

	start := time.Now()
	report, err := p.analyzer.Analyze(ctx, item.Bundle)
	duration := time.Since(start)

	p.metrics.AnalyzerLatency.Observe(duration.Seconds())

	if err != nil {
		p.metrics.AnalyzerRequestsTotal.WithLabelValues(p.analyzer.Name(), "error").Inc()
		logger.Error("analysis failed",
			"duration", duration,
			"error", err,
		)

		// Create a fallback report so operators still get diagnostic data.
		report = &model.RCAReport{
			FaultEventID:     eventID,
			Timestamp:        time.Now().UTC(),
			RootCause:        "Automated analysis failed — raw diagnostics attached",
			Severity:         item.Severity,
			Category:         "unknown",
			Confidence:       0.0,
			RawAnalysis:      err.Error(),
			DiagnosticBundle: item.Bundle,
			AnalyzerBackend:  p.analyzer.Name(),
		}
	} else {
		p.metrics.AnalyzerRequestsTotal.WithLabelValues(p.analyzer.Name(), "success").Inc()
		p.metrics.AnalyzerTokensUsedTotal.WithLabelValues(p.analyzer.Name(), "input").Add(float64(report.TokensUsed.Input))
		p.metrics.AnalyzerTokensUsedTotal.WithLabelValues(p.analyzer.Name(), "output").Add(float64(report.TokensUsed.Output))

		logger.Info("analysis complete",
			"duration", duration,
			"root_cause", report.RootCause,
			"category", report.Category,
			"confidence", report.Confidence,
		)
	}

	// Enqueue for sink delivery.
	p.queues.Sink.Add(queue.SinkItem{Report: report})
}

// runSinkWorker processes items from the sink queue. Each item is delivered
// to all configured sinks. One sink's failure does not block others.
func (p *Pipeline) runSinkWorker(ctx context.Context, workerID int) {
	logger := p.logger.With("worker", "sink", "worker_id", workerID)
	logger.Debug("sink worker started")

	for {
		obj, shutdown := p.queues.Sink.Get()
		if shutdown {
			logger.Debug("sink worker shutting down")
			return
		}

		item, ok := obj.(queue.SinkItem)
		if !ok {
			logger.Error("unexpected item type in sink queue",
				"type", fmt.Sprintf("%T", obj),
			)
			p.queues.Sink.Done(obj)
			continue
		}

		p.processSinkItem(ctx, logger, item)
		p.queues.Sink.Done(obj)
	}
}

// processSinkItem delivers an RCA report to all configured sinks.
func (p *Pipeline) processSinkItem(ctx context.Context, logger *slog.Logger, item queue.SinkItem) {
	if item.Report == nil {
		logger.Error("nil report in sink item")
		return
	}

	logger = logger.With(
		"fault_event_id", item.Report.FaultEventID,
		"severity", item.Report.Severity,
		"root_cause", item.Report.RootCause,
	)

	for _, s := range p.sinks {
		sinkLogger := logger.With("sink", s.Name())
		if err := s.Deliver(ctx, item.Report); err != nil {
			p.metrics.SinkDeliveriesTotal.WithLabelValues(s.Name(), "failure").Inc()
			sinkLogger.Error("sink delivery failed",
				"error", err,
			)
		} else {
			p.metrics.SinkDeliveriesTotal.WithLabelValues(s.Name(), "success").Inc()
			sinkLogger.Debug("sink delivery succeeded")
		}
	}
}

// Queues returns the underlying work queues for external access (e.g., tests).
func (p *Pipeline) Queues() *queue.Queues {
	return p.queues
}
