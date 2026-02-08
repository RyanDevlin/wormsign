package controller

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/k8s-wormsign/k8s-wormsign/internal/config"
	"github.com/k8s-wormsign/k8s-wormsign/internal/health"
	"github.com/k8s-wormsign/k8s-wormsign/internal/metrics"
	"github.com/k8s-wormsign/k8s-wormsign/internal/shard"
)

// newTestOpts creates a valid Options struct for testing using a fake clientset.
func newTestOpts(t *testing.T) Options {
	t.Helper()
	reg := prometheus.NewRegistry()
	m := metrics.NewMetrics(reg)
	h := health.NewHandler()
	cfg := config.Default()
	clientset := fake.NewSimpleClientset()

	return Options{
		Logger:        slog.Default(),
		Config:        cfg,
		Clientset:     clientset,
		HealthHandler: h,
		Metrics:       m,
	}
}

func TestNew_ValidOptions(t *testing.T) {
	opts := newTestOpts(t)
	ctrl, err := New(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctrl == nil {
		t.Fatal("expected non-nil controller")
	}
}

func TestNew_NilConfig(t *testing.T) {
	opts := newTestOpts(t)
	opts.Config = nil
	_, err := New(opts)
	if err == nil {
		t.Fatal("expected error for nil config")
	}
}

func TestNew_NilClientset(t *testing.T) {
	opts := newTestOpts(t)
	opts.Clientset = nil
	_, err := New(opts)
	if err == nil {
		t.Fatal("expected error for nil clientset")
	}
}

func TestNew_NilHealthHandler(t *testing.T) {
	opts := newTestOpts(t)
	opts.HealthHandler = nil
	_, err := New(opts)
	if err == nil {
		t.Fatal("expected error for nil health handler")
	}
}

func TestNew_NilMetrics(t *testing.T) {
	opts := newTestOpts(t)
	opts.Metrics = nil
	_, err := New(opts)
	if err == nil {
		t.Fatal("expected error for nil metrics")
	}
}

func TestNew_DefaultLogger(t *testing.T) {
	opts := newTestOpts(t)
	opts.Logger = nil // should use slog.Default()
	ctrl, err := New(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ctrl.logger == nil {
		t.Fatal("expected non-nil logger")
	}
}

func TestController_RunAndShutdown(t *testing.T) {
	t.Setenv("POD_NAME", "test-pod")
	t.Setenv("POD_NAMESPACE", "default")

	opts := newTestOpts(t)
	ctrl, err := New(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- ctrl.Run(ctx)
	}()

	// Give the controller time to start up.
	time.Sleep(500 * time.Millisecond)

	// Cancel the context to trigger shutdown.
	cancel()

	// Wait for Run to return.
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for Run to return")
	}

	// Verify shutdown ordering per Section 2.6.
	order := ctrl.ShutdownOrder()

	// Expected order: 7 stages per Section 2.6 shutdown sequence.
	expected := []string{
		"detection_stop",   // Step 1: Stop accepting new fault events
		"informers_stop",   // Step 2: Stop informers
		"correlation_drain", // Step 3: Drain correlation window
		"gathering_drain",  // Step 4: Wait for in-flight gatherers (30s)
		"analysis_drain",   // Step 5: Wait for in-flight analyzers (60s)
		"sink_drain",       // Step 6: Deliver pending sink messages (30s)
		"done",             // Step 7: Exit
	}
	if len(order) != len(expected) {
		t.Fatalf("expected %d shutdown stages, got %d: %v", len(expected), len(order), order)
	}
	for i, want := range expected {
		if order[i] != want {
			t.Errorf("shutdown stage[%d] = %q, want %q", i, order[i], want)
		}
	}
}

func TestController_ShutdownOrder_DetectionBeforeInformersBeforeDrain(t *testing.T) {
	// Verify Section 2.6 ordering: detection stop → informers stop →
	// correlation drain → gathering drain → analysis drain → sink drain → done.
	t.Setenv("POD_NAME", "test-pod-order")
	t.Setenv("POD_NAMESPACE", "default")

	opts := newTestOpts(t)
	ctrl, err := New(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- ctrl.Run(ctx)
	}()

	time.Sleep(500 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for Run to return")
	}

	order := ctrl.ShutdownOrder()

	// Find indices of key stages.
	stageIndices := make(map[string]int)
	for i, stage := range order {
		stageIndices[stage] = i
	}

	// Verify detection stops before informers.
	detIdx, ok := stageIndices["detection_stop"]
	if !ok {
		t.Fatal("detection_stop not found in shutdown order")
	}
	infIdx, ok := stageIndices["informers_stop"]
	if !ok {
		t.Fatal("informers_stop not found in shutdown order")
	}
	if detIdx >= infIdx {
		t.Errorf("detection_stop (index %d) should come before informers_stop (index %d)", detIdx, infIdx)
	}

	// Verify informers stop before correlation drain.
	corrIdx, ok := stageIndices["correlation_drain"]
	if !ok {
		t.Fatal("correlation_drain not found in shutdown order")
	}
	if infIdx >= corrIdx {
		t.Errorf("informers_stop (index %d) should come before correlation_drain (index %d)", infIdx, corrIdx)
	}

	// Verify gathering comes after correlation.
	gathIdx, ok := stageIndices["gathering_drain"]
	if !ok {
		t.Fatal("gathering_drain not found in shutdown order")
	}
	if corrIdx >= gathIdx {
		t.Errorf("correlation_drain (index %d) should come before gathering_drain (index %d)", corrIdx, gathIdx)
	}

	// Verify analysis comes after gathering.
	analIdx, ok := stageIndices["analysis_drain"]
	if !ok {
		t.Fatal("analysis_drain not found in shutdown order")
	}
	if gathIdx >= analIdx {
		t.Errorf("gathering_drain (index %d) should come before analysis_drain (index %d)", gathIdx, analIdx)
	}

	// Verify sinks come after analysis.
	sinkIdx, ok := stageIndices["sink_drain"]
	if !ok {
		t.Fatal("sink_drain not found in shutdown order")
	}
	if analIdx >= sinkIdx {
		t.Errorf("analysis_drain (index %d) should come before sink_drain (index %d)", analIdx, sinkIdx)
	}

	// Verify done is last.
	doneIdx, ok := stageIndices["done"]
	if !ok {
		t.Fatal("done not found in shutdown order")
	}
	if sinkIdx >= doneIdx {
		t.Errorf("sink_drain (index %d) should come before done (index %d)", sinkIdx, doneIdx)
	}
}

func TestController_DoubleRunReturnsError(t *testing.T) {
	t.Setenv("POD_NAME", "test-pod-double")
	t.Setenv("POD_NAMESPACE", "default")

	opts := newTestOpts(t)
	ctrl, err := New(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Start the first Run in background.
	errCh := make(chan error, 1)
	go func() {
		errCh <- ctrl.Run(ctx)
	}()

	// Give it time to start.
	time.Sleep(300 * time.Millisecond)

	// Second Run should fail immediately.
	err = ctrl.Run(ctx)
	if err == nil {
		t.Fatal("expected error for double Run")
	}

	// Clean up.
	cancel()
	select {
	case <-errCh:
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for first Run to return")
	}
}

func TestController_RestartAfterStopReturnsError(t *testing.T) {
	t.Setenv("POD_NAME", "test-pod-restart")
	t.Setenv("POD_NAMESPACE", "default")

	opts := newTestOpts(t)
	ctrl, err := New(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- ctrl.Run(ctx)
	}()

	time.Sleep(300 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("timed out")
	}

	// Attempting to restart should fail.
	err = ctrl.Run(context.Background())
	if err == nil {
		t.Fatal("expected error for restarting a stopped controller")
	}
}

func TestController_HealthHandlerIsReady(t *testing.T) {
	t.Setenv("POD_NAME", "test-pod-health")
	t.Setenv("POD_NAMESPACE", "default")

	opts := newTestOpts(t)
	h := health.NewHandler()
	opts.HealthHandler = h

	ctrl, err := New(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Before Run, readiness should fail (no API server, no informers, no detectors).
	if err := h.ReadyzCheck(); err == nil {
		t.Fatal("expected readiness to fail before Run")
	}

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- ctrl.Run(ctx)
	}()

	// Wait for controller to set health state.
	time.Sleep(500 * time.Millisecond)

	// After Run starts, readiness should pass.
	if err := h.ReadyzCheck(); err != nil {
		t.Errorf("expected readiness to pass after Run started, got: %v", err)
	}

	cancel()
	select {
	case <-errCh:
	case <-time.After(30 * time.Second):
		t.Fatal("timed out")
	}
}

func TestController_HeartbeatUpdated(t *testing.T) {
	t.Setenv("POD_NAME", "test-pod-heartbeat")
	t.Setenv("POD_NAMESPACE", "default")

	opts := newTestOpts(t)
	h := health.NewHandler()
	opts.HealthHandler = h

	ctrl, err := New(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- ctrl.Run(ctx)
	}()

	// The health handler sets initial heartbeat at creation time,
	// so liveness should pass immediately after Run starts.
	time.Sleep(300 * time.Millisecond)

	if err := h.LivezCheck(); err != nil {
		t.Errorf("expected liveness to pass, got: %v", err)
	}

	cancel()
	select {
	case <-errCh:
	case <-time.After(30 * time.Second):
		t.Fatal("timed out")
	}
}

func TestControllerLeaderCallbacks_OnStartedLeading(t *testing.T) {
	t.Setenv("POD_NAME", "cb-test-pod")
	t.Setenv("POD_NAMESPACE", "default")

	opts := newTestOpts(t)
	ctrl, err := New(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Create the shard manager so the callback has something to work with.
	shardMgr, err := shard.NewManager(
		opts.Clientset,
		"default",
		"cb-test-pod",
		1,
	)
	if err != nil {
		t.Fatalf("creating shard manager: %v", err)
	}
	ctrl.shardManager = shardMgr

	cb := &controllerLeaderCallbacks{
		controller: ctrl,
		logger:     ctrl.logger,
	}

	// OnStartedLeading should block until ctx is cancelled.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		cb.OnStartedLeading(ctx)
		close(done)
	}()

	// Give coordinator time to start.
	time.Sleep(200 * time.Millisecond)

	// Cancel to unblock.
	cancel()

	select {
	case <-done:
		// Good.
	case <-time.After(5 * time.Second):
		t.Fatal("OnStartedLeading did not return after context cancellation")
	}
}

func TestControllerLeaderCallbacks_OnStoppedLeading(t *testing.T) {
	t.Setenv("POD_NAME", "cb-stop-test")
	t.Setenv("POD_NAMESPACE", "default")

	opts := newTestOpts(t)
	ctrl, err := New(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	shardMgr, err := shard.NewManager(
		opts.Clientset,
		"default",
		"cb-stop-test",
		1,
	)
	if err != nil {
		t.Fatalf("creating shard manager: %v", err)
	}
	ctrl.shardManager = shardMgr

	cb := &controllerLeaderCallbacks{
		controller: ctrl,
		logger:     ctrl.logger,
	}

	// Start leading and then stop.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		cb.OnStartedLeading(ctx)
		close(done)
	}()

	time.Sleep(200 * time.Millisecond)

	// OnStoppedLeading cancels the coordinator context.
	cb.OnStoppedLeading()
	// Also cancel the outer context to let OnStartedLeading return.
	cancel()

	select {
	case <-done:
		// Good.
	case <-time.After(5 * time.Second):
		t.Fatal("OnStartedLeading did not return after OnStoppedLeading")
	}
}

func TestControllerLeaderCallbacks_OnNewLeader(t *testing.T) {
	cb := &controllerLeaderCallbacks{
		logger: slog.Default(),
	}
	// Should not panic.
	cb.OnNewLeader("some-other-pod")
}

func TestController_ShutdownTimeoutConstants(t *testing.T) {
	// Verify that the timeout constants match spec Section 2.6:
	// Total max shutdown = 120s = gatherTimeout(30s) + analyzeTimeout(60s) + sinkTimeout(30s).
	total := shutdownGatherTimeout + shutdownAnalyzeTimeout + shutdownSinkTimeout
	if total != 120*time.Second {
		t.Errorf("total shutdown timeout = %v, want 120s", total)
	}

	if shutdownGatherTimeout != 30*time.Second {
		t.Errorf("gatherTimeout = %v, want 30s", shutdownGatherTimeout)
	}
	if shutdownAnalyzeTimeout != 60*time.Second {
		t.Errorf("analyzeTimeout = %v, want 60s", shutdownAnalyzeTimeout)
	}
	if shutdownSinkTimeout != 30*time.Second {
		t.Errorf("sinkTimeout = %v, want 30s", shutdownSinkTimeout)
	}
}

func TestController_HeartbeatInterval(t *testing.T) {
	if heartbeatInterval != 10*time.Second {
		t.Errorf("heartbeatInterval = %v, want 10s", heartbeatInterval)
	}
}

func TestController_RunWithLeaderElection(t *testing.T) {
	// Verify that the full Run path creates a leader elector and all subsystems.
	t.Setenv("POD_NAME", "test-pod-leader")
	t.Setenv("POD_NAMESPACE", "default")

	opts := newTestOpts(t)
	ctrl, err := New(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- ctrl.Run(ctx)
	}()

	// Wait for leader election to kick in and coordinator to start.
	time.Sleep(1 * time.Second)

	// Verify all subsystems were created.
	if ctrl.leaderElector == nil {
		t.Error("expected leaderElector to be set")
	}
	if ctrl.shardManager == nil {
		t.Error("expected shardManager to be set")
	}
	if ctrl.informerManager == nil {
		t.Error("expected informerManager to be set")
	}
	if ctrl.pipeline == nil {
		t.Error("expected pipeline to be set")
	}
	if ctrl.filterEngine == nil {
		t.Error("expected filterEngine to be set")
	}

	cancel()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for Run to return")
	}
}

func TestController_ConcurrentShutdown(t *testing.T) {
	// Verify that concurrent context cancellation is safe.
	t.Setenv("POD_NAME", "concurrent-test")
	t.Setenv("POD_NAMESPACE", "default")

	opts := newTestOpts(t)
	ctrl, err := New(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- ctrl.Run(ctx)
	}()

	time.Sleep(300 * time.Millisecond)

	// Cancel from multiple goroutines simultaneously.
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cancel()
		}()
	}
	wg.Wait()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for Run to return")
	}
}

func TestController_SubsystemsAreNilBeforeRun(t *testing.T) {
	opts := newTestOpts(t)
	ctrl, err := New(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Before Run, subsystems should not yet be initialized.
	if ctrl.leaderElector != nil {
		t.Error("leaderElector should be nil before Run")
	}
	if ctrl.shardManager != nil {
		t.Error("shardManager should be nil before Run")
	}
	if ctrl.informerManager != nil {
		t.Error("informerManager should be nil before Run")
	}
	if ctrl.pipeline != nil {
		t.Error("pipeline should be nil before Run")
	}
	if ctrl.filterEngine != nil {
		t.Error("filterEngine should be nil before Run")
	}
}

func TestController_ShutdownOrderIsEmpty_BeforeRun(t *testing.T) {
	opts := newTestOpts(t)
	ctrl, err := New(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	order := ctrl.ShutdownOrder()
	if len(order) != 0 {
		t.Errorf("expected empty shutdown order before run, got %v", order)
	}
}
