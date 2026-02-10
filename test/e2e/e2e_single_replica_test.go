//go:build e2e

package e2e

import (
	"fmt"
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

		// CrashLoop needs ~3 restarts (70s) + scan interval (5s in e2e).
		waitForWormsignEvent(t, faultNs, podName, 2*time.Minute)
		assertEventHasRulesAnalysis(t, faultNs, podName)
		t.Logf("WormsignRCA event detected for crashloop pod %s/%s", faultNs, podName)
	})

	t.Run("StuckPendingDetection", func(t *testing.T) {
		t.Parallel()
		faultNs := createTestNamespace(t, "sp")
		podName := injectStuckPendingPod(t, faultNs)

		// StuckPending threshold=30s + scan interval 5s + buffer.
		waitForWormsignEvent(t, faultNs, podName, 90*time.Second)
		assertEventHasRulesAnalysis(t, faultNs, podName)
		t.Logf("WormsignRCA event detected for stuck-pending pod %s/%s", faultNs, podName)
	})

	t.Run("BadImageDetection", func(t *testing.T) {
		t.Parallel()
		faultNs := createTestNamespace(t, "bi")
		podName := injectBadImagePod(t, faultNs)

		// Bad image → PodFailed should fire relatively quickly.
		waitForWormsignEvent(t, faultNs, podName, 90*time.Second)
		assertEventHasRulesAnalysis(t, faultNs, podName)
		t.Logf("WormsignRCA event detected for bad-image pod %s/%s", faultNs, podName)
	})

	t.Run("JobDeadlineExceeded", func(t *testing.T) {
		t.Parallel()
		faultNs := createTestNamespace(t, "jd")
		jobName := injectDeadlineJob(t, faultNs)

		// Deadline fires at 5s, Job controller marks DeadlineExceeded, detector picks it up.
		waitForWormsignEvent(t, faultNs, jobName, 2*time.Minute)
		assertEventHasRulesAnalysis(t, faultNs, jobName)
		t.Logf("WormsignRCA event detected for deadline-exceeded job %s/%s", faultNs, jobName)
	})

	t.Run("MetricsAfterFault", func(t *testing.T) {
		t.Parallel()
		faultNs := createTestNamespace(t, "mf")
		podName := injectCrashLoopPod(t, faultNs)

		// Wait for the event to ensure the pipeline has processed at least one fault.
		waitForWormsignEvent(t, faultNs, podName, 2*time.Minute)

		// Now scrape metrics and validate counters.
		pods := getControllerPods(t, ctrlNs)
		if len(pods) == 0 {
			t.Fatal("no controller pods found")
		}
		metricsText := scrapeMetrics(t, ctrlNs, pods[0])

		// fault_events_total should be > 0 after processing a fault.
		val, ok := findMetricValue(metricsText, "wormsign_fault_events_total", nil)
		if !ok {
			t.Error("wormsign_fault_events_total metric not found")
		} else if val < 1 {
			t.Errorf("wormsign_fault_events_total = %v, want >= 1", val)
		}

		// sink_deliveries_total with status=success should be > 0.
		val, ok = findMetricValue(metricsText, "wormsign_sink_deliveries_total", map[string]string{"status": "success"})
		if !ok {
			t.Error("wormsign_sink_deliveries_total{status=success} metric not found")
		} else if val < 1 {
			t.Errorf("wormsign_sink_deliveries_total{status=success} = %v, want >= 1", val)
		}

		// Leader gauge should still be 1.
		val, ok = findMetricValue(metricsText, "wormsign_leader_is_leader", nil)
		if !ok {
			t.Error("wormsign_leader_is_leader metric not found")
		} else if val != 1 {
			t.Errorf("wormsign_leader_is_leader = %v, want 1", val)
		}

		// Shard namespaces should be >= 1.
		val, ok = findMetricValue(metricsText, "wormsign_shard_namespaces", nil)
		if !ok {
			t.Error("wormsign_shard_namespaces metric not found")
		} else if val < 1 {
			t.Errorf("wormsign_shard_namespaces = %v, want >= 1", val)
		}
	})

	t.Run("DetectorAnnotationExclusion", func(t *testing.T) {
		t.Parallel()
		faultNs := createTestNamespace(t, "ae")

		// Create a crash-looping pod with the exclude annotation.
		podName := "fault-excl-" + randomSuffix(6)
		yaml := fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: %s
  namespace: %s
  annotations:
    wormsign.io/exclude-detectors: "PodCrashLoop,PodFailed"
  labels:
    app: wormsign-fault-test
    fault-type: annotation-exclusion
spec:
  restartPolicy: Always
  containers:
    - name: crash
      image: busybox:1.36
      command: ["sh", "-c", "exit 1"]
      resources:
        requests:
          cpu: 10m
          memory: 8Mi
`, podName, faultNs)
		if err := kubectlApplyStdin(yaml); err != nil {
			t.Fatalf("inject annotated pod: %v", err)
		}
		t.Cleanup(func() {
			kubectl("delete", "pod", podName, "-n", faultNs, "--ignore-not-found")
		})

		// The pod will crash-loop but the filter should exclude it.
		// Wait long enough for 3+ restarts + scan, then confirm no event.
		assertNoWormsignEvent(t, faultNs, podName, 45*time.Second)
	})
}
