//go:build tools
// +build tools

// Package tools records tool dependencies for this project.
// This file ensures "go mod tidy" does not remove tool dependencies
// that are used via "go install" or "go run" but not imported in
// regular source code.
package tools

import (
	_ "sigs.k8s.io/controller-tools/cmd/controller-gen"
)
