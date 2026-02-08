package correlator

import (
	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

// nodeAnnotationKey is the annotation key used by detectors to record
// the node name associated with a pod fault event.
const nodeAnnotationKey = "wormsign.io/node-name"

// deploymentAnnotationKey is the annotation key used by detectors to record
// the owning Deployment name for a pod fault event.
const deploymentAnnotationKey = "wormsign.io/owner-deployment"

// pvcAnnotationKey is the annotation key used by detectors to record
// the PVC name associated with a pod fault event.
const pvcAnnotationKey = "wormsign.io/pvc-name"

// evaluateNodeCascade implements the NodeCascade correlation rule.
//
// Rule: A NodeNotReady event + N or more pod failure events on the same node
// within the correlation window produces a SuperEvent with the node as
// the primary resource.
//
// Pod events are matched to nodes via the wormsign.io/node-name annotation
// set by detectors during event creation.
func (c *Correlator) evaluateNodeCascade(events []model.FaultEvent, consumed map[string]bool) []*model.SuperEvent {
	// Find all NodeNotReady events.
	nodeEvents := make(map[string]model.FaultEvent) // node name -> event
	for _, e := range events {
		if consumed[e.ID] {
			continue
		}
		if e.DetectorName == "NodeNotReady" && e.Resource.Kind == "Node" {
			nodeEvents[e.Resource.Name] = e
		}
	}

	if len(nodeEvents) == 0 {
		return nil
	}

	// Group pod failure events by node.
	podsByNode := make(map[string][]model.FaultEvent)
	for _, e := range events {
		if consumed[e.ID] {
			continue
		}
		if e.Resource.Kind != "Pod" {
			continue
		}
		nodeName := e.Annotations[nodeAnnotationKey]
		if nodeName == "" {
			continue
		}
		if _, hasNodeEvent := nodeEvents[nodeName]; hasNodeEvent {
			podsByNode[nodeName] = append(podsByNode[nodeName], e)
		}
	}

	var result []*model.SuperEvent
	minPods := c.config.Rules.NodeCascade.MinPodFailures

	for nodeName, nodeEvent := range nodeEvents {
		podEvents := podsByNode[nodeName]
		if len(podEvents) < minPods {
			continue
		}

		// Build the correlated event list: node event + pod events.
		allEvents := make([]model.FaultEvent, 0, 1+len(podEvents))
		allEvents = append(allEvents, nodeEvent)
		allEvents = append(allEvents, podEvents...)

		se, err := model.NewSuperEvent("NodeCascade", nodeEvent.Resource, allEvents)
		if err != nil {
			c.logger.Error("failed to create NodeCascade super-event",
				"error", err,
				"node", nodeName,
			)
			continue
		}

		result = append(result, se)

		// Mark events as consumed.
		consumed[nodeEvent.ID] = true
		for _, pe := range podEvents {
			consumed[pe.ID] = true
		}

		c.logger.Info("NodeCascade correlation matched",
			"node", nodeName,
			"pod_failures", len(podEvents),
			"super_event_id", se.ID,
		)
	}

	return result
}

// evaluateDeploymentRollout implements the DeploymentRollout correlation rule.
//
// Rule: N or more pods from the same Deployment fail within the correlation
// window, producing a SuperEvent with the Deployment as the primary resource.
//
// Deployment ownership is resolved via the wormsign.io/owner-deployment
// annotation set by detectors.
func (c *Correlator) evaluateDeploymentRollout(events []model.FaultEvent, consumed map[string]bool) []*model.SuperEvent {
	// Group pod events by owning deployment (namespace/name).
	type deployKey struct {
		Namespace string
		Name      string
	}
	deployPods := make(map[deployKey][]model.FaultEvent)

	for _, e := range events {
		if consumed[e.ID] {
			continue
		}
		if e.Resource.Kind != "Pod" {
			continue
		}
		deployName := e.Annotations[deploymentAnnotationKey]
		if deployName == "" {
			continue
		}
		key := deployKey{
			Namespace: e.Resource.Namespace,
			Name:      deployName,
		}
		deployPods[key] = append(deployPods[key], e)
	}

	minPods := c.config.Rules.DeploymentRollout.MinPodFailures
	var result []*model.SuperEvent

	for key, podEvents := range deployPods {
		if len(podEvents) < minPods {
			continue
		}

		primaryResource := model.ResourceRef{
			Kind:      "Deployment",
			Namespace: key.Namespace,
			Name:      key.Name,
		}

		se, err := model.NewSuperEvent("DeploymentRollout", primaryResource, podEvents)
		if err != nil {
			c.logger.Error("failed to create DeploymentRollout super-event",
				"error", err,
				"deployment", key.Name,
				"namespace", key.Namespace,
			)
			continue
		}

		result = append(result, se)

		for _, pe := range podEvents {
			consumed[pe.ID] = true
		}

		c.logger.Info("DeploymentRollout correlation matched",
			"deployment", key.Name,
			"namespace", key.Namespace,
			"pod_failures", len(podEvents),
			"super_event_id", se.ID,
		)
	}

	return result
}

// evaluateStorageCascade implements the StorageCascade correlation rule.
//
// Rule: A PVCStuckBinding event + one or more pod failure events referencing
// the same PVC produces a SuperEvent with the PVC as the primary resource.
//
// PVC association is resolved via the wormsign.io/pvc-name annotation
// set by detectors.
func (c *Correlator) evaluateStorageCascade(events []model.FaultEvent, consumed map[string]bool) []*model.SuperEvent {
	// Find all PVCStuckBinding events.
	type pvcKey struct {
		Namespace string
		Name      string
	}
	pvcEvents := make(map[pvcKey]model.FaultEvent)
	for _, e := range events {
		if consumed[e.ID] {
			continue
		}
		if e.DetectorName == "PVCStuckBinding" && e.Resource.Kind == "PersistentVolumeClaim" {
			key := pvcKey{Namespace: e.Resource.Namespace, Name: e.Resource.Name}
			pvcEvents[key] = e
		}
	}

	if len(pvcEvents) == 0 {
		return nil
	}

	// Group pod events by PVC reference.
	podsByPVC := make(map[pvcKey][]model.FaultEvent)
	for _, e := range events {
		if consumed[e.ID] {
			continue
		}
		if e.Resource.Kind != "Pod" {
			continue
		}
		pvcName := e.Annotations[pvcAnnotationKey]
		if pvcName == "" {
			continue
		}
		key := pvcKey{Namespace: e.Resource.Namespace, Name: pvcName}
		if _, hasPVC := pvcEvents[key]; hasPVC {
			podsByPVC[key] = append(podsByPVC[key], e)
		}
	}

	var result []*model.SuperEvent

	for key, pvcEvent := range pvcEvents {
		podEvents := podsByPVC[key]
		if len(podEvents) == 0 {
			continue
		}

		// Build the correlated event list: PVC event + pod events.
		allEvents := make([]model.FaultEvent, 0, 1+len(podEvents))
		allEvents = append(allEvents, pvcEvent)
		allEvents = append(allEvents, podEvents...)

		se, err := model.NewSuperEvent("StorageCascade", pvcEvent.Resource, allEvents)
		if err != nil {
			c.logger.Error("failed to create StorageCascade super-event",
				"error", err,
				"pvc", key.Name,
				"namespace", key.Namespace,
			)
			continue
		}

		result = append(result, se)

		consumed[pvcEvent.ID] = true
		for _, pe := range podEvents {
			consumed[pe.ID] = true
		}

		c.logger.Info("StorageCascade correlation matched",
			"pvc", key.Name,
			"namespace", key.Namespace,
			"pod_failures", len(podEvents),
			"super_event_id", se.ID,
		)
	}

	return result
}

// evaluateNamespaceStorm implements the NamespaceStorm correlation rule.
//
// Rule: More than N fault events in the same namespace within the correlation
// window AND no other rule matched those events. This is a catch-all that
// prevents unbounded LLM calls during event storms.
//
// This rule only considers events not already consumed by other rules.
func (c *Correlator) evaluateNamespaceStorm(events []model.FaultEvent, consumed map[string]bool) []*model.SuperEvent {
	// Group unconsumed events by namespace.
	nsByEvents := make(map[string][]model.FaultEvent)
	for _, e := range events {
		if consumed[e.ID] {
			continue
		}
		ns := e.Resource.Namespace
		if ns == "" {
			continue // skip cluster-scoped resources
		}
		nsByEvents[ns] = append(nsByEvents[ns], e)
	}

	threshold := c.config.Rules.NamespaceStorm.Threshold
	var result []*model.SuperEvent

	for ns, nsEvents := range nsByEvents {
		if len(nsEvents) <= threshold {
			continue
		}

		primaryResource := model.ResourceRef{
			Kind:      "Namespace",
			Namespace: "",
			Name:      ns,
		}

		se, err := model.NewSuperEvent("NamespaceStorm", primaryResource, nsEvents)
		if err != nil {
			c.logger.Error("failed to create NamespaceStorm super-event",
				"error", err,
				"namespace", ns,
			)
			continue
		}

		result = append(result, se)

		for _, e := range nsEvents {
			consumed[e.ID] = true
		}

		c.logger.Info("NamespaceStorm correlation matched",
			"namespace", ns,
			"event_count", len(nsEvents),
			"super_event_id", se.ID,
		)
	}

	return result
}
