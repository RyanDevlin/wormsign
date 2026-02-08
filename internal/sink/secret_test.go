package sink

import (
	"context"
	"fmt"
	"testing"
)

// mockSecretResolver implements SecretResolver for testing.
type mockSecretResolver struct {
	secrets map[string]string // keyed by "namespace/name/key"
}

func (m *mockSecretResolver) GetSecretValue(_ context.Context, namespace, secretName, key string) (string, error) {
	k := fmt.Sprintf("%s/%s/%s", namespace, secretName, key)
	v, ok := m.secrets[k]
	if !ok {
		return "", fmt.Errorf("secret %s/%s key %q not found", namespace, secretName, key)
	}
	return v, nil
}

func TestResolveSecretRefs(t *testing.T) {
	resolver := &mockSecretResolver{
		secrets: map[string]string{
			"wormsign-system/wormsign-sink-secrets/api-token":      "my-secret-token",
			"wormsign-system/wormsign-sink-secrets/slack-webhook":  "https://hooks.slack.com/xxx",
			"wormsign-system/other-secrets/key1":                   "value1",
		},
	}

	tests := []struct {
		name      string
		headers   map[string]string
		namespace string
		want      map[string]string
		wantErr   bool
	}{
		{
			name: "single secret reference",
			headers: map[string]string{
				"Authorization": "Bearer ${SECRET:wormsign-sink-secrets:api-token}",
			},
			namespace: "wormsign-system",
			want: map[string]string{
				"Authorization": "Bearer my-secret-token",
			},
		},
		{
			name: "no secret references",
			headers: map[string]string{
				"Content-Type": "application/json",
				"X-Custom":     "static-value",
			},
			namespace: "wormsign-system",
			want: map[string]string{
				"Content-Type": "application/json",
				"X-Custom":     "static-value",
			},
		},
		{
			name: "multiple headers with mixed refs",
			headers: map[string]string{
				"Authorization": "Bearer ${SECRET:wormsign-sink-secrets:api-token}",
				"Content-Type":  "application/json",
				"X-API-Key":     "${SECRET:other-secrets:key1}",
			},
			namespace: "wormsign-system",
			want: map[string]string{
				"Authorization": "Bearer my-secret-token",
				"Content-Type":  "application/json",
				"X-API-Key":     "value1",
			},
		},
		{
			name: "entire value is a secret ref",
			headers: map[string]string{
				"X-Webhook-URL": "${SECRET:wormsign-sink-secrets:slack-webhook}",
			},
			namespace: "wormsign-system",
			want: map[string]string{
				"X-Webhook-URL": "https://hooks.slack.com/xxx",
			},
		},
		{
			name: "secret not found",
			headers: map[string]string{
				"Authorization": "Bearer ${SECRET:nonexistent:key}",
			},
			namespace: "wormsign-system",
			wantErr:   true,
		},
		{
			name:      "empty headers",
			headers:   map[string]string{},
			namespace: "wormsign-system",
			want:      map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ResolveSecretRefs(context.Background(), tt.headers, tt.namespace, resolver)
			if (err != nil) != tt.wantErr {
				t.Errorf("ResolveSecretRefs() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantErr {
				return
			}
			if len(got) != len(tt.want) {
				t.Errorf("ResolveSecretRefs() returned %d headers, want %d", len(got), len(tt.want))
				return
			}
			for k, wantV := range tt.want {
				if gotV, ok := got[k]; !ok || gotV != wantV {
					t.Errorf("header %q = %q, want %q", k, gotV, wantV)
				}
			}
		})
	}
}

func TestResolveSecretRefs_NilResolver(t *testing.T) {
	_, err := ResolveSecretRefs(context.Background(), map[string]string{"k": "v"}, "ns", nil)
	if err == nil {
		t.Fatal("expected error for nil resolver")
	}
}

func TestContainsSecretRefs(t *testing.T) {
	tests := []struct {
		value string
		want  bool
	}{
		{"Bearer ${SECRET:name:key}", true},
		{"${SECRET:a:b}", true},
		{"plain-value", false},
		{"${SECRET:}", false},
		{"${SECRET:name}", false},
		{"prefix ${SECRET:n:k} suffix", true},
		{"", false},
	}

	for _, tt := range tests {
		got := ContainsSecretRefs(tt.value)
		if got != tt.want {
			t.Errorf("ContainsSecretRefs(%q) = %v, want %v", tt.value, got, tt.want)
		}
	}
}

func TestHeadersContainSecretRefs(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string]string
		want    bool
	}{
		{
			name:    "has secret refs",
			headers: map[string]string{"Auth": "Bearer ${SECRET:s:k}"},
			want:    true,
		},
		{
			name:    "no secret refs",
			headers: map[string]string{"Content-Type": "application/json"},
			want:    false,
		},
		{
			name:    "empty headers",
			headers: map[string]string{},
			want:    false,
		},
		{
			name:    "nil headers",
			headers: nil,
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HeadersContainSecretRefs(tt.headers)
			if got != tt.want {
				t.Errorf("HeadersContainSecretRefs() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolveSecretRefs_DoesNotModifyOriginal(t *testing.T) {
	resolver := &mockSecretResolver{
		secrets: map[string]string{
			"ns/secret/key": "resolved-value",
		},
	}

	original := map[string]string{
		"Header": "${SECRET:secret:key}",
	}
	originalCopy := original["Header"]

	_, err := ResolveSecretRefs(context.Background(), original, "ns", resolver)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if original["Header"] != originalCopy {
		t.Errorf("original map was modified: got %q, want %q", original["Header"], originalCopy)
	}
}
