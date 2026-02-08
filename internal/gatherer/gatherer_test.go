package gatherer

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

// silentLogger returns a logger that discards output (for tests).
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(discardWriter{}, nil))
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// --- Mock gatherers for testing ---

// stubGatherer is a minimal Gatherer that returns a fixed result.
type stubGatherer struct {
	name    string
	section *model.DiagnosticSection
	err     error
	delay   time.Duration
}

func (s *stubGatherer) Name() string { return s.name }

func (s *stubGatherer) Gather(_ context.Context, ref model.ResourceRef) (*model.DiagnosticSection, error) {
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	if s.err != nil {
		return nil, s.err
	}
	return s.section, nil
}

// triggerGatherer implements both Gatherer and TriggerMatcher.
type triggerGatherer struct {
	name      string
	kinds     []string
	detectors []string
	section   *model.DiagnosticSection
	err       error
}

func (t *triggerGatherer) Name() string { return t.name }

func (t *triggerGatherer) Gather(_ context.Context, ref model.ResourceRef) (*model.DiagnosticSection, error) {
	if t.err != nil {
		return nil, t.err
	}
	return t.section, nil
}

func (t *triggerGatherer) ResourceKinds() []string  { return t.kinds }
func (t *triggerGatherer) DetectorNames() []string { return t.detectors }

// --- MatchesTrigger tests ---

func TestMatchesTrigger_NoTriggerMatcher(t *testing.T) {
	g := &stubGatherer{name: "simple"}
	if !MatchesTrigger(g, "Pod", "PodCrashLoop") {
		t.Error("gatherer without TriggerMatcher should always match")
	}
}

func TestMatchesTrigger_EmptyLists(t *testing.T) {
	g := &triggerGatherer{name: "empty-triggers", kinds: nil, detectors: nil}
	if !MatchesTrigger(g, "Pod", "PodCrashLoop") {
		t.Error("empty kinds and detectors should match all")
	}
}

func TestMatchesTrigger_KindMatch(t *testing.T) {
	g := &triggerGatherer{name: "pod-gatherer", kinds: []string{"Pod"}, detectors: nil}
	if !MatchesTrigger(g, "Pod", "AnyDetector") {
		t.Error("should match when resource kind is in the list")
	}
	if MatchesTrigger(g, "Node", "AnyDetector") {
		t.Error("should not match when resource kind is not in the list")
	}
}

func TestMatchesTrigger_DetectorMatch(t *testing.T) {
	g := &triggerGatherer{name: "crashloop-gatherer", kinds: nil, detectors: []string{"PodCrashLoop", "PodFailed"}}
	if !MatchesTrigger(g, "Pod", "PodCrashLoop") {
		t.Error("should match when detector name is in the list")
	}
	if !MatchesTrigger(g, "Pod", "PodFailed") {
		t.Error("should match when detector name is in the list")
	}
	if MatchesTrigger(g, "Pod", "NodeNotReady") {
		t.Error("should not match when detector name is not in the list")
	}
}

func TestMatchesTrigger_BothConditions(t *testing.T) {
	g := &triggerGatherer{
		name:      "specific-gatherer",
		kinds:     []string{"Pod"},
		detectors: []string{"PodCrashLoop"},
	}

	tests := []struct {
		name     string
		kind     string
		detector string
		want     bool
	}{
		{"both match", "Pod", "PodCrashLoop", true},
		{"kind mismatch", "Node", "PodCrashLoop", false},
		{"detector mismatch", "Pod", "NodeNotReady", false},
		{"both mismatch", "Node", "NodeNotReady", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := MatchesTrigger(g, tt.kind, tt.detector)
			if got != tt.want {
				t.Errorf("MatchesTrigger(kind=%q, detector=%q) = %v, want %v",
					tt.kind, tt.detector, got, tt.want)
			}
		})
	}
}

