package controller

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/k8s-wormsign/k8s-wormsign/internal/config"
	"github.com/k8s-wormsign/k8s-wormsign/internal/metrics"
)

// mockCallbacks records leader election callback invocations for testing.
type mockCallbacks struct {
	mu              sync.Mutex
	startedLeading  int
	stoppedLeading  int
	newLeaderCalls  []string
	startedCtx      context.Context
	onStartedFunc   func(ctx context.Context) // optional hook for custom behavior
	onStoppedFunc   func()                    // optional hook for custom behavior
	onNewLeaderFunc func(identity string)     // optional hook for custom behavior
}

func (m *mockCallbacks) OnStartedLeading(ctx context.Context) {
	m.mu.Lock()
	m.startedLeading++
	m.startedCtx = ctx
	fn := m.onStartedFunc
	m.mu.Unlock()
	if fn != nil {
		fn(ctx)
	}
}

func (m *mockCallbacks) OnStoppedLeading() {
	m.mu.Lock()
	m.stoppedLeading++
	fn := m.onStoppedFunc
	m.mu.Unlock()
	if fn != nil {
		fn()
	}
}

func (m *mockCallbacks) OnNewLeader(identity string) {
	m.mu.Lock()
	m.newLeaderCalls = append(m.newLeaderCalls, identity)
	fn := m.onNewLeaderFunc
	m.mu.Unlock()
	if fn != nil {
		fn(identity)
	}
}

func (m *mockCallbacks) getStartedLeading() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.startedLeading
}

func (m *mockCallbacks) getStoppedLeading() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.stoppedLeading
}

func (m *mockCallbacks) getNewLeaderCalls() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]string, len(m.newLeaderCalls))
	copy(result, m.newLeaderCalls)
	return result
}

// newTestMetrics creates a Metrics instance with a fresh registry for testing.
func newTestMetrics(t *testing.T) *metrics.Metrics {
	t.Helper()
	reg := prometheus.NewRegistry()
	return metrics.NewMetrics(reg)
}

func defaultLeaderConfig() config.LeaderElectionConfig {
	return config.LeaderElectionConfig{
		Enabled:       true,
		LeaseDuration: 15 * time.Second,
		RenewDeadline: 10 * time.Second,
		RetryPeriod:   2 * time.Second,
	}
}

