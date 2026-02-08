package correlator

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/k8s-wormsign/k8s-wormsign/internal/metrics"
	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

// testMetrics creates a Metrics instance with a fresh registry for testing.
func testMetrics(t *testing.T) *metrics.Metrics {
	t.Helper()
	reg := prometheus.NewRegistry()
	return metrics.NewMetrics(reg)
}

// collectResults collects CorrelationResults from the handler.
type resultCollector struct {
	mu      sync.Mutex
	results []CorrelationResult
}

func (rc *resultCollector) handler(result CorrelationResult) {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	rc.results = append(rc.results, result)
}

func (rc *resultCollector) getResults() []CorrelationResult {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	out := make([]CorrelationResult, len(rc.results))
	copy(out, rc.results)
	return out
}

// allSuperEvents returns all SuperEvents across all collected results.
func (rc *resultCollector) allSuperEvents() []*model.SuperEvent {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	var all []*model.SuperEvent
	for _, r := range rc.results {
		all = append(all, r.SuperEvents...)
	}
	return all
}

// allUncorrelated returns all uncorrelated FaultEvents across all results.
func (rc *resultCollector) allUncorrelated() []model.FaultEvent {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	var all []model.FaultEvent
	for _, r := range rc.results {
		all = append(all, r.UncorrelatedEvents...)
	}
	return all
}

// makeFaultEvent creates a test FaultEvent with the given parameters.
func makeFaultEvent(id, detector string, severity model.Severity, kind, namespace, name string, annotations map[string]string) model.FaultEvent {
	if annotations == nil {
		annotations = make(map[string]string)
	}
	return model.FaultEvent{
		ID:           id,
		DetectorName: detector,
		Severity:     severity,
		Timestamp:    time.Now().UTC(),
		Resource: model.ResourceRef{
			Kind:      kind,
			Namespace: namespace,
			Name:      name,
			UID:       "uid-" + id,
		},
		Description: detector + " detected on " + name,
		Labels:      make(map[string]string),
		Annotations: annotations,
	}
}

