package gatherer

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
	"github.com/k8s-wormsign/k8s-wormsign/internal/redact"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// testLogger returns a silent logger for testing.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(&discardWriter{}, &slog.HandlerOptions{Level: slog.LevelError}))
}

// testRedactor returns a redactor for testing with default patterns only.
func testRedactor() *redact.Redactor {
	r, err := redact.New(nil, redact.WithLogger(testLogger()))
	if err != nil {
		panic("failed to create test redactor: " + err.Error())
	}
	return r
}

// fakeKubeClient implements KubeClient for testing.
type fakeKubeClient struct {
	pods         map[string]*corev1.Pod
	nodes        map[string]*corev1.Node
	pvcs         map[string]*corev1.PersistentVolumeClaim
	pvs          map[string]*corev1.PersistentVolume
	events       map[string]*corev1.EventList
	logs         map[string]string
	replicaSets  map[string]*appsv1.ReplicaSet
	deployments  map[string]*appsv1.Deployment
	statefulSets map[string]*appsv1.StatefulSet
	daemonSets   map[string]*appsv1.DaemonSet
	jobs         map[string]*batchv1.Job

	// logErrors simulates errors fetching logs for specific containers
	logErrors map[string]error
}

func newFakeKubeClient() *fakeKubeClient {
	return &fakeKubeClient{
		pods:         make(map[string]*corev1.Pod),
		nodes:        make(map[string]*corev1.Node),
		pvcs:         make(map[string]*corev1.PersistentVolumeClaim),
		pvs:          make(map[string]*corev1.PersistentVolume),
		events:       make(map[string]*corev1.EventList),
		logs:         make(map[string]string),
		replicaSets:  make(map[string]*appsv1.ReplicaSet),
		deployments:  make(map[string]*appsv1.Deployment),
		statefulSets: make(map[string]*appsv1.StatefulSet),
		daemonSets:   make(map[string]*appsv1.DaemonSet),
		jobs:         make(map[string]*batchv1.Job),
		logErrors:    make(map[string]error),
	}
}

func (f *fakeKubeClient) GetPod(ctx context.Context, namespace, name string) (*corev1.Pod, error) {
	key := namespace + "/" + name
	pod, ok := f.pods[key]
	if !ok {
		return nil, fmt.Errorf("pod %q not found", key)
	}
	return pod, nil
}

func (f *fakeKubeClient) GetNode(ctx context.Context, name string) (*corev1.Node, error) {
	node, ok := f.nodes[name]
	if !ok {
		return nil, fmt.Errorf("node %q not found", name)
	}
	return node, nil
}

func (f *fakeKubeClient) GetPVC(ctx context.Context, namespace, name string) (*corev1.PersistentVolumeClaim, error) {
	key := namespace + "/" + name
	pvc, ok := f.pvcs[key]
	if !ok {
		return nil, fmt.Errorf("PVC %q not found", key)
	}
	return pvc, nil
}

func (f *fakeKubeClient) GetPV(ctx context.Context, name string) (*corev1.PersistentVolume, error) {
	pv, ok := f.pvs[name]
	if !ok {
		return nil, fmt.Errorf("PV %q not found", name)
	}
	return pv, nil
}

func (f *fakeKubeClient) ListEvents(ctx context.Context, namespace string, opts metav1.ListOptions) (*corev1.EventList, error) {
	// Build key from namespace + field selector for more precise matching
	key := namespace
	if opts.FieldSelector != "" {
		key = namespace + "?" + opts.FieldSelector
	}
	events, ok := f.events[key]
	if !ok {
		// Fall back to namespace-only key
		events, ok = f.events[namespace]
		if !ok {
			return &corev1.EventList{}, nil
		}
	}
	return events, nil
}

func (f *fakeKubeClient) GetPodLogs(ctx context.Context, namespace, podName, container string, tailLines *int64, previous bool) (string, error) {
	key := fmt.Sprintf("%s/%s/%s", namespace, podName, container)
	if previous {
		key += "/previous"
	}
	if err, ok := f.logErrors[key]; ok {
		return "", err
	}
	logs, ok := f.logs[key]
	if !ok {
		return "", nil
	}
	return logs, nil
}

func (f *fakeKubeClient) GetReplicaSet(ctx context.Context, namespace, name string) (*appsv1.ReplicaSet, error) {
	key := namespace + "/" + name
	rs, ok := f.replicaSets[key]
	if !ok {
		return nil, fmt.Errorf("ReplicaSet %q not found", key)
	}
	return rs, nil
}

func (f *fakeKubeClient) GetDeployment(ctx context.Context, namespace, name string) (*appsv1.Deployment, error) {
	key := namespace + "/" + name
	d, ok := f.deployments[key]
	if !ok {
		return nil, fmt.Errorf("Deployment %q not found", key)
	}
	return d, nil
}

func (f *fakeKubeClient) GetStatefulSet(ctx context.Context, namespace, name string) (*appsv1.StatefulSet, error) {
	key := namespace + "/" + name
	s, ok := f.statefulSets[key]
	if !ok {
		return nil, fmt.Errorf("StatefulSet %q not found", key)
	}
	return s, nil
}

func (f *fakeKubeClient) GetDaemonSet(ctx context.Context, namespace, name string) (*appsv1.DaemonSet, error) {
	key := namespace + "/" + name
	d, ok := f.daemonSets[key]
	if !ok {
		return nil, fmt.Errorf("DaemonSet %q not found", key)
	}
	return d, nil
}

func (f *fakeKubeClient) GetJob(ctx context.Context, namespace, name string) (*batchv1.Job, error) {
	key := namespace + "/" + name
	j, ok := f.jobs[key]
	if !ok {
		return nil, fmt.Errorf("Job %q not found", key)
	}
	return j, nil
}

// fakeKarpenterFetcher implements KarpenterFetcher for testing.
type fakeKarpenterFetcher struct {
	nodePools  string
	nodeClaims string
	npError    error
	ncError    error
}

func (f *fakeKarpenterFetcher) ListNodePools(ctx context.Context) (string, error) {
	if f.npError != nil {
		return "", f.npError
	}
	return f.nodePools, nil
}

func (f *fakeKarpenterFetcher) ListNodeClaims(ctx context.Context) (string, error) {
	if f.ncError != nil {
		return "", f.ncError
	}
	return f.nodeClaims, nil
}

// --- Helpers ---

func boolPtr(b bool) *bool          { return &b }
func int32Ptr(i int32) *int32       { return &i }
func int64Ptr(i int64) *int64       { return &i }
func strPtr(s string) *string       { return &s }

func podRef(ns, name string) model.ResourceRef {
	return model.ResourceRef{Kind: "Pod", Namespace: ns, Name: name, UID: "pod-uid-1"}
}

func nodeRef(name string) model.ResourceRef {
	return model.ResourceRef{Kind: "Node", Name: name, UID: "node-uid-1"}
}

func pvcRef(ns, name string) model.ResourceRef {
	return model.ResourceRef{Kind: "PersistentVolumeClaim", Namespace: ns, Name: name, UID: "pvc-uid-1"}
}

// --- PodDescribe Tests ---

func TestPodDescribe_Name(t *testing.T) {
	g := NewPodDescribe(newFakeKubeClient(), testRedactor(), testLogger())
	if g.Name() != "PodDescribe" {
		t.Errorf("expected name PodDescribe, got %s", g.Name())
	}
}

func TestPodDescribe_TriggerMatcher(t *testing.T) {
	g := NewPodDescribe(newFakeKubeClient(), testRedactor(), testLogger())
	if kinds := g.ResourceKinds(); len(kinds) != 1 || kinds[0] != "Pod" {
		t.Errorf("expected ResourceKinds [Pod], got %v", kinds)
	}
	if detectors := g.DetectorNames(); detectors != nil {
		t.Errorf("expected nil DetectorNames, got %v", detectors)
	}
}

func TestPodDescribe_BasicPod(t *testing.T) {
	client := newFakeKubeClient()
	now := metav1.Now()
	client.pods["default/test-pod"] = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-pod",
			Namespace: "default",
			UID:       types.UID("pod-uid-123"),
		},
		Spec: corev1.PodSpec{
			NodeName:           "node-1",
			ServiceAccountName: "default",
			Containers: []corev1.Container{
				{
					Name:  "app",
					Image: "nginx:1.25",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("100m"),
							corev1.ResourceMemory: resource.MustParse("128Mi"),
						},
						Limits: corev1.ResourceList{
							corev1.ResourceCPU:    resource.MustParse("500m"),
							corev1.ResourceMemory: resource.MustParse("512Mi"),
						},
					},
				},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: corev1.ConditionTrue},
			},
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name:         "app",
					Ready:        true,
					RestartCount: 0,
					State: corev1.ContainerState{
						Running: &corev1.ContainerStateRunning{StartedAt: now},
					},
				},
			},
		},
	}

	g := NewPodDescribe(client, testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), podRef("default", "test-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if section.GathererName != "PodDescribe" {
		t.Errorf("expected GathererName PodDescribe, got %s", section.GathererName)
	}
	if section.Format != "text" {
		t.Errorf("expected format text, got %s", section.Format)
	}

	// Check that key info is present
	for _, expected := range []string{
		"Pod: default/test-pod",
		"Phase: Running",
		"Node: node-1",
		"Container: app",
		"Image: nginx:1.25",
		"Requests: cpu=100m",
		"Limits: cpu=500m",
		"Ready: True",
		"ready=true, restartCount=0",
	} {
		if !strings.Contains(section.Content, expected) {
			t.Errorf("expected content to contain %q, got:\n%s", expected, section.Content)
		}
	}
}

