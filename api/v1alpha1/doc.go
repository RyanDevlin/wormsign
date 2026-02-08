// Package v1alpha1 contains API types for the wormsign.io API group.
//
// These types define the Custom Resource Definitions (CRDs) used by the
// Wormsign Kubernetes controller: WormsignDetector, WormsignGatherer,
// WormsignSink, and WormsignPolicy.
//
// All CRDs are namespaced and include a status subresource with standardized
// conditions (Ready, Valid, Active).
//
// +kubebuilder:object:generate=true
// +groupName=wormsign.io
package v1alpha1
