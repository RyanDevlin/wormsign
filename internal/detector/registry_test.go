package detector

import (
	"context"
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
