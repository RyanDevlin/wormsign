package sink

import (
	"context"
	"fmt"
	"regexp"
	"strings"
)

// secretRefPattern matches ${SECRET:name:key} placeholders in header values.
// The pattern captures the secret name and key as separate groups.
var secretRefPattern = regexp.MustCompile(`\$\{SECRET:([^:}]+):([^}]+)\}`)

// SecretResolver looks up a value from a Kubernetes Secret by name and key.
// Implementations should return an error if the secret or key is not found.
type SecretResolver interface {
	// GetSecretValue returns the value of the given key in the named Secret.
	GetSecretValue(ctx context.Context, namespace, secretName, key string) (string, error)
}

// ResolveSecretRefs resolves all ${SECRET:name:key} references in header values
// using the provided SecretResolver. It returns a new map with all references
// replaced by their resolved values. Original map is not modified.
//
// If any reference cannot be resolved, an error is returned and no partial
// results are provided.
func ResolveSecretRefs(ctx context.Context, headers map[string]string, namespace string, resolver SecretResolver) (map[string]string, error) {
	if resolver == nil {
		return nil, fmt.Errorf("secret resolver must not be nil")
	}

	resolved := make(map[string]string, len(headers))
	for k, v := range headers {
		resolvedValue, err := resolveSecretValue(ctx, v, namespace, resolver)
		if err != nil {
			return nil, fmt.Errorf("resolving header %q: %w", k, err)
		}
		resolved[k] = resolvedValue
	}
	return resolved, nil
}

// resolveSecretValue replaces all ${SECRET:name:key} references in a single
// string value. A value may contain multiple references or a mix of literal
// text and references.
func resolveSecretValue(ctx context.Context, value, namespace string, resolver SecretResolver) (string, error) {
	if !strings.Contains(value, "${SECRET:") {
		return value, nil
	}

	var resolveErr error
	result := secretRefPattern.ReplaceAllStringFunc(value, func(match string) string {
		if resolveErr != nil {
			return match
		}

		parts := secretRefPattern.FindStringSubmatch(match)
		if len(parts) != 3 {
			resolveErr = fmt.Errorf("invalid secret reference %q", match)
			return match
		}

		secretName := parts[1]
		key := parts[2]

		secretValue, err := resolver.GetSecretValue(ctx, namespace, secretName, key)
		if err != nil {
			resolveErr = fmt.Errorf("resolving ${SECRET:%s:%s}: %w", secretName, key, err)
			return match
		}

		return secretValue
	})

	if resolveErr != nil {
		return "", resolveErr
	}
	return result, nil
}

// ContainsSecretRefs returns true if the value contains any ${SECRET:name:key}
// references that need resolution.
func ContainsSecretRefs(value string) bool {
	return secretRefPattern.MatchString(value)
}

// HeadersContainSecretRefs returns true if any header value contains
// ${SECRET:name:key} references.
func HeadersContainSecretRefs(headers map[string]string) bool {
	for _, v := range headers {
		if ContainsSecretRefs(v) {
			return true
		}
	}
	return false
}
