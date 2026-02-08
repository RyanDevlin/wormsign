package gatherer

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
	"github.com/k8s-wormsign/k8s-wormsign/internal/redact"
)

// ReplicaSetStatus gathers the owning ReplicaSet/Deployment status, replica
// counts, and rollout conditions for pod failures.
type ReplicaSetStatus struct {
	client   KubeClient
	redactor *redact.Redactor
	logger   *slog.Logger
}

// NewReplicaSetStatus creates a new ReplicaSetStatus gatherer.
func NewReplicaSetStatus(client KubeClient, redactor *redact.Redactor, logger *slog.Logger) *ReplicaSetStatus {
	if logger == nil {
		logger = slog.Default()
	}
	return &ReplicaSetStatus{client: client, redactor: redactor, logger: logger}
}

func (g *ReplicaSetStatus) Name() string { return "ReplicaSetStatus" }

func (g *ReplicaSetStatus) ResourceKinds() []string { return []string{"Pod"} }
func (g *ReplicaSetStatus) DetectorNames() []string { return nil }

func (g *ReplicaSetStatus) Gather(ctx context.Context, ref model.ResourceRef) (*model.DiagnosticSection, error) {
	pod, err := g.client.GetPod(ctx, ref.Namespace, ref.Name)
	if err != nil {
		return nil, fmt.Errorf("fetching pod %s/%s for owner info: %w", ref.Namespace, ref.Name, err)
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Owner chain for Pod %s/%s:\n", ref.Namespace, ref.Name))

	// Find the owning ReplicaSet
	var rsName string
	for _, owner := range pod.OwnerReferences {
		if owner.Kind == "ReplicaSet" {
			rsName = owner.Name
			break
		}
	}

	if rsName == "" {
		b.WriteString("  Pod has no ReplicaSet owner.\n")

		// Check for other owners
		for _, owner := range pod.OwnerReferences {
			b.WriteString(fmt.Sprintf("  Owner: %s/%s (controller=%v)\n",
				owner.Kind, owner.Name, owner.Controller != nil && *owner.Controller))
		}

		content := b.String()
		if g.redactor != nil {
			content = g.redactor.Redact(content)
		}
		return &model.DiagnosticSection{
			GathererName: g.Name(),
			Title:        "ReplicaSet Status",
			Content:      content,
			Format:       "text",
		}, nil
	}

	// Fetch the ReplicaSet
	rs, err := g.client.GetReplicaSet(ctx, ref.Namespace, rsName)
	if err != nil {
		b.WriteString(fmt.Sprintf("  Error fetching ReplicaSet %s: %s\n", rsName, err))
	} else {
		desired := int32(0)
		if rs.Spec.Replicas != nil {
			desired = *rs.Spec.Replicas
		}
		b.WriteString(fmt.Sprintf("\nReplicaSet: %s/%s\n", rs.Namespace, rs.Name))
		b.WriteString(fmt.Sprintf("  Desired: %d, Ready: %d, Available: %d\n",
			desired, rs.Status.ReadyReplicas, rs.Status.AvailableReplicas))

		// Find the owning Deployment
		for _, owner := range rs.OwnerReferences {
			if owner.Kind == "Deployment" {
				deploy, err := g.client.GetDeployment(ctx, ref.Namespace, owner.Name)
				if err != nil {
					b.WriteString(fmt.Sprintf("  Error fetching Deployment %s: %s\n", owner.Name, err))
				} else {
					desired := int32(0)
					if deploy.Spec.Replicas != nil {
						desired = *deploy.Spec.Replicas
					}
					b.WriteString(fmt.Sprintf("\nDeployment: %s/%s\n", deploy.Namespace, deploy.Name))
					b.WriteString(fmt.Sprintf("  Desired: %d, Ready: %d, Available: %d, Updated: %d\n",
						desired,
						deploy.Status.ReadyReplicas,
						deploy.Status.AvailableReplicas,
						deploy.Status.UpdatedReplicas))
					if deploy.Spec.Strategy.Type != "" {
						b.WriteString(fmt.Sprintf("  Strategy: %s\n", deploy.Spec.Strategy.Type))
					}

					for _, c := range deploy.Status.Conditions {
						b.WriteString(fmt.Sprintf("  Condition %s: %s", c.Type, c.Status))
						if c.Reason != "" {
							b.WriteString(fmt.Sprintf(" (Reason: %s)", c.Reason))
						}
						if c.Message != "" {
							b.WriteString(fmt.Sprintf(" - %s", c.Message))
						}
						b.WriteString("\n")
					}
				}
				break
			}
		}
	}

	content := b.String()
	if g.redactor != nil {
		content = g.redactor.Redact(content)
	}

	return &model.DiagnosticSection{
		GathererName: g.Name(),
		Title:        "ReplicaSet Status",
		Content:      content,
		Format:       "text",
	}, nil
}

var (
	_ Gatherer       = (*ReplicaSetStatus)(nil)
	_ TriggerMatcher = (*ReplicaSetStatus)(nil)
)
