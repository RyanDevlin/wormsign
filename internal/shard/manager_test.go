package shard

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// devNull discards all log output in tests.
type devNull struct{}

func (devNull) Write(p []byte) (int, error) { return len(p), nil }

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(devNull{}, &slog.HandlerOptions{Level: slog.LevelError + 1}))
}

// createFakeNamespaces creates namespace objects in the fake clientset.
func createFakeNamespaces(t *testing.T, client *fake.Clientset, names ...string) {
	t.Helper()
	for _, name := range names {
		_, err := client.CoreV1().Namespaces().Create(
			context.Background(),
			&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: name}},
			metav1.CreateOptions{},
		)
		if err != nil {
			t.Fatalf("creating namespace %q: %v", name, err)
		}
	}
}

func TestNewManager_Validation(t *testing.T) {
	client := fake.NewSimpleClientset()

	// Test valid creation.
	mgr, err := NewManager(client, "wormsign-system", "0", 1, WithLogger(silentLogger()))
	if err != nil {
		t.Fatalf("NewManager() with valid args: %v", err)
	}
	if mgr == nil {
		t.Fatal("NewManager() returned nil manager")
	}

	// Test nil clientset.
	_, err = NewManager(nil, "wormsign-system", "0", 1)
	if err == nil {
		t.Error("NewManager(nil clientset) should fail")
	}

	// Test empty namespace.
	_, err = NewManager(client, "", "0", 1)
	if err == nil {
		t.Error("NewManager(empty namespace) should fail")
	}

	// Test empty replicaID.
	_, err = NewManager(client, "wormsign-system", "", 1)
	if err == nil {
		t.Error("NewManager(empty replicaID) should fail")
	}

	// Test zero replicas.
	_, err = NewManager(client, "wormsign-system", "0", 0)
	if err == nil {
		t.Error("NewManager(0 replicas) should fail")
	}

	// Test negative replicas.
	_, err = NewManager(client, "wormsign-system", "0", -1)
	if err == nil {
		t.Error("NewManager(-1 replicas) should fail")
	}
}

