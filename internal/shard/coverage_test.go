package shard

import (
	"context"
	"encoding/json"
	"sort"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	metadatafake "k8s.io/client-go/metadata/fake"
)

// TestInformerManager_WithMetadataClient verifies that setting a metadata client
// via the option causes metadata informer factories to be created.
func TestInformerManager_WithMetadataClient(t *testing.T) {
	client := fake.NewSimpleClientset()

	scheme := runtime.NewScheme()
	metav1.AddMetaToScheme(scheme)

	metaClient := metadatafake.NewSimpleMetadataClient(scheme)

	im, err := NewInformerManager(client,
		WithInformerLogger(silentLogger()),
		WithResyncPeriod(time.Hour),
		WithMetadataClient(metaClient),
	)
	if err != nil {
		t.Fatalf("NewInformerManager: %v", err)
	}
	defer im.Stop()

	// Verify the metadata client was stored.
	if im.metadataClient == nil {
		t.Fatal("metadataClient should not be nil after WithMetadataClient")
	}

	// Add namespace - should create both full and metadata factories.
	im.HandleShardChange([]string{"default"}, nil)

	// Full factory should exist.
	if f := im.GetFactory("default"); f == nil {
		t.Error("expected non-nil full factory")
	}

	// Metadata factory should also exist.
	if f := im.GetMetadataFactory("default"); f == nil {
		t.Error("expected non-nil metadata factory after WithMetadataClient")
	}
}

// TestInformerManager_GetMetadataFactory_NoMetadataClient verifies that without
// a metadata client, GetMetadataFactory returns nil.
func TestInformerManager_GetMetadataFactory_NoMetadataClient(t *testing.T) {
	client := fake.NewSimpleClientset()

	im, err := NewInformerManager(client,
		WithInformerLogger(silentLogger()),
		WithResyncPeriod(time.Hour),
	)
	if err != nil {
		t.Fatalf("NewInformerManager: %v", err)
	}
	defer im.Stop()

	im.HandleShardChange([]string{"default"}, nil)

	// Without metadata client, GetMetadataFactory should return nil.
	if f := im.GetMetadataFactory("default"); f != nil {
		t.Error("expected nil metadata factory without metadata client")
	}

	// Non-existent namespace also returns nil.
	if f := im.GetMetadataFactory("nonexistent"); f != nil {
		t.Error("expected nil metadata factory for nonexistent namespace")
	}
}

// TestInformerManager_WaitForCacheSync_WithFactories tests that WaitForCacheSync
// correctly handles factories that have been started.
func TestInformerManager_WaitForCacheSync_WithFactories(t *testing.T) {
	client := fake.NewSimpleClientset()

	im, err := NewInformerManager(client,
		WithInformerLogger(silentLogger()),
		WithResyncPeriod(time.Hour),
	)
	if err != nil {
		t.Fatalf("NewInformerManager: %v", err)
	}
	defer im.Stop()

	// Add a namespace so there are active factories.
	im.HandleShardChange([]string{"default", "payments"}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// With fake client, caches should sync quickly.
	result := im.WaitForCacheSync(ctx)
	// This should succeed since no informers are actually watching anything.
	if !result {
		t.Error("WaitForCacheSync should return true with fake client factories")
	}
}

// TestInformerManager_WaitForCacheSync_ContextTimeout tests that
// WaitForCacheSync respects context cancellation.
func TestInformerManager_WaitForCacheSync_ContextTimeout(t *testing.T) {
	client := fake.NewSimpleClientset()

	im, err := NewInformerManager(client,
		WithInformerLogger(silentLogger()),
		WithResyncPeriod(time.Hour),
	)
	if err != nil {
		t.Fatalf("NewInformerManager: %v", err)
	}
	defer im.Stop()

	// Add namespaces with active factories.
	im.HandleShardChange([]string{"ns-1"}, nil)

	// Cancel context immediately.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Should not hang even if context is already done.
	im.WaitForCacheSync(ctx)
}

// TestInformerManager_MetadataFactory_RemovedOnShardChange tests that
// metadata factories are cleaned up when namespaces are removed.
func TestInformerManager_MetadataFactory_RemovedOnShardChange(t *testing.T) {
	client := fake.NewSimpleClientset()

	scheme := runtime.NewScheme()
	metav1.AddMetaToScheme(scheme)
	metaClient := metadatafake.NewSimpleMetadataClient(scheme)

	im, err := NewInformerManager(client,
		WithInformerLogger(silentLogger()),
		WithResyncPeriod(time.Hour),
		WithMetadataClient(metaClient),
	)
	if err != nil {
		t.Fatalf("NewInformerManager: %v", err)
	}
	defer im.Stop()

	im.HandleShardChange([]string{"default"}, nil)

	if f := im.GetMetadataFactory("default"); f == nil {
		t.Fatal("expected metadata factory for default")
	}

	// Remove the namespace.
	im.HandleShardChange(nil, []string{"default"})

	if f := im.GetMetadataFactory("default"); f != nil {
		t.Error("expected nil metadata factory after namespace removal")
	}
}

// TestManager_SyncFromConfigMap_MissingKey verifies error when ConfigMap
// exists but lacks the shard-map data key.
func TestManager_SyncFromConfigMap_MissingKey(t *testing.T) {
	client := fake.NewSimpleClientset()

	// Create a ConfigMap without the expected key.
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ShardMapConfigMapName,
			Namespace: "wormsign-system",
		},
		Data: map[string]string{
			"wrong-key": "some-data",
		},
	}
	_, err := client.CoreV1().ConfigMaps("wormsign-system").Create(
		context.Background(), cm, metav1.CreateOptions{},
	)
	if err != nil {
		t.Fatalf("creating ConfigMap: %v", err)
	}

	mgr, err := NewManager(client, "wormsign-system", "0", 1,
		WithLogger(silentLogger()),
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// syncFromConfigMap should fail because shard-map key is missing.
	syncErr := mgr.syncFromConfigMap(context.Background())
	if syncErr == nil {
		t.Error("syncFromConfigMap should fail when shard-map key is missing")
	}
	if !containsString(syncErr.Error(), "missing key") {
		t.Errorf("error = %q, expected 'missing key' substring", syncErr.Error())
	}
}

