package shard

import (
	"context"
	"testing"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func createFakeLease(t *testing.T, client *fake.Clientset, namespace, name string, renewTime time.Time) {
	t.Helper()
	micro := metav1.NewMicroTime(renewTime)
	_, err := client.CoordinationV1().Leases(namespace).Create(
		context.Background(),
		&coordinationv1.Lease{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
			},
			Spec: coordinationv1.LeaseSpec{
				RenewTime: &micro,
			},
		},
		metav1.CreateOptions{},
	)
	if err != nil {
		t.Fatalf("creating lease %q: %v", name, err)
	}
}

func TestNewReplicaWatcher_Validation(t *testing.T) {
	client := fake.NewSimpleClientset()
	mgr, _ := NewManager(client, "wormsign-system", "0", 1, WithLogger(silentLogger()))

	// Valid creation.
	rw, err := NewReplicaWatcher(client, "wormsign-system", mgr,
		WithReplicaWatcherLogger(silentLogger()),
	)
	if err != nil {
		t.Fatalf("NewReplicaWatcher: %v", err)
	}
	if rw == nil {
		t.Fatal("NewReplicaWatcher returned nil")
	}

	// Nil clientset.
	_, err = NewReplicaWatcher(nil, "wormsign-system", mgr)
	if err == nil {
		t.Error("NewReplicaWatcher(nil clientset) should fail")
	}

	// Empty namespace.
	_, err = NewReplicaWatcher(client, "", mgr)
	if err == nil {
		t.Error("NewReplicaWatcher(empty namespace) should fail")
	}

	// Nil manager.
	_, err = NewReplicaWatcher(client, "wormsign-system", nil)
	if err == nil {
		t.Error("NewReplicaWatcher(nil manager) should fail")
	}
}

func TestReplicaWatcher_CountsActiveReplicas(t *testing.T) {
	now := time.Date(2026, 2, 8, 12, 0, 0, 0, time.UTC)
	client := fake.NewSimpleClientset()

	// Create 3 active wormsign Leases (renewed recently).
	createFakeLease(t, client, "wormsign-system", "wormsign-0", now.Add(-5*time.Second))
	createFakeLease(t, client, "wormsign-system", "wormsign-1", now.Add(-10*time.Second))
	createFakeLease(t, client, "wormsign-system", "wormsign-2", now.Add(-20*time.Second))

	// Create the leader election Lease (should be ignored).
	createFakeLease(t, client, "wormsign-system", "wormsign-leader", now.Add(-2*time.Second))

	// Create a non-wormsign Lease (should be ignored).
	createFakeLease(t, client, "wormsign-system", "kube-scheduler", now.Add(-1*time.Second))

	// Create an expired wormsign Lease (should be ignored).
	createFakeLease(t, client, "wormsign-system", "wormsign-3", now.Add(-5*time.Minute))

	mgr, _ := NewManager(client, "wormsign-system", "0", 1, WithLogger(silentLogger()))

	rw, err := NewReplicaWatcher(client, "wormsign-system", mgr,
		WithReplicaWatcherLogger(silentLogger()),
		WithReplicaWatcherNowFunc(func() time.Time { return now }),
		WithWatchInterval(time.Hour), // prevent periodic runs during test
	)
	if err != nil {
		t.Fatalf("NewReplicaWatcher: %v", err)
	}

	// Run briefly and check.
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- rw.Run(ctx) }()

	time.Sleep(200 * time.Millisecond)
	cancel()
	<-errCh

	// Should have detected 3 active replicas (wormsign-0, wormsign-1, wormsign-2).
	mgr.mu.RLock()
	numReplicas := mgr.numReplicas
	mgr.mu.RUnlock()

	if numReplicas != 3 {
		t.Errorf("numReplicas = %d, want 3", numReplicas)
	}
}

