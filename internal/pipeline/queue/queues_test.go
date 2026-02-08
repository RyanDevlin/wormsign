package queue

import (
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/k8s-wormsign/k8s-wormsign/internal/metrics"
	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
	"github.com/prometheus/client_golang/prometheus"
)

func newTestMetrics(t *testing.T) *metrics.Metrics {
	t.Helper()
	reg := prometheus.NewRegistry()
	return metrics.NewMetrics(reg)
}

func TestNewQueues(t *testing.T) {
	m := newTestMetrics(t)
	logger := slog.Default()

	q, err := NewQueues(m, logger)
	if err != nil {
		t.Fatalf("NewQueues() error = %v", err)
	}

	if q.Detection == nil {
		t.Error("Detection queue is nil")
	}
	if q.Correlation == nil {
		t.Error("Correlation queue is nil")
	}
	if q.Gathering == nil {
		t.Error("Gathering queue is nil")
	}
	if q.Analysis == nil {
		t.Error("Analysis queue is nil")
	}
	if q.Sink == nil {
		t.Error("Sink queue is nil")
	}
}

func TestNewQueues_NilMetrics(t *testing.T) {
	_, err := NewQueues(nil, slog.Default())
	if err == nil {
		t.Fatal("expected error for nil metrics, got nil")
	}
}

func TestNewQueues_NilLogger(t *testing.T) {
	m := newTestMetrics(t)
	q, err := NewQueues(m, nil)
	if err != nil {
		t.Fatalf("NewQueues() with nil logger should not error, got %v", err)
	}
	if q == nil {
		t.Fatal("expected non-nil Queues")
	}
}

func TestQueues_Depths(t *testing.T) {
	m := newTestMetrics(t)
	q, err := NewQueues(m, slog.Default())
	if err != nil {
		t.Fatalf("NewQueues() error = %v", err)
	}

	// All queues should start empty.
	depths := q.Depths()
	for stage, depth := range depths {
		if depth != 0 {
			t.Errorf("expected %s depth = 0, got %d", stage, depth)
		}
	}

	// Add items and check depths.
	q.Detection.Add("item1")
	q.Gathering.Add("item2")
	q.Sink.Add("item3")

	depths = q.Depths()
	if depths[StageDetection] != 1 {
		t.Errorf("expected detection depth = 1, got %d", depths[StageDetection])
	}
	if depths[StageGathering] != 1 {
		t.Errorf("expected gathering depth = 1, got %d", depths[StageGathering])
	}
	if depths[StageSink] != 1 {
		t.Errorf("expected sink depth = 1, got %d", depths[StageSink])
	}

	q.ShutDown()
}

func TestQueues_ShutDown(t *testing.T) {
	m := newTestMetrics(t)
	q, err := NewQueues(m, slog.Default())
	if err != nil {
		t.Fatalf("NewQueues() error = %v", err)
	}

	q.ShutDown()

	// Double shutdown should not panic.
	q.ShutDown()
}

// --- PriorityQueue Tests ---

func TestPriorityQueue_BasicAddGet(t *testing.T) {
	pq := NewPriorityQueue("test")

	item := AnalysisItem{
		Severity: model.SeverityWarning,
		Bundle: model.DiagnosticBundle{
			FaultEvent: &model.FaultEvent{ID: "test-1"},
		},
	}
	pq.Add(item)

	got, shutdown := pq.Get()
	if shutdown {
		t.Fatal("unexpected shutdown")
	}
	if got.Bundle.FaultEvent.ID != "test-1" {
		t.Errorf("expected ID test-1, got %s", got.Bundle.FaultEvent.ID)
	}
	pq.Done()
}

