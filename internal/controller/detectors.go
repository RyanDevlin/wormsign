// Package controller — detector bridge wiring.
//
// This file creates built-in detector instances and wires Kubernetes informer
// event handlers to detector Check() methods, feeding detected faults into
// the pipeline via SubmitFaultEvent. It also runs a periodic scan for
// time-based detectors (PodStuckPending).
package controller

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/tools/cache"

	"github.com/k8s-wormsign/k8s-wormsign/internal/config"
	"github.com/k8s-wormsign/k8s-wormsign/internal/detector"
	"github.com/k8s-wormsign/k8s-wormsign/internal/filter"
	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
	"github.com/k8s-wormsign/k8s-wormsign/internal/pipeline"
	"github.com/k8s-wormsign/k8s-wormsign/internal/shard"
)

// detectorBridge creates detectors, wires informer event handlers to them,
// and feeds fault events into the pipeline.
type detectorBridge struct {
	logger   *slog.Logger
	pipeline *pipeline.Pipeline
	imr      *shard.InformerManager

	crashLoop    *detector.PodCrashLoop
	podFailed    *detector.PodFailed
	stuckPending *detector.PodStuckPending
	jobDeadline  *detector.JobDeadlineExceeded

	// Track namespaces we've already wired up to avoid double-registration.
	mu              sync.Mutex
	wiredNamespaces map[string]bool
}

// newDetectorBridge creates all enabled detectors and returns a bridge ready
// to handle shard change events. The bridge registers informer event handlers
// for each namespace that gets assigned to this replica.
func newDetectorBridge(
	logger *slog.Logger,
	cfg *config.Config,
	p *pipeline.Pipeline,
	imr *shard.InformerManager,
) (*detectorBridge, error) {
	bridge := &detectorBridge{
		logger:          logger,
		pipeline:        p,
		imr:             imr,
		wiredNamespaces: make(map[string]bool),
	}

	// Common callback: detector emits FaultEvent → pipeline.SubmitFaultEvent
	cb := func(event *model.FaultEvent) {
		input := filter.FilterInput{
			DetectorName: event.DetectorName,
			Resource: filter.ResourceMeta{
				Kind:      event.Resource.Kind,
				Namespace: event.Resource.Namespace,
				Name:      event.Resource.Name,
				UID:       event.Resource.UID,
				Labels:    event.Labels,
			},
		}
		p.SubmitFaultEvent(event, input)
	}

	dc := cfg.Detectors

	if dc.PodCrashLoop.Enabled {
		d, err := detector.NewPodCrashLoop(detector.PodCrashLoopConfig{
			Threshold: int32(dc.PodCrashLoop.Threshold),
			Cooldown:  dc.PodCrashLoop.Cooldown,
			Callback:  cb,
			Logger:    logger.With("detector", "PodCrashLoop"),
		})
		if err != nil {
			return nil, fmt.Errorf("creating PodCrashLoop detector: %w", err)
		}
		bridge.crashLoop = d
	}

	if dc.PodFailed.Enabled {
		ignoreCodes := make([]int32, len(dc.PodFailed.IgnoreExitCodes))
		for i, c := range dc.PodFailed.IgnoreExitCodes {
			ignoreCodes[i] = int32(c)
		}
		d, err := detector.NewPodFailed(detector.PodFailedConfig{
			IgnoreExitCodes: ignoreCodes,
			Cooldown:        dc.PodFailed.Cooldown,
			Callback:        cb,
			Logger:          logger.With("detector", "PodFailed"),
		})
		if err != nil {
			return nil, fmt.Errorf("creating PodFailed detector: %w", err)
		}
		bridge.podFailed = d
	}

	if dc.PodStuckPending.Enabled {
		d, err := detector.NewPodStuckPending(detector.PodStuckPendingConfig{
			Threshold: dc.PodStuckPending.Threshold,
			Cooldown:  dc.PodStuckPending.Cooldown,
			Callback:  cb,
			Logger:    logger.With("detector", "PodStuckPending"),
		})
		if err != nil {
			return nil, fmt.Errorf("creating PodStuckPending detector: %w", err)
		}
		bridge.stuckPending = d
	}

	if dc.JobDeadlineExceeded.Enabled {
		d, err := detector.NewJobDeadlineExceeded(detector.JobDeadlineExceededConfig{
			Cooldown: dc.JobDeadlineExceeded.Cooldown,
			Callback: cb,
			Logger:   logger.With("detector", "JobDeadlineExceeded"),
		})
		if err != nil {
			return nil, fmt.Errorf("creating JobDeadlineExceeded detector: %w", err)
		}
		bridge.jobDeadline = d
	}

	count := 0
	if bridge.crashLoop != nil {
		count++
	}
	if bridge.podFailed != nil {
		count++
	}
	if bridge.stuckPending != nil {
		count++
	}
	if bridge.jobDeadline != nil {
		count++
	}

	logger.Info("detector bridge initialized", "active_detectors", count)
	return bridge, nil
}