func TestPodDescribe_CrashLoopBackOff(t *testing.T) {
	client := newFakeKubeClient()
	client.pods["default/crash-pod"] = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "crash-pod",
			Namespace: "default",
			UID:       types.UID("crash-uid"),
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "app", Image: "myapp:bad"},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name:         "app",
					Ready:        false,
					RestartCount: 5,
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{
							Reason:  "CrashLoopBackOff",
							Message: "back-off 5m0s restarting failed container",
						},
					},
					LastTerminationState: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							ExitCode: 1,
							Reason:   "Error",
						},
					},
				},
			},
		},
	}

	g := NewPodDescribe(client, testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), podRef("default", "crash-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, expected := range []string{
		"CrashLoopBackOff",
		"restartCount=5",
		"exitCode=1",
		"LastTermination",
	} {
		if !strings.Contains(section.Content, expected) {
			t.Errorf("expected content to contain %q", expected)
		}
	}
}

func TestPodDescribe_RedactsSecrets(t *testing.T) {
	client := newFakeKubeClient()
	client.pods["default/secret-pod"] = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "secret-pod",
			Namespace: "default",
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "app",
					Image: "myapp:latest",
					Env: []corev1.EnvVar{
						{Name: "DB_HOST", Value: "db.example.com"},
						{
							Name: "DB_PASSWORD",
							ValueFrom: &corev1.EnvVarSource{
								SecretKeyRef: &corev1.SecretKeySelector{
									LocalObjectReference: corev1.LocalObjectReference{Name: "db-secret"},
									Key:                  "password",
								},
							},
						},
					},
				},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}

	g := NewPodDescribe(client, testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), podRef("default", "secret-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(section.Content, "[REDACTED]") {
		t.Errorf("expected secret to be redacted")
	}
	if !strings.Contains(section.Content, "DB_HOST=db.example.com") {
		t.Errorf("expected non-secret env var to be present")
	}
}

func TestPodDescribe_PodNotFound(t *testing.T) {
	g := NewPodDescribe(newFakeKubeClient(), testRedactor(), testLogger())
	_, err := g.Gather(context.Background(), podRef("default", "nonexistent"))
	if err == nil {
		t.Fatal("expected error for nonexistent pod")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected not found error, got: %v", err)
	}
}

func TestPodDescribe_NilRedactor(t *testing.T) {
	client := newFakeKubeClient()
	client.pods["default/test-pod"] = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "default"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "img"}}},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}

	g := NewPodDescribe(client, nil, testLogger())
	section, err := g.Gather(context.Background(), podRef("default", "test-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if section.Content == "" {
		t.Error("expected non-empty content")
	}
}

// --- PodEvents Tests ---

func TestPodEvents_Name(t *testing.T) {
	g := NewPodEvents(newFakeKubeClient(), testRedactor(), testLogger())
	if g.Name() != "PodEvents" {
		t.Errorf("expected PodEvents, got %s", g.Name())
	}
}

func TestPodEvents_TriggerMatcher(t *testing.T) {
	g := NewPodEvents(newFakeKubeClient(), testRedactor(), testLogger())
	if kinds := g.ResourceKinds(); len(kinds) != 1 || kinds[0] != "Pod" {
		t.Errorf("expected [Pod], got %v", kinds)
	}
}

func TestPodEvents_WithEvents(t *testing.T) {
	client := newFakeKubeClient()
	now := metav1.NewTime(time.Date(2026, 2, 8, 12, 0, 0, 0, time.UTC))
	fieldSel := "involvedObject.name=test-pod,involvedObject.kind=Pod"
	client.events["default?"+fieldSel] = &corev1.EventList{
		Items: []corev1.Event{
			{
				ObjectMeta:    metav1.ObjectMeta{Name: "ev1"},
				Type:          "Warning",
				Reason:        "BackOff",
				Message:       "Back-off restarting failed container",
				LastTimestamp: now,
				Count:         3,
				InvolvedObject: corev1.ObjectReference{
					Kind: "Pod", Name: "test-pod", Namespace: "default",
				},
			},
			{
				ObjectMeta:    metav1.ObjectMeta{Name: "ev2"},
				Type:          "Normal",
				Reason:        "Pulled",
				Message:       "Container image already present",
				LastTimestamp: now,
				InvolvedObject: corev1.ObjectReference{
					Kind: "Pod", Name: "test-pod", Namespace: "default",
				},
			},
		},
	}

	g := NewPodEvents(client, testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), podRef("default", "test-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if section.GathererName != "PodEvents" {
		t.Errorf("expected GathererName PodEvents, got %s", section.GathererName)
	}

	for _, expected := range []string{
		"BackOff",
		"Back-off restarting failed container",
		"(x3)",
		"Pulled",
	} {
		if !strings.Contains(section.Content, expected) {
			t.Errorf("expected content to contain %q", expected)
		}
	}
}

func TestPodEvents_NoEvents(t *testing.T) {
	g := NewPodEvents(newFakeKubeClient(), testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), podRef("default", "lonely-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(section.Content, "no events found") {
		t.Errorf("expected 'no events found', got: %s", section.Content)
	}
}

// --- PodLogs Tests ---

func TestPodLogs_Name(t *testing.T) {
	g := NewPodLogs(newFakeKubeClient(), testRedactor(), testLogger(), 100, true)
	if g.Name() != "PodLogs" {
		t.Errorf("expected PodLogs, got %s", g.Name())
	}
}

func TestPodLogs_TriggerMatcher(t *testing.T) {
	g := NewPodLogs(newFakeKubeClient(), testRedactor(), testLogger(), 100, true)
	if kinds := g.ResourceKinds(); len(kinds) != 1 || kinds[0] != "Pod" {
		t.Errorf("expected [Pod], got %v", kinds)
	}
	detectors := g.DetectorNames()
	if len(detectors) != 2 {
		t.Fatalf("expected 2 detector names, got %d", len(detectors))
	}
}

func TestPodLogs_WithLogs(t *testing.T) {
	client := newFakeKubeClient()
	client.pods["default/logging-pod"] = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "logging-pod", Namespace: "default"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "app", Image: "img"}},
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "app", Ready: false, RestartCount: 3},
			},
		},
	}
	client.logs["default/logging-pod/app"] = "line1\nline2\nline3\n"
	client.logs["default/logging-pod/app/previous"] = "prev-line1\nprev-line2\n"

	g := NewPodLogs(client, testRedactor(), testLogger(), 100, true)
	section, err := g.Gather(context.Background(), podRef("default", "logging-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, expected := range []string{
		"Container: app",
		"line1",
		"line2",
		"(previous)",
		"prev-line1",
	} {
		if !strings.Contains(section.Content, expected) {
			t.Errorf("expected content to contain %q", expected)
		}
	}
}

func TestPodLogs_RedactsSensitiveData(t *testing.T) {
	client := newFakeKubeClient()
	client.pods["default/secret-log-pod"] = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "secret-log-pod", Namespace: "default"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "app", Ready: true, RestartCount: 0},
			},
		},
	}
	client.logs["default/secret-log-pod/app"] = "connecting with password=supersecret123\n"

	g := NewPodLogs(client, testRedactor(), testLogger(), 100, false)
	section, err := g.Gather(context.Background(), podRef("default", "secret-log-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(section.Content, "supersecret123") {
		t.Error("expected password to be redacted from logs")
	}
	if !strings.Contains(section.Content, "[REDACTED]") {
		t.Error("expected [REDACTED] placeholder in logs")
	}
}

func TestPodLogs_DefaultTailLines(t *testing.T) {
	g := NewPodLogs(newFakeKubeClient(), testRedactor(), testLogger(), 0, true)
	if g.tailLines != 100 {
		t.Errorf("expected default tailLines 100, got %d", g.tailLines)
	}
}

func TestPodLogs_PodNotFound(t *testing.T) {
	g := NewPodLogs(newFakeKubeClient(), testRedactor(), testLogger(), 100, true)
	_, err := g.Gather(context.Background(), podRef("default", "gone"))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestPodLogs_InitContainerWithError(t *testing.T) {
	client := newFakeKubeClient()
	client.pods["default/init-fail-pod"] = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "init-fail-pod", Namespace: "default"},
		Spec: corev1.PodSpec{
			InitContainers: []corev1.Container{{Name: "init", Image: "init:v1"}},
			Containers:     []corev1.Container{{Name: "app", Image: "app:v1"}},
		},
		Status: corev1.PodStatus{
			InitContainerStatuses: []corev1.ContainerStatus{
				{
					Name: "init",
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							ExitCode: 1,
							Reason:   "Error",
						},
					},
				},
			},
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "app", Ready: false, RestartCount: 0},
			},
		},
	}
	client.logs["default/init-fail-pod/init"] = "init failed: config missing\n"

	g := NewPodLogs(client, testRedactor(), testLogger(), 100, false)
	section, err := g.Gather(context.Background(), podRef("default", "init-fail-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(section.Content, "Init Container: init") {
		t.Errorf("expected init container logs, got:\n%s", section.Content)
	}
	if !strings.Contains(section.Content, "init failed: config missing") {
		t.Errorf("expected init container log content")
	}
}

// --- NodeConditions Tests ---

func TestNodeConditions_Name(t *testing.T) {
	g := NewNodeConditions(newFakeKubeClient(), testRedactor(), testLogger(), true)
	if g.Name() != "NodeConditions" {
		t.Errorf("expected NodeConditions, got %s", g.Name())
	}
}

func TestNodeConditions_TriggerMatcher(t *testing.T) {
	g := NewNodeConditions(newFakeKubeClient(), testRedactor(), testLogger(), true)
	kinds := g.ResourceKinds()
	if len(kinds) != 2 {
		t.Errorf("expected 2 kinds, got %d", len(kinds))
	}
}

