package pipeline

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
	"github.com/k8s-wormsign/k8s-wormsign/internal/pipeline/queue"
)

// --- Queues accessor ---

func TestPipeline_Queues(t *testing.T) {
	m := newTestMetrics(t)
	a := &mockAnalyzer{name: "test", healthVal: true}

	cfg := DefaultConfig()
	cfg.Correlation.Enabled = false

	p, err := New(cfg,
		WithMetrics(m),
		WithAnalyzer(a),
		WithLogger(slog.Default()),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	q := p.Queues()
	if q == nil {
		t.Fatal("Queues() returned nil")
	}
	if q.Gathering == nil {
		t.Error("Queues().Gathering is nil")
	}
	if q.Sink == nil {
		t.Error("Queues().Sink is nil")
	}
}

// --- processSinkItem with nil report ---

func TestPipeline_ProcessSinkItem_NilReport(t *testing.T) {
	m := newTestMetrics(t)
	a := &mockAnalyzer{name: "test", healthVal: true}
	s := &mockSink{name: "test-sink"}

	cfg := DefaultConfig()
	cfg.Correlation.Enabled = false

	p, err := New(cfg,
		WithMetrics(m),
		WithAnalyzer(a),
		WithSinks([]Sink{s}),
		WithLogger(slog.Default()),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	logger := slog.Default()
	// Should not panic and should return early without delivering.
	p.processSinkItem(context.Background(), logger, queue.SinkItem{Report: nil})

	if len(s.getDelivered()) != 0 {
		t.Error("expected no deliveries for nil report")
	}
}

// --- processGatherItem with neither FaultEvent nor SuperEvent ---

func TestPipeline_ProcessGatherItem_Empty(t *testing.T) {
	m := newTestMetrics(t)
	a := &mockAnalyzer{name: "test", healthVal: true}

	cfg := DefaultConfig()
	cfg.Correlation.Enabled = false

	p, err := New(cfg,
		WithMetrics(m),
		WithAnalyzer(a),
		WithLogger(slog.Default()),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	logger := slog.Default()
	// Should not panic and should return early.
	p.processGatherItem(context.Background(), logger, queue.GatherItem{})

	// No analysis item should be enqueued.
	if p.queues.Analysis.Len() != 0 {
		t.Errorf("expected 0 analysis items, got %d", p.queues.Analysis.Len())
	}
}

// --- runSinkWorker with wrong type in queue ---

func TestPipeline_RunSinkWorker_WrongType(t *testing.T) {
	m := newTestMetrics(t)
	a := &mockAnalyzer{name: "test", healthVal: true}
	s := &mockSink{name: "test-sink"}

	cfg := DefaultConfig()
	cfg.Correlation.Enabled = false
	cfg.Workers.Sink = 1

	p, err := New(cfg,
		WithMetrics(m),
		WithAnalyzer(a),
		WithSinks([]Sink{s}),
		WithLogger(slog.Default()),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Add a wrong-type item directly to the sink queue.
	p.queues.Sink.Add("not-a-sink-item")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		p.runSinkWorker(ctx, 0)
		close(done)
	}()

	// Give worker time to process the wrong-type item.
	time.Sleep(100 * time.Millisecond)

	// Shutdown the queue and cancel context.
	p.queues.Sink.ShutDown()
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runSinkWorker did not return")
	}

	// Should not have delivered anything.
	if len(s.getDelivered()) != 0 {
		t.Error("expected no deliveries for wrong-type item")
	}
}

// --- runGatheringWorker with wrong type in queue ---

func TestPipeline_RunGatheringWorker_WrongType(t *testing.T) {
	m := newTestMetrics(t)
	a := &mockAnalyzer{name: "test", healthVal: true}

	cfg := DefaultConfig()
	cfg.Correlation.Enabled = false
	cfg.Workers.Gathering = 1

	p, err := New(cfg,
		WithMetrics(m),
		WithAnalyzer(a),
		WithLogger(slog.Default()),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Add a wrong-type item directly to the gathering queue.
	p.queues.Gathering.Add("not-a-gather-item")

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		p.runGatheringWorker(ctx, 0)
		close(done)
	}()

	// Give worker time to process.
	time.Sleep(100 * time.Millisecond)

	p.queues.Gathering.ShutDown()
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runGatheringWorker did not return")
	}
}

// --- drainAnalysisWithTimeout timeout path ---

func TestPipeline_DrainAnalysisWithTimeout_Timeout(t *testing.T) {
	m := newTestMetrics(t)
	// Use a slow analyzer that blocks.
	a := &mockAnalyzer{name: "test", healthVal: true}

	cfg := DefaultConfig()
	cfg.Correlation.Enabled = false
	cfg.AnalyzeTimeout = 50 * time.Millisecond

	p, err := New(cfg,
		WithMetrics(m),
		WithAnalyzer(a),
		WithLogger(slog.Default()),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Add an item to the analysis queue but don't start any workers.
	// This simulates items remaining when timeout fires.
	p.queues.Analysis.Add(queue.AnalysisItem{
		Bundle: model.DiagnosticBundle{
			Timestamp: time.Now(),
		},
		Severity: model.SeverityWarning,
	})

	// drainAnalysisWithTimeout should log a warning and return.
	p.drainAnalysisWithTimeout(50 * time.Millisecond)
	// No assertion needed beyond "does not hang".
}

// --- drainStageWithTimeout timeout path ---

func TestPipeline_DrainStageWithTimeout_Timeout(t *testing.T) {
	m := newTestMetrics(t)
	a := &mockAnalyzer{name: "test", healthVal: true}

	cfg := DefaultConfig()
	cfg.Correlation.Enabled = false

	p, err := New(cfg,
		WithMetrics(m),
		WithAnalyzer(a),
		WithLogger(slog.Default()),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Add an item but don't process it so the queue never drains.
	p.queues.Gathering.Add("stuck-item")

	// Should timeout and return without hanging.
	p.drainStageWithTimeout(p.queues.Gathering, "gathering", 100*time.Millisecond)
}

// --- drainSinkWithTimeout timeout path ---

func TestPipeline_DrainSinkWithTimeout_Timeout(t *testing.T) {
	m := newTestMetrics(t)
	a := &mockAnalyzer{name: "test", healthVal: true}

	cfg := DefaultConfig()
	cfg.Correlation.Enabled = false

	p, err := New(cfg,
		WithMetrics(m),
		WithAnalyzer(a),
		WithLogger(slog.Default()),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Add an item but don't process it so the queue never drains.
	p.queues.Sink.Add("stuck-item")

	// Should timeout and return without hanging.
	p.drainSinkWithTimeout(100 * time.Millisecond)
}