// DetectorCount returns the number of enabled detectors.
func (b *detectorBridge) DetectorCount() int {
	count := 0
	if b.crashLoop != nil {
		count++
	}
	if b.podFailed != nil {
		count++
	}
	if b.stuckPending != nil {
		count++
	}
	if b.jobDeadline != nil {
		count++
	}
	return count
}

// HandleShardChange is a shard.ShardChangeCallback. For each added namespace
// it gets the informer factory and registers event handlers. For removed
// namespaces it cleans up tracking state (informers are stopped by InformerManager).
func (b *detectorBridge) HandleShardChange(added, removed []string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for _, ns := range removed {
		delete(b.wiredNamespaces, ns)
	}

	for _, ns := range added {
		if b.wiredNamespaces[ns] {
			continue
		}
		factory := b.imr.GetFactory(ns)
		if factory == nil {
			b.logger.Warn("no informer factory for namespace, skipping detector wiring",
				"namespace", ns,
			)
			continue
		}
		b.wireNamespace(ns, factory)
		b.wiredNamespaces[ns] = true
	}
}

// wireNamespace registers pod and job informer event handlers for a namespace.
func (b *detectorBridge) wireNamespace(ns string, factory informers.SharedInformerFactory) {
	b.logger.Info("wiring detectors for namespace", "namespace", ns)

	// Wire pod informer for CrashLoop, Failed, and StuckPending detectors.
	if b.crashLoop != nil || b.podFailed != nil || b.stuckPending != nil {
		podInformer := factory.Core().V1().Pods().Informer()
		podInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				pod, ok := obj.(*corev1.Pod)
				if !ok {
					return
				}
				b.checkPod(pod)
			},
			UpdateFunc: func(_, newObj interface{}) {
				pod, ok := newObj.(*corev1.Pod)
				if !ok {
					return
				}
				b.checkPod(pod)
			},
		})

		// Start the newly registered informer. Start() is idempotent for
		// informers that are already running.
		factory.Start(make(chan struct{}))
		b.logger.Debug("pod informer wired", "namespace", ns)
	}

	// Wire job informer for JobDeadlineExceeded detector.
	if b.jobDeadline != nil {
		jobInformer := factory.Batch().V1().Jobs().Informer()
		jobInformer.AddEventHandler(cache.ResourceEventHandlerFuncs{
			AddFunc: func(obj interface{}) {
				job, ok := obj.(*batchv1.Job)
				if !ok {
					return
				}
				b.checkJob(job)
			},
			UpdateFunc: func(_, newObj interface{}) {
				job, ok := newObj.(*batchv1.Job)
				if !ok {
					return
				}
				b.checkJob(job)
			},
		})
		factory.Start(make(chan struct{}))
		b.logger.Debug("job informer wired", "namespace", ns)
	}
}

// checkPod runs all pod-related detectors against a pod object.
func (b *detectorBridge) checkPod(pod *corev1.Pod) {
	if b.crashLoop != nil {
		state := toCrashLoopState(pod)
		b.crashLoop.Check(state)
	}

	if b.podFailed != nil {
		state := toFailedState(pod)
		b.podFailed.Check(state)
	}

	if b.stuckPending != nil {
		state := toPendingState(pod)
		b.stuckPending.Check(state)
	}
}

// checkJob runs the JobDeadlineExceeded detector against a job object.
func (b *detectorBridge) checkJob(job *batchv1.Job) {
	if b.jobDeadline == nil {
		return
	}
	state := toJobState(job)
	b.jobDeadline.Check(state)
}

