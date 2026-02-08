package detector

import (
	"testing"
	"time"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

// =====================================================================
// PodStuckPending tests
// =====================================================================

func TestPodStuckPending_Check(t *testing.T) {
	now := time.Date(2026, 2, 8, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		pod      PodState
		wantEmit bool
	}{
		{
			name: "pending past threshold emits",
			pod: PodState{
				Name: "stuck-pod", Namespace: "default", UID: "uid-1",
				Phase: "Pending", CreationTimestamp: now.Add(-20 * time.Minute),
			},
			wantEmit: true,
		},
		{
			name: "pending exactly at threshold emits",
			pod: PodState{
				Name: "exact-pod", Namespace: "default", UID: "uid-2",
				Phase: "Pending", CreationTimestamp: now.Add(-15 * time.Minute),
			},
			wantEmit: true,
		},
		{
			name: "pending below threshold does not emit",
			pod: PodState{
				Name: "new-pod", Namespace: "default", UID: "uid-3",
				Phase: "Pending", CreationTimestamp: now.Add(-5 * time.Minute),
			},
			wantEmit: false,
		},
		{
			name: "running pod does not emit",
			pod: PodState{
				Name: "running-pod", Namespace: "default", UID: "uid-4",
				Phase: "Running", CreationTimestamp: now.Add(-30 * time.Minute),
			},
			wantEmit: false,
		},
		{
			name: "succeeded pod does not emit",
			pod: PodState{
				Name: "done-pod", Namespace: "default", UID: "uid-5",
				Phase: "Succeeded", CreationTimestamp: now.Add(-30 * time.Minute),
			},
			wantEmit: false,
		},
		{
			name: "failed pod does not emit",
			pod: PodState{
				Name: "failed-pod", Namespace: "default", UID: "uid-6",
				Phase: "Failed", CreationTimestamp: now.Add(-30 * time.Minute),
			},
			wantEmit: false,
		},
		{
			name: "pending just barely under threshold",
			pod: PodState{
				Name: "almost-pod", Namespace: "default", UID: "uid-7",
				Phase: "Pending", CreationTimestamp: now.Add(-14*time.Minute - 59*time.Second),
			},
			wantEmit: false,
		},
		{
			name: "pending with labels propagated",
			pod: PodState{
				Name: "labeled-pod", Namespace: "default", UID: "uid-8",
				Phase: "Pending", CreationTimestamp: now.Add(-20 * time.Minute),
				Labels: map[string]string{"app": "web"},
			},
			wantEmit: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cb, getEvents := collectCallback()
			d, err := NewPodStuckPending(PodStuckPendingConfig{
				Threshold: 15 * time.Minute,
				Cooldown:  30 * time.Minute,
				Callback:  cb,
				Logger:    silentLogger(),
			})
			if err != nil {
				t.Fatalf("NewPodStuckPending() error = %v", err)
			}
			d.base.SetNowFunc(func() time.Time { return now })

			got := d.Check(tt.pod)
			if got != tt.wantEmit {
				t.Errorf("Check() = %v, want %v", got, tt.wantEmit)
			}

			events := getEvents()
			if tt.wantEmit && len(events) == 0 {
				t.Error("expected event to be emitted")
			}
			if !tt.wantEmit && len(events) > 0 {
				t.Errorf("expected no events, got %d", len(events))
			}

			if tt.wantEmit && len(events) > 0 {
				e := events[0]
				if e.DetectorName != "PodStuckPending" {
					t.Errorf("DetectorName = %q, want PodStuckPending", e.DetectorName)
				}
				if e.Severity != model.SeverityWarning {
					t.Errorf("Severity = %q, want warning", e.Severity)
				}
				if e.Resource.Kind != "Pod" {
					t.Errorf("Resource.Kind = %q, want Pod", e.Resource.Kind)
				}
				if e.Resource.Name != tt.pod.Name {
					t.Errorf("Resource.Name = %q, want %q", e.Resource.Name, tt.pod.Name)
				}
			}
		})
	}
}

func TestPodStuckPending_DefaultThreshold(t *testing.T) {
	cb, _ := collectCallback()
	d, err := NewPodStuckPending(PodStuckPendingConfig{Callback: cb, Logger: silentLogger()})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if d.Threshold() != 15*time.Minute {
		t.Errorf("default threshold = %v, want 15m", d.Threshold())
	}
}

func TestPodStuckPending_Interface(t *testing.T) {
	cb, _ := collectCallback()
	d, _ := NewPodStuckPending(PodStuckPendingConfig{Callback: cb, Logger: silentLogger()})

	var _ Detector = d // compile-time check
	if d.Name() != "PodStuckPending" {
		t.Errorf("Name() = %q", d.Name())
	}
	if d.Severity() != model.SeverityWarning {
		t.Errorf("Severity() = %q", d.Severity())
	}
	if d.IsLeaderOnly() {
		t.Error("should not be leader-only")
	}
}

func TestPodStuckPending_Cooldown(t *testing.T) {
	cb, getEvents := collectCallback()
	now := time.Date(2026, 2, 8, 12, 0, 0, 0, time.UTC)

	d, _ := NewPodStuckPending(PodStuckPendingConfig{
		Threshold: 15 * time.Minute,
		Cooldown:  30 * time.Minute,
		Callback:  cb,
		Logger:    silentLogger(),
	})
	d.base.SetNowFunc(func() time.Time { return now })

	pod := PodState{
		Name: "stuck", Namespace: "ns", UID: "uid-cool",
		Phase: "Pending", CreationTimestamp: now.Add(-20 * time.Minute),
	}

	// First check emits.
	d.Check(pod)
	// Second check within cooldown should not emit.
	d.Check(pod)

	if len(getEvents()) != 1 {
		t.Errorf("expected 1 event (cooldown should block second), got %d", len(getEvents()))
	}
}

// =====================================================================
// PodCrashLoop tests
// =====================================================================

