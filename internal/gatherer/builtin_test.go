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
