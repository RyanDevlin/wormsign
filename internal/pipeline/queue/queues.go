// Package queue provides per-stage work queues for the Wormsign pipeline.
// Each pipeline stage (detection, correlation, gathering, analysis, sink)
// has its own work queue backed by client-go's workqueue package.
//
// The analysis queue implements severity-based priority ordering so that
// critical events are processed before warning and info events.
package queue

import (
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/k8s-wormsign/k8s-wormsign/internal/metrics"
	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
	"k8s.io/client-go/util/workqueue"
)

// Stage identifies a pipeline stage for metrics and logging.
type Stage string

const (
	StageDetection   Stage = "detection"
	StageCorrelation Stage = "correlation"
	StageGathering   Stage = "gathering"
	StageAnalysis    Stage = "analysis"
	StageSink        Stage = "sink"
)

// AnalysisItem wraps a DiagnosticBundle for the analysis queue, carrying
// severity metadata for priority ordering. Critical items are dequeued
// before warning, and warning before info.
type AnalysisItem struct {
	Bundle   model.DiagnosticBundle
	Severity model.Severity
}

// SinkItem wraps an RCAReport for the sink delivery queue.
type SinkItem struct {
	Report *model.RCAReport
}

// GatherItem represents a work item for the gathering stage. It carries
// either a single FaultEvent or a SuperEvent for diagnostic collection.
type GatherItem struct {
	FaultEvent *model.FaultEvent
	SuperEvent *model.SuperEvent
}

// Queues holds all per-stage work queues for the pipeline.
type Queues struct {
	Detection   workqueue.RateLimitingInterface
	Correlation workqueue.RateLimitingInterface
	Gathering   workqueue.RateLimitingInterface
	Analysis    *PriorityQueue
	Sink        workqueue.Interface

	metrics *metrics.Metrics
	logger  *slog.Logger
	stopCh  chan struct{}
	once    sync.Once
}

// NewQueues creates all pipeline work queues and starts the metrics reporter.
func NewQueues(m *metrics.Metrics, logger *slog.Logger) (*Queues, error) {
	if m == nil {
		return nil, fmt.Errorf("metrics must not be nil")
	}
	if logger == nil {
		logger = slog.Default()
	}

	q := &Queues{
		Detection: workqueue.NewRateLimitingQueueWithConfig(
			workqueue.DefaultControllerRateLimiter(),
			workqueue.RateLimitingQueueConfig{Name: "detection"},
		),
		Correlation: workqueue.NewRateLimitingQueueWithConfig(
			workqueue.DefaultControllerRateLimiter(),
			workqueue.RateLimitingQueueConfig{Name: "correlation"},
		),
		Gathering: workqueue.NewRateLimitingQueueWithConfig(
			workqueue.DefaultControllerRateLimiter(),
			workqueue.RateLimitingQueueConfig{Name: "gathering"},
		),
		Analysis: NewPriorityQueue("analysis"),
		Sink: workqueue.NewWithConfig(workqueue.QueueConfig{
			Name: "sink",
		}),
		metrics: m,
		logger:  logger,
		stopCh:  make(chan struct{}),
	}

	return q, nil
}

// StartMetricsReporter starts a goroutine that periodically reports
// queue depths to Prometheus metrics. It reports every 5 seconds.
func (q *Queues) StartMetricsReporter() {
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-q.stopCh:
				return
			case <-ticker.C:
				q.reportMetrics()
			}
		}
	}()
}

// reportMetrics updates Prometheus gauge metrics with current queue depths.
func (q *Queues) reportMetrics() {
	q.metrics.PipelineQueueDepth.WithLabelValues(string(StageDetection)).Set(float64(q.Detection.Len()))
	q.metrics.PipelineQueueDepth.WithLabelValues(string(StageCorrelation)).Set(float64(q.Correlation.Len()))
	q.metrics.PipelineQueueDepth.WithLabelValues(string(StageGathering)).Set(float64(q.Gathering.Len()))
	q.metrics.PipelineQueueDepth.WithLabelValues(string(StageAnalysis)).Set(float64(q.Analysis.Len()))
	q.metrics.PipelineQueueDepth.WithLabelValues(string(StageSink)).Set(float64(q.Sink.Len()))
}

// ShutDown gracefully shuts down all queues. After this call, no new items
// can be added and Get calls will return shutdown=true.
func (q *Queues) ShutDown() {
	q.once.Do(func() {
		close(q.stopCh)
	})
	q.Detection.ShutDown()
	q.Correlation.ShutDown()
	q.Gathering.ShutDown()
	q.Analysis.ShutDown()
	q.Sink.ShutDown()
	q.logger.Info("all pipeline queues shut down")
}

