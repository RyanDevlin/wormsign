package shard

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
)

const (
	// defaultResyncPeriod is the default resync period for per-namespace informer factories.
	defaultResyncPeriod = 30 * time.Minute
)

// InformerManager manages namespace-scoped shared informer factories.
// When the shard assignment changes (namespaces are added or removed),
// it tears down informer factories for removed namespaces and creates
// new ones for added namespaces.
//
// This implements the informer lifecycle described in Section 3.1.1 of
// the project specification.
type InformerManager struct {
	logger       *slog.Logger
	clientset    kubernetes.Interface
	resyncPeriod time.Duration

	mu        sync.RWMutex
	factories map[string]*namespaceInformerEntry // namespace name → entry
	stopped   bool
}

// namespaceInformerEntry tracks an informer factory and its cancellation.
type namespaceInformerEntry struct {
	factory informers.SharedInformerFactory
	cancel  context.CancelFunc
}

// InformerManagerOption configures an InformerManager.
type InformerManagerOption func(*InformerManager)

// WithInformerLogger sets the logger for the InformerManager.
func WithInformerLogger(logger *slog.Logger) InformerManagerOption {
	return func(im *InformerManager) {
		im.logger = logger
	}
}

// WithResyncPeriod sets the informer resync period.
func WithResyncPeriod(d time.Duration) InformerManagerOption {
	return func(im *InformerManager) {
		im.resyncPeriod = d
	}
}

// NewInformerManager creates a new InformerManager. It does not start
// any informers; call HandleShardChange or register it as a shard
// change callback with Manager.OnShardChange.
func NewInformerManager(
	clientset kubernetes.Interface,
	opts ...InformerManagerOption,
) (*InformerManager, error) {
	if clientset == nil {
		return nil, fmt.Errorf("shard: InformerManager clientset must not be nil")
	}

	im := &InformerManager{
		logger:       slog.Default(),
		clientset:    clientset,
		resyncPeriod: defaultResyncPeriod,
		factories:    make(map[string]*namespaceInformerEntry),
	}

	for _, opt := range opts {
		opt(im)
	}

	return im, nil
}

// HandleShardChange is a ShardChangeCallback that creates informer factories
// for added namespaces and tears down factories for removed namespaces.
// It is safe to call from multiple goroutines.
func (im *InformerManager) HandleShardChange(added, removed []string) {
	im.mu.Lock()
	defer im.mu.Unlock()

	if im.stopped {
		im.logger.Warn("informer manager is stopped, ignoring shard change")
		return
	}

	// Tear down informers for removed namespaces.
	for _, ns := range removed {
		entry, ok := im.factories[ns]
		if !ok {
			continue
		}
		im.logger.Info("stopping informers for removed namespace",
			"namespace", ns,
		)
		entry.cancel()
		delete(im.factories, ns)
	}

	// Create informers for added namespaces.
	for _, ns := range added {
		if _, exists := im.factories[ns]; exists {
			im.logger.Warn("informer factory already exists for namespace, skipping",
				"namespace", ns,
			)
			continue
		}
		im.logger.Info("starting informers for new namespace",
			"namespace", ns,
		)
		im.startFactory(ns)
	}
}

// startFactory creates and starts a namespace-scoped informer factory.
// Must be called with im.mu held.
func (im *InformerManager) startFactory(namespace string) {
	factory := informers.NewSharedInformerFactoryWithOptions(
		im.clientset,
		im.resyncPeriod,
		informers.WithNamespace(namespace),
	)

	ctx, cancel := context.WithCancel(context.Background())
	factory.Start(ctx.Done())

	im.factories[namespace] = &namespaceInformerEntry{
		factory: factory,
		cancel:  cancel,
	}
}

// GetFactory returns the informer factory for the given namespace, or nil
// if no factory exists (namespace is not assigned to this shard).
func (im *InformerManager) GetFactory(namespace string) informers.SharedInformerFactory {
	im.mu.RLock()
	defer im.mu.RUnlock()
	entry, ok := im.factories[namespace]
	if !ok {
		return nil
	}
	return entry.factory
}

// Namespaces returns the list of namespaces that currently have active
// informer factories.
func (im *InformerManager) Namespaces() []string {
	im.mu.RLock()
	defer im.mu.RUnlock()
	result := make([]string, 0, len(im.factories))
	for ns := range im.factories {
		result = append(result, ns)
	}
	return result
}

// WaitForCacheSync waits for all active informer factories to sync their
// caches. Returns true if all caches synced within the context deadline,
// false otherwise.
func (im *InformerManager) WaitForCacheSync(ctx context.Context) bool {
	im.mu.RLock()
	entries := make(map[string]*namespaceInformerEntry, len(im.factories))
	for k, v := range im.factories {
		entries[k] = v
	}
	im.mu.RUnlock()

	allSynced := true
	for ns, entry := range entries {
		synced := entry.factory.WaitForCacheSync(ctx.Done())
		for typ, ok := range synced {
			if !ok {
				im.logger.Warn("cache sync failed",
					"namespace", ns,
					"type", typ.String(),
				)
				allSynced = false
			}
		}
	}
	return allSynced
}

// Stop tears down all informer factories. After Stop is called,
// HandleShardChange will be a no-op.
func (im *InformerManager) Stop() {
	im.mu.Lock()
	defer im.mu.Unlock()

	im.stopped = true
	for ns, entry := range im.factories {
		im.logger.Info("stopping informers on shutdown",
			"namespace", ns,
		)
		entry.cancel()
	}
	im.factories = make(map[string]*namespaceInformerEntry)
}
