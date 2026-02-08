package metrics

import (
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
)

func TestNewMetrics_RegistersAllCollectors(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	if m == nil {
		t.Fatal("NewMetrics returned nil")
	}

	// Touch each Vec metric so it produces at least one time series
	// when gathered. Without this, empty Vecs are not reported by Gather().
	m.FaultEventsTotal.WithLabelValues("_init", "_init", "_init")
	m.SuperEventsTotal.WithLabelValues("_init", "_init")
	m.EventsFilteredTotal.WithLabelValues("_init", "_init", "_init")
	m.DiagnosticGatherDuration.WithLabelValues("_init")
	m.AnalyzerRequestsTotal.WithLabelValues("_init", "_init")
	m.AnalyzerTokensUsedTotal.WithLabelValues("_init", "_init")
	m.SinkDeliveriesTotal.WithLabelValues("_init", "_init")
	m.PipelineQueueDepth.WithLabelValues("_init")

	// Gather all registered metric families.
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	// Build a set of registered metric names.
	registered := make(map[string]bool, len(families))
	for _, f := range families {
		registered[f.GetName()] = true
	}

	expected := []string{
		"wormsign_fault_events_total",
		"wormsign_super_events_total",
		"wormsign_events_filtered_total",
		"wormsign_diagnostic_gather_duration_seconds",
		"wormsign_analyzer_requests_total",
		"wormsign_analyzer_tokens_used_total",
		"wormsign_analyzer_latency_seconds",
		"wormsign_sink_deliveries_total",
		"wormsign_circuit_breaker_state",
		"wormsign_pipeline_queue_depth",
		"wormsign_shard_namespaces",
		"wormsign_leader_is_leader",
	}

	for _, name := range expected {
		if !registered[name] {
			t.Errorf("metric %q not registered", name)
		}
	}

	if len(expected) != len(families) {
		t.Errorf("expected %d metric families, got %d", len(expected), len(families))
	}
}

func TestNewMetrics_DoubleRegistrationPanics(t *testing.T) {
	reg := prometheus.NewRegistry()
	_ = NewMetrics(reg)

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on double registration, but none occurred")
		}
	}()

	// Second registration on the same registry must panic via MustRegister.
	_ = NewMetrics(reg)
}

func TestFaultEventsTotal_Labels(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	m.FaultEventsTotal.WithLabelValues("PodCrashLoop", "critical", "default").Inc()
	m.FaultEventsTotal.WithLabelValues("PodStuckPending", "warning", "kube-system").Add(3)

	expected := `
		# HELP wormsign_fault_events_total Total number of fault events emitted.
		# TYPE wormsign_fault_events_total counter
		wormsign_fault_events_total{detector="PodCrashLoop",namespace="default",severity="critical"} 1
		wormsign_fault_events_total{detector="PodStuckPending",namespace="kube-system",severity="warning"} 3
	`
	if err := testutil.CollectAndCompare(m.FaultEventsTotal, strings.NewReader(expected)); err != nil {
		t.Errorf("FaultEventsTotal mismatch: %v", err)
	}
}

func TestSuperEventsTotal_Labels(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	m.SuperEventsTotal.WithLabelValues("NodeCascade", "critical").Inc()

	expected := `
		# HELP wormsign_super_events_total Total number of super-events (correlated groups) emitted.
		# TYPE wormsign_super_events_total counter
		wormsign_super_events_total{correlation_rule="NodeCascade",severity="critical"} 1
	`
	if err := testutil.CollectAndCompare(m.SuperEventsTotal, strings.NewReader(expected)); err != nil {
		t.Errorf("SuperEventsTotal mismatch: %v", err)
	}
}

func TestEventsFilteredTotal_Labels(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	m.EventsFilteredTotal.WithLabelValues("PodFailed", "default", "namespace_global").Inc()
	m.EventsFilteredTotal.WithLabelValues("PodCrashLoop", "payments", "policy_exclude").Add(5)

	expected := `
		# HELP wormsign_events_filtered_total Total number of events excluded by filters.
		# TYPE wormsign_events_filtered_total counter
		wormsign_events_filtered_total{detector="PodCrashLoop",namespace="payments",reason="policy_exclude"} 5
		wormsign_events_filtered_total{detector="PodFailed",namespace="default",reason="namespace_global"} 1
	`
	if err := testutil.CollectAndCompare(m.EventsFilteredTotal, strings.NewReader(expected)); err != nil {
		t.Errorf("EventsFilteredTotal mismatch: %v", err)
	}
}

func TestDiagnosticGatherDuration_Labels(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	m.DiagnosticGatherDuration.WithLabelValues("PodLogs").Observe(0.5)
	m.DiagnosticGatherDuration.WithLabelValues("PodEvents").Observe(1.2)

	// Verify the metric exists and has the expected label.
	count := testutil.CollectAndCount(m.DiagnosticGatherDuration)
	if count != 2 {
		t.Errorf("expected 2 DiagnosticGatherDuration series, got %d", count)
	}
}

func TestAnalyzerRequestsTotal_Labels(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	m.AnalyzerRequestsTotal.WithLabelValues("claude", "success").Inc()
	m.AnalyzerRequestsTotal.WithLabelValues("claude", "error").Inc()

	expected := `
		# HELP wormsign_analyzer_requests_total Total number of LLM analyzer API calls.
		# TYPE wormsign_analyzer_requests_total counter
		wormsign_analyzer_requests_total{backend="claude",status="error"} 1
		wormsign_analyzer_requests_total{backend="claude",status="success"} 1
	`
	if err := testutil.CollectAndCompare(m.AnalyzerRequestsTotal, strings.NewReader(expected)); err != nil {
		t.Errorf("AnalyzerRequestsTotal mismatch: %v", err)
	}
}

