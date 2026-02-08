package sink

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

// silentLogger returns a logger that discards all output.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// testReport returns a minimal RCAReport for testing.
func testReport() *model.RCAReport {
	return &model.RCAReport{
		FaultEventID:    "test-event-123",
		Timestamp:       time.Date(2026, 2, 8, 14, 30, 0, 0, time.UTC),
		RootCause:       "Pod OOMKilled due to memory limit",
		Severity:        model.SeverityCritical,
		Category:        "resources",
		Systemic:        false,
		BlastRadius:     "Single pod affected",
		Remediation:     []string{"Increase memory limit", "Check for memory leaks"},
		RelatedResources: []model.ResourceRef{
			{Kind: "Pod", Namespace: "default", Name: "test-pod", UID: "uid-123"},
		},
		Confidence:      0.95,
		AnalyzerBackend: "claude",
		TokensUsed:      model.TokenUsage{Input: 1000, Output: 200},
		DiagnosticBundle: model.DiagnosticBundle{
			FaultEvent: &model.FaultEvent{
				ID:           "test-event-123",
				DetectorName: "PodFailed",
				Severity:     model.SeverityCritical,
				Timestamp:    time.Date(2026, 2, 8, 14, 0, 0, 0, time.UTC),
				Resource: model.ResourceRef{
					Kind:      "Pod",
					Namespace: "default",
					Name:      "test-pod",
					UID:       "uid-123",
				},
				Description: "Pod failed with OOMKilled",
				Labels:      map[string]string{"app": "test"},
				Annotations: map[string]string{},
			},
			Timestamp: time.Date(2026, 2, 8, 14, 15, 0, 0, time.UTC),
		},
	}
}

// testSuperEventReport returns an RCAReport backed by a SuperEvent.
func testSuperEventReport() *model.RCAReport {
	return &model.RCAReport{
		FaultEventID:    "super-event-456",
		Timestamp:       time.Date(2026, 2, 8, 14, 30, 0, 0, time.UTC),
		RootCause:       "Node NotReady causing cascading pod failures",
		Severity:        model.SeverityCritical,
		Category:        "node",
		Systemic:        true,
		BlastRadius:     "All pods on node-1 affected",
		Remediation:     []string{"Investigate node health", "Cordon and drain node"},
		Confidence:      0.9,
		AnalyzerBackend: "claude",
		DiagnosticBundle: model.DiagnosticBundle{
			SuperEvent: &model.SuperEvent{
				ID:              "super-event-456",
				CorrelationRule: "NodeCascade",
				PrimaryResource: model.ResourceRef{
					Kind: "Node",
					Name: "node-1",
				},
				FaultEvents: []model.FaultEvent{
					{
						ID:           "fe-1",
						DetectorName: "PodFailed",
						Severity:     model.SeverityCritical,
						Resource:     model.ResourceRef{Kind: "Pod", Namespace: "default", Name: "pod-a", UID: "uid-a"},
					},
					{
						ID:           "fe-2",
						DetectorName: "PodFailed",
						Severity:     model.SeverityWarning,
						Resource:     model.ResourceRef{Kind: "Pod", Namespace: "default", Name: "pod-b", UID: "uid-b"},
					},
				},
				Timestamp: time.Date(2026, 2, 8, 14, 0, 0, 0, time.UTC),
				Severity:  model.SeverityCritical,
			},
			Timestamp: time.Date(2026, 2, 8, 14, 15, 0, 0, time.UTC),
		},
	}
}

// --- AcceptsSeverity tests ---

type fakeSink struct {
	name   string
	filter []model.Severity
}

func (f *fakeSink) Name() string                                                { return f.name }
func (f *fakeSink) Deliver(_ context.Context, _ *model.RCAReport) error         { return nil }
func (f *fakeSink) SeverityFilter() []model.Severity                            { return f.filter }

func TestAcceptsSeverity(t *testing.T) {
	tests := []struct {
		name     string
		filter   []model.Severity
		severity model.Severity
		want     bool
	}{
		{
			name:     "empty filter accepts all",
			filter:   nil,
			severity: model.SeverityCritical,
			want:     true,
		},
		{
			name:     "filter matches critical",
			filter:   []model.Severity{model.SeverityCritical, model.SeverityWarning},
			severity: model.SeverityCritical,
			want:     true,
		},
		{
			name:     "filter matches warning",
			filter:   []model.Severity{model.SeverityCritical, model.SeverityWarning},
			severity: model.SeverityWarning,
			want:     true,
		},
		{
			name:     "filter rejects info",
			filter:   []model.Severity{model.SeverityCritical, model.SeverityWarning},
			severity: model.SeverityInfo,
			want:     false,
		},
		{
			name:     "single filter",
			filter:   []model.Severity{model.SeverityCritical},
			severity: model.SeverityWarning,
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &fakeSink{filter: tt.filter}
			got := AcceptsSeverity(s, tt.severity)
			if got != tt.want {
				t.Errorf("AcceptsSeverity() = %v, want %v", got, tt.want)
			}
		})
	}
}

// --- deliverWithRetry tests ---

func TestDeliverWithRetry_Success(t *testing.T) {
	calls := 0
	err := deliverWithRetry(context.Background(), silentLogger(), "test", retryConfig{
		maxAttempts: 3,
		baseDelay:   1 * time.Millisecond,
		multiplier:  2.0,
	}, func(_ context.Context) error {
		calls++
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 1 {
		t.Errorf("expected 1 call, got %d", calls)
	}
}

func TestDeliverWithRetry_SuccessAfterRetries(t *testing.T) {
	calls := 0
	err := deliverWithRetry(context.Background(), silentLogger(), "test", retryConfig{
		maxAttempts: 3,
		baseDelay:   1 * time.Millisecond,
		multiplier:  2.0,
	}, func(_ context.Context) error {
		calls++
		if calls < 3 {
			return fmt.Errorf("transient error")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if calls != 3 {
		t.Errorf("expected 3 calls, got %d", calls)
	}
}

func TestDeliverWithRetry_AllAttemptsFail(t *testing.T) {
	calls := 0
	err := deliverWithRetry(context.Background(), silentLogger(), "test", retryConfig{
		maxAttempts: 3,
		baseDelay:   1 * time.Millisecond,
		multiplier:  2.0,
	}, func(_ context.Context) error {
		calls++
		return fmt.Errorf("permanent error")
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if calls != 3 {
		t.Errorf("expected 3 calls, got %d", calls)
	}
}

func TestDeliverWithRetry_ContextCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := deliverWithRetry(ctx, silentLogger(), "test", retryConfig{
		maxAttempts: 3,
		baseDelay:   1 * time.Second,
		multiplier:  2.0,
	}, func(_ context.Context) error {
		return fmt.Errorf("should not reach here")
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