func TestNew_ValidConfig(t *testing.T) {
	collector := &resultCollector{}
	m := testMetrics(t)

	c, err := New(DefaultConfig(), collector.handler, m, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if c == nil {
		t.Fatal("New() returned nil")
	}
}

func TestNew_InvalidConfig(t *testing.T) {
	collector := &resultCollector{}
	m := testMetrics(t)

	tests := []struct {
		name    string
		cfg     Config
		wantErr string
	}{
		{
			name: "zero window duration",
			cfg: Config{
				Enabled:        true,
				WindowDuration: 0,
			},
			wantErr: "window duration must be positive",
		},
		{
			name: "negative window duration",
			cfg: Config{
				Enabled:        true,
				WindowDuration: -1 * time.Second,
			},
			wantErr: "window duration must be positive",
		},
		{
			name: "NodeCascade zero min failures",
			cfg: Config{
				Enabled:        true,
				WindowDuration: time.Minute,
				Rules: RulesConfig{
					NodeCascade: NodeCascadeConfig{Enabled: true, MinPodFailures: 0},
				},
			},
			wantErr: "NodeCascade minPodFailures must be >= 1",
		},
		{
			name: "DeploymentRollout zero min failures",
			cfg: Config{
				Enabled:        true,
				WindowDuration: time.Minute,
				Rules: RulesConfig{
					DeploymentRollout: DeploymentRolloutConfig{Enabled: true, MinPodFailures: 0},
				},
			},
			wantErr: "DeploymentRollout minPodFailures must be >= 1",
		},
		{
			name: "NamespaceStorm zero threshold",
			cfg: Config{
				Enabled:        true,
				WindowDuration: time.Minute,
				Rules: RulesConfig{
					NamespaceStorm: NamespaceStormConfig{Enabled: true, Threshold: 0},
				},
			},
			wantErr: "NamespaceStorm threshold must be >= 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := New(tt.cfg, collector.handler, m, nil)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if c != nil {
				t.Error("expected nil correlator on error")
			}
			if !containsSubstring(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestNew_NilHandler(t *testing.T) {
	m := testMetrics(t)
	_, err := New(DefaultConfig(), nil, m, nil)
	if err == nil {
		t.Fatal("expected error for nil handler")
	}
}

func TestNew_NilMetrics(t *testing.T) {
	collector := &resultCollector{}
	_, err := New(DefaultConfig(), collector.handler, nil, nil)
	if err == nil {
		t.Fatal("expected error for nil metrics")
	}
}

func TestSubmit_DisabledPassesThrough(t *testing.T) {
	collector := &resultCollector{}
	m := testMetrics(t)

	cfg := DefaultConfig()
	cfg.Enabled = false

	c, err := New(cfg, collector.handler, m, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	event := makeFaultEvent("e1", "PodCrashLoop", model.SeverityWarning, "Pod", "default", "pod-1", nil)
	c.Submit(event)

	results := collector.getResults()
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if len(results[0].UncorrelatedEvents) != 1 {
		t.Fatalf("expected 1 uncorrelated event, got %d", len(results[0].UncorrelatedEvents))
	}
	if results[0].UncorrelatedEvents[0].ID != "e1" {
		t.Errorf("event ID = %q, want %q", results[0].UncorrelatedEvents[0].ID, "e1")
	}
}

func TestSubmit_EnabledBuffersEvent(t *testing.T) {
	collector := &resultCollector{}
	m := testMetrics(t)
	cfg := DefaultConfig()

	c, err := New(cfg, collector.handler, m, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	event := makeFaultEvent("e1", "PodCrashLoop", model.SeverityWarning, "Pod", "default", "pod-1", nil)
	c.Submit(event)

	// Should be buffered, not emitted.
	results := collector.getResults()
	if len(results) != 0 {
		t.Errorf("expected 0 results (buffered), got %d", len(results))
	}

	// Check buffer.
	c.mu.Lock()
	bufLen := len(c.buffer)
	c.mu.Unlock()
	if bufLen != 1 {
		t.Errorf("buffer length = %d, want 1", bufLen)
	}
}

func TestFlush_DrainsBuffer(t *testing.T) {
	collector := &resultCollector{}
	m := testMetrics(t)
	cfg := DefaultConfig()
	cfg.WindowDuration = 10 * time.Minute // long window so ticker won't fire

	c, err := New(cfg, collector.handler, m, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runDone := make(chan struct{})
	go func() {
		c.Run(ctx)
		close(runDone)
	}()

	// Submit events.
	c.Submit(makeFaultEvent("e1", "PodCrashLoop", model.SeverityWarning, "Pod", "default", "pod-1", nil))
	c.Submit(makeFaultEvent("e2", "PodFailed", model.SeverityCritical, "Pod", "default", "pod-2", nil))

	// Give a small moment for events to be buffered.
	time.Sleep(10 * time.Millisecond)

	// Flush should drain.
	c.Flush()
	<-runDone

	uncorrelated := collector.allUncorrelated()
	if len(uncorrelated) != 2 {
		t.Errorf("expected 2 uncorrelated events after flush, got %d", len(uncorrelated))
	}
}

func TestRun_ContextCancellation(t *testing.T) {
	collector := &resultCollector{}
	m := testMetrics(t)
	cfg := DefaultConfig()
	cfg.WindowDuration = 10 * time.Minute

	c, err := New(cfg, collector.handler, m, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	runDone := make(chan struct{})
	go func() {
		c.Run(ctx)
		close(runDone)
	}()

	c.Submit(makeFaultEvent("e1", "PodCrashLoop", model.SeverityWarning, "Pod", "default", "pod-1", nil))
	time.Sleep(10 * time.Millisecond)

	cancel()

	select {
	case <-runDone:
		// OK
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop within timeout after context cancellation")
	}

	uncorrelated := collector.allUncorrelated()
	if len(uncorrelated) != 1 {
		t.Errorf("expected 1 uncorrelated event after context cancel, got %d", len(uncorrelated))
	}
}

func TestRun_DisabledMode(t *testing.T) {
	collector := &resultCollector{}
	m := testMetrics(t)
	cfg := DefaultConfig()
	cfg.Enabled = false

	c, err := New(cfg, collector.handler, m, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	runDone := make(chan struct{})
	go func() {
		c.Run(ctx)
		close(runDone)
	}()

	cancel()

	select {
	case <-runDone:
		// OK
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not stop within timeout for disabled correlator")
	}
}

func TestEvaluate_EmptyBuffer(t *testing.T) {
	collector := &resultCollector{}
	m := testMetrics(t)

	c, err := New(DefaultConfig(), collector.handler, m, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	result := c.evaluate(nil)
	if len(result.SuperEvents) != 0 {
		t.Errorf("expected 0 super events, got %d", len(result.SuperEvents))
	}
	if len(result.UncorrelatedEvents) != 0 {
		t.Errorf("expected 0 uncorrelated events, got %d", len(result.UncorrelatedEvents))
	}
}

func TestEvaluate_AllUncorrelated(t *testing.T) {
	collector := &resultCollector{}
	m := testMetrics(t)
	cfg := DefaultConfig()
	cfg.Rules.NamespaceStorm.Threshold = 100 // high threshold so NamespaceStorm won't trigger

	c, err := New(cfg, collector.handler, m, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events := []model.FaultEvent{
		makeFaultEvent("e1", "PodCrashLoop", model.SeverityWarning, "Pod", "default", "pod-1", nil),
		makeFaultEvent("e2", "PodFailed", model.SeverityCritical, "Pod", "other-ns", "pod-2", nil),
	}

	result := c.evaluate(events)
	if len(result.SuperEvents) != 0 {
		t.Errorf("expected 0 super events, got %d", len(result.SuperEvents))
	}
	if len(result.UncorrelatedEvents) != 2 {
		t.Errorf("expected 2 uncorrelated events, got %d", len(result.UncorrelatedEvents))
	}
}

// --- NodeCascade tests ---

func TestNodeCascade_BasicCorrelation(t *testing.T) {
	collector := &resultCollector{}
	m := testMetrics(t)
	cfg := DefaultConfig()
	cfg.Rules.NodeCascade.MinPodFailures = 2

	c, err := New(cfg, collector.handler, m, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events := []model.FaultEvent{
		makeFaultEvent("n1", "NodeNotReady", model.SeverityCritical, "Node", "", "node-1", nil),
		makeFaultEvent("p1", "PodFailed", model.SeverityWarning, "Pod", "default", "pod-1",
			map[string]string{nodeAnnotationKey: "node-1"}),
		makeFaultEvent("p2", "PodCrashLoop", model.SeverityWarning, "Pod", "default", "pod-2",
			map[string]string{nodeAnnotationKey: "node-1"}),
	}

	result := c.evaluate(events)
	if len(result.SuperEvents) != 1 {
		t.Fatalf("expected 1 super event, got %d", len(result.SuperEvents))
	}

	se := result.SuperEvents[0]
	if se.CorrelationRule != "NodeCascade" {
		t.Errorf("CorrelationRule = %q, want %q", se.CorrelationRule, "NodeCascade")
	}
	if se.PrimaryResource.Kind != "Node" {
		t.Errorf("PrimaryResource.Kind = %q, want %q", se.PrimaryResource.Kind, "Node")
	}
	if se.PrimaryResource.Name != "node-1" {
		t.Errorf("PrimaryResource.Name = %q, want %q", se.PrimaryResource.Name, "node-1")
	}
	if len(se.FaultEvents) != 3 {
		t.Errorf("FaultEvents count = %d, want 3 (1 node + 2 pods)", len(se.FaultEvents))
	}
	if se.Severity != model.SeverityCritical {
		t.Errorf("Severity = %q, want %q", se.Severity, model.SeverityCritical)
	}
	if len(result.UncorrelatedEvents) != 0 {
		t.Errorf("expected 0 uncorrelated events, got %d", len(result.UncorrelatedEvents))
	}
}

func TestNodeCascade_BelowThreshold(t *testing.T) {
	collector := &resultCollector{}
	m := testMetrics(t)
	cfg := DefaultConfig()
	cfg.Rules.NodeCascade.MinPodFailures = 3

	c, err := New(cfg, collector.handler, m, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events := []model.FaultEvent{
		makeFaultEvent("n1", "NodeNotReady", model.SeverityCritical, "Node", "", "node-1", nil),
		makeFaultEvent("p1", "PodFailed", model.SeverityWarning, "Pod", "default", "pod-1",
			map[string]string{nodeAnnotationKey: "node-1"}),
		// Only 1 pod failure, threshold is 3.
	}

	result := c.evaluate(events)
	if len(result.SuperEvents) != 0 {
		t.Errorf("expected 0 super events (below threshold), got %d", len(result.SuperEvents))
	}
	// All events should be uncorrelated.
	if len(result.UncorrelatedEvents) != 2 {
		t.Errorf("expected 2 uncorrelated events, got %d", len(result.UncorrelatedEvents))
	}
}

func TestNodeCascade_NoNodeEvent(t *testing.T) {
	collector := &resultCollector{}
	m := testMetrics(t)
	cfg := DefaultConfig()
	cfg.Rules.NamespaceStorm.Threshold = 100

	c, err := New(cfg, collector.handler, m, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events := []model.FaultEvent{
		makeFaultEvent("p1", "PodFailed", model.SeverityWarning, "Pod", "default", "pod-1",
			map[string]string{nodeAnnotationKey: "node-1"}),
		makeFaultEvent("p2", "PodFailed", model.SeverityWarning, "Pod", "default", "pod-2",
			map[string]string{nodeAnnotationKey: "node-1"}),
	}

	result := c.evaluate(events)
	if len(result.SuperEvents) != 0 {
		t.Errorf("expected 0 super events without node event, got %d", len(result.SuperEvents))
	}
}

func TestNodeCascade_MultipleNodes(t *testing.T) {
	collector := &resultCollector{}
	m := testMetrics(t)
	cfg := DefaultConfig()
	cfg.Rules.NodeCascade.MinPodFailures = 2

	c, err := New(cfg, collector.handler, m, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events := []model.FaultEvent{
		makeFaultEvent("n1", "NodeNotReady", model.SeverityCritical, "Node", "", "node-1", nil),
		makeFaultEvent("n2", "NodeNotReady", model.SeverityWarning, "Node", "", "node-2", nil),
		makeFaultEvent("p1", "PodFailed", model.SeverityWarning, "Pod", "default", "pod-1",
			map[string]string{nodeAnnotationKey: "node-1"}),
		makeFaultEvent("p2", "PodFailed", model.SeverityWarning, "Pod", "default", "pod-2",
			map[string]string{nodeAnnotationKey: "node-1"}),
		makeFaultEvent("p3", "PodFailed", model.SeverityWarning, "Pod", "default", "pod-3",
			map[string]string{nodeAnnotationKey: "node-2"}),
		makeFaultEvent("p4", "PodFailed", model.SeverityCritical, "Pod", "default", "pod-4",
			map[string]string{nodeAnnotationKey: "node-2"}),
	}

	result := c.evaluate(events)
	if len(result.SuperEvents) != 2 {
		t.Fatalf("expected 2 super events (one per node), got %d", len(result.SuperEvents))
	}
	if len(result.UncorrelatedEvents) != 0 {
		t.Errorf("expected 0 uncorrelated events, got %d", len(result.UncorrelatedEvents))
	}
}

func TestNodeCascade_Disabled(t *testing.T) {
	collector := &resultCollector{}
	m := testMetrics(t)
	cfg := DefaultConfig()
	cfg.Rules.NodeCascade.Enabled = false
	cfg.Rules.NamespaceStorm.Threshold = 100

	c, err := New(cfg, collector.handler, m, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events := []model.FaultEvent{
		makeFaultEvent("n1", "NodeNotReady", model.SeverityCritical, "Node", "", "node-1", nil),
		makeFaultEvent("p1", "PodFailed", model.SeverityWarning, "Pod", "default", "pod-1",
			map[string]string{nodeAnnotationKey: "node-1"}),
		makeFaultEvent("p2", "PodFailed", model.SeverityWarning, "Pod", "default", "pod-2",
			map[string]string{nodeAnnotationKey: "node-1"}),
	}

	result := c.evaluate(events)
	if len(result.SuperEvents) != 0 {
		t.Errorf("expected 0 super events when NodeCascade disabled, got %d", len(result.SuperEvents))
	}
}

// --- DeploymentRollout tests ---

func TestDeploymentRollout_BasicCorrelation(t *testing.T) {
	collector := &resultCollector{}
	m := testMetrics(t)
	cfg := DefaultConfig()
	cfg.Rules.DeploymentRollout.MinPodFailures = 3

	c, err := New(cfg, collector.handler, m, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events := []model.FaultEvent{
		makeFaultEvent("p1", "PodCrashLoop", model.SeverityWarning, "Pod", "default", "web-api-abc-1",
			map[string]string{deploymentAnnotationKey: "web-api"}),
		makeFaultEvent("p2", "PodCrashLoop", model.SeverityWarning, "Pod", "default", "web-api-abc-2",
			map[string]string{deploymentAnnotationKey: "web-api"}),
		makeFaultEvent("p3", "PodFailed", model.SeverityCritical, "Pod", "default", "web-api-abc-3",
			map[string]string{deploymentAnnotationKey: "web-api"}),
	}

	result := c.evaluate(events)
	if len(result.SuperEvents) != 1 {
		t.Fatalf("expected 1 super event, got %d", len(result.SuperEvents))
	}

	se := result.SuperEvents[0]
	if se.CorrelationRule != "DeploymentRollout" {
		t.Errorf("CorrelationRule = %q, want %q", se.CorrelationRule, "DeploymentRollout")
	}
	if se.PrimaryResource.Kind != "Deployment" {
		t.Errorf("PrimaryResource.Kind = %q, want %q", se.PrimaryResource.Kind, "Deployment")
	}
	if se.PrimaryResource.Name != "web-api" {
		t.Errorf("PrimaryResource.Name = %q, want %q", se.PrimaryResource.Name, "web-api")
	}
	if se.PrimaryResource.Namespace != "default" {
		t.Errorf("PrimaryResource.Namespace = %q, want %q", se.PrimaryResource.Namespace, "default")
	}
	if len(se.FaultEvents) != 3 {
		t.Errorf("FaultEvents count = %d, want 3", len(se.FaultEvents))
	}
	if se.Severity != model.SeverityCritical {
		t.Errorf("Severity = %q, want %q (highest)", se.Severity, model.SeverityCritical)
	}
	if len(result.UncorrelatedEvents) != 0 {
		t.Errorf("expected 0 uncorrelated events, got %d", len(result.UncorrelatedEvents))
	}
}

func TestDeploymentRollout_BelowThreshold(t *testing.T) {
	collector := &resultCollector{}
	m := testMetrics(t)
	cfg := DefaultConfig()
	cfg.Rules.DeploymentRollout.MinPodFailures = 3
	cfg.Rules.NamespaceStorm.Threshold = 100

	c, err := New(cfg, collector.handler, m, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events := []model.FaultEvent{
		makeFaultEvent("p1", "PodCrashLoop", model.SeverityWarning, "Pod", "default", "web-api-abc-1",
			map[string]string{deploymentAnnotationKey: "web-api"}),
		makeFaultEvent("p2", "PodCrashLoop", model.SeverityWarning, "Pod", "default", "web-api-abc-2",
			map[string]string{deploymentAnnotationKey: "web-api"}),
		// Only 2, threshold is 3.
	}

	result := c.evaluate(events)
	if len(result.SuperEvents) != 0 {
		t.Errorf("expected 0 super events below threshold, got %d", len(result.SuperEvents))
	}
	if len(result.UncorrelatedEvents) != 2 {
		t.Errorf("expected 2 uncorrelated events, got %d", len(result.UncorrelatedEvents))
	}
}

func TestDeploymentRollout_MultipleDeployments(t *testing.T) {
	collector := &resultCollector{}
	m := testMetrics(t)
	cfg := DefaultConfig()
	cfg.Rules.DeploymentRollout.MinPodFailures = 2

	c, err := New(cfg, collector.handler, m, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events := []model.FaultEvent{
		makeFaultEvent("p1", "PodFailed", model.SeverityWarning, "Pod", "default", "web-1",
			map[string]string{deploymentAnnotationKey: "web"}),
		makeFaultEvent("p2", "PodFailed", model.SeverityWarning, "Pod", "default", "web-2",
			map[string]string{deploymentAnnotationKey: "web"}),
		makeFaultEvent("p3", "PodFailed", model.SeverityWarning, "Pod", "payments", "pay-1",
			map[string]string{deploymentAnnotationKey: "pay-api"}),
		makeFaultEvent("p4", "PodFailed", model.SeverityCritical, "Pod", "payments", "pay-2",
			map[string]string{deploymentAnnotationKey: "pay-api"}),
	}

	result := c.evaluate(events)
	if len(result.SuperEvents) != 2 {
		t.Fatalf("expected 2 super events, got %d", len(result.SuperEvents))
	}
	if len(result.UncorrelatedEvents) != 0 {
		t.Errorf("expected 0 uncorrelated events, got %d", len(result.UncorrelatedEvents))
	}
}

func TestDeploymentRollout_SameDeployDifferentNamespaces(t *testing.T) {
	collector := &resultCollector{}
	m := testMetrics(t)
	cfg := DefaultConfig()
	cfg.Rules.DeploymentRollout.MinPodFailures = 2
	cfg.Rules.NamespaceStorm.Threshold = 100

	c, err := New(cfg, collector.handler, m, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Same deployment name in different namespaces should NOT correlate.
	events := []model.FaultEvent{
		makeFaultEvent("p1", "PodFailed", model.SeverityWarning, "Pod", "ns-a", "web-1",
			map[string]string{deploymentAnnotationKey: "web"}),
		makeFaultEvent("p2", "PodFailed", model.SeverityWarning, "Pod", "ns-b", "web-2",
			map[string]string{deploymentAnnotationKey: "web"}),
	}

	result := c.evaluate(events)
	if len(result.SuperEvents) != 0 {
		t.Errorf("expected 0 super events (different namespaces), got %d", len(result.SuperEvents))
	}
	if len(result.UncorrelatedEvents) != 2 {
		t.Errorf("expected 2 uncorrelated events, got %d", len(result.UncorrelatedEvents))
	}
}

func TestDeploymentRollout_Disabled(t *testing.T) {
	collector := &resultCollector{}
	m := testMetrics(t)
	cfg := DefaultConfig()
	cfg.Rules.DeploymentRollout.Enabled = false
	cfg.Rules.NamespaceStorm.Threshold = 100

	c, err := New(cfg, collector.handler, m, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events := []model.FaultEvent{
		makeFaultEvent("p1", "PodFailed", model.SeverityWarning, "Pod", "default", "web-1",
			map[string]string{deploymentAnnotationKey: "web"}),
		makeFaultEvent("p2", "PodFailed", model.SeverityWarning, "Pod", "default", "web-2",
			map[string]string{deploymentAnnotationKey: "web"}),
		makeFaultEvent("p3", "PodFailed", model.SeverityWarning, "Pod", "default", "web-3",
			map[string]string{deploymentAnnotationKey: "web"}),
	}

	result := c.evaluate(events)
	if len(result.SuperEvents) != 0 {
		t.Errorf("expected 0 super events when disabled, got %d", len(result.SuperEvents))
	}
}

// --- StorageCascade tests ---

func TestStorageCascade_BasicCorrelation(t *testing.T) {
	collector := &resultCollector{}
	m := testMetrics(t)
	cfg := DefaultConfig()

	c, err := New(cfg, collector.handler, m, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events := []model.FaultEvent{
		makeFaultEvent("pvc1", "PVCStuckBinding", model.SeverityWarning, "PersistentVolumeClaim", "default", "data-pvc", nil),
		makeFaultEvent("p1", "PodStuckPending", model.SeverityWarning, "Pod", "default", "db-pod-1",
			map[string]string{pvcAnnotationKey: "data-pvc"}),
	}

	result := c.evaluate(events)
	if len(result.SuperEvents) != 1 {
		t.Fatalf("expected 1 super event, got %d", len(result.SuperEvents))
	}

	se := result.SuperEvents[0]
	if se.CorrelationRule != "StorageCascade" {
		t.Errorf("CorrelationRule = %q, want %q", se.CorrelationRule, "StorageCascade")
	}
	if se.PrimaryResource.Kind != "PersistentVolumeClaim" {
		t.Errorf("PrimaryResource.Kind = %q, want %q", se.PrimaryResource.Kind, "PersistentVolumeClaim")
	}
	if se.PrimaryResource.Name != "data-pvc" {
		t.Errorf("PrimaryResource.Name = %q, want %q", se.PrimaryResource.Name, "data-pvc")
	}
	if len(se.FaultEvents) != 2 {
		t.Errorf("FaultEvents count = %d, want 2 (1 PVC + 1 pod)", len(se.FaultEvents))
	}
}

func TestStorageCascade_MultiplePodsSamePVC(t *testing.T) {
	collector := &resultCollector{}
	m := testMetrics(t)
	cfg := DefaultConfig()

	c, err := New(cfg, collector.handler, m, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events := []model.FaultEvent{
		makeFaultEvent("pvc1", "PVCStuckBinding", model.SeverityWarning, "PersistentVolumeClaim", "default", "shared-pvc", nil),
		makeFaultEvent("p1", "PodStuckPending", model.SeverityWarning, "Pod", "default", "pod-1",
			map[string]string{pvcAnnotationKey: "shared-pvc"}),
		makeFaultEvent("p2", "PodStuckPending", model.SeverityWarning, "Pod", "default", "pod-2",
			map[string]string{pvcAnnotationKey: "shared-pvc"}),
		makeFaultEvent("p3", "PodStuckPending", model.SeverityCritical, "Pod", "default", "pod-3",
			map[string]string{pvcAnnotationKey: "shared-pvc"}),
	}

	result := c.evaluate(events)
	if len(result.SuperEvents) != 1 {
		t.Fatalf("expected 1 super event, got %d", len(result.SuperEvents))
	}
	if len(result.SuperEvents[0].FaultEvents) != 4 {
		t.Errorf("expected 4 fault events in super event, got %d", len(result.SuperEvents[0].FaultEvents))
	}
	if result.SuperEvents[0].Severity != model.SeverityCritical {
		t.Errorf("Severity = %q, want %q", result.SuperEvents[0].Severity, model.SeverityCritical)
	}
}

func TestStorageCascade_NoPodFailures(t *testing.T) {
	collector := &resultCollector{}
	m := testMetrics(t)
	cfg := DefaultConfig()
	cfg.Rules.NamespaceStorm.Threshold = 100

	c, err := New(cfg, collector.handler, m, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events := []model.FaultEvent{
		makeFaultEvent("pvc1", "PVCStuckBinding", model.SeverityWarning, "PersistentVolumeClaim", "default", "data-pvc", nil),
		// No pods referencing this PVC.
	}

	result := c.evaluate(events)
	if len(result.SuperEvents) != 0 {
		t.Errorf("expected 0 super events without pod failures, got %d", len(result.SuperEvents))
	}
	if len(result.UncorrelatedEvents) != 1 {
		t.Errorf("expected 1 uncorrelated event (PVC), got %d", len(result.UncorrelatedEvents))
	}
}

func TestStorageCascade_DifferentNamespaces(t *testing.T) {
	collector := &resultCollector{}
	m := testMetrics(t)
	cfg := DefaultConfig()
	cfg.Rules.NamespaceStorm.Threshold = 100

	c, err := New(cfg, collector.handler, m, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// PVC in one namespace, pod in another — should NOT correlate.
	events := []model.FaultEvent{
		makeFaultEvent("pvc1", "PVCStuckBinding", model.SeverityWarning, "PersistentVolumeClaim", "ns-a", "data-pvc", nil),
		makeFaultEvent("p1", "PodStuckPending", model.SeverityWarning, "Pod", "ns-b", "pod-1",
			map[string]string{pvcAnnotationKey: "data-pvc"}),
	}

	result := c.evaluate(events)
	if len(result.SuperEvents) != 0 {
		t.Errorf("expected 0 super events (different namespaces), got %d", len(result.SuperEvents))
	}
}

func TestStorageCascade_Disabled(t *testing.T) {
	collector := &resultCollector{}
	m := testMetrics(t)
	cfg := DefaultConfig()
	cfg.Rules.StorageCascade.Enabled = false
	cfg.Rules.NamespaceStorm.Threshold = 100

	c, err := New(cfg, collector.handler, m, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events := []model.FaultEvent{
		makeFaultEvent("pvc1", "PVCStuckBinding", model.SeverityWarning, "PersistentVolumeClaim", "default", "data-pvc", nil),
		makeFaultEvent("p1", "PodStuckPending", model.SeverityWarning, "Pod", "default", "pod-1",
			map[string]string{pvcAnnotationKey: "data-pvc"}),
	}

	result := c.evaluate(events)
	if len(result.SuperEvents) != 0 {
		t.Errorf("expected 0 super events when disabled, got %d", len(result.SuperEvents))
	}
}

// --- NamespaceStorm tests ---

func TestNamespaceStorm_BasicCorrelation(t *testing.T) {
	collector := &resultCollector{}
	m := testMetrics(t)
	cfg := DefaultConfig()
	cfg.Rules.NamespaceStorm.Threshold = 3
	// Disable other rules so they don't consume events.
	cfg.Rules.NodeCascade.Enabled = false
	cfg.Rules.DeploymentRollout.Enabled = false
	cfg.Rules.StorageCascade.Enabled = false

	c, err := New(cfg, collector.handler, m, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events := make([]model.FaultEvent, 0, 5)
	for i := 0; i < 5; i++ {
		events = append(events, makeFaultEvent(
			fmt.Sprintf("e%d", i),
			"PodFailed",
			model.SeverityWarning,
			"Pod", "storm-ns", fmt.Sprintf("pod-%d", i),
			nil,
		))
	}

	result := c.evaluate(events)
	if len(result.SuperEvents) != 1 {
		t.Fatalf("expected 1 super event, got %d", len(result.SuperEvents))
	}

	se := result.SuperEvents[0]
	if se.CorrelationRule != "NamespaceStorm" {
		t.Errorf("CorrelationRule = %q, want %q", se.CorrelationRule, "NamespaceStorm")
	}
	if se.PrimaryResource.Kind != "Namespace" {
		t.Errorf("PrimaryResource.Kind = %q, want %q", se.PrimaryResource.Kind, "Namespace")
	}
	if se.PrimaryResource.Name != "storm-ns" {
		t.Errorf("PrimaryResource.Name = %q, want %q", se.PrimaryResource.Name, "storm-ns")
	}
	if len(se.FaultEvents) != 5 {
		t.Errorf("FaultEvents count = %d, want 5", len(se.FaultEvents))
	}
	if len(result.UncorrelatedEvents) != 0 {
		t.Errorf("expected 0 uncorrelated events, got %d", len(result.UncorrelatedEvents))
	}
}

func TestNamespaceStorm_AtThreshold(t *testing.T) {
	collector := &resultCollector{}
	m := testMetrics(t)
	cfg := DefaultConfig()
	cfg.Rules.NamespaceStorm.Threshold = 5
	cfg.Rules.NodeCascade.Enabled = false
	cfg.Rules.DeploymentRollout.Enabled = false
	cfg.Rules.StorageCascade.Enabled = false

	c, err := New(cfg, collector.handler, m, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Exactly at threshold (5) — should NOT trigger (spec says >N, not >=N).
	events := make([]model.FaultEvent, 0, 5)
	for i := 0; i < 5; i++ {
		events = append(events, makeFaultEvent(
			fmt.Sprintf("e%d", i),
			"PodFailed",
			model.SeverityWarning,
			"Pod", "ns", fmt.Sprintf("pod-%d", i),
			nil,
		))
	}

	result := c.evaluate(events)
	if len(result.SuperEvents) != 0 {
		t.Errorf("expected 0 super events at exact threshold, got %d", len(result.SuperEvents))
	}
	if len(result.UncorrelatedEvents) != 5 {
		t.Errorf("expected 5 uncorrelated events, got %d", len(result.UncorrelatedEvents))
	}
}

func TestNamespaceStorm_AboveThreshold(t *testing.T) {
	collector := &resultCollector{}
	m := testMetrics(t)
	cfg := DefaultConfig()
	cfg.Rules.NamespaceStorm.Threshold = 5
	cfg.Rules.NodeCascade.Enabled = false
	cfg.Rules.DeploymentRollout.Enabled = false
	cfg.Rules.StorageCascade.Enabled = false

	c, err := New(cfg, collector.handler, m, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// 6 events > threshold of 5 — should trigger.
	events := make([]model.FaultEvent, 0, 6)
	for i := 0; i < 6; i++ {
		events = append(events, makeFaultEvent(
			fmt.Sprintf("e%d", i),
			"PodFailed",
			model.SeverityWarning,
			"Pod", "ns", fmt.Sprintf("pod-%d", i),
			nil,
		))
	}

	result := c.evaluate(events)
	if len(result.SuperEvents) != 1 {
		t.Fatalf("expected 1 super event above threshold, got %d", len(result.SuperEvents))
	}
	if len(result.SuperEvents[0].FaultEvents) != 6 {
		t.Errorf("expected 6 fault events, got %d", len(result.SuperEvents[0].FaultEvents))
	}
}

func TestNamespaceStorm_SkipsClusterScoped(t *testing.T) {
	collector := &resultCollector{}
	m := testMetrics(t)
	cfg := DefaultConfig()
	cfg.Rules.NamespaceStorm.Threshold = 1
	cfg.Rules.NodeCascade.Enabled = false
	cfg.Rules.DeploymentRollout.Enabled = false
	cfg.Rules.StorageCascade.Enabled = false

	c, err := New(cfg, collector.handler, m, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Cluster-scoped events (empty namespace) should not trigger NamespaceStorm.
	events := []model.FaultEvent{
		makeFaultEvent("n1", "NodeNotReady", model.SeverityCritical, "Node", "", "node-1", nil),
		makeFaultEvent("n2", "NodeNotReady", model.SeverityCritical, "Node", "", "node-2", nil),
		makeFaultEvent("n3", "NodeNotReady", model.SeverityCritical, "Node", "", "node-3", nil),
	}

	result := c.evaluate(events)
	if len(result.SuperEvents) != 0 {
		t.Errorf("expected 0 super events for cluster-scoped resources, got %d", len(result.SuperEvents))
	}
	if len(result.UncorrelatedEvents) != 3 {
		t.Errorf("expected 3 uncorrelated events, got %d", len(result.UncorrelatedEvents))
	}
}

func TestNamespaceStorm_OnlyUncorrelatedEvents(t *testing.T) {
	collector := &resultCollector{}
	m := testMetrics(t)
	cfg := DefaultConfig()
	cfg.Rules.NodeCascade.MinPodFailures = 2
	cfg.Rules.NamespaceStorm.Threshold = 3

	c, err := New(cfg, collector.handler, m, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// 1 node event + 2 pod failures on node (consumed by NodeCascade)
	// + 5 more pod failures in same namespace (uncorrelated → 5 > 3 threshold).
	events := []model.FaultEvent{
		makeFaultEvent("n1", "NodeNotReady", model.SeverityCritical, "Node", "", "node-1", nil),
		makeFaultEvent("p1", "PodFailed", model.SeverityWarning, "Pod", "default", "pod-1",
			map[string]string{nodeAnnotationKey: "node-1"}),
		makeFaultEvent("p2", "PodFailed", model.SeverityWarning, "Pod", "default", "pod-2",
			map[string]string{nodeAnnotationKey: "node-1"}),
		// These 5 are not correlated by NodeCascade (no node annotation).
		makeFaultEvent("p3", "PodCrashLoop", model.SeverityWarning, "Pod", "default", "pod-3", nil),
		makeFaultEvent("p4", "PodCrashLoop", model.SeverityWarning, "Pod", "default", "pod-4", nil),
		makeFaultEvent("p5", "PodCrashLoop", model.SeverityWarning, "Pod", "default", "pod-5", nil),
		makeFaultEvent("p6", "PodCrashLoop", model.SeverityWarning, "Pod", "default", "pod-6", nil),
		makeFaultEvent("p7", "PodCrashLoop", model.SeverityWarning, "Pod", "default", "pod-7", nil),
	}

	result := c.evaluate(events)

	// NodeCascade should fire (1 node + 2 pods).
	nodeFound := false
	stormFound := false
	for _, se := range result.SuperEvents {
		if se.CorrelationRule == "NodeCascade" {
			nodeFound = true
			if len(se.FaultEvents) != 3 {
				t.Errorf("NodeCascade should have 3 events, got %d", len(se.FaultEvents))
			}
		}
		if se.CorrelationRule == "NamespaceStorm" {
			stormFound = true
			if len(se.FaultEvents) != 5 {
				t.Errorf("NamespaceStorm should have 5 events, got %d", len(se.FaultEvents))
			}
		}
	}

	if !nodeFound {
		t.Error("expected NodeCascade super event")
	}
	if !stormFound {
		t.Error("expected NamespaceStorm super event")
	}
}

func TestNamespaceStorm_Disabled(t *testing.T) {
	collector := &resultCollector{}
	m := testMetrics(t)
	cfg := DefaultConfig()
	cfg.Rules.NamespaceStorm.Enabled = false
	cfg.Rules.NodeCascade.Enabled = false
	cfg.Rules.DeploymentRollout.Enabled = false
	cfg.Rules.StorageCascade.Enabled = false

	c, err := New(cfg, collector.handler, m, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events := make([]model.FaultEvent, 0, 25)
	for i := 0; i < 25; i++ {
		events = append(events, makeFaultEvent(
			fmt.Sprintf("e%d", i),
			"PodFailed",
			model.SeverityWarning,
			"Pod", "default", fmt.Sprintf("pod-%d", i),
			nil,
		))
	}

	result := c.evaluate(events)
	if len(result.SuperEvents) != 0 {
		t.Errorf("expected 0 super events when disabled, got %d", len(result.SuperEvents))
	}
	if len(result.UncorrelatedEvents) != 25 {
		t.Errorf("expected 25 uncorrelated events, got %d", len(result.UncorrelatedEvents))
	}
}

// --- Cross-rule interaction tests ---

func TestRulePriority_EventsConsumedByEarlierRules(t *testing.T) {
	collector := &resultCollector{}
	m := testMetrics(t)
	cfg := DefaultConfig()
	cfg.Rules.NodeCascade.MinPodFailures = 2
	cfg.Rules.DeploymentRollout.MinPodFailures = 2

	c, err := New(cfg, collector.handler, m, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// These pods are on node-1 AND belong to web-api deployment.
	// NodeCascade runs first and should consume them.
	events := []model.FaultEvent{
		makeFaultEvent("n1", "NodeNotReady", model.SeverityCritical, "Node", "", "node-1", nil),
		makeFaultEvent("p1", "PodFailed", model.SeverityWarning, "Pod", "default", "web-api-1",
			map[string]string{
				nodeAnnotationKey:       "node-1",
				deploymentAnnotationKey: "web-api",
			}),
		makeFaultEvent("p2", "PodFailed", model.SeverityWarning, "Pod", "default", "web-api-2",
			map[string]string{
				nodeAnnotationKey:       "node-1",
				deploymentAnnotationKey: "web-api",
			}),
	}

	result := c.evaluate(events)

	// NodeCascade should consume all events.
	if len(result.SuperEvents) != 1 {
		t.Fatalf("expected 1 super event, got %d", len(result.SuperEvents))
	}
	if result.SuperEvents[0].CorrelationRule != "NodeCascade" {
		t.Errorf("CorrelationRule = %q, want %q", result.SuperEvents[0].CorrelationRule, "NodeCascade")
	}
	if len(result.UncorrelatedEvents) != 0 {
		t.Errorf("expected 0 uncorrelated events, got %d", len(result.UncorrelatedEvents))
	}
}

func TestMixedRules_NodeCascadeAndDeploymentRollout(t *testing.T) {
	collector := &resultCollector{}
	m := testMetrics(t)
	cfg := DefaultConfig()
	cfg.Rules.NodeCascade.MinPodFailures = 2
	cfg.Rules.DeploymentRollout.MinPodFailures = 2

	c, err := New(cfg, collector.handler, m, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events := []model.FaultEvent{
		// NodeCascade group.
		makeFaultEvent("n1", "NodeNotReady", model.SeverityCritical, "Node", "", "node-1", nil),
		makeFaultEvent("p1", "PodFailed", model.SeverityWarning, "Pod", "default", "pod-1",
			map[string]string{nodeAnnotationKey: "node-1"}),
		makeFaultEvent("p2", "PodFailed", model.SeverityWarning, "Pod", "default", "pod-2",
			map[string]string{nodeAnnotationKey: "node-1"}),
		// DeploymentRollout group (different pods, no node annotation).
		makeFaultEvent("p3", "PodCrashLoop", model.SeverityWarning, "Pod", "payments", "api-1",
			map[string]string{deploymentAnnotationKey: "api"}),
		makeFaultEvent("p4", "PodCrashLoop", model.SeverityCritical, "Pod", "payments", "api-2",
			map[string]string{deploymentAnnotationKey: "api"}),
	}

	result := c.evaluate(events)
	if len(result.SuperEvents) != 2 {
		t.Fatalf("expected 2 super events, got %d", len(result.SuperEvents))
	}

	rules := make(map[string]bool)
	for _, se := range result.SuperEvents {
		rules[se.CorrelationRule] = true
	}
	if !rules["NodeCascade"] {
		t.Error("expected NodeCascade super event")
	}
	if !rules["DeploymentRollout"] {
		t.Error("expected DeploymentRollout super event")
	}
	if len(result.UncorrelatedEvents) != 0 {
		t.Errorf("expected 0 uncorrelated events, got %d", len(result.UncorrelatedEvents))
	}
}

// --- Edge cases ---

func TestEvaluate_PodWithoutAnnotations(t *testing.T) {
	collector := &resultCollector{}
	m := testMetrics(t)
	cfg := DefaultConfig()
	cfg.Rules.NamespaceStorm.Threshold = 100

	c, err := New(cfg, collector.handler, m, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Pod events without any annotation keys should not crash.
	events := []model.FaultEvent{
		makeFaultEvent("n1", "NodeNotReady", model.SeverityCritical, "Node", "", "node-1", nil),
		makeFaultEvent("p1", "PodFailed", model.SeverityWarning, "Pod", "default", "pod-1", nil),
	}

	result := c.evaluate(events)
	// Should not correlate since pod has no node annotation.
	if len(result.SuperEvents) != 0 {
		t.Errorf("expected 0 super events, got %d", len(result.SuperEvents))
	}
	if len(result.UncorrelatedEvents) != 2 {
		t.Errorf("expected 2 uncorrelated events, got %d", len(result.UncorrelatedEvents))
	}
}

func TestEvaluate_NonPodResources(t *testing.T) {
	collector := &resultCollector{}
	m := testMetrics(t)
	cfg := DefaultConfig()
	cfg.Rules.NamespaceStorm.Threshold = 100

	c, err := New(cfg, collector.handler, m, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	// Non-pod resources should not be matched by pod-related rules.
	events := []model.FaultEvent{
		makeFaultEvent("e1", "CustomDetector", model.SeverityWarning, "Service", "default", "svc-1", nil),
		makeFaultEvent("e2", "CustomDetector", model.SeverityInfo, "ConfigMap", "default", "cm-1", nil),
	}

	result := c.evaluate(events)
	if len(result.SuperEvents) != 0 {
		t.Errorf("expected 0 super events for non-pod resources, got %d", len(result.SuperEvents))
	}
	if len(result.UncorrelatedEvents) != 2 {
		t.Errorf("expected 2 uncorrelated events, got %d", len(result.UncorrelatedEvents))
	}
}

func TestEvaluate_AllRulesDisabled(t *testing.T) {
	collector := &resultCollector{}
	m := testMetrics(t)
	cfg := DefaultConfig()
	cfg.Rules.NodeCascade.Enabled = false
	cfg.Rules.DeploymentRollout.Enabled = false
	cfg.Rules.StorageCascade.Enabled = false
	cfg.Rules.NamespaceStorm.Enabled = false

	c, err := New(cfg, collector.handler, m, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events := []model.FaultEvent{
		makeFaultEvent("n1", "NodeNotReady", model.SeverityCritical, "Node", "", "node-1", nil),
		makeFaultEvent("p1", "PodFailed", model.SeverityWarning, "Pod", "default", "pod-1",
			map[string]string{nodeAnnotationKey: "node-1"}),
		makeFaultEvent("p2", "PodFailed", model.SeverityWarning, "Pod", "default", "pod-2",
			map[string]string{nodeAnnotationKey: "node-1"}),
	}

	result := c.evaluate(events)
	if len(result.SuperEvents) != 0 {
		t.Errorf("expected 0 super events when all rules disabled, got %d", len(result.SuperEvents))
	}
	if len(result.UncorrelatedEvents) != 3 {
		t.Errorf("expected 3 uncorrelated events, got %d", len(result.UncorrelatedEvents))
	}
}

func TestEvaluate_SingleEvent(t *testing.T) {
	collector := &resultCollector{}
	m := testMetrics(t)
	cfg := DefaultConfig()
	cfg.Rules.NamespaceStorm.Threshold = 100

	c, err := New(cfg, collector.handler, m, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events := []model.FaultEvent{
		makeFaultEvent("e1", "PodCrashLoop", model.SeverityWarning, "Pod", "default", "pod-1", nil),
	}

	result := c.evaluate(events)
	if len(result.SuperEvents) != 0 {
		t.Errorf("expected 0 super events for single event, got %d", len(result.SuperEvents))
	}
	if len(result.UncorrelatedEvents) != 1 {
		t.Errorf("expected 1 uncorrelated event, got %d", len(result.UncorrelatedEvents))
	}
}

func TestDefaultConfig_Values(t *testing.T) {
	cfg := DefaultConfig()

	if !cfg.Enabled {
		t.Error("Enabled should be true by default")
	}
	if cfg.WindowDuration != 2*time.Minute {
		t.Errorf("WindowDuration = %v, want 2m", cfg.WindowDuration)
	}
	if !cfg.Rules.NodeCascade.Enabled {
		t.Error("NodeCascade should be enabled by default")
	}
	if cfg.Rules.NodeCascade.MinPodFailures != 2 {
		t.Errorf("NodeCascade.MinPodFailures = %d, want 2", cfg.Rules.NodeCascade.MinPodFailures)
	}
	if !cfg.Rules.DeploymentRollout.Enabled {
		t.Error("DeploymentRollout should be enabled by default")
	}
	if cfg.Rules.DeploymentRollout.MinPodFailures != 3 {
		t.Errorf("DeploymentRollout.MinPodFailures = %d, want 3", cfg.Rules.DeploymentRollout.MinPodFailures)
	}
	if !cfg.Rules.StorageCascade.Enabled {
		t.Error("StorageCascade should be enabled by default")
	}
	if !cfg.Rules.NamespaceStorm.Enabled {
		t.Error("NamespaceStorm should be enabled by default")
	}
	if cfg.Rules.NamespaceStorm.Threshold != 20 {
		t.Errorf("NamespaceStorm.Threshold = %d, want 20", cfg.Rules.NamespaceStorm.Threshold)
	}
}

func TestValidateConfig_ValidConfigs(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{
			name: "default config",
			cfg:  DefaultConfig(),
		},
		{
			name: "all rules disabled",
			cfg: Config{
				Enabled:        true,
				WindowDuration: time.Minute,
				Rules: RulesConfig{
					NodeCascade:       NodeCascadeConfig{Enabled: false},
					DeploymentRollout: DeploymentRolloutConfig{Enabled: false},
					StorageCascade:    StorageCascadeConfig{Enabled: false},
					NamespaceStorm:    NamespaceStormConfig{Enabled: false},
				},
			},
		},
		{
			name: "custom thresholds",
			cfg: Config{
				Enabled:        true,
				WindowDuration: 5 * time.Minute,
				Rules: RulesConfig{
					NodeCascade:       NodeCascadeConfig{Enabled: true, MinPodFailures: 5},
					DeploymentRollout: DeploymentRolloutConfig{Enabled: true, MinPodFailures: 10},
					StorageCascade:    StorageCascadeConfig{Enabled: true},
					NamespaceStorm:    NamespaceStormConfig{Enabled: true, Threshold: 50},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateConfig(tt.cfg); err != nil {
				t.Errorf("ValidateConfig() error = %v", err)
			}
		})
	}
}

// --- Concurrent access test ---

func TestSubmit_ConcurrentSafety(t *testing.T) {
	collector := &resultCollector{}
	m := testMetrics(t)
	cfg := DefaultConfig()
	cfg.WindowDuration = 10 * time.Minute

	c, err := New(cfg, collector.handler, m, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go c.Run(ctx)

	var wg sync.WaitGroup
	eventCount := 100
	wg.Add(eventCount)

	for i := 0; i < eventCount; i++ {
		go func(i int) {
			defer wg.Done()
			event := makeFaultEvent(
				fmt.Sprintf("e%d", i),
				"PodFailed",
				model.SeverityWarning,
				"Pod", "default", fmt.Sprintf("pod-%d", i),
				nil,
			)
			c.Submit(event)
		}(i)
	}

	wg.Wait()

	c.mu.Lock()
	bufLen := len(c.buffer)
	c.mu.Unlock()

	if bufLen != eventCount {
		t.Errorf("buffer length = %d, want %d", bufLen, eventCount)
	}
}

// --- Window evaluation with timer ---

func TestRun_WindowEvaluation(t *testing.T) {
	collector := &resultCollector{}
	m := testMetrics(t)
	cfg := DefaultConfig()
	cfg.WindowDuration = 100 * time.Millisecond // short for testing
	cfg.Rules.NamespaceStorm.Threshold = 100    // high to avoid triggering

	c, err := New(cfg, collector.handler, m, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go c.Run(ctx)

	c.Submit(makeFaultEvent("e1", "PodFailed", model.SeverityWarning, "Pod", "default", "pod-1", nil))
	c.Submit(makeFaultEvent("e2", "PodFailed", model.SeverityWarning, "Pod", "default", "pod-2", nil))

	// Wait for window to evaluate.
	time.Sleep(300 * time.Millisecond)

	uncorrelated := collector.allUncorrelated()
	if len(uncorrelated) != 2 {
		t.Errorf("expected 2 uncorrelated events after window, got %d", len(uncorrelated))
	}
}

// --- StorageCascade + NodeCascade combined ---

func TestStorageCascadeAndNodeCascade_Combined(t *testing.T) {
	collector := &resultCollector{}
	m := testMetrics(t)
	cfg := DefaultConfig()
	cfg.Rules.NodeCascade.MinPodFailures = 2

	c, err := New(cfg, collector.handler, m, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	events := []model.FaultEvent{
		// NodeCascade group.
		makeFaultEvent("n1", "NodeNotReady", model.SeverityCritical, "Node", "", "node-1", nil),
		makeFaultEvent("p1", "PodFailed", model.SeverityWarning, "Pod", "default", "pod-1",
			map[string]string{nodeAnnotationKey: "node-1"}),
		makeFaultEvent("p2", "PodFailed", model.SeverityWarning, "Pod", "default", "pod-2",
			map[string]string{nodeAnnotationKey: "node-1"}),
		// StorageCascade group.
		makeFaultEvent("pvc1", "PVCStuckBinding", model.SeverityWarning, "PersistentVolumeClaim", "data-ns", "my-pvc", nil),
		makeFaultEvent("p3", "PodStuckPending", model.SeverityWarning, "Pod", "data-ns", "db-pod",
			map[string]string{pvcAnnotationKey: "my-pvc"}),
	}

	result := c.evaluate(events)
	if len(result.SuperEvents) != 2 {
		t.Fatalf("expected 2 super events, got %d", len(result.SuperEvents))
	}

	rules := make(map[string]bool)
	for _, se := range result.SuperEvents {
		rules[se.CorrelationRule] = true
	}
	if !rules["NodeCascade"] {
		t.Error("expected NodeCascade")
	}
	if !rules["StorageCascade"] {
		t.Error("expected StorageCascade")
	}
	if len(result.UncorrelatedEvents) != 0 {
		t.Errorf("expected 0 uncorrelated events, got %d", len(result.UncorrelatedEvents))
	}
}

// containsSubstring checks if s contains substr.
func containsSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