func TestManager_SingleReplica_Coordinator(t *testing.T) {
	client := fake.NewSimpleClientset()
	createFakeNamespaces(t, client, "default", "payments", "orders")

	now := time.Date(2026, 2, 8, 12, 0, 0, 0, time.UTC)
	var metricsCount float64

	mgr, err := NewManager(client, "wormsign-system", "0", 1,
		WithLogger(silentLogger()),
		WithNowFunc(func() time.Time { return now }),
		WithMetricsFunc(func(count float64) { metricsCount = count }),
		WithReconcileInterval(time.Hour), // long interval so only manual reconcile runs
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// Run coordinator with a context that we cancel after first reconcile.
	ctx, cancel := context.WithCancel(context.Background())

	// Use RunCoordinator in a goroutine and cancel shortly after.
	errCh := make(chan error, 1)
	go func() {
		errCh <- mgr.RunCoordinator(ctx)
	}()

	// Wait a bit for the initial reconcile to happen.
	time.Sleep(200 * time.Millisecond)
	cancel()

	// Wait for the goroutine to exit.
	if err := <-errCh; err != nil && err != context.Canceled {
		t.Fatalf("RunCoordinator: %v", err)
	}

	// Verify all namespaces assigned to replica 0.
	assigned := mgr.AssignedNamespaces()
	sort.Strings(assigned)
	expected := []string{"default", "orders", "payments"}
	if len(assigned) != len(expected) {
		t.Fatalf("assigned namespaces: got %v, want %v", assigned, expected)
	}
	for i := range expected {
		if assigned[i] != expected[i] {
			t.Errorf("assigned[%d] = %q, want %q", i, assigned[i], expected[i])
		}
	}

	// Verify metrics were updated.
	if metricsCount != 3 {
		t.Errorf("metrics count = %v, want 3", metricsCount)
	}

	// Verify IsAssigned works.
	if !mgr.IsAssigned("payments") {
		t.Error("IsAssigned(payments) = false, want true")
	}
	if mgr.IsAssigned("nonexistent") {
		t.Error("IsAssigned(nonexistent) = true, want false")
	}

	// Verify the ConfigMap was written.
	cm, err := client.CoreV1().ConfigMaps("wormsign-system").Get(
		context.Background(), ShardMapConfigMapName, metav1.GetOptions{},
	)
	if err != nil {
		t.Fatalf("reading shard map ConfigMap: %v", err)
	}
	data, ok := cm.Data[shardMapDataKey]
	if !ok {
		t.Fatal("ConfigMap missing shard-map key")
	}

	var shardMap ShardMap
	if err := json.Unmarshal([]byte(data), &shardMap); err != nil {
		t.Fatalf("unmarshaling shard map: %v", err)
	}
	if shardMap.NumReplicas != 1 {
		t.Errorf("shard map NumReplicas = %d, want 1", shardMap.NumReplicas)
	}
	if len(shardMap.Assignments["0"]) != 3 {
		t.Errorf("shard map assignment[0] has %d namespaces, want 3",
			len(shardMap.Assignments["0"]))
	}
}

func TestManager_ExcludeNamespaces(t *testing.T) {
	client := fake.NewSimpleClientset()
	createFakeNamespaces(t, client, "default", "kube-system", "kube-public", "payments")

	mgr, err := NewManager(client, "wormsign-system", "0", 1,
		WithLogger(silentLogger()),
		WithExcludeNamespaces([]string{"kube-system", "kube-public"}),
		WithReconcileInterval(time.Hour),
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- mgr.RunCoordinator(ctx) }()

	time.Sleep(200 * time.Millisecond)
	cancel()
	<-errCh

	assigned := mgr.AssignedNamespaces()
	sort.Strings(assigned)
	expected := []string{"default", "payments"}
	if len(assigned) != len(expected) {
		t.Fatalf("assigned: got %v, want %v", assigned, expected)
	}
	for i := range expected {
		if assigned[i] != expected[i] {
			t.Errorf("assigned[%d] = %q, want %q", i, assigned[i], expected[i])
		}
	}

	// Excluded namespaces should not be assigned.
	if mgr.IsAssigned("kube-system") {
		t.Error("kube-system should be excluded")
	}
}

func TestManager_MultipleReplicas_Coordinator(t *testing.T) {
	client := fake.NewSimpleClientset()
	namespaces := []string{
		"ns-0", "ns-1", "ns-2", "ns-3", "ns-4",
		"ns-5", "ns-6", "ns-7", "ns-8", "ns-9",
	}
	createFakeNamespaces(t, client, namespaces...)

	// Create coordinator as replica 0 of 3.
	mgr, err := NewManager(client, "wormsign-system", "0", 3,
		WithLogger(silentLogger()),
		WithReconcileInterval(time.Hour),
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- mgr.RunCoordinator(ctx) }()

	time.Sleep(200 * time.Millisecond)
	cancel()
	<-errCh

	// Get the shard map.
	shardMap := mgr.GetShardMap()
	if shardMap == nil {
		t.Fatal("GetShardMap() returned nil")
	}
	if shardMap.NumReplicas != 3 {
		t.Errorf("NumReplicas = %d, want 3", shardMap.NumReplicas)
	}

	// Verify all namespaces are assigned exactly once.
	allAssigned := make(map[string]bool)
	for i := 0; i < 3; i++ {
		key := fmt.Sprintf("%d", i)
		for _, ns := range shardMap.Assignments[key] {
			if allAssigned[ns] {
				t.Errorf("namespace %q assigned to multiple replicas", ns)
			}
			allAssigned[ns] = true
		}
		t.Logf("replica %d: %v", i, shardMap.Assignments[key])
	}
	for _, ns := range namespaces {
		if !allAssigned[ns] {
			t.Errorf("namespace %q not assigned to any replica", ns)
		}
	}
}

func TestManager_Follower(t *testing.T) {
	client := fake.NewSimpleClientset()

	// Pre-create the shard map ConfigMap as if the coordinator wrote it.
	shardMap := &ShardMap{
		Assignments: map[string][]string{
			"0": {"default", "payments"},
			"1": {"orders", "shipping"},
			"2": {"analytics"},
		},
		NumReplicas: 3,
		UpdatedAt:   time.Now().UTC(),
	}
	data, _ := json.Marshal(shardMap)

	_, err := client.CoreV1().ConfigMaps("wormsign-system").Create(
		context.Background(),
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      ShardMapConfigMapName,
				Namespace: "wormsign-system",
			},
			Data: map[string]string{
				shardMapDataKey: string(data),
			},
		},
		metav1.CreateOptions{},
	)
	if err != nil {
		t.Fatalf("creating ConfigMap: %v", err)
	}

	// Create follower as replica 1.
	var metricsCount float64
	mgr, err := NewManager(client, "wormsign-system", "1", 3,
		WithLogger(silentLogger()),
		WithMetricsFunc(func(count float64) { metricsCount = count }),
		WithReconcileInterval(time.Hour),
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

	// Replica 1 should have orders and shipping.
	assigned := mgr.AssignedNamespaces()
	sort.Strings(assigned)
	expected := []string{"orders", "shipping"}
	if len(assigned) != len(expected) {
		t.Fatalf("follower assigned: got %v, want %v", assigned, expected)
	}
	for i := range expected {
		if assigned[i] != expected[i] {
			t.Errorf("assigned[%d] = %q, want %q", i, assigned[i], expected[i])
		}
	}

	if metricsCount != 2 {
		t.Errorf("metrics count = %v, want 2", metricsCount)
	}
}

