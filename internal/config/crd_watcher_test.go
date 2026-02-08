package config

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"text/template"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1alpha1 "github.com/k8s-wormsign/k8s-wormsign/api/v1alpha1"
	celengine "github.com/k8s-wormsign/k8s-wormsign/internal/detector/cel"
	"github.com/k8s-wormsign/k8s-wormsign/internal/filter"
)

// --- Mock registries ---

type mockDetectorRegistry struct {
	mu          sync.Mutex
	registered  map[string]RegisteredDetector
	unregCalled map[string]int
}

func newMockDetectorRegistry() *mockDetectorRegistry {
	return &mockDetectorRegistry{
		registered:  make(map[string]RegisteredDetector),
		unregCalled: make(map[string]int),
	}
}

func (m *mockDetectorRegistry) RegisterDetector(key string, detector RegisteredDetector) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.registered[key] = detector
}

func (m *mockDetectorRegistry) UnregisterDetector(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.registered, key)
	m.unregCalled[key]++
}

func (m *mockDetectorRegistry) get(key string) (RegisteredDetector, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.registered[key]
	return d, ok
}

func (m *mockDetectorRegistry) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.registered)
}

type mockGathererRegistry struct {
	mu          sync.Mutex
	registered  map[string]RegisteredGatherer
	unregCalled map[string]int
}

func newMockGathererRegistry() *mockGathererRegistry {
	return &mockGathererRegistry{
		registered:  make(map[string]RegisteredGatherer),
		unregCalled: make(map[string]int),
	}
}

func (m *mockGathererRegistry) RegisterGatherer(key string, gatherer RegisteredGatherer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.registered[key] = gatherer
}

func (m *mockGathererRegistry) UnregisterGatherer(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.registered, key)
	m.unregCalled[key]++
}

func (m *mockGathererRegistry) get(key string) (RegisteredGatherer, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	g, ok := m.registered[key]
	return g, ok
}

func (m *mockGathererRegistry) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.registered)
}

type mockSinkRegistry struct {
	mu          sync.Mutex
	registered  map[string]RegisteredSink
	unregCalled map[string]int
}

func newMockSinkRegistry() *mockSinkRegistry {
	return &mockSinkRegistry{
		registered:  make(map[string]RegisteredSink),
		unregCalled: make(map[string]int),
	}
}

func (m *mockSinkRegistry) RegisterSink(key string, sink RegisteredSink) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.registered[key] = sink
}

func (m *mockSinkRegistry) UnregisterSink(key string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.registered, key)
	m.unregCalled[key]++
}

func (m *mockSinkRegistry) get(key string) (RegisteredSink, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.registered[key]
	return s, ok
}

func (m *mockSinkRegistry) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.registered)
}

// --- Mock status updater ---

type statusUpdate struct {
	resourceType string
	namespace    string
	name         string
	conditions   []metav1.Condition
}

type mockStatusUpdater struct {
	mu      sync.Mutex
	updates []statusUpdate
	err     error // inject error for testing
}

func newMockStatusUpdater() *mockStatusUpdater {
	return &mockStatusUpdater{}
}

func (m *mockStatusUpdater) UpdateDetectorStatus(_ context.Context, namespace, name string, status v1alpha1.WormsignDetectorStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.updates = append(m.updates, statusUpdate{
		resourceType: "detector",
		namespace:    namespace,
		name:         name,
		conditions:   status.Conditions,
	})
	return nil
}

func (m *mockStatusUpdater) UpdateGathererStatus(_ context.Context, namespace, name string, status v1alpha1.WormsignGathererStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.updates = append(m.updates, statusUpdate{
		resourceType: "gatherer",
		namespace:    namespace,
		name:         name,
		conditions:   status.Conditions,
	})
	return nil
}

func (m *mockStatusUpdater) UpdateSinkStatus(_ context.Context, namespace, name string, status v1alpha1.WormsignSinkStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.updates = append(m.updates, statusUpdate{
		resourceType: "sink",
		namespace:    namespace,
		name:         name,
		conditions:   status.Conditions,
	})
	return nil
}

func (m *mockStatusUpdater) UpdatePolicyStatus(_ context.Context, namespace, name string, status v1alpha1.WormsignPolicyStatus) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.updates = append(m.updates, statusUpdate{
		resourceType: "policy",
		namespace:    namespace,
		name:         name,
		conditions:   status.Conditions,
	})
	return nil
}

func (m *mockStatusUpdater) lastUpdate() (statusUpdate, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.updates) == 0 {
		return statusUpdate{}, false
	}
	return m.updates[len(m.updates)-1], true
}

func (m *mockStatusUpdater) updateCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.updates)
}

func (m *mockStatusUpdater) updatesForResource(resourceType, namespace, name string) []statusUpdate {
	m.mu.Lock()
	defer m.mu.Unlock()
	var result []statusUpdate
	for _, u := range m.updates {
		if u.resourceType == resourceType && u.namespace == namespace && u.name == name {
			result = append(result, u)
		}
	}
	return result
}

// --- Test helpers ---

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(discard{}, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

type discard struct{}

func (discard) Write(p []byte) (int, error) { return len(p), nil }

func newTestCRDWatcher(t *testing.T) (*CRDWatcher, *mockDetectorRegistry, *mockGathererRegistry, *mockSinkRegistry, *mockStatusUpdater) {
	t.Helper()
	dr := newMockDetectorRegistry()
	gr := newMockGathererRegistry()
	sr := newMockSinkRegistry()
	su := newMockStatusUpdater()
	fe, err := filter.NewEngine(filter.GlobalFilterConfig{}, silentLogger())
	if err != nil {
		t.Fatalf("failed to create filter engine: %v", err)
	}
	eval := celengine.NewEvaluator(0)

	w, err := NewCRDWatcher(CRDWatcherConfig{
		CELEvaluator:     eval,
		DetectorRegistry: dr,
		GathererRegistry: gr,
		SinkRegistry:     sr,
		FilterEngine:     fe,
		StatusUpdater:    su,
		Logger:           silentLogger(),
	})
	if err != nil {
		t.Fatalf("failed to create CRDWatcher: %v", err)
	}
	return w, dr, gr, sr, su
}

func findCondition(conditions []metav1.Condition, condType string) *metav1.Condition {
	for i, c := range conditions {
		if c.Type == condType {
			return &conditions[i]
		}
	}
	return nil
}

func validDetector(ns, name string) *v1alpha1.WormsignDetector {
	return &v1alpha1.WormsignDetector{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ns,
			Name:      name,
		},
		Spec: v1alpha1.WormsignDetectorSpec{
			Resource:  "pods",
			Condition: `resource.metadata.name == "test"`,
			Severity:  v1alpha1.SeverityWarning,
			Cooldown:  "30m",
		},
	}
}

func validGatherer(ns, name string) *v1alpha1.WormsignGatherer {
	return &v1alpha1.WormsignGatherer{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ns,
			Name:      name,
		},
		Spec: v1alpha1.WormsignGathererSpec{
			TriggerOn: v1alpha1.GathererTrigger{
				ResourceKinds: []string{"Pod"},
			},
			Collect: []v1alpha1.CollectAction{
				{
					Type:       v1alpha1.CollectTypeResource,
					APIVersion: "v1",
					Resource:   "pods",
					Name:       "{resource.name}",
				},
			},
		},
	}
}

func validSink(ns, name string) *v1alpha1.WormsignSink {
	return &v1alpha1.WormsignSink{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ns,
			Name:      name,
		},
		Spec: v1alpha1.WormsignSinkSpec{
			Type: v1alpha1.SinkTypeWebhook,
			Webhook: &v1alpha1.WebhookConfig{
				URL:          "https://example.com/webhook",
				Method:       "POST",
				BodyTemplate: `{"msg": "{{ .RootCause }}"}`,
			},
			SeverityFilter: []v1alpha1.Severity{v1alpha1.SeverityCritical},
		},
	}
}

func validPolicy(ns, name string) *v1alpha1.WormsignPolicy {
	return &v1alpha1.WormsignPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: ns,
			Name:      name,
		},
		Spec: v1alpha1.WormsignPolicySpec{
			Action: v1alpha1.PolicyActionExclude,
			Match: &v1alpha1.PolicyMatch{
				OwnerNames: []string{"test-*"},
			},
		},
	}
}

// ==========================================
// Tests: NewCRDWatcher construction
// ==========================================

func TestNewCRDWatcher_ValidConfig(t *testing.T) {
	w, _, _, _, _ := newTestCRDWatcher(t)
	if w == nil {
		t.Fatal("expected non-nil CRDWatcher")
	}
}

func TestNewCRDWatcher_NilCELEvaluator(t *testing.T) {
	_, err := NewCRDWatcher(CRDWatcherConfig{
		CELEvaluator:     nil,
		DetectorRegistry: newMockDetectorRegistry(),
		GathererRegistry: newMockGathererRegistry(),
		SinkRegistry:     newMockSinkRegistry(),
		FilterEngine:     &filter.Engine{},
		StatusUpdater:    newMockStatusUpdater(),
	})
	if err == nil {
		t.Fatal("expected error for nil CEL evaluator")
	}
}

func TestNewCRDWatcher_NilDetectorRegistry(t *testing.T) {
	_, err := NewCRDWatcher(CRDWatcherConfig{
		CELEvaluator:     celengine.NewEvaluator(0),
		DetectorRegistry: nil,
		GathererRegistry: newMockGathererRegistry(),
		SinkRegistry:     newMockSinkRegistry(),
		FilterEngine:     &filter.Engine{},
		StatusUpdater:    newMockStatusUpdater(),
	})
	if err == nil {
		t.Fatal("expected error for nil detector registry")
	}
}

func TestNewCRDWatcher_NilGathererRegistry(t *testing.T) {
	_, err := NewCRDWatcher(CRDWatcherConfig{
		CELEvaluator:     celengine.NewEvaluator(0),
		DetectorRegistry: newMockDetectorRegistry(),
		GathererRegistry: nil,
		SinkRegistry:     newMockSinkRegistry(),
		FilterEngine:     &filter.Engine{},
		StatusUpdater:    newMockStatusUpdater(),
	})
	if err == nil {
		t.Fatal("expected error for nil gatherer registry")
	}
}