func TestPodCrashLoop_Check(t *testing.T) {
	tests := []struct {
		name     string
		pod      PodContainerState
		wantEmit bool
	}{
		{
			name: "crashloopbackoff with restarts above threshold",
			pod: PodContainerState{
				Name: "crash-pod", Namespace: "default", UID: "uid-cl-1",
				ContainerStatuses: []ContainerStatus{
					{Name: "app", RestartCount: 5, Waiting: true, WaitingReason: "CrashLoopBackOff"},
				},
			},
			wantEmit: true,
		},
		{
			name: "crashloopbackoff at exactly threshold",
			pod: PodContainerState{
				Name: "exact-pod", Namespace: "default", UID: "uid-cl-2",
				ContainerStatuses: []ContainerStatus{
					{Name: "app", RestartCount: 3, Waiting: true, WaitingReason: "CrashLoopBackOff"},
				},
			},
			wantEmit: true,
		},
		{
			name: "crashloopbackoff below threshold",
			pod: PodContainerState{
				Name: "low-pod", Namespace: "default", UID: "uid-cl-3",
				ContainerStatuses: []ContainerStatus{
					{Name: "app", RestartCount: 2, Waiting: true, WaitingReason: "CrashLoopBackOff"},
				},
			},
			wantEmit: false,
		},
		{
			name: "high restarts but not in CrashLoopBackOff",
			pod: PodContainerState{
				Name: "restart-pod", Namespace: "default", UID: "uid-cl-4",
				ContainerStatuses: []ContainerStatus{
					{Name: "app", RestartCount: 10, Waiting: false, WaitingReason: ""},
				},
			},
			wantEmit: false,
		},
		{
			name: "waiting with different reason",
			pod: PodContainerState{
				Name: "other-pod", Namespace: "default", UID: "uid-cl-5",
				ContainerStatuses: []ContainerStatus{
					{Name: "app", RestartCount: 5, Waiting: true, WaitingReason: "ImagePullBackOff"},
				},
			},
			wantEmit: false,
		},
		{
			name: "no container statuses",
			pod: PodContainerState{
				Name: "empty-pod", Namespace: "default", UID: "uid-cl-6",
				ContainerStatuses: nil,
			},
			wantEmit: false,
		},
		{
			name: "init container crashlooping",
			pod: PodContainerState{
				Name: "init-pod", Namespace: "default", UID: "uid-cl-7",
				ContainerStatuses: []ContainerStatus{
					{Name: "app", RestartCount: 0, Waiting: false},
				},
				InitContainerStatuses: []ContainerStatus{
					{Name: "init", RestartCount: 5, Waiting: true, WaitingReason: "CrashLoopBackOff"},
				},
			},
			wantEmit: true,
		},
		{
			name: "multiple containers one crashlooping",
			pod: PodContainerState{
				Name: "multi-pod", Namespace: "default", UID: "uid-cl-8",
				ContainerStatuses: []ContainerStatus{
					{Name: "healthy", RestartCount: 0, Waiting: false},
					{Name: "unhealthy", RestartCount: 10, Waiting: true, WaitingReason: "CrashLoopBackOff"},
				},
			},
			wantEmit: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cb, getEvents := collectCallback()
			d, err := NewPodCrashLoop(PodCrashLoopConfig{
				Threshold: 3,
				Cooldown:  30 * time.Minute,
				Callback:  cb,
				Logger:    silentLogger(),
			})
			if err != nil {
				t.Fatalf("NewPodCrashLoop() error = %v", err)
			}

			got := d.Check(tt.pod)
			if got != tt.wantEmit {
				t.Errorf("Check() = %v, want %v", got, tt.wantEmit)
			}

			events := getEvents()
			if tt.wantEmit && len(events) == 0 {
				t.Error("expected event to be emitted")
			}
			if !tt.wantEmit && len(events) > 0 {
				t.Errorf("expected no events, got %d", len(events))
			}

			if tt.wantEmit && len(events) > 0 {
				e := events[0]
				if e.DetectorName != "PodCrashLoop" {
					t.Errorf("DetectorName = %q", e.DetectorName)
				}
				if e.Resource.Kind != "Pod" {
					t.Errorf("Resource.Kind = %q", e.Resource.Kind)
				}
			}
		})
	}
}

func TestPodCrashLoop_DefaultThreshold(t *testing.T) {
	cb, _ := collectCallback()
	d, err := NewPodCrashLoop(PodCrashLoopConfig{Callback: cb, Logger: silentLogger()})
	if err != nil {
		t.Fatalf("error: %v", err)
	}
	if d.Threshold() != 3 {
		t.Errorf("default threshold = %d, want 3", d.Threshold())
	}
}

func TestPodCrashLoop_Interface(t *testing.T) {
	cb, _ := collectCallback()
	d, _ := NewPodCrashLoop(PodCrashLoopConfig{Callback: cb, Logger: silentLogger()})

	var _ Detector = d
	if d.Name() != "PodCrashLoop" {
		t.Errorf("Name() = %q", d.Name())
	}
	if d.IsLeaderOnly() {
		t.Error("should not be leader-only")
	}
}

func TestPodCrashLoop_EventAnnotations(t *testing.T) {
	cb, getEvents := collectCallback()
	d, _ := NewPodCrashLoop(PodCrashLoopConfig{
		Threshold: 3, Cooldown: 30 * time.Minute,
		Callback: cb, Logger: silentLogger(),
	})

	d.Check(PodContainerState{
		Name: "pod", Namespace: "ns", UID: "uid",
		ContainerStatuses: []ContainerStatus{
			{Name: "myapp", RestartCount: 5, Waiting: true, WaitingReason: "CrashLoopBackOff"},
		},
	})

	events := getEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Annotations["container"] != "myapp" {
		t.Errorf("annotation container = %q, want myapp", events[0].Annotations["container"])
	}
	if events[0].Annotations["restartCount"] != "5" {
		t.Errorf("annotation restartCount = %q, want 5", events[0].Annotations["restartCount"])
	}
}

// =====================================================================
// PodFailed tests
// =====================================================================

