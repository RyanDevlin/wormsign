package gatherer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	v1alpha1 "github.com/k8s-wormsign/k8s-wormsign/api/v1alpha1"
	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

// --- Mock ResourceFetcher ---

type mockFetcher struct {
	resources map[string][]byte // key: "apiVersion/resource/namespace/name"
	lists     map[string][]byte // key: "apiVersion/resource/namespace"
	logs      map[string]string // key: "namespace/pod/container"
	getErr    error
	listErr   error
	logsErr   error
}

func newMockFetcher() *mockFetcher {
	return &mockFetcher{
		resources: make(map[string][]byte),
		lists:     make(map[string][]byte),
		logs:      make(map[string]string),
	}
}

func (m *mockFetcher) GetResource(_ context.Context, apiVersion, resource, namespace, name string) ([]byte, error) {
	if m.getErr != nil {
		return nil, m.getErr
	}
	key := apiVersion + "/" + resource + "/" + namespace + "/" + name
	data, ok := m.resources[key]
	if !ok {
		return nil, fmt.Errorf("resource not found: %s", key)
	}
	return data, nil
}

func (m *mockFetcher) ListResources(_ context.Context, apiVersion, resource, namespace string) ([]byte, error) {
	if m.listErr != nil {
		return nil, m.listErr
	}
	key := apiVersion + "/" + resource + "/" + namespace
	data, ok := m.lists[key]
	if !ok {
		return nil, fmt.Errorf("list not found: %s", key)
	}
	return data, nil
}

func (m *mockFetcher) GetLogs(_ context.Context, namespace, podName, container string, tailLines *int64, previous bool) (string, error) {
	if m.logsErr != nil {
		return "", m.logsErr
	}
	key := namespace + "/" + podName + "/" + container
	logs, ok := m.logs[key]
	if !ok {
		return "", fmt.Errorf("logs not found: %s", key)
	}
	return logs, nil
}

// --- NewCustomGatherer validation tests ---

func TestNewCustomGatherer_Valid(t *testing.T) {
	fetcher := newMockFetcher()
	spec := v1alpha1.WormsignGathererSpec{
		TriggerOn: v1alpha1.GathererTrigger{
			ResourceKinds: []string{"Pod"},
			Detectors:     []string{"PodCrashLoop"},
		},
		Collect: []v1alpha1.CollectAction{
			{
				Type:       v1alpha1.CollectTypeResource,
				APIVersion: "v1",
				Resource:   "pods",
				Name:       "{resource.name}",
			},
		},
	}

	cg, err := NewCustomGatherer("test/my-gatherer", spec, fetcher, silentLogger())
	if err != nil {
		t.Fatalf("NewCustomGatherer() error = %v", err)
	}
	if cg.Name() != "test/my-gatherer" {
		t.Errorf("Name() = %q, want %q", cg.Name(), "test/my-gatherer")
	}
}

func TestNewCustomGatherer_EmptyName(t *testing.T) {
	_, err := NewCustomGatherer("", v1alpha1.WormsignGathererSpec{
		Collect: []v1alpha1.CollectAction{{Type: v1alpha1.CollectTypeLogs}},
	}, newMockFetcher(), nil)
	if err == nil {
		t.Fatal("expected error for empty name")
	}
	if !strings.Contains(err.Error(), "name must not be empty") {
		t.Errorf("error = %q, want substring %q", err.Error(), "name must not be empty")
	}
}

func TestNewCustomGatherer_NilFetcher(t *testing.T) {
	_, err := NewCustomGatherer("test", v1alpha1.WormsignGathererSpec{
		Collect: []v1alpha1.CollectAction{{Type: v1alpha1.CollectTypeLogs}},
	}, nil, nil)
	if err == nil {
		t.Fatal("expected error for nil fetcher")
	}
	if !strings.Contains(err.Error(), "resource fetcher must not be nil") {
		t.Errorf("error = %q, want substring %q", err.Error(), "resource fetcher must not be nil")
	}
}

