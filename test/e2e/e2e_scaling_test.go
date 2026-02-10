//go:build e2e

package e2e

import (
	"testing"
	"time"
)

// TestE2E_ScaleUp starts with 1 replica, scales to 3, and verifies that
// namespaces are redistributed across all replicas.
func TestE2E_ScaleUp(t *testing.T) {
	ctrlNs := createTestNamespace(t, "scaleup")
	helmInstall(t, ctrlNs, 1)
	t.Cleanup(func() { helmUninstall(t, ctrlNs) })

	waitForRollout(t, ctrlNs, deploymentName, 120*time.Second)

	// Wait for 1-replica shard map.
	var smBefore *shardMapData
	pollUntil(t, 45*time.Second, 3*time.Second, "1-replica shard map", func() (bool, error) {
		smBefore = getShardMap(t, ctrlNs)
		return smBefore != nil && len(smBefore.Assignments) == 1, nil
	})

	for _, nss := range smBefore.Assignments {
		t.Logf("before scale-up: 1 replica with %d namespaces", len(nss))
	}

	// Scale to 3 replicas.
	helmScale(t, ctrlNs, 3)
	waitForPodsReady(t, ctrlNs,
		"app.kubernetes.io/name=k8s-wormsign,app.kubernetes.io/component=controller",
		3, 180*time.Second)

	// Wait for shard map to redistribute to 3 entries.
	var smAfter *shardMapData
	pollUntil(t, 60*time.Second, 3*time.Second, "3-replica shard map after scale-up", func() (bool, error) {
		smAfter = getShardMap(t, ctrlNs)
		return smAfter != nil && len(smAfter.Assignments) == 3, nil
	})

	// Verify no namespace is assigned to multiple replicas.
	seen := make(map[string]string)
	for pod, nss := range smAfter.Assignments {
		t.Logf("after scale-up: pod %s has %d namespaces", pod, len(nss))
		for _, ns := range nss {
			if other, dup := seen[ns]; dup {
				t.Errorf("namespace %q assigned to both %s and %s", ns, pod, other)
			}
			seen[ns] = pod
		}
	}
}

// TestE2E_ScaleDown starts with 3 replicas, scales to 1, and verifies the
// remaining replica picks up all namespaces.
func TestE2E_ScaleDown(t *testing.T) {
	ctrlNs := createTestNamespace(t, "scaledn")
	helmInstall(t, ctrlNs, 3)
	t.Cleanup(func() { helmUninstall(t, ctrlNs) })

	waitForPodsReady(t, ctrlNs,
		"app.kubernetes.io/name=k8s-wormsign,app.kubernetes.io/component=controller",
		3, 180*time.Second)

	// Wait for 3-replica shard map.
	var smBefore *shardMapData
	pollUntil(t, 60*time.Second, 3*time.Second, "3-replica shard map", func() (bool, error) {
		smBefore = getShardMap(t, ctrlNs)
		return smBefore != nil && len(smBefore.Assignments) == 3, nil
	})

	for pod, nss := range smBefore.Assignments {
		t.Logf("before scale-down: pod %s has %d namespaces", pod, len(nss))
	}

	// Scale down to 1 replica.
	helmScale(t, ctrlNs, 1)
	waitForRollout(t, ctrlNs, deploymentName, 180*time.Second)

	// Wait for shard map to consolidate to 1 entry.
	// We only check that one replica owns all namespaces — the total count
	// may differ from before because namespaces from other tests are cleaned
	// up concurrently.
	pollUntil(t, 60*time.Second, 3*time.Second, "1-replica shard map after scale-down", func() (bool, error) {
		sm := getShardMap(t, ctrlNs)
		return sm != nil && len(sm.Assignments) == 1, nil
	})

	smAfter := getShardMap(t, ctrlNs)
	for pod, nss := range smAfter.Assignments {
		t.Logf("after scale-down: pod %s has %d namespaces", pod, len(nss))
	}
}
