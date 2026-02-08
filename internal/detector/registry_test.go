package detector

import (
	"context"
	"fmt"
	"sort"
	"testing"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

// mockDetector is a minimal Detector implementation for registry tests.
type mockDetector struct {
	name       string
	severity   model.Severity
	leaderOnly bool
}

func (m *mockDetector) Name() string                       { return m.name }
func (m *mockDetector) Start(_ context.Context) error      { return nil }
func (m *mockDetector) Stop()                              {}
func (m *mockDetector) Severity() model.Severity           { return m.severity }
func (m *mockDetector) IsLeaderOnly() bool                 { return m.leaderOnly }

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry(silentLogger())

	d := &mockDetector{name: "TestDetector", severity: model.SeverityWarning}
	if err := r.Register(d); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	got := r.Get("TestDetector")
	if got == nil {
		t.Fatal("Get() returned nil")
	}
	if got.Name() != "TestDetector" {
		t.Errorf("Get().Name() = %q, want %q", got.Name(), "TestDetector")
	}
}

func TestRegistry_GetNotFound(t *testing.T) {
	r := NewRegistry(silentLogger())
	if got := r.Get("nonexistent"); got != nil {
		t.Errorf("Get(nonexistent) = %v, want nil", got)
	}
}

func TestRegistry_RegisterDuplicate(t *testing.T) {
	r := NewRegistry(silentLogger())

	d1 := &mockDetector{name: "Dup", severity: model.SeverityWarning}
	d2 := &mockDetector{name: "Dup", severity: model.SeverityCritical}

	if err := r.Register(d1); err != nil {
		t.Fatalf("first Register() error = %v", err)
	}
	err := r.Register(d2)
	if err == nil {
		t.Fatal("expected error on duplicate registration")
	}
	if !containsStr(err.Error(), "already registered") {
		t.Errorf("error = %q, want substring %q", err.Error(), "already registered")
	}
}

func TestRegistry_RegisterNil(t *testing.T) {
	r := NewRegistry(silentLogger())
	err := r.Register(nil)
	if err == nil {
		t.Fatal("expected error on nil detector")
	}
	if !containsStr(err.Error(), "must not be nil") {
		t.Errorf("error = %q, want substring %q", err.Error(), "must not be nil")
	}
}

func TestRegistry_RegisterEmptyName(t *testing.T) {
	r := NewRegistry(silentLogger())
	d := &mockDetector{name: "", severity: model.SeverityWarning}
	err := r.Register(d)
	if err == nil {
		t.Fatal("expected error on empty name")
	}
	if !containsStr(err.Error(), "name must not be empty") {
		t.Errorf("error = %q, want substring %q", err.Error(), "name must not be empty")
	}
}

func TestRegistry_All(t *testing.T) {
	r := NewRegistry(silentLogger())

	d1 := &mockDetector{name: "A", severity: model.SeverityWarning}
	d2 := &mockDetector{name: "B", severity: model.SeverityCritical}
	d3 := &mockDetector{name: "C", severity: model.SeverityInfo}

	for _, d := range []*mockDetector{d1, d2, d3} {
		if err := r.Register(d); err != nil {
			t.Fatalf("Register(%s) error = %v", d.name, err)
		}
	}

	all := r.All()
	if len(all) != 3 {
		t.Fatalf("All() returned %d detectors, want 3", len(all))
	}
}

func TestRegistry_Count(t *testing.T) {
	r := NewRegistry(silentLogger())
	if r.Count() != 0 {
		t.Errorf("Count() = %d, want 0", r.Count())
	}

	r.Register(&mockDetector{name: "A", severity: model.SeverityWarning})
	if r.Count() != 1 {
		t.Errorf("Count() = %d, want 1", r.Count())
	}

	r.Register(&mockDetector{name: "B", severity: model.SeverityWarning})
	if r.Count() != 2 {
		t.Errorf("Count() = %d, want 2", r.Count())
	}
}

