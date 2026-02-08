package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=wg
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// WormsignGatherer defines a custom diagnostic data collector. Gatherers
// collect Kubernetes resource data and logs relevant to a fault event.
type WormsignGatherer struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec defines the gatherer configuration.
	Spec WormsignGathererSpec `json:"spec"`

	// Status contains the observed state of the gatherer.
	// +optional
	Status WormsignGathererStatus `json:"status,omitempty"`
}

// WormsignGathererSpec defines the desired behavior of a custom gatherer.
type WormsignGathererSpec struct {
	// Description is a human-readable explanation of what this gatherer
	// collects.
	// +optional
	// +kubebuilder:validation:MaxLength=1024
	Description string `json:"description,omitempty"`

	// TriggerOn specifies which fault events activate this gatherer.
	// +kubebuilder:validation:Required
	TriggerOn GathererTrigger `json:"triggerOn"`

	// Collect defines the list of data collection actions to perform.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	Collect []CollectAction `json:"collect"`
}

// GathererTrigger specifies the conditions under which a gatherer is activated.
type GathererTrigger struct {
	// ResourceKinds is a list of Kubernetes resource kinds (e.g., "Pod",
	// "Node") that trigger this gatherer when they appear in a fault event.
	// +optional
	// +kubebuilder:validation:MinItems=1
	ResourceKinds []string `json:"resourceKinds,omitempty"`

	// Detectors is a list of detector names (built-in or custom) that
	// trigger this gatherer when they emit a fault event.
	// +optional
	Detectors []string `json:"detectors,omitempty"`
}

// CollectAction defines a single data collection operation.
type CollectAction struct {
	// Type is the kind of collection to perform.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=resource;logs
	Type CollectType `json:"type"`

	// APIVersion is the Kubernetes API version of the resource to collect.
	// Required when type is "resource".
	// +optional
	APIVersion string `json:"apiVersion,omitempty"`

	// Resource is the lowercase plural resource name to collect (e.g.,
	// "pods", "destinationrules"). Required when type is "resource".
	// +optional
	Resource string `json:"resource,omitempty"`

	// Name is the name of the specific resource to fetch. Supports template
	// variables: {resource.name}, {resource.namespace}, {resource.kind},
	// {resource.uid}, {node.name}. Required when type is "resource" and
	// listAll is false.
	// +optional
	Name string `json:"name,omitempty"`

	// Namespace is the namespace of the resource to fetch. Supports template
	// variables. Defaults to the fault event resource's namespace.
	// +optional
	Namespace string `json:"namespace,omitempty"`

	// JSONPath is an optional JSONPath expression to extract specific fields
	// from the resource. Only used when type is "resource".
	// +optional
	JSONPath string `json:"jsonPath,omitempty"`

	// ListAll, when true, lists all resources of the specified type in the
	// namespace rather than fetching a single named resource. Only used when
	// type is "resource".
	// +optional
	ListAll bool `json:"listAll,omitempty"`

	// Container is the name of the container whose logs to collect. Only
	// used when type is "logs".
	// +optional
	Container string `json:"container,omitempty"`

	// TailLines is the number of log lines to collect from the end. Only
	// used when type is "logs".
	// +optional
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=10000
	TailLines *int64 `json:"tailLines,omitempty"`

	// Previous, when true, collects logs from the previous terminated
	// container instance. Only used when type is "logs".
	// +optional
	Previous bool `json:"previous,omitempty"`
}

// CollectType specifies the type of data collection operation.
// +kubebuilder:validation:Enum=resource;logs
type CollectType string

const (
	// CollectTypeResource fetches a Kubernetes resource via the API server.
	CollectTypeResource CollectType = "resource"

	// CollectTypeLogs fetches container logs from a pod.
	CollectTypeLogs CollectType = "logs"
)

// WormsignGathererStatus contains the observed state of a WormsignGatherer.
type WormsignGathererStatus struct {
	WormsignStatus `json:",inline"`
}

// +kubebuilder:object:root=true

// WormsignGathererList contains a list of WormsignGatherer resources.
type WormsignGathererList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []WormsignGatherer `json:"items"`
}
