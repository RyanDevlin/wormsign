//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"os/exec"
)

// ---------------------------------------------------------------------------
// Command execution
// ---------------------------------------------------------------------------

// kubectl runs a kubectl command and returns stdout. On error, returns combined output.
func kubectl(args ...string) (string, error) {
	return runCmdCombined(kubectlBin, args...)
}

// kubectlApplyStdin applies YAML from stdin.
func kubectlApplyStdin(yaml string, extraArgs ...string) error {
	args := append([]string{"apply", "-f", "-"}, extraArgs...)
	cmd := exec.Command(kubectlBin, args...)
	cmd.Stdin = strings.NewReader(yaml)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("kubectl apply: %s\n%s", err, out)
	}
	return nil
}

// helmCmd runs a helm command and returns stdout.
func helmCmd(args ...string) (string, error) {
	return runCmdCombined(helmBin, args...)
}

// ---------------------------------------------------------------------------
// Helm management
// ---------------------------------------------------------------------------

const (
	releaseName    = "ws-e2e"
	deploymentName = releaseName + "-k8s-wormsign-controller"
	chartPath      = "deploy/helm/wormsign"
)

// helmInstall deploys Wormsign with the given replica count.
func helmInstall(t *testing.T, ns string, replicas int) {
	t.Helper()
	chart := filepath.Join(projectRoot, chartPath)
	args := []string{
		"upgrade", "--install", releaseName, chart,
		"--namespace", ns, "--create-namespace",
		"--set", "image.repository=" + imageName,
		"--set", "image.tag=" + imageTag,
		"--set", "image.pullPolicy=Never",
		"--set", fmt.Sprintf("replicaCount=%d", replicas),
		"--set", "analyzer.backend=noop",
		"--set", "logging.level=debug",
		"--set", "correlation.enabled=false",
		"--set", "sinks.kubernetesEvent.enabled=true",
		"--set", "sinks.kubernetesEvent.severityFilter={critical,warning,info}",
		"--set", "detectors.jobDeadlineExceeded.enabled=true",
		"--set", "detectors.podStuckPending.threshold=1m",
		"--set", "detectors.podStuckPending.cooldown=2m",
		"--set", "detectors.podCrashLoop.cooldown=2m",
		"--set", "detectors.podFailed.cooldown=2m",
		"--set", "detectors.nodeNotReady.cooldown=2m",
		"--set", "metrics.enabled=true",
		"--wait", "--timeout", "180s",
	}
	if err := runCmdStreamed(helmBin, args...); err != nil {
		t.Fatalf("helm install failed: %v", err)
	}
	t.Logf("helm install completed in namespace %s with %d replicas", ns, replicas)
}

// helmUninstall removes the Wormsign release.
func helmUninstall(t *testing.T, ns string) {
	t.Helper()
	out, err := helmCmd("uninstall", releaseName, "--namespace", ns)
	if err != nil {
		t.Logf("helm uninstall warning: %s\n%s", err, out)
	}
	// Delete the namespace too.
	kubectl("delete", "namespace", ns, "--ignore-not-found", "--wait=false")
}

// helmScale changes the replica count via helm upgrade.
func helmScale(t *testing.T, ns string, replicas int) {
	t.Helper()
	chart := filepath.Join(projectRoot, chartPath)
	args := []string{
		"upgrade", releaseName, chart,
		"--namespace", ns, "--reuse-values",
		"--set", fmt.Sprintf("replicaCount=%d", replicas),
		"--wait", "--timeout", "180s",
	}
	if err := runCmdStreamed(helmBin, args...); err != nil {
		t.Fatalf("helm scale to %d failed: %v", replicas, err)
	}
	t.Logf("scaled to %d replicas in namespace %s", replicas, ns)
}

// ---------------------------------------------------------------------------
// Namespace management
// ---------------------------------------------------------------------------

