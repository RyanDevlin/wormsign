package shard

import (
	"fmt"
	"math"
	"testing"
)

func TestJumpHash_Deterministic(t *testing.T) {
	// Same inputs must always produce the same output.
	for i := 0; i < 100; i++ {
		key := uint64(i * 12345)
		for buckets := 1; buckets <= 10; buckets++ {
			a := JumpHash(key, buckets)
			b := JumpHash(key, buckets)
			if a != b {
				t.Errorf("JumpHash(%d, %d): got %d and %d on two calls", key, buckets, a, b)
			}
		}
	}
}

func TestJumpHash_InRange(t *testing.T) {
	// Output must always be in [0, numBuckets).
	for key := uint64(0); key < 1000; key++ {
		for buckets := 1; buckets <= 20; buckets++ {
			got := JumpHash(key, buckets)
			if got < 0 || got >= buckets {
				t.Errorf("JumpHash(%d, %d) = %d, want [0, %d)", key, buckets, got, buckets)
			}
		}
	}
}

func TestJumpHash_SingleBucket(t *testing.T) {
	// With 1 bucket, everything maps to 0.
	for key := uint64(0); key < 100; key++ {
		got := JumpHash(key, 1)
		if got != 0 {
			t.Errorf("JumpHash(%d, 1) = %d, want 0", key, got)
		}
	}
}

func TestJumpHash_PanicsOnZeroBuckets(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("JumpHash(0, 0) did not panic")
		}
	}()
	JumpHash(0, 0)
}

func TestJumpHash_PanicsOnNegativeBuckets(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("JumpHash(0, -1) did not panic")
		}
	}()
	JumpHash(0, -1)
}

func TestJumpHash_MinimalDisruption(t *testing.T) {
	// When adding a bucket (n → n+1), at most 1/(n+1) fraction of keys
	// should move. We test with 10000 keys going from 5 to 6 buckets.
	const numKeys = 10000
	const oldBuckets = 5
	const newBuckets = 6

	moved := 0
	for key := uint64(0); key < numKeys; key++ {
		oldBucket := JumpHash(key, oldBuckets)
		newBucket := JumpHash(key, newBuckets)
		if oldBucket != newBucket {
			moved++
		}
	}

	// Expected: roughly numKeys/newBuckets keys move (1/6 ≈ 16.67%).
	// Allow generous margin: up to 25%.
	expectedMax := numKeys / newBuckets * 3 / 2 // 1.5x expected
	if moved > expectedMax {
		t.Errorf("too many keys moved when adding bucket: %d/%d (max expected %d)",
			moved, numKeys, expectedMax)
	}

	// But some keys must move — at least a few percent.
	expectedMin := numKeys / newBuckets / 2 // 0.5x expected
	if moved < expectedMin {
		t.Errorf("too few keys moved when adding bucket: %d/%d (min expected %d)",
			moved, numKeys, expectedMin)
	}

	t.Logf("keys moved from %d to %d buckets: %d/%d (%.1f%%)",
		oldBuckets, newBuckets, moved, numKeys, float64(moved)/float64(numKeys)*100)
}

func TestJumpHash_UniformDistribution(t *testing.T) {
	// Keys should be approximately uniformly distributed across buckets.
	const numKeys = 100000
	const numBuckets = 10

	counts := make([]int, numBuckets)
	for key := uint64(0); key < numKeys; key++ {
		bucket := JumpHash(key, numBuckets)
		counts[bucket]++
	}

	expected := float64(numKeys) / float64(numBuckets)
	maxDeviation := 0.0
	for i, c := range counts {
		deviation := math.Abs(float64(c)-expected) / expected
		if deviation > maxDeviation {
			maxDeviation = deviation
		}
		t.Logf("bucket %d: %d (%.1f%% deviation)", i, c, deviation*100)
	}

	// Allow up to 5% deviation from perfect uniform.
	if maxDeviation > 0.05 {
		t.Errorf("distribution too uneven: max deviation %.2f%% (threshold 5%%)", maxDeviation*100)
	}
}

func TestHashNamespace_Deterministic(t *testing.T) {
	tests := []string{
		"default",
		"kube-system",
		"payments",
		"very-long-namespace-name-that-is-still-valid",
		"",
	}
	for _, ns := range tests {
		a := HashNamespace(ns)
		b := HashNamespace(ns)
		if a != b {
			t.Errorf("HashNamespace(%q): got %d and %d", ns, a, b)
		}
	}
}

func TestHashNamespace_Distinct(t *testing.T) {
	// Different namespace names should (usually) produce different hashes.
	hashes := make(map[uint64]string)
	for i := 0; i < 1000; i++ {
		ns := fmt.Sprintf("ns-%d", i)
		h := HashNamespace(ns)
		if prev, exists := hashes[h]; exists {
			t.Errorf("hash collision: %q and %q both hash to %d", ns, prev, h)
		}
		hashes[h] = ns
	}
}

