// Package gatherer — kubernetes.go defines the KubeClient interface used by
// built-in gatherers to interact with the Kubernetes API server. This
// abstraction enables unit testing with fake clients.
package gatherer

import (
	"context"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// KubeClient abstracts the Kubernetes API operations needed by built-in
// gatherers. All methods return concrete Kubernetes types. The interface is
// intentionally narrow: each method fetches exactly the resource type that
// gatherers need.
type KubeClient interface {
	// GetPod fetches a pod by namespace and name.
	GetPod(ctx context.Context, namespace, name string) (*corev1.Pod, error)

	// GetNode fetches a node by name.
	GetNode(ctx context.Context, name string) (*corev1.Node, error)

	// GetPVC fetches a PersistentVolumeClaim by namespace and name.
	GetPVC(ctx context.Context, namespace, name string) (*corev1.PersistentVolumeClaim, error)

	// GetPV fetches a PersistentVolume by name.
	GetPV(ctx context.Context, name string) (*corev1.PersistentVolume, error)

	// ListEvents lists Kubernetes events matching the given field selector
	// in a namespace. Use an empty namespace for cluster-scoped events.
	ListEvents(ctx context.Context, namespace string, opts metav1.ListOptions) (*corev1.EventList, error)

	// GetPodLogs returns the log output for a pod container.
	GetPodLogs(ctx context.Context, namespace, podName, container string, tailLines *int64, previous bool) (string, error)

	// GetReplicaSet fetches a ReplicaSet by namespace and name.
	GetReplicaSet(ctx context.Context, namespace, name string) (*appsv1.ReplicaSet, error)

	// GetDeployment fetches a Deployment by namespace and name.
	GetDeployment(ctx context.Context, namespace, name string) (*appsv1.Deployment, error)

	// GetStatefulSet fetches a StatefulSet by namespace and name.
	GetStatefulSet(ctx context.Context, namespace, name string) (*appsv1.StatefulSet, error)

	// GetDaemonSet fetches a DaemonSet by namespace and name.
	GetDaemonSet(ctx context.Context, namespace, name string) (*appsv1.DaemonSet, error)

	// GetJob fetches a Job by namespace and name.
	GetJob(ctx context.Context, namespace, name string) (*batchv1.Job, error)
}
