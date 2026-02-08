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