func TestRegistry_Names(t *testing.T) {
	r := NewRegistry(silentLogger())
	r.Register(&mockDetector{name: "Beta", severity: model.SeverityWarning})
	r.Register(&mockDetector{name: "Alpha", severity: model.SeverityWarning})

	names := r.Names()
	sort.Strings(names)
	if len(names) != 2 || names[0] != "Alpha" || names[1] != "Beta" {
		t.Errorf("Names() = %v, want [Alpha, Beta]", names)
	}
}

func TestRegistry_Unregister(t *testing.T) {
	r := NewRegistry(silentLogger())
	r.Register(&mockDetector{name: "ToRemove", severity: model.SeverityWarning})

	if err := r.Unregister("ToRemove"); err != nil {
		t.Fatalf("Unregister() error = %v", err)
	}
	if r.Count() != 0 {
		t.Errorf("Count() = %d after unregister, want 0", r.Count())
	}
	if r.Get("ToRemove") != nil {
		t.Error("Get() should return nil after unregister")
	}
}

func TestRegistry_UnregisterNotFound(t *testing.T) {
	r := NewRegistry(silentLogger())
	err := r.Unregister("nonexistent")
	if err == nil {
		t.Fatal("expected error on unregistering nonexistent detector")
	}
	if !containsStr(err.Error(), "not registered") {
		t.Errorf("error = %q, want substring %q", err.Error(), "not registered")
	}
}

func TestRegistry_NilLogger(t *testing.T) {
	r := NewRegistry(nil) // should use default
	if r == nil {
		t.Fatal("NewRegistry(nil) returned nil")
	}
}

func TestRegistry_RegisterFactory(t *testing.T) {
	r := NewRegistry(silentLogger())
	factory := func(cb EventCallback) (Detector, error) {
		return &mockDetector{name: "FromFactory", severity: model.SeverityWarning}, nil
	}

	if err := r.RegisterFactory("FromFactory", factory); err != nil {
		t.Fatalf("RegisterFactory() error = %v", err)
	}

	got := r.GetFactory("FromFactory")
	if got == nil {
		t.Fatal("GetFactory() returned nil after registration")
	}
}

func TestRegistry_RegisterFactory_EmptyName(t *testing.T) {
	r := NewRegistry(silentLogger())
	factory := func(cb EventCallback) (Detector, error) {
		return &mockDetector{name: "X", severity: model.SeverityWarning}, nil
	}

	err := r.RegisterFactory("", factory)
	if err == nil {
		t.Fatal("expected error on empty factory name")
	}
	if !containsStr(err.Error(), "factory name must not be empty") {
		t.Errorf("error = %q, want substring about empty name", err.Error())
	}
}

func TestRegistry_RegisterFactory_NilFactory(t *testing.T) {
	r := NewRegistry(silentLogger())

	err := r.RegisterFactory("Test", nil)
	if err == nil {
		t.Fatal("expected error on nil factory")
	}
	if !containsStr(err.Error(), "must not be nil") {
		t.Errorf("error = %q, want substring about nil factory", err.Error())
	}
}

func TestRegistry_RegisterFactory_Duplicate(t *testing.T) {
	r := NewRegistry(silentLogger())
	factory := func(cb EventCallback) (Detector, error) {
		return &mockDetector{name: "Dup", severity: model.SeverityWarning}, nil
	}

	if err := r.RegisterFactory("Dup", factory); err != nil {
		t.Fatalf("first RegisterFactory() error = %v", err)
	}
	err := r.RegisterFactory("Dup", factory)
	if err == nil {
		t.Fatal("expected error on duplicate factory registration")
	}
	if !containsStr(err.Error(), "already registered") {
		t.Errorf("error = %q, want substring about already registered", err.Error())
	}
}