func TestNodeConditions_DirectNode(t *testing.T) {
	client := newFakeKubeClient()
	client.nodes["node-1"] = &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "node-1",
			UID:    types.UID("node-uid-1"),
			Labels: map[string]string{"topology.kubernetes.io/zone": "us-east-1a"},
		},
		Spec: corev1.NodeSpec{
			Taints: []corev1.Taint{
				{Key: "node.kubernetes.io/not-ready", Effect: corev1.TaintEffectNoSchedule},
			},
		},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionFalse, Reason: "KubeletNotReady", Message: "container runtime down"},
			},
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("4"),
				corev1.ResourceMemory: resource.MustParse("16Gi"),
			},
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    resource.MustParse("3800m"),
				corev1.ResourceMemory: resource.MustParse("15Gi"),
			},
		},
	}

	g := NewNodeConditions(client, testRedactor(), testLogger(), true)
	section, err := g.Gather(context.Background(), nodeRef("node-1"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, expected := range []string{
		"Node: node-1",
		"Ready: False",
		"KubeletNotReady",
		"container runtime down",
		"Capacity:",
		"Allocatable:",
		"Taints:",
		"not-ready",
		"us-east-1a",
	} {
		if !strings.Contains(section.Content, expected) {
			t.Errorf("expected content to contain %q, got:\n%s", expected, section.Content)
		}
	}
}

func TestNodeConditions_FromPod(t *testing.T) {
	client := newFakeKubeClient()
	client.pods["default/test-pod"] = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "test-pod", Namespace: "default"},
		Spec:       corev1.PodSpec{NodeName: "node-2"},
	}
	client.nodes["node-2"] = &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-2"},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
		},
	}

	g := NewNodeConditions(client, testRedactor(), testLogger(), false)
	section, err := g.Gather(context.Background(), podRef("default", "test-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(section.Content, "Node: node-2") {
		t.Errorf("expected node-2 in content")
	}
}

func TestNodeConditions_PodNotScheduled(t *testing.T) {
	client := newFakeKubeClient()
	client.pods["default/pending-pod"] = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pending-pod", Namespace: "default"},
		Spec:       corev1.PodSpec{NodeName: ""},
	}

	g := NewNodeConditions(client, testRedactor(), testLogger(), true)
	section, err := g.Gather(context.Background(), podRef("default", "pending-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(section.Content, "not scheduled") {
		t.Errorf("expected 'not scheduled' message, got: %s", section.Content)
	}
}

func TestNodeConditions_UnsupportedKind(t *testing.T) {
	g := NewNodeConditions(newFakeKubeClient(), testRedactor(), testLogger(), true)
	_, err := g.Gather(context.Background(), model.ResourceRef{Kind: "Deployment", Name: "x"})
	if err == nil {
		t.Fatal("expected error for unsupported kind")
	}
}

func TestNodeConditions_WithoutAllocatable(t *testing.T) {
	client := newFakeKubeClient()
	client.nodes["node-1"] = &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("4"),
			},
		},
	}

	g := NewNodeConditions(client, testRedactor(), testLogger(), false)
	section, err := g.Gather(context.Background(), nodeRef("node-1"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(section.Content, "Capacity:") {
		t.Errorf("expected no Capacity section when includeAllocatable=false")
	}
}

// --- PVCStatus Tests ---

func TestPVCStatus_Name(t *testing.T) {
	g := NewPVCStatus(newFakeKubeClient(), testRedactor(), testLogger())
	if g.Name() != "PVCStatus" {
		t.Errorf("expected PVCStatus, got %s", g.Name())
	}
}

func TestPVCStatus_DirectPVC(t *testing.T) {
	client := newFakeKubeClient()
	sc := "gp3"
	client.pvcs["default/data-pvc"] = &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "data-pvc", Namespace: "default"},
		Spec: corev1.PersistentVolumeClaimSpec{
			StorageClassName: &sc,
			AccessModes:      []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			VolumeName:       "pv-1",
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					"storage": resource.MustParse("10Gi"),
				},
			},
		},
		Status: corev1.PersistentVolumeClaimStatus{
			Phase: corev1.ClaimBound,
		},
	}
	client.pvs["pv-1"] = &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "pv-1"},
		Spec: corev1.PersistentVolumeSpec{
			PersistentVolumeSource: corev1.PersistentVolumeSource{
				CSI: &corev1.CSIPersistentVolumeSource{
					Driver:       "ebs.csi.aws.com",
					VolumeHandle: "vol-0123456789abcdef0",
				},
			},
		},
		Status: corev1.PersistentVolumeStatus{
			Phase: corev1.VolumeBound,
		},
	}

	g := NewPVCStatus(client, testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), pvcRef("default", "data-pvc"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, expected := range []string{
		"PVC: default/data-pvc",
		"Phase: Bound",
		"StorageClass: gp3",
		"10Gi",
		"ReadWriteOnce",
		"BoundVolume: pv-1",
		"ebs.csi.aws.com",
		"vol-0123456789abcdef0",
	} {
		if !strings.Contains(section.Content, expected) {
			t.Errorf("expected content to contain %q, got:\n%s", expected, section.Content)
		}
	}
}

func TestPVCStatus_FromPod(t *testing.T) {
	client := newFakeKubeClient()
	client.pods["default/pvc-pod"] = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pvc-pod", Namespace: "default"},
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{
				{
					Name: "data",
					VolumeSource: corev1.VolumeSource{
						PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
							ClaimName: "my-pvc",
						},
					},
				},
			},
		},
	}
	client.pvcs["default/my-pvc"] = &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "my-pvc", Namespace: "default"},
		Spec: corev1.PersistentVolumeClaimSpec{
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					"storage": resource.MustParse("5Gi"),
				},
			},
		},
		Status: corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimPending},
	}

	g := NewPVCStatus(client, testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), podRef("default", "pvc-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(section.Content, "my-pvc") {
		t.Errorf("expected PVC name in content")
	}
	if !strings.Contains(section.Content, "Pending") {
		t.Errorf("expected Pending phase")
	}
}

func TestPVCStatus_PodWithNoPVCs(t *testing.T) {
	client := newFakeKubeClient()
	client.pods["default/no-pvc-pod"] = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "no-pvc-pod", Namespace: "default"},
		Spec:       corev1.PodSpec{},
	}

	g := NewPVCStatus(client, testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), podRef("default", "no-pvc-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(section.Content, "no PVC volumes") {
		t.Errorf("expected 'no PVC volumes' message")
	}
}

func TestPVCStatus_UnsupportedKind(t *testing.T) {
	g := NewPVCStatus(newFakeKubeClient(), testRedactor(), testLogger())
	_, err := g.Gather(context.Background(), nodeRef("n"))
	if err == nil {
		t.Fatal("expected error for Node kind")
	}
}

// --- NamespaceEvents Tests ---

func TestNamespaceEvents_Name(t *testing.T) {
	g := NewNamespaceEvents(newFakeKubeClient(), testRedactor(), testLogger())
	if g.Name() != "NamespaceEvents" {
		t.Errorf("expected NamespaceEvents, got %s", g.Name())
	}
}

func TestNamespaceEvents_NoTriggerMatcher(t *testing.T) {
	g := NewNamespaceEvents(newFakeKubeClient(), testRedactor(), testLogger())
	// NamespaceEvents should NOT implement TriggerMatcher (fires for all events)
	_, ok := interface{}(g).(TriggerMatcher)
	if ok {
		t.Error("NamespaceEvents should not implement TriggerMatcher")
	}
}

func TestNamespaceEvents_WithEvents(t *testing.T) {
	client := newFakeKubeClient()
	now := metav1.NewTime(time.Date(2026, 2, 8, 14, 0, 0, 0, time.UTC))
	client.events["payments"] = &corev1.EventList{
		Items: []corev1.Event{
			{
				ObjectMeta:    metav1.ObjectMeta{Name: "ev1"},
				Type:          "Warning",
				Reason:        "FailedScheduling",
				Message:       "0/3 nodes available",
				LastTimestamp: now,
				InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "app-abc"},
			},
		},
	}

	g := NewNamespaceEvents(client, testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), model.ResourceRef{
		Kind: "Pod", Namespace: "payments", Name: "app-abc",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(section.Content, "FailedScheduling") {
		t.Errorf("expected FailedScheduling in content")
	}
	if !strings.Contains(section.Content, "0/3 nodes available") {
		t.Errorf("expected scheduling message")
	}
}

func TestNamespaceEvents_ClusterScoped(t *testing.T) {
	g := NewNamespaceEvents(newFakeKubeClient(), testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), nodeRef("node-1"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(section.Content, "cluster-scoped") {
		t.Errorf("expected cluster-scoped message for node")
	}
}

// --- ReplicaSetStatus Tests ---

func TestReplicaSetStatus_Name(t *testing.T) {
	g := NewReplicaSetStatus(newFakeKubeClient(), testRedactor(), testLogger())
	if g.Name() != "ReplicaSetStatus" {
		t.Errorf("expected ReplicaSetStatus, got %s", g.Name())
	}
}

func TestReplicaSetStatus_WithDeployment(t *testing.T) {
	client := newFakeKubeClient()
	client.pods["default/app-pod"] = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "app-pod",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{
					Kind:       "ReplicaSet",
					Name:       "app-rs-1",
					Controller: boolPtr(true),
				},
			},
		},
	}
	client.replicaSets["default/app-rs-1"] = &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "app-rs-1",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "Deployment", Name: "app-deploy", Controller: boolPtr(true)},
			},
		},
		Spec: appsv1.ReplicaSetSpec{Replicas: int32Ptr(3)},
		Status: appsv1.ReplicaSetStatus{
			ReadyReplicas:     2,
			AvailableReplicas: 2,
		},
	}
	client.deployments["default/app-deploy"] = &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "app-deploy", Namespace: "default"},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32Ptr(3),
			Strategy: appsv1.DeploymentStrategy{Type: appsv1.RollingUpdateDeploymentStrategyType},
		},
		Status: appsv1.DeploymentStatus{
			ReadyReplicas:     2,
			AvailableReplicas: 2,
			UpdatedReplicas:   3,
			Conditions: []appsv1.DeploymentCondition{
				{Type: appsv1.DeploymentAvailable, Status: corev1.ConditionTrue, Reason: "MinimumReplicasAvailable"},
			},
		},
	}

	g := NewReplicaSetStatus(client, testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), podRef("default", "app-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, expected := range []string{
		"ReplicaSet: default/app-rs-1",
		"Desired: 3, Ready: 2",
		"Deployment: default/app-deploy",
		"RollingUpdate",
		"MinimumReplicasAvailable",
	} {
		if !strings.Contains(section.Content, expected) {
			t.Errorf("expected content to contain %q, got:\n%s", expected, section.Content)
		}
	}
}

