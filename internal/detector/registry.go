package detector

import (
	"fmt"
	"log/slog"
	"sync"
)

// DetectorFactory is a function that creates a Detector given a callback for
// emitting fault events. Factory functions are registered by name and invoked
// when the controller starts detectors based on configuration.
type DetectorFactory func(callback EventCallback) (Detector, error)

// Registry manages the set of active detectors and factory functions. It
// provides thread-safe registration, lookup, and lifecycle management.
type Registry struct {
	mu        sync.RWMutex
	detectors map[string]Detector
	factories map[string]DetectorFactory
	logger    *slog.Logger
}

// NewRegistry creates a new detector Registry.
func NewRegistry(logger *slog.Logger) *Registry {
	if logger == nil {
		logger = slog.Default()
	}
	return &Registry{
		detectors: make(map[string]Detector),
		factories: make(map[string]DetectorFactory),
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

// RegisterFactory registers a factory function under the given name. The
// factory can later be used to create a detector instance via CreateFromFactory.
// Returns an error if a factory with the same name is already registered.
func (r *Registry) RegisterFactory(name string, factory DetectorFactory) error {
	if name == "" {
		return fmt.Errorf("factory name must not be empty")
	}
	if factory == nil {
		return fmt.Errorf("factory function must not be nil for %q", name)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.factories[name]; exists {
		return fmt.Errorf("factory %q is already registered", name)
	}

	r.factories[name] = factory
	r.logger.Info("detector factory registered", "detector", name)
	return nil
}

// CreateFromFactory creates a detector using the named factory function and
// registers it in the detector map. Returns an error if the factory is not
// found or if detector creation fails.
func (r *Registry) CreateFromFactory(name string, callback EventCallback) (Detector, error) {
	r.mu.RLock()
	factory, exists := r.factories[name]
	r.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("no factory registered for detector %q", name)
	}

	d, err := factory(callback)
	if err != nil {
		return nil, fmt.Errorf("creating detector %q: %w", name, err)
	}

	if err := r.Register(d); err != nil {
		return nil, err
	}

	return d, nil
}

// GetFactory returns the factory function registered under the given name,
// or nil if not found.
func (r *Registry) GetFactory(name string) DetectorFactory {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.factories[name]
}

// FactoryNames returns the names of all registered factory functions.
func (r *Registry) FactoryNames() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	names := make([]string, 0, len(r.factories))
	for name := range r.factories {
		names = append(names, name)
	}
	return names
}
