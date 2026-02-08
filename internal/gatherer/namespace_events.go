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

// NamespaceEvents gathers Kubernetes events in the affected namespace
// (last 30m). Triggered by all fault events.
type NamespaceEvents struct {
	client   KubeClient
	redactor *redact.Redactor
	logger   *slog.Logger
}

// NewNamespaceEvents creates a new NamespaceEvents gatherer.
func NewNamespaceEvents(client KubeClient, redactor *redact.Redactor, logger *slog.Logger) *NamespaceEvents {
	if logger == nil {
		logger = slog.Default()
	}
	return &NamespaceEvents{client: client, redactor: redactor, logger: logger}
}

func (g *NamespaceEvents) Name() string { return "NamespaceEvents" }

// NamespaceEvents does not implement TriggerMatcher, so it fires for all
// fault events as required by the spec.

func (g *NamespaceEvents) Gather(ctx context.Context, ref model.ResourceRef) (*model.DiagnosticSection, error) {
	if ref.Namespace == "" {
		return &model.DiagnosticSection{
			GathererName: g.Name(),
			Title:        "Namespace Events",
			Content:      "Resource is cluster-scoped; no namespace events to gather.",
			Format:       "text",
		}, nil
	}

	events, err := g.client.ListEvents(ctx, ref.Namespace, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("listing events in namespace %s: %w", ref.Namespace, err)
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Events in namespace %s:\n", ref.Namespace))

	if len(events.Items) == 0 {
		b.WriteString("  (no events found)\n")
	}

	for _, e := range events.Items {
		ts := e.LastTimestamp.Time
		if ts.IsZero() && !e.EventTime.Time.IsZero() {
			ts = e.EventTime.Time
		}
		b.WriteString(fmt.Sprintf("  %s  %s/%s  %s  %s: %s",
			ts.Format("2006-01-02T15:04:05Z"),
			e.InvolvedObject.Kind,
			e.InvolvedObject.Name,
			e.Type,
			e.Reason,
			e.Message,
		))
		if e.Count > 1 {
			b.WriteString(fmt.Sprintf(" (x%d)", e.Count))
		}
		b.WriteString("\n")
	}

	content := b.String()
	if g.redactor != nil {
		content = g.redactor.Redact(content)
	}

	return &model.DiagnosticSection{
		GathererName: g.Name(),
		Title:        "Namespace Events",
		Content:      content,
		Format:       "text",
	}, nil
}

var _ Gatherer = (*NamespaceEvents)(nil)
