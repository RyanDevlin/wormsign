//go:build e2e

package e2e

import (
	"testing"
	"time"
)

// TestE2E_FilterExclusion deploys Wormsign with a namespace exclusion filter
// and verifies that faults in excluded namespaces do not produce events, while
// faults in non-excluded namespaces still do.
func TestE2E_FilterExclusion(t *testing.T) {
	ctrlNs := createTestNamespace(t, "filt")
	helmInstall(t, ctrlNs, 1,
		// Exclude namespaces matching the ws-e2e-excl.* regex pattern.
		// The filter engine treats patterns with metacharacters as regex.
		`filters.excludeNamespaces={kube-system,kube-public,kube-node-lease,ws-e2e-excl.*}`,
	)
	t.Cleanup(func() { helmUninstall(t, ctrlNs) })

	waitForRollout(t, ctrlNs, deploymentName, 120*time.Second)

	t.Run("NamespaceExclusionFilter", func(t *testing.T) {
		// Create a namespace matching the exclusion pattern.
		excludedNs := createTestNamespace(t, "excl")

		// Create a non-excluded namespace as a control.
		controlNs := createTestNamespace(t, "ctrl")

		// Inject crash-looping pods in both namespaces.
		excludedPod := injectCrashLoopPod(t, excludedNs)
		controlPod := injectCrashLoopPod(t, controlNs)

		// The control pod in the non-excluded namespace should produce an event.
		waitForWormsignEvent(t, controlNs, controlPod, 5*time.Minute)
		t.Logf("control: WormsignRCA event detected in non-excluded namespace %s", controlNs)

		// The excluded namespace should NOT produce an event.
		// We already waited ~5min for the control, so the excluded namespace
		// has had plenty of time too. Check for an additional 90s.
		assertNoWormsignEvent(t, excludedNs, excludedPod, 90*time.Second)
	})
}
