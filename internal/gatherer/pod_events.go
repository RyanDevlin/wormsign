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

// PodEvents gathers Kubernetes events for a pod (last 1h).
type PodEvents struct {
	client   KubeClient
	redactor *redact.Redactor
	logger   *slog.Logger
}

// NewPodEvents creates a new PodEvents gatherer.
func NewPodEvents(client KubeClient, redactor *redact.Redactor, logger *slog.Logger) *PodEvents {
	if logger == nil {
		logger = slog.Default()
	}
	return &PodEvents{client: client, redactor: redactor, logger: logger}
}

func (g *PodEvents) Name() string { return "PodEvents" }

func (g *PodEvents) ResourceKinds() []string  { return []string{"Pod"} }
func (g *PodEvents) DetectorNames() []string   { return nil }

func (g *PodEvents) Gather(ctx context.Context, ref model.ResourceRef) (*model.DiagnosticSection, error) {
	opts := metav1.ListOptions{
		FieldSelector: fmt.Sprintf("involvedObject.name=%s,involvedObject.kind=Pod", ref.Name),
	}

	events, err := g.client.ListEvents(ctx, ref.Namespace, opts)
	if err != nil {
		return nil, fmt.Errorf("listing events for pod %s/%s: %w", ref.Namespace, ref.Name, err)
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Events for Pod %s/%s:\n", ref.Namespace, ref.Name))

	if len(events.Items) == 0 {
		b.WriteString("  (no events found)\n")
	}

	for _, e := range events.Items {
		ts := e.LastTimestamp.Time
		if ts.IsZero() && e.EventTime.Time.IsZero() == false {
			ts = e.EventTime.Time
		}
		b.WriteString(fmt.Sprintf("  %s  %s  %s: %s",
			ts.Format("2006-01-02T15:04:05Z"),
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
		Title:        "Pod Events",
		Content:      content,
		Format:       "text",
	}, nil
}

var (
	_ Gatherer       = (*PodEvents)(nil)
	_ TriggerMatcher = (*PodEvents)(nil)
)