func TestReplicaSetStatus_NoReplicaSetOwner(t *testing.T) {
	client := newFakeKubeClient()
	client.pods["default/standalone-pod"] = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "standalone-pod",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "StatefulSet", Name: "my-sts", Controller: boolPtr(true)},
			},
		},
	}

	g := NewReplicaSetStatus(client, testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), podRef("default", "standalone-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(section.Content, "no ReplicaSet owner") {
		t.Errorf("expected 'no ReplicaSet owner' message")
	}
	if !strings.Contains(section.Content, "StatefulSet/my-sts") {
		t.Errorf("expected StatefulSet owner to be listed")
	}
}

// --- OwnerChain Tests ---

func TestOwnerChain_Name(t *testing.T) {
	g := NewOwnerChain(newFakeKubeClient(), testRedactor(), testLogger())
	if g.Name() != "OwnerChain" {
		t.Errorf("expected OwnerChain, got %s", g.Name())
	}
}

func TestOwnerChain_DeploymentChain(t *testing.T) {
	client := newFakeKubeClient()
	client.pods["default/chain-pod"] = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "chain-pod",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "ReplicaSet", Name: "chain-rs", Controller: boolPtr(true)},
			},
		},
	}
	client.replicaSets["default/chain-rs"] = &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "chain-rs",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "Deployment", Name: "chain-deploy", Controller: boolPtr(true)},
			},
		},
		Spec:   appsv1.ReplicaSetSpec{Replicas: int32Ptr(2)},
		Status: appsv1.ReplicaSetStatus{ReadyReplicas: 1, AvailableReplicas: 1},
	}
	client.deployments["default/chain-deploy"] = &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "chain-deploy", Namespace: "default"},
		Spec:       appsv1.DeploymentSpec{Replicas: int32Ptr(2)},
		Status:     appsv1.DeploymentStatus{ReadyReplicas: 1, AvailableReplicas: 1},
	}

	g := NewOwnerChain(client, testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), podRef("default", "chain-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(section.Content, "ReplicaSet/chain-rs") {
		t.Errorf("expected ReplicaSet in chain")
	}
	if !strings.Contains(section.Content, "Deployment/chain-deploy") {
		t.Errorf("expected Deployment in chain")
	}
}

func TestOwnerChain_StandalonePod(t *testing.T) {
	client := newFakeKubeClient()
	client.pods["default/alone-pod"] = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "alone-pod", Namespace: "default"},
	}

	g := NewOwnerChain(client, testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), podRef("default", "alone-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(section.Content, "standalone pod") {
		t.Errorf("expected 'standalone pod' message, got: %s", section.Content)
	}
}

func TestOwnerChain_JobOwner(t *testing.T) {
	client := newFakeKubeClient()
	client.pods["batch/job-pod"] = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "job-pod",
			Namespace: "batch",
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "Job", Name: "my-job", Controller: boolPtr(true)},
			},
		},
	}
	client.jobs["batch/my-job"] = &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{Name: "my-job", Namespace: "batch"},
		Status:     batchv1.JobStatus{Active: 1, Succeeded: 0, Failed: 2},
	}

	g := NewOwnerChain(client, testRedactor(), testLogger())
	ref := model.ResourceRef{Kind: "Pod", Namespace: "batch", Name: "job-pod"}
	section, err := g.Gather(context.Background(), ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(section.Content, "Job/my-job") {
		t.Errorf("expected Job in chain, got: %s", section.Content)
	}
	if !strings.Contains(section.Content, "Failed: 2") {
		t.Errorf("expected job status in chain")
	}
}

// --- KarpenterState Tests ---

func TestKarpenterState_Name(t *testing.T) {
	g := NewKarpenterState(newFakeKubeClient(), &fakeKarpenterFetcher{}, testRedactor(), testLogger())
	if g.Name() != "KarpenterState" {
		t.Errorf("expected KarpenterState, got %s", g.Name())
	}
}

func TestKarpenterState_TriggerMatcher(t *testing.T) {
	g := NewKarpenterState(newFakeKubeClient(), &fakeKarpenterFetcher{}, testRedactor(), testLogger())
	kinds := g.ResourceKinds()
	if len(kinds) != 1 || kinds[0] != "Pod" {
		t.Errorf("expected [Pod], got %v", kinds)
	}
	detectors := g.DetectorNames()
	if len(detectors) != 1 || detectors[0] != "PodStuckPending" {
		t.Errorf("expected [PodStuckPending], got %v", detectors)
	}
}

func TestKarpenterState_SuccessfulGather(t *testing.T) {
	client := newFakeKubeClient()
	kf := &fakeKarpenterFetcher{
		nodePools:  "  default: Ready, capacity=100\n  gpu: Ready, capacity=10\n",
		nodeClaims: "  node-abc: Provisioned, type=m5.xlarge\n",
	}

	g := NewKarpenterState(client, kf, testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), podRef("default", "pending-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, expected := range []string{
		"NodePools:",
		"default: Ready",
		"gpu: Ready",
		"NodeClaims:",
		"node-abc: Provisioned",
	} {
		if !strings.Contains(section.Content, expected) {
			t.Errorf("expected content to contain %q", expected)
		}
	}
}

func TestKarpenterState_FetcherErrors(t *testing.T) {
	client := newFakeKubeClient()
	kf := &fakeKarpenterFetcher{
		npError: fmt.Errorf("CRD not found"),
		ncError: fmt.Errorf("CRD not found"),
	}

	g := NewKarpenterState(client, kf, testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), podRef("default", "pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(section.Content, "Error listing NodePools") {
		t.Errorf("expected error message in content")
	}
}

// --- Interface compliance checks ---

func TestBuiltinGatherers_ImplementGatherer(t *testing.T) {
	client := newFakeKubeClient()
	r := testRedactor()
	l := testLogger()

	var _ Gatherer = NewPodDescribe(client, r, l)
	var _ Gatherer = NewPodEvents(client, r, l)
	var _ Gatherer = NewPodLogs(client, r, l, 100, true)
	var _ Gatherer = NewNodeConditions(client, r, l, true)
	var _ Gatherer = NewPVCStatus(client, r, l)
	var _ Gatherer = NewNamespaceEvents(client, r, l)
	var _ Gatherer = NewReplicaSetStatus(client, r, l)
	var _ Gatherer = NewOwnerChain(client, r, l)
	var _ Gatherer = NewKarpenterState(client, &fakeKarpenterFetcher{}, r, l)
}

func TestBuiltinGatherers_TriggerMatcherCompliance(t *testing.T) {
	client := newFakeKubeClient()
	r := testRedactor()
	l := testLogger()

	// These should implement TriggerMatcher
	triggerMatchers := []Gatherer{
		NewPodDescribe(client, r, l),
		NewPodEvents(client, r, l),
		NewPodLogs(client, r, l, 100, true),
		NewNodeConditions(client, r, l, true),
		NewPVCStatus(client, r, l),
		NewReplicaSetStatus(client, r, l),
		NewOwnerChain(client, r, l),
		NewKarpenterState(client, &fakeKarpenterFetcher{}, r, l),
	}

	for _, g := range triggerMatchers {
		if _, ok := g.(TriggerMatcher); !ok {
			t.Errorf("%s should implement TriggerMatcher", g.Name())
		}
	}

	// NamespaceEvents should NOT implement TriggerMatcher
	nsEvents := NewNamespaceEvents(client, r, l)
	if _, ok := interface{}(nsEvents).(TriggerMatcher); ok {
		t.Error("NamespaceEvents should not implement TriggerMatcher")
	}
}

// --- Registration test ---

func TestBuiltinGatherers_RegisterAll(t *testing.T) {
	client := newFakeKubeClient()
	r := testRedactor()
	l := testLogger()

	reg := NewRegistry(l)

	gatherers := []Gatherer{
		NewPodDescribe(client, r, l),
		NewPodEvents(client, r, l),
		NewPodLogs(client, r, l, 100, true),
		NewNodeConditions(client, r, l, true),
		NewPVCStatus(client, r, l),
		NewNamespaceEvents(client, r, l),
		NewReplicaSetStatus(client, r, l),
		NewOwnerChain(client, r, l),
		NewKarpenterState(client, &fakeKarpenterFetcher{}, r, l),
	}

	for _, g := range gatherers {
		if err := reg.Register(g); err != nil {
			t.Errorf("failed to register %s: %v", g.Name(), err)
		}
	}

	if reg.Count() != 9 {
		t.Errorf("expected 9 registered gatherers, got %d", reg.Count())
	}

	expectedNames := map[string]bool{
		"PodDescribe": true, "PodEvents": true, "PodLogs": true,
		"NodeConditions": true, "PVCStatus": true, "NamespaceEvents": true,
		"ReplicaSetStatus": true, "OwnerChain": true, "KarpenterState": true,
	}
	for _, name := range reg.Names() {
		if !expectedNames[name] {
			t.Errorf("unexpected gatherer name: %s", name)
		}
	}
}