// randomSuffix generates a random lowercase alphanumeric string of length n.
func randomSuffix(n int) string {
	const chars = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = chars[rand.IntN(len(chars))]
	}
	return string(b)
}

// createTestNamespace creates a unique namespace and registers cleanup.
func createTestNamespace(t *testing.T, prefix string) string {
	t.Helper()
	name := fmt.Sprintf("ws-e2e-%s-%s", prefix, randomSuffix(6))
	out, err := kubectl("create", "namespace", name)
	if err != nil {
		t.Fatalf("create namespace %s: %s\n%s", name, err, out)
	}
	t.Cleanup(func() {
		kubectl("delete", "namespace", name, "--ignore-not-found", "--wait=false")
	})
	t.Logf("created test namespace %s", name)
	return name
}

// ---------------------------------------------------------------------------
// Polling / waiting
// ---------------------------------------------------------------------------

// pollUntil polls fn at interval until it returns true or timeout expires.
// Logs progress every 30 seconds so long-running waits show signs of life.
func pollUntil(t *testing.T, timeout, interval time.Duration, desc string, fn func() (bool, error)) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	start := time.Now()
	lastLog := start
	attempts := 0
	var lastErr error
	for time.Now().Before(deadline) {
		attempts++
		ok, err := fn()
		if err != nil {
			lastErr = err
		}
		if ok {
			t.Logf("poll: %s succeeded after %v (%d attempts)", desc, time.Since(start).Round(time.Second), attempts)
			return
		}
		if time.Since(lastLog) >= 30*time.Second {
			elapsed := time.Since(start).Round(time.Second)
			remaining := time.Until(deadline).Round(time.Second)
			if lastErr != nil {
				t.Logf("poll: still waiting for %s (%v elapsed, %v remaining, last error: %v)", desc, elapsed, remaining, lastErr)
			} else {
				t.Logf("poll: still waiting for %s (%v elapsed, %v remaining)", desc, elapsed, remaining)
			}
			lastLog = time.Now()
		}
		time.Sleep(interval)
	}
	if lastErr != nil {
		t.Fatalf("timed out after %v waiting for %s: last error: %v", timeout, desc, lastErr)
	}
	t.Fatalf("timed out after %v waiting for %s", timeout, desc)
}

// waitForRollout waits for a deployment rollout to complete.
func waitForRollout(t *testing.T, ns, deploy string, timeout time.Duration) {
	t.Helper()
	out, err := kubectl("rollout", "status", "deployment/"+deploy,
		"-n", ns, fmt.Sprintf("--timeout=%ds", int(timeout.Seconds())))
	if err != nil {
		t.Fatalf("rollout status %s/%s: %s\n%s", ns, deploy, err, out)
	}
}

// waitForPodsReady waits until the expected number of pods with the given label are Ready.
func waitForPodsReady(t *testing.T, ns, labelSelector string, count int, timeout time.Duration) {
	t.Helper()
	pollUntil(t, timeout, 5*time.Second, fmt.Sprintf("%d ready pods with %s", count, labelSelector), func() (bool, error) {
		out, err := kubectl("get", "pods", "-n", ns, "-l", labelSelector, "-o", "json")
		if err != nil {
			return false, fmt.Errorf("kubectl get pods: %w", err)
		}
		var podList struct {
			Items []struct {
				Metadata struct {
					Name string `json:"name"`
				} `json:"metadata"`
				Status struct {
					Phase      string `json:"phase"`
					Conditions []struct {
						Type   string `json:"type"`
						Status string `json:"status"`
					} `json:"conditions"`
				} `json:"status"`
			} `json:"items"`
		}
		if err := json.Unmarshal([]byte(out), &podList); err != nil {
			return false, err
		}
		readyCount := 0
		for _, pod := range podList.Items {
			for _, cond := range pod.Status.Conditions {
				if cond.Type == "Ready" && cond.Status == "True" {
					readyCount++
				}
			}
		}
		return readyCount >= count, nil
	})
}

