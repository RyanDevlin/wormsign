package prompt

import (
	"strings"
	"testing"
	"time"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

func TestDefaultSystemPrompt_NotEmpty(t *testing.T) {
	if DefaultSystemPrompt == "" {
		t.Fatal("DefaultSystemPrompt should not be empty")
	}
}

func TestDefaultSystemPrompt_ContainsKeyPhrases(t *testing.T) {
	phrases := []string{
		"Kubernetes diagnostics engine",
		"Wormsign",
		"Root Cause Analysis",
		"rootCause",
		"severity",
		"category",
		"confidence",
		"remediation",
		"OOMKilled",
		"CrashLoopBackOff",
	}
	for _, p := range phrases {
		if !strings.Contains(DefaultSystemPrompt, p) {
			t.Errorf("DefaultSystemPrompt should contain %q", p)
		}
	}
}

func TestNewBuilder_DefaultPrompt(t *testing.T) {
	b := NewBuilder("", "")
	if b.SystemPrompt() != DefaultSystemPrompt {
		t.Error("SystemPrompt() should equal DefaultSystemPrompt when no override/append")
	}
}

func TestNewBuilder_Override(t *testing.T) {
	override := "Custom system prompt for testing"
	b := NewBuilder(override, "")
	if b.SystemPrompt() != override {
		t.Errorf("SystemPrompt() = %q, want %q", b.SystemPrompt(), override)
	}
}

func TestNewBuilder_Append(t *testing.T) {
	appendText := "Additional context: this cluster uses Karpenter."
	b := NewBuilder("", appendText)
	if !strings.HasPrefix(b.SystemPrompt(), DefaultSystemPrompt) {
		t.Error("SystemPrompt() should start with DefaultSystemPrompt")
	}
	if !strings.HasSuffix(b.SystemPrompt(), appendText) {
		t.Error("SystemPrompt() should end with the appended text")
	}
	if !strings.Contains(b.SystemPrompt(), "\n\n") {
		t.Error("SystemPrompt() should have separator between default and append")
	}
}

func TestNewBuilder_OverrideAndAppend(t *testing.T) {
	override := "Custom base prompt"
	appendText := "Plus this context"
	b := NewBuilder(override, appendText)
	want := override + "\n\n" + appendText
	if b.SystemPrompt() != want {
		t.Errorf("SystemPrompt() = %q, want %q", b.SystemPrompt(), want)
	}
}

func TestNewBuilder_WhitespaceOnlyOverride(t *testing.T) {
	b := NewBuilder("   \n\t  ", "")
	if b.SystemPrompt() != DefaultSystemPrompt {
		t.Error("whitespace-only override should use default")
	}
}

func TestNewBuilder_WhitespaceOnlyAppend(t *testing.T) {
	b := NewBuilder("", "   \n\t  ")
	if b.SystemPrompt() != DefaultSystemPrompt {
		t.Error("whitespace-only append should not modify default")
	}
}

func TestBuildUserPrompt_FaultEvent(t *testing.T) {
	b := NewBuilder("", "")

	bundle := model.DiagnosticBundle{
		FaultEvent: &model.FaultEvent{
			ID:           "fe-1",
			DetectorName: "PodCrashLoop",
			Severity:     model.SeverityCritical,
			Timestamp:    time.Date(2026, 2, 8, 14, 30, 0, 0, time.UTC),
			Resource: model.ResourceRef{
				Kind:      "Pod",
				Namespace: "default",
				Name:      "web-app-abc123",
			},
			Description: "Container restarted 5 times in 10m",
			Labels: map[string]string{
				"app": "web-app",
			},
		},
		Timestamp: time.Now().UTC(),
		Sections: []model.DiagnosticSection{
			{
				GathererName: "PodEvents",
				Title:        "Pod Events",
				Content:      "Event: Back-off restarting failed container",
				Format:       "text",
			},
			{
				GathererName: "PodLogs",
				Title:        "Container Logs",
				Content:      `{"level":"error","msg":"connection refused"}`,
				Format:       "json",
			},
		},
	}

	prompt := b.BuildUserPrompt(bundle)

	requiredParts := []string{
		"# Diagnostic Bundle",
		"## Fault Event",
		"PodCrashLoop",
		"critical",
		"Pod/default/web-app-abc123",
		"Container restarted 5 times in 10m",
		"## Diagnostic Data",
		"### Pod Events",
		"Back-off restarting failed container",
		"### Container Logs",
		"```json",
		"connection refused",
	}
	for _, part := range requiredParts {
		if !strings.Contains(prompt, part) {
			t.Errorf("BuildUserPrompt() should contain %q", part)
		}
	}
}

func TestBuildUserPrompt_SuperEvent(t *testing.T) {
	b := NewBuilder("", "")

	bundle := model.DiagnosticBundle{
		SuperEvent: &model.SuperEvent{
			ID:              "se-1",
			CorrelationRule: "NodeCascade",
			PrimaryResource: model.ResourceRef{Kind: "Node", Name: "node-1"},
			Severity:        model.SeverityCritical,
			Timestamp:       time.Date(2026, 2, 8, 14, 30, 0, 0, time.UTC),
			FaultEvents: []model.FaultEvent{
				{
					ID:           "fe-1",
					DetectorName: "PodFailed",
					Severity:     model.SeverityWarning,
					Timestamp:    time.Date(2026, 2, 8, 14, 29, 0, 0, time.UTC),
					Resource:     model.ResourceRef{Kind: "Pod", Namespace: "default", Name: "pod-1"},
				},
				{
					ID:           "fe-2",
					DetectorName: "PodFailed",
					Severity:     model.SeverityCritical,
					Timestamp:    time.Date(2026, 2, 8, 14, 29, 30, 0, time.UTC),
					Resource:     model.ResourceRef{Kind: "Pod", Namespace: "default", Name: "pod-2"},
				},
			},
		},
		Timestamp: time.Now().UTC(),
		Sections:  []model.DiagnosticSection{},
	}

	prompt := b.BuildUserPrompt(bundle)

	requiredParts := []string{
		"## Super-Event (Correlated)",
		"NodeCascade",
		"Node/node-1",
		"Correlated Fault Events:** 2",
		"### Fault Event 1",
		"### Fault Event 2",
		"Pod/default/pod-1",
		"Pod/default/pod-2",
	}
	for _, part := range requiredParts {
		if !strings.Contains(prompt, part) {
			t.Errorf("BuildUserPrompt() should contain %q", part)
		}
	}
}

func TestBuildUserPrompt_SectionWithError(t *testing.T) {
	b := NewBuilder("", "")

	bundle := model.DiagnosticBundle{
		FaultEvent: &model.FaultEvent{
			ID:           "fe-1",
			DetectorName: "PodCrashLoop",
			Severity:     model.SeverityWarning,
			Timestamp:    time.Now().UTC(),
			Resource:     model.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "p"},
		},
		Sections: []model.DiagnosticSection{
			{
				GathererName: "PodLogs",
				Title:        "Container Logs",
				Error:        "container not found: istio-proxy",
			},
		},
	}

	prompt := b.BuildUserPrompt(bundle)

	if !strings.Contains(prompt, "Error gathering data:") {
		t.Error("prompt should include error message for failed gatherer")
	}
	if !strings.Contains(prompt, "container not found: istio-proxy") {
		t.Error("prompt should include specific error text")
	}
}

