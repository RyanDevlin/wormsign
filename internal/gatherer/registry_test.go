package gatherer

import (
	"context"
	"sort"
	"testing"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

// simpleGatherer is a minimal Gatherer for registry tests.
type simpleGatherer struct {
	name string
}

func (s *simpleGatherer) Name() string { return s.name }
func (s *simpleGatherer) Gather(_ context.Context, _ model.ResourceRef) (*model.DiagnosticSection, error) {
	return &model.DiagnosticSection{GathererName: s.name}, nil
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry(silentLogger())

	g := &simpleGatherer{name: "TestGatherer"}
	if err := r.Register(g); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	got := r.Get("TestGatherer")
	if got == nil {
		t.Fatal("Get() returned nil")
	}
	if got.Name() != "TestGatherer" {
		t.Errorf("Get().Name() = %q, want %q", got.Name(), "TestGatherer")
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

	g1 := &simpleGatherer{name: "Dup"}
	g2 := &simpleGatherer{name: "Dup"}

	if err := r.Register(g1); err != nil {
		t.Fatalf("first Register() error = %v", err)
	}
	err := r.Register(g2)
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
		t.Fatal("expected error on nil gatherer")
	}
	if !containsStr(err.Error(), "must not be nil") {
		t.Errorf("error = %q, want substring %q", err.Error(), "must not be nil")
	}
}

func TestRegistry_RegisterEmptyName(t *testing.T) {
	r := NewRegistry(silentLogger())
	g := &simpleGatherer{name: ""}
	err := r.Register(g)
	if err == nil {
		t.Fatal("expected error on empty name")
	}
	if !containsStr(err.Error(), "name must not be empty") {
		t.Errorf("error = %q, want substring %q", err.Error(), "name must not be empty")
	}
}

func TestRegistry_All(t *testing.T) {
	r := NewRegistry(silentLogger())

	for _, name := range []string{"A", "B", "C"} {
		if err := r.Register(&simpleGatherer{name: name}); err != nil {
			t.Fatalf("Register(%s) error = %v", name, err)
		}
	}

	all := r.All()
	if len(all) != 3 {
		t.Fatalf("All() returned %d gatherers, want 3", len(all))
	}
}

func TestRegistry_Count(t *testing.T) {
	r := NewRegistry(silentLogger())
	if r.Count() != 0 {
		t.Errorf("Count() = %d, want 0", r.Count())
	}

	r.Register(&simpleGatherer{name: "A"})
	if r.Count() != 1 {
		t.Errorf("Count() = %d, want 1", r.Count())
	}

	r.Register(&simpleGatherer{name: "B"})
	if r.Count() != 2 {
		t.Errorf("Count() = %d, want 2", r.Count())
	}
}

func TestRegistry_Names(t *testing.T) {
	r := NewRegistry(silentLogger())
	r.Register(&simpleGatherer{name: "Beta"})
	r.Register(&simpleGatherer{name: "Alpha"})

	names := r.Names()
	sort.Strings(names)
	if len(names) != 2 || names[0] != "Alpha" || names[1] != "Beta" {
		t.Errorf("Names() = %v, want [Alpha, Beta]", names)
	}
}

func TestRegistry_Unregister(t *testing.T) {
	r := NewRegistry(silentLogger())
	r.Register(&simpleGatherer{name: "ToRemove"})

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
		t.Fatal("expected error on unregistering nonexistent gatherer")
	}
	if !containsStr(err.Error(), "not registered") {
		t.Errorf("error = %q, want substring %q", err.Error(), "not registered")
	}
}

func TestRegistry_NilLogger(t *testing.T) {
	r := NewRegistry(nil)
	if r == nil {
		t.Fatal("NewRegistry(nil) returned nil")
	}
}

// containsStr reports whether s contains substr.
func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
