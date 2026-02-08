// Package analyzer — secret.go provides a shared utility for reading API keys
// from Kubernetes Secrets. All LLM backends that require API keys use this
// interface to decouple secret retrieval from the analyzer implementation.
package analyzer

import (
	"context"
	"fmt"
)

// SecretReader reads a value from a Kubernetes Secret by namespace, name, and key.
// In production this is backed by the Kubernetes API; in tests a stub is injected.
type SecretReader interface {
	ReadSecret(ctx context.Context, namespace, name, key string) (string, error)
}

// SecretRef identifies a Kubernetes Secret and key within it.
type SecretRef struct {
	Namespace string
	Name      string
	Key       string
}

// Validate checks that the SecretRef has all required fields populated.
func (s SecretRef) Validate() error {
	if s.Name == "" {
		return fmt.Errorf("secret name must not be empty")
	}
	if s.Key == "" {
		return fmt.Errorf("secret key must not be empty")
	}
	return nil
}
