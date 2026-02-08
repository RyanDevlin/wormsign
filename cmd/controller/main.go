// Package main is the entrypoint for the Wormsign Kubernetes controller.
// It initializes configuration, sets up signal handling, starts the health
// probe server and metrics endpoint, and runs the fault detection pipeline.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/k8s-wormsign/k8s-wormsign/internal/health"
	"github.com/k8s-wormsign/k8s-wormsign/internal/metrics"
)

const (
	// metricsPort is the default port for the Prometheus metrics endpoint.
	metricsPort = 8080
	// healthPort is the default port for health probe endpoints.
	healthPort = 8081
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	logger.Info("starting wormsign controller", "version", "dev")

	if err := run(logger); err != nil {
		logger.Error("controller exited with error", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	// Build Kubernetes client configuration.
	restConfig, err := rest.InClusterConfig()
	if err != nil {
		return fmt.Errorf("building in-cluster config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("creating kubernetes clientset: %w", err)
	}

	// Initialize Prometheus metrics.
	metricsRegistry := prometheus.NewRegistry()
	metricsRegistry.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	metricsRegistry.MustRegister(prometheus.NewGoCollector())
	m := metrics.NewMetrics(metricsRegistry)

	// Initialize health handler.
	healthHandler := health.NewHandler(health.WithLogger(logger))

	// Start health probe server.
	healthSrv, err := health.NewServer(healthHandler, healthPort)
	if err != nil {
		return fmt.Errorf("creating health server: %w", err)
	}
	go func() {
		if serveErr := healthSrv.ListenAndServe(); serveErr != nil && serveErr != http.ErrServerClosed {
			logger.Error("health server failed", "error", serveErr)
		}
	}()

	// Start metrics server.
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", promhttp.HandlerFor(metricsRegistry, promhttp.HandlerOpts{}))
	metricsSrv := &http.Server{
		Addr:    fmt.Sprintf(":%d", metricsPort),
		Handler: metricsMux,
	}
	go func() {
		if serveErr := metricsSrv.ListenAndServe(); serveErr != nil && serveErr != http.ErrServerClosed {
			logger.Error("metrics server failed", "error", serveErr)
		}
	}()

	logger.Info("controller initialized, waiting for signal",
		"healthPort", healthPort,
		"metricsPort", metricsPort,
	)

	// Suppress unused variable warnings. These will be wired into the
	// pipeline stages in subsequent tasks.
	_ = clientset
	_ = m

	// Block until shutdown signal.
	<-ctx.Done()
	logger.Info("shutdown signal received, draining")

	// Graceful shutdown of HTTP servers.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	if err := healthSrv.Shutdown(shutdownCtx); err != nil {
		logger.Error("health server shutdown error", "error", err)
	}
	if err := metricsSrv.Shutdown(shutdownCtx); err != nil {
		logger.Error("metrics server shutdown error", "error", err)
	}

	logger.Info("controller stopped")
	return nil
}