func TestNewCRDWatcher_NilSinkRegistry(t *testing.T) {
	_, err := NewCRDWatcher(CRDWatcherConfig{
		CELEvaluator:     celengine.NewEvaluator(0),
		DetectorRegistry: newMockDetectorRegistry(),
		GathererRegistry: newMockGathererRegistry(),
		SinkRegistry:     nil,
		FilterEngine:     &filter.Engine{},
		StatusUpdater:    newMockStatusUpdater(),
	})
	if err == nil {
		t.Fatal("expected error for nil sink registry")
	}
}

func TestNewCRDWatcher_NilFilterEngine(t *testing.T) {
	_, err := NewCRDWatcher(CRDWatcherConfig{
		CELEvaluator:     celengine.NewEvaluator(0),
		DetectorRegistry: newMockDetectorRegistry(),
		GathererRegistry: newMockGathererRegistry(),
		SinkRegistry:     newMockSinkRegistry(),
		FilterEngine:     nil,
		StatusUpdater:    newMockStatusUpdater(),
	})
	if err == nil {
		t.Fatal("expected error for nil filter engine")
	}
}

func TestNewCRDWatcher_NilStatusUpdater(t *testing.T) {
	_, err := NewCRDWatcher(CRDWatcherConfig{
		CELEvaluator:     celengine.NewEvaluator(0),
		DetectorRegistry: newMockDetectorRegistry(),
		GathererRegistry: newMockGathererRegistry(),
		SinkRegistry:     newMockSinkRegistry(),
		FilterEngine:     &filter.Engine{},
		StatusUpdater:    nil,
	})
	if err == nil {
		t.Fatal("expected error for nil status updater")
	}
}