func TestReplicaWatcher_MinimumOneReplica(t *testing.T) {
	// Even with no Leases, the count should be at least 1.
	client := fake.NewSimpleClientset()

	mgr, _ := NewManager(client, "wormsign-system", "0", 1, WithLogger(silentLogger()))

	rw, err := NewReplicaWatcher(client, "wormsign-system", mgr,
		WithReplicaWatcherLogger(silentLogger()),
		WithWatchInterval(time.Hour),
	)
	if err != nil {
		t.Fatalf("NewReplicaWatcher: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- rw.Run(ctx) }()

	time.Sleep(200 * time.Millisecond)
	cancel()
	<-errCh

	mgr.mu.RLock()
	numReplicas := mgr.numReplicas
	mgr.mu.RUnlock()

	if numReplicas != 1 {
		t.Errorf("numReplicas = %d, want 1 (minimum)", numReplicas)
	}
}

func TestReplicaWatcher_DetectsScaleDown(t *testing.T) {
	now := time.Date(2026, 2, 8, 12, 0, 0, 0, time.UTC)
	client := fake.NewSimpleClientset()

	// Start with 3 active Leases.
	createFakeLease(t, client, "wormsign-system", "wormsign-0", now.Add(-5*time.Second))
	createFakeLease(t, client, "wormsign-system", "wormsign-1", now.Add(-5*time.Second))
	createFakeLease(t, client, "wormsign-system", "wormsign-2", now.Add(-5*time.Second))

	mgr, _ := NewManager(client, "wormsign-system", "0", 3, WithLogger(silentLogger()))

	rw, err := NewReplicaWatcher(client, "wormsign-system", mgr,
		WithReplicaWatcherLogger(silentLogger()),
		WithReplicaWatcherNowFunc(func() time.Time { return now }),
		WithWatchInterval(time.Hour),
	)
	if err != nil {
		t.Fatalf("NewReplicaWatcher: %v", err)
	}

	// Run initial check which should see 3 replicas.
	err = rw.checkReplicas(context.Background())
	if err != nil {
		t.Fatalf("checkReplicas: %v", err)
	}

	mgr.mu.RLock()
	if mgr.numReplicas != 3 {
		t.Errorf("numReplicas = %d, want 3", mgr.numReplicas)
	}
	mgr.mu.RUnlock()

	// Simulate one replica going away by advancing time past its renewal.
	// Delete wormsign-2 and re-create with old renewal time.
	if err := client.CoordinationV1().Leases("wormsign-system").Delete(
		context.Background(), "wormsign-2", metav1.DeleteOptions{},
	); err != nil {
		t.Fatalf("deleting lease: %v", err)
	}
	createFakeLease(t, client, "wormsign-system", "wormsign-2", now.Add(-5*time.Minute))

	// Now check again — should see only 2 active replicas.
	err = rw.checkReplicas(context.Background())
	if err != nil {
		t.Fatalf("checkReplicas: %v", err)
	}

	mgr.mu.RLock()
	if mgr.numReplicas != 2 {
		t.Errorf("numReplicas = %d, want 2 (after scale down)", mgr.numReplicas)
	}
	mgr.mu.RUnlock()
}

func TestReplicaWatcher_IgnoresLeaderLease(t *testing.T) {
	now := time.Date(2026, 2, 8, 12, 0, 0, 0, time.UTC)
	client := fake.NewSimpleClientset()

	// Only a leader Lease and one replica Lease.
	createFakeLease(t, client, "wormsign-system", "wormsign-leader", now.Add(-2*time.Second))
	createFakeLease(t, client, "wormsign-system", "wormsign-0", now.Add(-5*time.Second))

	mgr, _ := NewManager(client, "wormsign-system", "0", 1, WithLogger(silentLogger()))

	rw, err := NewReplicaWatcher(client, "wormsign-system", mgr,
		WithReplicaWatcherLogger(silentLogger()),
		WithReplicaWatcherNowFunc(func() time.Time { return now }),
	)
	if err != nil {
		t.Fatalf("NewReplicaWatcher: %v", err)
	}

	err = rw.checkReplicas(context.Background())
	if err != nil {
		t.Fatalf("checkReplicas: %v", err)
	}

	mgr.mu.RLock()
	if mgr.numReplicas != 1 {
		t.Errorf("numReplicas = %d, want 1 (leader Lease should be ignored)", mgr.numReplicas)
	}
	mgr.mu.RUnlock()
}

func TestReplicaWatcher_SkipsLeasesWithNoRenewTime(t *testing.T) {
	client := fake.NewSimpleClientset()

	// Create a Lease with no RenewTime.
	_, err := client.CoordinationV1().Leases("wormsign-system").Create(
		context.Background(),
		&coordinationv1.Lease{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "wormsign-0",
				Namespace: "wormsign-system",
			},
			Spec: coordinationv1.LeaseSpec{
				// No RenewTime set.
			},
		},
		metav1.CreateOptions{},
	)
	if err != nil {
		t.Fatalf("creating lease: %v", err)
	}

	mgr, _ := NewManager(client, "wormsign-system", "0", 1, WithLogger(silentLogger()))

	rw, err := NewReplicaWatcher(client, "wormsign-system", mgr,
		WithReplicaWatcherLogger(silentLogger()),
	)
	if err != nil {
		t.Fatalf("NewReplicaWatcher: %v", err)
	}

	err = rw.checkReplicas(context.Background())
	if err != nil {
		t.Fatalf("checkReplicas: %v", err)
	}

	// Should still be 1 (minimum) since no active Leases were found.
	mgr.mu.RLock()
	if mgr.numReplicas != 1 {
		t.Errorf("numReplicas = %d, want 1", mgr.numReplicas)
	}
	mgr.mu.RUnlock()
}

func TestCountActiveReplicas_Direct(t *testing.T) {
	now := time.Date(2026, 2, 8, 12, 0, 0, 0, time.UTC)
	client := fake.NewSimpleClientset()
	mgr, _ := NewManager(client, "wormsign-system", "0", 1, WithLogger(silentLogger()))

	rw := &ReplicaWatcher{
		logger:  silentLogger(),
		nowFunc: func() time.Time { return now },
		manager: mgr,
	}

	recentRenew := metav1.NewMicroTime(now.Add(-10 * time.Second))
	expiredRenew := metav1.NewMicroTime(now.Add(-5 * time.Minute))

	tests := []struct {
		name     string
		leases   []coordinationv1.Lease
		expected int
	}{
		{
			name:     "no leases",
			leases:   nil,
			expected: 0,
		},
		{
			name: "one active",
			leases: []coordinationv1.Lease{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "wormsign-0"},
					Spec:       coordinationv1.LeaseSpec{RenewTime: &recentRenew},
				},
			},
			expected: 1,
		},
		{
			name: "one expired",
			leases: []coordinationv1.Lease{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "wormsign-0"},
					Spec:       coordinationv1.LeaseSpec{RenewTime: &expiredRenew},
				},
			},
			expected: 0,
		},
		{
			name: "mixed active and expired",
			leases: []coordinationv1.Lease{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "wormsign-0"},
					Spec:       coordinationv1.LeaseSpec{RenewTime: &recentRenew},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "wormsign-1"},
					Spec:       coordinationv1.LeaseSpec{RenewTime: &expiredRenew},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "wormsign-2"},
					Spec:       coordinationv1.LeaseSpec{RenewTime: &recentRenew},
				},
			},
			expected: 2,
		},
		{
			name: "ignores leader lease",
			leases: []coordinationv1.Lease{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "wormsign-leader"},
					Spec:       coordinationv1.LeaseSpec{RenewTime: &recentRenew},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "wormsign-0"},
					Spec:       coordinationv1.LeaseSpec{RenewTime: &recentRenew},
				},
			},
			expected: 1,
		},
		{
			name: "ignores non-wormsign leases",
			leases: []coordinationv1.Lease{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "kube-scheduler"},
					Spec:       coordinationv1.LeaseSpec{RenewTime: &recentRenew},
				},
				{
					ObjectMeta: metav1.ObjectMeta{Name: "wormsign-0"},
					Spec:       coordinationv1.LeaseSpec{RenewTime: &recentRenew},
				},
			},
			expected: 1,
		},
		{
			name: "no renew time",
			leases: []coordinationv1.Lease{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "wormsign-0"},
					Spec:       coordinationv1.LeaseSpec{},
				},
			},
			expected: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := rw.countActiveReplicas(tc.leases)
			if got != tc.expected {
				t.Errorf("countActiveReplicas() = %d, want %d", got, tc.expected)
			}
		})
	}
}
