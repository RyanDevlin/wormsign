package shard

import (
	"context"
	"sort"
	"testing"
	"time"

	"k8s.io/client-go/kubernetes/fake"
)

func TestNewInformerManager_Validation(t *testing.T) {
	client := fake.NewSimpleClientset()

	im, err := NewInformerManager(client, WithInformerLogger(silentLogger()))
	if err != nil {
		t.Fatalf("NewInformerManager: %v", err)
	}
	if im == nil {
		t.Fatal("NewInformerManager returned nil")
	}

	_, err = NewInformerManager(nil)
	if err == nil {
		t.Error("NewInformerManager(nil) should fail")
	}
}

func TestInformerManager_HandleShardChange_AddNamespaces(t *testing.T) {
	client := fake.NewSimpleClientset()

	im, err := NewInformerManager(client,
		WithInformerLogger(silentLogger()),
		WithResyncPeriod(time.Hour),
	)
	if err != nil {
		t.Fatalf("NewInformerManager: %v", err)
	}
	defer im.Stop()

	// Initially no namespaces.
	if ns := im.Namespaces(); len(ns) != 0 {
		t.Fatalf("expected 0 namespaces initially, got %v", ns)
	}

	// Add some namespaces.
	im.HandleShardChange([]string{"default", "payments", "orders"}, nil)

	ns := im.Namespaces()
	sort.Strings(ns)
	expected := []string{"default", "orders", "payments"}
	if len(ns) != len(expected) {
		t.Fatalf("expected namespaces %v, got %v", expected, ns)
	}
	for i := range expected {
		if ns[i] != expected[i] {
			t.Errorf("namespace[%d] = %q, want %q", i, ns[i], expected[i])
		}
	}
}

func TestInformerManager_HandleShardChange_RemoveNamespaces(t *testing.T) {
	client := fake.NewSimpleClientset()

	im, err := NewInformerManager(client,
		WithInformerLogger(silentLogger()),
		WithResyncPeriod(time.Hour),
	)
	if err != nil {
		t.Fatalf("NewInformerManager: %v", err)
	}
	defer im.Stop()

	// Add namespaces.
	im.HandleShardChange([]string{"default", "payments", "orders"}, nil)

	// Remove one.
	im.HandleShardChange(nil, []string{"payments"})

	ns := im.Namespaces()
	sort.Strings(ns)
	expected := []string{"default", "orders"}
	if len(ns) != len(expected) {
		t.Fatalf("expected namespaces %v, got %v", expected, ns)
	}
	for i := range expected {
		if ns[i] != expected[i] {
			t.Errorf("namespace[%d] = %q, want %q", i, ns[i], expected[i])
		}
	}
}

func TestInformerManager_HandleShardChange_AddAndRemoveSimultaneously(t *testing.T) {
	client := fake.NewSimpleClientset()

	im, err := NewInformerManager(client,
		WithInformerLogger(silentLogger()),
		WithResyncPeriod(time.Hour),
	)
	if err != nil {
		t.Fatalf("NewInformerManager: %v", err)
	}
	defer im.Stop()

	// Start with some namespaces.
	im.HandleShardChange([]string{"ns-a", "ns-b", "ns-c"}, nil)

	// Add ns-d, remove ns-b.
	im.HandleShardChange([]string{"ns-d"}, []string{"ns-b"})

	ns := im.Namespaces()
	sort.Strings(ns)
	expected := []string{"ns-a", "ns-c", "ns-d"}
	if len(ns) != len(expected) {
		t.Fatalf("expected namespaces %v, got %v", expected, ns)
	}
	for i := range expected {
		if ns[i] != expected[i] {
			t.Errorf("namespace[%d] = %q, want %q", i, ns[i], expected[i])
		}
	}
}

func TestInformerManager_GetFactory(t *testing.T) {
	client := fake.NewSimpleClientset()

	im, err := NewInformerManager(client,
		WithInformerLogger(silentLogger()),
		WithResyncPeriod(time.Hour),
	)
	if err != nil {
		t.Fatalf("NewInformerManager: %v", err)
	}
	defer im.Stop()

	// No factory before adding.
	if f := im.GetFactory("default"); f != nil {
		t.Error("expected nil factory for unadded namespace")
	}

	// Add namespace.
	im.HandleShardChange([]string{"default"}, nil)

	f := im.GetFactory("default")
	if f == nil {
		t.Fatal("expected non-nil factory after adding namespace")
	}

	// Factory for non-existent namespace still nil.
	if f := im.GetFactory("nonexistent"); f != nil {
		t.Error("expected nil factory for nonexistent namespace")
	}
}