func TestPriorityQueue_SeverityOrdering(t *testing.T) {
	pq := NewPriorityQueue("test")

	// Add in reverse priority order: info, warning, critical.
	pq.Add(AnalysisItem{
		Severity: model.SeverityInfo,
		Bundle:   model.DiagnosticBundle{FaultEvent: &model.FaultEvent{ID: "info-1"}},
	})
	pq.Add(AnalysisItem{
		Severity: model.SeverityWarning,
		Bundle:   model.DiagnosticBundle{FaultEvent: &model.FaultEvent{ID: "warning-1"}},
	})
	pq.Add(AnalysisItem{
		Severity: model.SeverityCritical,
		Bundle:   model.DiagnosticBundle{FaultEvent: &model.FaultEvent{ID: "critical-1"}},
	})

	// Should dequeue in priority order: critical, warning, info.
	expectedOrder := []struct {
		id       string
		severity model.Severity
	}{
		{"critical-1", model.SeverityCritical},
		{"warning-1", model.SeverityWarning},
		{"info-1", model.SeverityInfo},
	}

	for _, expected := range expectedOrder {
		got, shutdown := pq.Get()
		if shutdown {
			t.Fatal("unexpected shutdown")
		}
		if got.Bundle.FaultEvent.ID != expected.id {
			t.Errorf("expected ID %s, got %s", expected.id, got.Bundle.FaultEvent.ID)
		}
		if got.Severity != expected.severity {
			t.Errorf("expected severity %s, got %s", expected.severity, got.Severity)
		}
		pq.Done()
	}
}

func TestPriorityQueue_FIFOWithinSameSeverity(t *testing.T) {
	pq := NewPriorityQueue("test")

	// Add multiple items at the same severity.
	for i := 0; i < 5; i++ {
		pq.Add(AnalysisItem{
			Severity: model.SeverityWarning,
			Bundle:   model.DiagnosticBundle{FaultEvent: &model.FaultEvent{ID: fmt.Sprintf("w-%d", i)}},
		})
	}

	// Should come out in FIFO order.
	for i := 0; i < 5; i++ {
		got, shutdown := pq.Get()
		if shutdown {
			t.Fatal("unexpected shutdown")
		}
		expected := fmt.Sprintf("w-%d", i)
		if got.Bundle.FaultEvent.ID != expected {
			t.Errorf("expected ID %s, got %s", expected, got.Bundle.FaultEvent.ID)
		}
		pq.Done()
	}
}

func TestPriorityQueue_MixedSeverityOrder(t *testing.T) {
	pq := NewPriorityQueue("test")

	// Add items in mixed order.
	items := []AnalysisItem{
		{Severity: model.SeverityWarning, Bundle: model.DiagnosticBundle{FaultEvent: &model.FaultEvent{ID: "w-1"}}},
		{Severity: model.SeverityCritical, Bundle: model.DiagnosticBundle{FaultEvent: &model.FaultEvent{ID: "c-1"}}},
		{Severity: model.SeverityInfo, Bundle: model.DiagnosticBundle{FaultEvent: &model.FaultEvent{ID: "i-1"}}},
		{Severity: model.SeverityCritical, Bundle: model.DiagnosticBundle{FaultEvent: &model.FaultEvent{ID: "c-2"}}},
		{Severity: model.SeverityWarning, Bundle: model.DiagnosticBundle{FaultEvent: &model.FaultEvent{ID: "w-2"}}},
		{Severity: model.SeverityInfo, Bundle: model.DiagnosticBundle{FaultEvent: &model.FaultEvent{ID: "i-2"}}},
	}
	for _, item := range items {
		pq.Add(item)
	}

	expectedOrder := []string{"c-1", "c-2", "w-1", "w-2", "i-1", "i-2"}
	for _, expected := range expectedOrder {
		got, shutdown := pq.Get()
		if shutdown {
			t.Fatal("unexpected shutdown")
		}
		if got.Bundle.FaultEvent.ID != expected {
			t.Errorf("expected ID %s, got %s", expected, got.Bundle.FaultEvent.ID)
		}
		pq.Done()
	}
}

