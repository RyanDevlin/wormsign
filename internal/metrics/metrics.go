// Package metrics defines and registers all Prometheus metrics for the
// Wormsign controller. Consumers obtain a *Metrics instance via NewMetrics()
// and use the exported fields to record observations.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
)

const (
	namespace = "wormsign"
)

// Metrics holds all Prometheus metric collectors for the Wormsign controller.
type Metrics struct {
	// FaultEventsTotal counts fault events emitted, partitioned by detector,
	// severity, and namespace.
	FaultEventsTotal *prometheus.CounterVec

	// SuperEventsTotal counts super-events (correlated groups) emitted,
	// partitioned by correlation_rule and severity.
	SuperEventsTotal *prometheus.CounterVec

	// EventsFilteredTotal counts events excluded by filters, partitioned by
	// detector, namespace, and reason.
	EventsFilteredTotal *prometheus.CounterVec

	// DiagnosticGatherDuration observes the time to gather diagnostics,
	// partitioned by gatherer name.
	DiagnosticGatherDuration *prometheus.HistogramVec

	// AnalyzerRequestsTotal counts LLM API calls, partitioned by backend
	// and status.
	AnalyzerRequestsTotal *prometheus.CounterVec

	// AnalyzerTokensUsedTotal counts tokens consumed, partitioned by
	// backend and direction (input/output).
	AnalyzerTokensUsedTotal *prometheus.CounterVec

	// AnalyzerLatency observes LLM response latency in seconds.
	AnalyzerLatency prometheus.Histogram

	// SinkDeliveriesTotal counts sink deliveries, partitioned by sink name
	// and status (success/failure).
	SinkDeliveriesTotal *prometheus.CounterVec

	// CircuitBreakerState reports the current circuit breaker state:
	// 0 = closed, 1 = open, 2 = half-open.
	CircuitBreakerState prometheus.Gauge

	// PipelineQueueDepth reports the number of items in each pipeline stage
	// queue, partitioned by stage.
	PipelineQueueDepth *prometheus.GaugeVec

	// ShardNamespaces reports the number of namespaces assigned to this
	// replica.
	ShardNamespaces prometheus.Gauge

	// LeaderIsLeader reports whether this replica is the current leader
	// (1 = leader, 0 = not leader).
	LeaderIsLeader prometheus.Gauge
}

// NewMetrics creates a new Metrics instance and registers all collectors with
// the provided prometheus.Registerer. Use prometheus.DefaultRegisterer for
// the standard global registry.
func NewMetrics(reg prometheus.Registerer) *Metrics {
	m := &Metrics{
		FaultEventsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "fault_events_total",
				Help:      "Total number of fault events emitted.",
			},
			[]string{"detector", "severity", "namespace"},
		),

		SuperEventsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "super_events_total",
				Help:      "Total number of super-events (correlated groups) emitted.",
			},
			[]string{"correlation_rule", "severity"},
		),

		EventsFilteredTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "events_filtered_total",
				Help:      "Total number of events excluded by filters.",
			},
			[]string{"detector", "namespace", "reason"},
		),

		DiagnosticGatherDuration: prometheus.NewHistogramVec(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "diagnostic_gather_duration_seconds",
				Help:      "Time spent gathering diagnostics, in seconds.",
				Buckets:   prometheus.DefBuckets,
			},
			[]string{"gatherer"},
		),

		AnalyzerRequestsTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "analyzer_requests_total",
				Help:      "Total number of LLM analyzer API calls.",
			},
			[]string{"backend", "status"},
		),

		AnalyzerTokensUsedTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "analyzer_tokens_used_total",
				Help:      "Total tokens consumed by the LLM analyzer.",
			},
			[]string{"backend", "direction"},
		),

		AnalyzerLatency: prometheus.NewHistogram(
			prometheus.HistogramOpts{
				Namespace: namespace,
				Name:      "analyzer_latency_seconds",
				Help:      "LLM analyzer response latency in seconds.",
				Buckets:   []float64{0.5, 1, 2, 5, 10, 20, 30, 60},
			},
		),

		SinkDeliveriesTotal: prometheus.NewCounterVec(
			prometheus.CounterOpts{
				Namespace: namespace,
				Name:      "sink_deliveries_total",
				Help:      "Total number of sink deliveries.",
			},
			[]string{"sink", "status"},
		),

		CircuitBreakerState: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "circuit_breaker_state",
				Help:      "Current circuit breaker state: 0 = closed, 1 = open, 2 = half-open.",
			},
		),

		PipelineQueueDepth: prometheus.NewGaugeVec(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "pipeline_queue_depth",
				Help:      "Number of items in each pipeline stage queue.",
			},
			[]string{"stage"},
		),

		ShardNamespaces: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "shard_namespaces",
				Help:      "Number of namespaces assigned to this replica.",
			},
		),

		LeaderIsLeader: prometheus.NewGauge(
			prometheus.GaugeOpts{
				Namespace: namespace,
				Name:      "leader_is_leader",
				Help:      "Whether this replica is the current leader (1 = leader, 0 = not leader).",
			},
		),
	}

	reg.MustRegister(
		m.FaultEventsTotal,
		m.SuperEventsTotal,
		m.EventsFilteredTotal,
		m.DiagnosticGatherDuration,
		m.AnalyzerRequestsTotal,
		m.AnalyzerTokensUsedTotal,
		m.AnalyzerLatency,
		m.SinkDeliveriesTotal,
		m.CircuitBreakerState,
		m.PipelineQueueDepth,
		m.ShardNamespaces,
		m.LeaderIsLeader,
	)

	return m
}