// Depths returns a snapshot of all queue depths, keyed by stage name.
func (q *Queues) Depths() map[Stage]int {
	return map[Stage]int{
		StageDetection:   q.Detection.Len(),
		StageCorrelation: q.Correlation.Len(),
		StageGathering:   q.Gathering.Len(),
		StageAnalysis:    q.Analysis.Len(),
		StageSink:        q.Sink.Len(),
	}
}

// PriorityQueue implements a severity-based priority queue for analysis items.
// Critical items are dequeued before warning, which are dequeued before info.
// Within the same severity, items are dequeued in FIFO order.
//
// It exposes an interface compatible with workqueue usage patterns: Add, Get,
// Done, Len, ShutDown, ShuttingDown.
type PriorityQueue struct {
	mu       sync.Mutex
	cond     *sync.Cond
	name     string
	queues   [3][]AnalysisItem // index 0=critical, 1=warning, 2=info
	shutdown bool
	inflight int
}

// NewPriorityQueue creates a new severity-based priority queue.
func NewPriorityQueue(name string) *PriorityQueue {
	pq := &PriorityQueue{
		name: name,
	}
	pq.cond = sync.NewCond(&pq.mu)
	return pq
}

// severityIndex maps severity to priority index (lower = higher priority).
func severityIndex(s model.Severity) int {
	switch s {
	case model.SeverityCritical:
		return 0
	case model.SeverityWarning:
		return 1
	default:
		return 2
	}
}

// Add enqueues an AnalysisItem. Items are ordered by severity: critical
// first, then warning, then info.
func (pq *PriorityQueue) Add(item AnalysisItem) {
	pq.mu.Lock()
	defer pq.mu.Unlock()

	if pq.shutdown {
		return
	}

	idx := severityIndex(item.Severity)
	pq.queues[idx] = append(pq.queues[idx], item)
	pq.cond.Signal()
}

// Get blocks until an item is available or the queue is shut down.
// Returns the highest-priority item and shutdown=false, or a zero-value
// item and shutdown=true if the queue has been shut down.
func (pq *PriorityQueue) Get() (AnalysisItem, bool) {
	pq.mu.Lock()
	defer pq.mu.Unlock()

	for pq.totalLen() == 0 && !pq.shutdown {
		pq.cond.Wait()
	}

	if pq.shutdown && pq.totalLen() == 0 {
		return AnalysisItem{}, true
	}

	// Dequeue from the highest-priority non-empty queue.
	for i := range pq.queues {
		if len(pq.queues[i]) > 0 {
			item := pq.queues[i][0]
			pq.queues[i] = pq.queues[i][1:]
			pq.inflight++
			return item, false
		}
	}

	// Should not reach here if totalLen > 0.
	return AnalysisItem{}, true
}

// Done marks that processing of the item obtained from Get has finished.
func (pq *PriorityQueue) Done() {
	pq.mu.Lock()
	defer pq.mu.Unlock()
	if pq.inflight > 0 {
		pq.inflight--
	}
	pq.cond.Broadcast()
}

// Len returns the total number of items waiting in the queue (not including
// in-flight items).
func (pq *PriorityQueue) Len() int {
	pq.mu.Lock()
	defer pq.mu.Unlock()
	return pq.totalLen()
}

// totalLen returns the total count without locking. Caller must hold mu.
func (pq *PriorityQueue) totalLen() int {
	total := 0
	for _, q := range pq.queues {
		total += len(q)
	}
	return total
}

// ShutDown signals the queue to stop. Pending items can still be drained
// via Get until the queue is empty.
func (pq *PriorityQueue) ShutDown() {
	pq.mu.Lock()
	defer pq.mu.Unlock()
	pq.shutdown = true
	pq.cond.Broadcast()
}

// ShuttingDown reports whether ShutDown has been called.
func (pq *PriorityQueue) ShuttingDown() bool {
	pq.mu.Lock()
	defer pq.mu.Unlock()
	return pq.shutdown
}

// Drain removes and returns all items from the queue in priority order.
// This is used during graceful shutdown to process remaining items.
func (pq *PriorityQueue) Drain() []AnalysisItem {
	pq.mu.Lock()
	defer pq.mu.Unlock()

	var items []AnalysisItem
	for i := range pq.queues {
		items = append(items, pq.queues[i]...)
		pq.queues[i] = nil
	}
	return items
}

// WaitForInflight blocks until all in-flight items have called Done, or
// until the timeout elapses. Returns true if all items completed.
func (pq *PriorityQueue) WaitForInflight(timeout time.Duration) bool {
	done := make(chan struct{})
	go func() {
		pq.mu.Lock()
		defer pq.mu.Unlock()
		for pq.inflight > 0 {
			pq.cond.Wait()
		}
		close(done)
	}()

	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}