// TestManager_SyncFromConfigMap_InvalidJSON verifies error when ConfigMap
// data contains invalid JSON.
func TestManager_SyncFromConfigMap_InvalidJSON(t *testing.T) {
	client := fake.NewSimpleClientset()

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ShardMapConfigMapName,
			Namespace: "wormsign-system",
		},
		Data: map[string]string{
			shardMapDataKey: "not-valid-json{{{",
		},
	}
	_, err := client.CoreV1().ConfigMaps("wormsign-system").Create(
		context.Background(), cm, metav1.CreateOptions{},
	)
	if err != nil {
		t.Fatalf("creating ConfigMap: %v", err)
	}

	mgr, err := NewManager(client, "wormsign-system", "0", 1,
		WithLogger(silentLogger()),
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	syncErr := mgr.syncFromConfigMap(context.Background())
	if syncErr == nil {
		t.Error("syncFromConfigMap should fail with invalid JSON")
	}
	if !containsString(syncErr.Error(), "unmarshaling") {
		t.Errorf("error = %q, expected 'unmarshaling' substring", syncErr.Error())
	}
}

// TestManager_SyncFromConfigMap_NotFound verifies error when ConfigMap
// doesn't exist.
func TestManager_SyncFromConfigMap_NotFound(t *testing.T) {
	client := fake.NewSimpleClientset()

	mgr, err := NewManager(client, "wormsign-system", "0", 1,
		WithLogger(silentLogger()),
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	syncErr := mgr.syncFromConfigMap(context.Background())
	if syncErr == nil {
		t.Error("syncFromConfigMap should fail when ConfigMap doesn't exist")
	}
}

// TestManager_SyncFromConfigMap_ValidData verifies successful sync path.
func TestManager_SyncFromConfigMap_ValidData(t *testing.T) {
	client := fake.NewSimpleClientset()

	shardMap := ShardMap{
		Assignments: map[string][]string{
			"0": {"default", "payments"},
		},
		NumReplicas: 1,
		UpdatedAt:   time.Now().UTC(),
	}
	data, _ := json.Marshal(shardMap)

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ShardMapConfigMapName,
			Namespace: "wormsign-system",
		},
		Data: map[string]string{
			shardMapDataKey: string(data),
		},
	}
	_, err := client.CoreV1().ConfigMaps("wormsign-system").Create(
		context.Background(), cm, metav1.CreateOptions{},
	)
	if err != nil {
		t.Fatalf("creating ConfigMap: %v", err)
	}

	mgr, err := NewManager(client, "wormsign-system", "0", 1,
		WithLogger(silentLogger()),
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if err := mgr.syncFromConfigMap(context.Background()); err != nil {
		t.Fatalf("syncFromConfigMap failed: %v", err)
	}

	assigned := mgr.AssignedNamespaces()
	sort.Strings(assigned)
	expected := []string{"default", "payments"}
	if len(assigned) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, assigned)
	}
	for i := range expected {
		if assigned[i] != expected[i] {
			t.Errorf("assigned[%d] = %q, want %q", i, assigned[i], expected[i])
		}
	}
}

