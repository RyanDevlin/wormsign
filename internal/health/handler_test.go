package health

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// fixedTime returns an Option that pins the clock to the given time.
// The returned advanceFn moves the clock forward by the given duration.
func fixedTime(t time.Time) (Option, func(d time.Duration)) {
	mu := &sync.Mutex{}
	now := t
	advanceFn := func(d time.Duration) {
		mu.Lock()
		defer mu.Unlock()
		now = now.Add(d)
	}
	opt := WithNowFunc(func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	})
	return opt, advanceFn
}

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// --- NewHandler tests ---

func TestNewHandler_DefaultHeartbeat(t *testing.T) {
	baseTime := time.Date(2026, 2, 8, 12, 0, 0, 0, time.UTC)
	timeOpt, _ := fixedTime(baseTime)

	h := NewHandler(timeOpt, WithLogger(silentLogger()))

	// Heartbeat should be set to baseTime at construction.
	lastNano := h.heartbeat.Load()
	if lastNano != baseTime.UnixNano() {
		t.Errorf("expected initial heartbeat %d, got %d", baseTime.UnixNano(), lastNano)
	}
}

func TestNewHandler_DefaultsWork(t *testing.T) {
	h := NewHandler()
	if h.logger == nil {
		t.Error("expected non-nil logger")
	}
	if h.nowFunc == nil {
		t.Error("expected non-nil nowFunc")
	}
	// Should be alive immediately after construction.
	if err := h.LivezCheck(); err != nil {
		t.Errorf("liveness should pass immediately after construction: %v", err)
	}
}

// --- UpdateHeartbeat tests ---

func TestUpdateHeartbeat(t *testing.T) {
	baseTime := time.Date(2026, 2, 8, 12, 0, 0, 0, time.UTC)
	timeOpt, advance := fixedTime(baseTime)
	h := NewHandler(timeOpt, WithLogger(silentLogger()))

	advance(10 * time.Second)
	h.UpdateHeartbeat()

	expected := baseTime.Add(10 * time.Second).UnixNano()
	if got := h.heartbeat.Load(); got != expected {
		t.Errorf("expected heartbeat %d, got %d", expected, got)
	}
}

// --- LivezCheck tests ---

func TestLivezCheck_PassesWhenHeartbeatFresh(t *testing.T) {
	baseTime := time.Date(2026, 2, 8, 12, 0, 0, 0, time.UTC)
	timeOpt, advance := fixedTime(baseTime)
	h := NewHandler(timeOpt, WithLogger(silentLogger()))

	// Advance 29 seconds — still within 30s threshold.
	advance(29 * time.Second)
	if err := h.LivezCheck(); err != nil {
		t.Errorf("liveness should pass at 29s: %v", err)
	}
}

func TestLivezCheck_PassesAtExactThreshold(t *testing.T) {
	baseTime := time.Date(2026, 2, 8, 12, 0, 0, 0, time.UTC)
	timeOpt, advance := fixedTime(baseTime)
	h := NewHandler(timeOpt, WithLogger(silentLogger()))

	// Advance exactly 30 seconds — still passes (not strictly greater).
	advance(30 * time.Second)
	if err := h.LivezCheck(); err != nil {
		t.Errorf("liveness should pass at exactly 30s: %v", err)
	}
}

func TestLivezCheck_FailsWhenHeartbeatStale(t *testing.T) {
	baseTime := time.Date(2026, 2, 8, 12, 0, 0, 0, time.UTC)
	timeOpt, advance := fixedTime(baseTime)
	h := NewHandler(timeOpt, WithLogger(silentLogger()))

	// Advance 31 seconds — past the 30s threshold.
	advance(31 * time.Second)
	if err := h.LivezCheck(); err == nil {
		t.Error("liveness should fail at 31s")
	}
}

func TestLivezCheck_RecoverAfterHeartbeatUpdate(t *testing.T) {
	baseTime := time.Date(2026, 2, 8, 12, 0, 0, 0, time.UTC)
	timeOpt, advance := fixedTime(baseTime)
	h := NewHandler(timeOpt, WithLogger(silentLogger()))

	// Let the heartbeat go stale.
	advance(35 * time.Second)
	if err := h.LivezCheck(); err == nil {
		t.Error("liveness should fail when stale")
	}

	// Update heartbeat and verify recovery.
	h.UpdateHeartbeat()
	if err := h.LivezCheck(); err != nil {
		t.Errorf("liveness should pass after heartbeat update: %v", err)
	}
}

// --- ReadyzCheck tests ---

func TestReadyzCheck_FailsWhenAPIServerUnreachable(t *testing.T) {
	h := NewHandler(WithLogger(silentLogger()))
	// All conditions false by default.
	err := h.ReadyzCheck()
	if err == nil {
		t.Error("readiness should fail when API server is unreachable")
	}
}

func TestReadyzCheck_FailsWhenInformersNotSynced(t *testing.T) {
	h := NewHandler(WithLogger(silentLogger()))
	h.SetAPIServerReachable(true)

	err := h.ReadyzCheck()
	if err == nil {
		t.Error("readiness should fail when informers not synced")
	}
}

