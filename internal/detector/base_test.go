package detector

import (
	"log/slog"
	"sync"
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

// collectCallback returns a callback and a way to read the collected events.
func collectCallback() (EventCallback, func() []*model.FaultEvent) {
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

func TestNewBaseDetector_Validation(t *testing.T) {
	cb, _ := collectCallback()

	tests := []struct {
		name    string
		cfg     BaseDetectorConfig
		wantErr string
	}{
		{
			name:    "empty name",
			cfg:     BaseDetectorConfig{Name: "", Severity: model.SeverityWarning, Cooldown: time.Minute, Callback: cb},
			wantErr: "detector name must not be empty",
		},
		{
			name:    "invalid severity",
			cfg:     BaseDetectorConfig{Name: "Test", Severity: "bogus", Cooldown: time.Minute, Callback: cb},
			wantErr: "invalid severity",
		},
		{
			name:    "zero cooldown",
			cfg:     BaseDetectorConfig{Name: "Test", Severity: model.SeverityWarning, Cooldown: 0, Callback: cb},
			wantErr: "cooldown must be positive",
		},
		{
			name:    "negative cooldown",
			cfg:     BaseDetectorConfig{Name: "Test", Severity: model.SeverityWarning, Cooldown: -time.Second, Callback: cb},
			wantErr: "cooldown must be positive",
		},
		{
			name:    "nil callback",
			cfg:     BaseDetectorConfig{Name: "Test", Severity: model.SeverityWarning, Cooldown: time.Minute, Callback: nil},
			wantErr: "callback must not be nil",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewBaseDetector(tt.cfg)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !containsStr(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestNewBaseDetector_Success(t *testing.T) {
	cb, _ := collectCallback()
	base, err := NewBaseDetector(BaseDetectorConfig{
		Name:     "TestDetector",
		Severity: model.SeverityWarning,
		Cooldown: 30 * time.Minute,
		Callback: cb,
		Logger:   silentLogger(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if base.DetectorName() != "TestDetector" {
		t.Errorf("Name = %q, want %q", base.DetectorName(), "TestDetector")
	}
	if base.DetectorSeverity() != model.SeverityWarning {
		t.Errorf("Severity = %q, want %q", base.DetectorSeverity(), model.SeverityWarning)
	}
}

func TestBaseDetector_Emit(t *testing.T) {
	cb, getEvents := collectCallback()
	base, err := NewBaseDetector(BaseDetectorConfig{
		Name:     "TestEmit",
		Severity: model.SeverityCritical,
		Cooldown: 10 * time.Minute,
		Callback: cb,
		Logger:   silentLogger(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ref := model.ResourceRef{Kind: "Pod", Namespace: "default", Name: "test-pod", UID: "uid-1"}
	emitted := base.Emit(ref, "test fault", nil, nil)
	if !emitted {
		t.Error("expected event to be emitted")
	}

	events := getEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].DetectorName != "TestEmit" {
		t.Errorf("DetectorName = %q, want %q", events[0].DetectorName, "TestEmit")
	}
	if events[0].Severity != model.SeverityCritical {
		t.Errorf("Severity = %q, want %q", events[0].Severity, model.SeverityCritical)
	}
}

func TestBaseDetector_CooldownDedup(t *testing.T) {
	cb, getEvents := collectCallback()

	now := time.Date(2026, 2, 8, 12, 0, 0, 0, time.UTC)
	base, err := NewBaseDetector(BaseDetectorConfig{
		Name:     "TestCooldown",
		Severity: model.SeverityWarning,
		Cooldown: 30 * time.Minute,
		Callback: cb,
		Logger:   silentLogger(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	base.SetNowFunc(func() time.Time { return now })

	ref := model.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "pod-1", UID: "uid-1"}

	// First emit should succeed.
	if !base.Emit(ref, "first", nil, nil) {
		t.Error("first emit should succeed")
	}

	// Second emit within cooldown should be suppressed.
	now = now.Add(10 * time.Minute)
	base.SetNowFunc(func() time.Time { return now })
	if base.Emit(ref, "second", nil, nil) {
		t.Error("second emit within cooldown should be suppressed")
	}

	// After cooldown, emit should succeed again.
	now = now.Add(25 * time.Minute)
	base.SetNowFunc(func() time.Time { return now })
	if !base.Emit(ref, "third", nil, nil) {
		t.Error("third emit after cooldown should succeed")
	}

	events := getEvents()
	if len(events) != 2 {
		t.Errorf("expected 2 events, got %d", len(events))
	}
}

func TestBaseDetector_DedupByResourceUID(t *testing.T) {
	cb, getEvents := collectCallback()
	base, err := NewBaseDetector(BaseDetectorConfig{
		Name:     "TestDedup",
		Severity: model.SeverityWarning,
		Cooldown: 30 * time.Minute,
		Callback: cb,
		Logger:   silentLogger(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ref1 := model.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "pod-1", UID: "uid-1"}
	ref2 := model.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "pod-2", UID: "uid-2"}

	// Both should emit (different UIDs).
	if !base.Emit(ref1, "fault 1", nil, nil) {
		t.Error("first resource should emit")
	}
	if !base.Emit(ref2, "fault 2", nil, nil) {
		t.Error("second resource (different UID) should emit")
	}

	// Same UID again should be suppressed.
	if base.Emit(ref1, "fault 1 again", nil, nil) {
		t.Error("same UID within cooldown should be suppressed")
	}

	events := getEvents()
	if len(events) != 2 {
		t.Errorf("expected 2 events, got %d", len(events))
	}
}

func TestBaseDetector_CleanupExpiredEntries(t *testing.T) {
	cb, _ := collectCallback()
	now := time.Date(2026, 2, 8, 12, 0, 0, 0, time.UTC)
	base, err := NewBaseDetector(BaseDetectorConfig{
		Name:     "TestCleanup",
		Severity: model.SeverityWarning,
		Cooldown: 10 * time.Minute,
		Callback: cb,
		Logger:   silentLogger(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	base.SetNowFunc(func() time.Time { return now })

	ref := model.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "pod-1", UID: "uid-1"}
	base.Emit(ref, "fault", nil, nil)

	if base.DedupEntryCount() != 1 {
		t.Errorf("expected 1 dedup entry, got %d", base.DedupEntryCount())
	}

	// Move past 2x cooldown (cleanup threshold).
	now = now.Add(25 * time.Minute)
	base.SetNowFunc(func() time.Time { return now })
	base.CleanupExpiredEntries()

	if base.DedupEntryCount() != 0 {
		t.Errorf("expected 0 dedup entries after cleanup, got %d", base.DedupEntryCount())
	}
}

func TestBaseDetector_NilLogger(t *testing.T) {
	cb, _ := collectCallback()
	base, err := NewBaseDetector(BaseDetectorConfig{
		Name:     "TestNilLogger",
		Severity: model.SeverityWarning,
		Cooldown: time.Minute,
		Callback: cb,
		Logger:   nil, // should use default
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if base.Logger() == nil {
		t.Error("logger should not be nil")
	}
}

// containsStr checks if s contains substr.
func containsStr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