func TestMatchesTrigger_MultipleKinds(t *testing.T) {
	g := &triggerGatherer{
		name:  "multi-kind",
		kinds: []string{"Pod", "Node", "PersistentVolumeClaim"},
	}
	for _, kind := range []string{"Pod", "Node", "PersistentVolumeClaim"} {
		if !MatchesTrigger(g, kind, "Any") {
			t.Errorf("should match kind %q", kind)
		}
	}
	if MatchesTrigger(g, "Deployment", "Any") {
		t.Error("should not match Deployment")
	}
}

// --- FilterByTrigger tests ---

func TestFilterByTrigger(t *testing.T) {
	allGatherers := []Gatherer{
		&stubGatherer{name: "universal"},
		&triggerGatherer{name: "pod-only", kinds: []string{"Pod"}},
		&triggerGatherer{name: "node-only", kinds: []string{"Node"}},
		&triggerGatherer{name: "crashloop-only", detectors: []string{"PodCrashLoop"}},
	}

	tests := []struct {
		name         string
		kind         string
		detector     string
		wantNames    []string
	}{
		{
			name:      "pod crashloop event",
			kind:      "Pod",
			detector:  "PodCrashLoop",
			wantNames: []string{"universal", "pod-only", "crashloop-only"},
		},
		{
			name:      "node event",
			kind:      "Node",
			detector:  "NodeNotReady",
			wantNames: []string{"universal", "node-only"},
		},
		{
			name:      "deployment event",
			kind:      "Deployment",
			detector:  "SomeDetector",
			wantNames: []string{"universal"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FilterByTrigger(allGatherers, tt.kind, tt.detector)
			if len(got) != len(tt.wantNames) {
				gotNames := make([]string, len(got))
				for i, g := range got {
					gotNames[i] = g.Name()
				}
				t.Fatalf("FilterByTrigger returned %d gatherers %v, want %d %v",
					len(got), gotNames, len(tt.wantNames), tt.wantNames)
			}
			for i, g := range got {
				if g.Name() != tt.wantNames[i] {
					t.Errorf("gatherer[%d].Name() = %q, want %q", i, g.Name(), tt.wantNames[i])
				}
			}
		})
	}
}

func TestFilterByTrigger_EmptyInput(t *testing.T) {
	got := FilterByTrigger(nil, "Pod", "PodCrashLoop")
	if got != nil {
		t.Errorf("FilterByTrigger(nil) = %v, want nil", got)
	}
}

// --- RunAll tests ---

func TestRunAll_Success(t *testing.T) {
	gatherers := []Gatherer{
		&stubGatherer{
			name: "g1",
			section: &model.DiagnosticSection{
				GathererName: "g1",
				Title:        "Gatherer 1",
				Content:      "data from g1",
				Format:       "text",
			},
		},
		&stubGatherer{
			name: "g2",
			section: &model.DiagnosticSection{
				GathererName: "g2",
				Title:        "Gatherer 2",
				Content:      "data from g2",
				Format:       "json",
			},
		},
	}

	ref := model.ResourceRef{Kind: "Pod", Namespace: "default", Name: "test-pod"}
	sections := RunAll(context.Background(), silentLogger(), gatherers, ref)

	if len(sections) != 2 {
		t.Fatalf("RunAll returned %d sections, want 2", len(sections))
	}
	if sections[0].GathererName != "g1" {
		t.Errorf("sections[0].GathererName = %q, want %q", sections[0].GathererName, "g1")
	}
	if sections[0].Content != "data from g1" {
		t.Errorf("sections[0].Content = %q, want %q", sections[0].Content, "data from g1")
	}
	if sections[1].GathererName != "g2" {
		t.Errorf("sections[1].GathererName = %q, want %q", sections[1].GathererName, "g2")
	}
}

func TestRunAll_Empty(t *testing.T) {
	sections := RunAll(context.Background(), silentLogger(), nil, model.ResourceRef{})
	if sections != nil {
		t.Errorf("RunAll(nil) = %v, want nil", sections)
	}
}