func TestPriorityQueue_Len(t *testing.T) {
	pq := NewPriorityQueue("test")

	if pq.Len() != 0 {
		t.Errorf("expected Len() = 0, got %d", pq.Len())
	}

	pq.Add(AnalysisItem{Severity: model.SeverityCritical})
	pq.Add(AnalysisItem{Severity: model.SeverityWarning})
	pq.Add(AnalysisItem{Severity: model.SeverityInfo})

	if pq.Len() != 3 {
		t.Errorf("expected Len() = 3, got %d", pq.Len())
	}

	pq.Get()
	pq.Done()
	if pq.Len() != 2 {
		t.Errorf("expected Len() = 2, got %d", pq.Len())
	}
}

func TestPriorityQueue_ShutDown(t *testing.T) {
	pq := NewPriorityQueue("test")

	if pq.ShuttingDown() {
		t.Error("should not be shutting down yet")
	}

	pq.ShutDown()

	if !pq.ShuttingDown() {
		t.Error("should be shutting down")
	}

	// Get after shutdown with empty queue should return shutdown=true.
	_, shutdown := pq.Get()
	if !shutdown {
		t.Error("expected shutdown=true from Get after ShutDown")
	}
}

func TestPriorityQueue_ShutDownWithPendingItems(t *testing.T) {
	pq := NewPriorityQueue("test")

	pq.Add(AnalysisItem{Severity: model.SeverityCritical, Bundle: model.DiagnosticBundle{FaultEvent: &model.FaultEvent{ID: "c-1"}}})
	pq.Add(AnalysisItem{Severity: model.SeverityWarning, Bundle: model.DiagnosticBundle{FaultEvent: &model.FaultEvent{ID: "w-1"}}})

	pq.ShutDown()

	// Should still be able to drain pending items.
	got, shutdown := pq.Get()
	if shutdown {
		t.Fatal("should be able to drain pending items after shutdown")
	}
	if got.Bundle.FaultEvent.ID != "c-1" {
		t.Errorf("expected c-1, got %s", got.Bundle.FaultEvent.ID)
	}
	pq.Done()

	got, shutdown = pq.Get()
	if shutdown {
		t.Fatal("should be able to drain pending items after shutdown")
	}
	if got.Bundle.FaultEvent.ID != "w-1" {
		t.Errorf("expected w-1, got %s", got.Bundle.FaultEvent.ID)
	}
	pq.Done()

	// Now queue is empty and shutdown, should return shutdown.
	_, shutdown = pq.Get()
	if !shutdown {
		t.Error("expected shutdown=true after draining all items")
	}
}

func TestPriorityQueue_AddAfterShutDown(t *testing.T) {
	pq := NewPriorityQueue("test")
	pq.ShutDown()

	// Add after shutdown should be silently dropped.
	pq.Add(AnalysisItem{Severity: model.SeverityCritical})

	if pq.Len() != 0 {
		t.Errorf("expected Len() = 0 after adding to shut down queue, got %d", pq.Len())
	}
}

func TestPriorityQueue_ConcurrentAccess(t *testing.T) {
	pq := NewPriorityQueue("test")

	const numProducers = 10
	const itemsPerProducer = 100

	var producerWg sync.WaitGroup
	producerWg.Add(numProducers)

	// Concurrent producers.
	for p := 0; p < numProducers; p++ {
		go func(producerID int) {
			defer producerWg.Done()
			for i := 0; i < itemsPerProducer; i++ {
				severities := []model.Severity{model.SeverityCritical, model.SeverityWarning, model.SeverityInfo}
				sev := severities[i%3]
				pq.Add(AnalysisItem{
					Severity: sev,
					Bundle: model.DiagnosticBundle{
						FaultEvent: &model.FaultEvent{
							ID: fmt.Sprintf("p%d-i%d", producerID, i),
						},
					},
				})
			}
		}(p)
	}

	// Wait for all producers to finish.
	producerWg.Wait()

	total := numProducers * itemsPerProducer
	if pq.Len() != total {
		t.Errorf("expected Len() = %d, got %d", total, pq.Len())
	}

	// Concurrent consumers.
	var consumerWg sync.WaitGroup
	consumed := make(chan string, total)
	consumerWg.Add(3)

	pq.ShutDown() // Signal shutdown so consumers eventually exit.

	for c := 0; c < 3; c++ {
		go func() {
			defer consumerWg.Done()
			for {
				item, shutdown := pq.Get()
				if shutdown {
					return
				}
				consumed <- item.Bundle.FaultEvent.ID
				pq.Done()
			}
		}()
	}

	consumerWg.Wait()
	close(consumed)

	count := 0
	for range consumed {
		count++
	}
	if count != total {
		t.Errorf("expected %d consumed items, got %d", total, count)
	}
}

