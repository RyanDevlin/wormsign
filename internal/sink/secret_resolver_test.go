package sink

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"

	v1alpha1 "github.com/k8s-wormsign/k8s-wormsign/api/v1alpha1"
)

func TestNewSecretResolver_Validation(t *testing.T) {
	tests := []struct {
		name      string
		clientset bool
		logger    bool
		wantErr   bool
	}{
		{
			name:      "nil clientset",
			clientset: false,
			logger:    true,
			wantErr:   true,
		},
		{
			name:      "nil logger",
			clientset: true,
			logger:    false,
			wantErr:   true,
		},
		{
			name:      "valid",
			clientset: true,
			logger:    true,
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var cs kubernetes.Interface
			if tt.clientset {
				cs = fake.NewSimpleClientset()
			}
			var l = silentLogger()
			if !tt.logger {
				l = nil
			}
			_, err := NewSecretResolver(cs, l)
			if (err != nil) != tt.wantErr {
				t.Errorf("NewSecretResolver() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSecretResolver_ResolveSecretRef(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "wormsign-sink-secrets",
			Namespace: "wormsign-system",
		},
		Data: map[string][]byte{
			"webhook-url": []byte("https://hooks.slack.com/services/T00/B00/xxx"),
			"api-token":   []byte("my-secret-token"),
			"empty-key":   []byte(""),
			"spaces-only": []byte("  \n  "),
		},
	}
	cs := fake.NewSimpleClientset(secret)
	resolver, err := NewSecretResolver(cs, silentLogger())
	if err != nil {
		t.Fatalf("NewSecretResolver error: %v", err)
	}

	tests := []struct {
		name      string
		namespace string
		ref       v1alpha1.SecretReference
		want      string
		wantErr   bool
		errMsg    string
	}{
		{
			name:      "valid secret reference",
			namespace: "wormsign-system",
			ref:       v1alpha1.SecretReference{Name: "wormsign-sink-secrets", Key: "webhook-url"},
			want:      "https://hooks.slack.com/services/T00/B00/xxx",
			wantErr:   false,
		},
		{
			name:      "valid api token",
			namespace: "wormsign-system",
			ref:       v1alpha1.SecretReference{Name: "wormsign-sink-secrets", Key: "api-token"},
			want:      "my-secret-token",
			wantErr:   false,
		},
		{
			name:      "empty namespace",
			namespace: "",
			ref:       v1alpha1.SecretReference{Name: "wormsign-sink-secrets", Key: "webhook-url"},
			wantErr:   true,
			errMsg:    "namespace must not be empty",
		},
		{
			name:      "empty secret name",
			namespace: "wormsign-system",
			ref:       v1alpha1.SecretReference{Name: "", Key: "webhook-url"},
			wantErr:   true,
			errMsg:    "secret name must not be empty",
		},
		{
			name:      "empty secret key",
			namespace: "wormsign-system",
			ref:       v1alpha1.SecretReference{Name: "wormsign-sink-secrets", Key: ""},
			wantErr:   true,
			errMsg:    "secret key must not be empty",
		},
		{
			name:      "secret not found",
			namespace: "wormsign-system",
			ref:       v1alpha1.SecretReference{Name: "nonexistent-secret", Key: "webhook-url"},
			wantErr:   true,
			errMsg:    "reading secret",
		},
		{
			name:      "key not found in secret",
			namespace: "wormsign-system",
			ref:       v1alpha1.SecretReference{Name: "wormsign-sink-secrets", Key: "nonexistent-key"},
			wantErr:   true,
			errMsg:    "key \"nonexistent-key\" not found",
		},
		{
			name:      "empty value",
			namespace: "wormsign-system",
			ref:       v1alpha1.SecretReference{Name: "wormsign-sink-secrets", Key: "empty-key"},
			wantErr:   true,
			errMsg:    "is empty",
		},
		{
			name:      "whitespace-only value",
			namespace: "wormsign-system",
			ref:       v1alpha1.SecretReference{Name: "wormsign-sink-secrets", Key: "spaces-only"},
			wantErr:   true,
			errMsg:    "is empty",
		},
		{
			name:      "wrong namespace",
			namespace: "other-namespace",
			ref:       v1alpha1.SecretReference{Name: "wormsign-sink-secrets", Key: "webhook-url"},
			wantErr:   true,
			errMsg:    "reading secret",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolver.ResolveSecretRef(context.Background(), tt.namespace, tt.ref)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errMsg != "" && !contains(err.Error(), tt.errMsg) {
					t.Errorf("error %q should contain %q", err.Error(), tt.errMsg)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got != tt.want {
					t.Errorf("got %q, want %q", got, tt.want)
				}
			}
		})
	}
}

func TestSecretResolver_ResolveWebhookURL(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "wormsign-sink-secrets",
			Namespace: "wormsign-system",
		},
		Data: map[string][]byte{
			"webhook-url": []byte("https://hooks.example.com/webhook"),
		},
	}
	cs := fake.NewSimpleClientset(secret)
	resolver, err := NewSecretResolver(cs, silentLogger())
	if err != nil {
		t.Fatalf("NewSecretResolver error: %v", err)
	}

	tests := []struct {
		name      string
		namespace string
		webhook   *v1alpha1.WebhookConfig
		want      string
		wantErr   bool
		errMsg    string
	}{
		{
			name:      "direct URL",
			namespace: "wormsign-system",
			webhook: &v1alpha1.WebhookConfig{
				URL: "https://example.com/hook",
			},
			want:    "https://example.com/hook",
			wantErr: false,
		},
		{
			name:      "secret ref URL",
			namespace: "wormsign-system",
			webhook: &v1alpha1.WebhookConfig{
				URLSecretRef: &v1alpha1.SecretReference{
					Name: "wormsign-sink-secrets",
					Key:  "webhook-url",
				},
			},
			want:    "https://hooks.example.com/webhook",
			wantErr: false,
		},
		{
			name:      "nil webhook config",
			namespace: "wormsign-system",
			webhook:   nil,
			wantErr:   true,
			errMsg:    "webhook config must not be nil",
		},
		{
			name:      "neither URL nor secretRef",
			namespace: "wormsign-system",
			webhook:   &v1alpha1.WebhookConfig{},
			wantErr:   true,
			errMsg:    "must specify either url or secretRef",
		},
		{
			name:      "both URL and secretRef",
			namespace: "wormsign-system",
			webhook: &v1alpha1.WebhookConfig{
				URL: "https://example.com/hook",
				URLSecretRef: &v1alpha1.SecretReference{
					Name: "wormsign-sink-secrets",
					Key:  "webhook-url",
				},
			},
			wantErr: true,
			errMsg:  "not both",
		},
		{
			name:      "secret ref not found",
			namespace: "wormsign-system",
			webhook: &v1alpha1.WebhookConfig{
				URLSecretRef: &v1alpha1.SecretReference{
					Name: "nonexistent",
					Key:  "webhook-url",
				},
			},
			wantErr: true,
			errMsg:  "reading secret",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolver.ResolveWebhookURL(context.Background(), tt.namespace, tt.webhook)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errMsg != "" && !contains(err.Error(), tt.errMsg) {
					t.Errorf("error %q should contain %q", err.Error(), tt.errMsg)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if got != tt.want {
					t.Errorf("got %q, want %q", got, tt.want)
				}
			}
		})
	}
}

