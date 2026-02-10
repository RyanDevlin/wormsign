//go:build e2e

package e2e

import (
	"testing"
	"time"
)

// TestE2E_SingleReplica deploys Wormsign with a single replica and validates
// core functionality: health probes, metrics, and fault detection for multiple
// detector types.
func TestE2E_SingleReplica(t *testing.T) {
	ctrlNs := createTestNamespace(t, "single")
	helmInstall(t, ctrlNs, 1)
	t.Cleanup(func() { helmUninstall(t, ctrlNs) })

	waitForRollout(t, ctrlNs, deploymentName, 120*time.Second)

	t.Run("HealthProbes", func(t *testing.T) {
		pods := getControllerPods(t, ctrlNs)
		if len(pods) == 0 {
			t.Fatal("no controller pods found")
		}
		podName := pods[0]

		// Liveness probe.
		status := checkHealthEndpoint(t, ctrlNs, podName, 8081, "/healthz")
		if status != 200 {
			t.Errorf("/healthz returned %d, want 200", status)
		}

		// Readiness probe.
		status = checkHealthEndpoint(t, ctrlNs, podName, 8081, "/readyz")
		if status != 200 {
			t.Errorf("/readyz returned %d, want 200", status)
		}
	})

	t.Run("MetricsEndpoint", func(t *testing.T) {
		pods := getControllerPods(t, ctrlNs)
		if len(pods) == 0 {
			t.Fatal("no controller pods found")
		}
		metricsText := scrapeMetrics(t, ctrlNs, pods[0])

		// Leader gauge should be 1 for single replica.
		val, ok := findMetricValue(metricsText, "wormsign_leader_is_leader", nil)
		if !ok {
			t.Error("wormsign_leader_is_leader metric not found")
		} else if val != 1 {
			t.Errorf("wormsign_leader_is_leader = %v, want 1", val)
		}

		// Shard namespaces should be > 0.
		val, ok = findMetricValue(metricsText, "wormsign_shard_namespaces", nil)
		if !ok {
			t.Error("wormsign_shard_namespaces metric not found")
		} else if val < 1 {
			t.Errorf("wormsign_shard_namespaces = %v, want >= 1", val)
		}
	})

	t.Run("CrashLoopDetection", func(t *testing.T) {
		t.Parallel()
		faultNs := createTestNamespace(t, "cl")
		podName := injectCrashLoopPod(t, faultNs)

		// CrashLoop needs ~3 restarts (70s) + scan interval. Be generous.
		waitForWormsignEvent(t, faultNs, podName, 5*time.Minute)
		t.Logf("WormsignRCA event detected for crashloop pod %s/%s", faultNs, podName)
	})

	t.Run("StuckPendingDetection", func(t *testing.T) {
		t.Parallel()
		faultNs := createTestNamespace(t, "sp")
		podName := injectStuckPendingPod(t, faultNs)

		// StuckPending threshold=1m + scan interval 30s + buffer.
		waitForWormsignEvent(t, faultNs, podName, 3*time.Minute)
		t.Logf("WormsignRCA event detected for stuck-pending pod %s/%s", faultNs, podName)
	})

	t.Run("BadImageDetection", func(t *testing.T) {
		t.Parallel()
		faultNs := createTestNamespace(t, "bi")
		podName := injectBadImagePod(t, faultNs)

		// Bad image → PodFailed should fire relatively quickly.
		waitForWormsignEvent(t, faultNs, podName, 3*time.Minute)
		t.Logf("WormsignRCA event detected for bad-image pod %s/%s", faultNs, podName)
	})
}
