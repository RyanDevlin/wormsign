package shard

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const (
	// ShardMapConfigMapName is the well-known name of the ConfigMap that stores
	// namespace-to-replica shard assignments.
	ShardMapConfigMapName = "wormsign-shard-map"

	// shardMapDataKey is the key within the ConfigMap's data field that holds
	// the JSON-encoded shard map.
	shardMapDataKey = "shard-map"

	// defaultReconcileInterval is how often the coordinator re-evaluates
	// shard assignments even when no explicit event triggers it.
	defaultReconcileInterval = 30 * time.Second
)

// ShardMap is the serialized form of shard assignments stored in the ConfigMap.
// It maps replica identifiers (string indices "0", "1", ...) to lists of
// namespace names assigned to that replica.
type ShardMap struct {
	// Assignments maps replica ID (string) to assigned namespace names.
	Assignments map[string][]string `json:"assignments"`
	// NumReplicas is the number of replicas at the time of assignment.
	NumReplicas int `json:"numReplicas"`
	// UpdatedAt is when the shard map was last computed.
	UpdatedAt time.Time `json:"updatedAt"`
}

// ShardChangeCallback is invoked when the set of namespaces assigned to this
// replica changes. added contains newly assigned namespaces, removed contains
// namespaces that are no longer assigned.
type ShardChangeCallback func(added, removed []string)

// Manager coordinates namespace shard assignments across controller replicas.
// It has two operating modes:
//
//   - Coordinator (leader): watches namespace list, computes shard assignments
//     via consistent hashing, and writes the shard map ConfigMap.
//   - Follower: watches the shard map ConfigMap and applies changes to its
//     local namespace set.
//
// Both modes notify registered callbacks when the local assignment changes.
type Manager struct {
	logger    *slog.Logger
	clientset kubernetes.Interface

	// controllerNamespace is the namespace where the controller runs
	// (where ConfigMaps and Leases are stored).
	controllerNamespace string

	// replicaID is this replica's identifier (typically the pod ordinal
	// or a unique string). Used to look up this replica's assignment
	// in the shard map.
	replicaID string

	// numReplicas is the expected number of replicas. Set from config
	// and updated by the coordinator when scaling events are detected.
	numReplicas int

	// excludeNamespaces is the set of namespace names that should never
	// be assigned to any shard (global filter).
	excludeNamespaces map[string]bool

	mu sync.RWMutex
	// assignedNamespaces is the current set of namespaces assigned to
	// this replica. Protected by mu.
	assignedNamespaces map[string]bool
	// currentShardMap is the last known shard map. Protected by mu.
	currentShardMap *ShardMap

	// callbacks are invoked when the local assignment changes.
	callbacksMu sync.RWMutex
	callbacks   []ShardChangeCallback

	// metricsFunc is called with the count of assigned namespaces whenever
	// the assignment changes.
	metricsFunc func(count float64)

	// nowFunc returns the current time. Overridable for testing.
	nowFunc func() time.Time

	// reconcileInterval controls how often the coordinator re-checks
	// assignments.
	reconcileInterval time.Duration

	// peerSelector is a Kubernetes label selector used to discover
	// controller pods for multi-replica shard assignment. Required
	// when numReplicas > 1 so the coordinator can map shard indices
	// to actual pod names.
	peerSelector string
}

// ManagerOption configures a Manager.
type ManagerOption func(*Manager)

// WithLogger sets the logger for the Manager.
func WithLogger(logger *slog.Logger) ManagerOption {
	return func(m *Manager) {
		m.logger = logger
	}
}

// WithExcludeNamespaces sets namespaces to exclude from shard assignment.
func WithExcludeNamespaces(namespaces []string) ManagerOption {
	return func(m *Manager) {
		m.excludeNamespaces = make(map[string]bool, len(namespaces))
		for _, ns := range namespaces {
			m.excludeNamespaces[ns] = true
		}
	}
}

// WithMetricsFunc sets a function called with the assigned namespace count
// on each assignment change. Typically wired to metrics.ShardNamespaces.Set().
func WithMetricsFunc(fn func(count float64)) ManagerOption {
	return func(m *Manager) {
		m.metricsFunc = fn
	}
}

// WithNowFunc overrides the time source. Intended for testing.
func WithNowFunc(fn func() time.Time) ManagerOption {
	return func(m *Manager) {
		m.nowFunc = fn
	}
}

// WithReconcileInterval sets the interval for periodic reconciliation.
func WithReconcileInterval(d time.Duration) ManagerOption {
	return func(m *Manager) {
		m.reconcileInterval = d
	}
}

