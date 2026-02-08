package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=wp
// +kubebuilder:printcolumn:name="Action",type=string,JSONPath=`.spec.action`
// +kubebuilder:printcolumn:name="Active",type=string,JSONPath=`.status.conditions[?(@.type=="Active")].status`
// +kubebuilder:printcolumn:name="Matched",type=integer,JSONPath=`.status.matchedResources`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// WormsignPolicy defines exclusion filters and suppression rules that prevent
// specific resources, namespaces, or workloads from triggering fault detection.
type WormsignPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	// Spec defines the policy configuration.
	Spec WormsignPolicySpec `json:"spec"`

	// Status contains the observed state of the policy.
	// +optional
	Status WormsignPolicyStatus `json:"status,omitempty"`
}

// WormsignPolicySpec defines the desired behavior of a policy.
type WormsignPolicySpec struct {
	// Action specifies what the policy does when a resource matches.
	// "Exclude" prevents fault events from being emitted for matching
	// resources. "Suppress" temporarily suppresses all detection in the
	// policy's scope (useful for maintenance windows).
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Enum=Exclude;Suppress
	Action PolicyAction `json:"action"`

	// Detectors restricts the policy to specific detector names. If empty,
	// the policy applies to all detectors.
	// +optional
	Detectors []string `json:"detectors,omitempty"`

	// NamespaceSelector restricts which namespaces this policy applies to.
	// Only used for cross-namespace policies created in the wormsign-system
	// namespace. If empty, the policy applies to the namespace it is created
	// in.
	// +optional
	NamespaceSelector *metav1.LabelSelector `json:"namespaceSelector,omitempty"`

	// Match defines the resource matching criteria. All conditions within
	// Match are ANDed. If empty, the policy matches all resources in scope.
	// +optional
	Match *PolicyMatch `json:"match,omitempty"`

	// Schedule defines an optional time window during which the policy is
	// active. Only applicable when action is "Suppress". If omitted, the
	// policy is active as long as it exists.
	// +optional
	Schedule *PolicySchedule `json:"schedule,omitempty"`
}

// PolicyAction specifies the behavior of a policy when resources match.
// +kubebuilder:validation:Enum=Exclude;Suppress
type PolicyAction string

const (
	// PolicyActionExclude prevents fault events from being emitted for
	// matching resources.
	PolicyActionExclude PolicyAction = "Exclude"

	// PolicyActionSuppress temporarily suppresses all detection in the
	// policy's scope.
	PolicyActionSuppress PolicyAction = "Suppress"
)

// PolicyMatch defines criteria for matching Kubernetes resources against a
// policy. All specified conditions are ANDed.
type PolicyMatch struct {
	// ResourceSelector matches resources by label. Uses standard Kubernetes
	// label selector semantics.
	// +optional
	ResourceSelector *metav1.LabelSelector `json:"resourceSelector,omitempty"`

	// OwnerNames matches resources whose owning controller name matches
	// one of the specified glob patterns (e.g., "canary-deploy-*",
	// "load-test-*").
	// +optional
	OwnerNames []string `json:"ownerNames,omitempty"`
}

// PolicySchedule defines a time window during which a Suppress policy is
// active.
type PolicySchedule struct {
	// Start is the UTC timestamp when the suppression window begins.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Format=date-time
	Start metav1.Time `json:"start"`

	// End is the UTC timestamp when the suppression window ends.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Format=date-time
	End metav1.Time `json:"end"`
}

// WormsignPolicyStatus contains the observed state of a WormsignPolicy.
type WormsignPolicyStatus struct {
	WormsignStatus `json:",inline"`

	// MatchedResources is the number of resources currently matched by this
	// policy.
	// +optional
	MatchedResources int32 `json:"matchedResources,omitempty"`

	// LastEvaluated is the timestamp of the most recent policy evaluation.
	// +optional
	LastEvaluated *metav1.Time `json:"lastEvaluated,omitempty"`
}

// +kubebuilder:object:root=true

// WormsignPolicyList contains a list of WormsignPolicy resources.
type WormsignPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []WormsignPolicy `json:"items"`
}
