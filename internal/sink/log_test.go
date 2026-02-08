package sink

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

func TestNewLogSink_NilLogger(t *testing.T) {
	_, err := NewLogSink(nil)
	if err == nil {
		t.Fatal("NewLogSink(nil) should return error")
	}
}

func TestLogSink_Name(t *testing.T) {
	s, _ := NewLogSink(silentLogger())
	if s.Name() != "log" {
		t.Errorf("Name() = %q, want %q", s.Name(), "log")
	}
}

func TestLogSink_SeverityFilter(t *testing.T) {
	s, _ := NewLogSink(silentLogger())
	if f := s.SeverityFilter(); f != nil {
		t.Errorf("SeverityFilter() = %v, want nil", f)
	}
}

func TestLogSink_Deliver(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	s, err := NewLogSink(logger)
	if err != nil {
		t.Fatalf("NewLogSink error: %v", err)
	}

	report := testReport()
	err = s.Deliver(context.Background(), report)
	if err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}

	output := buf.String()
	if output == "" {
		t.Fatal("expected log output, got empty")
	}

	// Verify the log line is valid JSON.
	var logEntry map[string]interface{}
	if err := json.Unmarshal([]byte(strings.TrimSpace(output)), &logEntry); err != nil {
		t.Fatalf("log output is not valid JSON: %v", err)
	}

	// Verify key fields are present.
	expectedFields := []string{
		"fault_event_id",
		"root_cause",
		"severity",
		"category",
		"confidence",
		"analyzer_backend",
	}
	for _, field := range expectedFields {
		if _, ok := logEntry[field]; !ok {
			t.Errorf("missing field %q in log output", field)
		}
	}

	if logEntry["fault_event_id"] != "test-event-123" {
		t.Errorf("fault_event_id = %v, want %q", logEntry["fault_event_id"], "test-event-123")
	}
	if logEntry["severity"] != "critical" {
		t.Errorf("severity = %v, want %q", logEntry["severity"], "critical")
	}
}

func TestLogSink_Deliver_NilReport(t *testing.T) {
	s, _ := NewLogSink(silentLogger())
	err := s.Deliver(context.Background(), nil)
	if err == nil {
		t.Fatal("Deliver(nil) should return error")
	}
}

func TestLogSink_Deliver_SuperEvent(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	s, _ := NewLogSink(logger)
	report := testSuperEventReport()

	err := s.Deliver(context.Background(), report)
	if err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "super-event-456") {
		t.Error("expected super-event-456 in log output")
	}
}

func TestLogSink_Deliver_EmptyRemediation(t *testing.T) {
	s, _ := NewLogSink(silentLogger())
	report := testReport()
	report.Remediation = nil

	err := s.Deliver(context.Background(), report)
	if err != nil {
		t.Fatalf("Deliver() with nil remediation error = %v", err)
	}
}

func TestFormatRelatedResources(t *testing.T) {
	refs := []model.ResourceRef{
		{Kind: "Pod", Namespace: "default", Name: "pod-1"},
		{Kind: "Node", Name: "node-1"},
	}
	result := formatRelatedResources(refs)
	if len(result) != 2 {
		t.Fatalf("expected 2 results, got %d", len(result))
	}
	if result[0] != "Pod/default/pod-1" {
		t.Errorf("result[0] = %q, want %q", result[0], "Pod/default/pod-1")
	}
	if result[1] != "Node/node-1" {
		t.Errorf("result[1] = %q, want %q", result[1], "Node/node-1")
	}
}