// WithPeerSelector sets the label selector used to discover controller pods
// for multi-replica shard assignment. Required when numReplicas > 1.
func WithPeerSelector(selector string) ManagerOption {
	return func(m *Manager) {
		m.peerSelector = selector
	}
}

// NewManager creates a new ShardManager. It does not start any background
// goroutines; call RunCoordinator or RunFollower to start.
func NewManager(
	clientset kubernetes.Interface,
	controllerNamespace string,
	replicaID string,
	numReplicas int,
	opts ...ManagerOption,
) (*Manager, error) {
	if clientset == nil {
		return nil, fmt.Errorf("shard: clientset must not be nil")
	}
	if controllerNamespace == "" {
		return nil, fmt.Errorf("shard: controllerNamespace must not be empty")
	}
	if replicaID == "" {
		return nil, fmt.Errorf("shard: replicaID must not be empty")
	}
	if numReplicas < 1 {
		return nil, fmt.Errorf("shard: numReplicas must be >= 1, got %d", numReplicas)
	}

	mgr := &Manager{
		logger:              slog.Default(),
		clientset:           clientset,
		controllerNamespace: controllerNamespace,
		replicaID:           replicaID,
		numReplicas:         numReplicas,
		excludeNamespaces:   make(map[string]bool),
		assignedNamespaces:  make(map[string]bool),
		metricsFunc:         func(float64) {},
		nowFunc:             time.Now,
		reconcileInterval:   defaultReconcileInterval,
	}

	for _, opt := range opts {
		opt(mgr)
	}

	return mgr, nil
}

// OnShardChange registers a callback that will be invoked when the set of
// namespaces assigned to this replica changes.
func (m *Manager) OnShardChange(cb ShardChangeCallback) {
	m.callbacksMu.Lock()
	defer m.callbacksMu.Unlock()
	m.callbacks = append(m.callbacks, cb)
}

// AssignedNamespaces returns a snapshot of the namespace names currently
// assigned to this replica.
func (m *Manager) AssignedNamespaces() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]string, 0, len(m.assignedNamespaces))
	for ns := range m.assignedNamespaces {
		result = append(result, ns)
	}
	sort.Strings(result)
	return result
}

// IsAssigned reports whether the given namespace is currently assigned to
// this replica.
func (m *Manager) IsAssigned(namespace string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.assignedNamespaces[namespace]
}

// RunCoordinator runs the coordinator loop (leader-only). It periodically
// lists namespaces, computes shard assignments, writes the shard map
// ConfigMap, and updates the local assignment. It blocks until ctx is
// cancelled.
func (m *Manager) RunCoordinator(ctx context.Context) error {
	m.logger.Info("shard coordinator starting",
		"replicaID", m.replicaID,
		"numReplicas", m.numReplicas,
		"controllerNamespace", m.controllerNamespace,
	)

	// Perform an initial reconciliation immediately.
	if err := m.reconcile(ctx); err != nil {
		m.logger.Error("initial shard reconciliation failed", "error", err)
		// Don't return — keep trying on the periodic ticker.
	}

	ticker := time.NewTicker(m.reconcileInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			m.logger.Info("shard coordinator stopping")
			return ctx.Err()
		case <-ticker.C:
			if err := m.reconcile(ctx); err != nil {
				m.logger.Error("shard reconciliation failed", "error", err)
			}
		}
	}
}

// RunFollower runs the follower loop. It periodically reads the shard map
// ConfigMap and updates the local assignment. It blocks until ctx is cancelled.
func (m *Manager) RunFollower(ctx context.Context) error {
	m.logger.Info("shard follower starting",
		"replicaID", m.replicaID,
		"controllerNamespace", m.controllerNamespace,
	)

	// Try to read the initial shard map immediately.
	if err := m.syncFromConfigMap(ctx); err != nil {
		m.logger.Warn("initial shard map sync failed (coordinator may not have written it yet)", "error", err)
	}

	ticker := time.NewTicker(m.reconcileInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			m.logger.Info("shard follower stopping")
			return ctx.Err()
		case <-ticker.C:
			if err := m.syncFromConfigMap(ctx); err != nil {
				m.logger.Error("shard map sync failed", "error", err)
			}
		}
	}
}