func TestNewCustomGatherer_EmptyCollect(t *testing.T) {
	_, err := NewCustomGatherer("test", v1alpha1.WormsignGathererSpec{
		Collect: nil,
	}, newMockFetcher(), nil)
	if err == nil {
		t.Fatal("expected error for empty collect")
	}
	if !strings.Contains(err.Error(), "at least one collect action") {
		t.Errorf("error = %q, want substring %q", err.Error(), "at least one collect action")
	}
}

func TestNewCustomGatherer_InvalidResourceAction(t *testing.T) {
	tests := []struct {
		name    string
		action  v1alpha1.CollectAction
		wantErr string
	}{
		{
			name:    "missing apiVersion",
			action:  v1alpha1.CollectAction{Type: v1alpha1.CollectTypeResource, Resource: "pods", Name: "test"},
			wantErr: "apiVersion is required",
		},
		{
			name:    "missing resource",
			action:  v1alpha1.CollectAction{Type: v1alpha1.CollectTypeResource, APIVersion: "v1", Name: "test"},
			wantErr: "resource is required",
		},
		{
			name:    "missing name when not listAll",
			action:  v1alpha1.CollectAction{Type: v1alpha1.CollectTypeResource, APIVersion: "v1", Resource: "pods"},
			wantErr: "name is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewCustomGatherer("test", v1alpha1.WormsignGathererSpec{
				Collect: []v1alpha1.CollectAction{tt.action},
			}, newMockFetcher(), nil)
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want substring %q", err.Error(), tt.wantErr)
			}
		})
	}
}

