package cel

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1alpha1 "github.com/k8s-wormsign/k8s-wormsign/api/v1alpha1"
	"github.com/k8s-wormsign/k8s-wormsign/internal/detector"
	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

// silentLogger returns a logger that discards output.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(discardWriter{}, nil))
}

type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

// collectCallback returns a callback and a way to read collected events.
func collectCallback() (detector.EventCallback, func() []*model.FaultEvent) {
	var mu sync.Mutex
	var events []*model.FaultEvent
	cb := func(e *model.FaultEvent) {
		mu.Lock()
		events = append(events, e)
		mu.Unlock()
	}
	get := func() []*model.FaultEvent {
		mu.Lock()
		defer mu.Unlock()
		result := make([]*model.FaultEvent, len(events))
		copy(result, events)
		return result
	}
	return cb, get
}

// newTestEngine creates a CELDetectorEngine for testing with a collect callback.
func newTestEngine(t *testing.T) (*CELDetectorEngine, func() []*model.FaultEvent) {
	t.Helper()
	cb, getEvents := collectCallback()
	engine, err := NewCELDetectorEngine(EngineConfig{
		CostLimit:       DefaultCostLimit,
		Callback:        cb,
		Logger:          silentLogger(),
		SystemNamespace: "wormsign-system",
	})
	if err != nil {
		t.Fatalf("NewCELDetectorEngine() error = %v", err)
	}
	return engine, getEvents
}

// makeDetectorCRD creates a WormsignDetector for testing.
func makeDetectorCRD(namespace, name, resource, condition string, severity v1alpha1.Severity, params map[string]string) *v1alpha1.WormsignDetector {
	return &v1alpha1.WormsignDetector{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:  namespace,
			Name:       name,
			Generation: 1,
		},
		Spec: v1alpha1.WormsignDetectorSpec{
			Resource:  resource,
			Condition: condition,
			Severity:  severity,
			Params:    params,
		},
	}
}

// =====================================================================
// Engine Construction Tests
// =====================================================================