// TestManager_WriteShardMap_UpdateExisting verifies that writeShardMap
// correctly updates an existing ConfigMap.
func TestManager_WriteShardMap_UpdateExisting(t *testing.T) {
	client := fake.NewSimpleClientset()

	// Pre-create a ConfigMap.
	existingCM := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ShardMapConfigMapName,
			Namespace: "wormsign-system",
			Labels:    map[string]string{"old-label": "true"},
		},
		Data: map[string]string{
			shardMapDataKey: `{"assignments":{},"numReplicas":1}`,
		},
	}
	_, err := client.CoreV1().ConfigMaps("wormsign-system").Create(
		context.Background(), existingCM, metav1.CreateOptions{},
	)
	if err != nil {
		t.Fatalf("creating ConfigMap: %v", err)
	}

	mgr, err := NewManager(client, "wormsign-system", "0", 1,
		WithLogger(silentLogger()),
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// Write a new shard map.
	newMap := &ShardMap{
		Assignments: map[string][]string{
			"0": {"default", "payments"},
		},
		NumReplicas: 1,
		UpdatedAt:   time.Now().UTC(),
	}

	if err := mgr.writeShardMap(context.Background(), newMap); err != nil {
		t.Fatalf("writeShardMap failed: %v", err)
	}

	// Verify the ConfigMap was updated.
	cm, err := client.CoreV1().ConfigMaps("wormsign-system").Get(
		context.Background(), ShardMapConfigMapName, metav1.GetOptions{},
	)
	if err != nil {
		t.Fatalf("getting ConfigMap: %v", err)
	}

	// Labels should be updated.
	if cm.Labels["app.kubernetes.io/managed-by"] != "wormsign" {
		t.Errorf("label managed-by = %q, want wormsign", cm.Labels["app.kubernetes.io/managed-by"])
	}

	// Data should contain the new shard map.
	var readBack ShardMap
	if err := json.Unmarshal([]byte(cm.Data[shardMapDataKey]), &readBack); err != nil {
		t.Fatalf("unmarshaling shard map: %v", err)
	}
	if len(readBack.Assignments["0"]) != 2 {
		t.Errorf("expected 2 assignments for replica 0, got %d", len(readBack.Assignments["0"]))
	}
}

// TestManager_RunFollower_WithValidConfigMap tests that RunFollower correctly
// picks up and applies the shard map.
func TestManager_RunFollower_WithValidConfigMap(t *testing.T) {
	client := fake.NewSimpleClientset()

	shardMap := ShardMap{
		Assignments: map[string][]string{
			"0": {"default", "orders"},
		},
		NumReplicas: 1,
		UpdatedAt:   time.Now().UTC(),
	}
	data, _ := json.Marshal(shardMap)

	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      ShardMapConfigMapName,
			Namespace: "wormsign-system",
		},
		Data: map[string]string{
			shardMapDataKey: string(data),
		},
	}
	_, _ = client.CoreV1().ConfigMaps("wormsign-system").Create(
		context.Background(), cm, metav1.CreateOptions{},
	)

	mgr, err := NewManager(client, "wormsign-system", "0", 1,
		WithLogger(silentLogger()),
		WithReconcileInterval(50*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- mgr.RunFollower(ctx) }()

	time.Sleep(200 * time.Millisecond)
	cancel()
	<-errCh

	assigned := mgr.AssignedNamespaces()
	sort.Strings(assigned)
	if len(assigned) != 2 {
		t.Fatalf("expected 2 assigned namespaces, got %d: %v", len(assigned), assigned)
	}
}