func TestPodFailed_Check(t *testing.T) {
	tests := []struct {
		name     string
		pod      PodFailedState
		ignoreCodes []int32
		wantEmit bool
	}{
		{
			name: "failed phase emits",
			pod: PodFailedState{
				Name: "failed-pod", Namespace: "default", UID: "uid-f-1",
				Phase: "Failed",
			},
			wantEmit: true,
		},
		{
			name: "non-zero exit code emits",
			pod: PodFailedState{
				Name: "exit-pod", Namespace: "default", UID: "uid-f-2",
				Phase: "Running",
				Terminations: []ContainerTermination{
					{Name: "app", ExitCode: 1, Reason: "Error"},
				},
			},
			wantEmit: true,
		},
		{
			name: "exit code 137 (OOMKilled) emits",
			pod: PodFailedState{
				Name: "oom-pod", Namespace: "default", UID: "uid-f-3",
				Phase: "Running",
				Terminations: []ContainerTermination{
					{Name: "app", ExitCode: 137, Reason: "OOMKilled"},
				},
			},
			wantEmit: true,
		},
		{
			name: "exit code 0 ignored by default",
			pod: PodFailedState{
				Name: "ok-pod", Namespace: "default", UID: "uid-f-4",
				Phase: "Running",
				Terminations: []ContainerTermination{
					{Name: "app", ExitCode: 0, Reason: "Completed"},
				},
			},
			ignoreCodes: []int32{0},
			wantEmit:    false,
		},
		{
			name: "custom ignore exit code",
			pod: PodFailedState{
				Name: "custom-pod", Namespace: "default", UID: "uid-f-5",
				Phase: "Running",
				Terminations: []ContainerTermination{
					{Name: "app", ExitCode: 143, Reason: "SIGTERM"},
				},
			},
			ignoreCodes: []int32{0, 143},
			wantEmit:    false,
		},
		{
			name: "running pod with no terminations",
			pod: PodFailedState{
				Name: "healthy-pod", Namespace: "default", UID: "uid-f-6",
				Phase: "Running",
			},
			wantEmit: false,
		},
		{
			name: "succeeded pod does not emit",
			pod: PodFailedState{
				Name: "done-pod", Namespace: "default", UID: "uid-f-7",
				Phase: "Succeeded",
			},
			wantEmit: false,
		},
		{
			name: "pending pod does not emit",
			pod: PodFailedState{
				Name: "pending-pod", Namespace: "default", UID: "uid-f-8",
				Phase: "Pending",
			},
			wantEmit: false,
		},
		{
			name: "empty termination reason uses Unknown",
			pod: PodFailedState{
				Name: "no-reason-pod", Namespace: "default", UID: "uid-f-9",
				Phase: "Running",
				Terminations: []ContainerTermination{
					{Name: "app", ExitCode: 1, Reason: ""},
				},
			},
			wantEmit: true,
		},
		{
			name: "multiple terminations - first non-ignored triggers",
			pod: PodFailedState{
				Name: "multi-pod", Namespace: "default", UID: "uid-f-10",
				Phase: "Running",
				Terminations: []ContainerTermination{
					{Name: "sidecar", ExitCode: 0, Reason: "Completed"},
					{Name: "app", ExitCode: 1, Reason: "Error"},
				},
			},
			ignoreCodes: []int32{0},
			wantEmit:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cb, getEvents := collectCallback()
			d, err := NewPodFailed(PodFailedConfig{
				IgnoreExitCodes: tt.ignoreCodes,
				Cooldown:        30 * time.Minute,
				Callback:        cb,
				Logger:          silentLogger(),
			})
			if err != nil {
				t.Fatalf("NewPodFailed() error = %v", err)
			}

			got := d.Check(tt.pod)
			if got != tt.wantEmit {
				t.Errorf("Check() = %v, want %v", got, tt.wantEmit)
			}

			events := getEvents()
			if tt.wantEmit && len(events) == 0 {
				t.Error("expected event to be emitted")
			}
			if !tt.wantEmit && len(events) > 0 {
				t.Errorf("expected no events, got %d", len(events))
			}

			if tt.wantEmit && len(events) > 0 {
				e := events[0]
				if e.DetectorName != "PodFailed" {
					t.Errorf("DetectorName = %q", e.DetectorName)
				}
				if e.Resource.Kind != "Pod" {
					t.Errorf("Resource.Kind = %q", e.Resource.Kind)
				}
			}
		})
	}
}

func TestPodFailed_IgnoresExitCode(t *testing.T) {
	cb, _ := collectCallback()
	d, _ := NewPodFailed(PodFailedConfig{
		IgnoreExitCodes: []int32{0, 143},
		Callback:        cb,
		Logger:          silentLogger(),
	})

	if !d.IgnoresExitCode(0) {
		t.Error("should ignore exit code 0")
	}
	if !d.IgnoresExitCode(143) {
		t.Error("should ignore exit code 143")
	}
	if d.IgnoresExitCode(1) {
		t.Error("should not ignore exit code 1")
	}
}

func TestPodFailed_Interface(t *testing.T) {
	cb, _ := collectCallback()
	d, _ := NewPodFailed(PodFailedConfig{Callback: cb, Logger: silentLogger()})

	var _ Detector = d
	if d.Name() != "PodFailed" {
		t.Errorf("Name() = %q", d.Name())
	}
	if d.Severity() != model.SeverityWarning {
		t.Errorf("Severity() = %q", d.Severity())
	}
	if d.IsLeaderOnly() {
		t.Error("should not be leader-only")
	}
}

func TestPodFailed_EventAnnotationsOnTermination(t *testing.T) {
	cb, getEvents := collectCallback()
	d, _ := NewPodFailed(PodFailedConfig{
		Cooldown: 30 * time.Minute, Callback: cb, Logger: silentLogger(),
	})

	d.Check(PodFailedState{
		Name: "pod", Namespace: "ns", UID: "uid",
		Phase: "Running",
		Terminations: []ContainerTermination{
			{Name: "app", ExitCode: 137, Reason: "OOMKilled"},
		},
	})

	events := getEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Annotations["exitCode"] != "137" {
		t.Errorf("annotation exitCode = %q, want 137", events[0].Annotations["exitCode"])
	}
	if events[0].Annotations["reason"] != "OOMKilled" {
		t.Errorf("annotation reason = %q, want OOMKilled", events[0].Annotations["reason"])
	}
}

// =====================================================================
// NodeNotReady tests
// =====================================================================

func TestNodeNotReady_Check(t *testing.T) {
	now := time.Date(2026, 2, 8, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		node     NodeState
		wantEmit bool
	}{
		{
			name: "node not ready past threshold",
			node: NodeState{
				Name: "node-1", UID: "nuid-1",
				ReadyCondition:      NodeConditionFalse,
				ReadyTransitionTime: now.Add(-10 * time.Minute),
			},
			wantEmit: true,
		},
		{
			name: "node unknown past threshold",
			node: NodeState{
				Name: "node-2", UID: "nuid-2",
				ReadyCondition:      NodeConditionUnknown,
				ReadyTransitionTime: now.Add(-6 * time.Minute),
			},
			wantEmit: true,
		},
		{
			name: "node not ready below threshold",
			node: NodeState{
				Name: "node-3", UID: "nuid-3",
				ReadyCondition:      NodeConditionFalse,
				ReadyTransitionTime: now.Add(-3 * time.Minute),
			},
			wantEmit: false,
		},
		{
			name: "node ready does not emit",
			node: NodeState{
				Name: "node-4", UID: "nuid-4",
				ReadyCondition:      NodeConditionTrue,
				ReadyTransitionTime: now.Add(-30 * time.Minute),
			},
			wantEmit: false,
		},
		{
			name: "node not ready exactly at threshold",
			node: NodeState{
				Name: "node-5", UID: "nuid-5",
				ReadyCondition:      NodeConditionFalse,
				ReadyTransitionTime: now.Add(-5 * time.Minute),
			},
			wantEmit: true,
		},
		{
			name: "node unknown just under threshold",
			node: NodeState{
				Name: "node-6", UID: "nuid-6",
				ReadyCondition:      NodeConditionUnknown,
				ReadyTransitionTime: now.Add(-4*time.Minute - 59*time.Second),
			},
			wantEmit: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cb, getEvents := collectCallback()
			d, err := NewNodeNotReady(NodeNotReadyConfig{
				Threshold: 5 * time.Minute,
				Cooldown:  30 * time.Minute,
				Callback:  cb,
				Logger:    silentLogger(),
			})
			if err != nil {
				t.Fatalf("NewNodeNotReady() error = %v", err)
			}
			d.base.SetNowFunc(func() time.Time { return now })

			got := d.Check(tt.node)
			if got != tt.wantEmit {
				t.Errorf("Check() = %v, want %v", got, tt.wantEmit)
			}

			events := getEvents()
			if tt.wantEmit && len(events) == 0 {
				t.Error("expected event to be emitted")
			}
			if !tt.wantEmit && len(events) > 0 {
				t.Errorf("expected no events, got %d", len(events))
			}

			if tt.wantEmit && len(events) > 0 {
				e := events[0]
				if e.DetectorName != "NodeNotReady" {
					t.Errorf("DetectorName = %q", e.DetectorName)
				}
				if e.Severity != model.SeverityCritical {
					t.Errorf("Severity = %q, want critical", e.Severity)
				}
				if e.Resource.Kind != "Node" {
					t.Errorf("Resource.Kind = %q, want Node", e.Resource.Kind)
				}
				if e.Resource.Namespace != "" {
					t.Errorf("Resource.Namespace = %q, want empty (cluster-scoped)", e.Resource.Namespace)
				}
			}
		})
	}
}

