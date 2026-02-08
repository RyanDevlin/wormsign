package gatherer

import (
	"fmt"
	"log/slog"
	"sync"
)

// Registry manages the set of active gatherers. It provides thread-safe
// registration, lookup, and lifecycle management.
type Registry struct {
	mu        sync.RWMutex
	gatherers map[string]Gatherer
	logger    *slog.Logger
}

// NewRegistry creates a new gatherer Registry.
func NewRegistry(logger *slog.Logger) *Registry {
	if logger == nil {
		logger = slog.Default()
	}
	return &Registry{
		gatherers: make(map[string]Gatherer),
		logger:    logger,
	}
}

// Register adds a gatherer to the registry. Returns an error if a gatherer
// with the same name is already registered.
func (r *Registry) Register(g Gatherer) error {
	if g == nil {
		return fmt.Errorf("gatherer must not be nil")
	}

	name := g.Name()
	if name == "" {
		return fmt.Errorf("gatherer name must not be empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.gatherers[name]; exists {
		return fmt.Errorf("gatherer %q is already registered", name)
	}

	r.gatherers[name] = g
	r.logger.Info("gatherer registered", "gatherer", name)
	return nil
}

// Get returns the gatherer with the given name, or nil if not found.
func (r *Registry) Get(name string) Gatherer {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.gatherers[name]
}

// All returns a snapshot of all registered gatherers.
func (r *Registry) All() []Gatherer {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]Gatherer, 0, len(r.gatherers))
	for _, g := range r.gatherers {
		result = append(result, g)
	}
	return result
}

// Count returns the number of registered gatherers.
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.gatherers)
}

// Names returns the names of all registered gatherers.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.gatherers))
	for name := range r.gatherers {
		names = append(names, name)
	}
	return names
}

// Unregister removes a gatherer from the registry. Returns an error if the
// gatherer is not found.
func (r *Registry) Unregister(name string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.gatherers[name]; !exists {
		return fmt.Errorf("gatherer %q is not registered", name)
	}

	delete(r.gatherers, name)
	r.logger.Info("gatherer unregistered", "gatherer", name)
	return nil
}
