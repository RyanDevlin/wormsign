package detector

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

// JobDeadlineExceededConfig holds configuration for the JobDeadlineExceeded detector.
type JobDeadlineExceededConfig struct {
	// Cooldown is the minimum time between fault events for the same resource.
	// Default: 30m.
	Cooldown time.Duration

	// Callback receives emitted fault events.
	Callback EventCallback

	// Logger for structured logging.
	Logger *slog.Logger
}

// JobState represents the minimal job state needed for detection.
type JobState struct {
	Name      string
	Namespace string
	UID       string
	// ActiveDeadlineSeconds is the job's configured deadline (nil if not set).
	ActiveDeadlineSeconds *int64
	// StartTime is when the job started running (nil if not started).
	StartTime *time.Time
	// Completed is true if the job has already finished successfully.
	Completed bool
	// Failed is true if the job has already been marked as failed.
	Failed bool
	// ConditionDeadlineExceeded is true if the job has a "DeadlineExceeded"
	// condition. This is the most reliable signal from the Job controller.
	ConditionDeadlineExceeded bool
	// Labels from the job metadata.
	Labels map[string]string
	// Annotations from the job metadata.
	Annotations map[string]string
}

// JobDeadlineExceeded detects Jobs whose activeDeadlineSeconds has been
// reached without completion.
type JobDeadlineExceeded struct {
	base *BaseDetector

	cancel context.CancelFunc
}

// NewJobDeadlineExceeded creates a JobDeadlineExceeded detector.
func NewJobDeadlineExceeded(cfg JobDeadlineExceededConfig) (*JobDeadlineExceeded, error) {
	if cfg.Cooldown <= 0 {
		cfg.Cooldown = 30 * time.Minute
	}

	base, err := NewBaseDetector(BaseDetectorConfig{
		Name:     "JobDeadlineExceeded",
		Severity: model.SeverityWarning,
		Cooldown: cfg.Cooldown,
		Callback: cfg.Callback,
		Logger:   cfg.Logger,
	})
	if err != nil {
		return nil, fmt.Errorf("creating JobDeadlineExceeded detector: %w", err)
	}

	return &JobDeadlineExceeded{
		base: base,
	}, nil
}

func (d *JobDeadlineExceeded) Name() string             { return d.base.DetectorName() }
func (d *JobDeadlineExceeded) Severity() model.Severity  { return d.base.DetectorSeverity() }
func (d *JobDeadlineExceeded) IsLeaderOnly() bool        { return false }

func (d *JobDeadlineExceeded) Start(ctx context.Context) error {
	ctx, d.cancel = context.WithCancel(ctx)
	<-ctx.Done()
	return ctx.Err()
}

func (d *JobDeadlineExceeded) Stop() {
	if d.cancel != nil {
		d.cancel()
	}
}

// Check evaluates the job state and emits a fault event if the job's
// activeDeadlineSeconds has been reached without completion.
// Returns true if an event was emitted.
func (d *JobDeadlineExceeded) Check(job JobState) bool {
	// Already completed or failed — nothing to detect.
	if job.Completed {
		return false
	}

	// If the Job controller has already set the DeadlineExceeded condition,
	// use that as the authoritative signal.
	if job.ConditionDeadlineExceeded {
		return d.emit(job, "Job has DeadlineExceeded condition")
	}

	// Check if the deadline has been exceeded based on timing.
	if job.ActiveDeadlineSeconds == nil || job.StartTime == nil {
		return false
	}

	deadline := job.StartTime.Add(time.Duration(*job.ActiveDeadlineSeconds) * time.Second)
	now := d.base.Now()

	if now.Before(deadline) {
		return false
	}

	elapsed := now.Sub(*job.StartTime)
	return d.emit(job, fmt.Sprintf(
		"Job has been running for %s, exceeding deadline of %ds",
		elapsed.Round(time.Second), *job.ActiveDeadlineSeconds,
	))
}

// emit creates and delivers a fault event for the job.
func (d *JobDeadlineExceeded) emit(job JobState, reason string) bool {
	ref := model.ResourceRef{
		Kind:      "Job",
		Namespace: job.Namespace,
		Name:      job.Name,
		UID:       job.UID,
	}

	description := fmt.Sprintf("Job %s/%s: %s", job.Namespace, job.Name, reason)

	annotations := map[string]string{}
	if job.ActiveDeadlineSeconds != nil {
		annotations["activeDeadlineSeconds"] = fmt.Sprintf("%d", *job.ActiveDeadlineSeconds)
	}

	return d.base.Emit(ref, description, job.Labels, annotations)
}
