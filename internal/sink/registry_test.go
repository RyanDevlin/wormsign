package sink

import (
	"context"
	"fmt"
	"testing"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

func TestRegistry_Register(t *testing.T) {
	r := NewRegistry(silentLogger(), nil)

	s := &fakeSink{name: "test-sink"}
	if err := r.Register(s); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	if r.Count() != 1 {
		t.Errorf("Count() = %d, want 1", r.Count())
	}
}

func TestRegistry_RegisterNil(t *testing.T) {
	r := NewRegistry(silentLogger(), nil)
	err := r.Register(nil)
	if err == nil {
		t.Fatal("Register(nil) should return error")
	}
}

func TestRegistry_RegisterEmptyName(t *testing.T) {
	r := NewRegistry(silentLogger(), nil)
	err := r.Register(&fakeSink{name: ""})
	if err == nil {
		t.Fatal("Register with empty name should return error")
	}
}

func TestRegistry_RegisterDuplicate(t *testing.T) {
	r := NewRegistry(silentLogger(), nil)
	s := &fakeSink{name: "test-sink"}

	if err := r.Register(s); err != nil {
		t.Fatalf("first Register() error = %v", err)
	}

	err := r.Register(s)
	if err == nil {
		t.Fatal("duplicate Register() should return error")
	}
}

func TestRegistry_Get(t *testing.T) {
	r := NewRegistry(silentLogger(), nil)
	s := &fakeSink{name: "test-sink"}
	r.Register(s)

	got := r.Get("test-sink")
	if got == nil {
		t.Fatal("Get() returned nil")
	}
	if got.Name() != "test-sink" {
		t.Errorf("Get().Name() = %q, want %q", got.Name(), "test-sink")
	}

	got = r.Get("nonexistent")
	if got != nil {
		t.Errorf("Get(nonexistent) should return nil, got %v", got)
	}
}

func TestRegistry_Unregister(t *testing.T) {
	r := NewRegistry(silentLogger(), nil)
	s := &fakeSink{name: "test-sink"}
	r.Register(s)

	if err := r.Unregister("test-sink"); err != nil {
		t.Fatalf("Unregister() error = %v", err)
	}
	if r.Count() != 0 {
		t.Errorf("Count() = %d after unregister, want 0", r.Count())
	}
}

func TestRegistry_UnregisterNonexistent(t *testing.T) {
	r := NewRegistry(silentLogger(), nil)
	err := r.Unregister("nonexistent")
	if err == nil {
		t.Fatal("Unregister(nonexistent) should return error")
	}
}

func TestRegistry_All(t *testing.T) {
	r := NewRegistry(silentLogger(), nil)
	r.Register(&fakeSink{name: "a"})
	r.Register(&fakeSink{name: "b"})
	r.Register(&fakeSink{name: "c"})

	all := r.All()
	if len(all) != 3 {
		t.Errorf("All() returned %d sinks, want 3", len(all))
	}
}

func TestRegistry_Names(t *testing.T) {
	r := NewRegistry(silentLogger(), nil)
	r.Register(&fakeSink{name: "a"})
	r.Register(&fakeSink{name: "b"})

	names := r.Names()
	if len(names) != 2 {
		t.Errorf("Names() returned %d names, want 2", len(names))
	}
}

// deliverableSink tracks calls for testing DeliverAll.
type deliverableSink struct {
	name       string
	filter     []model.Severity
	delivered  int
	shouldFail bool
}

func (d *deliverableSink) Name() string                       { return d.name }
func (d *deliverableSink) SeverityFilter() []model.Severity   { return d.filter }
func (d *deliverableSink) Deliver(_ context.Context, _ *model.RCAReport) error {
	d.delivered++
	if d.shouldFail {
		return fmt.Errorf("delivery failed")
	}
	return nil
}

func TestRegistry_DeliverAll(t *testing.T) {
	r := NewRegistry(silentLogger(), nil)

	s1 := &deliverableSink{name: "sink-1"}
	s2 := &deliverableSink{name: "sink-2"}
	r.Register(s1)
	r.Register(s2)

	report := testReport()
	failures := r.DeliverAll(context.Background(), report)
	if failures != 0 {
		t.Errorf("DeliverAll() failures = %d, want 0", failures)
	}
	if s1.delivered != 1 {
		t.Errorf("sink-1 delivered = %d, want 1", s1.delivered)
	}
	if s2.delivered != 1 {
		t.Errorf("sink-2 delivered = %d, want 1", s2.delivered)
	}
}

func TestRegistry_DeliverAll_SeverityFilter(t *testing.T) {
	r := NewRegistry(silentLogger(), nil)

	criticalOnly := &deliverableSink{
		name:   "critical-only",
		filter: []model.Severity{model.SeverityCritical},
	}
	allSeverities := &deliverableSink{name: "all"}
	r.Register(criticalOnly)
	r.Register(allSeverities)

	report := testReport()
	report.Severity = model.SeverityInfo

	r.DeliverAll(context.Background(), report)

	if criticalOnly.delivered != 0 {
		t.Errorf("critical-only sink should not have delivered, got %d", criticalOnly.delivered)
	}
	if allSeverities.delivered != 1 {
		t.Errorf("all-severity sink should have delivered once, got %d", allSeverities.delivered)
	}
}

func TestRegistry_DeliverAll_PartialFailure(t *testing.T) {
	r := NewRegistry(silentLogger(), nil)

	good := &deliverableSink{name: "good"}
	bad := &deliverableSink{name: "bad", shouldFail: true}
	r.Register(good)
	r.Register(bad)

	report := testReport()
	failures := r.DeliverAll(context.Background(), report)
	if failures != 1 {
		t.Errorf("DeliverAll() failures = %d, want 1", failures)
	}
	// Both sinks should have been attempted.
	if good.delivered != 1 {
		t.Errorf("good sink delivered = %d, want 1", good.delivered)
	}
	if bad.delivered != 1 {
		t.Errorf("bad sink delivered = %d, want 1", bad.delivered)
	}
}

func TestRegistry_DeliverAll_NilReport(t *testing.T) {
	r := NewRegistry(silentLogger(), nil)
	failures := r.DeliverAll(context.Background(), nil)
	if failures != 0 {
		t.Errorf("DeliverAll(nil) failures = %d, want 0", failures)
	}
}

func TestRegistry_NilLogger(t *testing.T) {
	r := NewRegistry(nil, nil)
	if r == nil {
		t.Fatal("NewRegistry with nil logger should use default")
	}
}