func TestNodeNotReady_LeaderOnly(t *testing.T) {
	cb, _ := collectCallback()
	d, _ := NewNodeNotReady(NodeNotReadyConfig{Callback: cb, Logger: silentLogger()})

	if !d.IsLeaderOnly() {
		t.Error("NodeNotReady should be leader-only")
	}
}

func TestNodeNotReady_DefaultThreshold(t *testing.T) {
	cb, _ := collectCallback()
	d, _ := NewNodeNotReady(NodeNotReadyConfig{Callback: cb, Logger: silentLogger()})
	if d.Threshold() != 5*time.Minute {
		t.Errorf("default threshold = %v, want 5m", d.Threshold())
	}
}

func TestNodeNotReady_EventAnnotations(t *testing.T) {
	now := time.Date(2026, 2, 8, 12, 0, 0, 0, time.UTC)
	cb, getEvents := collectCallback()
	d, _ := NewNodeNotReady(NodeNotReadyConfig{
		Threshold: 5 * time.Minute, Cooldown: 30 * time.Minute,
		Callback: cb, Logger: silentLogger(),
	})
	d.base.SetNowFunc(func() time.Time { return now })

	d.Check(NodeState{
		Name: "node", UID: "uid",
		ReadyCondition:      NodeConditionFalse,
		ReadyTransitionTime: now.Add(-10 * time.Minute),
	})

	events := getEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Annotations["readyCondition"] != "False" {
		t.Errorf("annotation readyCondition = %q, want False", events[0].Annotations["readyCondition"])
	}
}

// =====================================================================
// PVCStuckBinding tests
// =====================================================================

func TestPVCStuckBinding_Check(t *testing.T) {
	now := time.Date(2026, 2, 8, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		pvc      PVCState
		wantEmit bool
	}{
		{
			name: "pending past threshold emits",
			pvc: PVCState{
				Name: "pvc-1", Namespace: "default", UID: "puid-1",
				Phase: "Pending", CreationTimestamp: now.Add(-15 * time.Minute),
			},
			wantEmit: true,
		},
		{
			name: "pending at exactly threshold",
			pvc: PVCState{
				Name: "pvc-2", Namespace: "default", UID: "puid-2",
				Phase: "Pending", CreationTimestamp: now.Add(-10 * time.Minute),
			},
			wantEmit: true,
		},
		{
			name: "pending below threshold",
			pvc: PVCState{
				Name: "pvc-3", Namespace: "default", UID: "puid-3",
				Phase: "Pending", CreationTimestamp: now.Add(-5 * time.Minute),
			},
			wantEmit: false,
		},
		{
			name: "bound PVC does not emit",
			pvc: PVCState{
				Name: "pvc-4", Namespace: "default", UID: "puid-4",
				Phase: "Bound", CreationTimestamp: now.Add(-30 * time.Minute),
			},
			wantEmit: false,
		},
		{
			name: "lost PVC does not emit",
			pvc: PVCState{
				Name: "pvc-5", Namespace: "default", UID: "puid-5",
				Phase: "Lost", CreationTimestamp: now.Add(-30 * time.Minute),
			},
			wantEmit: false,
		},
		{
			name: "pending with storage class annotation",
			pvc: PVCState{
				Name: "pvc-6", Namespace: "prod", UID: "puid-6",
				Phase: "Pending", CreationTimestamp: now.Add(-15 * time.Minute),
				StorageClassName: "gp3",
			},
			wantEmit: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cb, getEvents := collectCallback()
			d, err := NewPVCStuckBinding(PVCStuckBindingConfig{
				Threshold: 10 * time.Minute,
				Cooldown:  30 * time.Minute,
				Callback:  cb,
				Logger:    silentLogger(),
			})
			if err != nil {
				t.Fatalf("NewPVCStuckBinding() error = %v", err)
			}
			d.base.SetNowFunc(func() time.Time { return now })

			got := d.Check(tt.pvc)
			if got != tt.wantEmit {
				t.Errorf("Check() = %v, want %v", got, tt.wantEmit)
			}

			events := getEvents()
			if tt.wantEmit && len(events) == 0 {
				t.Error("expected event to be emitted")
			}
			if !tt.wantEmit && len(events) > 0 {
				t.Errorf("expected no events, got %d", len(events))
			}

			if tt.wantEmit && len(events) > 0 {
				e := events[0]
				if e.DetectorName != "PVCStuckBinding" {
					t.Errorf("DetectorName = %q", e.DetectorName)
				}
				if e.Resource.Kind != "PersistentVolumeClaim" {
					t.Errorf("Resource.Kind = %q, want PersistentVolumeClaim", e.Resource.Kind)
				}
			}
		})
	}
}

func TestPVCStuckBinding_StorageClassAnnotation(t *testing.T) {
	now := time.Date(2026, 2, 8, 12, 0, 0, 0, time.UTC)
	cb, getEvents := collectCallback()
	d, _ := NewPVCStuckBinding(PVCStuckBindingConfig{
		Threshold: 10 * time.Minute, Cooldown: 30 * time.Minute,
		Callback: cb, Logger: silentLogger(),
	})
	d.base.SetNowFunc(func() time.Time { return now })

	d.Check(PVCState{
		Name: "pvc", Namespace: "ns", UID: "uid",
		Phase: "Pending", CreationTimestamp: now.Add(-15 * time.Minute),
		StorageClassName: "gp3-encrypted",
	})

	events := getEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Annotations["storageClass"] != "gp3-encrypted" {
		t.Errorf("annotation storageClass = %q, want gp3-encrypted", events[0].Annotations["storageClass"])
	}
}

