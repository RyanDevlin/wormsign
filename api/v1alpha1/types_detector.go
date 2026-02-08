package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=wd
// +kubebuilder:printcolumn:name="Resource",type=string,JSONPath=`.spec.resource`
// +kubebuilder:printcolumn:name="Severity",type=string,JSONPath=`.spec.severity`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// WormsignDetector defines a custom fault detector using CEL expressions.
// Detectors watch Kubernetes resources and emit fault events when the CEL
// condition evaluates to true.
type WormsignDetector struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec defines the detector configuration.
	Spec WormsignDetectorSpec `json:"spec"`

	// Status contains the observed state of the detector.
	// +optional
	Status WormsignDetectorStatus `json:"status,omitempty"`
}

// WormsignDetectorSpec defines the desired behavior of a custom detector.
type WormsignDetectorSpec struct {
	// Description is a human-readable explanation of what this detector does.
	// +optional
	// +kubebuilder:validation:MaxLength=1024
	Description string `json:"description,omitempty"`

	// Resource is the Kubernetes resource type to watch. Must be a lowercase
	// plural resource name (e.g., "pods", "nodes", "persistentvolumeclaims").
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:Pattern=`^[a-z][a-z0-9]*$`
	Resource string `json:"resource"`

	// NamespaceSelector restricts which namespaces this detector watches.
	// If empty, the detector watches only the namespace it is created in.
	// Cross-namespace detectors must be created in the wormsign-system
	// namespace with a namespaceSelector specified.
	// +optional
	NamespaceSelector *metav1.LabelSelector `json:"namespaceSelector,omitempty"`

	// Condition is a CEL expression that must evaluate to true for a fault
	// event to be emitted. The expression has access to the resource object,
	// now(), duration(), timestamp(), params, and standard CEL functions.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Condition string `json:"condition"`

	// Params are user-defined parameters available in the CEL expression
	// as a string-keyed map. Values are strings; type coercion is done in
	// CEL.
	// +optional
	Params map[string]string `json:"params,omitempty"`

	// Severity is the severity level assigned to fault events emitted by
	// this detector.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=critical;warning;info
	// +kubebuilder:default=warning
	Severity Severity `json:"severity"`

	// Cooldown is the minimum duration between fault events for the same
	// resource from this detector. Prevents alert storms.
	// +optional
	// +kubebuilder:validation:Pattern=`^([0-9]+(s|m|h))+$`
	// +kubebuilder:default="30m"
	Cooldown string `json:"cooldown,omitempty"`
}

// WormsignDetectorStatus contains the observed state of a WormsignDetector.
type WormsignDetectorStatus struct {
	WormsignStatus `json:",inline"`

	// MatchedNamespaces is the number of namespaces currently being watched
	// by this detector.
	// +optional
	MatchedNamespaces int32 `json:"matchedNamespaces,omitempty"`

	// LastFired is the timestamp of the most recent fault event emitted by
	// this detector.
	// +optional
	LastFired *metav1.Time `json:"lastFired,omitempty"`

	// FaultEventsTotal is the total number of fault events emitted by this
	// detector since the controller started.
	// +optional
	FaultEventsTotal int64 `json:"faultEventsTotal,omitempty"`
}

// +kubebuilder:object:root=true

// WormsignDetectorList contains a list of WormsignDetector resources.
type WormsignDetectorList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []WormsignDetector `json:"items"`
}