func TestRunAll_ErrorDoesNotBlockOthers(t *testing.T) {
	gatherers := []Gatherer{
		&stubGatherer{
			name: "failing",
			err:  errors.New("connection refused"),
		},
		&stubGatherer{
			name: "succeeding",
			section: &model.DiagnosticSection{
				GathererName: "succeeding",
				Title:        "Success",
				Content:      "gathered data",
				Format:       "text",
			},
		},
	}

	ref := model.ResourceRef{Kind: "Pod", Namespace: "default", Name: "test-pod"}
	sections := RunAll(context.Background(), silentLogger(), gatherers, ref)

	if len(sections) != 2 {
		t.Fatalf("RunAll returned %d sections, want 2", len(sections))
	}

	// Failing gatherer should have error populated.
	if sections[0].Error == "" {
		t.Error("expected error in failing gatherer section")
	}
	if !strings.Contains(sections[0].Error, "connection refused") {
		t.Errorf("error = %q, want to contain %q", sections[0].Error, "connection refused")
	}
	if sections[0].GathererName != "failing" {
		t.Errorf("sections[0].GathererName = %q, want %q", sections[0].GathererName, "failing")
	}

	// Succeeding gatherer should have data.
	if sections[1].Error != "" {
		t.Errorf("unexpected error in succeeding gatherer: %q", sections[1].Error)
	}
	if sections[1].Content != "gathered data" {
		t.Errorf("sections[1].Content = %q, want %q", sections[1].Content, "gathered data")
	}
}

func TestRunAll_AllFail(t *testing.T) {
	gatherers := []Gatherer{
		&stubGatherer{name: "fail1", err: errors.New("error 1")},
		&stubGatherer{name: "fail2", err: errors.New("error 2")},
	}

	ref := model.ResourceRef{Kind: "Pod", Namespace: "default", Name: "test-pod"}
	sections := RunAll(context.Background(), silentLogger(), gatherers, ref)

	if len(sections) != 2 {
		t.Fatalf("RunAll returned %d sections, want 2", len(sections))
	}
	for i, s := range sections {
		if s.Error == "" {
			t.Errorf("sections[%d].Error is empty, want error", i)
		}
	}
}

func TestRunAll_ConcurrentExecution(t *testing.T) {
	// Verify that gatherers run concurrently by checking they complete
	// faster than sequential execution would allow.
	var running atomic.Int32
	var maxConcurrent atomic.Int32

	makeGatherer := func(name string) Gatherer {
		return &stubGatherer{
			name:  name,
			delay: 50 * time.Millisecond,
			section: &model.DiagnosticSection{
				GathererName: name,
				Title:        name,
				Content:      "ok",
				Format:       "text",
			},
		}
	}

	// Override stubGatherer to track concurrency.
	type concurrencyTracker struct {
		name    string
		running *atomic.Int32
		maxConc *atomic.Int32
	}

	trackerGatherers := make([]Gatherer, 5)
	for i := range trackerGatherers {
		name := string(rune('A' + i))
		trackerGatherers[i] = &concurrentGatherer{
			name:          name,
			running:       &running,
			maxConcurrent: &maxConcurrent,
		}
	}

	// Ignore makeGatherer, use trackerGatherers instead.
	_ = makeGatherer

	ref := model.ResourceRef{Kind: "Pod", Namespace: "default", Name: "test"}
	start := time.Now()
	sections := RunAll(context.Background(), silentLogger(), trackerGatherers, ref)
	elapsed := time.Since(start)

	if len(sections) != 5 {
		t.Fatalf("got %d sections, want 5", len(sections))
	}

	// Sequential would take 5*50ms = 250ms. Concurrent should be ~50ms.
	if elapsed > 200*time.Millisecond {
		t.Errorf("execution took %v, expected concurrent execution to be faster", elapsed)
	}

	if maxConcurrent.Load() < 2 {
		t.Errorf("max concurrent = %d, expected at least 2", maxConcurrent.Load())
	}
}

// concurrentGatherer tracks concurrent execution for testing.
type concurrentGatherer struct {
	name          string
	running       *atomic.Int32
	maxConcurrent *atomic.Int32
}

