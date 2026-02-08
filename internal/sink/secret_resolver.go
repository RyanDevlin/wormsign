package sink

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	v1alpha1 "github.com/k8s-wormsign/k8s-wormsign/api/v1alpha1"
)

// SecretResolver resolves SecretReference values by reading Kubernetes Secrets.
// It is used to resolve webhook URLs and header values that reference secrets.
type SecretResolver struct {
	clientset kubernetes.Interface
	logger    *slog.Logger
}

// NewSecretResolver creates a new SecretResolver. Both clientset and logger
// are required.
func NewSecretResolver(clientset kubernetes.Interface, logger *slog.Logger) (*SecretResolver, error) {
	if clientset == nil {
		return nil, fmt.Errorf("secret resolver: clientset must not be nil")
	}
	if logger == nil {
		return nil, errNilLogger
	}
	return &SecretResolver{
		clientset: clientset,
		logger:    logger,
	}, nil
}

// ResolveSecretRef reads a Kubernetes Secret and returns the value for the
// specified key. The namespace parameter determines which namespace to look
// up the Secret in (typically the namespace of the WormsignSink CRD).
func (r *SecretResolver) ResolveSecretRef(ctx context.Context, namespace string, ref v1alpha1.SecretReference) (string, error) {
	if namespace == "" {
		return "", fmt.Errorf("secret resolver: namespace must not be empty")
	}
	if ref.Name == "" {
		return "", fmt.Errorf("secret resolver: secret name must not be empty")
	}
	if ref.Key == "" {
		return "", fmt.Errorf("secret resolver: secret key must not be empty")
	}

	secret, err := r.clientset.CoreV1().Secrets(namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("secret resolver: reading secret %s/%s: %w", namespace, ref.Name, err)
	}

	value, ok := secret.Data[ref.Key]
	if !ok {
		return "", fmt.Errorf("secret resolver: key %q not found in secret %s/%s", ref.Key, namespace, ref.Name)
	}

	resolved := strings.TrimSpace(string(value))
	if resolved == "" {
		return "", fmt.Errorf("secret resolver: key %q in secret %s/%s is empty", ref.Key, namespace, ref.Name)
	}

	r.logger.Debug("resolved secret reference",
		"namespace", namespace,
		"secret", ref.Name,
		"key", ref.Key,
	)

	return resolved, nil
}

// ResolveWebhookURL resolves the webhook URL from a WormsignSink CRD's
// WebhookConfig. If the config has a direct URL, it is returned as-is.
// If it has a URLSecretRef, the secret is read and the URL is returned.
// Returns an error if neither is set or both are set.
func (r *SecretResolver) ResolveWebhookURL(ctx context.Context, namespace string, webhook *v1alpha1.WebhookConfig) (string, error) {
	if webhook == nil {
		return "", fmt.Errorf("secret resolver: webhook config must not be nil")
	}

	hasURL := webhook.URL != ""
	hasRef := webhook.URLSecretRef != nil

	if !hasURL && !hasRef {
		return "", fmt.Errorf("secret resolver: webhook must specify either url or secretRef")
	}
	if hasURL && hasRef {
		return "", fmt.Errorf("secret resolver: webhook must specify either url or secretRef, not both")
	}

	if hasURL {
		return webhook.URL, nil
	}

	return r.ResolveSecretRef(ctx, namespace, *webhook.URLSecretRef)
}

// ResolveHeaderSecrets resolves any secret references in webhook header
// values. Header values containing the pattern ${SECRET:secret-name:key}
// are resolved by reading the referenced Kubernetes Secret. Values without
// the pattern are returned as-is.
func (r *SecretResolver) ResolveHeaderSecrets(ctx context.Context, namespace string, headers map[string]string) (map[string]string, error) {
	if len(headers) == 0 {
		return headers, nil
	}

	resolved := make(map[string]string, len(headers))
	for k, v := range headers {
		resolvedValue, err := r.resolveHeaderValue(ctx, namespace, v)
		if err != nil {
			return nil, fmt.Errorf("secret resolver: resolving header %q: %w", k, err)
		}
		resolved[k] = resolvedValue
	}
	return resolved, nil
}

// resolveHeaderValue checks if a header value contains a secret reference
// pattern and resolves it. The pattern is ${SECRET:secret-name:key}.
func (r *SecretResolver) resolveHeaderValue(ctx context.Context, namespace, value string) (string, error) {
	const prefix = "${SECRET:"
	const suffix = "}"

	if !strings.Contains(value, prefix) {
		return value, nil
	}

	start := strings.Index(value, prefix)
	end := strings.Index(value[start:], suffix)
	if end == -1 {
		return "", fmt.Errorf("unterminated secret reference in header value")
	}
	end += start

	refStr := value[start+len(prefix) : end]
	parts := strings.SplitN(refStr, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("invalid secret reference format %q: expected SECRET:name:key", refStr)
	}

	ref := v1alpha1.SecretReference{
		Name: parts[0],
		Key:  parts[1],
	}

	secretValue, err := r.ResolveSecretRef(ctx, namespace, ref)
	if err != nil {
		return "", err
	}

	// Replace the entire ${SECRET:...} pattern with the resolved value.
	resolved := value[:start] + secretValue + value[end+1:]
	return resolved, nil
}
