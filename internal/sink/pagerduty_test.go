package sink

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

func TestNewPagerDutySink_Validation(t *testing.T) {
	tests := []struct {
		name    string
		cfg     PagerDutyConfig
		logger  bool
		wantErr bool
	}{
		{
			name:    "nil logger",
			cfg:     PagerDutyConfig{RoutingKey: "test-key"},
			logger:  false,
			wantErr: true,
		},
		{
			name:    "empty routing key",
			cfg:     PagerDutyConfig{RoutingKey: ""},
			logger:  true,
			wantErr: true,
		},
		{
			name:    "valid config",
			cfg:     PagerDutyConfig{RoutingKey: "test-key"},
			logger:  true,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var l = silentLogger()
			if !tt.logger {
				l = nil
			}
			_, err := NewPagerDutySink(tt.cfg, l)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewPagerDutySink() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestPagerDutySink_Name(t *testing.T) {
	s, _ := NewPagerDutySink(PagerDutyConfig{RoutingKey: "key"}, silentLogger())
	if s.Name() != "pagerduty" {
		t.Errorf("Name() = %q, want %q", s.Name(), "pagerduty")
	}
}

func TestPagerDutySink_Deliver(t *testing.T) {
	var receivedBody []byte
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		body, _ := io.ReadAll(r.Body)
		receivedBody = body
		w.WriteHeader(http.StatusAccepted)
	}))
	defer server.Close()

	s := &PagerDutySink{
		client:     server.Client(),
		routingKey: "test-routing-key",
		logger:     silentLogger(),
		eventsURL:  server.URL,
		retryCfg: retryConfig{
			maxAttempts: 1,
			baseDelay:   0,
			multiplier:  1,
		},
	}

	report := testReport()
	err := s.Deliver(context.Background(), report)
	if err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}

	var payload pdPayload
	if err := json.Unmarshal(receivedBody, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}

	if payload.RoutingKey != "test-routing-key" {
		t.Errorf("routing_key = %q, want %q", payload.RoutingKey, "test-routing-key")
	}
	if payload.EventAction != "trigger" {
		t.Errorf("event_action = %q, want %q", payload.EventAction, "trigger")
	}
	if payload.DedupKey != "test-event-123" {
		t.Errorf("dedup_key = %q, want %q", payload.DedupKey, "test-event-123")
	}
	if payload.Payload.Severity != "critical" {
		t.Errorf("severity = %q, want %q", payload.Payload.Severity, "critical")
	}
	if payload.Payload.Source != "wormsign" {
		t.Errorf("source = %q, want %q", payload.Payload.Source, "wormsign")
	}
	if payload.Payload.Class != "resources" {
		t.Errorf("class = %q, want %q", payload.Payload.Class, "resources")
	}
}

func TestPagerDutySink_Deliver_NilReport(t *testing.T) {
	s := &PagerDutySink{logger: silentLogger()}
	err := s.Deliver(context.Background(), nil)
	if err == nil {
		t.Fatal("Deliver(nil) should return error")
	}
}

func TestPagerDutySink_Deliver_ServerError(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	s := &PagerDutySink{
		client:     server.Client(),
		routingKey: "test-key",
		logger:     silentLogger(),
		eventsURL:  server.URL,
		retryCfg: retryConfig{
			maxAttempts: 1,
			baseDelay:   0,
			multiplier:  1,
		},
	}

	err := s.Deliver(context.Background(), testReport())
	if err == nil {
		t.Fatal("expected error for server error response")
	}
}

func TestPagerDutySink_SeverityFilter(t *testing.T) {
	s, _ := NewPagerDutySink(PagerDutyConfig{
		RoutingKey:     "key",
		SeverityFilter: []model.Severity{model.SeverityCritical},
	}, silentLogger())

	f := s.SeverityFilter()
	if len(f) != 1 || f[0] != model.SeverityCritical {
		t.Errorf("SeverityFilter() = %v, want [critical]", f)
	}
}

func TestPagerDutySink_BuildPayload_SuperEvent(t *testing.T) {
	s := &PagerDutySink{routingKey: "key"}
	report := testSuperEventReport()

	payload := s.buildPayload(report)
	if payload.Payload.Component != "Node/node-1" {
		t.Errorf("component = %q, want %q", payload.Payload.Component, "Node/node-1")
	}
}