// ---------------------------------------------------------------------------
// Kubernetes Event assertions
// ---------------------------------------------------------------------------

type eventInfo struct {
	Reason  string `json:"reason"`
	Message string `json:"message"`
	Type    string `json:"type"`
	Regarding struct {
		Kind      string `json:"kind"`
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"regarding"`
}

// waitForWormsignEvent polls for WormsignRCA events involving the named resource.
// On timeout, dumps controller pod logs to help diagnose why detection didn't fire.
func waitForWormsignEvent(t *testing.T, ns, resourceName string, timeout time.Duration) {
	t.Helper()
	desc := fmt.Sprintf("WormsignRCA event for %s in %s", resourceName, ns)
	deadline := time.Now().Add(timeout)
	start := time.Now()
	lastLog := start
	attempts := 0
	var lastErr error

	for time.Now().Before(deadline) {
		attempts++
		events, err := getWormsignEvents(ns)
		if err != nil {
			lastErr = err
		} else {
			for _, ev := range events {
				if resourceName == "" || ev.InvolvedObject.Name == resourceName {
					t.Logf("poll: %s succeeded after %v (%d attempts)", desc, time.Since(start).Round(time.Second), attempts)
					return
				}
			}
			// Log progress with context about what we're seeing.
			if time.Since(lastLog) >= 30*time.Second {
				elapsed := time.Since(start).Round(time.Second)
				remaining := time.Until(deadline).Round(time.Second)

				// Check the fault pod's current status for context.
				podStatus := "unknown"
				if resourceName != "" {
					if out, err := kubectl("get", "pod", resourceName, "-n", ns,
						"-o", "jsonpath={.status.phase} restarts={.status.containerStatuses[0].restartCount}"); err == nil {
						podStatus = strings.TrimSpace(out)
					}
				}
				t.Logf("poll: waiting for %s (%v elapsed, %v remaining, %d events in ns, fault pod: %s)",
					desc, elapsed, remaining, len(events), podStatus)
				lastLog = time.Now()
			}
		}
		time.Sleep(10 * time.Second)
	}

	// On timeout, dump controller logs to help diagnose.
	t.Logf("TIMEOUT: %s — dumping controller logs for diagnosis", desc)
	dumpControllerLogs(t, ns)

	if lastErr != nil {
		t.Fatalf("timed out after %v waiting for %s: last error: %v", timeout, desc, lastErr)
	}
	t.Fatalf("timed out after %v waiting for %s", timeout, desc)
}

// dumpControllerLogs prints the last 40 lines from each controller pod in the
// given namespace. Helps diagnose why a detector didn't fire.
func dumpControllerLogs(t *testing.T, ns string) {
	t.Helper()
	// Find the controller namespace — it may differ from the fault namespace.
	// Look for namespaces containing the controller deployment.
	controllerNs := ns
	if out, err := kubectl("get", "deployments", "-A",
		"-l", "app.kubernetes.io/name=k8s-wormsign",
		"-o", "jsonpath={.items[0].metadata.namespace}"); err == nil && strings.TrimSpace(out) != "" {
		controllerNs = strings.TrimSpace(out)
	}

	pods := getControllerPods(t, controllerNs)
	for _, pod := range pods {
		out, err := kubectl("logs", pod, "-n", controllerNs, "--tail=40")
		if err != nil {
			t.Logf("  [%s] failed to get logs: %v", pod, err)
			continue
		}
		t.Logf("  === Controller logs: %s ===", pod)
		for _, line := range strings.Split(out, "\n") {
			if line != "" {
				t.Logf("    %s", line)
			}
		}
	}
}

type k8sEvent struct {
	InvolvedObject struct {
		Kind      string `json:"kind"`
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"involvedObject"`
	Reason  string `json:"reason"`
	Message string `json:"message"`
	Type    string `json:"type"`
}

// getWormsignEvents fetches all WormsignRCA events in a namespace.
func getWormsignEvents(ns string) ([]k8sEvent, error) {
	out, err := kubectl("get", "events", "-n", ns,
		"--field-selector", "reason=WormsignRCA", "-o", "json")
	if err != nil {
		return nil, fmt.Errorf("get events: %w", err)
	}
	var eventList struct {
		Items []k8sEvent `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &eventList); err != nil {
		return nil, err
	}
	return eventList.Items, nil
}

// ---------------------------------------------------------------------------
// Shard map
// ---------------------------------------------------------------------------

type shardMapData struct {
	Assignments map[string][]string `json:"assignments"`
	NumReplicas int                 `json:"numReplicas"`
}

// getShardMap reads and parses the shard-map ConfigMap.
func getShardMap(t *testing.T, ns string) *shardMapData {
	t.Helper()
	out, err := kubectl("get", "configmap", "wormsign-shard-map", "-n", ns,
		"-o", "jsonpath={.data.shard-map}")
	if err != nil {
		t.Logf("getShardMap: %s", err)
		return nil
	}
	if strings.TrimSpace(out) == "" {
		return nil
	}
	var sm shardMapData
	if err := json.Unmarshal([]byte(out), &sm); err != nil {
		t.Logf("getShardMap unmarshal: %v", err)
		return nil
	}
	return &sm
}

// ---------------------------------------------------------------------------
// Controller pod helpers
// ---------------------------------------------------------------------------

// getControllerPods returns the names of controller pods in the namespace.
func getControllerPods(t *testing.T, ns string) []string {
	t.Helper()
	out, err := kubectl("get", "pods", "-n", ns,
		"-l", "app.kubernetes.io/name=k8s-wormsign,app.kubernetes.io/component=controller",
		"-o", "jsonpath={.items[*].metadata.name}")
	if err != nil {
		t.Fatalf("get controller pods: %s\n%s", err, out)
	}
	names := strings.Fields(strings.TrimSpace(out))
	return names
}

// ---------------------------------------------------------------------------
// Health & metrics via port-forward
// ---------------------------------------------------------------------------

// checkHealthEndpoint port-forwards to a pod and checks a health endpoint.
func checkHealthEndpoint(t *testing.T, ns, podName string, port int, path string) int {
	t.Helper()

	localPort := freePort(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, kubectlBin,
		"port-forward", "pod/"+podName, fmt.Sprintf("%d:%d", localPort, port), "-n", ns)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatalf("port-forward start: %v", err)
	}
	defer func() {
		cancel()
		_ = cmd.Wait()
	}()

	// Wait for port-forward to be ready.
	url := fmt.Sprintf("http://127.0.0.1:%d%s", localPort, path)
	var statusCode int
	for i := 0; i < 30; i++ {
		time.Sleep(500 * time.Millisecond)
		resp, err := http.Get(url)
		if err != nil {
			continue
		}
		statusCode = resp.StatusCode
		resp.Body.Close()
		return statusCode
	}
	t.Fatalf("port-forward to %s:%d%s timed out", podName, port, path)
	return 0
}

// scrapeMetrics port-forwards to the metrics port and returns the raw text.
func scrapeMetrics(t *testing.T, ns, podName string) string {
	t.Helper()

	localPort := freePort(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, kubectlBin,
		"port-forward", "pod/"+podName, fmt.Sprintf("%d:8080", localPort), "-n", ns)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		t.Fatalf("port-forward start: %v", err)
	}
	defer func() {
		cancel()
		_ = cmd.Wait()
	}()

	url := fmt.Sprintf("http://127.0.0.1:%d/metrics", localPort)
	for i := 0; i < 30; i++ {
		time.Sleep(500 * time.Millisecond)
		resp, err := http.Get(url)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return string(body)
	}
	t.Fatalf("scrape metrics from %s timed out", podName)
	return ""
}

// findMetricValue searches Prometheus text format for a metric with matching labels.
// Returns the value and true if found.
func findMetricValue(metricsText, metricName string, labels map[string]string) (float64, bool) {
	for _, line := range strings.Split(metricsText, "\n") {
		if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
			continue
		}
		// Match metric name.
		if !strings.HasPrefix(line, metricName) {
			continue
		}

		// Check all labels are present.
		allMatch := true
		for k, v := range labels {
			expected := fmt.Sprintf(`%s="%s"`, k, v)
			if !strings.Contains(line, expected) {
				allMatch = false
				break
			}
		}
		if !allMatch {
			continue
		}

		// Extract the value (last space-separated token).
		parts := strings.Fields(line)
		if len(parts) < 2 {
			continue
		}
		val, err := strconv.ParseFloat(parts[len(parts)-1], 64)
		if err != nil {
			continue
		}
		return val, true
	}
	return 0, false
}

// freePort finds an available TCP port.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("freePort: %v", err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return port
}

// ---------------------------------------------------------------------------
// Fault injection
// ---------------------------------------------------------------------------

// injectCrashLoopPod creates a pod that immediately exits with code 1.
func injectCrashLoopPod(t *testing.T, ns string) string {
	t.Helper()
	name := "fault-crashloop-" + randomSuffix(6)
	yaml := fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: %s
  namespace: %s
  labels:
    app: wormsign-fault-test
    fault-type: crashloop
spec:
  restartPolicy: Always
  containers:
    - name: crash
      image: busybox:1.36
      command: ["sh", "-c", "echo 'application error: config not found' >&2; exit 1"]
      resources:
        requests:
          cpu: 10m
          memory: 8Mi
        limits:
          cpu: 50m
          memory: 16Mi
`, name, ns)
	if err := kubectlApplyStdin(yaml); err != nil {
		t.Fatalf("inject crashloop pod: %v", err)
	}
	t.Cleanup(func() {
		kubectl("delete", "pod", name, "-n", ns, "--ignore-not-found")
	})
	t.Logf("injected crashloop pod %s/%s", ns, name)
	return name
}

// injectStuckPendingPod creates a pod requesting impossibly large resources.
func injectStuckPendingPod(t *testing.T, ns string) string {
	t.Helper()
	name := "fault-pending-" + randomSuffix(6)
	yaml := fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: %s
  namespace: %s
  labels:
    app: wormsign-fault-test
    fault-type: stuck-pending
spec:
  containers:
    - name: app
      image: nginx:1.25-alpine
      resources:
        requests:
          cpu: "100"
          memory: "512Gi"
`, name, ns)
	if err := kubectlApplyStdin(yaml); err != nil {
		t.Fatalf("inject stuck-pending pod: %v", err)
	}
	t.Cleanup(func() {
		kubectl("delete", "pod", name, "-n", ns, "--ignore-not-found")
	})
	t.Logf("injected stuck-pending pod %s/%s", ns, name)
	return name
}

// injectBadImagePod creates a pod with a nonexistent image.
func injectBadImagePod(t *testing.T, ns string) string {
	t.Helper()
	name := "fault-badimage-" + randomSuffix(6)
	yaml := fmt.Sprintf(`apiVersion: v1
kind: Pod
metadata:
  name: %s
  namespace: %s
  labels:
    app: wormsign-fault-test
    fault-type: bad-image
spec:
  containers:
    - name: app
      image: registry.example.com/nonexistent/image:v999.999.999
      resources:
        requests:
          cpu: 10m
          memory: 8Mi
`, name, ns)
	if err := kubectlApplyStdin(yaml); err != nil {
		t.Fatalf("inject bad-image pod: %v", err)
	}
	t.Cleanup(func() {
		kubectl("delete", "pod", name, "-n", ns, "--ignore-not-found")
	})
	t.Logf("injected bad-image pod %s/%s", ns, name)
	return name
}