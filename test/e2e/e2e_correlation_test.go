//go:build e2e

package e2e

import (
	"testing"
	"time"
)

// TestE2E_Correlation deploys Wormsign with correlation enabled and validates
// DeploymentRollout and NamespaceStorm correlation rules.
func TestE2E_Correlation(t *testing.T) {
	ctrlNs := createTestNamespace(t, "corr")
	helmInstall(t, ctrlNs, 1,
		"correlation.enabled=true",
		"correlation.windowDuration=3m",
		"correlation.rules.deploymentRollout.enabled=true",
		"correlation.rules.deploymentRollout.minPodFailures=3",
		"correlation.rules.namespaceStorm.enabled=true",
		"correlation.rules.namespaceStorm.threshold=5",
	)
	t.Cleanup(func() { helmUninstall(t, ctrlNs) })

	waitForRollout(t, ctrlNs, deploymentName, 120*time.Second)

	t.Run("DeploymentRolloutCorrelation", func(t *testing.T) {
		t.Parallel()
		faultNs := createTestNamespace(t, "drco")
		deployName := injectFailingDeployment(t, faultNs, 3)

		// 3-replica Deployment crash-loops → 3 PodCrashLoop events with same
		// owner-deployment annotation → DeploymentRollout rule fires after
		// correlation window. The K8s event sink creates events on both the
		// Deployment and each pod.
		waitForWormsignEvent(t, faultNs, deployName, 8*time.Minute)
		t.Logf("WormsignRCA event detected for failing deployment %s/%s", faultNs, deployName)
	})

	t.Run("NamespaceStormCorrelation", func(t *testing.T) {
		t.Parallel()
		faultNs := createTestNamespace(t, "nsst")

		// Inject 6 bare crash-looping pods (no owning Deployment) so they
		// remain uncorrelated by DeploymentRollout. With threshold=5, having
		// >5 uncorrelated events in one namespace triggers NamespaceStorm.
		for i := 0; i < 6; i++ {
			injectCrashLoopPod(t, faultNs)
		}

		// NamespaceStorm fires after correlation window. Events are created
		// on each fault pod in the namespace. Match any event.
		waitForWormsignEvent(t, faultNs, "", 8*time.Minute)
		t.Logf("WormsignRCA event detected for namespace storm in %s", faultNs)
	})
}
