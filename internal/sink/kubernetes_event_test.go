package sink

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

// eventCounter is used by the generateName reactor to produce unique names.
var eventCounter atomic.Int64

// fakeClientWithGenerateName returns a fake clientset that handles
// GenerateName for Event objects (the default fake client doesn't).
func fakeClientWithGenerateName() *fake.Clientset {
	cs := fake.NewSimpleClientset()
	cs.PrependReactor("create", "events", func(action k8stesting.Action) (bool, runtime.Object, error) {
		createAction := action.(k8stesting.CreateAction)
		event := createAction.GetObject().(*corev1.Event)
		if event.Name == "" && event.GenerateName != "" {
			event.Name = fmt.Sprintf("%s%d", event.GenerateName, eventCounter.Add(1))
		}
		return false, event, nil
	})
	return cs
}

func TestNewKubernetesEventSink_Validation(t *testing.T) {
	t.Run("nil clientset", func(t *testing.T) {
		// Pass a typed nil kubernetes.Interface to test the nil check.
		var cs kubernetes.Interface
		_, err := NewKubernetesEventSink(cs, KubernetesEventConfig{}, silentLogger())
		if err == nil {
			t.Fatal("expected error for nil clientset")
		}
	})

	t.Run("nil logger", func(t *testing.T) {
		cs := fake.NewSimpleClientset()
		_, err := NewKubernetesEventSink(cs, KubernetesEventConfig{}, nil)
		if err == nil {
			t.Fatal("expected error for nil logger")
		}
	})

	t.Run("valid", func(t *testing.T) {
		cs := fake.NewSimpleClientset()
		_, err := NewKubernetesEventSink(cs, KubernetesEventConfig{}, silentLogger())
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestKubernetesEventSink_Name(t *testing.T) {
	cs := fake.NewSimpleClientset()
	s, _ := NewKubernetesEventSink(cs, KubernetesEventConfig{}, silentLogger())
	if s.Name() != "kubernetes-event" {
		t.Errorf("Name() = %q, want %q", s.Name(), "kubernetes-event")
	}
}

func TestKubernetesEventSink_SeverityFilter(t *testing.T) {
	cs := fake.NewSimpleClientset()
	s, _ := NewKubernetesEventSink(cs, KubernetesEventConfig{
		SeverityFilter: []model.Severity{model.SeverityCritical},
	}, silentLogger())
	f := s.SeverityFilter()
	if len(f) != 1 || f[0] != model.SeverityCritical {
		t.Errorf("SeverityFilter() = %v, want [critical]", f)
	}
}

func TestKubernetesEventSink_Deliver_FaultEvent(t *testing.T) {
	cs := fakeClientWithGenerateName()
	s, _ := NewKubernetesEventSink(cs, KubernetesEventConfig{}, silentLogger())
	s.retryCfg = retryConfig{maxAttempts: 1, baseDelay: 0, multiplier: 1}

	report := testReport()
	err := s.Deliver(context.Background(), report)
	if err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}

	events, err := cs.CoreV1().Events("default").List(context.Background(), metav1.ListOptions{})
	if err != nil {
		t.Fatalf("listing events: %v", err)
	}
	if len(events.Items) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events.Items))
	}

	event := events.Items[0]
	if event.Reason != eventReasonRCA {
		t.Errorf("Reason = %q, want %q", event.Reason, eventReasonRCA)
	}
	if event.Type != corev1.EventTypeWarning {
		t.Errorf("Type = %q, want %q", event.Type, corev1.EventTypeWarning)
	}
	if event.InvolvedObject.Kind != "Pod" {
		t.Errorf("InvolvedObject.Kind = %q, want Pod", event.InvolvedObject.Kind)
	}
	if event.InvolvedObject.Name != "test-pod" {
		t.Errorf("InvolvedObject.Name = %q, want test-pod", event.InvolvedObject.Name)
	}
	if event.Source.Component != eventSource {
		t.Errorf("Source.Component = %q, want %q", event.Source.Component, eventSource)
	}
}

