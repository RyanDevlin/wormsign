// Package e2e contains integration tests that exercise the Wormsign pipeline
// stages working together: detection → filtering → correlation → gathering →
// analysis → sink delivery.
//
// These tests wire real internal components (pipeline, filter engine,
// correlator, noop analyzer) with test doubles for sinks. They do not require
// envtest because they operate above the Kubernetes API layer.
package e2e

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/k8s-wormsign/k8s-wormsign/internal/analyzer"
	"github.com/k8s-wormsign/k8s-wormsign/internal/filter"
	"github.com/k8s-wormsign/k8s-wormsign/internal/gatherer"
	"github.com/k8s-wormsign/k8s-wormsign/internal/metrics"
	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
	"github.com/k8s-wormsign/k8s-wormsign/internal/pipeline"
	"github.com/k8s-wormsign/k8s-wormsign/internal/pipeline/correlator"
)

// --------------------------------------------------------------------------
// Test helpers
// --------------------------------------------------------------------------

// collectingSink is a Sink implementation that records all delivered reports.
type collectingSink struct {
	mu      sync.Mutex
	reports []*model.RCAReport
}

func (s *collectingSink) Name() string { return "test-collector" }

func (s *collectingSink) Deliver(_ context.Context, report *model.RCAReport) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reports = append(s.reports, report)
	return nil
}

func (s *collectingSink) Reports() []*model.RCAReport {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*model.RCAReport, len(s.reports))
	copy(out, s.reports)
	return out
}

// newTestPipeline constructs a Pipeline wired with a noop analyzer, the
// given filter engine, and a collecting sink. Correlation is disabled so
// events flow through immediately unless overridden by corrCfg.
func newTestPipeline(t *testing.T, filterEngine *filter.Engine, corrCfg *correlator.Config) (*pipeline.Pipeline, *collectingSink) {
	t.Helper()
	ana, err := analyzer.NewNoopAnalyzer(slog.Default())
	if err != nil {
		t.Fatalf("creating noop analyzer: %v", err)
	}
	return newTestPipelineWithAnalyzer(t, ana, filterEngine, corrCfg)
}

// newTestPipelineWithRulesAnalyzer is like newTestPipeline but uses the
// rules-based analyzer instead of noop.
func newTestPipelineWithRulesAnalyzer(t *testing.T, filterEngine *filter.Engine, corrCfg *correlator.Config) (*pipeline.Pipeline, *collectingSink) {
	t.Helper()
	ana, err := analyzer.NewRulesAnalyzer(slog.Default())
	if err != nil {
		t.Fatalf("creating rules analyzer: %v", err)
	}
	return newTestPipelineWithAnalyzer(t, ana, filterEngine, corrCfg)
}

// newTestPipelineWithAnalyzer constructs a Pipeline wired with the given
// analyzer, filter engine, and a collecting sink.
func newTestPipelineWithAnalyzer(t *testing.T, ana analyzer.Analyzer, filterEngine *filter.Engine, corrCfg *correlator.Config) (*pipeline.Pipeline, *collectingSink) {
	t.Helper()

	logger := slog.Default()
	reg := prometheus.NewRegistry()
	m := metrics.NewMetrics(reg)

	sink := &collectingSink{}

	cfg := pipeline.DefaultConfig()
	// Use minimal workers for deterministic tests.
	cfg.Workers.Gathering = 1
	cfg.Workers.Analysis = 1
	cfg.Workers.Sink = 1

	if corrCfg != nil {
		cfg.Correlation = *corrCfg
	} else {
		// Disable correlation so events pass through immediately.
		cfg.Correlation.Enabled = false
	}

	opts := []pipeline.Option{
		pipeline.WithMetrics(m),
		pipeline.WithAnalyzer(ana),
		pipeline.WithLogger(logger),
		pipeline.WithGatherers([]gatherer.Gatherer{}),
		pipeline.WithSinks([]pipeline.Sink{sink}),
	}
	if filterEngine != nil {
		opts = append(opts, pipeline.WithFilter(filterEngine))
	}

	p, err := pipeline.New(cfg, opts...)
	if err != nil {
		t.Fatalf("creating pipeline: %v", err)
	}
	return p, sink
}