func TestNewLeaderElector(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	m := newTestMetrics(t)
	cb := &mockCallbacks{}
	cfg := defaultLeaderConfig()

	le, err := NewLeaderElector(nil, clientset, cfg, m, cb,
		WithLeaderIdentity("test-pod"),
		WithLeaderNamespace("test-ns"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if le.identity != "test-pod" {
		t.Errorf("expected identity %q, got %q", "test-pod", le.identity)
	}
	if le.namespace != "test-ns" {
		t.Errorf("expected namespace %q, got %q", "test-ns", le.namespace)
	}
	if le.leaseName != DefaultLeaseName {
		t.Errorf("expected lease name %q, got %q", DefaultLeaseName, le.leaseName)
	}

	// Verify metric is initialized to 0.
	val := getGaugeValue(t, le.metrics.LeaderIsLeader)
	if val != 0 {
		t.Errorf("expected LeaderIsLeader metric to be 0, got %f", val)
	}
}

func TestNewLeaderElector_NilClientset(t *testing.T) {
	m := newTestMetrics(t)
	cb := &mockCallbacks{}
	cfg := defaultLeaderConfig()

	_, err := NewLeaderElector(nil, nil, cfg, m, cb)
	if err == nil {
		t.Fatal("expected error for nil clientset")
	}
}

func TestNewLeaderElector_NilMetrics(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	cb := &mockCallbacks{}
	cfg := defaultLeaderConfig()

	_, err := NewLeaderElector(nil, clientset, cfg, nil, cb)
	if err == nil {
		t.Fatal("expected error for nil metrics")
	}
}

func TestNewLeaderElector_NilCallbacks(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	m := newTestMetrics(t)
	cfg := defaultLeaderConfig()

	_, err := NewLeaderElector(nil, clientset, cfg, m, nil)
	if err == nil {
		t.Fatal("expected error for nil callbacks")
	}
}

func TestNewLeaderElector_WithLeaseName(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	m := newTestMetrics(t)
	cb := &mockCallbacks{}
	cfg := defaultLeaderConfig()

	le, err := NewLeaderElector(nil, clientset, cfg, m, cb,
		WithLeaderIdentity("pod-1"),
		WithLeaseName("custom-lease"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if le.leaseName != "custom-lease" {
		t.Errorf("expected lease name %q, got %q", "custom-lease", le.leaseName)
	}
}

func TestLeaderElector_OnStartedLeading(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	m := newTestMetrics(t)
	cb := &mockCallbacks{}
	cfg := defaultLeaderConfig()

	le, err := NewLeaderElector(nil, clientset, cfg, m, cb,
		WithLeaderIdentity("pod-1"),
		WithLeaderNamespace("ns-1"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx := context.Background()
	le.onStartedLeading(ctx)

	if !le.IsLeader() {
		t.Error("expected IsLeader to be true after onStartedLeading")
	}

	val := getGaugeValue(t, le.metrics.LeaderIsLeader)
	if val != 1 {
		t.Errorf("expected LeaderIsLeader metric to be 1, got %f", val)
	}

	if cb.getStartedLeading() != 1 {
		t.Errorf("expected OnStartedLeading callback to be called once, got %d", cb.getStartedLeading())
	}
}

func TestLeaderElector_OnStoppedLeading(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	m := newTestMetrics(t)
	cb := &mockCallbacks{}
	cfg := defaultLeaderConfig()

	le, err := NewLeaderElector(nil, clientset, cfg, m, cb,
		WithLeaderIdentity("pod-1"),
		WithLeaderNamespace("ns-1"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Simulate gaining and then losing leadership.
	ctx := context.Background()
	le.onStartedLeading(ctx)
	le.onStoppedLeading()

	if le.IsLeader() {
		t.Error("expected IsLeader to be false after onStoppedLeading")
	}

	val := getGaugeValue(t, le.metrics.LeaderIsLeader)
	if val != 0 {
		t.Errorf("expected LeaderIsLeader metric to be 0, got %f", val)
	}

	if cb.getStoppedLeading() != 1 {
		t.Errorf("expected OnStoppedLeading callback to be called once, got %d", cb.getStoppedLeading())
	}
}

func TestLeaderElector_OnNewLeader_Self(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	m := newTestMetrics(t)
	cb := &mockCallbacks{}
	cfg := defaultLeaderConfig()

	le, err := NewLeaderElector(nil, clientset, cfg, m, cb,
		WithLeaderIdentity("pod-1"),
		WithLeaderNamespace("ns-1"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	le.onNewLeader("pod-1")

	calls := cb.getNewLeaderCalls()
	if len(calls) != 1 || calls[0] != "pod-1" {
		t.Errorf("expected OnNewLeader to be called with pod-1, got %v", calls)
	}
}

func TestLeaderElector_OnNewLeader_Other(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	m := newTestMetrics(t)
	cb := &mockCallbacks{}
	cfg := defaultLeaderConfig()

	le, err := NewLeaderElector(nil, clientset, cfg, m, cb,
		WithLeaderIdentity("pod-1"),
		WithLeaderNamespace("ns-1"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	le.onNewLeader("pod-2")

	calls := cb.getNewLeaderCalls()
	if len(calls) != 1 || calls[0] != "pod-2" {
		t.Errorf("expected OnNewLeader to be called with pod-2, got %v", calls)
	}
}

func TestLeaderElector_MetricTransitions(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	m := newTestMetrics(t)
	cb := &mockCallbacks{}
	cfg := defaultLeaderConfig()

	le, err := NewLeaderElector(nil, clientset, cfg, m, cb,
		WithLeaderIdentity("pod-1"),
		WithLeaderNamespace("ns-1"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Initial state: not leader.
	val := getGaugeValue(t, le.metrics.LeaderIsLeader)
	if val != 0 {
		t.Fatalf("expected initial metric 0, got %f", val)
	}

	// Gain leadership.
	ctx := context.Background()
	le.onStartedLeading(ctx)
	val = getGaugeValue(t, le.metrics.LeaderIsLeader)
	if val != 1 {
		t.Fatalf("expected metric 1 after gaining leadership, got %f", val)
	}

	// Lose leadership.
	le.onStoppedLeading()
	val = getGaugeValue(t, le.metrics.LeaderIsLeader)
	if val != 0 {
		t.Fatalf("expected metric 0 after losing leadership, got %f", val)
	}

	// Regain leadership.
	le.onStartedLeading(ctx)
	val = getGaugeValue(t, le.metrics.LeaderIsLeader)
	if val != 1 {
		t.Fatalf("expected metric 1 after regaining leadership, got %f", val)
	}
}

func TestLeaderElector_CallbackReceivesContext(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	m := newTestMetrics(t)

	var receivedCtx context.Context
	cb := &mockCallbacks{
		onStartedFunc: func(ctx context.Context) {
			receivedCtx = ctx
		},
	}
	cfg := defaultLeaderConfig()

	le, err := NewLeaderElector(nil, clientset, cfg, m, cb,
		WithLeaderIdentity("pod-1"),
		WithLeaderNamespace("ns-1"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	le.onStartedLeading(ctx)

	if receivedCtx == nil {
		t.Fatal("OnStartedLeading should receive a non-nil context")
	}

	// Verify the context is the one we passed.
	cancel()
	select {
	case <-receivedCtx.Done():
		// Good — context propagated.
	case <-time.After(time.Second):
		t.Fatal("expected context to be cancelled")
	}
}

func TestLeaderElector_Run_WithFakeClientset(t *testing.T) {
	// This test verifies that Run integrates with the client-go leader
	// election library correctly by running against a fake clientset.
	// The fake clientset supports Lease operations, so the elector
	// should be able to acquire the lease.
	clientset := fake.NewSimpleClientset()
	m := newTestMetrics(t)

	started := make(chan struct{})
	cb := &mockCallbacks{
		onStartedFunc: func(ctx context.Context) {
			close(started)
			// Block until context is cancelled (leadership lost).
			<-ctx.Done()
		},
	}
	cfg := defaultLeaderConfig()

	le, err := NewLeaderElector(nil, clientset, cfg, m, cb,
		WithLeaderIdentity("test-pod"),
		WithLeaderNamespace("default"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Run leader election in a goroutine.
	errCh := make(chan error, 1)
	go func() {
		errCh <- le.Run(ctx)
	}()

	// Wait for leadership to be acquired.
	select {
	case <-started:
		// Leadership acquired.
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for leader election")
	}

	// Verify we are the leader.
	if !le.IsLeader() {
		t.Error("expected IsLeader to be true")
	}

	val := getGaugeValue(t, le.metrics.LeaderIsLeader)
	if val != 1 {
		t.Errorf("expected metric to be 1, got %f", val)
	}

	// Cancel the context to trigger leadership loss.
	cancel()

	// Wait for Run to return.
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Run to return")
	}

	// After Run exits, the stopped callback should have been called.
	if cb.getStoppedLeading() < 1 {
		t.Error("expected OnStoppedLeading to be called at least once")
	}
}

func TestLeaderElector_Identity(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	m := newTestMetrics(t)
	cb := &mockCallbacks{}
	cfg := defaultLeaderConfig()

	le, err := NewLeaderElector(nil, clientset, cfg, m, cb,
		WithLeaderIdentity("my-pod-xyz"),
		WithLeaderNamespace("ns"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if le.Identity() != "my-pod-xyz" {
		t.Errorf("expected identity %q, got %q", "my-pod-xyz", le.Identity())
	}
}

func TestLeaderElector_DefaultsNamespace(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	m := newTestMetrics(t)
	cb := &mockCallbacks{}
	cfg := defaultLeaderConfig()

	// Don't set POD_NAMESPACE, don't use WithLeaderNamespace.
	t.Setenv("POD_NAMESPACE", "")

	le, err := NewLeaderElector(nil, clientset, cfg, m, cb,
		WithLeaderIdentity("pod-1"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if le.namespace != DefaultControllerNamespace {
		t.Errorf("expected default namespace %q, got %q", DefaultControllerNamespace, le.namespace)
	}
}

func TestLeaderElector_NamespaceFromEnv(t *testing.T) {
	clientset := fake.NewSimpleClientset()
	m := newTestMetrics(t)
	cb := &mockCallbacks{}
	cfg := defaultLeaderConfig()

	t.Setenv("POD_NAMESPACE", "custom-ns")

	le, err := NewLeaderElector(nil, clientset, cfg, m, cb,
		WithLeaderIdentity("pod-1"),
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if le.namespace != "custom-ns" {
		t.Errorf("expected namespace %q, got %q", "custom-ns", le.namespace)
	}
}

func TestResolveIdentity_FromEnv(t *testing.T) {
	t.Setenv("POD_NAME", "wormsign-controller-0")
	id := resolveIdentity()
	if id != "wormsign-controller-0" {
		t.Errorf("expected identity from POD_NAME, got %q", id)
	}
}

func TestResolveIdentity_FallbackHostname(t *testing.T) {
	t.Setenv("POD_NAME", "")
	id := resolveIdentity()
	if id == "" {
		t.Error("expected non-empty identity from hostname fallback")
	}
}

func TestDefaultLeaseName(t *testing.T) {
	if DefaultLeaseName != "wormsign-leader" {
		t.Errorf("expected lease name %q, got %q", "wormsign-leader", DefaultLeaseName)
	}
}

// getGaugeValue reads the current value of a Prometheus Gauge.
func getGaugeValue(t *testing.T, g prometheus.Gauge) float64 {
	t.Helper()
	m := &dto.Metric{}
	if err := g.Write(m); err != nil {
		t.Fatalf("failed to read gauge: %v", err)
	}
	return m.GetGauge().GetValue()
}