func TestPriorityQueue_GetBlocksUntilAvailable(t *testing.T) {
	pq := NewPriorityQueue("test")

	done := make(chan AnalysisItem, 1)
	go func() {
		item, _ := pq.Get()
		done <- item
		pq.Done()
	}()

	// Give the goroutine time to block on Get.
	time.Sleep(50 * time.Millisecond)

	// Add an item to unblock.
	pq.Add(AnalysisItem{
		Severity: model.SeverityWarning,
		Bundle:   model.DiagnosticBundle{FaultEvent: &model.FaultEvent{ID: "delayed"}},
	})

	select {
	case item := <-done:
		if item.Bundle.FaultEvent.ID != "delayed" {
			t.Errorf("expected delayed, got %s", item.Bundle.FaultEvent.ID)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Get to unblock")
	}
}

func TestPriorityQueue_Drain(t *testing.T) {
	pq := NewPriorityQueue("test")

	pq.Add(AnalysisItem{Severity: model.SeverityCritical, Bundle: model.DiagnosticBundle{FaultEvent: &model.FaultEvent{ID: "c-1"}}})
	pq.Add(AnalysisItem{Severity: model.SeverityWarning, Bundle: model.DiagnosticBundle{FaultEvent: &model.FaultEvent{ID: "w-1"}}})
	pq.Add(AnalysisItem{Severity: model.SeverityInfo, Bundle: model.DiagnosticBundle{FaultEvent: &model.FaultEvent{ID: "i-1"}}})

	items := pq.Drain()

	if len(items) != 3 {
		t.Fatalf("expected 3 drained items, got %d", len(items))
	}

	// Drain should return items in priority order.
	if items[0].Bundle.FaultEvent.ID != "c-1" {
		t.Errorf("expected first drained item c-1, got %s", items[0].Bundle.FaultEvent.ID)
	}
	if items[1].Bundle.FaultEvent.ID != "w-1" {
		t.Errorf("expected second drained item w-1, got %s", items[1].Bundle.FaultEvent.ID)
	}
	if items[2].Bundle.FaultEvent.ID != "i-1" {
		t.Errorf("expected third drained item i-1, got %s", items[2].Bundle.FaultEvent.ID)
	}

	// Queue should be empty after drain.
	if pq.Len() != 0 {
		t.Errorf("expected empty queue after drain, got %d", pq.Len())
	}
}

func TestPriorityQueue_WaitForInflight(t *testing.T) {
	pq := NewPriorityQueue("test")

	pq.Add(AnalysisItem{Severity: model.SeverityWarning})

	item, _ := pq.Get()
	_ = item

	// WaitForInflight should block until Done is called.
	go func() {
		time.Sleep(50 * time.Millisecond)
		pq.Done()
	}()

	if !pq.WaitForInflight(2 * time.Second) {
		t.Error("WaitForInflight should have returned true")
	}
}

func TestPriorityQueue_WaitForInflight_Timeout(t *testing.T) {
	pq := NewPriorityQueue("test")

	pq.Add(AnalysisItem{Severity: model.SeverityWarning})
	pq.Get()

	// Don't call Done — should timeout.
	if pq.WaitForInflight(100 * time.Millisecond) {
		t.Error("WaitForInflight should have returned false (timeout)")
	}

	// Clean up.
	pq.Done()
}

func TestQueues_MetricsReporter(t *testing.T) {
	m := newTestMetrics(t)
	q, err := NewQueues(m, slog.Default())
	if err != nil {
		t.Fatalf("NewQueues() error = %v", err)
	}

	q.Detection.Add("item1")
	q.Gathering.Add("item2")

	// Force a metrics report.
	q.reportMetrics()

	// The metrics should be updated. We can't easily read the gauge
	// values directly, but we verify no panics occurred.

	q.ShutDown()
}

func TestQueues_MetricsReporterValues(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := metrics.NewMetrics(reg)
	q, err := NewQueues(m, slog.Default())
	if err != nil {
		t.Fatalf("NewQueues() error = %v", err)
	}
	defer q.ShutDown()

	// Add items to different queues.
	q.Detection.Add("d1")
	q.Detection.Add("d2")
	q.Gathering.Add("g1")
	q.Analysis.Add(AnalysisItem{Severity: model.SeverityCritical})
	q.Analysis.Add(AnalysisItem{Severity: model.SeverityWarning})
	q.Analysis.Add(AnalysisItem{Severity: model.SeverityInfo})
	q.Sink.Add(SinkItem{})

	// Force metrics report.
	q.reportMetrics()

	// Gather metrics and verify values.
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}

	// Find the pipeline_queue_depth metric.
	depthByStage := map[string]float64{}
	for _, fam := range families {
		if fam.GetName() == "wormsign_pipeline_queue_depth" {
			for _, metric := range fam.GetMetric() {
				for _, label := range metric.GetLabel() {
					if label.GetName() == "stage" {
						depthByStage[label.GetValue()] = metric.GetGauge().GetValue()
					}
				}
			}
		}
	}

	expected := map[string]float64{
		"detection":   2,
		"correlation": 0,
		"gathering":   1,
		"analysis":    3,
		"sink":        1,
	}
	for stage, want := range expected {
		got, ok := depthByStage[stage]
		if !ok {
			t.Errorf("missing metric for stage %s", stage)
			continue
		}
		if got != want {
			t.Errorf("stage %s: expected depth %v, got %v", stage, want, got)
		}
	}
}