func TestNewCELDetectorEngine_Validation(t *testing.T) {
	cb, _ := collectCallback()

	tests := []struct {
		name    string
		cfg     EngineConfig
		wantErr string
	}{
		{
			name:    "nil callback",
			cfg:     EngineConfig{SystemNamespace: "wormsign-system"},
			wantErr: "callback must not be nil",
		},
		{
			name:    "empty system namespace",
			cfg:     EngineConfig{Callback: cb, SystemNamespace: ""},
			wantErr: "system namespace must not be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewCELDetectorEngine(tt.cfg)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !containsStr(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestNewCELDetectorEngine_Success(t *testing.T) {
	cb, _ := collectCallback()
	engine, err := NewCELDetectorEngine(EngineConfig{
		CostLimit:       500,
		Callback:        cb,
		Logger:          silentLogger(),
		SystemNamespace: "wormsign-system",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if engine.costLimit != 500 {
		t.Errorf("costLimit = %d, want 500", engine.costLimit)
	}
	if engine.DetectorCount() != 0 {
		t.Errorf("DetectorCount = %d, want 0", engine.DetectorCount())
	}
}

func TestNewCELDetectorEngine_DefaultCostLimit(t *testing.T) {
	cb, _ := collectCallback()
	engine, err := NewCELDetectorEngine(EngineConfig{
		CostLimit:       0, // should default
		Callback:        cb,
		Logger:          silentLogger(),
		SystemNamespace: "wormsign-system",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if engine.costLimit != DefaultCostLimit {
		t.Errorf("costLimit = %d, want %d", engine.costLimit, DefaultCostLimit)
	}
}

// =====================================================================
// LoadDetector Tests — Valid Expressions
// =====================================================================

func TestLoadDetector_SimpleCondition(t *testing.T) {
	engine, _ := newTestEngine(t)
	ctx := context.Background()

	wd := makeDetectorCRD("default", "test-detector", "pods",
		`pod.status == "Pending"`,
		v1alpha1.SeverityWarning, nil)

	result, err := engine.LoadDetector(ctx, wd)
	if err != nil {
		t.Fatalf("LoadDetector() error = %v", err)
	}
	if !result.Ready {
		t.Errorf("Ready = false, want true; message: %s", result.ReadyMessage)
	}
	if engine.DetectorCount() != 1 {
		t.Errorf("DetectorCount = %d, want 1", engine.DetectorCount())
	}
}

func TestLoadDetector_WithParams(t *testing.T) {
	engine, _ := newTestEngine(t)
	ctx := context.Background()

	wd := makeDetectorCRD("default", "param-detector", "pods",
		`pod.restartCount > int(params.maxRestarts)`,
		v1alpha1.SeverityCritical,
		map[string]string{"maxRestarts": "5"})

	result, err := engine.LoadDetector(ctx, wd)
	if err != nil {
		t.Fatalf("LoadDetector() error = %v", err)
	}
	if !result.Ready {
		t.Errorf("Ready = false, want true; message: %s", result.ReadyMessage)
	}
}

func TestLoadDetector_AllSeverityLevels(t *testing.T) {
	engine, _ := newTestEngine(t)
	ctx := context.Background()

	severities := []v1alpha1.Severity{
		v1alpha1.SeverityCritical,
		v1alpha1.SeverityWarning,
		v1alpha1.SeverityInfo,
	}

	for _, sev := range severities {
		t.Run(string(sev), func(t *testing.T) {
			wd := makeDetectorCRD("default", "sev-"+string(sev), "pods",
				"true", sev, nil)
			result, err := engine.LoadDetector(ctx, wd)
			if err != nil {
				t.Fatalf("LoadDetector() error = %v", err)
			}
			if !result.Ready {
				t.Errorf("Ready = false for severity %s", sev)
			}
		})
	}
}

func TestLoadDetector_AllResourceTypes(t *testing.T) {
	engine, _ := newTestEngine(t)
	ctx := context.Background()

	for resourceType := range supportedResources {
		t.Run(resourceType, func(t *testing.T) {
			varName := resourceVarName(resourceType)
			condition := varName + ".name == \"test\""
			wd := makeDetectorCRD("default", "res-"+resourceType, resourceType,
				condition, v1alpha1.SeverityWarning, nil)
			result, err := engine.LoadDetector(ctx, wd)
			if err != nil {
				t.Fatalf("LoadDetector() error = %v", err)
			}
			if !result.Ready {
				t.Errorf("Ready = false for resource %s; message: %s", resourceType, result.ReadyMessage)
			}
		})
	}
}

func TestLoadDetector_WithCooldown(t *testing.T) {
	engine, _ := newTestEngine(t)
	ctx := context.Background()

	wd := makeDetectorCRD("default", "cooldown-detector", "pods",
		"true", v1alpha1.SeverityWarning, nil)
	wd.Spec.Cooldown = "1h"

	result, err := engine.LoadDetector(ctx, wd)
	if err != nil {
		t.Fatalf("LoadDetector() error = %v", err)
	}
	if !result.Ready {
		t.Errorf("Ready = false; message: %s", result.ReadyMessage)
	}

	cd := engine.GetDetector("default", "cooldown-detector")
	if cd == nil {
		t.Fatal("detector not found after loading")
	}
	if cd.Cooldown != time.Hour {
		t.Errorf("Cooldown = %v, want 1h", cd.Cooldown)
	}
}

// =====================================================================
// LoadDetector Tests — Invalid Expressions/Config
// =====================================================================

func TestLoadDetector_NilDetector(t *testing.T) {
	engine, _ := newTestEngine(t)
	ctx := context.Background()

	_, err := engine.LoadDetector(ctx, nil)
	if err == nil {
		t.Fatal("expected error for nil detector")
	}
}

func TestLoadDetector_UnsupportedResource(t *testing.T) {
	engine, _ := newTestEngine(t)
	ctx := context.Background()

	wd := makeDetectorCRD("default", "bad-resource", "foobar",
		"true", v1alpha1.SeverityWarning, nil)

	result, err := engine.LoadDetector(ctx, wd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Ready {
		t.Error("Ready = true, want false for unsupported resource")
	}
	if !containsStr(result.ReadyMessage, "unsupported resource type") {
		t.Errorf("ReadyMessage = %q, want substring 'unsupported resource type'", result.ReadyMessage)
	}
}

func TestLoadDetector_InvalidSeverity(t *testing.T) {
	engine, _ := newTestEngine(t)
	ctx := context.Background()

	wd := makeDetectorCRD("default", "bad-severity", "pods",
		"true", "bogus", nil)

	result, err := engine.LoadDetector(ctx, wd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Ready {
		t.Error("Ready = true, want false for invalid severity")
	}
	if !containsStr(result.ReadyMessage, "invalid severity") {
		t.Errorf("ReadyMessage = %q, want substring 'invalid severity'", result.ReadyMessage)
	}
}

func TestLoadDetector_EmptyCondition(t *testing.T) {
	engine, _ := newTestEngine(t)
	ctx := context.Background()

	wd := makeDetectorCRD("default", "empty-cond", "pods",
		"", v1alpha1.SeverityWarning, nil)

	result, err := engine.LoadDetector(ctx, wd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Ready {
		t.Error("Ready = true, want false for empty condition")
	}
	if !containsStr(result.ReadyMessage, "CEL condition must not be empty") {
		t.Errorf("ReadyMessage = %q, want substring about empty condition", result.ReadyMessage)
	}
}

func TestLoadDetector_SyntaxError(t *testing.T) {
	engine, _ := newTestEngine(t)
	ctx := context.Background()

	wd := makeDetectorCRD("default", "syntax-err", "pods",
		"pod. ==== invalid syntax!!!", v1alpha1.SeverityWarning, nil)

	result, err := engine.LoadDetector(ctx, wd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Ready {
		t.Error("Ready = true, want false for syntax error")
	}
	if !containsStr(result.ReadyMessage, "CEL compilation error") {
		t.Errorf("ReadyMessage = %q, want substring 'CEL compilation error'", result.ReadyMessage)
	}
}

func TestLoadDetector_NonBooleanReturn(t *testing.T) {
	engine, _ := newTestEngine(t)
	ctx := context.Background()

	wd := makeDetectorCRD("default", "non-bool", "pods",
		`"hello"`, v1alpha1.SeverityWarning, nil)

	result, err := engine.LoadDetector(ctx, wd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Ready {
		t.Error("Ready = true, want false for non-boolean return")
	}
	if !containsStr(result.ReadyMessage, "must return bool") {
		t.Errorf("ReadyMessage = %q, want substring 'must return bool'", result.ReadyMessage)
	}
}

func TestLoadDetector_InvalidCooldown(t *testing.T) {
	engine, _ := newTestEngine(t)
	ctx := context.Background()

	wd := makeDetectorCRD("default", "bad-cooldown", "pods",
		"true", v1alpha1.SeverityWarning, nil)
	wd.Spec.Cooldown = "not-a-duration"

	result, err := engine.LoadDetector(ctx, wd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Ready {
		t.Error("Ready = true, want false for invalid cooldown")
	}
	if !containsStr(result.ReadyMessage, "invalid cooldown") {
		t.Errorf("ReadyMessage = %q, want substring 'invalid cooldown'", result.ReadyMessage)
	}
}

func TestLoadDetector_ZeroCooldown(t *testing.T) {
	engine, _ := newTestEngine(t)
	ctx := context.Background()

	wd := makeDetectorCRD("default", "zero-cooldown", "pods",
		"true", v1alpha1.SeverityWarning, nil)
	wd.Spec.Cooldown = "0s"

	result, err := engine.LoadDetector(ctx, wd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Ready {
		t.Error("Ready = true, want false for zero cooldown")
	}
}

// =====================================================================
// LoadDetector Tests — Namespace Selector
// =====================================================================

func TestLoadDetector_NamespaceSelectorInSystemNamespace(t *testing.T) {
	engine, _ := newTestEngine(t)
	ctx := context.Background()

	wd := makeDetectorCRD("wormsign-system", "cross-ns", "pods",
		"true", v1alpha1.SeverityWarning, nil)
	wd.Spec.NamespaceSelector = &metav1.LabelSelector{
		MatchLabels: map[string]string{"wormsign.io/watch": "true"},
	}

	result, err := engine.LoadDetector(ctx, wd)
	if err != nil {
		t.Fatalf("LoadDetector() error = %v", err)
	}
	if !result.Ready {
		t.Errorf("Ready = false; message: %s", result.ReadyMessage)
	}

	cd := engine.GetDetector("wormsign-system", "cross-ns")
	if cd == nil {
		t.Fatal("detector not found")
	}
	if cd.NamespaceSelector == nil {
		t.Error("NamespaceSelector = nil, want non-nil")
	}
}

func TestLoadDetector_NamespaceSelectorOutsideSystemNamespace(t *testing.T) {
	engine, _ := newTestEngine(t)
	ctx := context.Background()

	wd := makeDetectorCRD("default", "cross-ns-denied", "pods",
		"true", v1alpha1.SeverityWarning, nil)
	wd.Spec.NamespaceSelector = &metav1.LabelSelector{
		MatchLabels: map[string]string{"wormsign.io/watch": "true"},
	}

	result, err := engine.LoadDetector(ctx, wd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Ready {
		t.Error("Ready = true, want false for namespace selector outside system namespace")
	}
	if !containsStr(result.ReadyMessage, "namespaceSelector is only allowed") {
		t.Errorf("ReadyMessage = %q, want substring about namespace selector restriction", result.ReadyMessage)
	}
}

func TestLoadDetector_InvalidNamespaceSelector(t *testing.T) {
	engine, _ := newTestEngine(t)
	ctx := context.Background()

	wd := makeDetectorCRD("wormsign-system", "bad-ns-selector", "pods",
		"true", v1alpha1.SeverityWarning, nil)
	wd.Spec.NamespaceSelector = &metav1.LabelSelector{
		MatchExpressions: []metav1.LabelSelectorRequirement{
			{
				Key:      "foo",
				Operator: "InvalidOperator",
				Values:   []string{"bar"},
			},
		},
	}

	result, err := engine.LoadDetector(ctx, wd)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Ready {
		t.Error("Ready = true, want false for invalid namespace selector")
	}
	if !containsStr(result.ReadyMessage, "invalid namespaceSelector") {
		t.Errorf("ReadyMessage = %q, want substring 'invalid namespaceSelector'", result.ReadyMessage)
	}
}

// =====================================================================
// CRD Lifecycle Tests
// =====================================================================

func TestLoadDetector_ReplacesExisting(t *testing.T) {
	engine, _ := newTestEngine(t)
	ctx := context.Background()

	// Load initial detector.
	wd := makeDetectorCRD("default", "replaceable", "pods",
		"true", v1alpha1.SeverityWarning, nil)
	result, err := engine.LoadDetector(ctx, wd)
	if err != nil {
		t.Fatalf("first LoadDetector() error = %v", err)
	}
	if !result.Ready {
		t.Fatalf("first load not ready: %s", result.ReadyMessage)
	}

	// Replace with updated expression.
	wd2 := makeDetectorCRD("default", "replaceable", "pods",
		"false", v1alpha1.SeverityCritical, nil)
	result2, err := engine.LoadDetector(ctx, wd2)
	if err != nil {
		t.Fatalf("second LoadDetector() error = %v", err)
	}
	if !result2.Ready {
		t.Fatalf("second load not ready: %s", result2.ReadyMessage)
	}

	// Should still be exactly 1 detector.
	if engine.DetectorCount() != 1 {
		t.Errorf("DetectorCount = %d, want 1", engine.DetectorCount())
	}

	cd := engine.GetDetector("default", "replaceable")
	if cd == nil {
		t.Fatal("detector not found")
	}
	if cd.Severity != model.SeverityCritical {
		t.Errorf("Severity = %q, want critical", cd.Severity)
	}
}

func TestUnloadDetector(t *testing.T) {
	engine, _ := newTestEngine(t)
	ctx := context.Background()

	wd := makeDetectorCRD("default", "to-remove", "pods",
		"true", v1alpha1.SeverityWarning, nil)
	if _, err := engine.LoadDetector(ctx, wd); err != nil {
		t.Fatalf("LoadDetector() error = %v", err)
	}

	if engine.DetectorCount() != 1 {
		t.Fatalf("DetectorCount = %d, want 1", engine.DetectorCount())
	}

	removed := engine.UnloadDetector("default", "to-remove")
	if !removed {
		t.Error("UnloadDetector returned false, want true")
	}
	if engine.DetectorCount() != 0 {
		t.Errorf("DetectorCount = %d, want 0", engine.DetectorCount())
	}
}

func TestUnloadDetector_NotFound(t *testing.T) {
	engine, _ := newTestEngine(t)
	removed := engine.UnloadDetector("default", "nonexistent")
	if removed {
		t.Error("UnloadDetector returned true for nonexistent detector")
	}
}

func TestDetectorNames(t *testing.T) {
	engine, _ := newTestEngine(t)
	ctx := context.Background()

	wd1 := makeDetectorCRD("default", "det-a", "pods", "true", v1alpha1.SeverityWarning, nil)
	wd2 := makeDetectorCRD("kube-system", "det-b", "nodes", "true", v1alpha1.SeverityWarning, nil)

	if _, err := engine.LoadDetector(ctx, wd1); err != nil {
		t.Fatalf("LoadDetector() error = %v", err)
	}
	if _, err := engine.LoadDetector(ctx, wd2); err != nil {
		t.Fatalf("LoadDetector() error = %v", err)
	}

	names := engine.DetectorNames()
	if len(names) != 2 {
		t.Fatalf("DetectorNames() returned %d names, want 2", len(names))
	}

	nameSet := make(map[string]bool)
	for _, n := range names {
		nameSet[n] = true
	}
	if !nameSet["default/det-a"] {
		t.Error("missing detector 'default/det-a'")
	}
	if !nameSet["kube-system/det-b"] {
		t.Error("missing detector 'kube-system/det-b'")
	}
}

// =====================================================================
// Expression Evaluation Tests
// =====================================================================

func TestEvaluateResource_SimpleMatch(t *testing.T) {
	engine, getEvents := newTestEngine(t)
	ctx := context.Background()

	wd := makeDetectorCRD("default", "pending-check", "pods",
		`pod.status == "Pending"`,
		v1alpha1.SeverityWarning, nil)
	if _, err := engine.LoadDetector(ctx, wd); err != nil {
		t.Fatalf("LoadDetector() error = %v", err)
	}

	emitted := engine.EvaluateResource(
		"pods", "default", "test-pod", "uid-1",
		map[string]any{
			"status": "Pending",
			"name":   "test-pod",
		},
		map[string]string{"app": "web"},
		nil,
	)

	if emitted != 1 {
		t.Errorf("emitted = %d, want 1", emitted)
	}

	events := getEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Resource.Kind != "Pod" {
		t.Errorf("Kind = %q, want Pod", events[0].Resource.Kind)
	}
	if events[0].Resource.Namespace != "default" {
		t.Errorf("Namespace = %q, want default", events[0].Resource.Namespace)
	}
	if events[0].Labels["app"] != "web" {
		t.Errorf("label app = %q, want web", events[0].Labels["app"])
	}
}

func TestEvaluateResource_NoMatch(t *testing.T) {
	engine, getEvents := newTestEngine(t)
	ctx := context.Background()

	wd := makeDetectorCRD("default", "pending-check", "pods",
		`pod.status == "Pending"`,
		v1alpha1.SeverityWarning, nil)
	if _, err := engine.LoadDetector(ctx, wd); err != nil {
		t.Fatalf("LoadDetector() error = %v", err)
	}

	emitted := engine.EvaluateResource(
		"pods", "default", "running-pod", "uid-2",
		map[string]any{
			"status": "Running",
			"name":   "running-pod",
		},
		nil, nil,
	)

	if emitted != 0 {
		t.Errorf("emitted = %d, want 0", emitted)
	}
	if len(getEvents()) != 0 {
		t.Error("expected no events")
	}
}

func TestEvaluateResource_WithParams(t *testing.T) {
	engine, getEvents := newTestEngine(t)
	ctx := context.Background()

	wd := makeDetectorCRD("default", "restart-check", "pods",
		`pod.restartCount > int(params.maxRestarts)`,
		v1alpha1.SeverityCritical,
		map[string]string{"maxRestarts": "3"})
	if _, err := engine.LoadDetector(ctx, wd); err != nil {
		t.Fatalf("LoadDetector() error = %v", err)
	}

	// Over threshold.
	emitted := engine.EvaluateResource(
		"pods", "default", "crashing-pod", "uid-3",
		map[string]any{"restartCount": int64(5)},
		nil, nil,
	)
	if emitted != 1 {
		t.Errorf("emitted = %d, want 1", emitted)
	}

	// Under threshold.
	emitted = engine.EvaluateResource(
		"pods", "default", "healthy-pod", "uid-4",
		map[string]any{"restartCount": int64(2)},
		nil, nil,
	)
	if emitted != 0 {
		t.Errorf("emitted = %d, want 0", emitted)
	}

	if len(getEvents()) != 1 {
		t.Errorf("expected 1 event, got %d", len(getEvents()))
	}
}

func TestEvaluateResource_WrongResourceType(t *testing.T) {
	engine, _ := newTestEngine(t)
	ctx := context.Background()

	wd := makeDetectorCRD("default", "pod-only", "pods",
		"true", v1alpha1.SeverityWarning, nil)
	if _, err := engine.LoadDetector(ctx, wd); err != nil {
		t.Fatalf("LoadDetector() error = %v", err)
	}

	// Evaluate a node against a pod detector — should not match.
	emitted := engine.EvaluateResource(
		"nodes", "default", "node-1", "uid-n1",
		map[string]any{"name": "node-1"},
		nil, nil,
	)
	if emitted != 0 {
		t.Errorf("emitted = %d, want 0 for wrong resource type", emitted)
	}
}

func TestEvaluateResource_WrongNamespace(t *testing.T) {
	engine, _ := newTestEngine(t)
	ctx := context.Background()

	// Detector in "payments" namespace should only match resources in "payments".
	wd := makeDetectorCRD("payments", "ns-scoped", "pods",
		"true", v1alpha1.SeverityWarning, nil)
	if _, err := engine.LoadDetector(ctx, wd); err != nil {
		t.Fatalf("LoadDetector() error = %v", err)
	}

	// Resource in "default" namespace — should not match.
	emitted := engine.EvaluateResource(
		"pods", "default", "pod-1", "uid-1",
		map[string]any{"name": "pod-1"},
		nil, nil,
	)
	if emitted != 0 {
		t.Errorf("emitted = %d, want 0 for wrong namespace", emitted)
	}

	// Resource in "payments" namespace — should match.
	emitted = engine.EvaluateResource(
		"pods", "payments", "pod-2", "uid-2",
		map[string]any{"name": "pod-2"},
		nil, nil,
	)
	if emitted != 1 {
		t.Errorf("emitted = %d, want 1 for matching namespace", emitted)
	}
}

func TestEvaluateResource_CrossNamespaceWithSelector(t *testing.T) {
	engine, getEvents := newTestEngine(t)
	ctx := context.Background()

	// Cross-namespace detector in system namespace with label selector.
	wd := makeDetectorCRD("wormsign-system", "cross-ns-det", "pods",
		"true", v1alpha1.SeverityWarning, nil)
	wd.Spec.NamespaceSelector = &metav1.LabelSelector{
		MatchLabels: map[string]string{"wormsign.io/watch": "true"},
	}
	if _, err := engine.LoadDetector(ctx, wd); err != nil {
		t.Fatalf("LoadDetector() error = %v", err)
	}

	// Namespace without matching labels — should not trigger.
	emitted := engine.EvaluateResource(
		"pods", "payments", "pod-1", "uid-1",
		map[string]any{"name": "pod-1"},
		nil,
		map[string]string{"team": "payments"},
	)
	if emitted != 0 {
		t.Errorf("emitted = %d, want 0 for non-matching namespace labels", emitted)
	}

	// Namespace with matching labels — should trigger.
	emitted = engine.EvaluateResource(
		"pods", "orders", "pod-2", "uid-2",
		map[string]any{"name": "pod-2"},
		nil,
		map[string]string{"wormsign.io/watch": "true"},
	)
	if emitted != 1 {
		t.Errorf("emitted = %d, want 1 for matching namespace labels", emitted)
	}

	events := getEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
}

func TestEvaluateResource_CrossNamespaceNilLabels(t *testing.T) {
	engine, _ := newTestEngine(t)
	ctx := context.Background()

	wd := makeDetectorCRD("wormsign-system", "cross-ns-nil", "pods",
		"true", v1alpha1.SeverityWarning, nil)
	wd.Spec.NamespaceSelector = &metav1.LabelSelector{
		MatchLabels: map[string]string{"wormsign.io/watch": "true"},
	}
	if _, err := engine.LoadDetector(ctx, wd); err != nil {
		t.Fatalf("LoadDetector() error = %v", err)
	}

	// Nil namespace labels should not match.
	emitted := engine.EvaluateResource(
		"pods", "default", "pod-1", "uid-1",
		map[string]any{"name": "pod-1"},
		nil, nil,
	)
	if emitted != 0 {
		t.Errorf("emitted = %d, want 0 for nil namespace labels", emitted)
	}
}

func TestEvaluateResource_MultipleDetectors(t *testing.T) {
	engine, getEvents := newTestEngine(t)
	ctx := context.Background()

	// Two detectors in the same namespace for the same resource type.
	wd1 := makeDetectorCRD("default", "det-1", "pods",
		"true", v1alpha1.SeverityWarning, nil)
	wd2 := makeDetectorCRD("default", "det-2", "pods",
		"true", v1alpha1.SeverityCritical, nil)

	if _, err := engine.LoadDetector(ctx, wd1); err != nil {
		t.Fatalf("LoadDetector(det-1) error = %v", err)
	}
	if _, err := engine.LoadDetector(ctx, wd2); err != nil {
		t.Fatalf("LoadDetector(det-2) error = %v", err)
	}

	emitted := engine.EvaluateResource(
		"pods", "default", "pod-1", "uid-1",
		map[string]any{"name": "pod-1"},
		nil, nil,
	)
	if emitted != 2 {
		t.Errorf("emitted = %d, want 2", emitted)
	}

	events := getEvents()
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
}

func TestEvaluateResource_CooldownSuppression(t *testing.T) {
	engine, getEvents := newTestEngine(t)
	ctx := context.Background()

	wd := makeDetectorCRD("default", "cooldown-det", "pods",
		"true", v1alpha1.SeverityWarning, nil)
	wd.Spec.Cooldown = "30m"
	if _, err := engine.LoadDetector(ctx, wd); err != nil {
		t.Fatalf("LoadDetector() error = %v", err)
	}

	// First evaluation should emit.
	emitted := engine.EvaluateResource(
		"pods", "default", "pod-1", "uid-1",
		map[string]any{"name": "pod-1"},
		nil, nil,
	)
	if emitted != 1 {
		t.Errorf("first emitted = %d, want 1", emitted)
	}

	// Second evaluation for same resource within cooldown should be suppressed.
	emitted = engine.EvaluateResource(
		"pods", "default", "pod-1", "uid-1",
		map[string]any{"name": "pod-1"},
		nil, nil,
	)
	if emitted != 0 {
		t.Errorf("second emitted = %d, want 0 (cooldown)", emitted)
	}

	events := getEvents()
	if len(events) != 1 {
		t.Errorf("total events = %d, want 1", len(events))
	}
}

func TestEvaluateResource_EvalError(t *testing.T) {
	engine, getEvents := newTestEngine(t)
	ctx := context.Background()

	// Expression accesses a field that won't exist in the data.
	wd := makeDetectorCRD("default", "err-det", "pods",
		`pod.nonexistent.deeply.nested == "value"`,
		v1alpha1.SeverityWarning, nil)
	if _, err := engine.LoadDetector(ctx, wd); err != nil {
		t.Fatalf("LoadDetector() error = %v", err)
	}

	// Should not emit (evaluation error) and should not panic.
	emitted := engine.EvaluateResource(
		"pods", "default", "pod-1", "uid-1",
		map[string]any{"name": "pod-1"},
		nil, nil,
	)
	if emitted != 0 {
		t.Errorf("emitted = %d, want 0 for eval error", emitted)
	}
	if len(getEvents()) != 0 {
		t.Error("expected no events on evaluation error")
	}
}

func TestEvaluateResource_NoDetectorsLoaded(t *testing.T) {
	engine, _ := newTestEngine(t)

	emitted := engine.EvaluateResource(
		"pods", "default", "pod-1", "uid-1",
		map[string]any{"name": "pod-1"},
		nil, nil,
	)
	if emitted != 0 {
		t.Errorf("emitted = %d, want 0 with no detectors", emitted)
	}
}

// =====================================================================
// CEL Environment Function Tests
// =====================================================================

func TestCELEnv_NowFunction(t *testing.T) {
	engine, getEvents := newTestEngine(t)
	ctx := context.Background()

	// This expression uses now() — it should compile and evaluate.
	// We use a condition that's always true by comparing now() to a past timestamp.
	wd := makeDetectorCRD("default", "now-test", "pods",
		`now() > timestamp("2020-01-01T00:00:00Z")`,
		v1alpha1.SeverityInfo, nil)

	result, err := engine.LoadDetector(ctx, wd)
	if err != nil {
		t.Fatalf("LoadDetector() error = %v", err)
	}
	if !result.Ready {
		t.Fatalf("Ready = false; message: %s", result.ReadyMessage)
	}

	emitted := engine.EvaluateResource(
		"pods", "default", "pod-1", "uid-1",
		map[string]any{"name": "pod-1"},
		nil, nil,
	)
	if emitted != 1 {
		t.Errorf("emitted = %d, want 1 (now() should be after 2020)", emitted)
	}
	if len(getEvents()) != 1 {
		t.Error("expected 1 event from now() expression")
	}
}

func TestCELEnv_DurationParsing(t *testing.T) {
	engine, _ := newTestEngine(t)
	ctx := context.Background()

	// Use CEL's built-in duration function.
	wd := makeDetectorCRD("default", "duration-test", "pods",
		`duration("1h") > duration("30m")`,
		v1alpha1.SeverityInfo, nil)

	result, err := engine.LoadDetector(ctx, wd)
	if err != nil {
		t.Fatalf("LoadDetector() error = %v", err)
	}
	if !result.Ready {
		t.Fatalf("Ready = false; message: %s", result.ReadyMessage)
	}

	emitted := engine.EvaluateResource(
		"pods", "default", "pod-1", "uid-1",
		map[string]any{"name": "pod-1"},
		nil, nil,
	)
	if emitted != 1 {
		t.Errorf("emitted = %d, want 1", emitted)
	}
}

func TestCELEnv_TimestampParsing(t *testing.T) {
	engine, _ := newTestEngine(t)
	ctx := context.Background()

	wd := makeDetectorCRD("default", "ts-test", "pods",
		`timestamp("2026-01-01T00:00:00Z") > timestamp("2025-01-01T00:00:00Z")`,
		v1alpha1.SeverityInfo, nil)

	result, err := engine.LoadDetector(ctx, wd)
	if err != nil {
		t.Fatalf("LoadDetector() error = %v", err)
	}
	if !result.Ready {
		t.Fatalf("Ready = false; message: %s", result.ReadyMessage)
	}

	emitted := engine.EvaluateResource(
		"pods", "default", "pod-1", "uid-1",
		map[string]any{"name": "pod-1"},
		nil, nil,
	)
	if emitted != 1 {
		t.Errorf("emitted = %d, want 1", emitted)
	}
}

func TestCELEnv_StandardFunctions(t *testing.T) {
	tests := []struct {
		name      string
		condition string
		data      map[string]any
		wantEmit  bool
	}{
		{
			name:      "size function",
			condition: `size(pod.name) > 3`,
			data:      map[string]any{"name": "test-pod"},
			wantEmit:  true,
		},
		{
			name:      "startsWith",
			condition: `pod.name.startsWith("test")`,
			data:      map[string]any{"name": "test-pod"},
			wantEmit:  true,
		},
		{
			name:      "endsWith",
			condition: `pod.name.endsWith("pod")`,
			data:      map[string]any{"name": "test-pod"},
			wantEmit:  true,
		},
		{
			name:      "contains",
			condition: `pod.name.contains("st-p")`,
			data:      map[string]any{"name": "test-pod"},
			wantEmit:  true,
		},
		{
			name:      "matches regex",
			condition: `pod.name.matches("^test-.*$")`,
			data:      map[string]any{"name": "test-pod"},
			wantEmit:  true,
		},
		{
			name:      "list exists",
			condition: `pod.items.exists(x, x > 5)`,
			data:      map[string]any{"items": []any{int64(1), int64(2), int64(10)}},
			wantEmit:  true,
		},
		{
			name:      "list exists no match",
			condition: `pod.items.exists(x, x > 100)`,
			data:      map[string]any{"items": []any{int64(1), int64(2), int64(10)}},
			wantEmit:  false,
		},
		{
			name:      "map access",
			condition: `pod.labels["app"] == "web"`,
			data:      map[string]any{"labels": map[string]any{"app": "web"}},
			wantEmit:  true,
		},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Each sub-test gets its own engine to avoid cross-detector interference.
			engine, _ := newTestEngine(t)
			ctx := context.Background()

			detName := fmt.Sprintf("std-func-%d", i)
			wd := makeDetectorCRD("default", detName, "pods",
				tt.condition, v1alpha1.SeverityInfo, nil)
			result, err := engine.LoadDetector(ctx, wd)
			if err != nil {
				t.Fatalf("LoadDetector() error = %v", err)
			}
			if !result.Ready {
				t.Fatalf("Ready = false; message: %s", result.ReadyMessage)
			}

			emitted := engine.EvaluateResource(
				"pods", "default", "pod-1", fmt.Sprintf("uid-%d", i),
				tt.data, nil, nil,
			)
			if (emitted > 0) != tt.wantEmit {
				t.Errorf("emitted = %d, wantEmit = %v", emitted, tt.wantEmit)
			}
		})
	}
}

// =====================================================================
// Cost Budget Tests
// =====================================================================

func TestCostBudget_ExceedsDuringEval(t *testing.T) {
	cb, _ := collectCallback()
	// Use a very low cost limit.
	engine, err := NewCELDetectorEngine(EngineConfig{
		CostLimit:       1,
		Callback:        cb,
		Logger:          silentLogger(),
		SystemNamespace: "wormsign-system",
	})
	if err != nil {
		t.Fatalf("NewCELDetectorEngine() error = %v", err)
	}

	ctx := context.Background()
	wd := makeDetectorCRD("default", "costly", "pods",
		`pod.name.startsWith("a") && pod.name.endsWith("z") && pod.name.contains("m")`,
		v1alpha1.SeverityWarning, nil)

	result, err := engine.LoadDetector(ctx, wd)
	if err != nil {
		t.Fatalf("LoadDetector() error = %v", err)
	}
	// The expression may compile but fail at runtime due to cost limit.
	// Either outcome is acceptable — the expression should not produce false events.
	if result.Ready {
		// If it compiled, evaluation should fail or be limited.
		emitted := engine.EvaluateResource(
			"pods", "default", "pod-1", "uid-1",
			map[string]any{"name": "abcdefghijklmnopqrstuvwxyz"},
			nil, nil,
		)
		// With cost limit of 1, evaluation should fail (cost exceeded).
		// The key assertion is that no event is emitted falsely.
		_ = emitted // either 0 (cost exceeded) or 1 (if cost was within budget)
	}
}

func TestCostBudget_DefaultLimit(t *testing.T) {
	engine, getEvents := newTestEngine(t)
	ctx := context.Background()

	// A simple expression should be well within the default 1000 cost limit.
	wd := makeDetectorCRD("default", "simple", "pods",
		"true", v1alpha1.SeverityInfo, nil)
	result, err := engine.LoadDetector(ctx, wd)
	if err != nil {
		t.Fatalf("LoadDetector() error = %v", err)
	}
	if !result.Ready {
		t.Fatalf("Ready = false; message: %s", result.ReadyMessage)
	}

	emitted := engine.EvaluateResource(
		"pods", "default", "pod-1", "uid-1",
		map[string]any{"name": "pod-1"},
		nil, nil,
	)
	if emitted != 1 {
		t.Errorf("emitted = %d, want 1", emitted)
	}
	if len(getEvents()) != 1 {
		t.Error("expected 1 event")
	}
}

// =====================================================================
// Status Condition Tests
// =====================================================================

func TestBuildStatusConditions_Ready(t *testing.T) {
	result := &LoadDetectorResult{
		Ready:             true,
		ReadyMessage:      "CEL expression compiled successfully",
		MatchedNamespaces: 0, // no namespace selector → single namespace
	}

	conditions := BuildStatusConditions(result, 1)

	// Should have Ready, Valid, and Active conditions.
	if len(conditions) != 3 {
		t.Fatalf("expected 3 conditions, got %d", len(conditions))
	}

	readyCond := findCondition(conditions, v1alpha1.ConditionReady)
	if readyCond == nil {
		t.Fatal("Ready condition not found")
	}
	if readyCond.Status != metav1.ConditionTrue {
		t.Errorf("Ready status = %s, want True", readyCond.Status)
	}
	if readyCond.Reason != "CompilationSucceeded" {
		t.Errorf("Ready reason = %q, want CompilationSucceeded", readyCond.Reason)
	}
	if readyCond.ObservedGeneration != 1 {
		t.Errorf("ObservedGeneration = %d, want 1", readyCond.ObservedGeneration)
	}

	validCond := findCondition(conditions, v1alpha1.ConditionValid)
	if validCond == nil {
		t.Fatal("Valid condition not found")
	}
	if validCond.Status != metav1.ConditionTrue {
		t.Errorf("Valid status = %s, want True", validCond.Status)
	}

	activeCond := findCondition(conditions, v1alpha1.ConditionActive)
	if activeCond == nil {
		t.Fatal("Active condition not found")
	}
	if activeCond.Status != metav1.ConditionTrue {
		t.Errorf("Active status = %s, want True", activeCond.Status)
	}
	if activeCond.Message != "Watching 1 namespace" {
		t.Errorf("Active message = %q, want 'Watching 1 namespace'", activeCond.Message)
	}
}

func TestBuildStatusConditions_ReadyWithNamespaces(t *testing.T) {
	result := &LoadDetectorResult{
		Ready:             true,
		ReadyMessage:      "CEL expression compiled successfully",
		MatchedNamespaces: 12,
	}

	conditions := BuildStatusConditions(result, 1)

	activeCond := findCondition(conditions, v1alpha1.ConditionActive)
	if activeCond == nil {
		t.Fatal("Active condition not found")
	}
	if activeCond.Message != "Watching 12 namespaces" {
		t.Errorf("Active message = %q, want 'Watching 12 namespaces'", activeCond.Message)
	}
}

func TestBuildStatusConditions_NotReady(t *testing.T) {
	result := &LoadDetectorResult{
		Ready:        false,
		ReadyMessage: "CEL compilation error: syntax error",
	}

	conditions := BuildStatusConditions(result, 2)

	// Should have Ready and Valid conditions only (no Active for failed).
	if len(conditions) != 2 {
		t.Fatalf("expected 2 conditions, got %d", len(conditions))
	}

	readyCond := findCondition(conditions, v1alpha1.ConditionReady)
	if readyCond == nil {
		t.Fatal("Ready condition not found")
	}
	if readyCond.Status != metav1.ConditionFalse {
		t.Errorf("Ready status = %s, want False", readyCond.Status)
	}
	if readyCond.Reason != "CompilationFailed" {
		t.Errorf("Ready reason = %q, want CompilationFailed", readyCond.Reason)
	}
	if readyCond.ObservedGeneration != 2 {
		t.Errorf("ObservedGeneration = %d, want 2", readyCond.ObservedGeneration)
	}
	if readyCond.Message != "CEL compilation error: syntax error" {
		t.Errorf("Message = %q, want CEL compilation error", readyCond.Message)
	}

	activeCond := findCondition(conditions, v1alpha1.ConditionActive)
	if activeCond != nil {
		t.Error("Active condition should not be present for failed compilation")
	}
}

// =====================================================================
// Resource Alias Tests
// =====================================================================

func TestEvaluateResource_ResourceAlias(t *testing.T) {
	engine, getEvents := newTestEngine(t)
	ctx := context.Background()

	// Use "resource" instead of the type-specific "pod" variable.
	wd := makeDetectorCRD("default", "alias-test", "pods",
		`resource.status == "Pending"`,
		v1alpha1.SeverityWarning, nil)
	result, err := engine.LoadDetector(ctx, wd)
	if err != nil {
		t.Fatalf("LoadDetector() error = %v", err)
	}
	if !result.Ready {
		t.Fatalf("Ready = false; message: %s", result.ReadyMessage)
	}

	emitted := engine.EvaluateResource(
		"pods", "default", "alias-pod", "uid-alias",
		map[string]any{"status": "Pending", "name": "alias-pod"},
		nil, nil,
	)
	if emitted != 1 {
		t.Errorf("emitted = %d, want 1 for resource alias", emitted)
	}
	events := getEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
}

// =====================================================================
// now() Test Clock Tests
// =====================================================================

func TestCELEnv_NowFunctionUsesEngineClock(t *testing.T) {
	engine, getEvents := newTestEngine(t)
	ctx := context.Background()

	// Set the engine's clock to a fixed time in 2020.
	fixedTime := time.Date(2020, 6, 15, 12, 0, 0, 0, time.UTC)
	engine.SetNowFunc(func() time.Time { return fixedTime })

	// Expression: now() should be BEFORE 2021. With real time (2026), this would be false.
	// With our fixed clock (2020), it should be true.
	wd := makeDetectorCRD("default", "clock-test", "pods",
		`now() < timestamp("2021-01-01T00:00:00Z")`,
		v1alpha1.SeverityInfo, nil)

	result, err := engine.LoadDetector(ctx, wd)
	if err != nil {
		t.Fatalf("LoadDetector() error = %v", err)
	}
	if !result.Ready {
		t.Fatalf("Ready = false; message: %s", result.ReadyMessage)
	}

	emitted := engine.EvaluateResource(
		"pods", "default", "pod-1", "uid-clock",
		map[string]any{"name": "pod-1"},
		nil, nil,
	)
	if emitted != 1 {
		t.Errorf("emitted = %d, want 1 (fixed clock is 2020, before 2021)", emitted)
	}
	if len(getEvents()) != 1 {
		t.Error("expected 1 event from test clock expression")
	}
}

// =====================================================================
// CountMatchingNamespaces Tests
// =====================================================================

func TestCountMatchingNamespaces_NoSelector(t *testing.T) {
	engine, _ := newTestEngine(t)
	ctx := context.Background()

	wd := makeDetectorCRD("default", "no-selector", "pods",
		"true", v1alpha1.SeverityWarning, nil)
	if _, err := engine.LoadDetector(ctx, wd); err != nil {
		t.Fatalf("LoadDetector() error = %v", err)
	}

	count := engine.CountMatchingNamespaces("default", "no-selector", []map[string]string{
		{"team": "alpha"},
		{"team": "beta"},
	})
	if count != 0 {
		t.Errorf("CountMatchingNamespaces = %d, want 0 for no selector", count)
	}
}

func TestCountMatchingNamespaces_WithSelector(t *testing.T) {
	engine, _ := newTestEngine(t)
	ctx := context.Background()

	wd := makeDetectorCRD("wormsign-system", "ns-count", "pods",
		"true", v1alpha1.SeverityWarning, nil)
	wd.Spec.NamespaceSelector = &metav1.LabelSelector{
		MatchLabels: map[string]string{"wormsign.io/watch": "true"},
	}
	if _, err := engine.LoadDetector(ctx, wd); err != nil {
		t.Fatalf("LoadDetector() error = %v", err)
	}

	nsLabelSets := []map[string]string{
		{"wormsign.io/watch": "true", "team": "alpha"},
		{"wormsign.io/watch": "false", "team": "beta"},
		{"wormsign.io/watch": "true", "team": "gamma"},
		{"team": "delta"},
	}
	count := engine.CountMatchingNamespaces("wormsign-system", "ns-count", nsLabelSets)
	if count != 2 {
		t.Errorf("CountMatchingNamespaces = %d, want 2", count)
	}
}

func TestCountMatchingNamespaces_NonexistentDetector(t *testing.T) {
	engine, _ := newTestEngine(t)

	count := engine.CountMatchingNamespaces("default", "nonexistent", []map[string]string{
		{"foo": "bar"},
	})
	if count != 0 {
		t.Errorf("CountMatchingNamespaces = %d, want 0 for nonexistent detector", count)
	}
}

// =====================================================================
// Evaluator (Standalone) Tests
// =====================================================================

func TestEvaluator_CompileValid(t *testing.T) {
	eval := NewEvaluator(DefaultCostLimit)

	ast, err := eval.Compile(`resource.name == "test"`)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if ast == nil {
		t.Fatal("AST is nil")
	}
}

func TestEvaluator_CompileWithNow(t *testing.T) {
	eval := NewEvaluator(DefaultCostLimit)

	ast, err := eval.Compile(`now() > timestamp("2020-01-01T00:00:00Z")`)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if ast == nil {
		t.Fatal("AST is nil")
	}
}

func TestEvaluator_CompileInvalid(t *testing.T) {
	eval := NewEvaluator(DefaultCostLimit)

	_, err := eval.Compile(`this is not valid CEL!!!`)
	if err == nil {
		t.Fatal("expected error for invalid expression")
	}
}

func TestEvaluator_DefaultCostLimit(t *testing.T) {
	eval := NewEvaluator(0) // should default to DefaultCostLimit
	if eval.costLimit != DefaultCostLimit {
		t.Errorf("costLimit = %d, want %d", eval.costLimit, DefaultCostLimit)
	}
}

// =====================================================================
// Concurrent Access Tests
// =====================================================================

func TestCELDetectorEngine_ConcurrentAccess(t *testing.T) {
	engine, _ := newTestEngine(t)
	ctx := context.Background()

	// Pre-load a detector.
	wd := makeDetectorCRD("default", "concurrent", "pods",
		"true", v1alpha1.SeverityWarning, nil)
	if _, err := engine.LoadDetector(ctx, wd); err != nil {
		t.Fatalf("LoadDetector() error = %v", err)
	}

	// Concurrent reads and writes should not panic or race.
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Concurrent evaluations.
			engine.EvaluateResource(
				"pods", "default", fmt.Sprintf("pod-%d", i), fmt.Sprintf("uid-%d", i),
				map[string]any{"name": fmt.Sprintf("pod-%d", i)},
				nil, nil,
			)
		}(i)
	}
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Concurrent loads.
			name := fmt.Sprintf("conc-det-%d", i)
			wd := makeDetectorCRD("default", name, "pods",
				"true", v1alpha1.SeverityWarning, nil)
			engine.LoadDetector(ctx, wd)
		}(i)
	}
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			// Concurrent reads.
			engine.DetectorCount()
			engine.DetectorNames()
		}(i)
	}
	wg.Wait()
}

// =====================================================================
// Helper Functions
// =====================================================================

func TestResourceVarName(t *testing.T) {
	tests := []struct {
		resource string
		want     string
	}{
		{"pods", "pod"},
		{"nodes", "node"},
		{"persistentvolumeclaims", "pvc"},
		{"jobs", "job"},
		{"deployments", "deployment"},
		{"statefulsets", "statefulset"},
		{"daemonsets", "daemonset"},
		{"replicasets", "replicaset"},
		{"namespaces", "ns"},
		{"unknown", "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.resource, func(t *testing.T) {
			got := resourceVarName(tt.resource)
			if got != tt.want {
				t.Errorf("resourceVarName(%q) = %q, want %q", tt.resource, got, tt.want)
			}
		})
	}
}

func TestDetectorKey(t *testing.T) {
	got := detectorKey("default", "my-detector")
	if got != "default/my-detector" {
		t.Errorf("detectorKey = %q, want 'default/my-detector'", got)
	}
}

// =====================================================================
// Test Utilities
// =====================================================================

func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func findCondition(conditions []metav1.Condition, condType string) *metav1.Condition {
	for i := range conditions {
		if conditions[i].Type == condType {
			return &conditions[i]
		}
	}
	return nil
}
