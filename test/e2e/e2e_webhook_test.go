//go:build e2e

package e2e

import (
	"testing"
	"time"
)

// TestE2E_WebhookSink deploys a webhook receiver, then installs Wormsign with
// the webhook sink pointing at the receiver. Verifies that fault events are
// delivered via HTTP POST to the webhook URL.
func TestE2E_WebhookSink(t *testing.T) {
	ctrlNs := createTestNamespace(t, "whook")

	// Deploy webhook receiver first so we know its service URL.
	receiverPod, svcURL := injectWebhookReceiver(t, ctrlNs)

	helmInstall(t, ctrlNs, 1,
		"sinks.webhook.enabled=true",
		"sinks.webhook.url="+svcURL,
	)
	t.Cleanup(func() { helmUninstall(t, ctrlNs) })

	waitForRollout(t, ctrlNs, deploymentName, 120*time.Second)

	t.Run("WebhookDelivery", func(t *testing.T) {
		faultNs := createTestNamespace(t, "whkf")
		podName := injectCrashLoopPod(t, faultNs)

		// Wait for K8s event to confirm detection worked.
		waitForWormsignEvent(t, faultNs, podName, 5*time.Minute)
		t.Logf("WormsignRCA K8s event detected for %s/%s", faultNs, podName)

		// Now verify the webhook receiver got the POST payload.
		checkWebhookReceived(t, ctrlNs, receiverPod, podName, 60*time.Second)
		t.Logf("webhook receiver got payload containing pod name %s", podName)
	})
}