func TestQueues_AllStagesPresent(t *testing.T) {
	depths := map[Stage]bool{
		StageDetection:   false,
		StageCorrelation: false,
		StageGathering:   false,
		StageAnalysis:    false,
		StageSink:        false,
	}

	m := newTestMetrics(t)
	q, err := NewQueues(m, slog.Default())
	if err != nil {
		t.Fatalf("NewQueues() error = %v", err)
	}
	defer q.ShutDown()

	for stage := range q.Depths() {
		depths[stage] = true
	}

	for stage, present := range depths {
		if !present {
			t.Errorf("stage %s missing from Depths()", stage)
		}
	}
}

// --- PriorityQueue InFlight tests ---

func TestPriorityQueue_InFlight(t *testing.T) {
	pq := NewPriorityQueue("test")

	if pq.InFlight() != 0 {
		t.Errorf("expected InFlight() = 0, got %d", pq.InFlight())
	}

	pq.Add(AnalysisItem{Severity: model.SeverityCritical})
	pq.Add(AnalysisItem{Severity: model.SeverityWarning})

	// Get one item — inflight should be 1.
	pq.Get()
	if pq.InFlight() != 1 {
		t.Errorf("expected InFlight() = 1, got %d", pq.InFlight())
	}

	// Get another — inflight should be 2.
	pq.Get()
	if pq.InFlight() != 2 {
		t.Errorf("expected InFlight() = 2, got %d", pq.InFlight())
	}

	// Done one — inflight should be 1.
	pq.Done()
	if pq.InFlight() != 1 {
		t.Errorf("expected InFlight() = 1 after one Done, got %d", pq.InFlight())
	}

	// Done the other — inflight should be 0.
	pq.Done()
	if pq.InFlight() != 0 {
		t.Errorf("expected InFlight() = 0 after both Done, got %d", pq.InFlight())
	}
}