func TestNewCRDWatcher_NilLoggerUsesDefault(t *testing.T) {
	fe, _ := filter.NewEngine(filter.GlobalFilterConfig{}, silentLogger())
	w, err := NewCRDWatcher(CRDWatcherConfig{
		CELEvaluator:     celengine.NewEvaluator(0),
		DetectorRegistry: newMockDetectorRegistry(),
		GathererRegistry: newMockGathererRegistry(),
		SinkRegistry:     newMockSinkRegistry(),
		FilterEngine:     fe,
		StatusUpdater:    newMockStatusUpdater(),
		Logger:           nil,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if w.logger == nil {
		t.Fatal("expected non-nil logger even when nil is passed")
	}
}

// ==========================================
// Tests: crdKey helper
// ==========================================

func TestCrdKey_WithNamespace(t *testing.T) {
	key := crdKey("default", "my-detector")
	if key != "default/my-detector" {
		t.Errorf("expected 'default/my-detector', got %q", key)
	}
}

func TestCrdKey_WithoutNamespace(t *testing.T) {
	key := crdKey("", "my-detector")
	if key != "my-detector" {
		t.Errorf("expected 'my-detector', got %q", key)
	}
}

// ==========================================
// Tests: WormsignDetector lifecycle
// ==========================================

func TestDetector_AddValid(t *testing.T) {
	w, dr, _, _, su := newTestCRDWatcher(t)
	ctx := context.Background()

	det := validDetector("default", "my-detector")
	w.OnDetectorAdd(ctx, det)

	key := "default/my-detector"
	if _, ok := dr.get(key); !ok {
		t.Error("expected detector to be registered")
	}

	// Check status was set to Ready=True
	updates := su.updatesForResource("detector", "default", "my-detector")
	if len(updates) == 0 {
		t.Fatal("expected status update")
	}
	last := updates[len(updates)-1]
	readyCond := findCondition(last.conditions, v1alpha1.ConditionReady)
	if readyCond == nil {
		t.Fatal("expected Ready condition")
	}
	if readyCond.Status != metav1.ConditionTrue {
		t.Errorf("expected Ready=True, got %s", readyCond.Status)
	}

	activeCond := findCondition(last.conditions, v1alpha1.ConditionActive)
	if activeCond == nil {
		t.Fatal("expected Active condition")
	}
	if activeCond.Status != metav1.ConditionTrue {
		t.Errorf("expected Active=True, got %s", activeCond.Status)
	}
}

func TestDetector_AddValid_FieldsPropagated(t *testing.T) {
	w, dr, _, _, _ := newTestCRDWatcher(t)
	ctx := context.Background()

	det := validDetector("payments", "high-restart")
	det.Spec.Description = "detects high restarts"
	det.Spec.Params = map[string]string{"maxRestarts": "10"}
	det.Spec.Severity = v1alpha1.SeverityCritical
	det.Spec.Cooldown = "1h"
	w.OnDetectorAdd(ctx, det)

	key := "payments/high-restart"
	reg, ok := dr.get(key)
	if !ok {
		t.Fatal("expected detector to be registered")
	}
	if reg.Name != "high-restart" {
		t.Errorf("expected Name='high-restart', got %q", reg.Name)
	}
	if reg.Namespace != "payments" {
		t.Errorf("expected Namespace='payments', got %q", reg.Namespace)
	}
	if reg.Resource != "pods" {
		t.Errorf("expected Resource='pods', got %q", reg.Resource)
	}
	if reg.Description != "detects high restarts" {
		t.Errorf("expected Description='detects high restarts', got %q", reg.Description)
	}
	if reg.Severity != v1alpha1.SeverityCritical {
		t.Errorf("expected Severity=critical, got %q", reg.Severity)
	}
	if reg.Cooldown != "1h" {
		t.Errorf("expected Cooldown='1h', got %q", reg.Cooldown)
	}
	if reg.Params["maxRestarts"] != "10" {
		t.Errorf("expected param maxRestarts='10', got %q", reg.Params["maxRestarts"])
	}
}

func TestDetector_AddInvalid_EmptyResource(t *testing.T) {
	w, dr, _, _, su := newTestCRDWatcher(t)
	ctx := context.Background()

	det := validDetector("default", "bad-detector")
	det.Spec.Resource = ""
	w.OnDetectorAdd(ctx, det)

	if dr.count() != 0 {
		t.Error("expected no detectors to be registered")
	}

	updates := su.updatesForResource("detector", "default", "bad-detector")
	if len(updates) == 0 {
		t.Fatal("expected status update for invalid detector")
	}
	readyCond := findCondition(updates[len(updates)-1].conditions, v1alpha1.ConditionReady)
	if readyCond == nil || readyCond.Status != metav1.ConditionFalse {
		t.Error("expected Ready=False for invalid detector")
	}
	if readyCond != nil && readyCond.Message == "" {
		t.Error("expected non-empty message for invalid condition")
	}
}

func TestDetector_AddInvalid_EmptyCondition(t *testing.T) {
	w, dr, _, _, su := newTestCRDWatcher(t)
	ctx := context.Background()

	det := validDetector("default", "bad-detector")
	det.Spec.Condition = ""
	w.OnDetectorAdd(ctx, det)

	if dr.count() != 0 {
		t.Error("expected no detectors registered")
	}
	updates := su.updatesForResource("detector", "default", "bad-detector")
	if len(updates) == 0 {
		t.Fatal("expected status update")
	}
	readyCond := findCondition(updates[len(updates)-1].conditions, v1alpha1.ConditionReady)
	if readyCond == nil || readyCond.Status != metav1.ConditionFalse {
		t.Error("expected Ready=False")
	}
}

func TestDetector_AddInvalid_BadCELExpression(t *testing.T) {
	w, dr, _, _, su := newTestCRDWatcher(t)
	ctx := context.Background()

	det := validDetector("default", "bad-cel")
	det.Spec.Condition = "this is not valid CEL !!!"
	w.OnDetectorAdd(ctx, det)

	if dr.count() != 0 {
		t.Error("expected no detectors registered")
	}
	updates := su.updatesForResource("detector", "default", "bad-cel")
	if len(updates) == 0 {
		t.Fatal("expected status update")
	}
	readyCond := findCondition(updates[len(updates)-1].conditions, v1alpha1.ConditionReady)
	if readyCond == nil || readyCond.Status != metav1.ConditionFalse {
		t.Error("expected Ready=False for bad CEL")
	}
}

func TestDetector_AddInvalid_BadSeverity(t *testing.T) {
	w, dr, _, _, su := newTestCRDWatcher(t)
	ctx := context.Background()

	det := validDetector("default", "bad-severity")
	det.Spec.Severity = "extreme"
	w.OnDetectorAdd(ctx, det)

	if dr.count() != 0 {
		t.Error("expected no detectors registered")
	}
	updates := su.updatesForResource("detector", "default", "bad-severity")
	if len(updates) == 0 {
		t.Fatal("expected status update")
	}
	readyCond := findCondition(updates[len(updates)-1].conditions, v1alpha1.ConditionReady)
	if readyCond == nil || readyCond.Status != metav1.ConditionFalse {
		t.Error("expected Ready=False for bad severity")
	}
}

func TestDetector_UpdateValid(t *testing.T) {
	w, dr, _, _, _ := newTestCRDWatcher(t)
	ctx := context.Background()

	det := validDetector("default", "my-detector")
	w.OnDetectorAdd(ctx, det)

	// Update with new condition
	det2 := validDetector("default", "my-detector")
	det2.Spec.Condition = `resource.metadata.name == "updated"`
	det2.Spec.Severity = v1alpha1.SeverityCritical
	w.OnDetectorUpdate(ctx, det2)

	reg, ok := dr.get("default/my-detector")
	if !ok {
		t.Fatal("expected detector to still be registered after update")
	}
	if reg.Severity != v1alpha1.SeverityCritical {
		t.Errorf("expected severity to be updated to critical, got %q", reg.Severity)
	}
}

func TestDetector_UpdateInvalid_PreviousRemains(t *testing.T) {
	w, dr, _, _, su := newTestCRDWatcher(t)
	ctx := context.Background()

	// Add a valid detector first
	det := validDetector("default", "my-detector")
	w.OnDetectorAdd(ctx, det)

	_, ok := dr.get("default/my-detector")
	if !ok {
		t.Fatal("expected initial detector to be registered")
	}

	// Update with invalid CEL — should keep previous valid config
	det2 := validDetector("default", "my-detector")
	det2.Spec.Condition = "INVALID_CEL !!!"
	w.OnDetectorUpdate(ctx, det2)

	// Previous valid detector should still be in registry
	_, ok = dr.get("default/my-detector")
	if !ok {
		t.Error("expected previous valid detector to remain registered")
	}

	// Status should show Ready=False but Active=True (previous version active)
	updates := su.updatesForResource("detector", "default", "my-detector")
	lastUpdate := updates[len(updates)-1]
	readyCond := findCondition(lastUpdate.conditions, v1alpha1.ConditionReady)
	if readyCond == nil || readyCond.Status != metav1.ConditionFalse {
		t.Error("expected Ready=False after invalid update")
	}
	activeCond := findCondition(lastUpdate.conditions, v1alpha1.ConditionActive)
	if activeCond == nil || activeCond.Status != metav1.ConditionTrue {
		t.Error("expected Active=True (previous version) after invalid update")
	}
	if activeCond != nil && activeCond.Reason != "PreviousVersionActive" {
		t.Errorf("expected reason 'PreviousVersionActive', got %q", activeCond.Reason)
	}
}

func TestDetector_UpdateInvalid_NoPreviousValid(t *testing.T) {
	w, dr, _, _, su := newTestCRDWatcher(t)
	ctx := context.Background()

	// Add invalid detector (never valid)
	det := validDetector("default", "never-valid")
	det.Spec.Condition = "BAD_CEL !!!"
	w.OnDetectorAdd(ctx, det)

	if dr.count() != 0 {
		t.Error("expected no detectors registered")
	}

	// Update with another invalid
	det2 := validDetector("default", "never-valid")
	det2.Spec.Condition = "STILL_BAD !!!"
	w.OnDetectorUpdate(ctx, det2)

	// Status should show Active=False since no previous valid version
	updates := su.updatesForResource("detector", "default", "never-valid")
	lastUpdate := updates[len(updates)-1]
	activeCond := findCondition(lastUpdate.conditions, v1alpha1.ConditionActive)
	if activeCond == nil || activeCond.Status != metav1.ConditionFalse {
		t.Error("expected Active=False when no previous valid version exists")
	}
	if activeCond != nil && activeCond.Reason != "NotRegistered" {
		t.Errorf("expected reason 'NotRegistered', got %q", activeCond.Reason)
	}
}

func TestDetector_Delete(t *testing.T) {
	w, dr, _, _, _ := newTestCRDWatcher(t)
	ctx := context.Background()

	det := validDetector("default", "my-detector")
	w.OnDetectorAdd(ctx, det)

	_, ok := dr.get("default/my-detector")
	if !ok {
		t.Fatal("expected detector to be registered before delete")
	}

	w.OnDetectorDelete("default/my-detector")

	_, ok = dr.get("default/my-detector")
	if ok {
		t.Error("expected detector to be unregistered after delete")
	}
}

func TestDetector_DeleteThenAdd(t *testing.T) {
	w, dr, _, _, _ := newTestCRDWatcher(t)
	ctx := context.Background()

	det := validDetector("default", "my-detector")
	w.OnDetectorAdd(ctx, det)
	w.OnDetectorDelete("default/my-detector")
	if dr.count() != 0 {
		t.Error("expected zero detectors after delete")
	}

	// Re-add should work
	w.OnDetectorAdd(ctx, det)
	if dr.count() != 1 {
		t.Error("expected one detector after re-add")
	}
}

func TestDetector_DeleteNonExistent(t *testing.T) {
	w, _, _, _, _ := newTestCRDWatcher(t)
	// Should not panic
	w.OnDetectorDelete("nonexistent/detector")
}

func TestDetector_AllSeverities(t *testing.T) {
	tests := []struct {
		severity v1alpha1.Severity
		valid    bool
	}{
		{v1alpha1.SeverityCritical, true},
		{v1alpha1.SeverityWarning, true},
		{v1alpha1.SeverityInfo, true},
		{"extreme", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(string(tt.severity), func(t *testing.T) {
			w, dr, _, _, _ := newTestCRDWatcher(t)
			ctx := context.Background()

			det := validDetector("default", "sev-test")
			det.Spec.Severity = tt.severity
			w.OnDetectorAdd(ctx, det)

			if tt.valid {
				if dr.count() != 1 {
					t.Errorf("expected valid severity %q to register", tt.severity)
				}
			} else {
				if dr.count() != 0 {
					t.Errorf("expected invalid severity %q to not register", tt.severity)
				}
			}
		})
	}
}

func TestDetector_WithNamespaceSelector(t *testing.T) {
	w, dr, _, _, _ := newTestCRDWatcher(t)
	ctx := context.Background()

	det := validDetector("wormsign-system", "ns-selector-det")
	det.Spec.NamespaceSelector = &metav1.LabelSelector{
		MatchLabels: map[string]string{
			"wormsign.io/watch": "true",
		},
	}
	w.OnDetectorAdd(ctx, det)

	reg, ok := dr.get("wormsign-system/ns-selector-det")
	if !ok {
		t.Fatal("expected detector with namespace selector to be registered")
	}
	if reg.NamespaceSelector == nil {
		t.Error("expected NamespaceSelector to be propagated")
	}
}

// ==========================================
// Tests: WormsignGatherer lifecycle
// ==========================================

func TestGatherer_AddValid(t *testing.T) {
	w, _, gr, _, su := newTestCRDWatcher(t)
	ctx := context.Background()

	gath := validGatherer("default", "my-gatherer")
	w.OnGathererAdd(ctx, gath)

	key := "default/my-gatherer"
	if _, ok := gr.get(key); !ok {
		t.Error("expected gatherer to be registered")
	}

	updates := su.updatesForResource("gatherer", "default", "my-gatherer")
	if len(updates) == 0 {
		t.Fatal("expected status update")
	}
	readyCond := findCondition(updates[len(updates)-1].conditions, v1alpha1.ConditionReady)
	if readyCond == nil || readyCond.Status != metav1.ConditionTrue {
		t.Error("expected Ready=True for valid gatherer")
	}
}

func TestGatherer_AddValid_FieldsPropagated(t *testing.T) {
	w, _, gr, _, _ := newTestCRDWatcher(t)
	ctx := context.Background()

	gath := validGatherer("payments", "istio-gather")
	gath.Spec.Description = "Collects Istio config"
	gath.Spec.TriggerOn = v1alpha1.GathererTrigger{
		ResourceKinds: []string{"Pod"},
		Detectors:     []string{"PodCrashLoop"},
	}
	gath.Spec.Collect = []v1alpha1.CollectAction{
		{
			Type:       v1alpha1.CollectTypeLogs,
			Container:  "istio-proxy",
			TailLines:  int64Ptr(50),
		},
	}
	w.OnGathererAdd(ctx, gath)

	reg, ok := gr.get("payments/istio-gather")
	if !ok {
		t.Fatal("expected gatherer to be registered")
	}
	if reg.Name != "istio-gather" {
		t.Errorf("expected Name='istio-gather', got %q", reg.Name)
	}
	if reg.Namespace != "payments" {
		t.Errorf("expected Namespace='payments', got %q", reg.Namespace)
	}
	if reg.Description != "Collects Istio config" {
		t.Errorf("expected Description='Collects Istio config', got %q", reg.Description)
	}
	if len(reg.TriggerOn.ResourceKinds) != 1 || reg.TriggerOn.ResourceKinds[0] != "Pod" {
		t.Errorf("unexpected ResourceKinds: %v", reg.TriggerOn.ResourceKinds)
	}
	if len(reg.TriggerOn.Detectors) != 1 || reg.TriggerOn.Detectors[0] != "PodCrashLoop" {
		t.Errorf("unexpected Detectors: %v", reg.TriggerOn.Detectors)
	}
	if len(reg.Collect) != 1 || reg.Collect[0].Type != v1alpha1.CollectTypeLogs {
		t.Errorf("unexpected Collect: %v", reg.Collect)
	}
}

func TestGatherer_AddInvalid_EmptyTriggerOn(t *testing.T) {
	w, _, gr, _, su := newTestCRDWatcher(t)
	ctx := context.Background()

	gath := validGatherer("default", "bad-gatherer")
	gath.Spec.TriggerOn = v1alpha1.GathererTrigger{} // empty
	w.OnGathererAdd(ctx, gath)

	if gr.count() != 0 {
		t.Error("expected no gatherers registered")
	}
	updates := su.updatesForResource("gatherer", "default", "bad-gatherer")
	if len(updates) == 0 {
		t.Fatal("expected status update")
	}
	readyCond := findCondition(updates[len(updates)-1].conditions, v1alpha1.ConditionReady)
	if readyCond == nil || readyCond.Status != metav1.ConditionFalse {
		t.Error("expected Ready=False for empty triggerOn")
	}
}

func TestGatherer_AddInvalid_EmptyCollect(t *testing.T) {
	w, _, gr, _, su := newTestCRDWatcher(t)
	ctx := context.Background()

	gath := validGatherer("default", "no-collect")
	gath.Spec.Collect = nil
	w.OnGathererAdd(ctx, gath)

	if gr.count() != 0 {
		t.Error("expected no gatherers registered")
	}
	updates := su.updatesForResource("gatherer", "default", "no-collect")
	if len(updates) == 0 {
		t.Fatal("expected status update")
	}
	readyCond := findCondition(updates[len(updates)-1].conditions, v1alpha1.ConditionReady)
	if readyCond == nil || readyCond.Status != metav1.ConditionFalse {
		t.Error("expected Ready=False for empty collect")
	}
}

func TestGatherer_AddInvalid_ResourceCollect_MissingAPIVersion(t *testing.T) {
	w, _, gr, _, _ := newTestCRDWatcher(t)
	ctx := context.Background()

	gath := validGatherer("default", "bad-collect")
	gath.Spec.Collect = []v1alpha1.CollectAction{
		{
			Type:     v1alpha1.CollectTypeResource,
			Resource: "pods",
			Name:     "my-pod",
		},
	}
	w.OnGathererAdd(ctx, gath)

	if gr.count() != 0 {
		t.Error("expected no gatherers registered for missing apiVersion")
	}
}

func TestGatherer_AddInvalid_ResourceCollect_MissingResource(t *testing.T) {
	w, _, gr, _, _ := newTestCRDWatcher(t)
	ctx := context.Background()

	gath := validGatherer("default", "bad-collect")
	gath.Spec.Collect = []v1alpha1.CollectAction{
		{
			Type:       v1alpha1.CollectTypeResource,
			APIVersion: "v1",
			Name:       "my-pod",
		},
	}
	w.OnGathererAdd(ctx, gath)

	if gr.count() != 0 {
		t.Error("expected no gatherers registered for missing resource")
	}
}

func TestGatherer_AddInvalid_ResourceCollect_MissingName(t *testing.T) {
	w, _, gr, _, _ := newTestCRDWatcher(t)
	ctx := context.Background()

	gath := validGatherer("default", "bad-collect")
	gath.Spec.Collect = []v1alpha1.CollectAction{
		{
			Type:       v1alpha1.CollectTypeResource,
			APIVersion: "v1",
			Resource:   "pods",
			// Name missing and ListAll is false
		},
	}
	w.OnGathererAdd(ctx, gath)

	if gr.count() != 0 {
		t.Error("expected no gatherers registered when name missing and listAll false")
	}
}

func TestGatherer_AddValid_ResourceCollect_ListAll(t *testing.T) {
	w, _, gr, _, _ := newTestCRDWatcher(t)
	ctx := context.Background()

	gath := validGatherer("default", "list-all")
	gath.Spec.Collect = []v1alpha1.CollectAction{
		{
			Type:       v1alpha1.CollectTypeResource,
			APIVersion: "networking.istio.io/v1",
			Resource:   "destinationrules",
			ListAll:    true,
			// Name not required when ListAll is true
		},
	}
	w.OnGathererAdd(ctx, gath)

	if gr.count() != 1 {
		t.Error("expected gatherer with listAll=true and no name to be valid")
	}
}

func TestGatherer_AddInvalid_UnknownCollectType(t *testing.T) {
	w, _, gr, _, _ := newTestCRDWatcher(t)
	ctx := context.Background()

	gath := validGatherer("default", "bad-type")
	gath.Spec.Collect = []v1alpha1.CollectAction{
		{
			Type: "http", // not supported
		},
	}
	w.OnGathererAdd(ctx, gath)

	if gr.count() != 0 {
		t.Error("expected no gatherers registered for unknown collect type")
	}
}

func TestGatherer_AddValid_LogsCollect(t *testing.T) {
	w, _, gr, _, _ := newTestCRDWatcher(t)
	ctx := context.Background()

	gath := validGatherer("default", "logs-gather")
	gath.Spec.Collect = []v1alpha1.CollectAction{
		{
			Type:      v1alpha1.CollectTypeLogs,
			Container: "istio-proxy",
			TailLines: int64Ptr(50),
			Previous:  true,
		},
	}
	w.OnGathererAdd(ctx, gath)

	if gr.count() != 1 {
		t.Error("expected logs collect action to be valid")
	}
}

func TestGatherer_UpdateValid(t *testing.T) {
	w, _, gr, _, _ := newTestCRDWatcher(t)
	ctx := context.Background()

	gath := validGatherer("default", "my-gatherer")
	w.OnGathererAdd(ctx, gath)

	gath2 := validGatherer("default", "my-gatherer")
	gath2.Spec.Description = "updated description"
	w.OnGathererUpdate(ctx, gath2)

	reg, ok := gr.get("default/my-gatherer")
	if !ok {
		t.Fatal("expected gatherer to still be registered after update")
	}
	if reg.Description != "updated description" {
		t.Errorf("expected description to be updated, got %q", reg.Description)
	}
}

func TestGatherer_UpdateInvalid_PreviousRemains(t *testing.T) {
	w, _, gr, _, _ := newTestCRDWatcher(t)
	ctx := context.Background()

	gath := validGatherer("default", "my-gatherer")
	w.OnGathererAdd(ctx, gath)

	// Update with invalid
	gath2 := validGatherer("default", "my-gatherer")
	gath2.Spec.Collect = nil
	w.OnGathererUpdate(ctx, gath2)

	// Previous should remain registered
	if _, ok := gr.get("default/my-gatherer"); !ok {
		t.Error("expected previous valid gatherer to remain registered")
	}
}

func TestGatherer_Delete(t *testing.T) {
	w, _, gr, _, _ := newTestCRDWatcher(t)
	ctx := context.Background()

	gath := validGatherer("default", "my-gatherer")
	w.OnGathererAdd(ctx, gath)

	w.OnGathererDelete("default/my-gatherer")

	if _, ok := gr.get("default/my-gatherer"); ok {
		t.Error("expected gatherer to be unregistered after delete")
	}
}

func TestGatherer_DeleteNonExistent(t *testing.T) {
	w, _, _, _, _ := newTestCRDWatcher(t)
	// Should not panic
	w.OnGathererDelete("nonexistent/gatherer")
}

func TestGatherer_AddValid_DetectorOnlyTrigger(t *testing.T) {
	w, _, gr, _, _ := newTestCRDWatcher(t)
	ctx := context.Background()

	gath := validGatherer("default", "det-only")
	gath.Spec.TriggerOn = v1alpha1.GathererTrigger{
		Detectors: []string{"PodCrashLoop"},
		// No ResourceKinds — should still be valid
	}
	w.OnGathererAdd(ctx, gath)

	if gr.count() != 1 {
		t.Error("expected gatherer with detectors-only triggerOn to be valid")
	}
}

// ==========================================
// Tests: WormsignSink lifecycle
// ==========================================

func TestSink_AddValid(t *testing.T) {
	w, _, _, sr, su := newTestCRDWatcher(t)
	ctx := context.Background()

	sink := validSink("default", "my-sink")
	w.OnSinkAdd(ctx, sink)

	key := "default/my-sink"
	if _, ok := sr.get(key); !ok {
		t.Error("expected sink to be registered")
	}

	updates := su.updatesForResource("sink", "default", "my-sink")
	if len(updates) == 0 {
		t.Fatal("expected status update")
	}
	readyCond := findCondition(updates[len(updates)-1].conditions, v1alpha1.ConditionReady)
	if readyCond == nil || readyCond.Status != metav1.ConditionTrue {
		t.Error("expected Ready=True for valid sink")
	}
}

func TestSink_AddValid_FieldsPropagated(t *testing.T) {
	w, _, _, sr, _ := newTestCRDWatcher(t)
	ctx := context.Background()

	sink := validSink("payments", "teams-sink")
	sink.Spec.Description = "Teams notification"
	sink.Spec.SeverityFilter = []v1alpha1.Severity{v1alpha1.SeverityCritical, v1alpha1.SeverityWarning}
	w.OnSinkAdd(ctx, sink)

	reg, ok := sr.get("payments/teams-sink")
	if !ok {
		t.Fatal("expected sink to be registered")
	}
	if reg.Name != "teams-sink" {
		t.Errorf("expected Name='teams-sink', got %q", reg.Name)
	}
	if reg.Namespace != "payments" {
		t.Errorf("expected Namespace='payments', got %q", reg.Namespace)
	}
	if reg.Description != "Teams notification" {
		t.Errorf("expected Description='Teams notification', got %q", reg.Description)
	}
	if reg.SinkType != v1alpha1.SinkTypeWebhook {
		t.Errorf("expected SinkType='webhook', got %q", reg.SinkType)
	}
	if len(reg.SeverityFilter) != 2 {
		t.Errorf("expected 2 severity filters, got %d", len(reg.SeverityFilter))
	}
	if reg.BodyTemplate == nil {
		t.Error("expected BodyTemplate to be parsed")
	}
}

func TestSink_AddValid_WithSecretRef(t *testing.T) {
	w, _, _, sr, _ := newTestCRDWatcher(t)
	ctx := context.Background()

	sink := validSink("default", "secret-sink")
	sink.Spec.Webhook = &v1alpha1.WebhookConfig{
		URLSecretRef: &v1alpha1.SecretReference{
			Name: "my-secrets",
			Key:  "webhook-url",
		},
		Method: "POST",
	}
	w.OnSinkAdd(ctx, sink)

	if _, ok := sr.get("default/secret-sink"); !ok {
		t.Error("expected sink with URLSecretRef to be registered")
	}
}

func TestSink_AddValid_NoBodyTemplate(t *testing.T) {
	w, _, _, sr, _ := newTestCRDWatcher(t)
	ctx := context.Background()

	sink := validSink("default", "no-template")
	sink.Spec.Webhook = &v1alpha1.WebhookConfig{
		URL:    "https://example.com/webhook",
		Method: "POST",
	}
	w.OnSinkAdd(ctx, sink)

	reg, ok := sr.get("default/no-template")
	if !ok {
		t.Fatal("expected sink without body template to be registered")
	}
	if reg.BodyTemplate != nil {
		t.Error("expected BodyTemplate to be nil when not provided")
	}
}

func TestSink_AddInvalid_NonWebhookType(t *testing.T) {
	w, _, _, sr, su := newTestCRDWatcher(t)
	ctx := context.Background()

	sink := validSink("default", "bad-type")
	sink.Spec.Type = "slack" // not valid for custom sinks
	w.OnSinkAdd(ctx, sink)

	if sr.count() != 0 {
		t.Error("expected no sinks registered for non-webhook type")
	}
	updates := su.updatesForResource("sink", "default", "bad-type")
	if len(updates) == 0 {
		t.Fatal("expected status update")
	}
	readyCond := findCondition(updates[len(updates)-1].conditions, v1alpha1.ConditionReady)
	if readyCond == nil || readyCond.Status != metav1.ConditionFalse {
		t.Error("expected Ready=False")
	}
}

func TestSink_AddInvalid_NilWebhookConfig(t *testing.T) {
	w, _, _, sr, _ := newTestCRDWatcher(t)
	ctx := context.Background()

	sink := validSink("default", "no-webhook")
	sink.Spec.Webhook = nil
	w.OnSinkAdd(ctx, sink)

	if sr.count() != 0 {
		t.Error("expected no sinks registered for nil webhook config")
	}
}

func TestSink_AddInvalid_NoURLOrSecretRef(t *testing.T) {
	w, _, _, sr, _ := newTestCRDWatcher(t)
	ctx := context.Background()

	sink := validSink("default", "no-url")
	sink.Spec.Webhook = &v1alpha1.WebhookConfig{
		Method: "POST",
	}
	w.OnSinkAdd(ctx, sink)

	if sr.count() != 0 {
		t.Error("expected no sinks registered without URL or secretRef")
	}
}

func TestSink_AddInvalid_BothURLAndSecretRef(t *testing.T) {
	w, _, _, sr, _ := newTestCRDWatcher(t)
	ctx := context.Background()

	sink := validSink("default", "both-url")
	sink.Spec.Webhook = &v1alpha1.WebhookConfig{
		URL: "https://example.com",
		URLSecretRef: &v1alpha1.SecretReference{
			Name: "secret",
			Key:  "key",
		},
		Method: "POST",
	}
	w.OnSinkAdd(ctx, sink)

	if sr.count() != 0 {
		t.Error("expected no sinks registered with both URL and secretRef")
	}
}

func TestSink_AddInvalid_BadSeverityFilter(t *testing.T) {
	w, _, _, sr, _ := newTestCRDWatcher(t)
	ctx := context.Background()

	sink := validSink("default", "bad-sev")
	sink.Spec.SeverityFilter = []v1alpha1.Severity{"extreme"}
	w.OnSinkAdd(ctx, sink)

	if sr.count() != 0 {
		t.Error("expected no sinks registered for bad severity filter")
	}
}

func TestSink_AddInvalid_BadBodyTemplate(t *testing.T) {
	w, _, _, sr, su := newTestCRDWatcher(t)
	ctx := context.Background()

	sink := validSink("default", "bad-template")
	sink.Spec.Webhook.BodyTemplate = `{{ .RootCause | bad_func }}`
	w.OnSinkAdd(ctx, sink)

	if sr.count() != 0 {
		t.Error("expected no sinks registered for invalid template")
	}
	updates := su.updatesForResource("sink", "default", "bad-template")
	if len(updates) == 0 {
		t.Fatal("expected status update")
	}
	readyCond := findCondition(updates[len(updates)-1].conditions, v1alpha1.ConditionReady)
	if readyCond == nil || readyCond.Status != metav1.ConditionFalse {
		t.Error("expected Ready=False for bad template")
	}
}

func TestSink_UpdateValid(t *testing.T) {
	w, _, _, sr, _ := newTestCRDWatcher(t)
	ctx := context.Background()

	sink := validSink("default", "my-sink")
	w.OnSinkAdd(ctx, sink)

	sink2 := validSink("default", "my-sink")
	sink2.Spec.Description = "updated"
	sink2.Spec.SeverityFilter = []v1alpha1.Severity{v1alpha1.SeverityInfo}
	w.OnSinkUpdate(ctx, sink2)

	reg, ok := sr.get("default/my-sink")
	if !ok {
		t.Fatal("expected sink to still be registered after update")
	}
	if len(reg.SeverityFilter) != 1 || reg.SeverityFilter[0] != v1alpha1.SeverityInfo {
		t.Error("expected severity filter to be updated")
	}
}

func TestSink_UpdateInvalid_PreviousRemains(t *testing.T) {
	w, _, _, sr, su := newTestCRDWatcher(t)
	ctx := context.Background()

	sink := validSink("default", "my-sink")
	w.OnSinkAdd(ctx, sink)

	// Update with invalid template
	sink2 := validSink("default", "my-sink")
	sink2.Spec.Webhook.BodyTemplate = `{{ .Bad | invalid_func }}`
	w.OnSinkUpdate(ctx, sink2)

	// Previous should remain registered
	if _, ok := sr.get("default/my-sink"); !ok {
		t.Error("expected previous valid sink to remain registered")
	}

	// Status should show Ready=False
	updates := su.updatesForResource("sink", "default", "my-sink")
	lastUpdate := updates[len(updates)-1]
	readyCond := findCondition(lastUpdate.conditions, v1alpha1.ConditionReady)
	if readyCond == nil || readyCond.Status != metav1.ConditionFalse {
		t.Error("expected Ready=False after invalid update")
	}
}

func TestSink_Delete(t *testing.T) {
	w, _, _, sr, _ := newTestCRDWatcher(t)
	ctx := context.Background()

	sink := validSink("default", "my-sink")
	w.OnSinkAdd(ctx, sink)
	w.OnSinkDelete("default/my-sink")

	if _, ok := sr.get("default/my-sink"); ok {
		t.Error("expected sink to be unregistered after delete")
	}
}

func TestSink_DeleteNonExistent(t *testing.T) {
	w, _, _, _, _ := newTestCRDWatcher(t)
	// Should not panic
	w.OnSinkDelete("nonexistent/sink")
}

func TestSink_ValidEmptySeverityFilter(t *testing.T) {
	w, _, _, sr, _ := newTestCRDWatcher(t)
	ctx := context.Background()

	sink := validSink("default", "all-sev")
	sink.Spec.SeverityFilter = nil // empty means all severities
	w.OnSinkAdd(ctx, sink)

	if sr.count() != 1 {
		t.Error("expected sink with nil severity filter to be valid")
	}
}

func TestSink_BodyTemplateParsed(t *testing.T) {
	w, _, _, sr, _ := newTestCRDWatcher(t)
	ctx := context.Background()

	tmpl := `{"cause": "{{ .RootCause }}", "severity": "{{ .Severity }}"}`
	sink := validSink("default", "tmpl-sink")
	sink.Spec.Webhook.BodyTemplate = tmpl
	w.OnSinkAdd(ctx, sink)

	reg, ok := sr.get("default/tmpl-sink")
	if !ok {
		t.Fatal("expected sink to be registered")
	}
	if reg.BodyTemplate == nil {
		t.Fatal("expected BodyTemplate to be non-nil")
	}

	// Verify it's a usable template
	var buf = new(testBuffer)
	err := reg.BodyTemplate.Execute(buf, map[string]string{
		"RootCause": "OOM",
		"Severity":  "critical",
	})
	if err != nil {
		t.Errorf("expected template to execute successfully, got: %v", err)
	}
}

// testBuffer is a simple bytes.Buffer replacement that implements io.Writer.
type testBuffer struct {
	data []byte
}

func (b *testBuffer) Write(p []byte) (int, error) {
	b.data = append(b.data, p...)
	return len(p), nil
}

// ==========================================
// Tests: WormsignPolicy lifecycle
// ==========================================

func TestPolicy_AddValid_Exclude(t *testing.T) {
	w, _, _, _, su := newTestCRDWatcher(t)
	ctx := context.Background()

	policy := validPolicy("default", "my-policy")
	w.OnPolicyAdd(ctx, policy)

	updates := su.updatesForResource("policy", "default", "my-policy")
	if len(updates) == 0 {
		t.Fatal("expected status update")
	}
	readyCond := findCondition(updates[len(updates)-1].conditions, v1alpha1.ConditionReady)
	if readyCond == nil || readyCond.Status != metav1.ConditionTrue {
		t.Error("expected Ready=True for valid policy")
	}
	activeCond := findCondition(updates[len(updates)-1].conditions, v1alpha1.ConditionActive)
	if activeCond == nil || activeCond.Status != metav1.ConditionTrue {
		t.Error("expected Active=True for valid policy")
	}
}

func TestPolicy_AddValid_Suppress(t *testing.T) {
	w, _, _, _, su := newTestCRDWatcher(t)
	ctx := context.Background()

	policy := &v1alpha1.WormsignPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "wormsign-system",
			Name:      "maintenance",
		},
		Spec: v1alpha1.WormsignPolicySpec{
			Action: v1alpha1.PolicyActionSuppress,
			Schedule: &v1alpha1.PolicySchedule{
				Start: metav1.NewTime(time.Now().Add(-1 * time.Hour)),
				End:   metav1.NewTime(time.Now().Add(1 * time.Hour)),
			},
		},
	}
	w.OnPolicyAdd(ctx, policy)

	updates := su.updatesForResource("policy", "wormsign-system", "maintenance")
	if len(updates) == 0 {
		t.Fatal("expected status update")
	}
	readyCond := findCondition(updates[len(updates)-1].conditions, v1alpha1.ConditionReady)
	if readyCond == nil || readyCond.Status != metav1.ConditionTrue {
		t.Error("expected Ready=True for valid suppress policy")
	}
}

func TestPolicy_AddInvalid_BadAction(t *testing.T) {
	w, _, _, _, su := newTestCRDWatcher(t)
	ctx := context.Background()

	policy := validPolicy("default", "bad-action")
	policy.Spec.Action = "Block" // invalid
	w.OnPolicyAdd(ctx, policy)

	updates := su.updatesForResource("policy", "default", "bad-action")
	if len(updates) == 0 {
		t.Fatal("expected status update")
	}
	readyCond := findCondition(updates[len(updates)-1].conditions, v1alpha1.ConditionReady)
	if readyCond == nil || readyCond.Status != metav1.ConditionFalse {
		t.Error("expected Ready=False for invalid action")
	}
}

func TestPolicy_AddInvalid_ScheduleOnExclude(t *testing.T) {
	w, _, _, _, su := newTestCRDWatcher(t)
	ctx := context.Background()

	policy := validPolicy("default", "sched-exclude")
	policy.Spec.Schedule = &v1alpha1.PolicySchedule{
		Start: metav1.NewTime(time.Now()),
		End:   metav1.NewTime(time.Now().Add(1 * time.Hour)),
	}
	w.OnPolicyAdd(ctx, policy)

	updates := su.updatesForResource("policy", "default", "sched-exclude")
	if len(updates) == 0 {
		t.Fatal("expected status update")
	}
	readyCond := findCondition(updates[len(updates)-1].conditions, v1alpha1.ConditionReady)
	if readyCond == nil || readyCond.Status != metav1.ConditionFalse {
		t.Error("expected Ready=False for schedule on Exclude action")
	}
}

func TestPolicy_AddInvalid_ScheduleEndBeforeStart(t *testing.T) {
	w, _, _, _, su := newTestCRDWatcher(t)
	ctx := context.Background()

	now := time.Now()
	policy := &v1alpha1.WormsignPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "bad-schedule",
		},
		Spec: v1alpha1.WormsignPolicySpec{
			Action: v1alpha1.PolicyActionSuppress,
			Schedule: &v1alpha1.PolicySchedule{
				Start: metav1.NewTime(now.Add(1 * time.Hour)),
				End:   metav1.NewTime(now), // end before start
			},
		},
	}
	w.OnPolicyAdd(ctx, policy)

	updates := su.updatesForResource("policy", "default", "bad-schedule")
	if len(updates) == 0 {
		t.Fatal("expected status update")
	}
	readyCond := findCondition(updates[len(updates)-1].conditions, v1alpha1.ConditionReady)
	if readyCond == nil || readyCond.Status != metav1.ConditionFalse {
		t.Error("expected Ready=False for end-before-start schedule")
	}
}