func (c *concurrentGatherer) Name() string { return c.name }

func (c *concurrentGatherer) Gather(_ context.Context, _ model.ResourceRef) (*model.DiagnosticSection, error) {
	cur := c.running.Add(1)
	// Update max seen concurrency.
	for {
		old := c.maxConcurrent.Load()
		if cur <= old || c.maxConcurrent.CompareAndSwap(old, cur) {
			break
		}
	}
	time.Sleep(50 * time.Millisecond)
	c.running.Add(-1)

	return &model.DiagnosticSection{
		GathererName: c.name,
		Title:        c.name,
		Content:      "ok",
		Format:       "text",
	}, nil
}

func TestRunAll_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	var called atomic.Bool
	gatherers := []Gatherer{
		&contextAwareGatherer{name: "ctx-aware", called: &called},
	}

	ref := model.ResourceRef{Kind: "Pod", Namespace: "default", Name: "test"}
	sections := RunAll(ctx, silentLogger(), gatherers, ref)

	if len(sections) != 1 {
		t.Fatalf("got %d sections, want 1", len(sections))
	}
	// The gatherer should have been called and may or may not fail depending
	// on how fast it checks the context.
	if !called.Load() {
		t.Error("gatherer should have been called")
	}
}

// contextAwareGatherer checks for context cancellation.
type contextAwareGatherer struct {
	name   string
	called *atomic.Bool
}

func (c *contextAwareGatherer) Name() string { return c.name }

func (c *contextAwareGatherer) Gather(ctx context.Context, _ model.ResourceRef) (*model.DiagnosticSection, error) {
	c.called.Store(true)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return &model.DiagnosticSection{
		GathererName: c.name,
		Title:        c.name,
		Content:      "ok",
		Format:       "text",
	}, nil
}

// --- GatherForFaultEvent tests ---

func TestGatherForFaultEvent_NilEvent(t *testing.T) {
	sections := GatherForFaultEvent(context.Background(), silentLogger(), nil, nil)
	if sections != nil {
		t.Errorf("GatherForFaultEvent(nil) = %v, want nil", sections)
	}
}

func TestGatherForFaultEvent_FiltersGatherers(t *testing.T) {
	gatherers := []Gatherer{
		&triggerGatherer{
			name:  "pod-gatherer",
			kinds: []string{"Pod"},
			section: &model.DiagnosticSection{
				GathererName: "pod-gatherer",
				Title:        "Pod data",
				Content:      "pod stuff",
				Format:       "text",
			},
		},
		&triggerGatherer{
			name:  "node-gatherer",
			kinds: []string{"Node"},
			section: &model.DiagnosticSection{
				GathererName: "node-gatherer",
				Title:        "Node data",
				Content:      "node stuff",
				Format:       "text",
			},
		},
	}

	event := &model.FaultEvent{
		ID:           "test-event-1",
		DetectorName: "PodCrashLoop",
		Resource:     model.ResourceRef{Kind: "Pod", Namespace: "default", Name: "my-pod"},
	}

	sections := GatherForFaultEvent(context.Background(), silentLogger(), gatherers, event)
	if len(sections) != 1 {
		t.Fatalf("got %d sections, want 1", len(sections))
	}
	if sections[0].GathererName != "pod-gatherer" {
		t.Errorf("sections[0].GathererName = %q, want %q", sections[0].GathererName, "pod-gatherer")
	}
}

// --- GatherForSuperEvent tests ---

func TestGatherForSuperEvent_NilEvent(t *testing.T) {
	sections := GatherForSuperEvent(context.Background(), silentLogger(), nil, nil, 5)
	if sections != nil {
		t.Errorf("GatherForSuperEvent(nil) = %v, want nil", sections)
	}
}