// reconcile is the coordinator's core logic: list namespaces, compute
// assignments, write the ConfigMap, and apply local changes.
func (m *Manager) reconcile(ctx context.Context) error {
	// List all namespaces in the cluster.
	nsList, err := m.clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("listing namespaces: %w", err)
	}

	// Filter out excluded namespaces.
	var namespaces []string
	for _, ns := range nsList.Items {
		if m.excludeNamespaces[ns.Name] {
			continue
		}
		namespaces = append(namespaces, ns.Name)
	}
	sort.Strings(namespaces)

	// Compute shard assignments using consistent hashing.
	assignments := AssignNamespaces(namespaces, m.numReplicas)

	// Build the shard map. For single-replica mode, use the replicaID
	// directly as the key. For multi-replica, discover peer pod names
	// and use them as keys so each replica can find its assignment.
	shardMap := &ShardMap{
		Assignments: make(map[string][]string, m.numReplicas),
		NumReplicas: m.numReplicas,
		UpdatedAt:   m.nowFunc().UTC(),
	}
	if m.numReplicas == 1 {
		// Single-replica mode: assign all namespaces to this replica.
		all := assignments[0]
		sort.Strings(all)
		shardMap.Assignments[m.replicaID] = all
	} else {
		// Multi-replica mode: discover peers to map indices to pod names.
		peers, err := m.discoverPeers(ctx)
		if err != nil {
			return fmt.Errorf("discovering peers: %w", err)
		}

		// Adjust replica count to match actual running peers. This handles
		// scale-down (e.g. 3→1 via helm upgrade) where the surviving pod's
		// config still says numReplicas=3 but only 1 peer exists.
		effectiveReplicas := m.numReplicas
		if len(peers) > 0 && len(peers) != m.numReplicas {
			m.logger.Info("adjusting replica count to match discovered peers",
				"configured", m.numReplicas,
				"discovered", len(peers),
			)
			effectiveReplicas = len(peers)
			m.SetNumReplicas(effectiveReplicas)
			// Re-compute assignments with the actual peer count.
			assignments = AssignNamespaces(namespaces, effectiveReplicas)
			shardMap.NumReplicas = effectiveReplicas
		}

		for i := 0; i < effectiveReplicas; i++ {
			assigned := assignments[i]
			sort.Strings(assigned)
			shardMap.Assignments[peers[i]] = assigned
		}
	}

	// Write the shard map to the ConfigMap.
	if err := m.writeShardMap(ctx, shardMap); err != nil {
		return fmt.Errorf("writing shard map: %w", err)
	}

	// Apply to our local assignment.
	m.applyShardMap(shardMap)

	m.logger.Info("shard reconciliation complete",
		"totalNamespaces", len(namespaces),
		"numReplicas", m.numReplicas,
		"assignedToSelf", len(shardMap.Assignments[m.replicaID]),
	)

	return nil
}

// discoverPeers lists controller pods matching the peerSelector and returns
// their names sorted consistently. This provides the index→pod-name mapping
// needed for multi-replica shard assignment.
func (m *Manager) discoverPeers(ctx context.Context) ([]string, error) {
	if m.peerSelector == "" {
		return nil, fmt.Errorf("peer selector not configured (required for numReplicas > 1)")
	}

	pods, err := m.clientset.CoreV1().Pods(m.controllerNamespace).List(ctx, metav1.ListOptions{
		LabelSelector: m.peerSelector,
	})
	if err != nil {
		return nil, fmt.Errorf("listing controller pods: %w", err)
	}

	var names []string
	for _, pod := range pods.Items {
		// Skip terminal pods (completed or failed) — they won't process namespaces.
		if pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}
		names = append(names, pod.Name)
	}
	sort.Strings(names)

	m.logger.Debug("discovered peers",
		"peers", names,
		"count", len(names),
	)
	return names, nil
}

// syncFromConfigMap reads the shard map from the ConfigMap and applies
// local changes. Used by followers.
func (m *Manager) syncFromConfigMap(ctx context.Context) error {
	cm, err := m.clientset.CoreV1().ConfigMaps(m.controllerNamespace).Get(
		ctx, ShardMapConfigMapName, metav1.GetOptions{},
	)
	if err != nil {
		return fmt.Errorf("getting shard map ConfigMap: %w", err)
	}

	data, ok := cm.Data[shardMapDataKey]
	if !ok {
		return fmt.Errorf("shard map ConfigMap missing key %q", shardMapDataKey)
	}

	var shardMap ShardMap
	if err := json.Unmarshal([]byte(data), &shardMap); err != nil {
		return fmt.Errorf("unmarshaling shard map: %w", err)
	}

	m.applyShardMap(&shardMap)
	return nil
}