func TestPolicy_AddInvalid_EmptyOwnerNamePattern(t *testing.T) {
	w, _, _, _, su := newTestCRDWatcher(t)
	ctx := context.Background()

	policy := validPolicy("default", "empty-owner")
	policy.Spec.Match = &v1alpha1.PolicyMatch{
		OwnerNames: []string{"valid-*", ""}, // empty string
	}
	w.OnPolicyAdd(ctx, policy)

	updates := su.updatesForResource("policy", "default", "empty-owner")
	if len(updates) == 0 {
		t.Fatal("expected status update")
	}
	readyCond := findCondition(updates[len(updates)-1].conditions, v1alpha1.ConditionReady)
	if readyCond == nil || readyCond.Status != metav1.ConditionFalse {
		t.Error("expected Ready=False for empty owner name pattern")
	}
}

func TestPolicy_AddValid_WithDetectors(t *testing.T) {
	w, _, _, _, su := newTestCRDWatcher(t)
	ctx := context.Background()

	policy := validPolicy("default", "det-policy")
	policy.Spec.Detectors = []string{"PodCrashLoop", "PodFailed"}
	w.OnPolicyAdd(ctx, policy)

	updates := su.updatesForResource("policy", "default", "det-policy")
	if len(updates) == 0 {
		t.Fatal("expected status update")
	}
	readyCond := findCondition(updates[len(updates)-1].conditions, v1alpha1.ConditionReady)
	if readyCond == nil || readyCond.Status != metav1.ConditionTrue {
		t.Error("expected Ready=True for valid policy with detectors")
	}
}