func TestPriorityQueue_DoneOnEmptyInflight(t *testing.T) {
	pq := NewPriorityQueue("test")

	// Done without any in-flight should not underflow.
	pq.Done()
	if pq.InFlight() != 0 {
		t.Errorf("expected InFlight() = 0 after spurious Done, got %d", pq.InFlight())
	}
}

// --- PriorityQueue WaitForDrain tests ---

func TestPriorityQueue_WaitForDrain_EmptyQueue(t *testing.T) {
	pq := NewPriorityQueue("test")

	// WaitForDrain on empty queue with no inflight should return immediately.
	if !pq.WaitForDrain(100 * time.Millisecond) {
		t.Error("WaitForDrain should return true for empty queue")
	}
}

func TestPriorityQueue_WaitForDrain_WithPendingAndInflight(t *testing.T) {
	pq := NewPriorityQueue("test")

	pq.Add(AnalysisItem{Severity: model.SeverityCritical, Bundle: model.DiagnosticBundle{FaultEvent: &model.FaultEvent{ID: "c-1"}}})
	pq.Add(AnalysisItem{Severity: model.SeverityWarning, Bundle: model.DiagnosticBundle{FaultEvent: &model.FaultEvent{ID: "w-1"}}})

	// Get one item (makes it in-flight), leave one pending.
	pq.Get()

	// Start a goroutine that will drain the queue.
	go func() {
		time.Sleep(50 * time.Millisecond)
		// Get the pending item.
		pq.Get()
		time.Sleep(50 * time.Millisecond)
		// Done both.
		pq.Done()
		pq.Done()
	}()

	if !pq.WaitForDrain(2 * time.Second) {
		t.Error("WaitForDrain should have returned true")
	}
	if pq.Len() != 0 {
		t.Errorf("expected Len() = 0, got %d", pq.Len())
	}
	if pq.InFlight() != 0 {
		t.Errorf("expected InFlight() = 0, got %d", pq.InFlight())
	}
}

func TestPriorityQueue_WaitForDrain_Timeout(t *testing.T) {
	pq := NewPriorityQueue("test")

	pq.Add(AnalysisItem{Severity: model.SeverityWarning})
	pq.Get() // in-flight, never Done

	if pq.WaitForDrain(100 * time.Millisecond) {
		t.Error("WaitForDrain should have returned false (timeout)")
	}

	// Clean up.
	pq.Done()
}

// --- Rate-limiting queue tests ---

func TestDetectionQueue_RateLimiting(t *testing.T) {
	m := newTestMetrics(t)
	q, err := NewQueues(m, slog.Default())
	if err != nil {
		t.Fatalf("NewQueues() error = %v", err)
	}
	defer q.ShutDown()

	// Add and retrieve items with rate limiting support.
	q.Detection.AddRateLimited("event-1")

	obj, shutdown := q.Detection.Get()
	if shutdown {
		t.Fatal("unexpected shutdown")
	}
	if obj != "event-1" {
		t.Errorf("expected event-1, got %v", obj)
	}
	q.Detection.Done(obj)
}

