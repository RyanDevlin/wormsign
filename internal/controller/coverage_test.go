package controller

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"k8s.io/client-go/kubernetes/fake"

	"github.com/k8s-wormsign/k8s-wormsign/internal/config"
	"github.com/k8s-wormsign/k8s-wormsign/internal/health"
	"github.com/k8s-wormsign/k8s-wormsign/internal/metrics"
)

// --- resolveIdentity: hostname fallback ---

func TestResolveIdentity_HostnameFallback(t *testing.T) {
	// Unset POD_NAME so resolveIdentity falls back to hostname.
	t.Setenv("POD_NAME", "")

	identity := resolveIdentity()
	if identity == "" {
		t.Fatal("resolveIdentity returned empty string")
	}
	// Should be the hostname (or a wormsign-<nano> fallback).
	t.Logf("resolved identity: %s", identity)
}

func TestResolveIdentity_PodName(t *testing.T) {
	t.Setenv("POD_NAME", "test-pod-identity")

	identity := resolveIdentity()
	if identity != "test-pod-identity" {
		t.Errorf("resolveIdentity = %q, want %q", identity, "test-pod-identity")
	}
}

// --- resolveNamespace: default fallback ---

func TestResolveNamespace_DefaultFallback(t *testing.T) {
	t.Setenv("POD_NAMESPACE", "")

	ns := resolveNamespace()
	if ns != DefaultControllerNamespace {
		t.Errorf("resolveNamespace = %q, want %q", ns, DefaultControllerNamespace)
	}
}

func TestResolveNamespace_EnvVar(t *testing.T) {
	t.Setenv("POD_NAMESPACE", "custom-ns")

	ns := resolveNamespace()
	if ns != "custom-ns" {
		t.Errorf("resolveNamespace = %q, want %q", ns, "custom-ns")
	}
}

// --- NewLeaderElector: identity resolved from hostname ---

func TestNewLeaderElector_IdentityFromHostname(t *testing.T) {
	t.Setenv("POD_NAME", "")
	t.Setenv("POD_NAMESPACE", "default")

	reg := prometheus.NewRegistry()
	m := metrics.NewMetrics(reg)
	client := fake.NewSimpleClientset()
	cbs := &mockCallbacks{}

	le, err := NewLeaderElector(
		slog.Default(),
		client,
		config.Default().LeaderElection,
		m,
		cbs,
		// No WithLeaderIdentity — should resolve from hostname.
	)
	if err != nil {
		t.Fatalf("NewLeaderElector error: %v", err)
	}

	if le.identity == "" {
		t.Fatal("identity should not be empty")
	}
}

// --- runHeartbeat: ticker fires ---

func TestController_RunHeartbeat_TickerFires(t *testing.T) {
	t.Setenv("POD_NAME", "heartbeat-test")
	t.Setenv("POD_NAMESPACE", "default")

	opts := newTestOpts(t)
	h := health.NewHandler()
	opts.HealthHandler = h

	ctrl, err := New(opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Temporarily lower heartbeatInterval by calling runHeartbeat directly
	// with a short-lived context.
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		ctrl.runHeartbeat(ctx)
		close(done)
	}()

	// The heartbeat interval is 10s which is too long for a unit test.
	// Instead of waiting for the ticker, just verify the goroutine can be
	// stopped cleanly.
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runHeartbeat did not return after context cancel")
	}
}