func TestPVCStuckBinding_DefaultThreshold(t *testing.T) {
	cb, _ := collectCallback()
	d, _ := NewPVCStuckBinding(PVCStuckBindingConfig{Callback: cb, Logger: silentLogger()})
	if d.Threshold() != 10*time.Minute {
		t.Errorf("default threshold = %v, want 10m", d.Threshold())
	}
}

func TestPVCStuckBinding_Interface(t *testing.T) {
	cb, _ := collectCallback()
	d, _ := NewPVCStuckBinding(PVCStuckBindingConfig{Callback: cb, Logger: silentLogger()})

	var _ Detector = d
	if d.IsLeaderOnly() {
		t.Error("should not be leader-only")
	}
}

// =====================================================================
// HighPodCount tests
// =====================================================================

func TestHighPodCount_Check(t *testing.T) {
	tests := []struct {
		name     string
		ns       NamespacePodCount
		wantEmit bool
	}{
		{
			name:     "above threshold emits",
			ns:       NamespacePodCount{Name: "busy-ns", UID: "ns-uid-1", PodCount: 250},
			wantEmit: true,
		},
		{
			name:     "exactly at threshold does not emit",
			ns:       NamespacePodCount{Name: "border-ns", UID: "ns-uid-2", PodCount: 200},
			wantEmit: false,
		},
		{
			name:     "below threshold does not emit",
			ns:       NamespacePodCount{Name: "quiet-ns", UID: "ns-uid-3", PodCount: 50},
			wantEmit: false,
		},
		{
			name:     "just above threshold",
			ns:       NamespacePodCount{Name: "edge-ns", UID: "ns-uid-4", PodCount: 201},
			wantEmit: true,
		},
		{
			name:     "zero pods",
			ns:       NamespacePodCount{Name: "empty-ns", UID: "ns-uid-5", PodCount: 0},
			wantEmit: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cb, getEvents := collectCallback()
			d, err := NewHighPodCount(HighPodCountConfig{
				Threshold: 200,
				Cooldown:  1 * time.Hour,
				Callback:  cb,
				Logger:    silentLogger(),
			})
			if err != nil {
				t.Fatalf("NewHighPodCount() error = %v", err)
			}

			got := d.Check(tt.ns)
			if got != tt.wantEmit {
				t.Errorf("Check() = %v, want %v", got, tt.wantEmit)
			}

			events := getEvents()
			if tt.wantEmit && len(events) == 0 {
				t.Error("expected event to be emitted")
			}
			if !tt.wantEmit && len(events) > 0 {
				t.Errorf("expected no events, got %d", len(events))
			}

			if tt.wantEmit && len(events) > 0 {
				e := events[0]
				if e.DetectorName != "HighPodCount" {
					t.Errorf("DetectorName = %q", e.DetectorName)
				}
				if e.Severity != model.SeverityInfo {
					t.Errorf("Severity = %q, want info", e.Severity)
				}
				if e.Resource.Kind != "Namespace" {
					t.Errorf("Resource.Kind = %q, want Namespace", e.Resource.Kind)
				}
			}
		})
	}
}

func TestHighPodCount_DefaultThreshold(t *testing.T) {
	cb, _ := collectCallback()
	d, _ := NewHighPodCount(HighPodCountConfig{Callback: cb, Logger: silentLogger()})
	if d.Threshold() != 200 {
		t.Errorf("default threshold = %d, want 200", d.Threshold())
	}
}

func TestHighPodCount_Interface(t *testing.T) {
	cb, _ := collectCallback()
	d, _ := NewHighPodCount(HighPodCountConfig{Callback: cb, Logger: silentLogger()})

	var _ Detector = d
	if d.IsLeaderOnly() {
		t.Error("should not be leader-only")
	}
}

func TestHighPodCount_EventAnnotations(t *testing.T) {
	cb, getEvents := collectCallback()
	d, _ := NewHighPodCount(HighPodCountConfig{
		Threshold: 100, Cooldown: 1 * time.Hour,
		Callback: cb, Logger: silentLogger(),
	})

	d.Check(NamespacePodCount{Name: "ns", UID: "uid", PodCount: 150})

	events := getEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Annotations["podCount"] != "150" {
		t.Errorf("annotation podCount = %q, want 150", events[0].Annotations["podCount"])
	}
	if events[0].Annotations["threshold"] != "100" {
		t.Errorf("annotation threshold = %q, want 100", events[0].Annotations["threshold"])
	}
}

// =====================================================================
// JobDeadlineExceeded tests
// =====================================================================

func TestJobDeadlineExceeded_Check(t *testing.T) {
	now := time.Date(2026, 2, 8, 12, 0, 0, 0, time.UTC)
	deadline := int64(300) // 5 minutes
	longDeadline := int64(3600)

	tests := []struct {
		name     string
		job      JobState
		wantEmit bool
	}{
		{
			name: "deadline exceeded via condition",
			job: JobState{
				Name: "job-1", Namespace: "default", UID: "juid-1",
				ConditionDeadlineExceeded: true,
			},
			wantEmit: true,
		},
		{
			name: "deadline exceeded via timing",
			job: JobState{
				Name: "job-2", Namespace: "default", UID: "juid-2",
				ActiveDeadlineSeconds: &deadline,
				StartTime:             timePtr(now.Add(-10 * time.Minute)),
			},
			wantEmit: true,
		},
		{
			name: "deadline not yet exceeded",
			job: JobState{
				Name: "job-3", Namespace: "default", UID: "juid-3",
				ActiveDeadlineSeconds: &longDeadline,
				StartTime:             timePtr(now.Add(-10 * time.Minute)),
			},
			wantEmit: false,
		},
		{
			name: "completed job does not emit even with condition",
			job: JobState{
				Name: "job-4", Namespace: "default", UID: "juid-4",
				Completed:                 true,
				ConditionDeadlineExceeded: true,
			},
			wantEmit: false,
		},
		{
			name: "no deadline set",
			job: JobState{
				Name: "job-5", Namespace: "default", UID: "juid-5",
				StartTime: timePtr(now.Add(-1 * time.Hour)),
			},
			wantEmit: false,
		},
		{
			name: "not started yet",
			job: JobState{
				Name: "job-6", Namespace: "default", UID: "juid-6",
				ActiveDeadlineSeconds: &deadline,
			},
			wantEmit: false,
		},
		{
			name: "deadline exactly at boundary",
			job: JobState{
				Name: "job-7", Namespace: "default", UID: "juid-7",
				ActiveDeadlineSeconds: &deadline,
				StartTime:             timePtr(now.Add(-5 * time.Minute)),
			},
			wantEmit: true,
		},
		{
			name: "failed job with deadline exceeded still emits",
			job: JobState{
				Name: "job-8", Namespace: "default", UID: "juid-8",
				Failed:                    true,
				ConditionDeadlineExceeded: true,
			},
			wantEmit: true,
		},
		{
			name: "deadline just barely not exceeded",
			job: JobState{
				Name: "job-9", Namespace: "default", UID: "juid-9",
				ActiveDeadlineSeconds: &deadline,
				StartTime:             timePtr(now.Add(-4*time.Minute - 59*time.Second)),
			},
			wantEmit: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cb, getEvents := collectCallback()
			d, err := NewJobDeadlineExceeded(JobDeadlineExceededConfig{
				Cooldown: 30 * time.Minute,
				Callback: cb,
				Logger:   silentLogger(),
			})
			if err != nil {
				t.Fatalf("NewJobDeadlineExceeded() error = %v", err)
			}
			d.base.SetNowFunc(func() time.Time { return now })

			got := d.Check(tt.job)
			if got != tt.wantEmit {
				t.Errorf("Check() = %v, want %v", got, tt.wantEmit)
			}

			events := getEvents()
			if tt.wantEmit && len(events) == 0 {
				t.Error("expected event to be emitted")
			}
			if !tt.wantEmit && len(events) > 0 {
				t.Errorf("expected no events, got %d", len(events))
			}

			if tt.wantEmit && len(events) > 0 {
				e := events[0]
				if e.DetectorName != "JobDeadlineExceeded" {
					t.Errorf("DetectorName = %q", e.DetectorName)
				}
				if e.Severity != model.SeverityWarning {
					t.Errorf("Severity = %q, want warning", e.Severity)
				}
				if e.Resource.Kind != "Job" {
					t.Errorf("Resource.Kind = %q, want Job", e.Resource.Kind)
				}
			}
		})
	}
}

