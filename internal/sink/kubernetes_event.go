package sink

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

const (
	kubernetesEventSinkName = "kubernetes-event"
	eventSource             = "wormsign"
	eventReasonRCA          = "WormsignRCA"
	maxEventMessageLen      = 1024
)

// KubernetesEventConfig holds configuration for the Kubernetes Event sink.
type KubernetesEventConfig struct {
	// SeverityFilter restricts which severities create events.
	SeverityFilter []model.Severity
}

// KubernetesEventSink creates Kubernetes Warning/Normal events on affected
// resources with the root cause summary. For Super-Events, it fans out to
// each affected resource.
type KubernetesEventSink struct {
	clientset      kubernetes.Interface
	severityFilter []model.Severity
	logger         *slog.Logger
	retryCfg       retryConfig
	instanceName   string
}

// NewKubernetesEventSink creates a new Kubernetes Event sink.
func NewKubernetesEventSink(clientset kubernetes.Interface, cfg KubernetesEventConfig, logger *slog.Logger) (*KubernetesEventSink, error) {
	if clientset == nil {
		return nil, fmt.Errorf("kubernetes event sink: clientset must not be nil")
	}
	if logger == nil {
		return nil, errNilLogger
	}

	instanceName, _ := os.Hostname()
	if instanceName == "" {
		instanceName = "wormsign"
	}

	return &KubernetesEventSink{
		clientset:      clientset,
		severityFilter: cfg.SeverityFilter,
		logger:         logger,
		retryCfg:       defaultRetryConfig(),
		instanceName:   instanceName,
	}, nil
}

// Name returns "kubernetes-event".
func (s *KubernetesEventSink) Name() string {
	return kubernetesEventSinkName
}

// SeverityFilter returns the configured severity filter.
func (s *KubernetesEventSink) SeverityFilter() []model.Severity {
	return s.severityFilter
}

// Deliver creates Kubernetes events on the affected resources.
// For Super-Events, it creates events on each affected resource's owning
// resource.
func (s *KubernetesEventSink) Deliver(ctx context.Context, report *model.RCAReport) error {
	if report == nil {
		return errNilReport
	}

	refs := s.targetResources(report)
	if len(refs) == 0 {
		s.logger.Warn("kubernetes event sink: no target resources found",
			"fault_event_id", report.FaultEventID,
		)
		return nil
	}

	var errs []string
	for _, ref := range refs {
		err := deliverWithRetry(ctx, s.logger, kubernetesEventSinkName, s.retryCfg, func(ctx context.Context) error {
			return s.createEvent(ctx, ref, report)
		})
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %v", ref.String(), err))
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("kubernetes event sink: %d/%d events failed: %s",
			len(errs), len(refs), strings.Join(errs, "; "))
	}
	return nil
}

func (s *KubernetesEventSink) createEvent(ctx context.Context, ref model.ResourceRef, report *model.RCAReport) error {
	eventType := corev1.EventTypeWarning
	if report.Severity == model.SeverityInfo {
		eventType = corev1.EventTypeNormal
	}

	var message string
	if report.BlastRadius != "" {
		message = fmt.Sprintf("[%s] %s — %s", report.Category, report.RootCause, report.BlastRadius)
	} else {
		message = fmt.Sprintf("[%s] %s", report.Category, report.RootCause)
	}
	message = truncateMessage(message)

	namespace := ref.Namespace
	if namespace == "" {
		// Cluster-scoped resources use the default namespace for events.
		namespace = "default"
	}

	now := metav1.NewTime(time.Now().UTC())
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "wormsign-",
			Namespace:    namespace,
			Labels: map[string]string{
				"wormsign.io/category": report.Category,
				"wormsign.io/severity": string(report.Severity),
				"wormsign.io/detector": detectorName(report),
				"wormsign.io/systemic": strconv.FormatBool(report.Systemic),
			},
			Annotations: map[string]string{
				"wormsign.io/fault-event-id": report.FaultEventID,
				"wormsign.io/confidence":     fmt.Sprintf("%.2f", report.Confidence),
				"wormsign.io/analyzer":       report.AnalyzerBackend,
				"wormsign.io/remediation":    strings.Join(report.Remediation, "\n"),
			},
		},
		InvolvedObject: corev1.ObjectReference{
			Kind:      ref.Kind,
			Namespace: ref.Namespace,
			Name:      ref.Name,
			UID:       "",
		},
		Reason:              eventReasonRCA,
		Message:             message,
		Type:                eventType,
		FirstTimestamp:       now,
		LastTimestamp:        now,
		EventTime:           metav1.NewMicroTime(now.Time),
		Action:              "Analyzed",
		ReportingController: eventSource,
		ReportingInstance:   s.instanceName,
		Source: corev1.EventSource{
			Component: eventSource,
		},
	}

	_, err := s.clientset.CoreV1().Events(namespace).Create(ctx, event, metav1.CreateOptions{})
	if err != nil {
		return fmt.Errorf("creating event for %s: %w", ref.String(), err)
	}
	return nil
}

// detectorName extracts the detector or correlation rule name from the report.
func detectorName(report *model.RCAReport) string {
	if report.DiagnosticBundle.FaultEvent != nil {
		return report.DiagnosticBundle.FaultEvent.DetectorName
	}
	if report.DiagnosticBundle.SuperEvent != nil {
		return report.DiagnosticBundle.SuperEvent.CorrelationRule
	}
	return "unknown"
}

// targetResources extracts the resources that should receive events from
// the report. For Super-Events, this includes the primary resource and all
// affected resources' fault event resources.
func (s *KubernetesEventSink) targetResources(report *model.RCAReport) []model.ResourceRef {
	if report.DiagnosticBundle.SuperEvent != nil {
		se := report.DiagnosticBundle.SuperEvent
		refs := []model.ResourceRef{se.PrimaryResource}
		seen := map[string]bool{se.PrimaryResource.String(): true}
		for _, fe := range se.FaultEvents {
			key := fe.Resource.String()
			if !seen[key] {
				refs = append(refs, fe.Resource)
				seen[key] = true
			}
		}
		return refs
	}

	if report.DiagnosticBundle.FaultEvent != nil {
		return []model.ResourceRef{report.DiagnosticBundle.FaultEvent.Resource}
	}

	return nil
}

// truncateMessage ensures the event message doesn't exceed the max length.
func truncateMessage(msg string) string {
	if len(msg) <= maxEventMessageLen {
		return msg
	}
	return msg[:maxEventMessageLen-3] + "..."
}