func TestPolicy_AddValid_WithNamespaceSelector(t *testing.T) {
	w, _, _, _, su := newTestCRDWatcher(t)
	ctx := context.Background()

	policy := validPolicy("wormsign-system", "ns-selector")
	policy.Spec.NamespaceSelector = &metav1.LabelSelector{
		MatchLabels: map[string]string{
			"environment": "sandbox",
		},
	}
	w.OnPolicyAdd(ctx, policy)

	updates := su.updatesForResource("policy", "wormsign-system", "ns-selector")
	if len(updates) == 0 {
		t.Fatal("expected status update")
	}
	readyCond := findCondition(updates[len(updates)-1].conditions, v1alpha1.ConditionReady)
	if readyCond == nil || readyCond.Status != metav1.ConditionTrue {
		t.Error("expected Ready=True for valid policy with namespace selector")
	}
}

func TestPolicy_AddValid_WithResourceSelector(t *testing.T) {
	w, _, _, _, su := newTestCRDWatcher(t)
	ctx := context.Background()

	policy := validPolicy("default", "res-selector")
	policy.Spec.Match = &v1alpha1.PolicyMatch{
		ResourceSelector: &metav1.LabelSelector{
			MatchLabels: map[string]string{
				"app.kubernetes.io/part-of": "integration-tests",
			},
		},
	}
	w.OnPolicyAdd(ctx, policy)

	updates := su.updatesForResource("policy", "default", "res-selector")
	if len(updates) == 0 {
		t.Fatal("expected status update")
	}
	readyCond := findCondition(updates[len(updates)-1].conditions, v1alpha1.ConditionReady)
	if readyCond == nil || readyCond.Status != metav1.ConditionTrue {
		t.Error("expected Ready=True for valid policy with resource selector")
	}
}