func TestNewCustomGatherer_ListAllDoesNotRequireName(t *testing.T) {
	_, err := NewCustomGatherer("test", v1alpha1.WormsignGathererSpec{
		Collect: []v1alpha1.CollectAction{
			{
				Type:       v1alpha1.CollectTypeResource,
				APIVersion: "v1",
				Resource:   "pods",
				ListAll:    true,
			},
		},
	}, newMockFetcher(), nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// --- TriggerMatcher tests ---

func TestCustomGatherer_TriggerMatcher(t *testing.T) {
	fetcher := newMockFetcher()
	spec := v1alpha1.WormsignGathererSpec{
		TriggerOn: v1alpha1.GathererTrigger{
			ResourceKinds: []string{"Pod", "Node"},
			Detectors:     []string{"PodCrashLoop"},
		},
		Collect: []v1alpha1.CollectAction{
			{Type: v1alpha1.CollectTypeLogs},
		},
	}

	cg, err := NewCustomGatherer("test-trigger", spec, fetcher, silentLogger())
	if err != nil {
		t.Fatalf("NewCustomGatherer() error = %v", err)
	}

	kinds := cg.ResourceKinds()
	if len(kinds) != 2 || kinds[0] != "Pod" || kinds[1] != "Node" {
		t.Errorf("ResourceKinds() = %v, want [Pod, Node]", kinds)
	}

	detectors := cg.DetectorNames()
	if len(detectors) != 1 || detectors[0] != "PodCrashLoop" {
		t.Errorf("DetectorNames() = %v, want [PodCrashLoop]", detectors)
	}

	// Test that it matches correctly via MatchesTrigger.
	if !MatchesTrigger(cg, "Pod", "PodCrashLoop") {
		t.Error("should match Pod/PodCrashLoop")
	}
	if MatchesTrigger(cg, "Pod", "NodeNotReady") {
		t.Error("should not match Pod/NodeNotReady")
	}
	if MatchesTrigger(cg, "Deployment", "PodCrashLoop") {
		t.Error("should not match Deployment/PodCrashLoop")
	}
}

// --- Gather tests ---

func TestCustomGatherer_GatherResource(t *testing.T) {
	fetcher := newMockFetcher()
	podJSON, _ := json.Marshal(map[string]interface{}{
		"kind":       "Pod",
		"apiVersion": "v1",
		"metadata": map[string]interface{}{
			"name":      "my-pod",
			"namespace": "payments",
		},
		"status": map[string]interface{}{
			"phase": "Running",
		},
	})
	fetcher.resources["v1/pods/payments/my-pod"] = podJSON

	spec := v1alpha1.WormsignGathererSpec{
		Description: "Collects pod details",
		Collect: []v1alpha1.CollectAction{
			{
				Type:       v1alpha1.CollectTypeResource,
				APIVersion: "v1",
				Resource:   "pods",
				Name:       "{resource.name}",
				Namespace:  "{resource.namespace}",
			},
		},
	}

	cg, err := NewCustomGatherer("test/pod-gatherer", spec, fetcher, silentLogger())
	if err != nil {
		t.Fatalf("NewCustomGatherer() error = %v", err)
	}

	ref := model.ResourceRef{Kind: "Pod", Namespace: "payments", Name: "my-pod"}
	section, err := cg.Gather(context.Background(), ref)
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}

	if section.GathererName != "test/pod-gatherer" {
		t.Errorf("GathererName = %q, want %q", section.GathererName, "test/pod-gatherer")
	}
	if section.Title != "Collects pod details" {
		t.Errorf("Title = %q, want %q", section.Title, "Collects pod details")
	}
	if !strings.Contains(section.Content, "my-pod") {
		t.Errorf("Content should contain pod data, got: %s", section.Content)
	}
	if section.Error != "" {
		t.Errorf("unexpected Error: %s", section.Error)
	}
}

func TestCustomGatherer_GatherResourceList(t *testing.T) {
	fetcher := newMockFetcher()
	listJSON, _ := json.Marshal(map[string]interface{}{
		"items": []interface{}{
			map[string]interface{}{"metadata": map[string]interface{}{"name": "dr-1"}},
			map[string]interface{}{"metadata": map[string]interface{}{"name": "dr-2"}},
		},
	})
	fetcher.lists["networking.istio.io/v1/destinationrules/payments"] = listJSON

	spec := v1alpha1.WormsignGathererSpec{
		Collect: []v1alpha1.CollectAction{
			{
				Type:       v1alpha1.CollectTypeResource,
				APIVersion: "networking.istio.io/v1",
				Resource:   "destinationrules",
				ListAll:    true,
			},
		},
	}

	cg, err := NewCustomGatherer("test/list-gatherer", spec, fetcher, silentLogger())
	if err != nil {
		t.Fatalf("NewCustomGatherer() error = %v", err)
	}

	ref := model.ResourceRef{Kind: "Pod", Namespace: "payments", Name: "my-pod"}
	section, err := cg.Gather(context.Background(), ref)
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}

	if !strings.Contains(section.Content, "dr-1") || !strings.Contains(section.Content, "dr-2") {
		t.Errorf("Content should contain list data, got: %s", section.Content)
	}
}

func TestCustomGatherer_GatherLogs(t *testing.T) {
	fetcher := newMockFetcher()
	fetcher.logs["default/my-pod/app"] = "2026-02-08 ERROR connection refused\n2026-02-08 WARN retrying"

	tailLines := int64(50)
	spec := v1alpha1.WormsignGathererSpec{
		Collect: []v1alpha1.CollectAction{
			{
				Type:      v1alpha1.CollectTypeLogs,
				Container: "app",
				TailLines: &tailLines,
			},
		},
	}

	cg, err := NewCustomGatherer("test/log-gatherer", spec, fetcher, silentLogger())
	if err != nil {
		t.Fatalf("NewCustomGatherer() error = %v", err)
	}

	ref := model.ResourceRef{Kind: "Pod", Namespace: "default", Name: "my-pod"}
	section, err := cg.Gather(context.Background(), ref)
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}

	if !strings.Contains(section.Content, "ERROR connection refused") {
		t.Errorf("Content should contain logs, got: %s", section.Content)
	}
}