func TestManager_Follower_MissingConfigMap(t *testing.T) {
	// Follower should handle missing ConfigMap gracefully.
	client := fake.NewSimpleClientset()

	mgr, err := NewManager(client, "wormsign-system", "0", 1,
		WithLogger(silentLogger()),
		WithReconcileInterval(time.Hour),
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

	// Should have no assigned namespaces (ConfigMap doesn't exist yet).
	assigned := mgr.AssignedNamespaces()
	if len(assigned) != 0 {
		t.Errorf("expected 0 assigned namespaces, got %v", assigned)
	}
}

func TestManager_ShardChangeCallback(t *testing.T) {
	client := fake.NewSimpleClientset()
	createFakeNamespaces(t, client, "ns-a", "ns-b", "ns-c")

	mgr, err := NewManager(client, "wormsign-system", "0", 1,
		WithLogger(silentLogger()),
		WithReconcileInterval(time.Hour),
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	var mu sync.Mutex
	var callbackAdded, callbackRemoved []string
	callbackCalled := make(chan struct{}, 1)

	mgr.OnShardChange(func(added, removed []string) {
		mu.Lock()
		defer mu.Unlock()
		callbackAdded = added
		callbackRemoved = removed
		select {
		case callbackCalled <- struct{}{}:
		default:
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- mgr.RunCoordinator(ctx) }()

	// Wait for callback.
	select {
	case <-callbackCalled:
	case <-time.After(2 * time.Second):
		t.Fatal("callback not called within timeout")
	}
	cancel()
	<-errCh

	mu.Lock()
	defer mu.Unlock()

	sort.Strings(callbackAdded)
	expected := []string{"ns-a", "ns-b", "ns-c"}
	if len(callbackAdded) != len(expected) {
		t.Fatalf("callback added: got %v, want %v", callbackAdded, expected)
	}
	for i := range expected {
		if callbackAdded[i] != expected[i] {
			t.Errorf("callbackAdded[%d] = %q, want %q", i, callbackAdded[i], expected[i])
		}
	}
	if len(callbackRemoved) != 0 {
		t.Errorf("callback removed: got %v, want empty", callbackRemoved)
	}
}

func TestManager_ConfigMapUpdate(t *testing.T) {
	// Test that the coordinator updates an existing ConfigMap rather than
	// failing on create conflict.
	client := fake.NewSimpleClientset()

	// Pre-create a ConfigMap.
	_, err := client.CoreV1().ConfigMaps("wormsign-system").Create(
		context.Background(),
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      ShardMapConfigMapName,
				Namespace: "wormsign-system",
			},
			Data: map[string]string{
				shardMapDataKey: `{"assignments":{},"numReplicas":1,"updatedAt":"2026-01-01T00:00:00Z"}`,
			},
		},
		metav1.CreateOptions{},
	)
	if err != nil {
		t.Fatalf("creating initial ConfigMap: %v", err)
	}

	createFakeNamespaces(t, client, "default", "payments")

	mgr, err := NewManager(client, "wormsign-system", "0", 1,
		WithLogger(silentLogger()),
		WithReconcileInterval(time.Hour),
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- mgr.RunCoordinator(ctx) }()

	time.Sleep(200 * time.Millisecond)
	cancel()
	<-errCh

	// Verify the ConfigMap was updated (not created).
	cm, err := client.CoreV1().ConfigMaps("wormsign-system").Get(
		context.Background(), ShardMapConfigMapName, metav1.GetOptions{},
	)
	if err != nil {
		t.Fatalf("reading ConfigMap: %v", err)
	}

	var shardMap ShardMap
	if err := json.Unmarshal([]byte(cm.Data[shardMapDataKey]), &shardMap); err != nil {
		t.Fatalf("unmarshaling: %v", err)
	}

	if len(shardMap.Assignments["0"]) != 2 {
		t.Errorf("expected 2 namespaces in assignment, got %d", len(shardMap.Assignments["0"]))
	}

	// Verify labels were set.
	if cm.Labels["app.kubernetes.io/managed-by"] != "wormsign" {
		t.Error("ConfigMap missing managed-by label")
	}
}

func TestManager_SetNumReplicas(t *testing.T) {
	client := fake.NewSimpleClientset()

	mgr, err := NewManager(client, "wormsign-system", "0", 1,
		WithLogger(silentLogger()),
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// Setting a valid value should work.
	mgr.SetNumReplicas(3)
	mgr.mu.RLock()
	if mgr.numReplicas != 3 {
		t.Errorf("numReplicas = %d, want 3", mgr.numReplicas)
	}
	mgr.mu.RUnlock()

	// Setting an invalid value should be ignored.
	mgr.SetNumReplicas(0)
	mgr.mu.RLock()
	if mgr.numReplicas != 3 {
		t.Errorf("numReplicas = %d after invalid SetNumReplicas(0), want 3", mgr.numReplicas)
	}
	mgr.mu.RUnlock()
}

func TestManager_GetShardMap_Nil(t *testing.T) {
	client := fake.NewSimpleClientset()

	mgr, err := NewManager(client, "wormsign-system", "0", 1,
		WithLogger(silentLogger()),
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// Before any reconciliation, GetShardMap returns nil.
	if sm := mgr.GetShardMap(); sm != nil {
		t.Errorf("GetShardMap() before reconciliation = %+v, want nil", sm)
	}
}

func TestManager_GetShardMap_DeepCopy(t *testing.T) {
	client := fake.NewSimpleClientset()
	createFakeNamespaces(t, client, "default")

	mgr, err := NewManager(client, "wormsign-system", "0", 1,
		WithLogger(silentLogger()),
		WithReconcileInterval(time.Hour),
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- mgr.RunCoordinator(ctx) }()
	time.Sleep(200 * time.Millisecond)
	cancel()
	<-errCh

	// Get two copies and mutate one.
	sm1 := mgr.GetShardMap()
	sm2 := mgr.GetShardMap()

	sm1.Assignments["0"] = append(sm1.Assignments["0"], "mutated")

	// sm2 should not be affected.
	if len(sm2.Assignments["0"]) != 1 {
		t.Errorf("deep copy violated: sm2 has %d entries after mutating sm1",
			len(sm2.Assignments["0"]))
	}
}

func TestManager_ReplicaIDNotInShardMap(t *testing.T) {
	client := fake.NewSimpleClientset()

	// Pre-create a shard map that doesn't include replica "5".
	shardMap := &ShardMap{
		Assignments: map[string][]string{
			"0": {"default"},
			"1": {"payments"},
		},
		NumReplicas: 2,
		UpdatedAt:   time.Now().UTC(),
	}
	data, _ := json.Marshal(shardMap)

	_, err := client.CoreV1().ConfigMaps("wormsign-system").Create(
		context.Background(),
		&corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      ShardMapConfigMapName,
				Namespace: "wormsign-system",
			},
			Data: map[string]string{
				shardMapDataKey: string(data),
			},
		},
		metav1.CreateOptions{},
	)
	if err != nil {
		t.Fatalf("creating ConfigMap: %v", err)
	}

	// Create a follower with replica ID not in the map.
	mgr, err := NewManager(client, "wormsign-system", "5", 2,
		WithLogger(silentLogger()),
		WithReconcileInterval(time.Hour),
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

	// Should have no namespaces.
	assigned := mgr.AssignedNamespaces()
	if len(assigned) != 0 {
		t.Errorf("expected 0 namespaces for unknown replica, got %v", assigned)
	}
}

func TestManager_Reconcile_NoChangeNoCallback(t *testing.T) {
	client := fake.NewSimpleClientset()
	createFakeNamespaces(t, client, "default")

	mgr, err := NewManager(client, "wormsign-system", "0", 1,
		WithLogger(silentLogger()),
		WithReconcileInterval(50*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	callbackCount := 0
	mgr.OnShardChange(func(added, removed []string) {
		callbackCount++
	})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- mgr.RunCoordinator(ctx) }()

	// Let several reconcile cycles happen.
	time.Sleep(300 * time.Millisecond)
	cancel()
	<-errCh

	// The callback should only fire once (initial assignment), not on every
	// reconcile cycle, since the namespaces don't change.
	if callbackCount != 1 {
		t.Errorf("callback called %d times, expected 1 (only on initial assignment)", callbackCount)
	}
}

func TestManager_MultipleCallbacks(t *testing.T) {
	client := fake.NewSimpleClientset()
	createFakeNamespaces(t, client, "default")

	mgr, err := NewManager(client, "wormsign-system", "0", 1,
		WithLogger(silentLogger()),
		WithReconcileInterval(time.Hour),
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	var mu sync.Mutex
	called := make([]int, 3)

	for i := 0; i < 3; i++ {
		idx := i
		mgr.OnShardChange(func(added, removed []string) {
			mu.Lock()
			defer mu.Unlock()
			called[idx]++
		})
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- mgr.RunCoordinator(ctx) }()

	time.Sleep(200 * time.Millisecond)
	cancel()
	<-errCh

	mu.Lock()
	defer mu.Unlock()
	for i, c := range called {
		if c != 1 {
			t.Errorf("callback %d called %d times, want 1", i, c)
		}
	}
}

func TestShardMap_JSON_Roundtrip(t *testing.T) {
	original := &ShardMap{
		Assignments: map[string][]string{
			"0": {"default", "payments"},
			"1": {"orders"},
			"2": {},
		},
		NumReplicas: 3,
		UpdatedAt:   time.Date(2026, 2, 8, 12, 0, 0, 0, time.UTC),
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var decoded ShardMap
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if decoded.NumReplicas != original.NumReplicas {
		t.Errorf("NumReplicas = %d, want %d", decoded.NumReplicas, original.NumReplicas)
	}
	if !decoded.UpdatedAt.Equal(original.UpdatedAt) {
		t.Errorf("UpdatedAt = %v, want %v", decoded.UpdatedAt, original.UpdatedAt)
	}
	for key, expected := range original.Assignments {
		got := decoded.Assignments[key]
		if len(got) != len(expected) {
			t.Errorf("Assignments[%s]: got %v, want %v", key, got, expected)
		}
	}
}

func TestManager_ConcurrentAccess(t *testing.T) {
	client := fake.NewSimpleClientset()
	createFakeNamespaces(t, client, "default", "payments")

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

	// Concurrently read while coordinator is running.
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				_ = mgr.AssignedNamespaces()
				_ = mgr.IsAssigned("default")
				_ = mgr.GetShardMap()
			}
		}()
	}

	wg.Wait()
	cancel()
	<-errCh
}