func TestPolicy_AddValid_EmptyMatch(t *testing.T) {
	w, _, _, _, su := newTestCRDWatcher(t)
	ctx := context.Background()

	policy := &v1alpha1.WormsignPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "empty-match",
		},
		Spec: v1alpha1.WormsignPolicySpec{
			Action: v1alpha1.PolicyActionExclude,
			// Match is nil — matches all resources
		},
	}
	w.OnPolicyAdd(ctx, policy)

	updates := su.updatesForResource("policy", "default", "empty-match")
	if len(updates) == 0 {
		t.Fatal("expected status update")
	}
	readyCond := findCondition(updates[len(updates)-1].conditions, v1alpha1.ConditionReady)
	if readyCond == nil || readyCond.Status != metav1.ConditionTrue {
		t.Error("expected Ready=True for policy with empty match")
	}
}

func TestPolicy_UpdateValid(t *testing.T) {
	w, _, _, _, su := newTestCRDWatcher(t)
	ctx := context.Background()

	policy := validPolicy("default", "my-policy")
	w.OnPolicyAdd(ctx, policy)

	policy2 := validPolicy("default", "my-policy")
	policy2.Spec.Detectors = []string{"PodCrashLoop"}
	w.OnPolicyUpdate(ctx, policy2)

	updates := su.updatesForResource("policy", "default", "my-policy")
	// Should have 2 updates (add and update)
	if len(updates) < 2 {
		t.Fatalf("expected at least 2 status updates, got %d", len(updates))
	}
}

func TestPolicy_UpdateInvalid_PreviousRemains(t *testing.T) {
	w, _, _, _, su := newTestCRDWatcher(t)
	ctx := context.Background()

	policy := validPolicy("default", "my-policy")
	w.OnPolicyAdd(ctx, policy)

	// Update with invalid action
	policy2 := validPolicy("default", "my-policy")
	policy2.Spec.Action = "Invalid"
	w.OnPolicyUpdate(ctx, policy2)

	updates := su.updatesForResource("policy", "default", "my-policy")
	lastUpdate := updates[len(updates)-1]
	readyCond := findCondition(lastUpdate.conditions, v1alpha1.ConditionReady)
	if readyCond == nil || readyCond.Status != metav1.ConditionFalse {
		t.Error("expected Ready=False after invalid update")
	}
	activeCond := findCondition(lastUpdate.conditions, v1alpha1.ConditionActive)
	if activeCond == nil || activeCond.Status != metav1.ConditionTrue {
		t.Error("expected Active=True (previous version) after invalid update")
	}
}

func TestPolicy_Delete(t *testing.T) {
	w, _, _, _, su := newTestCRDWatcher(t)
	ctx := context.Background()

	policy := validPolicy("default", "my-policy")
	w.OnPolicyAdd(ctx, policy)

	initialUpdates := su.updateCount()

	w.OnPolicyDelete("default/my-policy")

	// Internal tracking should be cleared
	w.mu.RLock()
	_, stillValid := w.validPolicies["default/my-policy"]
	_, stillInStore := w.policyStore["default/my-policy"]
	w.mu.RUnlock()

	if stillValid {
		t.Error("expected policy to be removed from validPolicies")
	}
	if stillInStore {
		t.Error("expected policy to be removed from policyStore")
	}

	// No additional status update needed for delete, just log
	_ = initialUpdates
}

func TestPolicy_DeleteNonExistent(t *testing.T) {
	w, _, _, _, _ := newTestCRDWatcher(t)
	// Should not panic
	w.OnPolicyDelete("nonexistent/policy")
}

// ==========================================
// Tests: Policy → filter engine integration
// ==========================================

func TestPolicy_RebuildPolicies_SinglePolicy(t *testing.T) {
	w, _, _, _, _ := newTestCRDWatcher(t)
	ctx := context.Background()

	policy := validPolicy("default", "test-policy")
	policy.Spec.Action = v1alpha1.PolicyActionExclude
	policy.Spec.Match = &v1alpha1.PolicyMatch{
		OwnerNames: []string{"load-test-*"},
	}
	w.OnPolicyAdd(ctx, policy)

	// Verify the filter engine received the policy by checking internal state
	w.mu.RLock()
	validCount := 0
	for _, v := range w.validPolicies {
		if v {
			validCount++
		}
	}
	w.mu.RUnlock()

	if validCount != 1 {
		t.Errorf("expected 1 valid policy, got %d", validCount)
	}
}

func TestPolicy_RebuildPolicies_MultiplePolicies(t *testing.T) {
	w, _, _, _, _ := newTestCRDWatcher(t)
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		policy := validPolicy("default", fmt.Sprintf("policy-%d", i))
		w.OnPolicyAdd(ctx, policy)
	}

	w.mu.RLock()
	storeCount := len(w.policyStore)
	w.mu.RUnlock()

	if storeCount != 3 {
		t.Errorf("expected 3 policies in store, got %d", storeCount)
	}
}

func TestPolicy_RebuildAfterDelete(t *testing.T) {
	w, _, _, _, _ := newTestCRDWatcher(t)
	ctx := context.Background()

	p1 := validPolicy("default", "policy-1")
	p2 := validPolicy("default", "policy-2")
	w.OnPolicyAdd(ctx, p1)
	w.OnPolicyAdd(ctx, p2)

	w.OnPolicyDelete("default/policy-1")

	w.mu.RLock()
	storeCount := len(w.policyStore)
	w.mu.RUnlock()

	if storeCount != 1 {
		t.Errorf("expected 1 policy after delete, got %d", storeCount)
	}
}

// ==========================================
// Tests: convertToFilterPolicy
// ==========================================

func TestConvertToFilterPolicy_Basic(t *testing.T) {
	policy := &v1alpha1.WormsignPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "test-policy",
		},
		Spec: v1alpha1.WormsignPolicySpec{
			Action:    v1alpha1.PolicyActionExclude,
			Detectors: []string{"PodCrashLoop"},
		},
	}

	fp := convertToFilterPolicy(policy)
	if fp.Name != "test-policy" {
		t.Errorf("expected Name='test-policy', got %q", fp.Name)
	}
	if fp.Namespace != "default" {
		t.Errorf("expected Namespace='default', got %q", fp.Namespace)
	}
	if fp.Action != filter.PolicyActionExclude {
		t.Errorf("expected Action='Exclude', got %q", fp.Action)
	}
	if len(fp.Detectors) != 1 || fp.Detectors[0] != "PodCrashLoop" {
		t.Errorf("unexpected Detectors: %v", fp.Detectors)
	}
}