func TestGatherForSuperEvent_PrimaryAndAffected(t *testing.T) {
	var mu sync.Mutex
	var gatheredResources []string

	trackingGatherer := &trackingStubGatherer{
		name:     "tracker",
		mu:       &mu,
		gathered: &gatheredResources,
	}

	event := &model.SuperEvent{
		ID:              "super-1",
		CorrelationRule: "NodeCascade",
		PrimaryResource: model.ResourceRef{Kind: "Node", Namespace: "", Name: "node-1", UID: "uid-node-1"},
		FaultEvents: []model.FaultEvent{
			{
				ID:           "fe-1",
				DetectorName: "PodFailed",
				Resource:     model.ResourceRef{Kind: "Pod", Namespace: "default", Name: "pod-1", UID: "uid-pod-1"},
			},
			{
				ID:           "fe-2",
				DetectorName: "PodFailed",
				Resource:     model.ResourceRef{Kind: "Pod", Namespace: "default", Name: "pod-2", UID: "uid-pod-2"},
			},
			{
				ID:           "fe-3",
				DetectorName: "PodFailed",
				Resource:     model.ResourceRef{Kind: "Pod", Namespace: "default", Name: "pod-3", UID: "uid-pod-3"},
			},
		},
		Severity: model.SeverityCritical,
	}

	gatherers := []Gatherer{trackingGatherer}
	sections := GatherForSuperEvent(context.Background(), silentLogger(), gatherers, event, 2)

	// Should gather for primary (node-1) + 2 affected pods.
	mu.Lock()
	resources := make([]string, len(gatheredResources))
	copy(resources, gatheredResources)
	mu.Unlock()

	if len(resources) != 3 {
		t.Fatalf("gathered %d resources %v, want 3", len(resources), resources)
	}

	// Primary resource should be first. Node is cluster-scoped (no namespace),
	// so String() returns "Node/node-1".
	if resources[0] != "Node/node-1" {
		t.Errorf("first gathered resource = %q, want %q", resources[0], "Node/node-1")
	}

	// Should have exactly 3 sections (1 primary + 2 affected).
	if len(sections) != 3 {
		t.Fatalf("got %d sections, want 3", len(sections))
	}
}

func TestGatherForSuperEvent_MaxAffectedZero(t *testing.T) {
	var mu sync.Mutex
	var gatheredResources []string

	trackingGatherer := &trackingStubGatherer{
		name:     "tracker",
		mu:       &mu,
		gathered: &gatheredResources,
	}

	event := &model.SuperEvent{
		ID:              "super-1",
		CorrelationRule: "NodeCascade",
		PrimaryResource: model.ResourceRef{Kind: "Node", Name: "node-1", UID: "uid-node-1"},
		FaultEvents: []model.FaultEvent{
			{
				ID:           "fe-1",
				DetectorName: "PodFailed",
				Resource:     model.ResourceRef{Kind: "Pod", Namespace: "default", Name: "pod-1", UID: "uid-pod-1"},
			},
		},
		Severity: model.SeverityCritical,
	}

	sections := GatherForSuperEvent(context.Background(), silentLogger(), []Gatherer{trackingGatherer}, event, 0)

	// Only primary resource should be gathered.
	mu.Lock()
	count := len(gatheredResources)
	mu.Unlock()

	if count != 1 {
		t.Errorf("gathered %d resources, want 1 (primary only)", count)
	}
	if len(sections) != 1 {
		t.Errorf("got %d sections, want 1", len(sections))
	}
}

func TestGatherForSuperEvent_NegativeMaxAffected(t *testing.T) {
	var mu sync.Mutex
	var gatheredResources []string

	trackingGatherer := &trackingStubGatherer{
		name:     "tracker",
		mu:       &mu,
		gathered: &gatheredResources,
	}

	event := &model.SuperEvent{
		ID:              "super-1",
		CorrelationRule: "Test",
		PrimaryResource: model.ResourceRef{Kind: "Node", Name: "node-1", UID: "uid-node-1"},
		FaultEvents: []model.FaultEvent{
			{
				ID:       "fe-1",
				Resource: model.ResourceRef{Kind: "Pod", Namespace: "default", Name: "pod-1", UID: "uid-pod-1"},
			},
		},
		Severity: model.SeverityWarning,
	}

	sections := GatherForSuperEvent(context.Background(), silentLogger(), []Gatherer{trackingGatherer}, event, -1)

	mu.Lock()
	count := len(gatheredResources)
	mu.Unlock()

	if count != 1 {
		t.Errorf("gathered %d resources, want 1 (primary only, negative treated as 0)", count)
	}
	if len(sections) != 1 {
		t.Errorf("got %d sections, want 1", len(sections))
	}
}

