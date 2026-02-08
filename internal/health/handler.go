// Package health provides HTTP handlers for Kubernetes liveness and readiness
// probes as specified in Section 2.7 of the project specification.
//
// The Handler struct is goroutine-safe: state is updated from the main
// goroutine and controller subsystems, while HTTP handlers read it
// concurrently from the probe server.
package health

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

// DefaultPort is the default listen port for health probe endpoints.
const DefaultPort = 8081

// HeartbeatTimeout is the maximum time since the last heartbeat before
// the liveness probe reports failure. Per Section 2.7, this is 30 seconds.
const HeartbeatTimeout = 30 * time.Second

// statusResponse is the JSON body returned by health endpoints.
type statusResponse struct {
	Status  string            `json:"status"`
	Details map[string]string `json:"details,omitempty"`
}

// Handler manages health and readiness state and serves HTTP probe endpoints.
// All state-mutation methods are goroutine-safe.
type Handler struct {
	logger *slog.Logger

	// heartbeat tracks the last time UpdateHeartbeat was called.
	// Stored as UnixNano. Accessed atomically.
	heartbeat atomic.Int64

	// nowFunc returns the current time. Overridable for testing.
	nowFunc func() time.Time

	// mu guards the readiness fields below.
	mu                 sync.RWMutex
	apiServerReachable bool
	informersSynced    bool
	detectorCount      int
}

// Option configures a Handler.
type Option func(*Handler)

// WithLogger sets the logger for the Handler. If not set, slog.Default() is used.
func WithLogger(logger *slog.Logger) Option {
	return func(h *Handler) {
		h.logger = logger
	}
}

// WithNowFunc overrides the time source. Intended for testing.
func WithNowFunc(fn func() time.Time) Option {
	return func(h *Handler) {
		h.nowFunc = fn
	}
}

// NewHandler creates a new Handler with the given options.
// The initial heartbeat is set to the current time so the liveness probe
// succeeds immediately after startup.
func NewHandler(opts ...Option) *Handler {
	h := &Handler{
		logger:  slog.Default(),
		nowFunc: time.Now,
	}
	for _, opt := range opts {
		opt(h)
	}
	// Set initial heartbeat to now so liveness succeeds at startup.
	h.heartbeat.Store(h.nowFunc().UnixNano())
	return h
}

// UpdateHeartbeat records that the main goroutine is alive.
// This should be called periodically (e.g., every 10s) from the main loop.
func (h *Handler) UpdateHeartbeat() {
	h.heartbeat.Store(h.nowFunc().UnixNano())
}

// SetAPIServerReachable updates whether the Kubernetes API server is reachable.
func (h *Handler) SetAPIServerReachable(reachable bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.apiServerReachable = reachable
}

// SetInformersSynced updates whether shared informers have completed their
// initial sync.
func (h *Handler) SetInformersSynced(synced bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.informersSynced = synced
}

// SetDetectorCount updates the number of currently running detectors.
func (h *Handler) SetDetectorCount(count int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.detectorCount = count
}

// LivezCheck returns nil if the liveness check passes, or an error describing
// why it fails. Exported for programmatic use.
func (h *Handler) LivezCheck() error {
	lastNano := h.heartbeat.Load()
	last := time.Unix(0, lastNano)
	elapsed := h.nowFunc().Sub(last)
	if elapsed > HeartbeatTimeout {
		return fmt.Errorf("heartbeat stale: last update %s ago (threshold %s)", elapsed.Round(time.Second), HeartbeatTimeout)
	}
	return nil
}

// ReadyzCheck returns nil if the readiness check passes, or an error describing
// why it fails. Exported for programmatic use.
func (h *Handler) ReadyzCheck() error {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if !h.apiServerReachable {
		return fmt.Errorf("API server is not reachable")
	}
	if !h.informersSynced {
		return fmt.Errorf("informers have not synced")
	}
	if h.detectorCount < 1 {
		return fmt.Errorf("no detectors running (count: %d)", h.detectorCount)
	}
	return nil
}

// HandleLivez is the HTTP handler for the /healthz liveness endpoint.
// Returns 200 if the heartbeat was updated within the last 30 seconds,
// 503 otherwise.
func (h *Handler) HandleLivez(w http.ResponseWriter, r *http.Request) {
	if err := h.LivezCheck(); err != nil {
		h.logger.Warn("liveness check failed", "error", err)
		writeJSON(w, http.StatusServiceUnavailable, statusResponse{
			Status: "fail",
			Details: map[string]string{
				"heartbeat": err.Error(),
			},
		})
		return
	}

	writeJSON(w, http.StatusOK, statusResponse{Status: "ok"})
}

// HandleReadyz is the HTTP handler for the /readyz readiness endpoint.
// Returns 200 if: (a) API server is reachable, (b) informers have synced,
// (c) at least one detector is running. Returns 503 otherwise.
func (h *Handler) HandleReadyz(w http.ResponseWriter, r *http.Request) {
	if err := h.ReadyzCheck(); err != nil {
		h.logger.Warn("readiness check failed", "error", err)
		writeJSON(w, http.StatusServiceUnavailable, statusResponse{
			Status: "fail",
			Details: map[string]string{
				"reason": err.Error(),
			},
		})
		return
	}

	writeJSON(w, http.StatusOK, statusResponse{Status: "ok"})
}

// NewServeMux creates an http.ServeMux wired to the handler's endpoints.
func (h *Handler) NewServeMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", h.HandleLivez)
	mux.HandleFunc("/readyz", h.HandleReadyz)
	return mux
}

// Server wraps an *http.Server for the health probe endpoints.
type Server struct {
	httpServer *http.Server
	handler    *Handler
	logger     *slog.Logger
}

// NewServer creates a health probe HTTP server listening on the given port.
// The port must be in range [1, 65535].
func NewServer(handler *Handler, port int) (*Server, error) {
	if handler == nil {
		return nil, fmt.Errorf("health: handler must not be nil")
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("health: port %d out of valid range [1, 65535]", port)
	}

	mux := handler.NewServeMux()
	addr := fmt.Sprintf(":%d", port)

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	return &Server{
		httpServer: srv,
		handler:    handler,
		logger:     handler.logger,
	}, nil
}

// ListenAndServe starts the health probe server. It blocks until the server
// is shut down or encounters an unrecoverable error. Returns
// http.ErrServerClosed on clean shutdown.
func (s *Server) ListenAndServe() error {
	s.logger.Info("health probe server starting", "addr", s.httpServer.Addr)
	return s.httpServer.ListenAndServe()
}

// Serve starts the health probe server on the given listener. Useful for
// testing where the port is dynamically assigned.
func (s *Server) Serve(ln net.Listener) error {
	s.logger.Info("health probe server starting", "addr", ln.Addr().String())
	return s.httpServer.Serve(ln)
}

// Shutdown gracefully shuts down the health probe server, waiting for
// in-flight requests to complete or until the context is cancelled.
func (s *Server) Shutdown(ctx context.Context) error {
	s.logger.Info("health probe server shutting down")
	return s.httpServer.Shutdown(ctx)
}

// writeJSON writes a JSON response with the given status code.
func writeJSON(w http.ResponseWriter, code int, body statusResponse) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	// Encoding error is non-actionable here — the response is already
	// partially written. Log would require access to the logger, but for
	// a simple probe response this is acceptable.
	_ = json.NewEncoder(w).Encode(body)
}
