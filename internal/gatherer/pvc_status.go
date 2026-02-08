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

// PVCStatus gathers PVC phase, bound PV details, PV events, and
// related storage information for PVC-related or pod-scheduling fault events.
type PVCStatus struct {
	client   KubeClient
	redactor *redact.Redactor
	logger   *slog.Logger
}

// NewPVCStatus creates a new PVCStatus gatherer.
func NewPVCStatus(client KubeClient, redactor *redact.Redactor, logger *slog.Logger) *PVCStatus {
	if logger == nil {
		logger = slog.Default()
	}
	return &PVCStatus{client: client, redactor: redactor, logger: logger}
}

func (g *PVCStatus) Name() string { return "PVCStatus" }

func (g *PVCStatus) ResourceKinds() []string {
	return []string{"PersistentVolumeClaim", "Pod"}
}
func (g *PVCStatus) DetectorNames() []string { return nil }

func (g *PVCStatus) Gather(ctx context.Context, ref model.ResourceRef) (*model.DiagnosticSection, error) {
	var b strings.Builder

	switch ref.Kind {
	case "PersistentVolumeClaim":
		if err := g.gatherPVC(ctx, ref.Namespace, ref.Name, &b); err != nil {
			return nil, err
		}
	case "Pod":
		// Fetch the pod to find its PVC references.
		pod, err := g.client.GetPod(ctx, ref.Namespace, ref.Name)
		if err != nil {
			return nil, fmt.Errorf("fetching pod %s/%s for PVC info: %w", ref.Namespace, ref.Name, err)
		}

		pvcCount := 0
		for _, vol := range pod.Spec.Volumes {
			if vol.PersistentVolumeClaim != nil {
				pvcCount++
				if err := g.gatherPVC(ctx, ref.Namespace, vol.PersistentVolumeClaim.ClaimName, &b); err != nil {
					b.WriteString(fmt.Sprintf("\nError gathering PVC %s: %s\n", vol.PersistentVolumeClaim.ClaimName, err))
				}
			}
		}
		if pvcCount == 0 {
			b.WriteString(fmt.Sprintf("Pod %s/%s has no PVC volumes.\n", ref.Namespace, ref.Name))
		}
	default:
		return nil, fmt.Errorf("PVCStatus gatherer does not support resource kind %q", ref.Kind)
	}

	content := b.String()
	if g.redactor != nil {
		content = g.redactor.Redact(content)
	}

	return &model.DiagnosticSection{
		GathererName: g.Name(),
		Title:        "PVC Status",
		Content:      content,
		Format:       "text",
	}, nil
}

func (g *PVCStatus) gatherPVC(ctx context.Context, namespace, name string, b *strings.Builder) error {
	pvc, err := g.client.GetPVC(ctx, namespace, name)
	if err != nil {
		return fmt.Errorf("fetching PVC %s/%s: %w", namespace, name, err)
	}

	b.WriteString(fmt.Sprintf("PVC: %s/%s\n", pvc.Namespace, pvc.Name))
	b.WriteString(fmt.Sprintf("Phase: %s\n", pvc.Status.Phase))
	if pvc.Spec.StorageClassName != nil {
		b.WriteString(fmt.Sprintf("StorageClass: %s\n", *pvc.Spec.StorageClassName))
	}
	if storage, ok := pvc.Spec.Resources.Requests["storage"]; ok {
		b.WriteString(fmt.Sprintf("Requested Storage: %s\n", storage.String()))
	}
	if len(pvc.Spec.AccessModes) > 0 {
		modes := make([]string, len(pvc.Spec.AccessModes))
		for i, m := range pvc.Spec.AccessModes {
			modes[i] = string(m)
		}
		b.WriteString(fmt.Sprintf("AccessModes: %s\n", strings.Join(modes, ", ")))
	}

	// Bound PV details
	if pvc.Spec.VolumeName != "" {
		b.WriteString(fmt.Sprintf("BoundVolume: %s\n", pvc.Spec.VolumeName))

		pv, err := g.client.GetPV(ctx, pvc.Spec.VolumeName)
		if err != nil {
			b.WriteString(fmt.Sprintf("  (error fetching PV: %s)\n", err))
		} else {
			b.WriteString(fmt.Sprintf("  PV Phase: %s\n", pv.Status.Phase))
			if pv.Spec.CSI != nil {
				b.WriteString(fmt.Sprintf("  CSI Driver: %s\n", pv.Spec.CSI.Driver))
				if volHandle := pv.Spec.CSI.VolumeHandle; volHandle != "" {
					b.WriteString(fmt.Sprintf("  Volume Handle: %s\n", volHandle))
				}
			}
			if pv.Spec.NodeAffinity != nil {
				b.WriteString("  PV has node affinity constraints\n")
			}
		}
	}

	// PVC events
	opts := metav1.ListOptions{
		FieldSelector: fmt.Sprintf("involvedObject.name=%s,involvedObject.kind=PersistentVolumeClaim", name),
	}
	events, err := g.client.ListEvents(ctx, namespace, opts)
	if err != nil {
		b.WriteString(fmt.Sprintf("  (error listing PVC events: %s)\n", err))
	} else if len(events.Items) > 0 {
		b.WriteString("Events:\n")
		for _, e := range events.Items {
			ts := e.LastTimestamp.Time
			if ts.IsZero() && !e.EventTime.Time.IsZero() {
				ts = e.EventTime.Time
			}
			b.WriteString(fmt.Sprintf("  %s  %s  %s: %s\n",
				ts.Format("2006-01-02T15:04:05Z"),
				e.Type,
				e.Reason,
				e.Message,
			))
		}
	}

	b.WriteString("\n")
	return nil
}

var (
	_ Gatherer       = (*PVCStatus)(nil)
	_ TriggerMatcher = (*PVCStatus)(nil)
)