func TestCustomGatherer_GatherLogsNonPod(t *testing.T) {
	fetcher := newMockFetcher()
	spec := v1alpha1.WormsignGathererSpec{
		Collect: []v1alpha1.CollectAction{
			{
				Type:      v1alpha1.CollectTypeLogs,
				Container: "app",
			},
		},
	}

	cg, err := NewCustomGatherer("test/log-nonpod", spec, fetcher, silentLogger())
	if err != nil {
		t.Fatalf("NewCustomGatherer() error = %v", err)
	}

	ref := model.ResourceRef{Kind: "Node", Name: "node-1"}
	section, err := cg.Gather(context.Background(), ref)
	if err != nil {
		t.Fatalf("Gather() should not return error, error = %v", err)
	}

	// The section should contain an error for the logs action.
	if section.Error == "" {
		t.Error("expected error when collecting logs from non-Pod resource")
	}
	if !strings.Contains(section.Error, "requires a Pod resource") {
		t.Errorf("error = %q, want substring %q", section.Error, "requires a Pod resource")
	}
}

func TestCustomGatherer_GatherMultipleActions(t *testing.T) {
	fetcher := newMockFetcher()

	podJSON, _ := json.Marshal(map[string]interface{}{
		"metadata": map[string]interface{}{"name": "my-pod"},
		"status":   map[string]interface{}{"phase": "Failed"},
	})
	fetcher.resources["v1/pods/default/my-pod"] = podJSON
	fetcher.logs["default/my-pod/main"] = "fatal: out of memory"

	spec := v1alpha1.WormsignGathererSpec{
		Description: "Pod details and logs",
		Collect: []v1alpha1.CollectAction{
			{
				Type:       v1alpha1.CollectTypeResource,
				APIVersion: "v1",
				Resource:   "pods",
				Name:       "{resource.name}",
			},
			{
				Type:      v1alpha1.CollectTypeLogs,
				Container: "main",
			},
		},
	}

	cg, err := NewCustomGatherer("test/multi", spec, fetcher, silentLogger())
	if err != nil {
		t.Fatalf("NewCustomGatherer() error = %v", err)
	}

	ref := model.ResourceRef{Kind: "Pod", Namespace: "default", Name: "my-pod"}
	section, err := cg.Gather(context.Background(), ref)
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}

	// Content should contain both resource data and logs, separated by "---".
	if !strings.Contains(section.Content, "my-pod") {
		t.Error("content should contain pod data")
	}
	if !strings.Contains(section.Content, "out of memory") {
		t.Error("content should contain log data")
	}
	if !strings.Contains(section.Content, "---") {
		t.Error("content should contain separator between actions")
	}
}

func TestCustomGatherer_PartialActionFailure(t *testing.T) {
	fetcher := newMockFetcher()
	// Only set up logs, not the resource — resource fetch will fail.
	fetcher.logs["default/my-pod/main"] = "some logs"

	spec := v1alpha1.WormsignGathererSpec{
		Collect: []v1alpha1.CollectAction{
			{
				Type:       v1alpha1.CollectTypeResource,
				APIVersion: "v1",
				Resource:   "pods",
				Name:       "{resource.name}",
			},
			{
				Type:      v1alpha1.CollectTypeLogs,
				Container: "main",
			},
		},
	}

	cg, err := NewCustomGatherer("test/partial", spec, fetcher, silentLogger())
	if err != nil {
		t.Fatalf("NewCustomGatherer() error = %v", err)
	}

	ref := model.ResourceRef{Kind: "Pod", Namespace: "default", Name: "my-pod"}
	section, err := cg.Gather(context.Background(), ref)
	if err != nil {
		t.Fatalf("Gather() should not return top-level error, got: %v", err)
	}

	// Should have partial content (logs) and an error for the failed resource fetch.
	if !strings.Contains(section.Content, "some logs") {
		t.Error("content should contain successful log data")
	}
	if section.Error == "" {
		t.Error("expected error for failed resource fetch")
	}
	if !strings.Contains(section.Error, "resource not found") {
		t.Errorf("error = %q, want substring %q", section.Error, "resource not found")
	}
}