func TestJobDeadlineExceeded_Interface(t *testing.T) {
	cb, _ := collectCallback()
	d, _ := NewJobDeadlineExceeded(JobDeadlineExceededConfig{Callback: cb, Logger: silentLogger()})

	var _ Detector = d
	if d.Name() != "JobDeadlineExceeded" {
		t.Errorf("Name() = %q", d.Name())
	}
	if d.IsLeaderOnly() {
		t.Error("should not be leader-only")
	}
}

func TestJobDeadlineExceeded_EventAnnotations(t *testing.T) {
	now := time.Date(2026, 2, 8, 12, 0, 0, 0, time.UTC)
	deadline := int64(300)
	cb, getEvents := collectCallback()
	d, _ := NewJobDeadlineExceeded(JobDeadlineExceededConfig{
		Cooldown: 30 * time.Minute, Callback: cb, Logger: silentLogger(),
	})
	d.base.SetNowFunc(func() time.Time { return now })

	d.Check(JobState{
		Name: "job", Namespace: "ns", UID: "uid",
		ActiveDeadlineSeconds: &deadline,
		StartTime:             timePtr(now.Add(-10 * time.Minute)),
	})

	events := getEvents()
	if len(events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events))
	}
	if events[0].Annotations["activeDeadlineSeconds"] != "300" {
		t.Errorf("annotation = %q, want 300", events[0].Annotations["activeDeadlineSeconds"])
	}
}

// =====================================================================
// Constructor validation tests for all detectors
// =====================================================================

func TestDetectorConstructors_NilCallback(t *testing.T) {
	tests := []struct {
		name    string
		newFunc func() error
	}{
		{
			name: "PodStuckPending",
			newFunc: func() error {
				_, err := NewPodStuckPending(PodStuckPendingConfig{Callback: nil})
				return err
			},
		},
		{
			name: "PodCrashLoop",
			newFunc: func() error {
				_, err := NewPodCrashLoop(PodCrashLoopConfig{Callback: nil})
				return err
			},
		},
		{
			name: "PodFailed",
			newFunc: func() error {
				_, err := NewPodFailed(PodFailedConfig{Callback: nil})
				return err
			},
		},
		{
			name: "NodeNotReady",
			newFunc: func() error {
				_, err := NewNodeNotReady(NodeNotReadyConfig{Callback: nil})
				return err
			},
		},
		{
			name: "PVCStuckBinding",
			newFunc: func() error {
				_, err := NewPVCStuckBinding(PVCStuckBindingConfig{Callback: nil})
				return err
			},
		},
		{
			name: "HighPodCount",
			newFunc: func() error {
				_, err := NewHighPodCount(HighPodCountConfig{Callback: nil})
				return err
			},
		},
		{
			name: "JobDeadlineExceeded",
			newFunc: func() error {
				_, err := NewJobDeadlineExceeded(JobDeadlineExceededConfig{Callback: nil})
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.newFunc()
			if err == nil {
				t.Fatal("expected error with nil callback")
			}
			if !containsStr(err.Error(), "callback must not be nil") {
				t.Errorf("error = %q, want substring about nil callback", err.Error())
			}
		})
	}
}

// =====================================================================
// Disabled detector pattern tests
// =====================================================================

// TestDisabledDetectors_DontFire verifies the pattern used by the controller:
// when a detector is disabled in config, it is simply not constructed, so no
// events can be emitted. This test documents that contract.
func TestDisabledDetectors_DontFire(t *testing.T) {
	type detectorCfg struct {
		name    string
		enabled bool
	}

	configs := []detectorCfg{
		{name: "PodStuckPending", enabled: true},
		{name: "PodCrashLoop", enabled: false},
		{name: "NodeNotReady", enabled: false},
	}

	registry := NewRegistry(silentLogger())
	cb, getEvents := collectCallback()
	now := time.Date(2026, 2, 8, 12, 0, 0, 0, time.UTC)

	for _, cfg := range configs {
		if !cfg.enabled {
			continue // disabled detectors are not created
		}
		switch cfg.name {
		case "PodStuckPending":
			d, err := NewPodStuckPending(PodStuckPendingConfig{
				Threshold: 15 * time.Minute,
				Cooldown:  30 * time.Minute,
				Callback:  cb,
				Logger:    silentLogger(),
			})
			if err != nil {
				t.Fatalf("error creating %s: %v", cfg.name, err)
			}
			d.base.SetNowFunc(func() time.Time { return now })
			if err := registry.Register(d); err != nil {
				t.Fatalf("error registering %s: %v", cfg.name, err)
			}
		}
	}

	// Only PodStuckPending should be registered.
	if registry.Count() != 1 {
		t.Fatalf("expected 1 detector registered, got %d", registry.Count())
	}

	// Verify that only PodStuckPending is active.
	if registry.Get("PodStuckPending") == nil {
		t.Error("PodStuckPending should be registered")
	}
	if registry.Get("PodCrashLoop") != nil {
		t.Error("PodCrashLoop should NOT be registered (disabled)")
	}
	if registry.Get("NodeNotReady") != nil {
		t.Error("NodeNotReady should NOT be registered (disabled)")
	}

	// The disabled detectors cannot fire because they don't exist.
	events := getEvents()
	if len(events) != 0 {
		t.Errorf("expected no events from disabled detectors, got %d", len(events))
	}
}