func TestReadyzCheck_FailsWhenNoDetectors(t *testing.T) {
	h := NewHandler(WithLogger(silentLogger()))
	h.SetAPIServerReachable(true)
	h.SetInformersSynced(true)
	// detectorCount is 0 by default.

	err := h.ReadyzCheck()
	if err == nil {
		t.Error("readiness should fail when no detectors running")
	}
}

func TestReadyzCheck_PassesWhenAllConditionsMet(t *testing.T) {
	h := NewHandler(WithLogger(silentLogger()))
	h.SetAPIServerReachable(true)
	h.SetInformersSynced(true)
	h.SetDetectorCount(3)

	if err := h.ReadyzCheck(); err != nil {
		t.Errorf("readiness should pass when all conditions met: %v", err)
	}
}

func TestReadyzCheck_FailsWhenAPIServerBecomesUnreachable(t *testing.T) {
	h := NewHandler(WithLogger(silentLogger()))
	h.SetAPIServerReachable(true)
	h.SetInformersSynced(true)
	h.SetDetectorCount(2)

	// Initially ready.
	if err := h.ReadyzCheck(); err != nil {
		t.Fatalf("should initially be ready: %v", err)
	}

	// API server goes away.
	h.SetAPIServerReachable(false)
	if err := h.ReadyzCheck(); err == nil {
		t.Error("readiness should fail when API server becomes unreachable")
	}
}

func TestReadyzCheck_FailsWhenDetectorCountDropsToZero(t *testing.T) {
	h := NewHandler(WithLogger(silentLogger()))
	h.SetAPIServerReachable(true)
	h.SetInformersSynced(true)
	h.SetDetectorCount(5)

	if err := h.ReadyzCheck(); err != nil {
		t.Fatalf("should initially be ready: %v", err)
	}

	h.SetDetectorCount(0)
	if err := h.ReadyzCheck(); err == nil {
		t.Error("readiness should fail when detector count drops to 0")
	}
}

// --- HTTP endpoint tests ---

func TestHandleLivez_Returns200WhenHealthy(t *testing.T) {
	h := NewHandler(WithLogger(silentLogger()))

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	h.HandleLivez(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	var resp statusResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("expected status 'ok', got %q", resp.Status)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected Content-Type application/json, got %q", ct)
	}
}

func TestHandleLivez_Returns503WhenStale(t *testing.T) {
	baseTime := time.Date(2026, 2, 8, 12, 0, 0, 0, time.UTC)
	timeOpt, advance := fixedTime(baseTime)
	h := NewHandler(timeOpt, WithLogger(silentLogger()))

	advance(35 * time.Second)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	h.HandleLivez(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", rec.Code)
	}

	var resp statusResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Status != "fail" {
		t.Errorf("expected status 'fail', got %q", resp.Status)
	}
	if resp.Details["heartbeat"] == "" {
		t.Error("expected non-empty heartbeat detail in failure response")
	}
}

func TestHandleReadyz_Returns200WhenReady(t *testing.T) {
	h := NewHandler(WithLogger(silentLogger()))
	h.SetAPIServerReachable(true)
	h.SetInformersSynced(true)
	h.SetDetectorCount(1)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	h.HandleReadyz(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	var resp statusResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Status != "ok" {
		t.Errorf("expected status 'ok', got %q", resp.Status)
	}
}

func TestHandleReadyz_Returns503WhenNotReady(t *testing.T) {
	h := NewHandler(WithLogger(silentLogger()))
	// All defaults are false/0 — not ready.

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	h.HandleReadyz(rec, req)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("expected status 503, got %d", rec.Code)
	}

	var resp statusResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Status != "fail" {
		t.Errorf("expected status 'fail', got %q", resp.Status)
	}
	if resp.Details["reason"] == "" {
		t.Error("expected non-empty reason detail in failure response")
	}
}

// --- Readiness failure message specificity ---

func TestHandleReadyz_FailureMessages(t *testing.T) {
	tests := []struct {
		name            string
		apiReachable    bool
		informersSynced bool
		detectorCount   int
		wantContains    string
	}{
		{
			name:         "api server unreachable",
			wantContains: "API server",
		},
		{
			name:         "informers not synced",
			apiReachable: true,
			wantContains: "informers",
		},
		{
			name:            "no detectors",
			apiReachable:    true,
			informersSynced: true,
			detectorCount:   0,
			wantContains:    "no detectors",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := NewHandler(WithLogger(silentLogger()))
			h.SetAPIServerReachable(tt.apiReachable)
			h.SetInformersSynced(tt.informersSynced)
			h.SetDetectorCount(tt.detectorCount)

			err := h.ReadyzCheck()
			if err == nil {
				t.Fatal("expected error")
			}
			if got := err.Error(); !containsSubstring(got, tt.wantContains) {
				t.Errorf("error %q does not contain %q", got, tt.wantContains)
			}
		})
	}
}

// --- ServeMux routing tests ---