func TestCustomGatherer_AllActionsFail(t *testing.T) {
	fetcher := newMockFetcher()
	// No resources or logs set up — everything fails.

	spec := v1alpha1.WormsignGathererSpec{
		Collect: []v1alpha1.CollectAction{
			{
				Type:       v1alpha1.CollectTypeResource,
				APIVersion: "v1",
				Resource:   "pods",
				Name:       "{resource.name}",
			},
			{
				Type:      v1alpha1.CollectTypeLogs,
				Container: "main",
			},
		},
	}

	cg, err := NewCustomGatherer("test/allfail", spec, fetcher, silentLogger())
	if err != nil {
		t.Fatalf("NewCustomGatherer() error = %v", err)
	}

	ref := model.ResourceRef{Kind: "Pod", Namespace: "default", Name: "my-pod"}
	section, err := cg.Gather(context.Background(), ref)
	if err != nil {
		t.Fatalf("Gather() should not return top-level error, got: %v", err)
	}

	if section.Content != "" {
		t.Errorf("expected empty content when all actions fail, got: %q", section.Content)
	}
	if section.Error == "" {
		t.Error("expected error when all actions fail")
	}
}

func TestCustomGatherer_JSONPathExtraction(t *testing.T) {
	fetcher := newMockFetcher()
	podJSON, _ := json.Marshal(map[string]interface{}{
		"metadata": map[string]interface{}{
			"name":      "my-pod",
			"namespace": "default",
			"annotations": map[string]interface{}{
				"sidecar.istio.io/status": "injected",
				"other": "value",
			},
		},
	})
	fetcher.resources["v1/pods/default/my-pod"] = podJSON

	spec := v1alpha1.WormsignGathererSpec{
		Collect: []v1alpha1.CollectAction{
			{
				Type:       v1alpha1.CollectTypeResource,
				APIVersion: "v1",
				Resource:   "pods",
				Name:       "{resource.name}",
				JSONPath:   ".metadata.annotations",
			},
		},
	}

	cg, err := NewCustomGatherer("test/jsonpath", spec, fetcher, silentLogger())
	if err != nil {
		t.Fatalf("NewCustomGatherer() error = %v", err)
	}

	ref := model.ResourceRef{Kind: "Pod", Namespace: "default", Name: "my-pod"}
	section, err := cg.Gather(context.Background(), ref)
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}

	if !strings.Contains(section.Content, "sidecar.istio.io/status") {
		t.Errorf("Content should contain annotations, got: %s", section.Content)
	}
}

func TestCustomGatherer_NamespaceDefaultsToResource(t *testing.T) {
	fetcher := newMockFetcher()
	podJSON := []byte(`{"kind":"Pod","metadata":{"name":"test-pod"}}`)
	fetcher.resources["v1/pods/my-namespace/test-pod"] = podJSON

	spec := v1alpha1.WormsignGathererSpec{
		Collect: []v1alpha1.CollectAction{
			{
				Type:       v1alpha1.CollectTypeResource,
				APIVersion: "v1",
				Resource:   "pods",
				Name:       "{resource.name}",
				// Namespace intentionally omitted — should default to resource namespace.
			},
		},
	}

	cg, err := NewCustomGatherer("test/ns-default", spec, fetcher, silentLogger())
	if err != nil {
		t.Fatalf("NewCustomGatherer() error = %v", err)
	}

	ref := model.ResourceRef{Kind: "Pod", Namespace: "my-namespace", Name: "test-pod"}
	section, err := cg.Gather(context.Background(), ref)
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}

	if section.Error != "" {
		t.Errorf("unexpected error: %s", section.Error)
	}
	if !strings.Contains(section.Content, "test-pod") {
		t.Errorf("content should contain pod data, got: %s", section.Content)
	}
}