// --- Context cancellation ---

func TestBuiltinGatherers_ContextCancellation(t *testing.T) {
	client := newFakeKubeClient()
	client.pods["default/pod"] = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod", Namespace: "default"},
		Spec:       corev1.PodSpec{NodeName: "n1"},
	}
	client.nodes["n1"] = &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "n1"},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
		},
	}

	g := NewNodeConditions(client, testRedactor(), testLogger(), true)

	// Use an already-canceled context — the gatherer should still work
	// since the fake client doesn't check context.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Fake client doesn't check ctx, so this should still succeed
	_, err := g.Gather(ctx, podRef("default", "pod"))
	if err != nil {
		t.Fatalf("unexpected error with canceled context: %v", err)
	}
}

// --- Additional PodDescribe Tests ---

func TestPodDescribe_WithVolumes(t *testing.T) {
	client := newFakeKubeClient()
	client.pods["default/vol-pod"] = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "vol-pod", Namespace: "default"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "app", Image: "img"}},
			Volumes: []corev1.Volume{
				{Name: "data", VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "my-pvc"},
				}},
				{Name: "config", VolumeSource: corev1.VolumeSource{
					ConfigMap: &corev1.ConfigMapVolumeSource{
						LocalObjectReference: corev1.LocalObjectReference{Name: "app-config"},
					},
				}},
				{Name: "secret-vol", VolumeSource: corev1.VolumeSource{
					Secret: &corev1.SecretVolumeSource{SecretName: "my-secret"},
				}},
				{Name: "tmp", VolumeSource: corev1.VolumeSource{
					EmptyDir: &corev1.EmptyDirVolumeSource{},
				}},
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}

	g := NewPodDescribe(client, testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), podRef("default", "vol-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, expected := range []string{
		"Volumes:",
		"PVC(my-pvc)",
		"ConfigMap(app-config)",
		"Secret(my-secret)",
		"EmptyDir",
	} {
		if !strings.Contains(section.Content, expected) {
			t.Errorf("expected content to contain %q, got:\n%s", expected, section.Content)
		}
	}
}

func TestPodDescribe_WithTolerationsAndNodeSelector(t *testing.T) {
	client := newFakeKubeClient()
	client.pods["default/tol-pod"] = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "tol-pod", Namespace: "default"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "app", Image: "img"}},
			Tolerations: []corev1.Toleration{
				{Key: "gpu", Value: "true", Effect: corev1.TaintEffectNoSchedule},
			},
			NodeSelector: map[string]string{
				"node-type": "gpu",
				"zone":      "us-east-1a",
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodPending},
	}

	g := NewPodDescribe(client, testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), podRef("default", "tol-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, expected := range []string{
		"Tolerations:",
		"gpu=true:NoSchedule",
		"NodeSelector:",
		"node-type=gpu",
	} {
		if !strings.Contains(section.Content, expected) {
			t.Errorf("expected content to contain %q, got:\n%s", expected, section.Content)
		}
	}
}

func TestPodDescribe_WithInitContainers(t *testing.T) {
	client := newFakeKubeClient()
	client.pods["default/init-pod"] = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "init-pod", Namespace: "default"},
		Spec: corev1.PodSpec{
			InitContainers: []corev1.Container{
				{Name: "init-db", Image: "init-db:v1"},
				{Name: "init-config", Image: "init-config:v2"},
			},
			Containers: []corev1.Container{{Name: "app", Image: "app:v1"}},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			InitContainerStatuses: []corev1.ContainerStatus{
				{
					Name:  "init-db",
					Ready: true,
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{ExitCode: 0, Reason: "Completed"},
					},
				},
			},
		},
	}

	g := NewPodDescribe(client, testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), podRef("default", "init-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, expected := range []string{
		"Init Container: init-db",
		"Image: init-db:v1",
		"Init Container: init-config",
		"Init Container Statuses:",
	} {
		if !strings.Contains(section.Content, expected) {
			t.Errorf("expected content to contain %q, got:\n%s", expected, section.Content)
		}
	}
}

func TestPodDescribe_TerminatedState(t *testing.T) {
	client := newFakeKubeClient()
	client.pods["default/dead-pod"] = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "dead-pod", Namespace: "default"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "app", Image: "app:v1"}},
		},
		Status: corev1.PodStatus{
			Phase:   corev1.PodFailed,
			Reason:  "Evicted",
			Message: "The node was low on resource: memory.",
			ContainerStatuses: []corev1.ContainerStatus{
				{
					Name:  "app",
					Ready: false,
					State: corev1.ContainerState{
						Terminated: &corev1.ContainerStateTerminated{
							ExitCode: 137,
							Reason:   "OOMKilled",
							Message:  "Memory limit exceeded",
						},
					},
				},
			},
		},
	}

	g := NewPodDescribe(client, testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), podRef("default", "dead-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, expected := range []string{
		"Phase: Failed",
		"Reason: Evicted",
		"Message: The node was low on resource: memory.",
		"exitCode=137",
		"OOMKilled",
		"Memory limit exceeded",
	} {
		if !strings.Contains(section.Content, expected) {
			t.Errorf("expected content to contain %q, got:\n%s", expected, section.Content)
		}
	}
}

func TestPodDescribe_WithPriority(t *testing.T) {
	client := newFakeKubeClient()
	priority := int32(1000)
	client.pods["default/priority-pod"] = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "priority-pod", Namespace: "default"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "app", Image: "img"}},
			Priority:   &priority,
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}

	g := NewPodDescribe(client, testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), podRef("default", "priority-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(section.Content, "Priority: 1000") {
		t.Errorf("expected priority in content, got:\n%s", section.Content)
	}
}

func TestPodDescribe_RedactsPatternInContent(t *testing.T) {
	client := newFakeKubeClient()
	client.pods["default/bearer-pod"] = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "bearer-pod", Namespace: "default"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "app",
					Image: "img",
					Env: []corev1.EnvVar{
						{Name: "API_TOKEN", Value: "Bearer eyJhbGciOiJIUzI1NiJ9.test"},
					},
				},
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}

	g := NewPodDescribe(client, testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), podRef("default", "bearer-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(section.Content, "eyJhbGciOiJIUzI1NiJ9") {
		t.Error("expected bearer token to be redacted from content")
	}
	if !strings.Contains(section.Content, "[REDACTED]") {
		t.Error("expected [REDACTED] placeholder")
	}
}

func TestPodDescribe_ConfigMapEnvSource(t *testing.T) {
	client := newFakeKubeClient()
	client.pods["default/cm-pod"] = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "cm-pod", Namespace: "default"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "app",
					Image: "img",
					Env: []corev1.EnvVar{
						{
							Name: "CONFIG_VAL",
							ValueFrom: &corev1.EnvVarSource{
								ConfigMapKeyRef: &corev1.ConfigMapKeySelector{
									LocalObjectReference: corev1.LocalObjectReference{Name: "app-cm"},
									Key:                  "setting",
								},
							},
						},
					},
				},
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}

	g := NewPodDescribe(client, testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), podRef("default", "cm-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(section.Content, "ConfigMap app-cm") {
		t.Errorf("expected ConfigMap reference, got:\n%s", section.Content)
	}
}

// --- Additional PodEvents Tests ---

func TestPodEvents_EventTimeOnly(t *testing.T) {
	client := newFakeKubeClient()
	eventTime := metav1.NewMicroTime(time.Date(2026, 2, 8, 15, 30, 0, 0, time.UTC))
	fieldSel := "involvedObject.name=new-pod,involvedObject.kind=Pod"
	client.events["default?"+fieldSel] = &corev1.EventList{
		Items: []corev1.Event{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "ev1"},
				Type:       "Normal",
				Reason:     "Scheduled",
				Message:    "Successfully assigned to node-1",
				EventTime:  eventTime,
				// LastTimestamp is zero — should fall back to EventTime
				InvolvedObject: corev1.ObjectReference{
					Kind: "Pod", Name: "new-pod", Namespace: "default",
				},
			},
		},
	}

	g := NewPodEvents(client, testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), podRef("default", "new-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(section.Content, "2026-02-08T15:30:00Z") {
		t.Errorf("expected EventTime timestamp in output, got:\n%s", section.Content)
	}
}

func TestPodEvents_RedactsSecrets(t *testing.T) {
	client := newFakeKubeClient()
	now := metav1.Now()
	fieldSel := "involvedObject.name=secret-pod,involvedObject.kind=Pod"
	client.events["default?"+fieldSel] = &corev1.EventList{
		Items: []corev1.Event{
			{
				ObjectMeta:   metav1.ObjectMeta{Name: "ev1"},
				Type:         "Warning",
				Reason:       "AuthFailed",
				Message:      "authentication failed with password=hunter2 for user admin",
				LastTimestamp: now,
				InvolvedObject: corev1.ObjectReference{
					Kind: "Pod", Name: "secret-pod", Namespace: "default",
				},
			},
		},
	}

	g := NewPodEvents(client, testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), podRef("default", "secret-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(section.Content, "hunter2") {
		t.Error("expected password to be redacted from events")
	}
	if !strings.Contains(section.Content, "[REDACTED]") {
		t.Error("expected [REDACTED] placeholder in events")
	}
}

