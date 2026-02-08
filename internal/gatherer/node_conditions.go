package gatherer

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
	"github.com/k8s-wormsign/k8s-wormsign/internal/redact"
)

// NodeConditions gathers node status, conditions, allocatable vs capacity,
// and taints for pod scheduling failures and node issues.
type NodeConditions struct {
	client             KubeClient
	redactor           *redact.Redactor
	logger             *slog.Logger
	includeAllocatable bool
}

// NewNodeConditions creates a new NodeConditions gatherer.
func NewNodeConditions(client KubeClient, redactor *redact.Redactor, logger *slog.Logger, includeAllocatable bool) *NodeConditions {
	if logger == nil {
		logger = slog.Default()
	}
	return &NodeConditions{
		client:             client,
		redactor:           redactor,
		logger:             logger,
		includeAllocatable: includeAllocatable,
	}
}

func (g *NodeConditions) Name() string { return "NodeConditions" }

func (g *NodeConditions) ResourceKinds() []string { return []string{"Pod", "Node"} }
func (g *NodeConditions) DetectorNames() []string { return nil }

func (g *NodeConditions) Gather(ctx context.Context, ref model.ResourceRef) (*model.DiagnosticSection, error) {
	nodeName := ""
	switch ref.Kind {
	case "Node":
		nodeName = ref.Name
	case "Pod":
		// Fetch the pod to get its node name.
		pod, err := g.client.GetPod(ctx, ref.Namespace, ref.Name)
		if err != nil {
			return nil, fmt.Errorf("fetching pod %s/%s for node info: %w", ref.Namespace, ref.Name, err)
		}
		nodeName = pod.Spec.NodeName
		if nodeName == "" {
			return &model.DiagnosticSection{
				GathererName: g.Name(),
				Title:        "Node Conditions",
				Content:      fmt.Sprintf("Pod %s/%s is not scheduled to a node yet.", ref.Namespace, ref.Name),
				Format:       "text",
			}, nil
		}
	default:
		return nil, fmt.Errorf("NodeConditions gatherer does not support resource kind %q", ref.Kind)
	}

	node, err := g.client.GetNode(ctx, nodeName)
	if err != nil {
		return nil, fmt.Errorf("fetching node %s: %w", nodeName, err)
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Node: %s\n", node.Name))
	b.WriteString(fmt.Sprintf("UID: %s\n", node.UID))

	// Conditions
	b.WriteString("\nConditions:\n")
	for _, c := range node.Status.Conditions {
		b.WriteString(fmt.Sprintf("  %s: %s", c.Type, c.Status))
		if c.Reason != "" {
			b.WriteString(fmt.Sprintf(" (Reason: %s)", c.Reason))
		}
		if c.Message != "" {
			b.WriteString(fmt.Sprintf(" - %s", c.Message))
		}
		b.WriteString("\n")
	}

	// Allocatable vs capacity
	if g.includeAllocatable {
		b.WriteString("\nCapacity:\n")
		for r, q := range node.Status.Capacity {
			b.WriteString(fmt.Sprintf("  %s: %s\n", r, q.String()))
		}
		b.WriteString("\nAllocatable:\n")
		for r, q := range node.Status.Allocatable {
			b.WriteString(fmt.Sprintf("  %s: %s\n", r, q.String()))
		}
	}

	// Taints
	if len(node.Spec.Taints) > 0 {
		b.WriteString("\nTaints:\n")
		for _, t := range node.Spec.Taints {
			b.WriteString(fmt.Sprintf("  %s=%s:%s\n", t.Key, t.Value, t.Effect))
		}
	}

	// Labels (select common scheduling-relevant labels)
	if len(node.Labels) > 0 {
		b.WriteString("\nLabels:\n")
		for k, v := range node.Labels {
			b.WriteString(fmt.Sprintf("  %s=%s\n", k, v))
		}
	}

	content := b.String()
	if g.redactor != nil {
		content = g.redactor.Redact(content)
	}

	return &model.DiagnosticSection{
		GathererName: g.Name(),
		Title:        "Node Conditions",
		Content:      content,
		Format:       "text",
	}, nil
}

var (
	_ Gatherer       = (*NodeConditions)(nil)
	_ TriggerMatcher = (*NodeConditions)(nil)
)
