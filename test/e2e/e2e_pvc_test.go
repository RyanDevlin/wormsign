//go:build e2e

package e2e

import (
	"testing"
	"time"
)

// TestE2E_PVCDetection deploys Wormsign with PVCStuckBinding detector enabled
// and verifies that a PVC referencing a nonexistent StorageClass triggers a
// WormsignRCA event.
func TestE2E_PVCDetection(t *testing.T) {
	ctrlNs := createTestNamespace(t, "pvc")
	helmInstall(t, ctrlNs, 1,
		"detectors.pvcStuckBinding.enabled=true",
		"detectors.pvcStuckBinding.threshold=30s",
		"detectors.pvcStuckBinding.cooldown=2m",
	)
	t.Cleanup(func() { helmUninstall(t, ctrlNs) })

	waitForRollout(t, ctrlNs, deploymentName, 120*time.Second)

	t.Run("PVCStuckBindingDetection", func(t *testing.T) {
		faultNs := createTestNamespace(t, "pvcf")
		pvcName := injectStuckPVC(t, faultNs)

		// PVC stays Pending (nonexistent StorageClass), threshold=30s + scan interval.
		waitForWormsignEvent(t, faultNs, pvcName, 5*time.Minute)
		t.Logf("WormsignRCA event detected for stuck PVC %s/%s", faultNs, pvcName)
	})
}
