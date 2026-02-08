package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// Condition type constants used across all Wormsign CRD status subresources.
const (
	// ConditionReady indicates the resource has been validated and is
	// operational.
	ConditionReady = "Ready"

	// ConditionValid indicates the resource's spec has been validated
	// (e.g., CEL expressions compile, templates parse).
	ConditionValid = "Valid"

	// ConditionActive indicates the resource is actively being used by
	// the controller pipeline.
	ConditionActive = "Active"
)

// WormsignStatus contains status fields shared by all Wormsign CRDs.
type WormsignStatus struct {
	// Conditions represent the latest available observations of the
	// resource's current state. Standard condition types are Ready, Valid,
	// and Active.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// SecretReference refers to a key within a Kubernetes Secret.
type SecretReference struct {
	// Name is the name of the Secret in the same namespace as the
	// referencing resource.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// Key is the key within the Secret's data map that holds the value.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`
}

// Severity represents the severity level of a fault event or RCA report.
// +kubebuilder:validation:Enum=critical;warning;info
type Severity string

const (
	// SeverityCritical indicates a critical fault that requires immediate
	// attention.
	SeverityCritical Severity = "critical"

	// SeverityWarning indicates a warning-level fault that should be
	// investigated.
	SeverityWarning Severity = "warning"

	// SeverityInfo indicates an informational fault event.
	SeverityInfo Severity = "info"
)
