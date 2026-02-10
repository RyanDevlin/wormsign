//go:build e2e

package e2e

import (
	"testing"
	"time"
)

// TestE2E_MultiReplica deploys 3 replicas and verifies shard distribution,
// all replicas reaching readiness, and fault detection across different shards.
func TestE2E_MultiReplica(t *testing.T) {
	ctrlNs := createTestNamespace(t, "multi")
	helmInstall(t, ctrlNs, 3)
	t.Cleanup(func() { helmUninstall(t, ctrlNs) })

	waitForPodsReady(t, ctrlNs,
		"app.kubernetes.io/name=k8s-wormsign,app.kubernetes.io/component=controller",
		3, 180*time.Second)

	t.Run("AllReplicasReady", func(t *testing.T) {
		pods := getControllerPods(t, ctrlNs)
		if len(pods) != 3 {
			t.Fatalf("expected 3 controller pods, got %d: %v", len(pods), pods)
		}

		// Verify each pod has a healthy readiness probe.
		for _, pod := range pods {
			status := checkHealthEndpoint(t, ctrlNs, pod, 8081, "/readyz")
			if status != 200 {
				t.Errorf("pod %s /readyz returned %d, want 200", pod, status)
			}
		}
	})

	t.Run("ShardDistribution", func(t *testing.T) {
		// Wait for shard map to have 3 entries.
		var sm *shardMapData
		pollUntil(t, 120*time.Second, 5*time.Second, "shard map with 3 entries", func() (bool, error) {
			sm = getShardMap(t, ctrlNs)
			return sm != nil && len(sm.Assignments) == 3, nil
		})

		// Log namespace distribution. With few namespaces (e.g. 3) and 3
		// replicas, consistent hashing may leave some pods with 0 — that's
		// expected and not a failure.
		for pod, namespaces := range sm.Assignments {
			t.Logf("pod %s: %d namespaces %v", pod, len(namespaces), namespaces)
		}

		// Verify no namespace is assigned to multiple replicas.
		seen := make(map[string]string)
		for pod, namespaces := range sm.Assignments {
			for _, ns := range namespaces {
				if other, dup := seen[ns]; dup {
					t.Errorf("namespace %q assigned to both %s and %s", ns, pod, other)
				}
				seen[ns] = pod
			}
		}
	})

	t.Run("SharedResponsibilities", func(t *testing.T) {
		// Create 3 test namespaces and inject faults in each.
		// The 3-replica setup should distribute these across different shards,
		// and all should eventually have WormsignRCA events.
		faultNs1 := createTestNamespace(t, "shard-a")
		faultNs2 := createTestNamespace(t, "shard-b")
		faultNs3 := createTestNamespace(t, "shard-c")

		// Wait for new namespaces to appear in the shard map (next reconcile cycle).
		pollUntil(t, 120*time.Second, 5*time.Second, "new namespaces in shard map", func() (bool, error) {
			sm := getShardMap(t, ctrlNs)
			if sm == nil {
				return false, nil
			}
			allNs := make(map[string]bool)
			for _, nss := range sm.Assignments {
				for _, ns := range nss {
					allNs[ns] = true
				}
			}
			return allNs[faultNs1] && allNs[faultNs2] && allNs[faultNs3], nil
		})

		// Inject crashloop pods in all 3 namespaces.
		pod1 := injectCrashLoopPod(t, faultNs1)
		pod2 := injectCrashLoopPod(t, faultNs2)
		pod3 := injectCrashLoopPod(t, faultNs3)

		// Verify WormsignRCA events appear in all 3 namespaces.
		// These may be handled by different replicas.
		for _, tc := range []struct {
			ns   string
			pod  string
		}{
			{faultNs1, pod1},
			{faultNs2, pod2},
			{faultNs3, pod3},
		} {
			tc := tc
			t.Run("ns="+tc.ns, func(t *testing.T) {
				t.Parallel()
				waitForWormsignEvent(t, tc.ns, tc.pod, 5*time.Minute)
				t.Logf("WormsignRCA event detected in %s for pod %s", tc.ns, tc.pod)
			})
		}
	})
}