func TestBuildUserPrompt_YAMLFormat(t *testing.T) {
	b := NewBuilder("", "")

	bundle := model.DiagnosticBundle{
		FaultEvent: &model.FaultEvent{
			ID:           "fe-1",
			DetectorName: "PodCrashLoop",
			Severity:     model.SeverityWarning,
			Timestamp:    time.Now().UTC(),
			Resource:     model.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "p"},
		},
		Sections: []model.DiagnosticSection{
			{
				GathererName: "PodDescribe",
				Title:        "Pod Description",
				Content:      "apiVersion: v1\nkind: Pod\nmetadata:\n  name: test-pod",
				Format:       "yaml",
			},
		},
	}

	prompt := b.BuildUserPrompt(bundle)

	if !strings.Contains(prompt, "```yaml") {
		t.Error("YAML content should be wrapped in yaml code fence")
	}
}

func TestBuildUserPrompt_EmptyBundle(t *testing.T) {
	b := NewBuilder("", "")

	bundle := model.DiagnosticBundle{
		FaultEvent: &model.FaultEvent{
			ID:           "fe-1",
			DetectorName: "Test",
			Severity:     model.SeverityInfo,
			Timestamp:    time.Now().UTC(),
			Resource:     model.ResourceRef{Kind: "Pod", Namespace: "ns", Name: "p"},
		},
		Sections: []model.DiagnosticSection{},
	}

	prompt := b.BuildUserPrompt(bundle)

	if !strings.Contains(prompt, "# Diagnostic Bundle") {
		t.Error("prompt should contain header")
	}
	if strings.Contains(prompt, "## Diagnostic Data") {
		t.Error("prompt should not contain Diagnostic Data section when no sections")
	}
}