func TestGatheringQueue_AddGetDone(t *testing.T) {
	m := newTestMetrics(t)
	q, err := NewQueues(m, slog.Default())
	if err != nil {
		t.Fatalf("NewQueues() error = %v", err)
	}
	defer q.ShutDown()

	item := GatherItem{
		FaultEvent: &model.FaultEvent{
			ID:           "fe-1",
			DetectorName: "PodCrashLoop",
			Severity:     model.SeverityWarning,
		},
	}
	q.Gathering.Add(item)

	if q.Gathering.Len() != 1 {
		t.Errorf("expected Gathering Len() = 1, got %d", q.Gathering.Len())
	}

	obj, shutdown := q.Gathering.Get()
	if shutdown {
		t.Fatal("unexpected shutdown")
	}

	got, ok := obj.(GatherItem)
	if !ok {
		t.Fatalf("expected GatherItem, got %T", obj)
	}
	if got.FaultEvent.ID != "fe-1" {
		t.Errorf("expected ID fe-1, got %s", got.FaultEvent.ID)
	}
	q.Gathering.Done(obj)
}

func TestGatheringQueue_SuperEvent(t *testing.T) {
	m := newTestMetrics(t)
	q, err := NewQueues(m, slog.Default())
	if err != nil {
		t.Fatalf("NewQueues() error = %v", err)
	}
	defer q.ShutDown()

	item := GatherItem{
		SuperEvent: &model.SuperEvent{
			ID:              "se-1",
			CorrelationRule: "NodeCascade",
			Severity:        model.SeverityCritical,
		},
	}
	q.Gathering.Add(item)

	obj, shutdown := q.Gathering.Get()
	if shutdown {
		t.Fatal("unexpected shutdown")
	}

	got, ok := obj.(GatherItem)
	if !ok {
		t.Fatalf("expected GatherItem, got %T", obj)
	}
	if got.SuperEvent == nil {
		t.Fatal("expected non-nil SuperEvent")
	}
	if got.SuperEvent.ID != "se-1" {
		t.Errorf("expected ID se-1, got %s", got.SuperEvent.ID)
	}
	q.Gathering.Done(obj)
}

func TestSinkQueue_AddGetDone(t *testing.T) {
	m := newTestMetrics(t)
	q, err := NewQueues(m, slog.Default())
	if err != nil {
		t.Fatalf("NewQueues() error = %v", err)
	}
	defer q.ShutDown()

	report := &model.RCAReport{
		FaultEventID: "fe-1",
		RootCause:    "test root cause",
		Severity:     model.SeverityWarning,
		Category:     "application",
	}
	q.Sink.Add(SinkItem{Report: report})

	if q.Sink.Len() != 1 {
		t.Errorf("expected Sink Len() = 1, got %d", q.Sink.Len())
	}

	obj, shutdown := q.Sink.Get()
	if shutdown {
		t.Fatal("unexpected shutdown")
	}

	got, ok := obj.(SinkItem)
	if !ok {
		t.Fatalf("expected SinkItem, got %T", obj)
	}
	if got.Report.FaultEventID != "fe-1" {
		t.Errorf("expected FaultEventID fe-1, got %s", got.Report.FaultEventID)
	}
	q.Sink.Done(obj)
}

func TestCorrelationQueue_AddGetDone(t *testing.T) {
	m := newTestMetrics(t)
	q, err := NewQueues(m, slog.Default())
	if err != nil {
		t.Fatalf("NewQueues() error = %v", err)
	}
	defer q.ShutDown()

	q.Correlation.Add("corr-event-1")

	if q.Correlation.Len() != 1 {
		t.Errorf("expected Correlation Len() = 1, got %d", q.Correlation.Len())
	}

	obj, shutdown := q.Correlation.Get()
	if shutdown {
		t.Fatal("unexpected shutdown")
	}
	if obj != "corr-event-1" {
		t.Errorf("expected corr-event-1, got %v", obj)
	}
	q.Correlation.Done(obj)
}

// --- Severity index tests ---

func TestSeverityIndex(t *testing.T) {
	tests := []struct {
		severity model.Severity
		expected int
	}{
		{model.SeverityCritical, 0},
		{model.SeverityWarning, 1},
		{model.SeverityInfo, 2},
		{"unknown", 2}, // default maps to lowest priority
	}

	for _, tt := range tests {
		got := severityIndex(tt.severity)
		if got != tt.expected {
			t.Errorf("severityIndex(%q) = %d, want %d", tt.severity, got, tt.expected)
		}
	}
}