func TestAssignNamespaces_SingleReplica(t *testing.T) {
	namespaces := []string{"default", "payments", "orders", "shipping"}
	result := AssignNamespaces(namespaces, 1)

	if len(result) != 1 {
		t.Fatalf("expected 1 replica entry, got %d", len(result))
	}
	if len(result[0]) != len(namespaces) {
		t.Errorf("single replica should get all %d namespaces, got %d",
			len(namespaces), len(result[0]))
	}

	// Verify all namespaces are present.
	assigned := make(map[string]bool)
	for _, ns := range result[0] {
		assigned[ns] = true
	}
	for _, ns := range namespaces {
		if !assigned[ns] {
			t.Errorf("namespace %q not assigned to replica 0", ns)
		}
	}
}

func TestAssignNamespaces_MultipleReplicas(t *testing.T) {
	namespaces := []string{
		"default", "payments", "orders", "shipping",
		"analytics", "monitoring", "logging", "auth",
		"frontend", "backend",
	}

	result := AssignNamespaces(namespaces, 3)

	// Should have entries for all 3 replicas.
	if len(result) != 3 {
		t.Fatalf("expected 3 replica entries, got %d", len(result))
	}

	// All namespaces must be assigned to exactly one replica.
	assigned := make(map[string]int)
	for replica, nsList := range result {
		for _, ns := range nsList {
			if prev, ok := assigned[ns]; ok {
				t.Errorf("namespace %q assigned to both replica %d and %d", ns, prev, replica)
			}
			assigned[ns] = replica
		}
	}
	for _, ns := range namespaces {
		if _, ok := assigned[ns]; !ok {
			t.Errorf("namespace %q not assigned to any replica", ns)
		}
	}

	// Each replica should get at least some namespaces (with 10 ns and 3 replicas).
	for i := 0; i < 3; i++ {
		if len(result[i]) == 0 {
			t.Errorf("replica %d got 0 namespaces", i)
		}
		t.Logf("replica %d: %d namespaces %v", i, len(result[i]), result[i])
	}
}

func TestAssignNamespaces_EmptyList(t *testing.T) {
	result := AssignNamespaces(nil, 3)

	if len(result) != 3 {
		t.Fatalf("expected 3 replica entries, got %d", len(result))
	}
	for i := 0; i < 3; i++ {
		if len(result[i]) != 0 {
			t.Errorf("replica %d should have 0 namespaces, got %d", i, len(result[i]))
		}
	}
}

func TestAssignNamespaces_Stability(t *testing.T) {
	// Same inputs should always produce the same output.
	namespaces := []string{"ns-a", "ns-b", "ns-c", "ns-d", "ns-e"}
	first := AssignNamespaces(namespaces, 3)
	for i := 0; i < 10; i++ {
		again := AssignNamespaces(namespaces, 3)
		for replica := 0; replica < 3; replica++ {
			if len(first[replica]) != len(again[replica]) {
				t.Fatalf("iteration %d: replica %d assignment length changed", i, replica)
			}
			for j := range first[replica] {
				if first[replica][j] != again[replica][j] {
					t.Errorf("iteration %d: replica %d ns[%d] changed from %q to %q",
						i, replica, j, first[replica][j], again[replica][j])
				}
			}
		}
	}
}

func TestAssignNamespaces_ScaleUpMinimalDisruption(t *testing.T) {
	// Generate a realistic set of namespaces.
	var namespaces []string
	for i := 0; i < 50; i++ {
		namespaces = append(namespaces, fmt.Sprintf("namespace-%d", i))
	}

	before := AssignNamespaces(namespaces, 3)
	after := AssignNamespaces(namespaces, 4)

	// Build "namespace → replica" maps.
	mapBefore := make(map[string]int)
	for replica, nsList := range before {
		for _, ns := range nsList {
			mapBefore[ns] = replica
		}
	}
	mapAfter := make(map[string]int)
	for replica, nsList := range after {
		for _, ns := range nsList {
			mapAfter[ns] = replica
		}
	}

	moved := 0
	for _, ns := range namespaces {
		if mapBefore[ns] != mapAfter[ns] {
			moved++
		}
	}

	// With jump hash, scaling from 3 to 4 should move roughly 1/4 of keys.
	// Allow up to 40% to move (generous margin).
	maxMoved := len(namespaces) * 2 / 5
	if moved > maxMoved {
		t.Errorf("too many namespaces moved during scale-up: %d/%d (max %d)",
			moved, len(namespaces), maxMoved)
	}

	t.Logf("namespaces moved 3→4 replicas: %d/%d (%.1f%%)",
		moved, len(namespaces), float64(moved)/float64(len(namespaces))*100)
}

func BenchmarkJumpHash(b *testing.B) {
	for i := 0; i < b.N; i++ {
		JumpHash(uint64(i), 10)
	}
}

func BenchmarkAssignNamespaces(b *testing.B) {
	var namespaces []string
	for i := 0; i < 200; i++ {
		namespaces = append(namespaces, fmt.Sprintf("ns-%d", i))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		AssignNamespaces(namespaces, 5)
	}
}
