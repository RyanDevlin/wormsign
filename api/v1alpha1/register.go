package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	// GroupName is the API group name for Wormsign CRDs.
	GroupName = "wormsign.io"

	// GroupVersion is the API version for this package.
	Version = "v1alpha1"
)

// SchemeGroupVersion is the group version used to register these objects.
var SchemeGroupVersion = schema.GroupVersion{Group: GroupName, Version: Version}

// Resource returns a GroupResource for the given resource name.
func Resource(resource string) schema.GroupResource {
	return SchemeGroupVersion.WithResource(resource).GroupResource()
}

var (
	// SchemeBuilder collects functions that add types to a scheme.
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)

	// AddToScheme applies all registered SchemeBuilder functions to a scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

// addKnownTypes registers the Wormsign CRD types with the given scheme.
func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(SchemeGroupVersion,
		&WormsignDetector{},
		&WormsignDetectorList{},
		&WormsignGatherer{},
		&WormsignGathererList{},
		&WormsignSink{},
		&WormsignSinkList{},
		&WormsignPolicy{},
		&WormsignPolicyList{},
	)
	metav1.AddToGroupVersion(scheme, SchemeGroupVersion)
	return nil
}
