package sink

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/k8s-wormsign/k8s-wormsign/internal/metrics"
	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
	"k8s.io/client-go/kubernetes/fake"
)

// --- S3 Sink: NewS3Sink validation tests ---

func TestNewS3Sink_Validation(t *testing.T) {
	tests := []struct {
		name    string
		bucket  string
		region  string
		wantErr string
	}{
		{
			name:    "empty bucket",
			bucket:  "",
			region:  "us-east-1",
			wantErr: "bucket must not be empty",
		},
		{
			name:    "empty region",
			bucket:  "test-bucket",
			region:  "",
			wantErr: "region must not be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewS3Sink(context.Background(), S3Config{
				Bucket: tt.bucket,
				Region: tt.region,
			}, silentLogger())
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !strContains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestNewS3Sink_NilLogger(t *testing.T) {
	_, err := NewS3Sink(context.Background(), S3Config{
		Bucket: "test",
		Region: "us-east-1",
	}, nil)
	if err == nil {
		t.Fatal("expected error for nil logger")
	}
}

// --- Registry: DeliverAll with metrics ---

func TestRegistry_DeliverAll_WithMetrics(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := metrics.NewMetrics(reg)

	r := NewRegistry(silentLogger(), m)

	good := &deliverableSink{name: "good-sink"}
	bad := &deliverableSink{name: "bad-sink", shouldFail: true}
	r.Register(good)
	r.Register(bad)

	report := testReport()
	failures := r.DeliverAll(context.Background(), report)
	if failures != 1 {
		t.Errorf("DeliverAll() failures = %d, want 1", failures)
	}
	if good.delivered != 1 {
		t.Errorf("good sink delivered = %d, want 1", good.delivered)
	}
	if bad.delivered != 1 {
		t.Errorf("bad sink delivered = %d, want 1", bad.delivered)
	}
}

func TestRegistry_DeliverAll_WithMetrics_AllSuccess(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := metrics.NewMetrics(reg)

	r := NewRegistry(silentLogger(), m)
	s := &deliverableSink{name: "test-sink"}
	r.Register(s)

	report := testReport()
	failures := r.DeliverAll(context.Background(), report)
	if failures != 0 {
		t.Errorf("DeliverAll() failures = %d, want 0", failures)
	}
}

// --- KubernetesEventSink: empty target resources ---

func TestKubernetesEventSink_Deliver_NoTargetResources(t *testing.T) {
	client := fake.NewSimpleClientset()
	kSink, err := NewKubernetesEventSink(client, KubernetesEventConfig{}, silentLogger())
	if err != nil {
		t.Fatalf("NewKubernetesEventSink: %v", err)
	}
	kSink.retryCfg = retryConfig{maxAttempts: 1, baseDelay: 0, multiplier: 1}

	// Report with no FaultEvent or SuperEvent - no target resources.
	report := &model.RCAReport{
		FaultEventID: "test-no-targets",
		Timestamp:    time.Now(),
		RootCause:    "Test",
		Severity:     model.SeverityWarning,
		Category:     "unknown",
		DiagnosticBundle: model.DiagnosticBundle{
			// Both FaultEvent and SuperEvent are nil.
		},
	}

	err = kSink.Deliver(context.Background(), report)
	if err != nil {
		t.Errorf("Deliver with no targets should return nil, got %v", err)
	}
}

// --- KubernetesEventSink: cluster-scoped resource ---

func TestKubernetesEventSink_Deliver_ClusterScoped(t *testing.T) {
	client := fake.NewSimpleClientset()
	kSink, err := NewKubernetesEventSink(client, KubernetesEventConfig{}, silentLogger())
	if err != nil {
		t.Fatalf("NewKubernetesEventSink: %v", err)
	}
	kSink.retryCfg = retryConfig{maxAttempts: 1, baseDelay: 0, multiplier: 1}

	// Report with cluster-scoped resource (no namespace).
	report := &model.RCAReport{
		FaultEventID: "test-cluster-scoped",
		Timestamp:    time.Now(),
		RootCause:    "Node not ready",
		Severity:     model.SeverityCritical,
		Category:     "node",
		DiagnosticBundle: model.DiagnosticBundle{
			FaultEvent: &model.FaultEvent{
				ID:           "fe-node",
				DetectorName: "NodeNotReady",
				Severity:     model.SeverityCritical,
				Resource: model.ResourceRef{
					Kind: "Node",
					Name: "node-1",
					// No namespace - cluster scoped
				},
			},
		},
	}

	err = kSink.Deliver(context.Background(), report)
	if err != nil {
		t.Errorf("Deliver with cluster-scoped resource error = %v", err)
	}
}

// --- KubernetesEventSink: info severity creates Normal event ---

func TestKubernetesEventSink_Deliver_InfoSeverityNormalEvent(t *testing.T) {
	client := fake.NewSimpleClientset()
	kSink, err := NewKubernetesEventSink(client, KubernetesEventConfig{}, silentLogger())
	if err != nil {
		t.Fatalf("NewKubernetesEventSink: %v", err)
	}
	kSink.retryCfg = retryConfig{maxAttempts: 1, baseDelay: 0, multiplier: 1}

	report := testReport()
	report.Severity = model.SeverityInfo

	err = kSink.Deliver(context.Background(), report)
	if err != nil {
		t.Errorf("Deliver error = %v", err)
	}
}

// --- PagerDuty: deliver with unreachable server ---

func TestPagerDutySink_Deliver_NetworkError(t *testing.T) {
	s, err := NewPagerDutySink(PagerDutyConfig{
		RoutingKey: "test-key",
	}, silentLogger())
	if err != nil {
		t.Fatalf("NewPagerDutySink: %v", err)
	}
	// Point to a server that's immediately closed.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	server.Close()

	s.eventsURL = server.URL
	s.retryCfg = retryConfig{maxAttempts: 1, baseDelay: 0, multiplier: 1}

	err = s.Deliver(context.Background(), testReport())
	if err == nil {
		t.Fatal("expected error for network error")
	}
}

// --- Slack: deliver with invalid domain ---

func TestNewSlackSink_InvalidDomain(t *testing.T) {
	_, err := NewSlackSink(SlackConfig{
		WebhookURL: "https://evil.example.com/webhook",
	}, silentLogger())
	if err == nil {
		t.Fatal("expected error for non-Slack domain")
	}
}

// --- Slack: deliver with network error ---

func TestSlackSink_Deliver_NetworkError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	server.Close()

	s := &SlackSink{
		client:     &http.Client{},
		webhookURL: server.URL,
		logger:     silentLogger(),
		retryCfg:   retryConfig{maxAttempts: 1, baseDelay: 0, multiplier: 1},
	}

	err := s.Deliver(context.Background(), testReport())
	if err == nil {
		t.Fatal("expected error for network error")
	}
}