// writeShardMap creates or updates the shard map ConfigMap.
func (m *Manager) writeShardMap(ctx context.Context, shardMap *ShardMap) error {
	data, err := json.Marshal(shardMap)
	if err != nil {
		return fmt.Errorf("marshaling shard map: %w", err)
	}

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ShardMapConfigMapName,
			Namespace: m.controllerNamespace,
			Labels: map[string]string{
				"app.kubernetes.io/managed-by": "wormsign",
				"app.kubernetes.io/component":  "shard-manager",
			},
		},
		Data: map[string]string{
			shardMapDataKey: string(data),
		},
	}

	// Try to update first; create if not found.
	existing, err := m.clientset.CoreV1().ConfigMaps(m.controllerNamespace).Get(
		ctx, ShardMapConfigMapName, metav1.GetOptions{},
	)
	if errors.IsNotFound(err) {
		_, createErr := m.clientset.CoreV1().ConfigMaps(m.controllerNamespace).Create(
			ctx, cm, metav1.CreateOptions{},
		)
		if createErr != nil {
			return fmt.Errorf("creating shard map ConfigMap: %w", createErr)
		}
		m.logger.Info("created shard map ConfigMap", "namespace", m.controllerNamespace)
		return nil
	}
	if err != nil {
		return fmt.Errorf("checking for existing shard map ConfigMap: %w", err)
	}

	// Update the existing ConfigMap, preserving its resource version.
	existing.Data = cm.Data
	existing.Labels = cm.Labels
	_, err = m.clientset.CoreV1().ConfigMaps(m.controllerNamespace).Update(
		ctx, existing, metav1.UpdateOptions{},
	)
	if err != nil {
		return fmt.Errorf("updating shard map ConfigMap: %w", err)
	}
	return nil
}

// applyShardMap computes the diff between the current and new assignment for
// this replica, updates internal state, and fires callbacks.
func (m *Manager) applyShardMap(shardMap *ShardMap) {
	myNamespaces, ok := shardMap.Assignments[m.replicaID]
	if !ok {
		m.logger.Warn("replica ID not found in shard map, assigning no namespaces",
			"replicaID", m.replicaID,
			"numReplicas", shardMap.NumReplicas,
		)
		myNamespaces = nil
	}

	// Build new assignment set.
	newSet := make(map[string]bool, len(myNamespaces))
	for _, ns := range myNamespaces {
		newSet[ns] = true
	}

	// Compute diff.
	m.mu.Lock()
	oldSet := m.assignedNamespaces

	var added, removed []string
	for ns := range newSet {
		if !oldSet[ns] {
			added = append(added, ns)
		}
	}
	for ns := range oldSet {
		if !newSet[ns] {
			removed = append(removed, ns)
		}
	}
	sort.Strings(added)
	sort.Strings(removed)

	m.assignedNamespaces = newSet
	m.currentShardMap = shardMap
	count := float64(len(newSet))
	m.mu.Unlock()

	// Update metrics.
	m.metricsFunc(count)

	// Fire callbacks only if there are changes.
	if len(added) == 0 && len(removed) == 0 {
		return
	}

	m.logger.Info("shard assignment changed",
		"replicaID", m.replicaID,
		"added", added,
		"removed", removed,
		"totalAssigned", len(newSet),
	)

	m.callbacksMu.RLock()
	cbs := make([]ShardChangeCallback, len(m.callbacks))
	copy(cbs, m.callbacks)
	m.callbacksMu.RUnlock()

	for _, cb := range cbs {
		cb(added, removed)
	}
}

// SetNumReplicas updates the expected number of replicas. The coordinator
// will use this on the next reconciliation cycle.
func (m *Manager) SetNumReplicas(n int) {
	if n < 1 {
		m.logger.Error("ignoring invalid numReplicas", "numReplicas", n)
		return
	}
	m.mu.Lock()
	m.numReplicas = n
	m.mu.Unlock()
	m.logger.Info("numReplicas updated", "numReplicas", n)
}

// GetShardMap returns a copy of the current shard map, or nil if no map
// has been loaded yet.
func (m *Manager) GetShardMap() *ShardMap {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if m.currentShardMap == nil {
		return nil
	}
	// Return a deep copy to prevent mutation.
	cp := &ShardMap{
		Assignments: make(map[string][]string, len(m.currentShardMap.Assignments)),
		NumReplicas: m.currentShardMap.NumReplicas,
		UpdatedAt:   m.currentShardMap.UpdatedAt,
	}
	for k, v := range m.currentShardMap.Assignments {
		ns := make([]string, len(v))
		copy(ns, v)
		cp.Assignments[k] = ns
	}
	return cp
}