func TestPodEvents_NilRedactor(t *testing.T) {
	client := newFakeKubeClient()
	now := metav1.Now()
	fieldSel := "involvedObject.name=test-pod,involvedObject.kind=Pod"
	client.events["default?"+fieldSel] = &corev1.EventList{
		Items: []corev1.Event{
			{
				ObjectMeta:   metav1.ObjectMeta{Name: "ev1"},
				Type:         "Normal",
				Reason:       "Pulled",
				Message:      "pulled image successfully",
				LastTimestamp: now,
			},
		},
	}

	g := NewPodEvents(client, nil, testLogger())
	section, err := g.Gather(context.Background(), podRef("default", "test-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if section.Content == "" {
		t.Error("expected non-empty content with nil redactor")
	}
}

func TestPodEvents_SingleCountNotShown(t *testing.T) {
	client := newFakeKubeClient()
	now := metav1.Now()
	fieldSel := "involvedObject.name=single-pod,involvedObject.kind=Pod"
	client.events["default?"+fieldSel] = &corev1.EventList{
		Items: []corev1.Event{
			{
				ObjectMeta:   metav1.ObjectMeta{Name: "ev1"},
				Type:         "Normal",
				Reason:       "Created",
				Message:      "container created",
				LastTimestamp: now,
				Count:        1,
			},
		},
	}

	g := NewPodEvents(client, testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), podRef("default", "single-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Count=1 should NOT show (x1)
	if strings.Contains(section.Content, "(x1)") {
		t.Error("expected single-count events to not show count")
	}
}

// --- Additional PodLogs Tests ---

func TestPodLogs_NoPreviousWhenDisabled(t *testing.T) {
	client := newFakeKubeClient()
	client.pods["default/no-prev-pod"] = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "no-prev-pod", Namespace: "default"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "app", RestartCount: 5},
			},
		},
	}
	client.logs["default/no-prev-pod/app"] = "current log\n"
	client.logs["default/no-prev-pod/app/previous"] = "previous log should not appear\n"

	g := NewPodLogs(client, testRedactor(), testLogger(), 100, false)
	section, err := g.Gather(context.Background(), podRef("default", "no-prev-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(section.Content, "previous log should not appear") {
		t.Error("previous logs should not be fetched when includePrevious is false")
	}
	if strings.Contains(section.Content, "(previous)") {
		t.Error("should not show previous section when disabled")
	}
}

func TestPodLogs_MultipleContainers(t *testing.T) {
	client := newFakeKubeClient()
	client.pods["default/multi-pod"] = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "multi-pod", Namespace: "default"},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{Name: "web", Image: "nginx"},
				{Name: "sidecar", Image: "proxy"},
			},
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "web", RestartCount: 0},
				{Name: "sidecar", RestartCount: 0},
			},
		},
	}
	client.logs["default/multi-pod/web"] = "web log line\n"
	client.logs["default/multi-pod/sidecar"] = "sidecar log line\n"

	g := NewPodLogs(client, testRedactor(), testLogger(), 50, false)
	section, err := g.Gather(context.Background(), podRef("default", "multi-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, expected := range []string{
		"Container: web",
		"web log line",
		"Container: sidecar",
		"sidecar log line",
	} {
		if !strings.Contains(section.Content, expected) {
			t.Errorf("expected content to contain %q", expected)
		}
	}
}

func TestPodLogs_LogFetchError(t *testing.T) {
	client := newFakeKubeClient()
	client.pods["default/error-pod"] = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "error-pod", Namespace: "default"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "app", RestartCount: 0},
			},
		},
	}
	client.logErrors["default/error-pod/app"] = fmt.Errorf("container not running")

	g := NewPodLogs(client, testRedactor(), testLogger(), 100, false)
	section, err := g.Gather(context.Background(), podRef("default", "error-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(section.Content, "error fetching logs") {
		t.Errorf("expected log error in content, got:\n%s", section.Content)
	}
}

func TestPodLogs_EmptyLogs(t *testing.T) {
	client := newFakeKubeClient()
	client.pods["default/empty-log-pod"] = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "empty-log-pod", Namespace: "default"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "app", RestartCount: 0},
			},
		},
	}
	// No logs registered in fake client — GetPodLogs returns ""

	g := NewPodLogs(client, testRedactor(), testLogger(), 100, false)
	section, err := g.Gather(context.Background(), podRef("default", "empty-log-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(section.Content, "(no logs)") {
		t.Errorf("expected '(no logs)' message, got:\n%s", section.Content)
	}
}

func TestPodLogs_NegativeTailLines(t *testing.T) {
	g := NewPodLogs(newFakeKubeClient(), testRedactor(), testLogger(), -5, true)
	if g.tailLines != 100 {
		t.Errorf("expected negative tailLines to default to 100, got %d", g.tailLines)
	}
}

func TestPodLogs_NilRedactor(t *testing.T) {
	client := newFakeKubeClient()
	client.pods["default/log-pod"] = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "log-pod", Namespace: "default"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "app", RestartCount: 0},
			},
		},
	}
	client.logs["default/log-pod/app"] = "some log line\n"

	g := NewPodLogs(client, nil, testLogger(), 100, false)
	section, err := g.Gather(context.Background(), podRef("default", "log-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(section.Content, "some log line") {
		t.Error("expected log content with nil redactor")
	}
}

func TestPodLogs_RedactsAWSKeys(t *testing.T) {
	client := newFakeKubeClient()
	client.pods["default/aws-pod"] = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "aws-pod", Namespace: "default"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app"}}},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "app", RestartCount: 0},
			},
		},
	}
	client.logs["default/aws-pod/app"] = "using key AKIAIOSFODNN7EXAMPLE for bucket access\n"

	g := NewPodLogs(client, testRedactor(), testLogger(), 100, false)
	section, err := g.Gather(context.Background(), podRef("default", "aws-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(section.Content, "AKIAIOSFODNN7EXAMPLE") {
		t.Error("expected AWS key to be redacted from logs")
	}
}

// --- Additional NodeConditions Tests ---

func TestNodeConditions_NodeNotFound(t *testing.T) {
	g := NewNodeConditions(newFakeKubeClient(), testRedactor(), testLogger(), true)
	_, err := g.Gather(context.Background(), nodeRef("nonexistent"))
	if err == nil {
		t.Fatal("expected error for nonexistent node")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected not found error, got: %v", err)
	}
}

func TestNodeConditions_PodNotFound(t *testing.T) {
	g := NewNodeConditions(newFakeKubeClient(), testRedactor(), testLogger(), true)
	_, err := g.Gather(context.Background(), podRef("default", "nonexistent-pod"))
	if err == nil {
		t.Fatal("expected error for nonexistent pod")
	}
}

func TestNodeConditions_MultipleTaints(t *testing.T) {
	client := newFakeKubeClient()
	client.nodes["tainted-node"] = &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "tainted-node"},
		Spec: corev1.NodeSpec{
			Taints: []corev1.Taint{
				{Key: "node.kubernetes.io/not-ready", Effect: corev1.TaintEffectNoSchedule},
				{Key: "node.kubernetes.io/unreachable", Effect: corev1.TaintEffectNoExecute},
				{Key: "dedicated", Value: "gpu", Effect: corev1.TaintEffectNoSchedule},
			},
		},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionFalse},
				{Type: corev1.NodeMemoryPressure, Status: corev1.ConditionTrue, Reason: "MemoryPressure", Message: "high memory usage"},
			},
		},
	}

	g := NewNodeConditions(client, testRedactor(), testLogger(), false)
	section, err := g.Gather(context.Background(), nodeRef("tainted-node"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, expected := range []string{
		"not-ready",
		"unreachable",
		"dedicated=gpu",
		"MemoryPressure",
		"high memory usage",
	} {
		if !strings.Contains(section.Content, expected) {
			t.Errorf("expected content to contain %q, got:\n%s", expected, section.Content)
		}
	}
}

func TestNodeConditions_NilRedactor(t *testing.T) {
	client := newFakeKubeClient()
	client.nodes["node-1"] = &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
		Status: corev1.NodeStatus{
			Conditions: []corev1.NodeCondition{
				{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			},
		},
	}

	g := NewNodeConditions(client, nil, testLogger(), true)
	section, err := g.Gather(context.Background(), nodeRef("node-1"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if section.Content == "" {
		t.Error("expected non-empty content with nil redactor")
	}
}

// --- Additional PVCStatus Tests ---

func TestPVCStatus_PVFetchError(t *testing.T) {
	client := newFakeKubeClient()
	client.pvcs["default/bound-pvc"] = &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "bound-pvc", Namespace: "default"},
		Spec: corev1.PersistentVolumeClaimSpec{
			VolumeName: "missing-pv",
		},
		Status: corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimBound},
	}
	// No PV registered — will trigger error

	g := NewPVCStatus(client, testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), pvcRef("default", "bound-pvc"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(section.Content, "error fetching PV") {
		t.Errorf("expected PV fetch error in content, got:\n%s", section.Content)
	}
}

func TestPVCStatus_WithPVCEvents(t *testing.T) {
	client := newFakeKubeClient()
	sc := "gp3"
	client.pvcs["default/events-pvc"] = &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "events-pvc", Namespace: "default"},
		Spec: corev1.PersistentVolumeClaimSpec{
			StorageClassName: &sc,
		},
		Status: corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimPending},
	}
	now := metav1.Now()
	fieldSel := "involvedObject.name=events-pvc,involvedObject.kind=PersistentVolumeClaim"
	client.events["default?"+fieldSel] = &corev1.EventList{
		Items: []corev1.Event{
			{
				ObjectMeta:   metav1.ObjectMeta{Name: "pvc-ev1"},
				Type:         "Warning",
				Reason:       "ProvisioningFailed",
				Message:      "Failed to provision volume with StorageClass gp3",
				LastTimestamp: now,
			},
		},
	}

	g := NewPVCStatus(client, testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), pvcRef("default", "events-pvc"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(section.Content, "ProvisioningFailed") {
		t.Errorf("expected PVC event in content, got:\n%s", section.Content)
	}
}

