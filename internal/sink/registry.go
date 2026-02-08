package sink

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/k8s-wormsign/k8s-wormsign/internal/metrics"
	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

// Registry manages the set of active sinks. It provides thread-safe
// registration, lookup, and fan-out delivery to all registered sinks.
type Registry struct {
	mu      sync.RWMutex
	sinks   map[string]Sink
	logger  *slog.Logger
	metrics *metrics.Metrics
}

// NewRegistry creates a new sink Registry. If logger is nil, slog.Default()
// is used. The metrics parameter may be nil if metric recording is not needed
// (e.g., in tests).
func NewRegistry(logger *slog.Logger, m *metrics.Metrics) *Registry {
	if logger == nil {
		logger = slog.Default()
	}
	return &Registry{
		sinks:   make(map[string]Sink),
		logger:  logger,
		metrics: m,
	}
}

// Register adds a sink to the registry. Returns an error if a sink with the
// same name is already registered.
func (r *Registry) Register(s Sink) error {
	if s == nil {
		return fmt.Errorf("sink must not be nil")
	}

	name := s.Name()
	if name == "" {
		return fmt.Errorf("sink name must not be empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.sinks[name]; exists {
		return fmt.Errorf("sink %q is already registered", name)
	}

	r.sinks[name] = s
	r.logger.Info("sink registered", "sink", name)
	return nil
}

// Unregister removes a sink from the registry. Returns an error if the
// sink is not found.
func (r *Registry) Unregister(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.sinks[name]; !exists {
		return fmt.Errorf("sink %q is not registered", name)
	}

	delete(r.sinks, name)
	r.logger.Info("sink unregistered", "sink", name)
	return nil
}

// Get returns the sink with the given name, or nil if not found.
func (r *Registry) Get(name string) Sink {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.sinks[name]
}

// All returns a snapshot of all registered sinks.
func (r *Registry) All() []Sink {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]Sink, 0, len(r.sinks))
	for _, s := range r.sinks {
		result = append(result, s)
	}
	return result
}

// Count returns the number of registered sinks.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.sinks)
}

// Names returns the names of all registered sinks.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.sinks))
	for name := range r.sinks {
		names = append(names, name)
	}
	return names
}

// DeliverAll fans out an RCA report to all registered sinks that accept
// the report's severity. One sink's failure does not block others.
// Delivery errors are logged and counted in metrics; the method returns
// the count of failed deliveries.
func (r *Registry) DeliverAll(ctx context.Context, report *model.RCAReport) int {
	if report == nil {
		r.logger.Error("DeliverAll called with nil report")
		return 0
	}

	r.mu.RLock()
	sinks := make([]Sink, 0, len(r.sinks))
	for _, s := range r.sinks {
		sinks = append(sinks, s)
	}
	r.mu.RUnlock()

	failures := 0
	for _, s := range sinks {
		if !AcceptsSeverity(s, report.Severity) {
			r.logger.Debug("sink skipped due to severity filter",
				"sink", s.Name(),
				"report_severity", string(report.Severity),
			)
			continue
		}

		err := s.Deliver(ctx, report)
		if err != nil {
			failures++
			r.logger.Error("sink delivery failed",
				"sink", s.Name(),
				"fault_event_id", report.FaultEventID,
				"error", err,
			)
			if r.metrics != nil {
				r.metrics.SinkDeliveriesTotal.WithLabelValues(s.Name(), "failure").Inc()
			}
		} else {
			r.logger.Info("sink delivery succeeded",
				"sink", s.Name(),
				"fault_event_id", report.FaultEventID,
			)
			if r.metrics != nil {
				r.metrics.SinkDeliveriesTotal.WithLabelValues(s.Name(), "success").Inc()
			}
		}
	}
	return failures
}