func TestGatherForSuperEvent_DeduplicatesResources(t *testing.T) {
	var mu sync.Mutex
	var gatheredResources []string

	trackingGatherer := &trackingStubGatherer{
		name:     "tracker",
		mu:       &mu,
		gathered: &gatheredResources,
	}

	// FaultEvent references the same resource as the primary.
	event := &model.SuperEvent{
		ID:              "super-1",
		CorrelationRule: "Test",
		PrimaryResource: model.ResourceRef{Kind: "Node", Name: "node-1", UID: "uid-node-1"},
		FaultEvents: []model.FaultEvent{
			{
				ID:       "fe-1",
				Resource: model.ResourceRef{Kind: "Node", Name: "node-1", UID: "uid-node-1"},
			},
			{
				ID:       "fe-2",
				Resource: model.ResourceRef{Kind: "Pod", Namespace: "default", Name: "pod-1", UID: "uid-pod-1"},
			},
		},
		Severity: model.SeverityWarning,
	}

	sections := GatherForSuperEvent(context.Background(), silentLogger(), []Gatherer{trackingGatherer}, event, 5)

	mu.Lock()
	count := len(gatheredResources)
	mu.Unlock()

	// Primary + 1 unique affected (node-1 duplicate is skipped).
	if count != 2 {
		t.Errorf("gathered %d resources, want 2 (primary + 1 unique affected)", count)
	}
	if len(sections) != 2 {
		t.Errorf("got %d sections, want 2", len(sections))
	}
}

func TestGatherForSuperEvent_DeduplicatesByKindNameNamespace(t *testing.T) {
	var mu sync.Mutex
	var gatheredResources []string

	trackingGatherer := &trackingStubGatherer{
		name:     "tracker",
		mu:       &mu,
		gathered: &gatheredResources,
	}

	// Resources with empty UIDs — deduplication falls back to Kind/Namespace/Name.
	event := &model.SuperEvent{
		ID:              "super-1",
		CorrelationRule: "Test",
		PrimaryResource: model.ResourceRef{Kind: "Node", Name: "node-1"},
		FaultEvents: []model.FaultEvent{
			{
				ID:       "fe-1",
				Resource: model.ResourceRef{Kind: "Node", Name: "node-1"}, // Same as primary.
			},
			{
				ID:       "fe-2",
				Resource: model.ResourceRef{Kind: "Pod", Namespace: "default", Name: "pod-1"},
			},
		},
		Severity: model.SeverityWarning,
	}

	sections := GatherForSuperEvent(context.Background(), silentLogger(), []Gatherer{trackingGatherer}, event, 5)

	mu.Lock()
	count := len(gatheredResources)
	mu.Unlock()

	if count != 2 {
		t.Errorf("gathered %d resources, want 2", count)
	}
	if len(sections) != 2 {
		t.Errorf("got %d sections, want 2", len(sections))
	}
}

// trackingStubGatherer records which resources it gathers for.
type trackingStubGatherer struct {
	name     string
	mu       *sync.Mutex
	gathered *[]string
}

func (t *trackingStubGatherer) Name() string { return t.name }

func (t *trackingStubGatherer) Gather(_ context.Context, ref model.ResourceRef) (*model.DiagnosticSection, error) {
	t.mu.Lock()
	*t.gathered = append(*t.gathered, ref.String())
	t.mu.Unlock()

	return &model.DiagnosticSection{
		GathererName: t.name,
		Title:        t.name + " for " + ref.String(),
		Content:      "data for " + ref.String(),
		Format:       "text",
	}, nil
}