func TestCustomGatherer_TitleFallbackToName(t *testing.T) {
	fetcher := newMockFetcher()
	fetcher.logs["default/pod/app"] = "some logs"

	spec := v1alpha1.WormsignGathererSpec{
		// Description intentionally empty.
		Collect: []v1alpha1.CollectAction{
			{
				Type:      v1alpha1.CollectTypeLogs,
				Container: "app",
			},
		},
	}

	cg, err := NewCustomGatherer("test/no-desc", spec, fetcher, silentLogger())
	if err != nil {
		t.Fatalf("NewCustomGatherer() error = %v", err)
	}

	ref := model.ResourceRef{Kind: "Pod", Namespace: "default", Name: "pod"}
	section, err := cg.Gather(context.Background(), ref)
	if err != nil {
		t.Fatalf("Gather() error = %v", err)
	}

	if section.Title != "test/no-desc" {
		t.Errorf("Title = %q, want %q (should fall back to name)", section.Title, "test/no-desc")
	}
}

// --- splitJSONPath tests ---

func TestSplitJSONPath(t *testing.T) {
	tests := []struct {
		name string
		path string
		want []string
	}{
		{
			name: "simple",
			path: "metadata.name",
			want: []string{"metadata", "name"},
		},
		{
			name: "with wildcard",
			path: "metadata.annotations.*",
			want: []string{"metadata", "annotations", "*"},
		},
		{
			name: "escaped dots",
			path: `metadata.annotations.sidecar\.istio\.io/status`,
			want: []string{"metadata", "annotations", "sidecar.istio.io/status"},
		},
		{
			name: "single segment",
			path: "status",
			want: []string{"status"},
		},
		{
			name: "empty",
			path: "",
			want: nil,
		},
		{
			name: "leading dot stripped in caller",
			path: "metadata.name",
			want: []string{"metadata", "name"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := splitJSONPath(tt.path)
			if len(got) != len(tt.want) {
				t.Fatalf("splitJSONPath(%q) = %v (len %d), want %v (len %d)",
					tt.path, got, len(got), tt.want, len(tt.want))
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("segment[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// --- extractJSONPath tests ---

func TestExtractJSONPath(t *testing.T) {
	data, _ := json.Marshal(map[string]interface{}{
		"metadata": map[string]interface{}{
			"name":      "test-pod",
			"namespace": "default",
			"annotations": map[string]interface{}{
				"key1": "value1",
				"key2": "value2",
			},
		},
		"status": map[string]interface{}{
			"phase": "Running",
		},
	})

	tests := []struct {
		name    string
		path    string
		want    string
		wantErr bool
	}{
		{
			name: "simple field",
			path: ".metadata.name",
			want: `"test-pod"`,
		},
		{
			name: "nested field",
			path: ".status.phase",
			want: `"Running"`,
		},
		{
			name: "object field",
			path: ".metadata.annotations",
			want: "key1", // Just check it contains annotation data.
		},
		{
			name:    "nonexistent field",
			path:    ".metadata.missing",
			wantErr: true,
		},
		{
			name:    "wrong type traversal",
			path:    ".metadata.name.sub",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractJSONPath(data, tt.path)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !strings.Contains(got, tt.want) {
				t.Errorf("extractJSONPath() = %q, want to contain %q", got, tt.want)
			}
		})
	}
}

func TestExtractJSONPath_Wildcard(t *testing.T) {
	data, _ := json.Marshal(map[string]interface{}{
		"metadata": map[string]interface{}{
			"annotations": map[string]interface{}{
				"key1": "value1",
			},
		},
	})

	got, err := extractJSONPath(data, ".metadata.annotations.*")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, "key1") {
		t.Errorf("wildcard result should contain annotation data, got: %s", got)
	}
}

func TestExtractJSONPath_InvalidJSON(t *testing.T) {
	_, err := extractJSONPath([]byte("not json"), ".foo")
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// --- Compile-time interface checks ---

func TestCustomGatherer_ImplementsInterfaces(t *testing.T) {
	var _ Gatherer = (*CustomGatherer)(nil)
	var _ TriggerMatcher = (*CustomGatherer)(nil)
}