func TestSecretResolver_ResolveHeaderSecrets(t *testing.T) {
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "wormsign-sink-secrets",
			Namespace: "wormsign-system",
		},
		Data: map[string][]byte{
			"api-token": []byte("my-secret-token"),
		},
	}
	cs := fake.NewSimpleClientset(secret)
	resolver, err := NewSecretResolver(cs, silentLogger())
	if err != nil {
		t.Fatalf("NewSecretResolver error: %v", err)
	}

	tests := []struct {
		name      string
		namespace string
		headers   map[string]string
		want      map[string]string
		wantErr   bool
		errMsg    string
	}{
		{
			name:      "no headers",
			namespace: "wormsign-system",
			headers:   nil,
			want:      nil,
			wantErr:   false,
		},
		{
			name:      "plain headers",
			namespace: "wormsign-system",
			headers: map[string]string{
				"Content-Type": "application/json",
				"X-Custom":     "plain-value",
			},
			want: map[string]string{
				"Content-Type": "application/json",
				"X-Custom":     "plain-value",
			},
			wantErr: false,
		},
		{
			name:      "secret reference in header",
			namespace: "wormsign-system",
			headers: map[string]string{
				"Authorization": "Bearer ${SECRET:wormsign-sink-secrets:api-token}",
				"Content-Type":  "application/json",
			},
			want: map[string]string{
				"Authorization": "Bearer my-secret-token",
				"Content-Type":  "application/json",
			},
			wantErr: false,
		},
		{
			name:      "secret reference only",
			namespace: "wormsign-system",
			headers: map[string]string{
				"X-Token": "${SECRET:wormsign-sink-secrets:api-token}",
			},
			want: map[string]string{
				"X-Token": "my-secret-token",
			},
			wantErr: false,
		},
		{
			name:      "secret not found in header",
			namespace: "wormsign-system",
			headers: map[string]string{
				"Authorization": "Bearer ${SECRET:nonexistent:key}",
			},
			wantErr: true,
			errMsg:  "reading secret",
		},
		{
			name:      "malformed secret reference",
			namespace: "wormsign-system",
			headers: map[string]string{
				"Authorization": "Bearer ${SECRET:missing-key}",
			},
			wantErr: true,
			errMsg:  "invalid secret reference format",
		},
		{
			name:      "unterminated secret reference",
			namespace: "wormsign-system",
			headers: map[string]string{
				"Authorization": "Bearer ${SECRET:wormsign-sink-secrets:api-token",
			},
			wantErr: true,
			errMsg:  "unterminated",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolver.ResolveHeaderSecrets(context.Background(), tt.namespace, tt.headers)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				if tt.errMsg != "" && !contains(err.Error(), tt.errMsg) {
					t.Errorf("error %q should contain %q", err.Error(), tt.errMsg)
				}
			} else {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if tt.want == nil && got == nil {
					return
				}
				if len(got) != len(tt.want) {
					t.Fatalf("got %d headers, want %d", len(got), len(tt.want))
				}
				for k, v := range tt.want {
					if got[k] != v {
						t.Errorf("header %q = %q, want %q", k, got[k], v)
					}
				}
			}
		})
	}
}