// RunPeriodicScan runs a periodic scan for time-based detectors like
// PodStuckPending. Informer events alone won't detect pods stuck in Pending
// because there are no updates after the initial add.
func (b *detectorBridge) RunPeriodicScan(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	b.logger.Info("periodic detector scan started", "interval", interval)

	for {
		select {
		case <-ctx.Done():
			b.logger.Info("periodic detector scan stopped")
			return
		case <-ticker.C:
			b.scanPendingPods()
		}
	}
}

// scanPendingPods lists pods from all wired namespace informers and checks
// them against the PodStuckPending detector.
func (b *detectorBridge) scanPendingPods() {
	if b.stuckPending == nil {
		return
	}

	b.mu.Lock()
	namespaces := make([]string, 0, len(b.wiredNamespaces))
	for ns := range b.wiredNamespaces {
		namespaces = append(namespaces, ns)
	}
	b.mu.Unlock()

	for _, ns := range namespaces {
		factory := b.imr.GetFactory(ns)
		if factory == nil {
			continue
		}
		pods, err := factory.Core().V1().Pods().Lister().List(labels.Everything())
		if err != nil {
			b.logger.Error("failed to list pods for periodic scan",
				"namespace", ns,
				"error", err,
			)
			continue
		}
		for _, pod := range pods {
			if pod.Status.Phase == corev1.PodPending {
				state := toPendingState(pod)
				b.stuckPending.Check(state)
			}
		}
	}
}

// --- Kubernetes object → detector state type conversions ---

func toCrashLoopState(pod *corev1.Pod) detector.PodContainerState {
	state := detector.PodContainerState{
		Name:        pod.Name,
		Namespace:   pod.Namespace,
		UID:         string(pod.UID),
		Labels:      pod.Labels,
		Annotations: pod.Annotations,
	}
	for _, cs := range pod.Status.ContainerStatuses {
		state.ContainerStatuses = append(state.ContainerStatuses, detector.ContainerStatus{
			Name:          cs.Name,
			RestartCount:  cs.RestartCount,
			Waiting:       cs.State.Waiting != nil,
			WaitingReason: waitingReason(cs),
		})
	}
	for _, cs := range pod.Status.InitContainerStatuses {
		state.InitContainerStatuses = append(state.InitContainerStatuses, detector.ContainerStatus{
			Name:          cs.Name,
			RestartCount:  cs.RestartCount,
			Waiting:       cs.State.Waiting != nil,
			WaitingReason: waitingReason(cs),
		})
	}
	return state
}

func toFailedState(pod *corev1.Pod) detector.PodFailedState {
	state := detector.PodFailedState{
		Name:        pod.Name,
		Namespace:   pod.Namespace,
		UID:         string(pod.UID),
		Phase:       string(pod.Status.Phase),
		Labels:      pod.Labels,
		Annotations: pod.Annotations,
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Terminated != nil {
			state.Terminations = append(state.Terminations, detector.ContainerTermination{
				Name:     cs.Name,
				ExitCode: cs.State.Terminated.ExitCode,
				Reason:   cs.State.Terminated.Reason,
			})
		}
	}
	return state
}

func toPendingState(pod *corev1.Pod) detector.PodState {
	return detector.PodState{
		Name:              pod.Name,
		Namespace:         pod.Namespace,
		UID:               string(pod.UID),
		Phase:             string(pod.Status.Phase),
		CreationTimestamp: pod.CreationTimestamp.Time,
		Labels:            pod.Labels,
		Annotations:       pod.Annotations,
	}
}

func toJobState(job *batchv1.Job) detector.JobState {
	state := detector.JobState{
		Name:      job.Name,
		Namespace: job.Namespace,
		UID:       string(job.UID),
		Labels:    job.Labels,
	}
	if job.Spec.ActiveDeadlineSeconds != nil {
		state.ActiveDeadlineSeconds = job.Spec.ActiveDeadlineSeconds
	}
	if job.Status.StartTime != nil {
		t := job.Status.StartTime.Time
		state.StartTime = &t
	}
	for _, c := range job.Status.Conditions {
		if c.Type == batchv1.JobComplete && c.Status == corev1.ConditionTrue {
			state.Completed = true
		}
		if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue {
			state.Failed = true
			if c.Reason == "DeadlineExceeded" {
				state.ConditionDeadlineExceeded = true
			}
		}
	}
	return state
}

func waitingReason(cs corev1.ContainerStatus) string {
	if cs.State.Waiting != nil {
		return cs.State.Waiting.Reason
	}
	return ""
}
