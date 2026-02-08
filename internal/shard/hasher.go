// Package shard implements namespace shard assignment and coordination for
// horizontal scaling. It uses consistent hashing (jump hash) to distribute
// namespaces across controller replicas, with coordination via a ConfigMap.
//
// See PROJECT_SPEC.md Section 3.1 for the full design.
package shard

import "hash/fnv"

// JumpHash implements Google's "A Fast, Minimal Memory, Consistent Hash Algorithm"
// (Lamping & Veach, 2014). Given a key and numBuckets, it returns a bucket in
// [0, numBuckets). The algorithm guarantees:
//   - Deterministic: same (key, numBuckets) always returns the same bucket.
//   - Minimal disruption: when numBuckets changes, roughly 1/numBuckets fraction
//     of keys are reassigned — optimal for a consistent hash.
//   - Uniform: keys are distributed uniformly across buckets.
//
// numBuckets must be >= 1; passing 0 will panic.
func JumpHash(key uint64, numBuckets int) int {
	if numBuckets <= 0 {
		panic("shard: JumpHash called with numBuckets <= 0")
	}
	var b, j int64
	b = -1
	j = 0
	for j < int64(numBuckets) {
		b = j
		key = key*2862933555777941757 + 1
		j = int64(float64(b+1) * (float64(int64(1)<<31) / float64((key>>33)+1)))
	}
	return int(b)
}

// HashNamespace computes a stable hash of a namespace name for use as a
// JumpHash key. Uses FNV-1a for speed and good distribution.
func HashNamespace(namespace string) uint64 {
	h := fnv.New64a()
	// fnv.Write never returns an error.
	_, _ = h.Write([]byte(namespace))
	return h.Sum64()
}

// AssignNamespaces distributes a list of namespace names across numReplicas
// using JumpHash. It returns a map from replica index to the set of namespace
// names assigned to that replica.
//
// If numReplicas is 1, all namespaces are assigned to replica 0 (single-replica mode).
// The returned map always contains entries for all replica indices [0, numReplicas).
func AssignNamespaces(namespaces []string, numReplicas int) map[int][]string {
	result := make(map[int][]string, numReplicas)
	for i := 0; i < numReplicas; i++ {
		result[i] = []string{}
	}
	for _, ns := range namespaces {
		key := HashNamespace(ns)
		bucket := JumpHash(key, numReplicas)
		result[bucket] = append(result[bucket], ns)
	}
	return result
}