// =====================================================================
// Cooldown tests across all detectors
// =====================================================================

func TestPodCrashLoop_Cooldown(t *testing.T) {
	cb, getEvents := collectCallback()
	d, _ := NewPodCrashLoop(PodCrashLoopConfig{
		Threshold: 3,
		Cooldown:  30 * time.Minute,
		Callback:  cb,
		Logger:    silentLogger(),
	})

	pod := PodContainerState{
		Name: "pod", Namespace: "ns", UID: "uid-cool",
		ContainerStatuses: []ContainerStatus{
			{Name: "app", RestartCount: 5, Waiting: true, WaitingReason: "CrashLoopBackOff"},
		},
	}

	// First check emits.
	if !d.Check(pod) {
		t.Error("first check should emit")
	}
	// Second check within cooldown should be suppressed.
	if d.Check(pod) {
		t.Error("second check within cooldown should be suppressed")
	}

	events := getEvents()
	if len(events) != 1 {
		t.Errorf("expected 1 event, got %d", len(events))
	}
}

func TestNodeNotReady_Cooldown(t *testing.T) {
	now := time.Date(2026, 2, 8, 12, 0, 0, 0, time.UTC)
	cb, getEvents := collectCallback()
	d, _ := NewNodeNotReady(NodeNotReadyConfig{
		Threshold: 5 * time.Minute,
		Cooldown:  30 * time.Minute,
		Callback:  cb,
		Logger:    silentLogger(),
	})
	d.base.SetNowFunc(func() time.Time { return now })

	node := NodeState{
		Name: "node", UID: "uid-cool",
		ReadyCondition:      NodeConditionFalse,
		ReadyTransitionTime: now.Add(-10 * time.Minute),
	}

	d.Check(node)
	d.Check(node)

	if len(getEvents()) != 1 {
		t.Errorf("expected 1 event (cooldown should suppress second), got %d", len(getEvents()))
	}
}

func TestPVCStuckBinding_Cooldown(t *testing.T) {
	now := time.Date(2026, 2, 8, 12, 0, 0, 0, time.UTC)
	cb, getEvents := collectCallback()
	d, _ := NewPVCStuckBinding(PVCStuckBindingConfig{
		Threshold: 10 * time.Minute,
		Cooldown:  30 * time.Minute,
		Callback:  cb,
		Logger:    silentLogger(),
	})
	d.base.SetNowFunc(func() time.Time { return now })

	pvc := PVCState{
		Name: "pvc", Namespace: "ns", UID: "uid-cool",
		Phase: "Pending", CreationTimestamp: now.Add(-15 * time.Minute),
	}

	d.Check(pvc)
	d.Check(pvc)

	if len(getEvents()) != 1 {
		t.Errorf("expected 1 event (cooldown should suppress second), got %d", len(getEvents()))
	}
}

func TestHighPodCount_Cooldown(t *testing.T) {
	cb, getEvents := collectCallback()
	d, _ := NewHighPodCount(HighPodCountConfig{
		Threshold: 100,
		Cooldown:  1 * time.Hour,
		Callback:  cb,
		Logger:    silentLogger(),
	})

	ns := NamespacePodCount{Name: "ns", UID: "uid-cool", PodCount: 150}

	d.Check(ns)
	d.Check(ns)

	if len(getEvents()) != 1 {
		t.Errorf("expected 1 event (cooldown should suppress second), got %d", len(getEvents()))
	}
}

func TestJobDeadlineExceeded_Cooldown(t *testing.T) {
	now := time.Date(2026, 2, 8, 12, 0, 0, 0, time.UTC)
	cb, getEvents := collectCallback()
	d, _ := NewJobDeadlineExceeded(JobDeadlineExceededConfig{
		Cooldown: 30 * time.Minute,
		Callback: cb,
		Logger:   silentLogger(),
	})
	d.base.SetNowFunc(func() time.Time { return now })

	job := JobState{
		Name: "job", Namespace: "ns", UID: "uid-cool",
		ConditionDeadlineExceeded: true,
	}

	d.Check(job)
	d.Check(job)

	if len(getEvents()) != 1 {
		t.Errorf("expected 1 event (cooldown should suppress second), got %d", len(getEvents()))
	}
}

func TestPodFailed_Cooldown(t *testing.T) {
	cb, getEvents := collectCallback()
	d, _ := NewPodFailed(PodFailedConfig{
		Cooldown: 30 * time.Minute,
		Callback: cb,
		Logger:   silentLogger(),
	})

	pod := PodFailedState{
		Name: "pod", Namespace: "ns", UID: "uid-cool",
		Phase: "Failed",
	}

	d.Check(pod)
	d.Check(pod)

	if len(getEvents()) != 1 {
		t.Errorf("expected 1 event (cooldown should suppress second), got %d", len(getEvents()))
	}
}

// =====================================================================
// Input slice safety test for PodCrashLoop
// =====================================================================

func TestPodCrashLoop_DoesNotMutateInputSlice(t *testing.T) {
	cb, _ := collectCallback()
	d, _ := NewPodCrashLoop(PodCrashLoopConfig{
		Threshold: 3,
		Cooldown:  30 * time.Minute,
		Callback:  cb,
		Logger:    silentLogger(),
	})

	containers := []ContainerStatus{
		{Name: "app", RestartCount: 5, Waiting: true, WaitingReason: "CrashLoopBackOff"},
	}
	initContainers := []ContainerStatus{
		{Name: "init", RestartCount: 0, Waiting: false},
	}

	// Make a copy to compare after Check.
	origLen := len(containers)

	pod := PodContainerState{
		Name:                  "pod",
		Namespace:             "ns",
		UID:                   "uid",
		ContainerStatuses:     containers,
		InitContainerStatuses: initContainers,
	}

	d.Check(pod)

	if len(containers) != origLen {
		t.Errorf("Check() mutated input ContainerStatuses slice: len changed from %d to %d", origLen, len(containers))
	}
}

// =====================================================================
// PodFailed exit code filtering edge cases
// =====================================================================

func TestPodFailed_AllExitCodesIgnored(t *testing.T) {
	cb, getEvents := collectCallback()
	d, _ := NewPodFailed(PodFailedConfig{
		IgnoreExitCodes: []int32{0, 1, 137},
		Cooldown:        30 * time.Minute,
		Callback:        cb,
		Logger:          silentLogger(),
	})

	// A pod with multiple terminations, all with ignored exit codes.
	pod := PodFailedState{
		Name: "pod", Namespace: "ns", UID: "uid",
		Phase: "Running",
		Terminations: []ContainerTermination{
			{Name: "app", ExitCode: 0, Reason: "Completed"},
			{Name: "sidecar", ExitCode: 1, Reason: "Error"},
			{Name: "init", ExitCode: 137, Reason: "OOMKilled"},
		},
	}

	if d.Check(pod) {
		t.Error("should not emit when all exit codes are ignored")
	}
	if len(getEvents()) != 0 {
		t.Errorf("expected 0 events, got %d", len(getEvents()))
	}
}