func TestConvertToFilterPolicy_WithNamespaceSelector(t *testing.T) {
	policy := &v1alpha1.WormsignPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "wormsign-system",
			Name:      "ns-select",
		},
		Spec: v1alpha1.WormsignPolicySpec{
			Action: v1alpha1.PolicyActionExclude,
			NamespaceSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{
					"env": "sandbox",
				},
			},
		},
	}

	fp := convertToFilterPolicy(policy)
	if fp.NamespaceSelector == nil {
		t.Fatal("expected NamespaceSelector to be non-nil")
	}
	if fp.NamespaceSelector.MatchLabels["env"] != "sandbox" {
		t.Errorf("expected MatchLabels[env]='sandbox', got %q", fp.NamespaceSelector.MatchLabels["env"])
	}
}

func TestConvertToFilterPolicy_WithResourceSelector(t *testing.T) {
	policy := &v1alpha1.WormsignPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "res-select",
		},
		Spec: v1alpha1.WormsignPolicySpec{
			Action: v1alpha1.PolicyActionExclude,
			Match: &v1alpha1.PolicyMatch{
				ResourceSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{
						"app": "test",
					},
					MatchExpressions: []metav1.LabelSelectorRequirement{
						{
							Key:      "team",
							Operator: metav1.LabelSelectorOpIn,
							Values:   []string{"qa", "dev"},
						},
					},
				},
			},
		},
	}

	fp := convertToFilterPolicy(policy)
	if fp.ResourceSelector == nil {
		t.Fatal("expected ResourceSelector to be non-nil")
	}
	if fp.ResourceSelector.MatchLabels["app"] != "test" {
		t.Errorf("expected MatchLabels[app]='test', got %q", fp.ResourceSelector.MatchLabels["app"])
	}
	if len(fp.ResourceSelector.MatchExpressions) != 1 {
		t.Fatalf("expected 1 match expression, got %d", len(fp.ResourceSelector.MatchExpressions))
	}
	expr := fp.ResourceSelector.MatchExpressions[0]
	if expr.Key != "team" {
		t.Errorf("expected Key='team', got %q", expr.Key)
	}
	if expr.Operator != filter.SelectorOpIn {
		t.Errorf("expected Operator='In', got %q", expr.Operator)
	}
	if len(expr.Values) != 2 {
		t.Errorf("expected 2 values, got %d", len(expr.Values))
	}
}

func TestConvertToFilterPolicy_WithSchedule(t *testing.T) {
	now := time.Now()
	policy := &v1alpha1.WormsignPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "scheduled",
		},
		Spec: v1alpha1.WormsignPolicySpec{
			Action: v1alpha1.PolicyActionSuppress,
			Schedule: &v1alpha1.PolicySchedule{
				Start: metav1.NewTime(now),
				End:   metav1.NewTime(now.Add(2 * time.Hour)),
			},
		},
	}

	fp := convertToFilterPolicy(policy)
	if fp.Schedule == nil {
		t.Fatal("expected Schedule to be non-nil")
	}
	if !fp.Schedule.Start.Equal(now) {
		t.Errorf("expected Start=%v, got %v", now, fp.Schedule.Start)
	}
	if !fp.Schedule.End.Equal(now.Add(2 * time.Hour)) {
		t.Errorf("expected End=%v, got %v", now.Add(2*time.Hour), fp.Schedule.End)
	}
}

func TestConvertToFilterPolicy_WithOwnerNames(t *testing.T) {
	policy := &v1alpha1.WormsignPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "owner-names",
		},
		Spec: v1alpha1.WormsignPolicySpec{
			Action: v1alpha1.PolicyActionExclude,
			Match: &v1alpha1.PolicyMatch{
				OwnerNames: []string{"canary-*", "load-test-*"},
			},
		},
	}

	fp := convertToFilterPolicy(policy)
	if len(fp.OwnerNames) != 2 {
		t.Fatalf("expected 2 owner names, got %d", len(fp.OwnerNames))
	}
	if fp.OwnerNames[0] != "canary-*" {
		t.Errorf("expected first owner name 'canary-*', got %q", fp.OwnerNames[0])
	}
}

func TestConvertToFilterPolicy_NilMatch(t *testing.T) {
	policy := &v1alpha1.WormsignPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "nil-match",
		},
		Spec: v1alpha1.WormsignPolicySpec{
			Action: v1alpha1.PolicyActionExclude,
			// Match is nil
		},
	}

	fp := convertToFilterPolicy(policy)
	if fp.ResourceSelector != nil {
		t.Error("expected ResourceSelector to be nil when Match is nil")
	}
	if len(fp.OwnerNames) != 0 {
		t.Error("expected OwnerNames to be empty when Match is nil")
	}
}

func TestConvertLabelSelector_Nil(t *testing.T) {
	result := convertLabelSelector(nil)
	if result != nil {
		t.Error("expected nil result for nil input")
	}
}

func TestConvertLabelSelector_Empty(t *testing.T) {
	result := convertLabelSelector(&metav1.LabelSelector{})
	if result == nil {
		t.Fatal("expected non-nil result for empty selector")
	}
	if len(result.MatchLabels) != 0 {
		t.Errorf("expected empty MatchLabels, got %d", len(result.MatchLabels))
	}
}

func TestConvertLabelSelector_Full(t *testing.T) {
	selector := &metav1.LabelSelector{
		MatchLabels: map[string]string{
			"app": "web",
		},
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{
				Key:      "env",
				Operator: metav1.LabelSelectorOpNotIn,
				Values:   []string{"prod"},
			},
			{
				Key:      "feature-flag",
				Operator: metav1.LabelSelectorOpExists,
			},
			{
				Key:      "deprecated",
				Operator: metav1.LabelSelectorOpDoesNotExist,
			},
		},
	}

	result := convertLabelSelector(selector)
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if result.MatchLabels["app"] != "web" {
		t.Errorf("expected MatchLabels[app]='web', got %q", result.MatchLabels["app"])
	}
	if len(result.MatchExpressions) != 3 {
		t.Fatalf("expected 3 expressions, got %d", len(result.MatchExpressions))
	}
	if result.MatchExpressions[0].Operator != filter.SelectorOpNotIn {
		t.Errorf("expected NotIn, got %q", result.MatchExpressions[0].Operator)
	}
	if result.MatchExpressions[1].Operator != filter.SelectorOpExists {
		t.Errorf("expected Exists, got %q", result.MatchExpressions[1].Operator)
	}
	if result.MatchExpressions[2].Operator != filter.SelectorOpDoesNotExist {
		t.Errorf("expected DoesNotExist, got %q", result.MatchExpressions[2].Operator)
	}
}

// ==========================================
// Tests: setCondition helper
// ==========================================

func TestSetCondition_NewCondition(t *testing.T) {
	var conditions []metav1.Condition
	setCondition(&conditions, "Ready", metav1.ConditionTrue, "Valid", "All good")

	if len(conditions) != 1 {
		t.Fatalf("expected 1 condition, got %d", len(conditions))
	}
	if conditions[0].Type != "Ready" {
		t.Errorf("expected type 'Ready', got %q", conditions[0].Type)
	}
	if conditions[0].Status != metav1.ConditionTrue {
		t.Errorf("expected status True, got %s", conditions[0].Status)
	}
	if conditions[0].Reason != "Valid" {
		t.Errorf("expected reason 'Valid', got %q", conditions[0].Reason)
	}
	if conditions[0].Message != "All good" {
		t.Errorf("expected message 'All good', got %q", conditions[0].Message)
	}
	if conditions[0].LastTransitionTime.IsZero() {
		t.Error("expected LastTransitionTime to be set")
	}
}

func TestSetCondition_UpdateExisting(t *testing.T) {
	conditions := []metav1.Condition{
		{
			Type:    "Ready",
			Status:  metav1.ConditionTrue,
			Reason:  "Valid",
			Message: "OK",
		},
		{
			Type:    "Active",
			Status:  metav1.ConditionTrue,
			Reason:  "Running",
			Message: "Active",
		},
	}

	setCondition(&conditions, "Ready", metav1.ConditionFalse, "Failed", "Something broke")

	if len(conditions) != 2 {
		t.Fatalf("expected 2 conditions, got %d", len(conditions))
	}

	ready := findCondition(conditions, "Ready")
	if ready == nil {
		t.Fatal("expected Ready condition")
	}
	if ready.Status != metav1.ConditionFalse {
		t.Errorf("expected status False, got %s", ready.Status)
	}
	if ready.Reason != "Failed" {
		t.Errorf("expected reason 'Failed', got %q", ready.Reason)
	}
	if ready.Message != "Something broke" {
		t.Errorf("expected message 'Something broke', got %q", ready.Message)
	}

	// Active should be unchanged
	active := findCondition(conditions, "Active")
	if active == nil {
		t.Fatal("expected Active condition")
	}
	if active.Status != metav1.ConditionTrue {
		t.Errorf("expected Active to remain True, got %s", active.Status)
	}
}

func TestSetCondition_AddSecondCondition(t *testing.T) {
	var conditions []metav1.Condition
	setCondition(&conditions, "Ready", metav1.ConditionTrue, "Valid", "OK")
	setCondition(&conditions, "Active", metav1.ConditionTrue, "Running", "Active")

	if len(conditions) != 2 {
		t.Fatalf("expected 2 conditions, got %d", len(conditions))
	}
}

// ==========================================
// Tests: validateCollectAction
// ==========================================

func TestValidateCollectAction_ValidResource(t *testing.T) {
	action := v1alpha1.CollectAction{
		Type:       v1alpha1.CollectTypeResource,
		APIVersion: "v1",
		Resource:   "pods",
		Name:       "my-pod",
	}
	if err := validateCollectAction(action); err != nil {
		t.Errorf("expected valid, got: %v", err)
	}
}

func TestValidateCollectAction_ValidResourceListAll(t *testing.T) {
	action := v1alpha1.CollectAction{
		Type:       v1alpha1.CollectTypeResource,
		APIVersion: "networking.istio.io/v1",
		Resource:   "destinationrules",
		ListAll:    true,
	}
	if err := validateCollectAction(action); err != nil {
		t.Errorf("expected valid for listAll, got: %v", err)
	}
}