// --- Stage constant tests ---

func TestStageConstants(t *testing.T) {
	// Verify stage strings match expected values used in metrics.
	if StageDetection != "detection" {
		t.Errorf("StageDetection = %q, want %q", StageDetection, "detection")
	}
	if StageCorrelation != "correlation" {
		t.Errorf("StageCorrelation = %q, want %q", StageCorrelation, "correlation")
	}
	if StageGathering != "gathering" {
		t.Errorf("StageGathering = %q, want %q", StageGathering, "gathering")
	}
	if StageAnalysis != "analysis" {
		t.Errorf("StageAnalysis = %q, want %q", StageAnalysis, "analysis")
	}
	if StageSink != "sink" {
		t.Errorf("StageSink = %q, want %q", StageSink, "sink")
	}
}

// --- Concurrent producer/consumer with WaitForDrain ---

func TestPriorityQueue_ConcurrentProducerConsumerDrain(t *testing.T) {
	pq := NewPriorityQueue("test")

	const numItems = 200
	var producerDone sync.WaitGroup
	producerDone.Add(1)

	// Producer adds items concurrently.
	go func() {
		defer producerDone.Done()
		for i := 0; i < numItems; i++ {
			severities := []model.Severity{model.SeverityCritical, model.SeverityWarning, model.SeverityInfo}
			pq.Add(AnalysisItem{
				Severity: severities[i%3],
				Bundle:   model.DiagnosticBundle{FaultEvent: &model.FaultEvent{ID: fmt.Sprintf("item-%d", i)}},
			})
		}
	}()

	// Wait for producer to finish.
	producerDone.Wait()

	// Start 3 consumers.
	var consumed sync.Map
	var consumerWg sync.WaitGroup
	consumerWg.Add(3)

	pq.ShutDown() // Signal that no more items will be added.

	for c := 0; c < 3; c++ {
		go func() {
			defer consumerWg.Done()
			for {
				item, shutdown := pq.Get()
				if shutdown {
					return
				}
				consumed.Store(item.Bundle.FaultEvent.ID, true)
				pq.Done()
			}
		}()
	}

	consumerWg.Wait()

	// Verify all items were consumed.
	count := 0
	consumed.Range(func(_, _ any) bool {
		count++
		return true
	})
	if count != numItems {
		t.Errorf("expected %d consumed items, got %d", numItems, count)
	}

	// Verify priority ordering: all critical before all warning before all info.
	// This is verified by the MixedSeverityOrder test above; here we just verify
	// all items were processed.
}

func TestQueues_MetricsReporterStartStop(t *testing.T) {
	m := newTestMetrics(t)
	q, err := NewQueues(m, slog.Default())
	if err != nil {
		t.Fatalf("NewQueues() error = %v", err)
	}

	// Start the metrics reporter.
	q.StartMetricsReporter()

	// Add some items so there's something to report.
	q.Detection.Add("item1")
	q.Analysis.Add(AnalysisItem{Severity: model.SeverityCritical})

	// Give the reporter time to tick at least once (it ticks every 5s,
	// but we just want to verify it doesn't panic on shutdown).
	time.Sleep(50 * time.Millisecond)

	// ShutDown closes the stopCh which stops the reporter.
	q.ShutDown()
}

func TestPriorityQueue_GetUnblocksOnShutDown(t *testing.T) {
	pq := NewPriorityQueue("test")

	done := make(chan bool, 1)
	go func() {
		_, shutdown := pq.Get()
		done <- shutdown
	}()

	// Give goroutine time to block on Get.
	time.Sleep(50 * time.Millisecond)

	// ShutDown should unblock the Get call.
	pq.ShutDown()

	select {
	case shutdown := <-done:
		if !shutdown {
			t.Error("expected shutdown=true when Get unblocked by ShutDown")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for Get to unblock on ShutDown")
	}
}