func TestAnalyzerTokensUsedTotal_Labels(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	m.AnalyzerTokensUsedTotal.WithLabelValues("claude-bedrock", "input").Add(1500)
	m.AnalyzerTokensUsedTotal.WithLabelValues("claude-bedrock", "output").Add(500)

	expected := `
		# HELP wormsign_analyzer_tokens_used_total Total tokens consumed by the LLM analyzer.
		# TYPE wormsign_analyzer_tokens_used_total counter
		wormsign_analyzer_tokens_used_total{backend="claude-bedrock",direction="input"} 1500
		wormsign_analyzer_tokens_used_total{backend="claude-bedrock",direction="output"} 500
	`
	if err := testutil.CollectAndCompare(m.AnalyzerTokensUsedTotal, strings.NewReader(expected)); err != nil {
		t.Errorf("AnalyzerTokensUsedTotal mismatch: %v", err)
	}
}

func TestAnalyzerLatency(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	m.AnalyzerLatency.Observe(2.5)
	m.AnalyzerLatency.Observe(10.0)

	count := testutil.CollectAndCount(m.AnalyzerLatency)
	if count != 1 {
		t.Errorf("expected 1 AnalyzerLatency series, got %d", count)
	}
}

func TestSinkDeliveriesTotal_Labels(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	m.SinkDeliveriesTotal.WithLabelValues("slack", "success").Add(10)
	m.SinkDeliveriesTotal.WithLabelValues("slack", "failure").Inc()
	m.SinkDeliveriesTotal.WithLabelValues("pagerduty", "success").Add(3)

	expected := `
		# HELP wormsign_sink_deliveries_total Total number of sink deliveries.
		# TYPE wormsign_sink_deliveries_total counter
		wormsign_sink_deliveries_total{sink="pagerduty",status="success"} 3
		wormsign_sink_deliveries_total{sink="slack",status="failure"} 1
		wormsign_sink_deliveries_total{sink="slack",status="success"} 10
	`
	if err := testutil.CollectAndCompare(m.SinkDeliveriesTotal, strings.NewReader(expected)); err != nil {
		t.Errorf("SinkDeliveriesTotal mismatch: %v", err)
	}
}

func TestCircuitBreakerState(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	// Default should be 0 (closed).
	val := testutil.ToFloat64(m.CircuitBreakerState)
	if val != 0 {
		t.Errorf("expected initial circuit breaker state 0, got %f", val)
	}

	m.CircuitBreakerState.Set(1) // open
	val = testutil.ToFloat64(m.CircuitBreakerState)
	if val != 1 {
		t.Errorf("expected circuit breaker state 1, got %f", val)
	}

	m.CircuitBreakerState.Set(2) // half-open
	val = testutil.ToFloat64(m.CircuitBreakerState)
	if val != 2 {
		t.Errorf("expected circuit breaker state 2, got %f", val)
	}
}

func TestPipelineQueueDepth_Labels(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	m.PipelineQueueDepth.WithLabelValues("gathering").Set(10)
	m.PipelineQueueDepth.WithLabelValues("analysis").Set(5)
	m.PipelineQueueDepth.WithLabelValues("sink").Set(2)

	expected := `
		# HELP wormsign_pipeline_queue_depth Number of items in each pipeline stage queue.
		# TYPE wormsign_pipeline_queue_depth gauge
		wormsign_pipeline_queue_depth{stage="analysis"} 5
		wormsign_pipeline_queue_depth{stage="gathering"} 10
		wormsign_pipeline_queue_depth{stage="sink"} 2
	`
	if err := testutil.CollectAndCompare(m.PipelineQueueDepth, strings.NewReader(expected)); err != nil {
		t.Errorf("PipelineQueueDepth mismatch: %v", err)
	}
}

func TestShardNamespaces(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	m.ShardNamespaces.Set(12)
	val := testutil.ToFloat64(m.ShardNamespaces)
	if val != 12 {
		t.Errorf("expected shard namespaces 12, got %f", val)
	}
}

func TestLeaderIsLeader(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	// Default 0.
	val := testutil.ToFloat64(m.LeaderIsLeader)
	if val != 0 {
		t.Errorf("expected initial leader state 0, got %f", val)
	}

	m.LeaderIsLeader.Set(1)
	val = testutil.ToFloat64(m.LeaderIsLeader)
	if val != 1 {
		t.Errorf("expected leader state 1, got %f", val)
	}
}

func TestAnalyzerLatency_Buckets(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewMetrics(reg)

	// The custom buckets should be: 0.5, 1, 2, 5, 10, 20, 30, 60
	// Observe values at bucket boundaries and verify histogram captures them.
	observations := []float64{0.3, 0.5, 1.0, 3.0, 10.0, 45.0}
	for _, v := range observations {
		m.AnalyzerLatency.Observe(v)
	}

	// Verify the metric is collected and reports the correct count.
	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("failed to gather: %v", err)
	}

	var found bool
	for _, f := range families {
		if f.GetName() == "wormsign_analyzer_latency_seconds" {
			found = true
			for _, metric := range f.GetMetric() {
				h := metric.GetHistogram()
				if h == nil {
					t.Fatal("expected histogram metric")
				}
				if h.GetSampleCount() != uint64(len(observations)) {
					t.Errorf("expected sample count %d, got %d", len(observations), h.GetSampleCount())
				}
				// Verify bucket count matches expected: 8 custom buckets.
				// The +Inf bucket is implicit and not included in GetBucket().
				if len(h.GetBucket()) != 8 {
					t.Errorf("expected 8 buckets, got %d", len(h.GetBucket()))
				}
			}
		}
	}
	if !found {
		t.Error("wormsign_analyzer_latency_seconds not found in gathered metrics")
	}
}
