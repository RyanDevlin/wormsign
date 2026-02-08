package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/k8s-wormsign/k8s-wormsign/internal/analyzer"
	"github.com/k8s-wormsign/k8s-wormsign/internal/filter"
	"github.com/k8s-wormsign/k8s-wormsign/internal/gatherer"
	"github.com/k8s-wormsign/k8s-wormsign/internal/metrics"
	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
	"github.com/k8s-wormsign/k8s-wormsign/internal/pipeline/correlator"
	"github.com/prometheus/client_golang/prometheus"
)

// --- Test Helpers ---

func newTestMetrics(t *testing.T) *metrics.Metrics {
	t.Helper()
	reg := prometheus.NewRegistry()
	return metrics.NewMetrics(reg)
}

// mockAnalyzer implements analyzer.Analyzer for testing.
type mockAnalyzer struct {
	name      string
	calls     atomic.Int64
	reportFn  func(bundle model.DiagnosticBundle) *model.RCAReport
	healthVal bool
}

var _ analyzer.Analyzer = (*mockAnalyzer)(nil)

func (m *mockAnalyzer) Name() string { return m.name }

func (m *mockAnalyzer) Analyze(_ context.Context, bundle model.DiagnosticBundle) (*model.RCAReport, error) {
	m.calls.Add(1)
	if m.reportFn != nil {
		return m.reportFn(bundle), nil
	}
	eventID := ""
	if bundle.FaultEvent != nil {
		eventID = bundle.FaultEvent.ID
	} else if bundle.SuperEvent != nil {
		eventID = bundle.SuperEvent.ID
	}
	return &model.RCAReport{
		FaultEventID:     eventID,
		Timestamp:        time.Now().UTC(),
		RootCause:        "test root cause",
		Severity:         model.SeverityWarning,
		Category:         "application",
		Confidence:       0.9,
		DiagnosticBundle: bundle,
		AnalyzerBackend:  m.name,
	}, nil
}

func (m *mockAnalyzer) Healthy(_ context.Context) bool {
	return m.healthVal
}

// mockGatherer implements gatherer.Gatherer for testing.
type mockGatherer struct {
	name    string
	section *model.DiagnosticSection
}

var _ gatherer.Gatherer = (*mockGatherer)(nil)

func (g *mockGatherer) Name() string { return g.name }

func (g *mockGatherer) Gather(_ context.Context, ref model.ResourceRef) (*model.DiagnosticSection, error) {
	if g.section != nil {
		return g.section, nil
	}
	return &model.DiagnosticSection{
		GathererName: g.name,
		Title:        g.name,
		Content:      fmt.Sprintf("gathered data for %s", ref.String()),
		Format:       "text",
	}, nil
}

// mockSink implements Sink for testing.
type mockSink struct {
	name      string
	mu        sync.Mutex
	delivered []*model.RCAReport
	errFn     func() error
}

func (s *mockSink) Name() string { return s.name }

func (s *mockSink) Deliver(_ context.Context, report *model.RCAReport) error {
	if s.errFn != nil {
		if err := s.errFn(); err != nil {
			return err
		}
	}
	s.mu.Lock()
	s.delivered = append(s.delivered, report)
	s.mu.Unlock()
	return nil
}

func (s *mockSink) getDelivered() []*model.RCAReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]*model.RCAReport, len(s.delivered))
	copy(result, s.delivered)
	return result
}

// --- Pipeline Construction Tests ---

func TestNew_ValidConfig(t *testing.T) {
	m := newTestMetrics(t)
	a := &mockAnalyzer{name: "test", healthVal: true}

	p, err := New(DefaultConfig(),
		WithMetrics(m),
		WithAnalyzer(a),
		WithLogger(slog.Default()),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil Pipeline")
	}
}

func TestNew_MissingMetrics(t *testing.T) {
	a := &mockAnalyzer{name: "test", healthVal: true}
	_, err := New(DefaultConfig(), WithAnalyzer(a))
	if err == nil {
		t.Fatal("expected error for missing metrics")
	}
}

