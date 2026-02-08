package analyzer

import (
	"context"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
}

func TestNewNoopAnalyzer_NilLogger(t *testing.T) {
	_, err := NewNoopAnalyzer(nil)
	if err == nil {
		t.Fatal("expected error for nil logger")
	}
}

func TestNoopAnalyzer_Name(t *testing.T) {
	a, err := NewNoopAnalyzer(testLogger())
	if err != nil {
		t.Fatalf("NewNoopAnalyzer() error = %v", err)
	}
	if a.Name() != "noop" {
		t.Errorf("Name() = %q, want %q", a.Name(), "noop")
	}
}

func TestNoopAnalyzer_Healthy(t *testing.T) {
	a, err := NewNoopAnalyzer(testLogger())
	if err != nil {
		t.Fatalf("NewNoopAnalyzer() error = %v", err)
	}
	if !a.Healthy(context.Background()) {
		t.Error("Healthy() = false, want true")
	}
}

func TestNoopAnalyzer_Analyze_FaultEvent(t *testing.T) {
	a, err := NewNoopAnalyzer(testLogger())
	if err != nil {
		t.Fatalf("NewNoopAnalyzer() error = %v", err)
	}

	fe := &model.FaultEvent{
		ID:           "fe-123",
		DetectorName: "PodCrashLoop",
		Severity:     model.SeverityCritical,
		Timestamp:    time.Now().UTC(),
		Resource: model.ResourceRef{
			Kind:      "Pod",
			Namespace: "default",
			Name:      "test-pod",
		},
		Description: "pod is crash looping",
	}
	bundle := model.DiagnosticBundle{
		FaultEvent: fe,
		Timestamp:  time.Now().UTC(),
		Sections:   []model.DiagnosticSection{},
	}

	before := time.Now().UTC()
	report, err := a.Analyze(context.Background(), bundle)
	after := time.Now().UTC()

	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if report == nil {
		t.Fatal("Analyze() returned nil report")
	}
	if report.FaultEventID != "fe-123" {
		t.Errorf("FaultEventID = %q, want %q", report.FaultEventID, "fe-123")
	}
	if report.Severity != model.SeverityCritical {
		t.Errorf("Severity = %q, want %q", report.Severity, model.SeverityCritical)
	}
	if report.Category != "unknown" {
		t.Errorf("Category = %q, want %q", report.Category, "unknown")
	}
	if report.AnalyzerBackend != "noop" {
		t.Errorf("AnalyzerBackend = %q, want %q", report.AnalyzerBackend, "noop")
	}
	if report.Confidence != 0.0 {
		t.Errorf("Confidence = %f, want 0.0", report.Confidence)
	}
	if report.RootCause == "" {
		t.Error("RootCause should not be empty")
	}
	if report.Timestamp.Before(before) || report.Timestamp.After(after) {
		t.Errorf("Timestamp %v not in expected range", report.Timestamp)
	}
	if report.Remediation == nil {
		t.Error("Remediation should not be nil")
	}
	if report.RelatedResources == nil {
		t.Error("RelatedResources should not be nil")
	}
}

func TestNoopAnalyzer_Analyze_SuperEvent(t *testing.T) {
	a, err := NewNoopAnalyzer(testLogger())
	if err != nil {
		t.Fatalf("NewNoopAnalyzer() error = %v", err)
	}

	se := &model.SuperEvent{
		ID:              "se-456",
		CorrelationRule: "NodeCascade",
		Severity:        model.SeverityWarning,
	}
	bundle := model.DiagnosticBundle{
		SuperEvent: se,
		Timestamp:  time.Now().UTC(),
		Sections:   []model.DiagnosticSection{},
	}

	report, err := a.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if report.FaultEventID != "se-456" {
		t.Errorf("FaultEventID = %q, want %q", report.FaultEventID, "se-456")
	}
	if report.Severity != model.SeverityWarning {
		t.Errorf("Severity = %q, want %q", report.Severity, model.SeverityWarning)
	}
}

func TestNoopAnalyzer_Analyze_NeitherEventSet(t *testing.T) {
	a, err := NewNoopAnalyzer(testLogger())
	if err != nil {
		t.Fatalf("NewNoopAnalyzer() error = %v", err)
	}

	bundle := model.DiagnosticBundle{
		Timestamp: time.Now().UTC(),
		Sections:  []model.DiagnosticSection{},
	}

	_, err = a.Analyze(context.Background(), bundle)
	if err == nil {
		t.Fatal("expected error when neither FaultEvent nor SuperEvent is set")
	}
}

func TestNoopAnalyzer_Analyze_CancelledContext(t *testing.T) {
	a, err := NewNoopAnalyzer(testLogger())
	if err != nil {
		t.Fatalf("NewNoopAnalyzer() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	bundle := model.DiagnosticBundle{
		FaultEvent: &model.FaultEvent{ID: "fe-1", Severity: model.SeverityInfo},
	}

	_, err = a.Analyze(ctx, bundle)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}