func TestPVCStatus_PVWithNodeAffinity(t *testing.T) {
	client := newFakeKubeClient()
	client.pvcs["default/affinity-pvc"] = &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "affinity-pvc", Namespace: "default"},
		Spec: corev1.PersistentVolumeClaimSpec{
			VolumeName: "affinity-pv",
		},
		Status: corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimBound},
	}
	client.pvs["affinity-pv"] = &corev1.PersistentVolume{
		ObjectMeta: metav1.ObjectMeta{Name: "affinity-pv"},
		Spec: corev1.PersistentVolumeSpec{
			NodeAffinity: &corev1.VolumeNodeAffinity{
				Required: &corev1.NodeSelector{
					NodeSelectorTerms: []corev1.NodeSelectorTerm{
						{
							MatchExpressions: []corev1.NodeSelectorRequirement{
								{Key: "topology.kubernetes.io/zone", Operator: corev1.NodeSelectorOpIn, Values: []string{"us-east-1a"}},
							},
						},
					},
				},
			},
		},
		Status: corev1.PersistentVolumeStatus{Phase: corev1.VolumeBound},
	}

	g := NewPVCStatus(client, testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), pvcRef("default", "affinity-pvc"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(section.Content, "node affinity") {
		t.Errorf("expected node affinity message, got:\n%s", section.Content)
	}
}

func TestPVCStatus_PodWithMultiplePVCs(t *testing.T) {
	client := newFakeKubeClient()
	client.pods["default/multi-pvc-pod"] = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "multi-pvc-pod", Namespace: "default"},
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{
				{Name: "data", VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "data-pvc"},
				}},
				{Name: "logs", VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "logs-pvc"},
				}},
			},
		},
	}
	client.pvcs["default/data-pvc"] = &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "data-pvc", Namespace: "default"},
		Status:     corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimBound},
	}
	client.pvcs["default/logs-pvc"] = &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "logs-pvc", Namespace: "default"},
		Status:     corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimPending},
	}

	g := NewPVCStatus(client, testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), podRef("default", "multi-pvc-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(section.Content, "data-pvc") || !strings.Contains(section.Content, "logs-pvc") {
		t.Errorf("expected both PVCs in content, got:\n%s", section.Content)
	}
}

func TestPVCStatus_PodWithPVCFetchError(t *testing.T) {
	client := newFakeKubeClient()
	client.pods["default/bad-pvc-pod"] = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "bad-pvc-pod", Namespace: "default"},
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{
				{Name: "data", VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: "missing-pvc"},
				}},
			},
		},
	}
	// No PVC registered — will trigger error

	g := NewPVCStatus(client, testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), podRef("default", "bad-pvc-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(section.Content, "Error gathering PVC") {
		t.Errorf("expected PVC error in content, got:\n%s", section.Content)
	}
}

func TestPVCStatus_NilRedactor(t *testing.T) {
	client := newFakeKubeClient()
	client.pvcs["default/pvc"] = &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "pvc", Namespace: "default"},
		Status:     corev1.PersistentVolumeClaimStatus{Phase: corev1.ClaimBound},
	}

	g := NewPVCStatus(client, nil, testLogger())
	section, err := g.Gather(context.Background(), pvcRef("default", "pvc"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if section.Content == "" {
		t.Error("expected non-empty content with nil redactor")
	}
}

// --- Additional NamespaceEvents Tests ---

func TestNamespaceEvents_NoEvents(t *testing.T) {
	g := NewNamespaceEvents(newFakeKubeClient(), testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), podRef("empty-ns", "pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(section.Content, "no events found") {
		t.Errorf("expected 'no events found', got: %s", section.Content)
	}
}

func TestNamespaceEvents_MultipleEvents(t *testing.T) {
	client := newFakeKubeClient()
	now := metav1.NewTime(time.Date(2026, 2, 8, 14, 0, 0, 0, time.UTC))
	client.events["multi-ns"] = &corev1.EventList{
		Items: []corev1.Event{
			{
				ObjectMeta:     metav1.ObjectMeta{Name: "ev1"},
				Type:           "Warning",
				Reason:         "FailedScheduling",
				Message:        "no nodes available",
				LastTimestamp:   now,
				InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "pod-a"},
				Count:          5,
			},
			{
				ObjectMeta:     metav1.ObjectMeta{Name: "ev2"},
				Type:           "Normal",
				Reason:         "Scheduled",
				Message:        "assigned to node-1",
				LastTimestamp:   now,
				InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "pod-b"},
			},
		},
	}

	g := NewNamespaceEvents(client, testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), model.ResourceRef{Kind: "Pod", Namespace: "multi-ns", Name: "pod"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, expected := range []string{
		"FailedScheduling",
		"(x5)",
		"Scheduled",
		"Pod/pod-a",
		"Pod/pod-b",
	} {
		if !strings.Contains(section.Content, expected) {
			t.Errorf("expected content to contain %q, got:\n%s", expected, section.Content)
		}
	}
}

func TestNamespaceEvents_RedactsSecrets(t *testing.T) {
	client := newFakeKubeClient()
	now := metav1.Now()
	client.events["secret-ns"] = &corev1.EventList{
		Items: []corev1.Event{
			{
				ObjectMeta:     metav1.ObjectMeta{Name: "ev1"},
				Type:           "Warning",
				Reason:         "AuthError",
				Message:        "failed with token=abc123xyz.secret",
				LastTimestamp:   now,
				InvolvedObject: corev1.ObjectReference{Kind: "Pod", Name: "pod-1"},
			},
		},
	}

	g := NewNamespaceEvents(client, testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), model.ResourceRef{Kind: "Pod", Namespace: "secret-ns", Name: "pod"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if strings.Contains(section.Content, "abc123xyz.secret") {
		t.Error("expected token to be redacted from namespace events")
	}
}

// --- Additional ReplicaSetStatus Tests ---

func TestReplicaSetStatus_PodNotFound(t *testing.T) {
	g := NewReplicaSetStatus(newFakeKubeClient(), testRedactor(), testLogger())
	_, err := g.Gather(context.Background(), podRef("default", "nonexistent"))
	if err == nil {
		t.Fatal("expected error for nonexistent pod")
	}
}

func TestReplicaSetStatus_ReplicaSetFetchError(t *testing.T) {
	client := newFakeKubeClient()
	client.pods["default/rs-err-pod"] = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "rs-err-pod",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "ReplicaSet", Name: "missing-rs", Controller: boolPtr(true)},
			},
		},
	}
	// No ReplicaSet registered — will trigger error

	g := NewReplicaSetStatus(client, testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), podRef("default", "rs-err-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(section.Content, "Error fetching ReplicaSet") {
		t.Errorf("expected RS fetch error in content, got:\n%s", section.Content)
	}
}

func TestReplicaSetStatus_DeploymentFetchError(t *testing.T) {
	client := newFakeKubeClient()
	client.pods["default/deploy-err-pod"] = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "deploy-err-pod",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "ReplicaSet", Name: "err-rs", Controller: boolPtr(true)},
			},
		},
	}
	client.replicaSets["default/err-rs"] = &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "err-rs",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "Deployment", Name: "missing-deploy", Controller: boolPtr(true)},
			},
		},
		Spec: appsv1.ReplicaSetSpec{Replicas: int32Ptr(1)},
	}
	// No Deployment registered — will trigger error

	g := NewReplicaSetStatus(client, testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), podRef("default", "deploy-err-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(section.Content, "Error fetching Deployment") {
		t.Errorf("expected Deployment fetch error in content, got:\n%s", section.Content)
	}
}

func TestReplicaSetStatus_StandalonePodNoOwners(t *testing.T) {
	client := newFakeKubeClient()
	client.pods["default/no-owner-pod"] = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "no-owner-pod",
			Namespace: "default",
		},
	}

	g := NewReplicaSetStatus(client, testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), podRef("default", "no-owner-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(section.Content, "no ReplicaSet owner") {
		t.Errorf("expected 'no ReplicaSet owner' message, got:\n%s", section.Content)
	}
}

func TestReplicaSetStatus_NilReplicaCount(t *testing.T) {
	client := newFakeKubeClient()
	client.pods["default/nil-rs-pod"] = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "nil-rs-pod",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "ReplicaSet", Name: "nil-rs", Controller: boolPtr(true)},
			},
		},
	}
	client.replicaSets["default/nil-rs"] = &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{Name: "nil-rs", Namespace: "default"},
		Spec:       appsv1.ReplicaSetSpec{Replicas: nil}, // nil replicas
		Status:     appsv1.ReplicaSetStatus{ReadyReplicas: 1},
	}

	g := NewReplicaSetStatus(client, testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), podRef("default", "nil-rs-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(section.Content, "Desired: 0") {
		t.Errorf("expected Desired: 0 for nil replicas, got:\n%s", section.Content)
	}
}

// --- Additional OwnerChain Tests ---

func TestOwnerChain_StatefulSetOwner(t *testing.T) {
	client := newFakeKubeClient()
	client.pods["default/sts-pod"] = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "sts-pod",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "StatefulSet", Name: "my-sts", Controller: boolPtr(true)},
			},
		},
	}
	client.statefulSets["default/my-sts"] = &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{Name: "my-sts", Namespace: "default"},
		Spec:       appsv1.StatefulSetSpec{Replicas: int32Ptr(3)},
		Status:     appsv1.StatefulSetStatus{ReadyReplicas: 2, AvailableReplicas: 2},
	}

	g := NewOwnerChain(client, testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), podRef("default", "sts-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(section.Content, "StatefulSet/my-sts") {
		t.Errorf("expected StatefulSet in chain, got:\n%s", section.Content)
	}
	if !strings.Contains(section.Content, "3 desired") {
		t.Errorf("expected StatefulSet status in chain, got:\n%s", section.Content)
	}
}

