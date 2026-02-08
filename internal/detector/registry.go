package detector

import (
	"fmt"
	"log/slog"
	"sync"
)

// Registry manages the set of active detectors. It provides thread-safe
// registration, lookup, and lifecycle management.
type Registry struct {
	mu        sync.RWMutex
	detectors map[string]Detector
	logger    *slog.Logger
}

// NewRegistry creates a new detector Registry.
func NewRegistry(logger *slog.Logger) *Registry {
	if logger == nil {
		logger = slog.Default()
	}
	return &Registry{
		detectors: make(map[string]Detector),
		logger:    logger,
	}
}

// Register adds a detector to the registry. Returns an error if a detector
// with the same name is already registered.
func (r *Registry) Register(d Detector) error {
	if d == nil {
		return fmt.Errorf("detector must not be nil")
	}

	name := d.Name()
	if name == "" {
		return fmt.Errorf("detector name must not be empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.detectors[name]; exists {
		return fmt.Errorf("detector %q is already registered", name)
	}

	r.detectors[name] = d
	r.logger.Info("detector registered", "detector", name, "severity", string(d.Severity()))
	return nil
}

// Get returns the detector with the given name, or nil if not found.
func (r *Registry) Get(name string) Detector {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.detectors[name]
}

// All returns a snapshot of all registered detectors.
func (r *Registry) All() []Detector {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]Detector, 0, len(r.detectors))
	for _, d := range r.detectors {
		result = append(result, d)
	}
	return result
}

// Count returns the number of registered detectors.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.detectors)
}

// Names returns the names of all registered detectors.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.detectors))
	for name := range r.detectors {
		names = append(names, name)
	}
	return names
}

// Unregister removes a detector from the registry. Returns an error if the
// detector is not found.
func (r *Registry) Unregister(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.detectors[name]; !exists {
		return fmt.Errorf("detector %q is not registered", name)
	}

	delete(r.detectors, name)
	r.logger.Info("detector unregistered", "detector", name)
	return nil
}
