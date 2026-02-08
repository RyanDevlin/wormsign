package gatherer

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
	"github.com/k8s-wormsign/k8s-wormsign/internal/redact"
)

// PodDescribe gathers the full pod spec, status, conditions, container
// statuses, exit codes, and resource requests/limits. It fetches the full
// pod object via a direct GET (since detection uses metadata-only informers).
type PodDescribe struct {
	client   KubeClient
	redactor *redact.Redactor
	logger   *slog.Logger
}

// NewPodDescribe creates a new PodDescribe gatherer.
func NewPodDescribe(client KubeClient, redactor *redact.Redactor, logger *slog.Logger) *PodDescribe {
	if logger == nil {
		logger = slog.Default()
	}
	return &PodDescribe{client: client, redactor: redactor, logger: logger}
}

func (g *PodDescribe) Name() string { return "PodDescribe" }

func (g *PodDescribe) ResourceKinds() []string  { return []string{"Pod"} }
func (g *PodDescribe) DetectorNames() []string   { return nil }

func (g *PodDescribe) Gather(ctx context.Context, ref model.ResourceRef) (*model.DiagnosticSection, error) {
	pod, err := g.client.GetPod(ctx, ref.Namespace, ref.Name)
	if err != nil {
		return nil, fmt.Errorf("fetching pod %s/%s: %w", ref.Namespace, ref.Name, err)
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Pod: %s/%s\n", pod.Namespace, pod.Name))
	b.WriteString(fmt.Sprintf("UID: %s\n", pod.UID))
	b.WriteString(fmt.Sprintf("Node: %s\n", pod.Spec.NodeName))
	b.WriteString(fmt.Sprintf("Phase: %s\n", pod.Status.Phase))
	if pod.Status.Reason != "" {
		b.WriteString(fmt.Sprintf("Reason: %s\n", pod.Status.Reason))
	}
	if pod.Status.Message != "" {
		b.WriteString(fmt.Sprintf("Message: %s\n", pod.Status.Message))
	}
	b.WriteString(fmt.Sprintf("ServiceAccount: %s\n", pod.Spec.ServiceAccountName))
	if pod.Spec.Priority != nil {
		b.WriteString(fmt.Sprintf("Priority: %d\n", *pod.Spec.Priority))
	}

	// Conditions
	if len(pod.Status.Conditions) > 0 {
		b.WriteString("\nConditions:\n")
		for _, c := range pod.Status.Conditions {
			b.WriteString(fmt.Sprintf("  %s: %s", c.Type, c.Status))
			if c.Reason != "" {
				b.WriteString(fmt.Sprintf(" (Reason: %s)", c.Reason))
			}
			if c.Message != "" {
				b.WriteString(fmt.Sprintf(" - %s", c.Message))
			}
			b.WriteString("\n")
		}
	}

	// Container specs and statuses
	for _, c := range pod.Spec.Containers {
		b.WriteString(fmt.Sprintf("\nContainer: %s\n", c.Name))
		b.WriteString(fmt.Sprintf("  Image: %s\n", c.Image))
		if c.Resources.Requests != nil {
			b.WriteString(fmt.Sprintf("  Requests: cpu=%s, memory=%s\n",
				c.Resources.Requests.Cpu().String(),
				c.Resources.Requests.Memory().String()))
		}
		if c.Resources.Limits != nil {
			b.WriteString(fmt.Sprintf("  Limits: cpu=%s, memory=%s\n",
				c.Resources.Limits.Cpu().String(),
				c.Resources.Limits.Memory().String()))
		}

		// Redact environment variables sourced from secrets.
		if len(c.Env) > 0 && g.redactor != nil {
			redacted := g.redactor.RedactEnvVars(c.Env)
			b.WriteString("  Environment:\n")
			for _, ev := range redacted {
				if ev.ValueFrom != nil {
					if ev.ValueFrom.SecretKeyRef != nil {
						b.WriteString(fmt.Sprintf("    %s=%s (from Secret %s)\n", ev.Name, redact.Placeholder, ev.ValueFrom.SecretKeyRef.Name))
					} else if ev.ValueFrom.ConfigMapKeyRef != nil {
						b.WriteString(fmt.Sprintf("    %s=(from ConfigMap %s/%s)\n", ev.Name, ev.ValueFrom.ConfigMapKeyRef.Name, ev.ValueFrom.ConfigMapKeyRef.Key))
					}
				} else {
					b.WriteString(fmt.Sprintf("    %s=%s\n", ev.Name, ev.Value))
				}
			}
		}
	}

	// Init container specs
	for _, c := range pod.Spec.InitContainers {
		b.WriteString(fmt.Sprintf("\nInit Container: %s\n", c.Name))
		b.WriteString(fmt.Sprintf("  Image: %s\n", c.Image))
	}

	// Container statuses
	if len(pod.Status.ContainerStatuses) > 0 {
		b.WriteString("\nContainer Statuses:\n")
		for _, cs := range pod.Status.ContainerStatuses {
			b.WriteString(fmt.Sprintf("  %s: ready=%t, restartCount=%d\n", cs.Name, cs.Ready, cs.RestartCount))
			if cs.State.Waiting != nil {
				b.WriteString(fmt.Sprintf("    State: Waiting (reason=%s", cs.State.Waiting.Reason))
				if cs.State.Waiting.Message != "" {
					b.WriteString(fmt.Sprintf(", message=%s", cs.State.Waiting.Message))
				}
				b.WriteString(")\n")
			}
			if cs.State.Running != nil {
				b.WriteString(fmt.Sprintf("    State: Running (since %s)\n", cs.State.Running.StartedAt.Time))
			}
			if cs.State.Terminated != nil {
				t := cs.State.Terminated
				b.WriteString(fmt.Sprintf("    State: Terminated (exitCode=%d, reason=%s", t.ExitCode, t.Reason))
				if t.Message != "" {
					b.WriteString(fmt.Sprintf(", message=%s", t.Message))
				}
				b.WriteString(")\n")
			}
			if cs.LastTerminationState.Terminated != nil {
				t := cs.LastTerminationState.Terminated
				b.WriteString(fmt.Sprintf("    LastTermination: exitCode=%d, reason=%s\n", t.ExitCode, t.Reason))
			}
		}
	}

	// Init container statuses
	if len(pod.Status.InitContainerStatuses) > 0 {
		b.WriteString("\nInit Container Statuses:\n")
		for _, cs := range pod.Status.InitContainerStatuses {
			b.WriteString(fmt.Sprintf("  %s: ready=%t, restartCount=%d\n", cs.Name, cs.Ready, cs.RestartCount))
			if cs.State.Terminated != nil {
				t := cs.State.Terminated
				b.WriteString(fmt.Sprintf("    State: Terminated (exitCode=%d, reason=%s)\n", t.ExitCode, t.Reason))
			}
		}
	}

	// Tolerations
	if len(pod.Spec.Tolerations) > 0 {
		b.WriteString("\nTolerations:\n")
		for _, t := range pod.Spec.Tolerations {
			b.WriteString(fmt.Sprintf("  %s=%s:%s\n", t.Key, t.Value, t.Effect))
		}
	}

	// Node selector
	if len(pod.Spec.NodeSelector) > 0 {
		b.WriteString("\nNodeSelector:\n")
		for k, v := range pod.Spec.NodeSelector {
			b.WriteString(fmt.Sprintf("  %s=%s\n", k, v))
		}
	}

	// Volume mounts summary
	if len(pod.Spec.Volumes) > 0 {
		b.WriteString("\nVolumes:\n")
		for _, v := range pod.Spec.Volumes {
			b.WriteString(fmt.Sprintf("  %s:", v.Name))
			if v.PersistentVolumeClaim != nil {
				b.WriteString(fmt.Sprintf(" PVC(%s)", v.PersistentVolumeClaim.ClaimName))
			} else if v.ConfigMap != nil {
				b.WriteString(fmt.Sprintf(" ConfigMap(%s)", v.ConfigMap.Name))
			} else if v.Secret != nil {
				b.WriteString(fmt.Sprintf(" Secret(%s)", v.Secret.SecretName))
			} else if v.EmptyDir != nil {
				b.WriteString(" EmptyDir")
			} else {
				b.WriteString(" (other)")
			}
			b.WriteString("\n")
		}
	}

	content := b.String()
	if g.redactor != nil {
		content = g.redactor.Redact(content)
	}

	return &model.DiagnosticSection{
		GathererName: g.Name(),
		Title:        "Pod Description",
		Content:      content,
		Format:       "text",
	}, nil
}

var (
	_ Gatherer       = (*PodDescribe)(nil)
	_ TriggerMatcher = (*PodDescribe)(nil)
)