func TestNew_MissingAnalyzer(t *testing.T) {
	m := newTestMetrics(t)
	_, err := New(DefaultConfig(), WithMetrics(m))
	if err == nil {
		t.Fatal("expected error for missing analyzer")
	}
}

func TestNew_InvalidWorkerCount(t *testing.T) {
	m := newTestMetrics(t)
	a := &mockAnalyzer{name: "test", healthVal: true}

	cfg := DefaultConfig()
	cfg.Workers.Gathering = 0

	_, err := New(cfg, WithMetrics(m), WithAnalyzer(a))
	if err == nil {
		t.Fatal("expected error for invalid worker count")
	}
}

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(*Config)
		wantErr bool
	}{
		{
			name:    "valid defaults",
			modify:  func(c *Config) {},
			wantErr: false,
		},
		{
			name:    "zero gathering workers",
			modify:  func(c *Config) { c.Workers.Gathering = 0 },
			wantErr: true,
		},
		{
			name:    "zero analysis workers",
			modify:  func(c *Config) { c.Workers.Analysis = 0 },
			wantErr: true,
		},
		{
			name:    "zero sink workers",
			modify:  func(c *Config) { c.Workers.Sink = 0 },
			wantErr: true,
		},
		{
			name:    "zero gather timeout",
			modify:  func(c *Config) { c.GatherTimeout = 0 },
			wantErr: true,
		},
		{
			name:    "zero analyze timeout",
			modify:  func(c *Config) { c.AnalyzeTimeout = 0 },
			wantErr: true,
		},
		{
			name:    "zero sink timeout",
			modify:  func(c *Config) { c.SinkTimeout = 0 },
			wantErr: true,
		},
		{
			name: "invalid correlation config",
			modify: func(c *Config) {
				c.Correlation.WindowDuration = -1
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			tt.modify(&cfg)
			err := ValidateConfig(cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// --- Full Pipeline Flow Tests ---

func TestPipeline_EndToEnd_SingleEvent(t *testing.T) {
	m := newTestMetrics(t)
	a := &mockAnalyzer{name: "test-analyzer", healthVal: true}
	g := &mockGatherer{name: "test-gatherer"}
	s := &mockSink{name: "test-sink"}

	cfg := DefaultConfig()
	// Disable correlation to avoid window delay.
	cfg.Correlation.Enabled = false
	// Use minimal workers for testing.
	cfg.Workers.Gathering = 1
	cfg.Workers.Analysis = 1
	cfg.Workers.Sink = 1

	p, err := New(cfg,
		WithMetrics(m),
		WithAnalyzer(a),
		WithGatherers([]gatherer.Gatherer{g}),
		WithSinks([]Sink{s}),
		WithLogger(slog.Default()),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Submit a fault event without filtering.
	event := &model.FaultEvent{
		ID:           "test-event-1",
		DetectorName: "PodCrashLoop",
		Severity:     model.SeverityWarning,
		Timestamp:    time.Now().UTC(),
		Resource: model.ResourceRef{
			Kind:      "Pod",
			Namespace: "default",
			Name:      "my-pod",
			UID:       "uid-1",
		},
		Description: "Pod is crash looping",
		Labels:      map[string]string{},
		Annotations: map[string]string{},
	}

	p.SubmitFaultEvent(event, filter.FilterInput{
		DetectorName: event.DetectorName,
		Resource: filter.ResourceMeta{
			Kind:      event.Resource.Kind,
			Namespace: event.Resource.Namespace,
			Name:      event.Resource.Name,
		},
		Namespace: filter.NamespaceMeta{
			Name: event.Resource.Namespace,
		},
	})

	// Wait for the report to be delivered to the sink.
	deadline := time.After(10 * time.Second)
	for {
		delivered := s.getDelivered()
		if len(delivered) > 0 {
			report := delivered[0]
			if report.FaultEventID != "test-event-1" {
				t.Errorf("expected FaultEventID = test-event-1, got %s", report.FaultEventID)
			}
			if report.RootCause != "test root cause" {
				t.Errorf("expected root cause 'test root cause', got %s", report.RootCause)
			}
			if report.AnalyzerBackend != "test-analyzer" {
				t.Errorf("expected backend test-analyzer, got %s", report.AnalyzerBackend)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for sink delivery")
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}

	// Verify analyzer was called.
	if a.calls.Load() != 1 {
		t.Errorf("expected 1 analyzer call, got %d", a.calls.Load())
	}

	p.Stop()
}

func TestPipeline_EndToEnd_MultipleEvents(t *testing.T) {
	m := newTestMetrics(t)
	a := &mockAnalyzer{name: "test-analyzer", healthVal: true}
	g := &mockGatherer{name: "test-gatherer"}
	s := &mockSink{name: "test-sink"}

	cfg := DefaultConfig()
	cfg.Correlation.Enabled = false
	cfg.Workers.Gathering = 2
	cfg.Workers.Analysis = 2
	cfg.Workers.Sink = 2

	p, err := New(cfg,
		WithMetrics(m),
		WithAnalyzer(a),
		WithGatherers([]gatherer.Gatherer{g}),
		WithSinks([]Sink{s}),
		WithLogger(slog.Default()),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	const numEvents = 10
	for i := 0; i < numEvents; i++ {
		event := &model.FaultEvent{
			ID:           fmt.Sprintf("event-%d", i),
			DetectorName: "PodFailed",
			Severity:     model.SeverityWarning,
			Timestamp:    time.Now().UTC(),
			Resource: model.ResourceRef{
				Kind:      "Pod",
				Namespace: "default",
				Name:      fmt.Sprintf("pod-%d", i),
				UID:       fmt.Sprintf("uid-%d", i),
			},
			Description: "Pod failed",
			Labels:      map[string]string{},
			Annotations: map[string]string{},
		}
		p.SubmitFaultEvent(event, filter.FilterInput{
			DetectorName: event.DetectorName,
			Resource: filter.ResourceMeta{
				Kind:      event.Resource.Kind,
				Namespace: event.Resource.Namespace,
				Name:      event.Resource.Name,
			},
			Namespace: filter.NamespaceMeta{Name: event.Resource.Namespace},
		})
	}

	// Wait for all reports to be delivered.
	deadline := time.After(10 * time.Second)
	for {
		delivered := s.getDelivered()
		if len(delivered) >= numEvents {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timed out: expected %d deliveries, got %d", numEvents, len(s.getDelivered()))
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}

	if a.calls.Load() != numEvents {
		t.Errorf("expected %d analyzer calls, got %d", numEvents, a.calls.Load())
	}

	p.Stop()
}

func TestPipeline_FilterExclusion(t *testing.T) {
	m := newTestMetrics(t)
	a := &mockAnalyzer{name: "test-analyzer", healthVal: true}
	s := &mockSink{name: "test-sink"}

	filterEngine, err := filter.NewEngine(filter.GlobalFilterConfig{
		ExcludeNamespaces: []string{"kube-system"},
	}, slog.Default())
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}

	cfg := DefaultConfig()
	cfg.Correlation.Enabled = false

	p, err := New(cfg,
		WithMetrics(m),
		WithAnalyzer(a),
		WithFilter(filterEngine),
		WithSinks([]Sink{s}),
		WithLogger(slog.Default()),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Submit an event in an excluded namespace.
	event := &model.FaultEvent{
		ID:           "filtered-event",
		DetectorName: "PodCrashLoop",
		Severity:     model.SeverityWarning,
		Timestamp:    time.Now().UTC(),
		Resource: model.ResourceRef{
			Kind:      "Pod",
			Namespace: "kube-system",
			Name:      "coredns",
			UID:       "uid-filtered",
		},
		Description: "Pod crash loop",
		Labels:      map[string]string{},
		Annotations: map[string]string{},
	}

	p.SubmitFaultEvent(event, filter.FilterInput{
		DetectorName: event.DetectorName,
		Resource: filter.ResourceMeta{
			Kind:      event.Resource.Kind,
			Namespace: event.Resource.Namespace,
			Name:      event.Resource.Name,
		},
		Namespace: filter.NamespaceMeta{
			Name: "kube-system",
		},
	})

	// Give some time for the event to be processed if it were not filtered.
	time.Sleep(500 * time.Millisecond)

	// Verify no reports were delivered.
	delivered := s.getDelivered()
	if len(delivered) != 0 {
		t.Errorf("expected 0 deliveries for filtered event, got %d", len(delivered))
	}

	// Verify analyzer was never called.
	if a.calls.Load() != 0 {
		t.Errorf("expected 0 analyzer calls for filtered event, got %d", a.calls.Load())
	}

	p.Stop()
}

func TestPipeline_MultipleSinks(t *testing.T) {
	m := newTestMetrics(t)
	a := &mockAnalyzer{name: "test-analyzer", healthVal: true}
	g := &mockGatherer{name: "test-gatherer"}
	sink1 := &mockSink{name: "sink-1"}
	sink2 := &mockSink{name: "sink-2"}
	sink3 := &mockSink{name: "sink-3"}

	cfg := DefaultConfig()
	cfg.Correlation.Enabled = false
	cfg.Workers.Gathering = 1
	cfg.Workers.Analysis = 1
	cfg.Workers.Sink = 1

	p, err := New(cfg,
		WithMetrics(m),
		WithAnalyzer(a),
		WithGatherers([]gatherer.Gatherer{g}),
		WithSinks([]Sink{sink1, sink2, sink3}),
		WithLogger(slog.Default()),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	event := &model.FaultEvent{
		ID:           "multi-sink-event",
		DetectorName: "PodFailed",
		Severity:     model.SeverityCritical,
		Timestamp:    time.Now().UTC(),
		Resource: model.ResourceRef{
			Kind:      "Pod",
			Namespace: "default",
			Name:      "my-pod",
			UID:       "uid-ms",
		},
		Description: "Pod failed",
		Labels:      map[string]string{},
		Annotations: map[string]string{},
	}

	p.SubmitFaultEvent(event, filter.FilterInput{
		DetectorName: event.DetectorName,
		Resource:     filter.ResourceMeta{Kind: "Pod", Namespace: "default", Name: "my-pod"},
		Namespace:    filter.NamespaceMeta{Name: "default"},
	})

	deadline := time.After(10 * time.Second)
	for {
		d1 := sink1.getDelivered()
		d2 := sink2.getDelivered()
		d3 := sink3.getDelivered()
		if len(d1) > 0 && len(d2) > 0 && len(d3) > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timed out: sink1=%d, sink2=%d, sink3=%d", len(d1), len(d2), len(d3))
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}

	p.Stop()
}

func TestPipeline_SinkFailureDoesNotBlock(t *testing.T) {
	m := newTestMetrics(t)
	a := &mockAnalyzer{name: "test-analyzer", healthVal: true}
	g := &mockGatherer{name: "test-gatherer"}

	failingSink := &mockSink{
		name:  "failing-sink",
		errFn: func() error { return fmt.Errorf("delivery failed") },
	}
	successSink := &mockSink{name: "success-sink"}

	cfg := DefaultConfig()
	cfg.Correlation.Enabled = false
	cfg.Workers.Gathering = 1
	cfg.Workers.Analysis = 1
	cfg.Workers.Sink = 1

	p, err := New(cfg,
		WithMetrics(m),
		WithAnalyzer(a),
		WithGatherers([]gatherer.Gatherer{g}),
		WithSinks([]Sink{failingSink, successSink}),
		WithLogger(slog.Default()),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	event := &model.FaultEvent{
		ID:           "sink-fail-event",
		DetectorName: "PodCrashLoop",
		Severity:     model.SeverityWarning,
		Timestamp:    time.Now().UTC(),
		Resource: model.ResourceRef{
			Kind:      "Pod",
			Namespace: "default",
			Name:      "test-pod",
			UID:       "uid-sf",
		},
		Description: "Pod crash loop",
		Labels:      map[string]string{},
		Annotations: map[string]string{},
	}

	p.SubmitFaultEvent(event, filter.FilterInput{
		DetectorName: event.DetectorName,
		Resource:     filter.ResourceMeta{Kind: "Pod", Namespace: "default", Name: "test-pod"},
		Namespace:    filter.NamespaceMeta{Name: "default"},
	})

	// Wait for the success sink to receive the report.
	deadline := time.After(10 * time.Second)
	for {
		delivered := successSink.getDelivered()
		if len(delivered) > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for success sink delivery")
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}

	// The failing sink should not have delivered.
	if len(failingSink.getDelivered()) != 0 {
		t.Error("expected 0 deliveries from failing sink")
	}

	p.Stop()
}

func TestPipeline_AnalyzerError_FallbackReport(t *testing.T) {
	m := newTestMetrics(t)
	a := &mockAnalyzer{
		name:      "failing-analyzer",
		healthVal: true,
		reportFn: func(bundle model.DiagnosticBundle) *model.RCAReport {
			return nil // will cause our mock to return nil
		},
	}
	// Override Analyze to return an error.
	errAnalyzer := &errorAnalyzer{name: "error-analyzer"}
	g := &mockGatherer{name: "test-gatherer"}
	s := &mockSink{name: "test-sink"}

	cfg := DefaultConfig()
	cfg.Correlation.Enabled = false
	cfg.Workers.Gathering = 1
	cfg.Workers.Analysis = 1
	cfg.Workers.Sink = 1

	_ = a // not used, using errAnalyzer instead

	p, err := New(cfg,
		WithMetrics(m),
		WithAnalyzer(errAnalyzer),
		WithGatherers([]gatherer.Gatherer{g}),
		WithSinks([]Sink{s}),
		WithLogger(slog.Default()),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	event := &model.FaultEvent{
		ID:           "analyzer-fail-event",
		DetectorName: "PodCrashLoop",
		Severity:     model.SeverityCritical,
		Timestamp:    time.Now().UTC(),
		Resource: model.ResourceRef{
			Kind:      "Pod",
			Namespace: "default",
			Name:      "test-pod",
			UID:       "uid-af",
		},
		Description: "Pod crash loop",
		Labels:      map[string]string{},
		Annotations: map[string]string{},
	}

	p.SubmitFaultEvent(event, filter.FilterInput{
		DetectorName: event.DetectorName,
		Resource:     filter.ResourceMeta{Kind: "Pod", Namespace: "default", Name: "test-pod"},
		Namespace:    filter.NamespaceMeta{Name: "default"},
	})

	// Wait for the fallback report.
	deadline := time.After(10 * time.Second)
	for {
		delivered := s.getDelivered()
		if len(delivered) > 0 {
			report := delivered[0]
			if report.RootCause != "Automated analysis failed — raw diagnostics attached" {
				t.Errorf("expected fallback root cause, got %s", report.RootCause)
			}
			if report.Severity != model.SeverityCritical {
				t.Errorf("expected critical severity, got %s", report.Severity)
			}
			if report.Category != "unknown" {
				t.Errorf("expected unknown category, got %s", report.Category)
			}
			if report.Confidence != 0.0 {
				t.Errorf("expected 0.0 confidence, got %f", report.Confidence)
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for fallback report")
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}

	p.Stop()
}

// errorAnalyzer always returns an error from Analyze.
type errorAnalyzer struct {
	name string
}

func (e *errorAnalyzer) Name() string { return e.name }
func (e *errorAnalyzer) Analyze(_ context.Context, _ model.DiagnosticBundle) (*model.RCAReport, error) {
	return nil, fmt.Errorf("LLM backend unavailable")
}
func (e *errorAnalyzer) Healthy(_ context.Context) bool { return false }

func TestPipeline_NilEventIgnored(t *testing.T) {
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Submit nil — should not panic.
	p.SubmitFaultEvent(nil, filter.FilterInput{})

	time.Sleep(200 * time.Millisecond)

	if a.calls.Load() != 0 {
		t.Errorf("expected 0 analyzer calls for nil event, got %d", a.calls.Load())
	}

	p.Stop()
}

func TestPipeline_GracefulShutdown(t *testing.T) {
	m := newTestMetrics(t)
	a := &mockAnalyzer{name: "test-analyzer", healthVal: true}
	g := &mockGatherer{name: "test-gatherer"}
	s := &mockSink{name: "test-sink"}

	cfg := DefaultConfig()
	cfg.Correlation.Enabled = false
	cfg.Workers.Gathering = 1
	cfg.Workers.Analysis = 1
	cfg.Workers.Sink = 1
	cfg.GatherTimeout = 2 * time.Second
	cfg.AnalyzeTimeout = 2 * time.Second
	cfg.SinkTimeout = 2 * time.Second

	p, err := New(cfg,
		WithMetrics(m),
		WithAnalyzer(a),
		WithGatherers([]gatherer.Gatherer{g}),
		WithSinks([]Sink{s}),
		WithLogger(slog.Default()),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Submit several events.
	for i := 0; i < 5; i++ {
		event := &model.FaultEvent{
			ID:           fmt.Sprintf("shutdown-event-%d", i),
			DetectorName: "PodFailed",
			Severity:     model.SeverityWarning,
			Timestamp:    time.Now().UTC(),
			Resource: model.ResourceRef{
				Kind:      "Pod",
				Namespace: "default",
				Name:      fmt.Sprintf("pod-%d", i),
				UID:       fmt.Sprintf("uid-s%d", i),
			},
			Description: "Pod failed",
			Labels:      map[string]string{},
			Annotations: map[string]string{},
		}
		p.SubmitFaultEvent(event, filter.FilterInput{
			DetectorName: event.DetectorName,
			Resource:     filter.ResourceMeta{Kind: "Pod", Namespace: "default", Name: fmt.Sprintf("pod-%d", i)},
			Namespace:    filter.NamespaceMeta{Name: "default"},
		})
	}

	// Give pipeline time to process.
	time.Sleep(500 * time.Millisecond)

	// Stop should complete without hanging.
	done := make(chan struct{})
	go func() {
		p.Stop()
		close(done)
	}()

	select {
	case <-done:
		// Success.
	case <-time.After(15 * time.Second):
		t.Fatal("Stop() timed out")
	}
}

func TestPipeline_DoubleStop(t *testing.T) {
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Double Stop should not panic.
	p.Stop()
	p.Stop()
}

func TestPipeline_WithCorrelation(t *testing.T) {
	m := newTestMetrics(t)
	a := &mockAnalyzer{name: "test-analyzer", healthVal: true}
	g := &mockGatherer{name: "test-gatherer"}
	s := &mockSink{name: "test-sink"}

	cfg := DefaultConfig()
	// Enable correlation with a short window for testing.
	cfg.Correlation.Enabled = true
	cfg.Correlation.WindowDuration = 200 * time.Millisecond
	cfg.Workers.Gathering = 1
	cfg.Workers.Analysis = 1
	cfg.Workers.Sink = 1

	p, err := New(cfg,
		WithMetrics(m),
		WithAnalyzer(a),
		WithGatherers([]gatherer.Gatherer{g}),
		WithSinks([]Sink{s}),
		WithLogger(slog.Default()),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Submit events that should pass through correlation uncorrelated.
	event := &model.FaultEvent{
		ID:           "corr-event-1",
		DetectorName: "PodCrashLoop",
		Severity:     model.SeverityWarning,
		Timestamp:    time.Now().UTC(),
		Resource: model.ResourceRef{
			Kind:      "Pod",
			Namespace: "default",
			Name:      "my-pod",
			UID:       "uid-c1",
		},
		Description: "Pod crash loop",
		Labels:      map[string]string{},
		Annotations: map[string]string{},
	}

	p.SubmitFaultEvent(event, filter.FilterInput{
		DetectorName: event.DetectorName,
		Resource:     filter.ResourceMeta{Kind: "Pod", Namespace: "default", Name: "my-pod"},
		Namespace:    filter.NamespaceMeta{Name: "default"},
	})

	// Wait for the correlation window to fire and the event to flow through.
	deadline := time.After(10 * time.Second)
	for {
		delivered := s.getDelivered()
		if len(delivered) > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for correlated event delivery")
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}

	p.Stop()
}

func TestPipeline_NoSinks(t *testing.T) {
	m := newTestMetrics(t)
	a := &mockAnalyzer{name: "test-analyzer", healthVal: true}
	g := &mockGatherer{name: "test-gatherer"}

	cfg := DefaultConfig()
	cfg.Correlation.Enabled = false
	cfg.Workers.Gathering = 1
	cfg.Workers.Analysis = 1
	cfg.Workers.Sink = 1

	// No sinks configured — should still work, just no delivery.
	p, err := New(cfg,
		WithMetrics(m),
		WithAnalyzer(a),
		WithGatherers([]gatherer.Gatherer{g}),
		WithLogger(slog.Default()),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	event := &model.FaultEvent{
		ID:           "no-sink-event",
		DetectorName: "PodFailed",
		Severity:     model.SeverityWarning,
		Timestamp:    time.Now().UTC(),
		Resource: model.ResourceRef{
			Kind:      "Pod",
			Namespace: "default",
			Name:      "test-pod",
			UID:       "uid-ns",
		},
		Description: "Pod failed",
		Labels:      map[string]string{},
		Annotations: map[string]string{},
	}

	p.SubmitFaultEvent(event, filter.FilterInput{
		DetectorName: event.DetectorName,
		Resource:     filter.ResourceMeta{Kind: "Pod", Namespace: "default", Name: "test-pod"},
		Namespace:    filter.NamespaceMeta{Name: "default"},
	})

	// Wait for analyzer to process.
	deadline := time.After(5 * time.Second)
	for {
		if a.calls.Load() > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for analyzer call")
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}

	p.Stop()
}

func TestPipeline_WithCorrelation_NodeCascade(t *testing.T) {
	m := newTestMetrics(t)
	a := &mockAnalyzer{name: "test-analyzer", healthVal: true}
	g := &mockGatherer{name: "test-gatherer"}
	s := &mockSink{name: "test-sink"}

	cfg := DefaultConfig()
	cfg.Correlation.Enabled = true
	cfg.Correlation.WindowDuration = 300 * time.Millisecond
	cfg.Correlation.Rules = correlator.RulesConfig{
		NodeCascade: correlator.NodeCascadeConfig{
			Enabled:        true,
			MinPodFailures: 2,
		},
		DeploymentRollout: correlator.DeploymentRolloutConfig{Enabled: false},
		StorageCascade:    correlator.StorageCascadeConfig{Enabled: false},
		NamespaceStorm:    correlator.NamespaceStormConfig{Enabled: false, Threshold: 20},
	}
	cfg.Workers.Gathering = 1
	cfg.Workers.Analysis = 1
	cfg.Workers.Sink = 1

	p, err := New(cfg,
		WithMetrics(m),
		WithAnalyzer(a),
		WithGatherers([]gatherer.Gatherer{g}),
		WithSinks([]Sink{s}),
		WithLogger(slog.Default()),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Submit a NodeNotReady event and pod failures on the same node.
	nodeEvent := &model.FaultEvent{
		ID:           "node-event",
		DetectorName: "NodeNotReady",
		Severity:     model.SeverityCritical,
		Timestamp:    time.Now().UTC(),
		Resource: model.ResourceRef{
			Kind: "Node",
			Name: "node-1",
			UID:  "uid-node-1",
		},
		Description: "Node not ready",
		Labels:      map[string]string{},
		Annotations: map[string]string{"wormsign.io/node-name": "node-1"},
	}
	p.SubmitFaultEvent(nodeEvent, filter.FilterInput{
		DetectorName: nodeEvent.DetectorName,
		Resource:     filter.ResourceMeta{Kind: "Node", Name: "node-1"},
		Namespace:    filter.NamespaceMeta{Name: ""},
	})

	// Submit pod failures on the same node.
	for i := 0; i < 3; i++ {
		podEvent := &model.FaultEvent{
			ID:           fmt.Sprintf("pod-node-event-%d", i),
			DetectorName: "PodFailed",
			Severity:     model.SeverityWarning,
			Timestamp:    time.Now().UTC(),
			Resource: model.ResourceRef{
				Kind:      "Pod",
				Namespace: "default",
				Name:      fmt.Sprintf("pod-%d", i),
				UID:       fmt.Sprintf("uid-pn-%d", i),
			},
			Description: "Pod failed",
			Labels:      map[string]string{},
			Annotations: map[string]string{"wormsign.io/node-name": "node-1"},
		}
		p.SubmitFaultEvent(podEvent, filter.FilterInput{
			DetectorName: podEvent.DetectorName,
			Resource:     filter.ResourceMeta{Kind: "Pod", Namespace: "default", Name: fmt.Sprintf("pod-%d", i)},
			Namespace:    filter.NamespaceMeta{Name: "default"},
		})
	}

	// Wait for the correlation window and the event to flow through.
	deadline := time.After(10 * time.Second)
	for {
		delivered := s.getDelivered()
		if len(delivered) > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for correlated node cascade delivery")
		default:
			time.Sleep(100 * time.Millisecond)
		}
	}

	p.Stop()
}

func TestPipeline_NoGatherers(t *testing.T) {
	m := newTestMetrics(t)
	a := &mockAnalyzer{name: "test-analyzer", healthVal: true}
	s := &mockSink{name: "test-sink"}

	cfg := DefaultConfig()
	cfg.Correlation.Enabled = false
	cfg.Workers.Gathering = 1
	cfg.Workers.Analysis = 1
	cfg.Workers.Sink = 1

	// No gatherers — bundle should have empty sections.
	p, err := New(cfg,
		WithMetrics(m),
		WithAnalyzer(a),
		WithSinks([]Sink{s}),
		WithLogger(slog.Default()),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := p.Start(ctx); err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	event := &model.FaultEvent{
		ID:           "no-gatherer-event",
		DetectorName: "PodFailed",
		Severity:     model.SeverityWarning,
		Timestamp:    time.Now().UTC(),
		Resource: model.ResourceRef{
			Kind:      "Pod",
			Namespace: "default",
			Name:      "test-pod",
			UID:       "uid-ng",
		},
		Description: "Pod failed",
		Labels:      map[string]string{},
		Annotations: map[string]string{},
	}

	p.SubmitFaultEvent(event, filter.FilterInput{
		DetectorName: event.DetectorName,
		Resource:     filter.ResourceMeta{Kind: "Pod", Namespace: "default", Name: "test-pod"},
		Namespace:    filter.NamespaceMeta{Name: "default"},
	})

	deadline := time.After(10 * time.Second)
	for {
		delivered := s.getDelivered()
		if len(delivered) > 0 {
			report := delivered[0]
			if len(report.DiagnosticBundle.Sections) != 0 {
				t.Errorf("expected 0 sections with no gatherers, got %d", len(report.DiagnosticBundle.Sections))
			}
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out")
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}

	p.Stop()
}