func TestValidateCollectAction_ValidLogs(t *testing.T) {
	action := v1alpha1.CollectAction{
		Type:      v1alpha1.CollectTypeLogs,
		Container: "app",
		TailLines: int64Ptr(100),
	}
	if err := validateCollectAction(action); err != nil {
		t.Errorf("expected valid, got: %v", err)
	}
}

func TestValidateCollectAction_ValidLogsMinimal(t *testing.T) {
	action := v1alpha1.CollectAction{
		Type: v1alpha1.CollectTypeLogs,
	}
	if err := validateCollectAction(action); err != nil {
		t.Errorf("expected valid for minimal logs, got: %v", err)
	}
}

func TestValidateCollectAction_ResourceMissingAPIVersion(t *testing.T) {
	action := v1alpha1.CollectAction{
		Type:     v1alpha1.CollectTypeResource,
		Resource: "pods",
		Name:     "my-pod",
	}
	if err := validateCollectAction(action); err == nil {
		t.Error("expected error for missing apiVersion")
	}
}

func TestValidateCollectAction_ResourceMissingResource(t *testing.T) {
	action := v1alpha1.CollectAction{
		Type:       v1alpha1.CollectTypeResource,
		APIVersion: "v1",
		Name:       "my-pod",
	}
	if err := validateCollectAction(action); err == nil {
		t.Error("expected error for missing resource")
	}
}

func TestValidateCollectAction_ResourceMissingNameNoListAll(t *testing.T) {
	action := v1alpha1.CollectAction{
		Type:       v1alpha1.CollectTypeResource,
		APIVersion: "v1",
		Resource:   "pods",
	}
	if err := validateCollectAction(action); err == nil {
		t.Error("expected error for missing name when listAll is false")
	}
}

func TestValidateCollectAction_UnknownType(t *testing.T) {
	action := v1alpha1.CollectAction{
		Type: "http",
	}
	if err := validateCollectAction(action); err == nil {
		t.Error("expected error for unknown type")
	}
}

// ==========================================
// Tests: Status updater error handling
// ==========================================

func TestDetector_StatusUpdateError_DoesNotPanic(t *testing.T) {
	w, _, _, _, su := newTestCRDWatcher(t)
	ctx := context.Background()
	su.err = fmt.Errorf("simulated API error")

	det := validDetector("default", "my-detector")
	// Should not panic even when status update fails
	w.OnDetectorAdd(ctx, det)
}

func TestGatherer_StatusUpdateError_DoesNotPanic(t *testing.T) {
	w, _, _, _, su := newTestCRDWatcher(t)
	ctx := context.Background()
	su.err = fmt.Errorf("simulated API error")

	gath := validGatherer("default", "my-gatherer")
	// Should not panic
	w.OnGathererAdd(ctx, gath)
}

func TestSink_StatusUpdateError_DoesNotPanic(t *testing.T) {
	w, _, _, _, su := newTestCRDWatcher(t)
	ctx := context.Background()
	su.err = fmt.Errorf("simulated API error")

	sink := validSink("default", "my-sink")
	// Should not panic
	w.OnSinkAdd(ctx, sink)
}

func TestPolicy_StatusUpdateError_DoesNotPanic(t *testing.T) {
	w, _, _, _, su := newTestCRDWatcher(t)
	ctx := context.Background()
	su.err = fmt.Errorf("simulated API error")

	policy := validPolicy("default", "my-policy")
	// Should not panic
	w.OnPolicyAdd(ctx, policy)
}

// ==========================================
// Tests: Cross-type lifecycle (integration)
// ==========================================

func TestMultipleResourceTypes_IndependentLifecycles(t *testing.T) {
	w, dr, gr, sr, _ := newTestCRDWatcher(t)
	ctx := context.Background()

	// Add one of each
	w.OnDetectorAdd(ctx, validDetector("default", "det-1"))
	w.OnGathererAdd(ctx, validGatherer("default", "gath-1"))
	w.OnSinkAdd(ctx, validSink("default", "sink-1"))
	w.OnPolicyAdd(ctx, validPolicy("default", "pol-1"))

	if dr.count() != 1 {
		t.Errorf("expected 1 detector, got %d", dr.count())
	}
	if gr.count() != 1 {
		t.Errorf("expected 1 gatherer, got %d", gr.count())
	}
	if sr.count() != 1 {
		t.Errorf("expected 1 sink, got %d", sr.count())
	}

	// Delete detector, others should remain
	w.OnDetectorDelete("default/det-1")
	if dr.count() != 0 {
		t.Error("expected 0 detectors after delete")
	}
	if gr.count() != 1 {
		t.Error("expected gatherer to remain after detector delete")
	}
	if sr.count() != 1 {
		t.Error("expected sink to remain after detector delete")
	}
}

func TestMultipleDetectors_SameNamespace(t *testing.T) {
	w, dr, _, _, _ := newTestCRDWatcher(t)
	ctx := context.Background()

	for i := 0; i < 5; i++ {
		det := validDetector("default", fmt.Sprintf("detector-%d", i))
		w.OnDetectorAdd(ctx, det)
	}

	if dr.count() != 5 {
		t.Errorf("expected 5 detectors, got %d", dr.count())
	}

	// Delete one
	w.OnDetectorDelete("default/detector-2")
	if dr.count() != 4 {
		t.Errorf("expected 4 detectors after delete, got %d", dr.count())
	}
}

func TestMultipleDetectors_DifferentNamespaces(t *testing.T) {
	w, dr, _, _, _ := newTestCRDWatcher(t)
	ctx := context.Background()

	namespaces := []string{"default", "payments", "orders", "shipping"}
	for _, ns := range namespaces {
		det := validDetector(ns, "restart-detector")
		w.OnDetectorAdd(ctx, det)
	}

	if dr.count() != 4 {
		t.Errorf("expected 4 detectors in different namespaces, got %d", dr.count())
	}
}

// ==========================================
// Tests: Template parsing edge cases
// ==========================================

func TestSink_ComplexBodyTemplate(t *testing.T) {
	w, _, _, sr, _ := newTestCRDWatcher(t)
	ctx := context.Background()

	complexTemplate := `{
		"@type": "MessageCard",
		"themeColor": "{{ if eq .Severity "critical" }}FF0000{{ else }}FFA500{{ end }}",
		"summary": "K8s Wormsign: {{ .RootCause }}",
		"sections": [{
			"activityTitle": "{{ .Resource.Kind }}/{{ .Resource.Name }}"
		}]
	}`

	sink := validSink("default", "complex-tmpl")
	sink.Spec.Webhook.BodyTemplate = complexTemplate
	w.OnSinkAdd(ctx, sink)

	if sr.count() != 1 {
		t.Error("expected complex template to be valid")
	}
}

func TestSink_BodyTemplateWithActions(t *testing.T) {
	w, _, _, sr, _ := newTestCRDWatcher(t)
	ctx := context.Background()

	tmpl := `{{ range .Remediation }}{{ . }}{{ end }}`
	sink := validSink("default", "range-tmpl")
	sink.Spec.Webhook.BodyTemplate = tmpl
	w.OnSinkAdd(ctx, sink)

	if sr.count() != 1 {
		t.Error("expected template with range action to be valid")
	}
}

func TestSink_EmptyBodyTemplate(t *testing.T) {
	w, _, _, sr, _ := newTestCRDWatcher(t)
	ctx := context.Background()

	sink := validSink("default", "empty-tmpl")
	sink.Spec.Webhook.BodyTemplate = ""
	w.OnSinkAdd(ctx, sink)

	if sr.count() != 1 {
		t.Error("expected sink with empty body template to be valid")
	}
	reg, _ := sr.get("default/empty-tmpl")
	if reg.BodyTemplate != nil {
		t.Error("expected nil BodyTemplate for empty string")
	}
}

// ==========================================
// Tests: Concurrent access safety
// ==========================================

func TestConcurrent_AddAndDelete(t *testing.T) {
	w, _, _, _, _ := newTestCRDWatcher(t)
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			det := validDetector("default", fmt.Sprintf("concurrent-det-%d", idx))
			w.OnDetectorAdd(ctx, det)
			w.OnDetectorDelete(fmt.Sprintf("default/concurrent-det-%d", idx))
		}(i)
	}
	wg.Wait()
	// No panics = success
}

func TestConcurrent_PolicyAddAndDelete(t *testing.T) {
	w, _, _, _, _ := newTestCRDWatcher(t)
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			policy := validPolicy("default", fmt.Sprintf("concurrent-pol-%d", idx))
			w.OnPolicyAdd(ctx, policy)
			w.OnPolicyDelete(fmt.Sprintf("default/concurrent-pol-%d", idx))
		}(i)
	}
	wg.Wait()
}

func TestConcurrent_MixedOperations(t *testing.T) {
	w, _, _, _, _ := newTestCRDWatcher(t)
	ctx := context.Background()

	var wg sync.WaitGroup
	// Detectors
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			det := validDetector("default", fmt.Sprintf("mix-det-%d", idx))
			w.OnDetectorAdd(ctx, det)
		}(i)
	}
	// Gatherers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			gath := validGatherer("default", fmt.Sprintf("mix-gath-%d", idx))
			w.OnGathererAdd(ctx, gath)
		}(i)
	}
	// Sinks
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sink := validSink("default", fmt.Sprintf("mix-sink-%d", idx))
			w.OnSinkAdd(ctx, sink)
		}(i)
	}
	// Policies
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			policy := validPolicy("default", fmt.Sprintf("mix-pol-%d", idx))
			w.OnPolicyAdd(ctx, policy)
		}(i)
	}
	wg.Wait()
}

// --- helpers ---

func int64Ptr(v int64) *int64 {
	return &v
}

// Verify template.Template is usable after parsing
func TestRegisteredSink_TemplateUsable(t *testing.T) {
	tmpl, err := template.New("test").Parse(`Hello {{ .Name }}`)
	if err != nil {
		t.Fatalf("failed to parse template: %v", err)
	}
	reg := RegisteredSink{
		BodyTemplate: tmpl,
	}
	if reg.BodyTemplate == nil {
		t.Fatal("expected non-nil template")
	}
}