// --- Slack: buildPayload with empty remediation ---

func TestSlackSink_BuildPayload_EmptyRemediation(t *testing.T) {
	s := &SlackSink{logger: silentLogger()}

	report := testReport()
	report.Remediation = nil

	payload := s.buildPayload(report)
	// Should not panic and should have attachments.
	if len(payload.Attachments) == 0 {
		t.Fatal("expected at least one attachment")
	}
	// Check that remediation field has the default message.
	for _, f := range payload.Attachments[0].Fields {
		if f.Title == "Remediation" {
			if f.Value != "No remediation steps provided." {
				t.Errorf("Remediation = %q, want default message", f.Value)
			}
		}
	}
}

// --- deliverWithRetry: context cancelled during backoff ---

func TestDeliverWithRetry_ContextCancelledDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0

	// Cancel after first failure, during backoff.
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	err := deliverWithRetry(ctx, silentLogger(), "test", retryConfig{
		maxAttempts: 5,
		baseDelay:   1 * time.Second, // Long delay to ensure context cancel hits
		multiplier:  1.0,
	}, func(_ context.Context) error {
		calls++
		return fmt.Errorf("fail")
	})

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if calls != 1 {
		t.Errorf("expected 1 call (cancelled during backoff), got %d", calls)
	}
	if !strContains(err.Error(), "context cancelled during backoff") {
		t.Errorf("error = %q, expected 'context cancelled during backoff'", err.Error())
	}
}

// --- secret: resolver error ---

func TestResolveSecretRefs_ResolverError(t *testing.T) {
	resolver := &failingResolver{err: fmt.Errorf("secret not found")}
	headers := map[string]string{
		"Authorization": "Bearer ${SECRET:my-secret:token}",
	}

	_, err := ResolveSecretRefs(context.Background(), headers, "default", resolver)
	if err == nil {
		t.Fatal("expected error from failing resolver")
	}
	if !strContains(err.Error(), "secret not found") {
		t.Errorf("error = %q, expected 'secret not found' substring", err.Error())
	}
}

type failingResolver struct {
	err error
}

func (f *failingResolver) GetSecretValue(_ context.Context, _, _, _ string) (string, error) {
	return "", f.err
}

// --- Webhook: deliver with network error ---

func TestWebhookSink_Deliver_NetworkError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	server.Close()

	s := &WebhookSink{
		client:   &http.Client{},
		url:      server.URL,
		logger:   silentLogger(),
		retryCfg: retryConfig{maxAttempts: 1, baseDelay: 0, multiplier: 1},
	}

	err := s.Deliver(context.Background(), testReport())
	if err == nil {
		t.Fatal("expected error for network error")
	}
}

// --- truncateMessage edge cases ---

func TestTruncateMessage_ExactLength(t *testing.T) {
	// Message exactly at max length should not be truncated.
	msg := make([]byte, maxEventMessageLen)
	for i := range msg {
		msg[i] = 'a'
	}
	result := truncateMessage(string(msg))
	if len(result) != maxEventMessageLen {
		t.Errorf("length = %d, want %d", len(result), maxEventMessageLen)
	}
}

func TestTruncateMessage_OverLength(t *testing.T) {
	msg := make([]byte, maxEventMessageLen+100)
	for i := range msg {
		msg[i] = 'a'
	}
	result := truncateMessage(string(msg))
	if len(result) != maxEventMessageLen {
		t.Errorf("length = %d, want %d", len(result), maxEventMessageLen)
	}
	// Should end with "..."
	if result[len(result)-3:] != "..." {
		t.Error("truncated message should end with '...'")
	}
}

// --- helpers ---

func strContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