// waitForReports polls the sink until it has at least n reports or the
// timeout expires.
func waitForReports(sink *collectingSink, n int, timeout time.Duration) []*model.RCAReport {
	deadline := time.After(timeout)
	for {
		reports := sink.Reports()
		if len(reports) >= n {
			return reports
		}
		select {
		case <-deadline:
			return sink.Reports()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// makeFaultEvent is a convenience wrapper around model.NewFaultEvent that
// calls t.Fatal on error.
func makeFaultEvent(t *testing.T, detector string, severity model.Severity, ref model.ResourceRef, desc string, labels, annotations map[string]string) *model.FaultEvent {
	t.Helper()
	ev, err := model.NewFaultEvent(detector, severity, ref, desc, labels, annotations)
	if err != nil {
		t.Fatalf("creating fault event: %v", err)
	}
	return ev
}

// filterInputFor builds a FilterInput for the given FaultEvent.
func filterInputFor(ev *model.FaultEvent, nsMeta filter.NamespaceMeta, owners []filter.OwnerMeta) filter.FilterInput {
	return filter.FilterInput{
		DetectorName: ev.DetectorName,
		Resource: filter.ResourceMeta{
			Kind:        ev.Resource.Kind,
			Namespace:   ev.Resource.Namespace,
			Name:        ev.Resource.Name,
			UID:         ev.Resource.UID,
			Labels:      ev.Labels,
			Annotations: ev.Annotations,
		},
		Namespace: nsMeta,
		Owners:    owners,
	}
}

// --------------------------------------------------------------------------
// Integration test 1: PodCrashLoop through the full pipeline
// --------------------------------------------------------------------------

func TestIntegration_PodCrashLoop_Pipeline(t *testing.T) {
	p, sink := newTestPipeline(t, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := p.Start(ctx); err != nil {
		t.Fatalf("starting pipeline: %v", err)
	}

	// Simulate a PodCrashLoop fault event.
	ev := makeFaultEvent(t,
		"PodCrashLoop",
		model.SeverityWarning,
		model.ResourceRef{Kind: "Pod", Namespace: "default", Name: "crashloop-pod", UID: "uid-cl-1"},
		"Container app has restarted 5 times",
		map[string]string{"app": "crashloop-test"},
		nil,
	)

	// Submit with no filter.
	p.SubmitFaultEvent(ev, filterInputFor(ev, filter.NamespaceMeta{Name: "default"}, nil))

	// Wait for the report to be delivered to the sink.
	reports := waitForReports(sink, 1, 10*time.Second)
	if len(reports) == 0 {
		t.Fatal("expected at least 1 report, got 0")
	}

	report := reports[0]
	if report.FaultEventID != ev.ID {
		t.Errorf("report.FaultEventID = %q, want %q", report.FaultEventID, ev.ID)
	}
	if report.Severity != model.SeverityWarning {
		t.Errorf("report.Severity = %q, want %q", report.Severity, model.SeverityWarning)
	}
	if report.AnalyzerBackend != "noop" {
		t.Errorf("report.AnalyzerBackend = %q, want %q", report.AnalyzerBackend, "noop")
	}
	// Verify the diagnostic bundle carries the original event.
	if report.DiagnosticBundle.FaultEvent == nil {
		t.Fatal("report.DiagnosticBundle.FaultEvent is nil")
	}
	if report.DiagnosticBundle.FaultEvent.DetectorName != "PodCrashLoop" {
		t.Errorf("bundle detector = %q, want PodCrashLoop", report.DiagnosticBundle.FaultEvent.DetectorName)
	}

	p.Stop()
}

// --------------------------------------------------------------------------
// Integration test 2: Filter exclusion prevents pipeline processing
// --------------------------------------------------------------------------

func TestIntegration_FilterExclusion(t *testing.T) {
	logger := slog.Default()

	// Create a filter engine with a global namespace exclusion.
	eng, err := filter.NewEngine(filter.GlobalFilterConfig{
		ExcludeNamespaces: []string{"kube-system"},
	}, logger)
	if err != nil {
		t.Fatalf("creating filter engine: %v", err)
	}

	p, sink := newTestPipeline(t, eng, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := p.Start(ctx); err != nil {
		t.Fatalf("starting pipeline: %v", err)
	}

	// Submit an event in the excluded namespace — should be filtered.
	excludedEv := makeFaultEvent(t,
		"PodCrashLoop",
		model.SeverityWarning,
		model.ResourceRef{Kind: "Pod", Namespace: "kube-system", Name: "coredns-abc", UID: "uid-exc-1"},
		"Container restarted",
		nil, nil,
	)
	p.SubmitFaultEvent(excludedEv, filterInputFor(excludedEv, filter.NamespaceMeta{Name: "kube-system"}, nil))

	// Submit an event in an allowed namespace — should pass through.
	allowedEv := makeFaultEvent(t,
		"PodCrashLoop",
		model.SeverityWarning,
		model.ResourceRef{Kind: "Pod", Namespace: "production", Name: "web-abc", UID: "uid-allow-1"},
		"Container restarted",
		nil, nil,
	)
	p.SubmitFaultEvent(allowedEv, filterInputFor(allowedEv, filter.NamespaceMeta{Name: "production"}, nil))

	// Wait for the allowed event's report.
	reports := waitForReports(sink, 1, 10*time.Second)
	if len(reports) == 0 {
		t.Fatal("expected at least 1 report for the allowed event, got 0")
	}

	// Verify only the allowed event arrived.
	if reports[0].FaultEventID != allowedEv.ID {
		t.Errorf("report.FaultEventID = %q, want %q (the allowed event)", reports[0].FaultEventID, allowedEv.ID)
	}

	// Give extra time to confirm the excluded event does NOT produce a report.
	time.Sleep(500 * time.Millisecond)
	allReports := sink.Reports()
	if len(allReports) != 1 {
		t.Errorf("expected exactly 1 report (excluded event should be filtered), got %d", len(allReports))
	}

	p.Stop()
}

// TestIntegration_FilterExclusion_ResourceAnnotation verifies that a resource
// annotated with wormsign.io/exclude: "true" is filtered out.
func TestIntegration_FilterExclusion_ResourceAnnotation(t *testing.T) {
	logger := slog.Default()

	eng, err := filter.NewEngine(filter.GlobalFilterConfig{}, logger)
	if err != nil {
		t.Fatalf("creating filter engine: %v", err)
	}

	p, sink := newTestPipeline(t, eng, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := p.Start(ctx); err != nil {
		t.Fatalf("starting pipeline: %v", err)
	}

	// Event with exclude annotation — should be filtered.
	excludedEv := makeFaultEvent(t,
		"PodStuckPending",
		model.SeverityWarning,
		model.ResourceRef{Kind: "Pod", Namespace: "default", Name: "excluded-pod", UID: "uid-ra-1"},
		"Pod stuck in Pending",
		nil,
		map[string]string{"wormsign.io/exclude": "true"},
	)

	excludedInput := filterInputFor(excludedEv, filter.NamespaceMeta{Name: "default"}, nil)
	p.SubmitFaultEvent(excludedEv, excludedInput)

	// Event without exclude annotation — should pass through.
	normalEv := makeFaultEvent(t,
		"PodStuckPending",
		model.SeverityWarning,
		model.ResourceRef{Kind: "Pod", Namespace: "default", Name: "normal-pod", UID: "uid-ra-2"},
		"Pod stuck in Pending",
		nil, nil,
	)
	p.SubmitFaultEvent(normalEv, filterInputFor(normalEv, filter.NamespaceMeta{Name: "default"}, nil))

	reports := waitForReports(sink, 1, 10*time.Second)
	if len(reports) == 0 {
		t.Fatal("expected at least 1 report, got 0")
	}
	if reports[0].FaultEventID != normalEv.ID {
		t.Errorf("report.FaultEventID = %q, want %q", reports[0].FaultEventID, normalEv.ID)
	}

	time.Sleep(500 * time.Millisecond)
	allReports := sink.Reports()
	if len(allReports) != 1 {
		t.Errorf("expected exactly 1 report, got %d", len(allReports))
	}

	p.Stop()
}

// TestIntegration_FilterExclusion_Policy verifies that a WormsignPolicy
// with action Exclude filters matching events.
func TestIntegration_FilterExclusion_Policy(t *testing.T) {
	logger := slog.Default()

	eng, err := filter.NewEngine(filter.GlobalFilterConfig{}, logger)
	if err != nil {
		t.Fatalf("creating filter engine: %v", err)
	}

	// Set an exclude policy targeting PodCrashLoop detector for resources
	// with the label app.kubernetes.io/part-of: integration-tests.
	eng.SetPolicies([]filter.Policy{
		{
			Name:      "exclude-test-workloads",
			Namespace: "test",
			Action:    filter.PolicyActionExclude,
			Detectors: []string{"PodCrashLoop", "PodFailed"},
			ResourceSelector: &filter.LabelSelector{
				MatchLabels: map[string]string{
					"app.kubernetes.io/part-of": "integration-tests",
				},
			},
		},
	})

	p, sink := newTestPipeline(t, eng, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := p.Start(ctx); err != nil {
		t.Fatalf("starting pipeline: %v", err)
	}

	// Event matching the policy — should be excluded.
	matchedEv := makeFaultEvent(t,
		"PodCrashLoop",
		model.SeverityWarning,
		model.ResourceRef{Kind: "Pod", Namespace: "test", Name: "test-pod", UID: "uid-pol-1"},
		"Container restarted",
		map[string]string{"app.kubernetes.io/part-of": "integration-tests"},
		nil,
	)
	matchedInput := filterInputFor(matchedEv, filter.NamespaceMeta{Name: "test"}, nil)
	p.SubmitFaultEvent(matchedEv, matchedInput)

	// Event NOT matching (different detector) — should pass through.
	unmatchedEv := makeFaultEvent(t,
		"PodStuckPending",
		model.SeverityWarning,
		model.ResourceRef{Kind: "Pod", Namespace: "test", Name: "test-pod-2", UID: "uid-pol-2"},
		"Pod stuck in Pending",
		map[string]string{"app.kubernetes.io/part-of": "integration-tests"},
		nil,
	)
	unmatchedInput := filterInputFor(unmatchedEv, filter.NamespaceMeta{Name: "test"}, nil)
	p.SubmitFaultEvent(unmatchedEv, unmatchedInput)

	reports := waitForReports(sink, 1, 10*time.Second)
	if len(reports) == 0 {
		t.Fatal("expected at least 1 report, got 0")
	}
	if reports[0].FaultEventID != unmatchedEv.ID {
		t.Errorf("report.FaultEventID = %q, want %q (the unmatched event)", reports[0].FaultEventID, unmatchedEv.ID)
	}

	time.Sleep(500 * time.Millisecond)
	allReports := sink.Reports()
	if len(allReports) != 1 {
		t.Errorf("expected exactly 1 report (matched event should be excluded by policy), got %d", len(allReports))
	}

	p.Stop()
}

// --------------------------------------------------------------------------
// Integration test 3: NodeCascade correlation
// --------------------------------------------------------------------------

func TestIntegration_NodeCascade_Correlation(t *testing.T) {
	// Enable correlation with NodeCascade rule, short window, and disable
	// all other rules to isolate the test.
	corrCfg := &correlator.Config{
		Enabled:        true,
		WindowDuration: 500 * time.Millisecond,
		Rules: correlator.RulesConfig{
			NodeCascade: correlator.NodeCascadeConfig{
				Enabled:        true,
				MinPodFailures: 2,
			},
			DeploymentRollout: correlator.DeploymentRolloutConfig{Enabled: false, MinPodFailures: 1},
			StorageCascade:    correlator.StorageCascadeConfig{Enabled: false},
			NamespaceStorm:    correlator.NamespaceStormConfig{Enabled: false, Threshold: 1},
		},
	}

	p, sink := newTestPipeline(t, nil, corrCfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := p.Start(ctx); err != nil {
		t.Fatalf("starting pipeline: %v", err)
	}

	defaultNS := filter.NamespaceMeta{Name: "default"}

	// Submit a NodeNotReady event for node "worker-1".
	nodeEv := makeFaultEvent(t,
		"NodeNotReady",
		model.SeverityCritical,
		model.ResourceRef{Kind: "Node", Namespace: "", Name: "worker-1", UID: "uid-node-1"},
		"Node worker-1 is NotReady",
		nil, nil,
	)
	p.SubmitFaultEvent(nodeEv, filterInputFor(nodeEv, defaultNS, nil))

	// Submit 2 pod failure events on the same node (annotated with node name).
	pod1 := makeFaultEvent(t,
		"PodCrashLoop",
		model.SeverityWarning,
		model.ResourceRef{Kind: "Pod", Namespace: "default", Name: "app-pod-1", UID: "uid-pod-1"},
		"Container restarted",
		nil,
		map[string]string{"wormsign.io/node-name": "worker-1"},
	)
	p.SubmitFaultEvent(pod1, filterInputFor(pod1, defaultNS, nil))

	pod2 := makeFaultEvent(t,
		"PodFailed",
		model.SeverityWarning,
		model.ResourceRef{Kind: "Pod", Namespace: "default", Name: "app-pod-2", UID: "uid-pod-2"},
		"Pod failed",
		nil,
		map[string]string{"wormsign.io/node-name": "worker-1"},
	)
	p.SubmitFaultEvent(pod2, filterInputFor(pod2, defaultNS, nil))

	// Wait for the correlation window to fire and the report to arrive.
	// The correlator should produce a single SuperEvent containing the
	// node event + 2 pod events.
	reports := waitForReports(sink, 1, 15*time.Second)
	if len(reports) == 0 {
		t.Fatal("expected at least 1 report from NodeCascade correlation, got 0")
	}

	report := reports[0]

	// The noop analyzer sets AnalyzerBackend to "noop".
	if report.AnalyzerBackend != "noop" {
		t.Errorf("report.AnalyzerBackend = %q, want %q", report.AnalyzerBackend, "noop")
	}

	// The diagnostic bundle should carry the SuperEvent (not a FaultEvent).
	if report.DiagnosticBundle.SuperEvent == nil {
		t.Fatal("expected SuperEvent in diagnostic bundle, got nil")
	}

	se := report.DiagnosticBundle.SuperEvent
	if se.CorrelationRule != "NodeCascade" {
		t.Errorf("SuperEvent.CorrelationRule = %q, want NodeCascade", se.CorrelationRule)
	}
	if se.PrimaryResource.Kind != "Node" {
		t.Errorf("SuperEvent.PrimaryResource.Kind = %q, want Node", se.PrimaryResource.Kind)
	}
	if se.PrimaryResource.Name != "worker-1" {
		t.Errorf("SuperEvent.PrimaryResource.Name = %q, want worker-1", se.PrimaryResource.Name)
	}
	// Should contain the node event + 2 pod events = 3 total.
	if len(se.FaultEvents) != 3 {
		t.Errorf("SuperEvent.FaultEvents count = %d, want 3", len(se.FaultEvents))
	}

	p.Stop()
}

// TestIntegration_NodeCascade_BelowThreshold verifies that when the number
// of pod failures is below the MinPodFailures threshold, no SuperEvent is
// created and events pass through individually.
func TestIntegration_NodeCascade_BelowThreshold(t *testing.T) {
	corrCfg := &correlator.Config{
		Enabled:        true,
		WindowDuration: 500 * time.Millisecond,
		Rules: correlator.RulesConfig{
			NodeCascade: correlator.NodeCascadeConfig{
				Enabled:        true,
				MinPodFailures: 3, // require 3, we'll only send 1
			},
			DeploymentRollout: correlator.DeploymentRolloutConfig{Enabled: false, MinPodFailures: 1},
			StorageCascade:    correlator.StorageCascadeConfig{Enabled: false},
			NamespaceStorm:    correlator.NamespaceStormConfig{Enabled: false, Threshold: 1},
		},
	}

	p, sink := newTestPipeline(t, nil, corrCfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := p.Start(ctx); err != nil {
		t.Fatalf("starting pipeline: %v", err)
	}

	defaultNS := filter.NamespaceMeta{Name: "default"}

	// NodeNotReady event.
	nodeEv := makeFaultEvent(t,
		"NodeNotReady",
		model.SeverityCritical,
		model.ResourceRef{Kind: "Node", Namespace: "", Name: "worker-2", UID: "uid-node-2"},
		"Node worker-2 is NotReady",
		nil, nil,
	)
	p.SubmitFaultEvent(nodeEv, filterInputFor(nodeEv, defaultNS, nil))

	// Only 1 pod failure (below threshold of 3).
	pod1 := makeFaultEvent(t,
		"PodCrashLoop",
		model.SeverityWarning,
		model.ResourceRef{Kind: "Pod", Namespace: "default", Name: "lonely-pod", UID: "uid-pod-lonely"},
		"Container restarted",
		nil,
		map[string]string{"wormsign.io/node-name": "worker-2"},
	)
	p.SubmitFaultEvent(pod1, filterInputFor(pod1, defaultNS, nil))

	// Events should pass through as uncorrelated individual events.
	reports := waitForReports(sink, 2, 15*time.Second)
	if len(reports) < 2 {
		t.Fatalf("expected 2 individual reports (below threshold), got %d", len(reports))
	}

	// Both should be FaultEvent reports, not SuperEvent.
	for i, r := range reports {
		if r.DiagnosticBundle.SuperEvent != nil {
			t.Errorf("report[%d] has unexpected SuperEvent (should be uncorrelated)", i)
		}
		if r.DiagnosticBundle.FaultEvent == nil {
			t.Errorf("report[%d] missing FaultEvent", i)
		}
	}

	p.Stop()
}

// --------------------------------------------------------------------------
// Integration test: Multiple events through full pipeline
// --------------------------------------------------------------------------

func TestIntegration_MultipleEvents_Pipeline(t *testing.T) {
	p, sink := newTestPipeline(t, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := p.Start(ctx); err != nil {
		t.Fatalf("starting pipeline: %v", err)
	}

	defaultNS := filter.NamespaceMeta{Name: "default"}

	// Submit 5 different fault events.
	detectors := []string{"PodCrashLoop", "PodStuckPending", "PodFailed", "NodeNotReady", "PVCStuckBinding"}
	kinds := []string{"Pod", "Pod", "Pod", "Node", "PersistentVolumeClaim"}

	for i, det := range detectors {
		ev := makeFaultEvent(t,
			det,
			model.SeverityWarning,
			model.ResourceRef{Kind: kinds[i], Namespace: "default", Name: "res-" + det, UID: "uid-multi-" + det},
			"Test event for "+det,
			nil, nil,
		)
		p.SubmitFaultEvent(ev, filterInputFor(ev, defaultNS, nil))
	}

	// All 5 should produce reports (correlation disabled).
	reports := waitForReports(sink, 5, 15*time.Second)
	if len(reports) != 5 {
		t.Fatalf("expected 5 reports, got %d", len(reports))
	}

	// Verify all reports use the noop analyzer.
	for i, r := range reports {
		if r.AnalyzerBackend != "noop" {
			t.Errorf("report[%d].AnalyzerBackend = %q, want noop", i, r.AnalyzerBackend)
		}
	}

	p.Stop()
}

// --------------------------------------------------------------------------
// Integration tests: Rules analyzer through the pipeline
// --------------------------------------------------------------------------

// TestIntegration_RulesAnalyzer_PodCrashLoop verifies that the rules analyzer
// produces a meaningful report (non-zero confidence, proper backend name,
// detector-derived root cause) when wired into the full pipeline.
func TestIntegration_RulesAnalyzer_PodCrashLoop(t *testing.T) {
	p, sink := newTestPipelineWithRulesAnalyzer(t, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := p.Start(ctx); err != nil {
		t.Fatalf("starting pipeline: %v", err)
	}

	ev := makeFaultEvent(t,
		"PodCrashLoop",
		model.SeverityCritical,
		model.ResourceRef{Kind: "Pod", Namespace: "prod", Name: "api-server-xyz", UID: "uid-rules-1"},
		"Container app has restarted 8 times",
		map[string]string{"app": "api-server"},
		nil,
	)

	p.SubmitFaultEvent(ev, filterInputFor(ev, filter.NamespaceMeta{Name: "prod"}, nil))

	reports := waitForReports(sink, 1, 10*time.Second)
	if len(reports) == 0 {
		t.Fatal("expected at least 1 report, got 0")
	}

	report := reports[0]
	if report.FaultEventID != ev.ID {
		t.Errorf("FaultEventID = %q, want %q", report.FaultEventID, ev.ID)
	}
	if report.AnalyzerBackend != "rules" {
		t.Errorf("AnalyzerBackend = %q, want %q", report.AnalyzerBackend, "rules")
	}
	// Rules analyzer should produce non-zero confidence (noop gives 0.0).
	if report.Confidence <= 0.0 {
		t.Errorf("Confidence = %f, want > 0 (rules analyzer should set confidence)", report.Confidence)
	}
	// Root cause should mention the detector, not the generic noop placeholder.
	if report.RootCause == "" {
		t.Error("RootCause should not be empty")
	}
	if report.RootCause == "Automated analysis not performed — raw diagnostics attached" {
		t.Error("RootCause should not be the noop placeholder text")
	}
	// Category should be set to something meaningful.
	if !model.ValidCategories[report.Category] {
		t.Errorf("Category %q is not valid", report.Category)
	}
	// Remediation should have actionable steps.
	if len(report.Remediation) == 0 {
		t.Error("Remediation should have at least one step")
	}
	// RawAnalysis (evidence) should not be empty.
	if report.RawAnalysis == "" {
		t.Error("RawAnalysis should contain evidence summary")
	}
	// Tokens should be zero (no LLM used).
	if report.TokensUsed.Total() != 0 {
		t.Errorf("TokensUsed = %+v, want zero for rules analyzer", report.TokensUsed)
	}

	p.Stop()
}

// TestIntegration_RulesAnalyzer_MultipleDetectors verifies that the rules
// analyzer produces distinct reports for different detector types, each with
// the correct backend and non-zero confidence.
func TestIntegration_RulesAnalyzer_MultipleDetectors(t *testing.T) {
	p, sink := newTestPipelineWithRulesAnalyzer(t, nil, nil)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := p.Start(ctx); err != nil {
		t.Fatalf("starting pipeline: %v", err)
	}

	defaultNS := filter.NamespaceMeta{Name: "default"}

	events := []*model.FaultEvent{
		makeFaultEvent(t, "PodCrashLoop", model.SeverityCritical,
			model.ResourceRef{Kind: "Pod", Namespace: "default", Name: "crash-pod", UID: "uid-rm-1"},
			"Container restarted 5 times", nil, nil),
		makeFaultEvent(t, "PodFailed", model.SeverityWarning,
			model.ResourceRef{Kind: "Pod", Namespace: "default", Name: "failed-pod", UID: "uid-rm-2"},
			"Pod entered Failed state", nil, nil),
		makeFaultEvent(t, "JobDeadlineExceeded", model.SeverityWarning,
			model.ResourceRef{Kind: "Job", Namespace: "default", Name: "etl-job", UID: "uid-rm-3"},
			"Job exceeded deadline", nil, nil),
		makeFaultEvent(t, "PVCStuckBinding", model.SeverityWarning,
			model.ResourceRef{Kind: "PersistentVolumeClaim", Namespace: "default", Name: "data-pvc", UID: "uid-rm-4"},
			"PVC stuck in Pending", nil, nil),
	}

	for _, ev := range events {
		p.SubmitFaultEvent(ev, filterInputFor(ev, defaultNS, nil))
	}

	reports := waitForReports(sink, len(events), 15*time.Second)
	if len(reports) != len(events) {
		t.Fatalf("expected %d reports, got %d", len(events), len(reports))
	}

	for i, r := range reports {
		if r.AnalyzerBackend != "rules" {
			t.Errorf("report[%d].AnalyzerBackend = %q, want rules", i, r.AnalyzerBackend)
		}
		if r.Confidence <= 0.0 {
			t.Errorf("report[%d].Confidence = %f, want > 0", i, r.Confidence)
		}
		if !model.ValidCategories[r.Category] {
			t.Errorf("report[%d].Category = %q, not a valid category", i, r.Category)
		}
	}

	p.Stop()
}

// TestIntegration_RulesAnalyzer_NodeCascade verifies that the rules analyzer
// handles correlated SuperEvents from the correlator, producing a systemic
// report with related resources populated.
func TestIntegration_RulesAnalyzer_NodeCascade(t *testing.T) {
	corrCfg := &correlator.Config{
		Enabled:        true,
		WindowDuration: 500 * time.Millisecond,
		Rules: correlator.RulesConfig{
			NodeCascade: correlator.NodeCascadeConfig{
				Enabled:        true,
				MinPodFailures: 2,
			},
			DeploymentRollout: correlator.DeploymentRolloutConfig{Enabled: false, MinPodFailures: 1},
			StorageCascade:    correlator.StorageCascadeConfig{Enabled: false},
			NamespaceStorm:    correlator.NamespaceStormConfig{Enabled: false, Threshold: 1},
		},
	}

	p, sink := newTestPipelineWithRulesAnalyzer(t, nil, corrCfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if err := p.Start(ctx); err != nil {
		t.Fatalf("starting pipeline: %v", err)
	}

	defaultNS := filter.NamespaceMeta{Name: "default"}

	nodeEv := makeFaultEvent(t,
		"NodeNotReady", model.SeverityCritical,
		model.ResourceRef{Kind: "Node", Name: "worker-3", UID: "uid-rn-1"},
		"Node worker-3 is NotReady", nil, nil,
	)
	p.SubmitFaultEvent(nodeEv, filterInputFor(nodeEv, defaultNS, nil))

	pod1 := makeFaultEvent(t,
		"PodCrashLoop", model.SeverityWarning,
		model.ResourceRef{Kind: "Pod", Namespace: "default", Name: "app-1", UID: "uid-rn-2"},
		"Container restarted", nil,
		map[string]string{"wormsign.io/node-name": "worker-3"},
	)
	p.SubmitFaultEvent(pod1, filterInputFor(pod1, defaultNS, nil))

	pod2 := makeFaultEvent(t,
		"PodFailed", model.SeverityWarning,
		model.ResourceRef{Kind: "Pod", Namespace: "default", Name: "app-2", UID: "uid-rn-3"},
		"Pod failed", nil,
		map[string]string{"wormsign.io/node-name": "worker-3"},
	)
	p.SubmitFaultEvent(pod2, filterInputFor(pod2, defaultNS, nil))

	reports := waitForReports(sink, 1, 15*time.Second)
	if len(reports) == 0 {
		t.Fatal("expected at least 1 report from NodeCascade correlation, got 0")
	}

	report := reports[0]
	if report.AnalyzerBackend != "rules" {
		t.Errorf("AnalyzerBackend = %q, want rules", report.AnalyzerBackend)
	}
	if report.DiagnosticBundle.SuperEvent == nil {
		t.Fatal("expected SuperEvent in diagnostic bundle")
	}
	// Rules analyzer should mark correlated events as systemic.
	if !report.Systemic {
		t.Error("SuperEvent report should be systemic")
	}
	if report.Confidence <= 0.0 {
		t.Errorf("Confidence = %f, want > 0", report.Confidence)
	}

	p.Stop()
}
