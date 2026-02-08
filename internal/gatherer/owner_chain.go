package gatherer

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
	"github.com/k8s-wormsign/k8s-wormsign/internal/redact"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// OwnerChain walks ownerReferences to find the top-level controller
// (Deployment, StatefulSet, Job, DaemonSet) for all pod-related fault events.
type OwnerChain struct {
	client   KubeClient
	redactor *redact.Redactor
	logger   *slog.Logger
}

// NewOwnerChain creates a new OwnerChain gatherer.
func NewOwnerChain(client KubeClient, redactor *redact.Redactor, logger *slog.Logger) *OwnerChain {
	if logger == nil {
		logger = slog.Default()
	}
	return &OwnerChain{client: client, redactor: redactor, logger: logger}
}

func (g *OwnerChain) Name() string { return "OwnerChain" }

func (g *OwnerChain) ResourceKinds() []string { return []string{"Pod"} }
func (g *OwnerChain) DetectorNames() []string { return nil }

func (g *OwnerChain) Gather(ctx context.Context, ref model.ResourceRef) (*model.DiagnosticSection, error) {
	pod, err := g.client.GetPod(ctx, ref.Namespace, ref.Name)
	if err != nil {
		return nil, fmt.Errorf("fetching pod %s/%s for owner chain: %w", ref.Namespace, ref.Name, err)
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Owner Chain for Pod %s/%s:\n", ref.Namespace, ref.Name))

	if len(pod.OwnerReferences) == 0 {
		b.WriteString("  Pod has no owner references (standalone pod).\n")
		return &model.DiagnosticSection{
			GathererName: g.Name(),
			Title:        "Owner Chain",
			Content:      b.String(),
			Format:       "text",
		}, nil
	}

	// Walk the owner references chain
	owners := pod.OwnerReferences
	namespace := ref.Namespace
	depth := 0
	maxDepth := 10 // prevent infinite loops

	for len(owners) > 0 && depth < maxDepth {
		depth++
		var nextOwners []metav1.OwnerReference

		for _, owner := range owners {
			isController := owner.Controller != nil && *owner.Controller
			indent := strings.Repeat("  ", depth)
			b.WriteString(fmt.Sprintf("%s%s/%s (controller=%v)\n", indent, owner.Kind, owner.Name, isController))

			// Try to fetch the owner and get its ownerReferences
			fetchedOwners, err := g.fetchOwnerRefs(ctx, namespace, owner.Kind, owner.Name)
			if err != nil {
				b.WriteString(fmt.Sprintf("%s  (error fetching: %s)\n", indent, err))
				continue
			}

			// Add status info for known controller types
			statusLine := g.fetchOwnerStatus(ctx, namespace, owner.Kind, owner.Name)
			if statusLine != "" {
				b.WriteString(fmt.Sprintf("%s  %s\n", indent, statusLine))
			}

			if len(fetchedOwners) > 0 && isController {
				nextOwners = append(nextOwners, fetchedOwners...)
			}
		}

		owners = nextOwners
	}

	content := b.String()
	if g.redactor != nil {
		content = g.redactor.Redact(content)
	}

	return &model.DiagnosticSection{
		GathererName: g.Name(),
		Title:        "Owner Chain",
		Content:      content,
		Format:       "text",
	}, nil
}

// fetchOwnerRefs fetches the ownerReferences of a resource by kind and name.
func (g *OwnerChain) fetchOwnerRefs(ctx context.Context, namespace, kind, name string) ([]metav1.OwnerReference, error) {
	switch kind {
	case "ReplicaSet":
		rs, err := g.client.GetReplicaSet(ctx, namespace, name)
		if err != nil {
			return nil, err
		}
		return rs.OwnerReferences, nil
	case "Deployment":
		// Deployments are typically top-level; return empty
		return nil, nil
	case "StatefulSet":
		return nil, nil
	case "DaemonSet":
		return nil, nil
	case "Job":
		job, err := g.client.GetJob(ctx, namespace, name)
		if err != nil {
			return nil, err
		}
		return job.OwnerReferences, nil
	default:
		return nil, nil
	}
}

// fetchOwnerStatus returns a brief status line for known controller types.
func (g *OwnerChain) fetchOwnerStatus(ctx context.Context, namespace, kind, name string) string {
	switch kind {
	case "Deployment":
		deploy, err := g.client.GetDeployment(ctx, namespace, name)
		if err != nil {
			return ""
		}
		desired := int32(0)
		if deploy.Spec.Replicas != nil {
			desired = *deploy.Spec.Replicas
		}
		return fmt.Sprintf("Replicas: %d desired, %d ready, %d available",
			desired, deploy.Status.ReadyReplicas, deploy.Status.AvailableReplicas)
	case "StatefulSet":
		sts, err := g.client.GetStatefulSet(ctx, namespace, name)
		if err != nil {
			return ""
		}
		desired := int32(0)
		if sts.Spec.Replicas != nil {
			desired = *sts.Spec.Replicas
		}
		return fmt.Sprintf("Replicas: %d desired, %d ready, %d available",
			desired, sts.Status.ReadyReplicas, sts.Status.AvailableReplicas)
	case "DaemonSet":
		ds, err := g.client.GetDaemonSet(ctx, namespace, name)
		if err != nil {
			return ""
		}
		return fmt.Sprintf("Desired: %d, Ready: %d, Available: %d",
			ds.Status.DesiredNumberScheduled, ds.Status.NumberReady, ds.Status.NumberAvailable)
	case "Job":
		job, err := g.client.GetJob(ctx, namespace, name)
		if err != nil {
			return ""
		}
		return fmt.Sprintf("Active: %d, Succeeded: %d, Failed: %d",
			job.Status.Active, job.Status.Succeeded, job.Status.Failed)
	case "ReplicaSet":
		rs, err := g.client.GetReplicaSet(ctx, namespace, name)
		if err != nil {
			return ""
		}
		desired := int32(0)
		if rs.Spec.Replicas != nil {
			desired = *rs.Spec.Replicas
		}
		return fmt.Sprintf("Replicas: %d desired, %d ready, %d available",
			desired, rs.Status.ReadyReplicas, rs.Status.AvailableReplicas)
	default:
		return ""
	}
}

var (
	_ Gatherer       = (*OwnerChain)(nil)
	_ TriggerMatcher = (*OwnerChain)(nil)
)