func TestPodFailed_EmptyIgnoreList(t *testing.T) {
	cb, getEvents := collectCallback()
	d, _ := NewPodFailed(PodFailedConfig{
		IgnoreExitCodes: nil, // no codes ignored
		Cooldown:        30 * time.Minute,
		Callback:        cb,
		Logger:          silentLogger(),
	})

	// Even exit code 0 should trigger when not in the ignore list.
	pod := PodFailedState{
		Name: "pod", Namespace: "ns", UID: "uid",
		Phase: "Running",
		Terminations: []ContainerTermination{
			{Name: "app", ExitCode: 0, Reason: "Completed"},
		},
	}

	if !d.Check(pod) {
		t.Error("should emit when ignore list is empty (exit code 0 not ignored)")
	}
	if len(getEvents()) != 1 {
		t.Errorf("expected 1 event, got %d", len(getEvents()))
	}
}

func TestPodFailed_FailedPhaseWithIgnoredExitCodes(t *testing.T) {
	cb, getEvents := collectCallback()
	d, _ := NewPodFailed(PodFailedConfig{
		IgnoreExitCodes: []int32{0},
		Cooldown:        30 * time.Minute,
		Callback:        cb,
		Logger:          silentLogger(),
	})

	// Pod phase is Failed but only has exit code 0 terminations.
	// The Failed phase itself should still trigger, regardless of exit codes.
	pod := PodFailedState{
		Name: "pod", Namespace: "ns", UID: "uid",
		Phase: "Failed",
		Terminations: []ContainerTermination{
			{Name: "app", ExitCode: 0, Reason: "Completed"},
		},
	}

	if !d.Check(pod) {
		t.Error("should emit for Failed phase regardless of exit code ignore list")
	}
	if len(getEvents()) != 1 {
		t.Errorf("expected 1 event, got %d", len(getEvents()))
	}
}

// =====================================================================
// Severity verification tests
// =====================================================================

func TestDetector_Severities(t *testing.T) {
	cb, _ := collectCallback()

	tests := []struct {
		name     string
		severity model.Severity
		newFunc  func() (Detector, error)
	}{
		{
			name:     "PodStuckPending is warning",
			severity: model.SeverityWarning,
			newFunc: func() (Detector, error) {
				return NewPodStuckPending(PodStuckPendingConfig{Callback: cb, Logger: silentLogger()})
			},
		},
		{
			name:     "PodCrashLoop is warning",
			severity: model.SeverityWarning,
			newFunc: func() (Detector, error) {
				return NewPodCrashLoop(PodCrashLoopConfig{Callback: cb, Logger: silentLogger()})
			},
		},
		{
			name:     "PodFailed is warning",
			severity: model.SeverityWarning,
			newFunc: func() (Detector, error) {
				return NewPodFailed(PodFailedConfig{Callback: cb, Logger: silentLogger()})
			},
		},
		{
			name:     "NodeNotReady is critical",
			severity: model.SeverityCritical,
			newFunc: func() (Detector, error) {
				return NewNodeNotReady(NodeNotReadyConfig{Callback: cb, Logger: silentLogger()})
			},
		},
		{
			name:     "PVCStuckBinding is warning",
			severity: model.SeverityWarning,
			newFunc: func() (Detector, error) {
				return NewPVCStuckBinding(PVCStuckBindingConfig{Callback: cb, Logger: silentLogger()})
			},
		},
		{
			name:     "HighPodCount is info",
			severity: model.SeverityInfo,
			newFunc: func() (Detector, error) {
				return NewHighPodCount(HighPodCountConfig{Callback: cb, Logger: silentLogger()})
			},
		},
		{
			name:     "JobDeadlineExceeded is warning",
			severity: model.SeverityWarning,
			newFunc: func() (Detector, error) {
				return NewJobDeadlineExceeded(JobDeadlineExceededConfig{Callback: cb, Logger: silentLogger()})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := tt.newFunc()
			if err != nil {
				t.Fatalf("constructor error: %v", err)
			}
			if d.Severity() != tt.severity {
				t.Errorf("Severity() = %q, want %q", d.Severity(), tt.severity)
			}
		})
	}
}

// =====================================================================
// Leader-only verification tests
// =====================================================================

func TestDetector_LeaderOnly(t *testing.T) {
	cb, _ := collectCallback()

	tests := []struct {
		name       string
		leaderOnly bool
		newFunc    func() (Detector, error)
	}{
		{
			name:       "PodStuckPending is not leader-only",
			leaderOnly: false,
			newFunc: func() (Detector, error) {
				return NewPodStuckPending(PodStuckPendingConfig{Callback: cb, Logger: silentLogger()})
			},
		},
		{
			name:       "PodCrashLoop is not leader-only",
			leaderOnly: false,
			newFunc: func() (Detector, error) {
				return NewPodCrashLoop(PodCrashLoopConfig{Callback: cb, Logger: silentLogger()})
			},
		},
		{
			name:       "PodFailed is not leader-only",
			leaderOnly: false,
			newFunc: func() (Detector, error) {
				return NewPodFailed(PodFailedConfig{Callback: cb, Logger: silentLogger()})
			},
		},
		{
			name:       "NodeNotReady IS leader-only",
			leaderOnly: true,
			newFunc: func() (Detector, error) {
				return NewNodeNotReady(NodeNotReadyConfig{Callback: cb, Logger: silentLogger()})
			},
		},
		{
			name:       "PVCStuckBinding is not leader-only",
			leaderOnly: false,
			newFunc: func() (Detector, error) {
				return NewPVCStuckBinding(PVCStuckBindingConfig{Callback: cb, Logger: silentLogger()})
			},
		},
		{
			name:       "HighPodCount is not leader-only",
			leaderOnly: false,
			newFunc: func() (Detector, error) {
				return NewHighPodCount(HighPodCountConfig{Callback: cb, Logger: silentLogger()})
			},
		},
		{
			name:       "JobDeadlineExceeded is not leader-only",
			leaderOnly: false,
			newFunc: func() (Detector, error) {
				return NewJobDeadlineExceeded(JobDeadlineExceededConfig{Callback: cb, Logger: silentLogger()})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := tt.newFunc()
			if err != nil {
				t.Fatalf("constructor error: %v", err)
			}
			if d.IsLeaderOnly() != tt.leaderOnly {
				t.Errorf("IsLeaderOnly() = %v, want %v", d.IsLeaderOnly(), tt.leaderOnly)
			}
		})
	}
}

// =====================================================================
// Helpers
// =====================================================================

func timePtr(t time.Time) *time.Time {
	return &t
}