func TestRegistry_CreateFromFactory(t *testing.T) {
	r := NewRegistry(silentLogger())
	factory := func(cb EventCallback) (Detector, error) {
		return &mockDetector{name: "Created", severity: model.SeverityCritical}, nil
	}

	if err := r.RegisterFactory("Created", factory); err != nil {
		t.Fatalf("RegisterFactory() error = %v", err)
	}

	d, err := r.CreateFromFactory("Created", func(e *model.FaultEvent) {})
	if err != nil {
		t.Fatalf("CreateFromFactory() error = %v", err)
	}
	if d.Name() != "Created" {
		t.Errorf("created detector name = %q, want %q", d.Name(), "Created")
	}
	if d.Severity() != model.SeverityCritical {
		t.Errorf("created detector severity = %q, want %q", d.Severity(), model.SeverityCritical)
	}

	// Verify it was also registered in the detectors map.
	got := r.Get("Created")
	if got == nil {
		t.Fatal("detector should be registered after CreateFromFactory")
	}
}

func TestRegistry_CreateFromFactory_NotFound(t *testing.T) {
	r := NewRegistry(silentLogger())

	_, err := r.CreateFromFactory("nonexistent", func(e *model.FaultEvent) {})
	if err == nil {
		t.Fatal("expected error when factory not found")
	}
	if !containsStr(err.Error(), "no factory registered") {
		t.Errorf("error = %q, want substring about no factory", err.Error())
	}
}

func TestRegistry_CreateFromFactory_FactoryError(t *testing.T) {
	r := NewRegistry(silentLogger())
	factory := func(cb EventCallback) (Detector, error) {
		return nil, fmt.Errorf("configuration error: missing required field")
	}

	if err := r.RegisterFactory("Failing", factory); err != nil {
		t.Fatalf("RegisterFactory() error = %v", err)
	}

	_, err := r.CreateFromFactory("Failing", func(e *model.FaultEvent) {})
	if err == nil {
		t.Fatal("expected error when factory returns error")
	}
	if !containsStr(err.Error(), "configuration error") {
		t.Errorf("error = %q, want to contain original error", err.Error())
	}
}

func TestRegistry_GetFactory_NotFound(t *testing.T) {
	r := NewRegistry(silentLogger())
	if got := r.GetFactory("nonexistent"); got != nil {
		t.Errorf("GetFactory(nonexistent) should return nil, got non-nil")
	}
}

func TestRegistry_FactoryNames(t *testing.T) {
	r := NewRegistry(silentLogger())

	names := r.FactoryNames()
	if len(names) != 0 {
		t.Errorf("FactoryNames() on empty registry = %v, want empty", names)
	}

	factory := func(cb EventCallback) (Detector, error) {
		return &mockDetector{name: "A", severity: model.SeverityWarning}, nil
	}
	r.RegisterFactory("Alpha", factory)
	r.RegisterFactory("Beta", factory)

	names = r.FactoryNames()
	sort.Strings(names)
	if len(names) != 2 || names[0] != "Alpha" || names[1] != "Beta" {
		t.Errorf("FactoryNames() = %v, want [Alpha, Beta]", names)
	}
}

func TestRegistry_CreateFromFactory_DuplicateDetector(t *testing.T) {
	r := NewRegistry(silentLogger())

	// Register a detector directly first.
	d := &mockDetector{name: "Conflict", severity: model.SeverityWarning}
	if err := r.Register(d); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	// Register a factory that produces a detector with the same name.
	factory := func(cb EventCallback) (Detector, error) {
		return &mockDetector{name: "Conflict", severity: model.SeverityCritical}, nil
	}
	if err := r.RegisterFactory("Conflict", factory); err != nil {
		t.Fatalf("RegisterFactory() error = %v", err)
	}

	// CreateFromFactory should fail because the detector name is already taken.
	_, err := r.CreateFromFactory("Conflict", func(e *model.FaultEvent) {})
	if err == nil {
		t.Fatal("expected error when detector name already registered")
	}
	if !containsStr(err.Error(), "already registered") {
		t.Errorf("error = %q, want substring about already registered", err.Error())
	}
}