// TestManager_RunFollower_NoConfigMap tests that RunFollower gracefully handles
// a missing ConfigMap (coordinator hasn't written it yet).
func TestManager_RunFollower_NoConfigMap(t *testing.T) {
	client := fake.NewSimpleClientset()

	mgr, err := NewManager(client, "wormsign-system", "0", 1,
		WithLogger(silentLogger()),
		WithReconcileInterval(50*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- mgr.RunFollower(ctx) }()

	// Let it run a few cycles - should not crash.
	time.Sleep(200 * time.Millisecond)
	cancel()

	err = <-errCh
	if err != nil && err != context.Canceled {
		t.Errorf("RunFollower error = %v, want nil or context.Canceled", err)
	}

	// No namespaces should be assigned.
	if len(mgr.AssignedNamespaces()) != 0 {
		t.Error("expected 0 assigned namespaces when ConfigMap doesn't exist")
	}
}

// TestManager_ApplyShardMap_ReplicaIDNotFound tests behavior when the
// replica ID is not present in the shard map.
func TestManager_ApplyShardMap_ReplicaIDNotFound(t *testing.T) {
	client := fake.NewSimpleClientset()

	mgr, err := NewManager(client, "wormsign-system", "5", 1,
		WithLogger(silentLogger()),
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	shardMap := &ShardMap{
		Assignments: map[string][]string{
			"0": {"default"},
			"1": {"payments"},
		},
		NumReplicas: 2,
	}

	mgr.applyShardMap(shardMap)

	if len(mgr.AssignedNamespaces()) != 0 {
		t.Error("expected 0 assigned namespaces when replica ID not in shard map")
	}
}

// TestManager_RunCoordinator_ReconcileError tests that coordinator continues
// running even when reconciliation fails.
func TestManager_RunCoordinator_ReconcileError(t *testing.T) {
	// Use a fake client with no namespace capability (will list empty).
	client := fake.NewSimpleClientset()

	mgr, err := NewManager(client, "wormsign-system", "0", 1,
		WithLogger(silentLogger()),
		WithReconcileInterval(50*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- mgr.RunCoordinator(ctx) }()

	time.Sleep(200 * time.Millisecond)
	cancel()

	err = <-errCh
	if err != nil && err != context.Canceled {
		t.Errorf("RunCoordinator error = %v, want nil or context.Canceled", err)
	}
}

// TestReplicaWatcher_RunLoop tests the full Run loop of the ReplicaWatcher
// with active leases.
func TestReplicaWatcher_RunLoop(t *testing.T) {
	client := fake.NewSimpleClientset()

	mgr, err := NewManager(client, "wormsign-system", "0", 1,
		WithLogger(silentLogger()),
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	rw, err := NewReplicaWatcher(client, "wormsign-system", mgr,
		WithReplicaWatcherLogger(silentLogger()),
		WithWatchInterval(50*time.Millisecond),
		WithReplicaWatcherNowFunc(time.Now),
	)
	if err != nil {
		t.Fatalf("NewReplicaWatcher: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- rw.Run(ctx) }()

	// Let it tick a few times.
	time.Sleep(200 * time.Millisecond)
	cancel()

	err = <-errCh
	if err != nil && err != context.Canceled {
		t.Errorf("Run error = %v, want nil or context.Canceled", err)
	}
}

// containsString checks if s contains substr.
func containsString(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// TestInformerManager_WithMetadataClient_MultipleNamespaces tests metadata
// factories with multiple namespaces.
func TestInformerManager_WithMetadataClient_MultipleNamespaces(t *testing.T) {
	client := fake.NewSimpleClientset()

	scheme := runtime.NewScheme()
	metav1.AddMetaToScheme(scheme)

	// Create a fake metadata client that knows about PartialObjectMetadata.
	metaClient := metadatafake.NewSimpleMetadataClient(scheme,
		&metav1.PartialObjectMetadata{
			TypeMeta: metav1.TypeMeta{
				APIVersion: "v1",
				Kind:       "Pod",
			},
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-pod",
				Namespace: "default",
			},
		},
	)

	im, err := NewInformerManager(client,
		WithInformerLogger(silentLogger()),
		WithResyncPeriod(time.Hour),
		WithMetadataClient(metaClient),
	)
	if err != nil {
		t.Fatalf("NewInformerManager: %v", err)
	}
	defer im.Stop()

	// Add multiple namespaces.
	im.HandleShardChange([]string{"default", "production", "staging"}, nil)

	// All should have metadata factories.
	for _, ns := range []string{"default", "production", "staging"} {
		if f := im.GetMetadataFactory(ns); f == nil {
			t.Errorf("expected non-nil metadata factory for %q", ns)
		}
	}

	// Remove one and verify it's cleaned up.
	im.HandleShardChange(nil, []string{"staging"})
	if f := im.GetMetadataFactory("staging"); f != nil {
		t.Error("expected nil metadata factory after removing staging")
	}
	// Others should still exist.
	if f := im.GetMetadataFactory("default"); f == nil {
		t.Error("expected metadata factory for default to still exist")
	}
}

// Ensure the metadata-only informer factory is registered correctly by
// verifying it can return informers for known resource types.
func TestInformerManager_MetadataFactory_Resource(t *testing.T) {
	client := fake.NewSimpleClientset()

	scheme := runtime.NewScheme()
	metav1.AddMetaToScheme(scheme)
	metaClient := metadatafake.NewSimpleMetadataClient(scheme)

	im, err := NewInformerManager(client,
		WithInformerLogger(silentLogger()),
		WithResyncPeriod(time.Hour),
		WithMetadataClient(metaClient),
	)
	if err != nil {
		t.Fatalf("NewInformerManager: %v", err)
	}
	defer im.Stop()

	im.HandleShardChange([]string{"default"}, nil)

	metaFactory := im.GetMetadataFactory("default")
	if metaFactory == nil {
		t.Fatal("expected non-nil metadata factory")
	}

	// Get a metadata informer for pods - this verifies the factory works.
	podsGVR := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}
	informer := metaFactory.ForResource(podsGVR)
	if informer == nil {
		t.Error("expected non-nil pod metadata informer")
	}
}
