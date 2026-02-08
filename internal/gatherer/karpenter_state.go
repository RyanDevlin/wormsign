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

// KarpenterFetcher abstracts Karpenter CRD access. This is separate from
// KubeClient because Karpenter CRDs may not be installed in every cluster.
type KarpenterFetcher interface {
	// ListNodePools returns a formatted string of all Karpenter NodePools.
	ListNodePools(ctx context.Context) (string, error)

	// ListNodeClaims returns a formatted string of Karpenter NodeClaims.
	ListNodeClaims(ctx context.Context) (string, error)
}

// KarpenterState gathers NodePool status, NodeClaim conditions, and Karpenter
// events for pod scheduling failures. Auto-disabled at startup if Karpenter
// CRDs are not detected.
type KarpenterState struct {
	client           KubeClient
	karpenterFetcher KarpenterFetcher
	redactor         *redact.Redactor
	logger           *slog.Logger
}

// NewKarpenterState creates a new KarpenterState gatherer.
func NewKarpenterState(client KubeClient, karpenterFetcher KarpenterFetcher, redactor *redact.Redactor, logger *slog.Logger) *KarpenterState {
	if logger == nil {
		logger = slog.Default()
	}
	return &KarpenterState{
		client:           client,
		karpenterFetcher: karpenterFetcher,
		redactor:         redactor,
		logger:           logger,
	}
}

func (g *KarpenterState) Name() string { return "KarpenterState" }

func (g *KarpenterState) ResourceKinds() []string { return []string{"Pod"} }
func (g *KarpenterState) DetectorNames() []string {
	return []string{"PodStuckPending"}
}

func (g *KarpenterState) Gather(ctx context.Context, ref model.ResourceRef) (*model.DiagnosticSection, error) {
	var b strings.Builder
	b.WriteString("Karpenter State:\n")

	// Gather NodePools
	nodePools, err := g.karpenterFetcher.ListNodePools(ctx)
	if err != nil {
		b.WriteString(fmt.Sprintf("  Error listing NodePools: %s\n", err))
	} else {
		b.WriteString("\nNodePools:\n")
		b.WriteString(nodePools)
		if !strings.HasSuffix(nodePools, "\n") {
			b.WriteString("\n")
		}
	}

	// Gather NodeClaims
	nodeClaims, err := g.karpenterFetcher.ListNodeClaims(ctx)
	if err != nil {
		b.WriteString(fmt.Sprintf("  Error listing NodeClaims: %s\n", err))
	} else {
		b.WriteString("\nNodeClaims:\n")
		b.WriteString(nodeClaims)
		if !strings.HasSuffix(nodeClaims, "\n") {
			b.WriteString("\n")
		}
	}

	// Gather Karpenter events from the karpenter namespace
	events, err := g.client.ListEvents(ctx, "kube-system", metav1.ListOptions{})
	if err != nil {
		b.WriteString(fmt.Sprintf("  Error listing Karpenter events: %s\n", err))
	} else {
		karpenterEvents := 0
		var eventBuf strings.Builder
		for _, e := range events.Items {
			// Filter for Karpenter-related events
			if strings.Contains(e.ReportingController, "karpenter") ||
				strings.Contains(e.Source.Component, "karpenter") ||
				e.InvolvedObject.Kind == "NodeClaim" ||
				e.InvolvedObject.Kind == "NodePool" {
				ts := e.LastTimestamp.Time
				if ts.IsZero() && !e.EventTime.Time.IsZero() {
					ts = e.EventTime.Time
				}
				eventBuf.WriteString(fmt.Sprintf("  %s  %s/%s  %s: %s\n",
					ts.Format("2006-01-02T15:04:05Z"),
					e.InvolvedObject.Kind,
					e.InvolvedObject.Name,
					e.Reason,
					e.Message,
				))
				karpenterEvents++
			}
		}
		if karpenterEvents > 0 {
			b.WriteString("\nKarpenter Events:\n")
			b.WriteString(eventBuf.String())
		} else {
			b.WriteString("\n  (no Karpenter events found)\n")
		}
	}

	content := b.String()
	if g.redactor != nil {
		content = g.redactor.Redact(content)
	}

	return &model.DiagnosticSection{
		GathererName: g.Name(),
		Title:        "Karpenter State",
		Content:      content,
		Format:       "text",
	}, nil
}

var (
	_ Gatherer       = (*KarpenterState)(nil)
	_ TriggerMatcher = (*KarpenterState)(nil)
)