func TestInformerManager_Stop(t *testing.T) {
	client := fake.NewSimpleClientset()

	im, err := NewInformerManager(client,
		WithInformerLogger(silentLogger()),
		WithResyncPeriod(time.Hour),
	)
	if err != nil {
		t.Fatalf("NewInformerManager: %v", err)
	}

	// Add namespaces.
	im.HandleShardChange([]string{"default", "payments"}, nil)
	if len(im.Namespaces()) != 2 {
		t.Fatal("expected 2 namespaces before stop")
	}

	// Stop should clear all factories.
	im.Stop()

	if ns := im.Namespaces(); len(ns) != 0 {
		t.Errorf("expected 0 namespaces after stop, got %v", ns)
	}

	// HandleShardChange after stop should be a no-op.
	im.HandleShardChange([]string{"new-ns"}, nil)
	if ns := im.Namespaces(); len(ns) != 0 {
		t.Errorf("expected 0 namespaces after adding to stopped manager, got %v", ns)
	}
}

func TestInformerManager_DuplicateAdd(t *testing.T) {
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
	f1 := im.GetFactory("default")

	// Adding the same namespace again should be a no-op (skip, not replace).
	im.HandleShardChange([]string{"default"}, nil)
	f2 := im.GetFactory("default")

	// The factory pointer should be the same (not recreated).
	if f1 != f2 {
		t.Error("expected same factory pointer on duplicate add")
	}
}

func TestInformerManager_RemoveNonexistent(t *testing.T) {
	client := fake.NewSimpleClientset()

	im, err := NewInformerManager(client,
		WithInformerLogger(silentLogger()),
		WithResyncPeriod(time.Hour),
	)
	if err != nil {
		t.Fatalf("NewInformerManager: %v", err)
	}
	defer im.Stop()

	// Removing a namespace that doesn't exist should not panic.
	im.HandleShardChange(nil, []string{"nonexistent"})
}

func TestInformerManager_WaitForCacheSync(t *testing.T) {
	client := fake.NewSimpleClientset()

	im, err := NewInformerManager(client,
		WithInformerLogger(silentLogger()),
		WithResyncPeriod(time.Hour),
	)
	if err != nil {
		t.Fatalf("NewInformerManager: %v", err)
	}
	defer im.Stop()

	// With no factories, WaitForCacheSync should return true immediately.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if !im.WaitForCacheSync(ctx) {
		t.Error("WaitForCacheSync should return true with no factories")
	}
}

func TestInformerManager_IntegrationWithManager(t *testing.T) {
	// Test that InformerManager works as a shard change callback.
	client := fake.NewSimpleClientset()
	createFakeNamespaces(t, client, "default", "payments")

	mgr, err := NewManager(client, "wormsign-system", "0", 1,
		WithLogger(silentLogger()),
		WithReconcileInterval(time.Hour),
	)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	im, err := NewInformerManager(client,
		WithInformerLogger(silentLogger()),
		WithResyncPeriod(time.Hour),
	)
	if err != nil {
		t.Fatalf("NewInformerManager: %v", err)
	}
	defer im.Stop()

	// Register InformerManager as a shard change callback.
	mgr.OnShardChange(im.HandleShardChange)

	// Run coordinator which should trigger the callback.
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- mgr.RunCoordinator(ctx) }()

	time.Sleep(200 * time.Millisecond)
	cancel()
	<-errCh

	// InformerManager should now have factories for both namespaces.
	ns := im.Namespaces()
	sort.Strings(ns)
	expected := []string{"default", "payments"}
	if len(ns) != len(expected) {
		t.Fatalf("expected namespaces %v, got %v", expected, ns)
	}
	for i := range expected {
		if ns[i] != expected[i] {
			t.Errorf("namespace[%d] = %q, want %q", i, ns[i], expected[i])
		}
	}

	// Verify factories exist.
	for _, name := range expected {
		if im.GetFactory(name) == nil {
			t.Errorf("no factory for namespace %q", name)
		}
	}
}