func TestNewServeMux_RoutesHealthz(t *testing.T) {
	h := NewHandler(WithLogger(silentLogger()))
	mux := h.NewServeMux()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 from /healthz, got %d", rec.Code)
	}
}

func TestNewServeMux_RoutesReadyz(t *testing.T) {
	h := NewHandler(WithLogger(silentLogger()))
	h.SetAPIServerReachable(true)
	h.SetInformersSynced(true)
	h.SetDetectorCount(1)

	mux := h.NewServeMux()

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 from /readyz, got %d", rec.Code)
	}
}

// --- Server construction tests ---

func TestNewServer_ValidPort(t *testing.T) {
	h := NewHandler(WithLogger(silentLogger()))
	srv, err := NewServer(h, 8081)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if srv == nil {
		t.Fatal("expected non-nil Server")
	}
}

func TestNewServer_NilHandler(t *testing.T) {
	_, err := NewServer(nil, 8081)
	if err == nil {
		t.Error("expected error for nil handler")
	}
}

func TestNewServer_InvalidPort(t *testing.T) {
	h := NewHandler(WithLogger(silentLogger()))

	tests := []struct {
		name string
		port int
	}{
		{"zero", 0},
		{"negative", -1},
		{"too high", 65536},
		{"way too high", 100000},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewServer(h, tt.port)
			if err == nil {
				t.Errorf("expected error for port %d", tt.port)
			}
		})
	}
}

func TestNewServer_DefaultPort(t *testing.T) {
	if DefaultPort != 8081 {
		t.Errorf("expected DefaultPort 8081, got %d", DefaultPort)
	}
}

// --- Integration test with real HTTP server ---

func TestServer_IntegrationHealthEndpoints(t *testing.T) {
	h := NewHandler(WithLogger(silentLogger()))
	h.SetAPIServerReachable(true)
	h.SetInformersSynced(true)
	h.SetDetectorCount(2)

	srv, err := NewServer(h, 0) // port 0 is invalid via NewServer
	if err == nil {
		_ = srv
	}

	// Use a listener with a dynamic port for testing.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create listener: %v", err)
	}
	defer ln.Close()

	handler := NewHandler(WithLogger(silentLogger()))
	handler.SetAPIServerReachable(true)
	handler.SetInformersSynced(true)
	handler.SetDetectorCount(2)

	server, err := NewServer(handler, 9999) // port doesn't matter; we'll use Serve()
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	go func() {
		if serveErr := server.Serve(ln); serveErr != nil && serveErr != http.ErrServerClosed {
			t.Logf("serve error: %v", serveErr)
		}
	}()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
	}()

	baseURL := "http://" + ln.Addr().String()
	client := &http.Client{Timeout: 5 * time.Second}

	// Test /healthz
	resp, err := client.Get(baseURL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected /healthz 200, got %d", resp.StatusCode)
	}

	// Test /readyz
	resp2, err := client.Get(baseURL + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz failed: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected /readyz 200, got %d", resp2.StatusCode)
	}
}

func TestServer_Shutdown(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to create listener: %v", err)
	}

	h := NewHandler(WithLogger(silentLogger()))
	srv, err := NewServer(h, 9999)
	if err != nil {
		t.Fatalf("failed to create server: %v", err)
	}

	serveDone := make(chan error, 1)
	go func() {
		serveDone <- srv.Serve(ln)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown failed: %v", err)
	}

	serveErr := <-serveDone
	if serveErr != http.ErrServerClosed {
		t.Errorf("expected ErrServerClosed, got %v", serveErr)
	}
}

// --- Concurrency safety test ---

func TestHandler_ConcurrentAccess(t *testing.T) {
	h := NewHandler(WithLogger(silentLogger()))
	done := make(chan struct{})

	// Start concurrent writers.
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			h.UpdateHeartbeat()
			h.SetAPIServerReachable(i%2 == 0)
			h.SetInformersSynced(i%3 == 0)
			h.SetDetectorCount(i % 5)
		}
	}()

	// Concurrent readers.
	for i := 0; i < 100; i++ {
		_ = h.LivezCheck()
		_ = h.ReadyzCheck()
	}

	<-done
}

// --- State transition tests ---

func TestReadyzCheck_AllStateCombinations(t *testing.T) {
	boolValues := []bool{false, true}
	detectorCounts := []int{0, 1, 5}

	for _, api := range boolValues {
		for _, inf := range boolValues {
			for _, det := range detectorCounts {
				h := NewHandler(WithLogger(silentLogger()))
				h.SetAPIServerReachable(api)
				h.SetInformersSynced(inf)
				h.SetDetectorCount(det)

				err := h.ReadyzCheck()
				shouldPass := api && inf && det >= 1

				if shouldPass && err != nil {
					t.Errorf("api=%v inf=%v det=%d: expected pass, got %v", api, inf, det, err)
				}
				if !shouldPass && err == nil {
					t.Errorf("api=%v inf=%v det=%d: expected fail, got pass", api, inf, det)
				}
			}
		}
	}
}

// containsSubstring is a helper to check if s contains substr.
func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