func TestOwnerChain_DaemonSetOwner(t *testing.T) {
	client := newFakeKubeClient()
	client.pods["kube-system/ds-pod"] = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ds-pod",
			Namespace: "kube-system",
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "DaemonSet", Name: "kube-proxy", Controller: boolPtr(true)},
			},
		},
	}
	client.daemonSets["kube-system/kube-proxy"] = &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{Name: "kube-proxy", Namespace: "kube-system"},
		Status: appsv1.DaemonSetStatus{
			DesiredNumberScheduled: 5,
			NumberReady:            4,
			NumberAvailable:        4,
		},
	}

	g := NewOwnerChain(client, testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), model.ResourceRef{Kind: "Pod", Namespace: "kube-system", Name: "ds-pod"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(section.Content, "DaemonSet/kube-proxy") {
		t.Errorf("expected DaemonSet in chain, got:\n%s", section.Content)
	}
	if !strings.Contains(section.Content, "Desired: 5") {
		t.Errorf("expected DaemonSet status in chain, got:\n%s", section.Content)
	}
}

func TestOwnerChain_PodNotFound(t *testing.T) {
	g := NewOwnerChain(newFakeKubeClient(), testRedactor(), testLogger())
	_, err := g.Gather(context.Background(), podRef("default", "nonexistent"))
	if err == nil {
		t.Fatal("expected error for nonexistent pod")
	}
}

func TestOwnerChain_OwnerFetchError(t *testing.T) {
	client := newFakeKubeClient()
	client.pods["default/err-pod"] = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "err-pod",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "ReplicaSet", Name: "missing-rs", Controller: boolPtr(true)},
			},
		},
	}
	// No ReplicaSet registered — will trigger error in fetchOwnerRefs

	g := NewOwnerChain(client, testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), podRef("default", "err-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(section.Content, "error fetching") {
		t.Errorf("expected error message in content, got:\n%s", section.Content)
	}
}

func TestOwnerChain_NonControllerOwner(t *testing.T) {
	client := newFakeKubeClient()
	client.pods["default/non-ctrl-pod"] = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "non-ctrl-pod",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "ReplicaSet", Name: "non-ctrl-rs", Controller: boolPtr(false)},
			},
		},
	}
	client.replicaSets["default/non-ctrl-rs"] = &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "non-ctrl-rs",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "Deployment", Name: "should-not-follow", Controller: boolPtr(true)},
			},
		},
		Spec: appsv1.ReplicaSetSpec{Replicas: int32Ptr(1)},
	}

	g := NewOwnerChain(client, testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), podRef("default", "non-ctrl-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should show the ReplicaSet but NOT follow to Deployment since controller=false
	if !strings.Contains(section.Content, "ReplicaSet/non-ctrl-rs") {
		t.Errorf("expected ReplicaSet in chain")
	}
	if strings.Contains(section.Content, "should-not-follow") {
		t.Errorf("should not follow owner chain when controller=false")
	}
}

func TestOwnerChain_NilRedactor(t *testing.T) {
	client := newFakeKubeClient()
	client.pods["default/pod"] = &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "pod", Namespace: "default"},
	}

	g := NewOwnerChain(client, nil, testLogger())
	section, err := g.Gather(context.Background(), podRef("default", "pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if section.Content == "" {
		t.Error("expected non-empty content with nil redactor")
	}
}

// --- Additional KarpenterState Tests ---

func TestKarpenterState_WithKarpenterEvents(t *testing.T) {
	client := newFakeKubeClient()
	now := metav1.Now()
	client.events["kube-system"] = &corev1.EventList{
		Items: []corev1.Event{
			{
				ObjectMeta:   metav1.ObjectMeta{Name: "karp-ev1"},
				Type:         "Normal",
				Reason:       "Provisioning",
				Message:      "Provisioning node for pod default/pending-pod",
				LastTimestamp: now,
				Source:        corev1.EventSource{Component: "karpenter"},
				InvolvedObject: corev1.ObjectReference{
					Kind: "NodeClaim", Name: "claim-abc",
				},
			},
			{
				ObjectMeta:   metav1.ObjectMeta{Name: "non-karp"},
				Type:         "Normal",
				Reason:       "LeaderElection",
				Message:      "elected leader",
				LastTimestamp: now,
				InvolvedObject: corev1.ObjectReference{
					Kind: "Pod", Name: "some-system-pod",
				},
			},
		},
	}

	kf := &fakeKarpenterFetcher{
		nodePools:  "  default: Ready\n",
		nodeClaims: "  claim-abc: Provisioning\n",
	}

	g := NewKarpenterState(client, kf, testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), podRef("default", "pending-pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should include karpenter events but not non-karpenter events
	if !strings.Contains(section.Content, "Karpenter Events:") {
		t.Errorf("expected Karpenter Events section")
	}
	if !strings.Contains(section.Content, "Provisioning") {
		t.Errorf("expected provisioning event")
	}
	if strings.Contains(section.Content, "LeaderElection") {
		t.Errorf("should not include non-Karpenter events")
	}
}

func TestKarpenterState_NoKarpenterEvents(t *testing.T) {
	client := newFakeKubeClient()
	client.events["kube-system"] = &corev1.EventList{
		Items: []corev1.Event{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "non-karp"},
				Type:       "Normal",
				Reason:     "LeaderElection",
				Message:    "elected",
				InvolvedObject: corev1.ObjectReference{
					Kind: "Pod", Name: "some-pod",
				},
			},
		},
	}

	kf := &fakeKarpenterFetcher{
		nodePools:  "  default: Ready\n",
		nodeClaims: "",
	}

	g := NewKarpenterState(client, kf, testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), podRef("default", "pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(section.Content, "no Karpenter events found") {
		t.Errorf("expected 'no Karpenter events found', got:\n%s", section.Content)
	}
}

func TestKarpenterState_NilRedactor(t *testing.T) {
	kf := &fakeKarpenterFetcher{nodePools: "test\n", nodeClaims: "test\n"}
	g := NewKarpenterState(newFakeKubeClient(), kf, nil, testLogger())
	section, err := g.Gather(context.Background(), podRef("default", "pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if section.Content == "" {
		t.Error("expected non-empty content with nil redactor")
	}
}

func TestKarpenterState_PartialErrors(t *testing.T) {
	client := newFakeKubeClient()
	kf := &fakeKarpenterFetcher{
		nodePools:  "  default: Ready\n",
		ncError:    fmt.Errorf("permission denied"),
	}

	g := NewKarpenterState(client, kf, testRedactor(), testLogger())
	section, err := g.Gather(context.Background(), podRef("default", "pod"))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// NodePools should succeed
	if !strings.Contains(section.Content, "default: Ready") {
		t.Errorf("expected NodePools data")
	}
	// NodeClaims should show error
	if !strings.Contains(section.Content, "Error listing NodeClaims") {
		t.Errorf("expected NodeClaims error, got:\n%s", section.Content)
	}
}

// --- Cross-gatherer redaction verification ---

func TestAllGatherers_RedactBearerTokens(t *testing.T) {
	// Verify that the redactor handles Bearer tokens across different gatherers
	r, err := redact.New(nil, redact.WithLogger(testLogger()))
	if err != nil {
		t.Fatalf("failed to create redactor: %v", err)
	}

	input := "Authorization: Bearer eyJhbGciOiJIUzI1NiJ9.sensitive"
	result := r.Redact(input)
	if strings.Contains(result, "eyJhbGciOiJIUzI1NiJ9") {
		t.Error("expected bearer token to be redacted")
	}
}

func TestAllGatherers_RedactAWSKeys(t *testing.T) {
	r, err := redact.New(nil, redact.WithLogger(testLogger()))
	if err != nil {
		t.Fatalf("failed to create redactor: %v", err)
	}

	input := "AWS key: AKIAIOSFODNN7EXAMPLE"
	result := r.Redact(input)
	if strings.Contains(result, "AKIAIOSFODNN7EXAMPLE") {
		t.Error("expected AWS key to be redacted")
	}
}

func TestAllGatherers_CustomRedactPatterns(t *testing.T) {
	r, err := redact.New([]string{`SSN:\s*\d{3}-\d{2}-\d{4}`}, redact.WithLogger(testLogger()))
	if err != nil {
		t.Fatalf("failed to create redactor: %v", err)
	}

	input := "User SSN: 123-45-6789"
	result := r.Redact(input)
	if strings.Contains(result, "123-45-6789") {
		t.Error("expected SSN to be redacted with custom pattern")
	}
}

// --- NilLogger tests for all gatherers ---

func TestAllGatherers_NilLogger(t *testing.T) {
	client := newFakeKubeClient()
	r := testRedactor()

	// All constructors should handle nil logger gracefully
	tests := []struct {
		name string
		fn   func() Gatherer
	}{
		{"PodDescribe", func() Gatherer { return NewPodDescribe(client, r, nil) }},
		{"PodEvents", func() Gatherer { return NewPodEvents(client, r, nil) }},
		{"PodLogs", func() Gatherer { return NewPodLogs(client, r, nil, 100, true) }},
		{"NodeConditions", func() Gatherer { return NewNodeConditions(client, r, nil, true) }},
		{"PVCStatus", func() Gatherer { return NewPVCStatus(client, r, nil) }},
		{"NamespaceEvents", func() Gatherer { return NewNamespaceEvents(client, r, nil) }},
		{"ReplicaSetStatus", func() Gatherer { return NewReplicaSetStatus(client, r, nil) }},
		{"OwnerChain", func() Gatherer { return NewOwnerChain(client, r, nil) }},
		{"KarpenterState", func() Gatherer {
			return NewKarpenterState(client, &fakeKarpenterFetcher{}, r, nil)
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			g := tt.fn()
			if g == nil {
				t.Fatal("constructor returned nil with nil logger")
			}
			if g.Name() != tt.name {
				t.Errorf("expected name %q, got %q", tt.name, g.Name())
			}
		})
	}
}
