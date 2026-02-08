package gatherer

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
	"github.com/k8s-wormsign/k8s-wormsign/internal/redact"
)

// PodLogs gathers the last N lines of container logs, including the
// previous terminated container. Logs are passed through the redaction engine.
type PodLogs struct {
	client          KubeClient
	redactor        *redact.Redactor
	logger          *slog.Logger
	tailLines       int64
	includePrevious bool
}

// NewPodLogs creates a new PodLogs gatherer.
func NewPodLogs(client KubeClient, redactor *redact.Redactor, logger *slog.Logger, tailLines int64, includePrevious bool) *PodLogs {
	if logger == nil {
		logger = slog.Default()
	}
	if tailLines <= 0 {
		tailLines = 100
	}
	return &PodLogs{
		client:          client,
		redactor:        redactor,
		logger:          logger,
		tailLines:       tailLines,
		includePrevious: includePrevious,
	}
}

func (g *PodLogs) Name() string { return "PodLogs" }

func (g *PodLogs) ResourceKinds() []string { return []string{"Pod"} }
func (g *PodLogs) DetectorNames() []string {
	return []string{"PodCrashLoop", "PodFailed"}
}

func (g *PodLogs) Gather(ctx context.Context, ref model.ResourceRef) (*model.DiagnosticSection, error) {
	// Get the pod to enumerate containers.
	pod, err := g.client.GetPod(ctx, ref.Namespace, ref.Name)
	if err != nil {
		return nil, fmt.Errorf("fetching pod %s/%s for log collection: %w", ref.Namespace, ref.Name, err)
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Logs for Pod %s/%s:\n", ref.Namespace, ref.Name))

	for _, cs := range pod.Status.ContainerStatuses {
		b.WriteString(fmt.Sprintf("\n--- Container: %s ---\n", cs.Name))

		// Current logs
		logs, err := g.client.GetPodLogs(ctx, ref.Namespace, ref.Name, cs.Name, &g.tailLines, false)
		if err != nil {
			b.WriteString(fmt.Sprintf("  (error fetching logs: %s)\n", err))
		} else if logs == "" {
			b.WriteString("  (no logs)\n")
		} else {
			if g.redactor != nil {
				logs = g.redactor.Redact(logs)
			}
			b.WriteString(logs)
			if !strings.HasSuffix(logs, "\n") {
				b.WriteString("\n")
			}
		}

		// Previous terminated container logs
		if g.includePrevious && cs.RestartCount > 0 {
			b.WriteString(fmt.Sprintf("\n--- Container: %s (previous) ---\n", cs.Name))
			prevLogs, err := g.client.GetPodLogs(ctx, ref.Namespace, ref.Name, cs.Name, &g.tailLines, true)
			if err != nil {
				b.WriteString(fmt.Sprintf("  (error fetching previous logs: %s)\n", err))
			} else if prevLogs == "" {
				b.WriteString("  (no previous logs)\n")
			} else {
				if g.redactor != nil {
					prevLogs = g.redactor.Redact(prevLogs)
				}
				b.WriteString(prevLogs)
				if !strings.HasSuffix(prevLogs, "\n") {
					b.WriteString("\n")
				}
			}
		}
	}

	// Also collect init container logs if any terminated with error
	for _, cs := range pod.Status.InitContainerStatuses {
		if cs.State.Terminated != nil && cs.State.Terminated.ExitCode != 0 {
			b.WriteString(fmt.Sprintf("\n--- Init Container: %s ---\n", cs.Name))
			logs, err := g.client.GetPodLogs(ctx, ref.Namespace, ref.Name, cs.Name, &g.tailLines, false)
			if err != nil {
				b.WriteString(fmt.Sprintf("  (error fetching logs: %s)\n", err))
			} else if logs != "" {
				if g.redactor != nil {
					logs = g.redactor.Redact(logs)
				}
				b.WriteString(logs)
				if !strings.HasSuffix(logs, "\n") {
					b.WriteString("\n")
				}
			}
		}
	}

	return &model.DiagnosticSection{
		GathererName: g.Name(),
		Title:        "Pod Logs",
		Content:      b.String(),
		Format:       "text",
	}, nil
}

var (
	_ Gatherer       = (*PodLogs)(nil)
	_ TriggerMatcher = (*PodLogs)(nil)
)
