package gatherer

import (
	"testing"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

func TestExpandTemplate(t *testing.T) {
	vars := TemplateVars{
		Resource: model.ResourceRef{
			Kind:      "Pod",
			Namespace: "payments",
			Name:      "api-server-abc123",
			UID:       "uid-12345",
		},
		NodeName: "node-us-east-1a",
	}

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "resource name",
			input: "{resource.name}",
			want:  "api-server-abc123",
		},
		{
			name:  "resource namespace",
			input: "{resource.namespace}",
			want:  "payments",
		},
		{
			name:  "resource kind",
			input: "{resource.kind}",
			want:  "Pod",
		},
		{
			name:  "resource uid",
			input: "{resource.uid}",
			want:  "uid-12345",
		},
		{
			name:  "node name",
			input: "{node.name}",
			want:  "node-us-east-1a",
		},
		{
			name:  "multiple variables",
			input: "{resource.namespace}/{resource.name}",
			want:  "payments/api-server-abc123",
		},
		{
			name:  "embedded in text",
			input: "pod-{resource.name}-logs",
			want:  "pod-api-server-abc123-logs",
		},
		{
			name:  "no variables",
			input: "plain text with no templates",
			want:  "plain text with no templates",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "unrecognized variable",
			input: "{unknown.var}",
			want:  "{unknown.var}",
		},
		{
			name:  "mixed known and unknown",
			input: "{resource.name}-{unknown.var}",
			want:  "api-server-abc123-{unknown.var}",
		},
		{
			name:  "all variables",
			input: "{resource.kind}/{resource.namespace}/{resource.name}/{resource.uid}@{node.name}",
			want:  "Pod/payments/api-server-abc123/uid-12345@node-us-east-1a",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExpandTemplate(tt.input, vars)
			if got != tt.want {
				t.Errorf("ExpandTemplate(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestExpandTemplate_EmptyVars(t *testing.T) {
	vars := TemplateVars{}

	got := ExpandTemplate("{resource.name}", vars)
	if got != "" {
		t.Errorf("ExpandTemplate with empty vars = %q, want empty", got)
	}
}