func TestKubernetesEventSink_Deliver_SuperEvent(t *testing.T) {
	cs := fakeClientWithGenerateName()
	s, _ := NewKubernetesEventSink(cs, KubernetesEventConfig{}, silentLogger())
	s.retryCfg = retryConfig{maxAttempts: 1, baseDelay: 0, multiplier: 1}

	report := testSuperEventReport()
	err := s.Deliver(context.Background(), report)
	if err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}

	// For SuperEvent: primary (Node/node-1, ns="") → goes to "default" namespace,
	// plus 2 fault events (Pod/default/pod-a, Pod/default/pod-b).
	// All 3 events end up in "default" namespace.
	defaultEvents, _ := cs.CoreV1().Events("default").List(context.Background(), metav1.ListOptions{})

	totalEvents := len(defaultEvents.Items)
	if totalEvents != 3 {
		t.Errorf("expected 3 events (1 node + 2 pods), got %d", totalEvents)
	}
}

func TestKubernetesEventSink_Deliver_InfoSeverity(t *testing.T) {
	cs := fakeClientWithGenerateName()
	s, _ := NewKubernetesEventSink(cs, KubernetesEventConfig{}, silentLogger())
	s.retryCfg = retryConfig{maxAttempts: 1, baseDelay: 0, multiplier: 1}

	report := testReport()
	report.Severity = model.SeverityInfo

	err := s.Deliver(context.Background(), report)
	if err != nil {
		t.Fatalf("Deliver() error = %v", err)
	}

	events, _ := cs.CoreV1().Events("default").List(context.Background(), metav1.ListOptions{})
	if len(events.Items) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events.Items))
	}
	if events.Items[0].Type != corev1.EventTypeNormal {
		t.Errorf("info severity should create Normal event, got %q", events.Items[0].Type)
	}
}

func TestKubernetesEventSink_Deliver_NilReport(t *testing.T) {
	cs := fake.NewSimpleClientset()
	s, _ := NewKubernetesEventSink(cs, KubernetesEventConfig{}, silentLogger())
	err := s.Deliver(context.Background(), nil)
	if err == nil {
		t.Fatal("Deliver(nil) should return error")
	}
}

func TestKubernetesEventSink_Deliver_APIError(t *testing.T) {
	cs := fake.NewSimpleClientset()
	cs.PrependReactor("create", "events", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, context.DeadlineExceeded
	})

	s, _ := NewKubernetesEventSink(cs, KubernetesEventConfig{}, silentLogger())
	s.retryCfg = retryConfig{maxAttempts: 1, baseDelay: 0, multiplier: 1}

	err := s.Deliver(context.Background(), testReport())
	if err == nil {
		t.Fatal("expected error for API failure")
	}
}

func TestKubernetesEventSink_TargetResources_Dedup(t *testing.T) {
	cs := fake.NewSimpleClientset()
	s, _ := NewKubernetesEventSink(cs, KubernetesEventConfig{}, silentLogger())

	report := &model.RCAReport{
		DiagnosticBundle: model.DiagnosticBundle{
			SuperEvent: &model.SuperEvent{
				PrimaryResource: model.ResourceRef{Kind: "Pod", Namespace: "default", Name: "pod-a"},
				FaultEvents: []model.FaultEvent{
					{Resource: model.ResourceRef{Kind: "Pod", Namespace: "default", Name: "pod-a"}},
					{Resource: model.ResourceRef{Kind: "Pod", Namespace: "default", Name: "pod-b"}},
					{Resource: model.ResourceRef{Kind: "Pod", Namespace: "default", Name: "pod-a"}}, // duplicate
				},
			},
		},
	}

	refs := s.targetResources(report)
	if len(refs) != 2 {
		t.Errorf("expected 2 unique refs, got %d", len(refs))
	}
}

func TestKubernetesEventSink_TargetResources_EmptyReport(t *testing.T) {
	cs := fake.NewSimpleClientset()
	s, _ := NewKubernetesEventSink(cs, KubernetesEventConfig{}, silentLogger())

	report := &model.RCAReport{DiagnosticBundle: model.DiagnosticBundle{}}
	refs := s.targetResources(report)
	if refs != nil {
		t.Errorf("expected nil refs for empty report, got %v", refs)
	}
}

func TestTruncateMessage(t *testing.T) {
	tests := []struct {
		name string
		msg  string
	}{
		{
			name: "short message",
			msg:  "short",
		},
		{
			name: "exact limit",
			msg:  string(make([]byte, maxEventMessageLen)),
		},
		{
			name: "over limit",
			msg:  string(make([]byte, maxEventMessageLen+100)),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateMessage(tt.msg)
			if len(got) > maxEventMessageLen {
				t.Errorf("truncateMessage() len = %d, exceeds max %d", len(got), maxEventMessageLen)
			}
		})
	}
}
