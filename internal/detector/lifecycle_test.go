package detector

import (
	"context"
	"testing"
	"time"
)

// TestDetector_StartStop tests the Start/Stop lifecycle for all detectors.
// Each detector's Start() blocks until the context is cancelled, and Stop()
// cancels the context.
func TestDetector_StartStop(t *testing.T) {
	cb, _ := collectCallback()

	detectors := []struct {
		name    string
		newFunc func() (Detector, error)
	}{
		{
			name: "PodStuckPending",
			newFunc: func() (Detector, error) {
				return NewPodStuckPending(PodStuckPendingConfig{Callback: cb, Logger: silentLogger()})
			},
		},
		{
			name: "PodCrashLoop",
			newFunc: func() (Detector, error) {
				return NewPodCrashLoop(PodCrashLoopConfig{Callback: cb, Logger: silentLogger()})
			},
		},
		{
			name: "PodFailed",
			newFunc: func() (Detector, error) {
				return NewPodFailed(PodFailedConfig{Callback: cb, Logger: silentLogger()})
			},
		},
		{
			name: "NodeNotReady",
			newFunc: func() (Detector, error) {
				return NewNodeNotReady(NodeNotReadyConfig{Callback: cb, Logger: silentLogger()})
			},
		},
		{
			name: "PVCStuckBinding",
			newFunc: func() (Detector, error) {
				return NewPVCStuckBinding(PVCStuckBindingConfig{Callback: cb, Logger: silentLogger()})
			},
		},
		{
			name: "HighPodCount",
			newFunc: func() (Detector, error) {
				return NewHighPodCount(HighPodCountConfig{Callback: cb, Logger: silentLogger()})
			},
		},
		{
			name: "JobDeadlineExceeded",
			newFunc: func() (Detector, error) {
				return NewJobDeadlineExceeded(JobDeadlineExceededConfig{Callback: cb, Logger: silentLogger()})
			},
		},
	}

	for _, tt := range detectors {
		t.Run(tt.name+"_StartStop", func(t *testing.T) {
			d, err := tt.newFunc()
			if err != nil {
				t.Fatalf("constructor error: %v", err)
			}

			ctx, cancel := context.WithCancel(context.Background())

			errCh := make(chan error, 1)
			go func() {
				errCh <- d.Start(ctx)
			}()

			// Give Start time to begin blocking.
			time.Sleep(10 * time.Millisecond)

			// Stop the detector.
			d.Stop()

			select {
			case err := <-errCh:
				if err != context.Canceled {
					t.Errorf("Start() returned %v, want context.Canceled", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("Start() did not return after Stop()")
			}

			cancel() // cleanup
		})

		t.Run(tt.name+"_StopBeforeStart", func(t *testing.T) {
			d, err := tt.newFunc()
			if err != nil {
				t.Fatalf("constructor error: %v", err)
			}
			// Stop before Start should not panic.
			d.Stop()
		})

		t.Run(tt.name+"_StartContextCancel", func(t *testing.T) {
			d, err := tt.newFunc()
			if err != nil {
				t.Fatalf("constructor error: %v", err)
			}

			ctx, cancel := context.WithCancel(context.Background())

			errCh := make(chan error, 1)
			go func() {
				errCh <- d.Start(ctx)
			}()

			time.Sleep(10 * time.Millisecond)
			cancel()

			select {
			case err := <-errCh:
				if err != context.Canceled {
					t.Errorf("Start() returned %v, want context.Canceled", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("Start() did not return after context cancel")
			}
		})
	}
}

// TestDetector_Names verifies that all detectors return the correct name.
// This covers the Name() methods that delegate to base.DetectorName().
func TestDetector_Names(t *testing.T) {
	cb, _ := collectCallback()

	tests := []struct {
		wantName string
		newFunc  func() (Detector, error)
	}{
		{
			wantName: "PodStuckPending",
			newFunc: func() (Detector, error) {
				return NewPodStuckPending(PodStuckPendingConfig{Callback: cb, Logger: silentLogger()})
			},
		},
		{
			wantName: "PodCrashLoop",
			newFunc: func() (Detector, error) {
				return NewPodCrashLoop(PodCrashLoopConfig{Callback: cb, Logger: silentLogger()})
			},
		},
		{
			wantName: "PodFailed",
			newFunc: func() (Detector, error) {
				return NewPodFailed(PodFailedConfig{Callback: cb, Logger: silentLogger()})
			},
		},
		{
			wantName: "NodeNotReady",
			newFunc: func() (Detector, error) {
				return NewNodeNotReady(NodeNotReadyConfig{Callback: cb, Logger: silentLogger()})
			},
		},
		{
			wantName: "PVCStuckBinding",
			newFunc: func() (Detector, error) {
				return NewPVCStuckBinding(PVCStuckBindingConfig{Callback: cb, Logger: silentLogger()})
			},
		},
		{
			wantName: "HighPodCount",
			newFunc: func() (Detector, error) {
				return NewHighPodCount(HighPodCountConfig{Callback: cb, Logger: silentLogger()})
			},
		},
		{
			wantName: "JobDeadlineExceeded",
			newFunc: func() (Detector, error) {
				return NewJobDeadlineExceeded(JobDeadlineExceededConfig{Callback: cb, Logger: silentLogger()})
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.wantName, func(t *testing.T) {
			d, err := tt.newFunc()
			if err != nil {
				t.Fatalf("constructor error: %v", err)
			}
			if d.Name() != tt.wantName {
				t.Errorf("Name() = %q, want %q", d.Name(), tt.wantName)
			}
		})
	}
}

// TestBaseDetector_Now_DefaultClock verifies the default clock fallback path
// when nowFunc is nil.
func TestBaseDetector_Now_NilNowFunc(t *testing.T) {
	cb, _ := collectCallback()
	base, err := NewBaseDetector(BaseDetectorConfig{
		Name:     "TestNilNow",
		Severity: "warning",
		Cooldown: time.Minute,
		Callback: cb,
		Logger:   silentLogger(),
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Explicitly set nowFunc to nil to test the fallback.
	base.mu.Lock()
	base.nowFunc = nil
	base.mu.Unlock()

	now := base.Now()
	if now.IsZero() {
		t.Error("Now() returned zero time with nil nowFunc")
	}
	// Verify the time is approximately current (within 5 seconds).
	diff := time.Since(now)
	if diff < 0 || diff > 5*time.Second {
		t.Errorf("Now() returned %v, expected approximately current time", now)
	}
}

// TestDetector_ConcurrentStartStop tests concurrent Start/Stop operations.
func TestDetector_ConcurrentStartStop(t *testing.T) {
	cb, _ := collectCallback()
	d, err := NewPodCrashLoop(PodCrashLoopConfig{Callback: cb, Logger: silentLogger()})
	if err != nil {
		t.Fatalf("constructor error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- d.Start(ctx)
	}()

	time.Sleep(10 * time.Millisecond)

	// Call Stop concurrently - should not race or panic.
	done := make(chan struct{})
	go func() {
		d.Stop()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("concurrent Stop() did not return")
	}

	select {
	case err := <-errCh:
		if err != context.Canceled {
			t.Errorf("Start() returned %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Start() did not return after concurrent Stop()")
	}
}
