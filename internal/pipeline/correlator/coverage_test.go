package correlator

import (
	"testing"
	"time"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

// TestNew_WithNowFuncOption verifies that the WithNowFunc option is applied
// correctly during construction.
func TestNew_WithNowFuncOption(t *testing.T) {
	m := testMetrics(t)
	collector := &resultCollector{}
	cfg := DefaultConfig()

	fixedTime := time.Date(2026, 2, 8, 12, 0, 0, 0, time.UTC)
	nowFn := func() time.Time { return fixedTime }

	c, err := New(cfg, collector.handler, m, nil, WithNowFunc(nowFn))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Verify the nowFunc was set.
	got := c.nowFunc()
	if !got.Equal(fixedTime) {
		t.Errorf("nowFunc() = %v, want %v", got, fixedTime)
	}
}

// TestNew_NilLogger verifies that a nil logger defaults to slog.Default().
func TestNew_NilLoggerUsesDefault(t *testing.T) {
	m := testMetrics(t)
	collector := &resultCollector{}
	cfg := DefaultConfig()

	c, err := New(cfg, collector.handler, m, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	if c.logger == nil {
		t.Fatal("logger should not be nil when nil was passed")
	}
}

// TestNew_MultipleOptions verifies that multiple options are applied.
func TestNew_MultipleOptions(t *testing.T) {
	m := testMetrics(t)
	collector := &resultCollector{}
	cfg := DefaultConfig()

	fixedTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	c, err := New(cfg, collector.handler, m, nil,
		WithNowFunc(func() time.Time { return fixedTime }),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	got := c.nowFunc()
	if !got.Equal(fixedTime) {
		t.Errorf("nowFunc() = %v, want %v", got, fixedTime)
	}
}

// TestEvaluate_EmptyNamespaceEvents tests that events with empty namespace
// are handled correctly by NamespaceStorm rule.
func TestEvaluate_EmptyNamespaceEventSkipped(t *testing.T) {
	m := testMetrics(t)
	collector := &resultCollector{}
	cfg := DefaultConfig()
	cfg.Enabled = true
	cfg.Rules.NamespaceStorm.Enabled = true
	cfg.Rules.NamespaceStorm.Threshold = 1

	c, err := New(cfg, collector.handler, m, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Event with empty namespace should be skipped by NamespaceStorm.
	events := []model.FaultEvent{
		{
			ID:           "fe-1",
			DetectorName: "PodFailed",
			Severity:     model.SeverityWarning,
			Resource: model.ResourceRef{
				Kind: "Node",
				Name: "node-1",
				// No namespace - cluster-scoped resource
			},
		},
	}

	c.buffer = events
	c.evaluateAndEmit()

	results := collector.getResults()
	if len(results) == 0 {
		t.Fatal("expected at least one result")
	}

	// Cluster-scoped event should pass through as uncorrelated.
	uncorrelated := collector.allUncorrelated()
	if len(uncorrelated) != 1 {
		t.Errorf("expected 1 uncorrelated event, got %d", len(uncorrelated))
	}
}

// TestEvaluate_SingleEventPassthrough tests that a single event with no matching
// rules passes through as uncorrelated.
func TestEvaluate_SingleEventNoRuleMatch(t *testing.T) {
	m := testMetrics(t)
	collector := &resultCollector{}
	cfg := DefaultConfig()
	cfg.Enabled = true
	// Disable all rules to ensure pass-through.
	cfg.Rules.NodeCascade.Enabled = false
	cfg.Rules.DeploymentRollout.Enabled = false
	cfg.Rules.StorageCascade.Enabled = false
	cfg.Rules.NamespaceStorm.Enabled = false

	c, err := New(cfg, collector.handler, m, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events := []model.FaultEvent{
		makeFaultEvent("fe-1", "PodFailed", model.SeverityWarning, "Pod", "default", "pod-1", nil),
	}

	c.buffer = events
	c.evaluateAndEmit()

	results := collector.getResults()
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if len(results[0].UncorrelatedEvents) != 1 {
		t.Errorf("expected 1 uncorrelated event, got %d", len(results[0].UncorrelatedEvents))
	}
	if len(results[0].SuperEvents) != 0 {
		t.Errorf("expected 0 super events, got %d", len(results[0].SuperEvents))
	}
}
