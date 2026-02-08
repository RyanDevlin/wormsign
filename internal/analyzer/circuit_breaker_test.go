package analyzer

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

// mockAnalyzer is a test double that can be configured to succeed or fail.
type mockAnalyzer struct {
	name      string
	healthy   bool
	err       error
	callCount int
	report    *model.RCAReport
}

func (m *mockAnalyzer) Name() string { return m.name }

func (m *mockAnalyzer) Analyze(ctx context.Context, bundle model.DiagnosticBundle) (*model.RCAReport, error) {
	m.callCount++
	if m.err != nil {
		return nil, m.err
	}
	if m.report != nil {
		return m.report, nil
	}
	return &model.RCAReport{
		FaultEventID:    faultEventID(bundle),
		AnalyzerBackend: m.name,
		Severity:        model.SeverityInfo,
		Category:        "unknown",
		Remediation:     []string{},
	}, nil
}

func (m *mockAnalyzer) Healthy(ctx context.Context) bool { return m.healthy }

func TestCircuitState_String(t *testing.T) {
	tests := []struct {
		state CircuitState
		want  string
	}{
		{CircuitClosed, "closed"},
		{CircuitOpen, "open"},
		{CircuitHalfOpen, "half-open"},
		{CircuitState(99), "unknown(99)"},
	}
	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			if got := tt.state.String(); got != tt.want {
				t.Errorf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestNewCircuitBreaker_Validation(t *testing.T) {
	primary := &mockAnalyzer{name: "primary", healthy: true}
	fallback := &mockAnalyzer{name: "fallback", healthy: true}
	logger := testLogger()

	tests := []struct {
		name     string
		primary  Analyzer
		fallback Analyzer
		cfg      CircuitBreakerConfig
		wantErr  string
	}{
		{
			name:     "nil primary",
			primary:  nil,
			fallback: fallback,
			cfg:      CircuitBreakerConfig{ConsecutiveFailures: 3, OpenDuration: time.Minute},
			wantErr:  "primary analyzer must not be nil",
		},
		{
			name:     "nil fallback",
			primary:  primary,
			fallback: nil,
			cfg:      CircuitBreakerConfig{ConsecutiveFailures: 3, OpenDuration: time.Minute},
			wantErr:  "fallback analyzer must not be nil",
		},
		{
			name:     "zero consecutive failures",
			primary:  primary,
			fallback: fallback,
			cfg:      CircuitBreakerConfig{ConsecutiveFailures: 0, OpenDuration: time.Minute},
			wantErr:  "consecutiveFailures must be >= 1",
		},
		{
			name:     "zero open duration",
			primary:  primary,
			fallback: fallback,
			cfg:      CircuitBreakerConfig{ConsecutiveFailures: 3, OpenDuration: 0},
			wantErr:  "openDuration must be > 0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewCircuitBreaker(tt.primary, tt.fallback, tt.cfg, logger)
			if err == nil {
				t.Fatal("expected error")
			}
			if !containsSubstring(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestNewCircuitBreaker_NilLogger(t *testing.T) {
	primary := &mockAnalyzer{name: "primary", healthy: true}
	fallback := &mockAnalyzer{name: "fallback", healthy: true}
	_, err := NewCircuitBreaker(primary, fallback, CircuitBreakerConfig{ConsecutiveFailures: 3, OpenDuration: time.Minute}, nil)
	if err == nil {
		t.Fatal("expected error for nil logger")
	}
}

func TestCircuitBreaker_StartsInClosedState(t *testing.T) {
	primary := &mockAnalyzer{name: "primary", healthy: true}
	fallback := &mockAnalyzer{name: "fallback", healthy: true}
	cb, err := NewCircuitBreaker(primary, fallback,
		CircuitBreakerConfig{ConsecutiveFailures: 3, OpenDuration: time.Minute},
		testLogger())
	if err != nil {
		t.Fatalf("NewCircuitBreaker() error = %v", err)
	}
	if cb.State() != CircuitClosed {
		t.Errorf("initial state = %v, want %v", cb.State(), CircuitClosed)
	}
}

func TestCircuitBreaker_Name(t *testing.T) {
	primary := &mockAnalyzer{name: "claude-bedrock", healthy: true}
	fallback := &mockAnalyzer{name: "noop", healthy: true}
	cb, err := NewCircuitBreaker(primary, fallback,
		CircuitBreakerConfig{ConsecutiveFailures: 3, OpenDuration: time.Minute},
		testLogger())
	if err != nil {
		t.Fatalf("NewCircuitBreaker() error = %v", err)
	}
	if cb.Name() != "claude-bedrock" {
		t.Errorf("Name() = %q, want %q", cb.Name(), "claude-bedrock")
	}
}

func TestCircuitBreaker_ClosedToOpen(t *testing.T) {
	primary := &mockAnalyzer{name: "primary", healthy: true, err: fmt.Errorf("LLM error")}
	fallback := &mockAnalyzer{name: "fallback", healthy: true}
	cb, err := NewCircuitBreaker(primary, fallback,
		CircuitBreakerConfig{ConsecutiveFailures: 3, OpenDuration: time.Minute},
		testLogger())
	if err != nil {
		t.Fatalf("NewCircuitBreaker() error = %v", err)
	}

	bundle := testBundle()

	// First two failures: circuit stays closed.
	for i := 0; i < 2; i++ {
		_, err := cb.Analyze(context.Background(), bundle)
		if err == nil {
			t.Fatalf("call %d: expected error", i+1)
		}
		if cb.State() != CircuitClosed {
			t.Errorf("after %d failures, state = %v, want closed", i+1, cb.State())
		}
	}

	// Third failure: circuit opens.
	_, err = cb.Analyze(context.Background(), bundle)
	if err == nil {
		t.Fatal("expected error on third failure")
	}
	if cb.State() != CircuitOpen {
		t.Errorf("after 3 failures, state = %v, want open", cb.State())
	}
}

func TestCircuitBreaker_OpenUsesFallback(t *testing.T) {
	primary := &mockAnalyzer{name: "primary", healthy: true, err: fmt.Errorf("LLM error")}
	fallback := &mockAnalyzer{name: "fallback", healthy: true}
	cb, err := NewCircuitBreaker(primary, fallback,
		CircuitBreakerConfig{ConsecutiveFailures: 1, OpenDuration: time.Minute},
		testLogger())
	if err != nil {
		t.Fatalf("NewCircuitBreaker() error = %v", err)
	}

	bundle := testBundle()

	// First failure opens the circuit.
	_, err = cb.Analyze(context.Background(), bundle)
	if err == nil {
		t.Fatal("expected error")
	}
	if cb.State() != CircuitOpen {
		t.Fatalf("state = %v, want open", cb.State())
	}

	// Next call should use fallback.
	report, err := cb.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("expected fallback to succeed, got error: %v", err)
	}
	if report.AnalyzerBackend != "fallback" {
		t.Errorf("AnalyzerBackend = %q, want %q", report.AnalyzerBackend, "fallback")
	}
	if fallback.callCount < 1 {
		t.Error("fallback should have been called")
	}
}

func TestCircuitBreaker_OpenToHalfOpen(t *testing.T) {
	primary := &mockAnalyzer{name: "primary", healthy: true, err: fmt.Errorf("LLM error")}
	fallback := &mockAnalyzer{name: "fallback", healthy: true}
	cb, err := NewCircuitBreaker(primary, fallback,
		CircuitBreakerConfig{ConsecutiveFailures: 1, OpenDuration: 5 * time.Minute},
		testLogger())
	if err != nil {
		t.Fatalf("NewCircuitBreaker() error = %v", err)
	}

	bundle := testBundle()

	// Open the circuit.
	_, _ = cb.Analyze(context.Background(), bundle)
	if cb.State() != CircuitOpen {
		t.Fatalf("state = %v, want open", cb.State())
	}

	// Advance time past the open duration.
	now := time.Now()
	cb.nowFunc = func() time.Time { return now.Add(6 * time.Minute) }

	// State should now be half-open.
	if cb.State() != CircuitHalfOpen {
		t.Errorf("state = %v, want half-open", cb.State())
	}
}

func TestCircuitBreaker_HalfOpenToClosed(t *testing.T) {
	primary := &mockAnalyzer{name: "primary", healthy: true, err: fmt.Errorf("LLM error")}
	fallback := &mockAnalyzer{name: "fallback", healthy: true}
	cb, err := NewCircuitBreaker(primary, fallback,
		CircuitBreakerConfig{ConsecutiveFailures: 1, OpenDuration: 5 * time.Minute},
		testLogger())
	if err != nil {
		t.Fatalf("NewCircuitBreaker() error = %v", err)
	}

	bundle := testBundle()

	// Open the circuit.
	_, _ = cb.Analyze(context.Background(), bundle)

	// Advance time to get half-open.
	now := time.Now()
	cb.nowFunc = func() time.Time { return now.Add(6 * time.Minute) }

	// Make primary succeed now.
	primary.err = nil

	// Probe request should succeed and close the circuit.
	report, err := cb.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("probe request error: %v", err)
	}
	if report.AnalyzerBackend != "primary" {
		t.Errorf("AnalyzerBackend = %q, want %q", report.AnalyzerBackend, "primary")
	}
	if cb.State() != CircuitClosed {
		t.Errorf("state = %v, want closed", cb.State())
	}
}

func TestCircuitBreaker_HalfOpenToOpen(t *testing.T) {
	primary := &mockAnalyzer{name: "primary", healthy: true, err: fmt.Errorf("LLM error")}
	fallback := &mockAnalyzer{name: "fallback", healthy: true}
	cb, err := NewCircuitBreaker(primary, fallback,
		CircuitBreakerConfig{ConsecutiveFailures: 1, OpenDuration: 5 * time.Minute},
		testLogger())
	if err != nil {
		t.Fatalf("NewCircuitBreaker() error = %v", err)
	}

	bundle := testBundle()

	// Open the circuit.
	_, _ = cb.Analyze(context.Background(), bundle)

	// Advance time to get half-open.
	openedAt := time.Now()
	cb.nowFunc = func() time.Time { return openedAt.Add(6 * time.Minute) }

	// primary still fails — should re-open and fallback is returned.
	report, err := cb.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("expected fallback, got error: %v", err)
	}
	if report.AnalyzerBackend != "fallback" {
		t.Errorf("AnalyzerBackend = %q, want %q", report.AnalyzerBackend, "fallback")
	}
	if cb.State() != CircuitOpen {
		t.Errorf("state = %v, want open", cb.State())
	}
}

func TestCircuitBreaker_SuccessResetsFailureCount(t *testing.T) {
	callNum := 0
	primary := &mockAnalyzer{name: "primary", healthy: true}
	// Make it fail on first 2 calls, then succeed.
	origAnalyze := primary.Analyze
	_ = origAnalyze

	// Use a custom primary that alternates.
	alternating := &alternatingAnalyzer{
		name:      "primary",
		failUntil: 2,
	}
	fallback := &mockAnalyzer{name: "fallback", healthy: true}
	cb, err := NewCircuitBreaker(alternating, fallback,
		CircuitBreakerConfig{ConsecutiveFailures: 3, OpenDuration: time.Minute},
		testLogger())
	if err != nil {
		t.Fatalf("NewCircuitBreaker() error = %v", err)
	}

	bundle := testBundle()
	_ = callNum

	// Two failures.
	_, _ = cb.Analyze(context.Background(), bundle)
	_, _ = cb.Analyze(context.Background(), bundle)

	// Third call succeeds (callCount is now 3, which is > failUntil of 2).
	report, err := cb.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	if report == nil {
		t.Fatal("expected non-nil report")
	}

	// Circuit should be closed and failure count reset.
	if cb.State() != CircuitClosed {
		t.Errorf("state = %v, want closed", cb.State())
	}

	// Another two failures should not open the circuit (count was reset).
	alternating.failUntil = 100
	_, _ = cb.Analyze(context.Background(), bundle)
	_, _ = cb.Analyze(context.Background(), bundle)
	if cb.State() != CircuitClosed {
		t.Errorf("state = %v, want closed (count was reset)", cb.State())
	}
}

func TestCircuitBreaker_Healthy_Open(t *testing.T) {
	primary := &mockAnalyzer{name: "primary", healthy: true, err: fmt.Errorf("fail")}
	fallback := &mockAnalyzer{name: "fallback", healthy: true}
	cb, err := NewCircuitBreaker(primary, fallback,
		CircuitBreakerConfig{ConsecutiveFailures: 1, OpenDuration: time.Minute},
		testLogger())
	if err != nil {
		t.Fatalf("NewCircuitBreaker() error = %v", err)
	}

	bundle := testBundle()
	_, _ = cb.Analyze(context.Background(), bundle)

	if cb.Healthy(context.Background()) {
		t.Error("Healthy() should be false when circuit is open")
	}
}

func TestCircuitBreaker_Healthy_Closed(t *testing.T) {
	primary := &mockAnalyzer{name: "primary", healthy: true}
	fallback := &mockAnalyzer{name: "fallback", healthy: true}
	cb, err := NewCircuitBreaker(primary, fallback,
		CircuitBreakerConfig{ConsecutiveFailures: 3, OpenDuration: time.Minute},
		testLogger())
	if err != nil {
		t.Fatalf("NewCircuitBreaker() error = %v", err)
	}

	if !cb.Healthy(context.Background()) {
		t.Error("Healthy() should be true when circuit is closed")
	}
}

func TestCircuitBreaker_Healthy_DelegatesToPrimary(t *testing.T) {
	primary := &mockAnalyzer{name: "primary", healthy: false}
	fallback := &mockAnalyzer{name: "fallback", healthy: true}
	cb, err := NewCircuitBreaker(primary, fallback,
		CircuitBreakerConfig{ConsecutiveFailures: 3, OpenDuration: time.Minute},
		testLogger())
	if err != nil {
		t.Fatalf("NewCircuitBreaker() error = %v", err)
	}

	if cb.Healthy(context.Background()) {
		t.Error("Healthy() should be false when primary is unhealthy")
	}
}

// alternatingAnalyzer fails for the first N calls, then succeeds.
type alternatingAnalyzer struct {
	name      string
	callCount int
	failUntil int
}

func (a *alternatingAnalyzer) Name() string { return a.name }

func (a *alternatingAnalyzer) Analyze(ctx context.Context, bundle model.DiagnosticBundle) (*model.RCAReport, error) {
	a.callCount++
	if a.callCount <= a.failUntil {
		return nil, fmt.Errorf("call %d: simulated failure", a.callCount)
	}
	return &model.RCAReport{
		FaultEventID:    faultEventID(bundle),
		AnalyzerBackend: a.name,
		Severity:        model.SeverityInfo,
		Category:        "unknown",
		Remediation:     []string{},
	}, nil
}

func (a *alternatingAnalyzer) Healthy(ctx context.Context) bool { return true }

// testBundle creates a minimal DiagnosticBundle for testing.
func testBundle() model.DiagnosticBundle {
	return model.DiagnosticBundle{
		FaultEvent: &model.FaultEvent{
			ID:           "test-fe-1",
			DetectorName: "PodCrashLoop",
			Severity:     model.SeverityWarning,
			Timestamp:    time.Now().UTC(),
			Resource: model.ResourceRef{
				Kind:      "Pod",
				Namespace: "default",
				Name:      "test-pod",
			},
		},
		Timestamp: time.Now().UTC(),
		Sections:  []model.DiagnosticSection{},
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
