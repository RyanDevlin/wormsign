// Package main is the entrypoint for the Wormsign Kubernetes controller.
// It initializes configuration, sets up signal handling, starts the health
// probe server and metrics endpoint, and runs the fault detection pipeline.
package main

import (
	"context"
	"flag"
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
	"k8s.io/client-go/tools/clientcmd"

	"github.com/k8s-wormsign/k8s-wormsign/internal/config"
	"github.com/k8s-wormsign/k8s-wormsign/internal/controller"
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
	// Parse flags early so --help works before config loading.
	configPath := flag.String("config", envOrDefault("WORMSIGN_CONFIG", config.DefaultConfigPath),
		"path to the controller configuration YAML file")
	kubeconfig := flag.String("kubeconfig", os.Getenv("KUBECONFIG"),
		"path to kubeconfig file (for out-of-cluster development)")
	flag.Parse()

	// Bootstrap logger (JSON/info) — will be reconfigured after config load.
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	logger.Info("starting wormsign controller", "version", "dev")

	if err := run(logger, *configPath, *kubeconfig); err != nil {
		logger.Error("controller exited with error", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger, configPath, kubeconfig string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	// --- Load and validate configuration ---
	logger.Info("loading configuration", "path", configPath)
	cfg, err := config.LoadFile(configPath)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}
	cfg.ApplyDefaults()

	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	// --- Reconfigure logger based on loaded config ---
	logger, err = buildLogger(cfg)
	if err != nil {
		return fmt.Errorf("configuring logger: %w", err)
	}
	slog.SetDefault(logger)
	logger.Info("configuration loaded",
		"logLevel", cfg.Logging.Level,
		"logFormat", cfg.Logging.Format,
		"analyzerBackend", cfg.Analyzer.Backend,
	)

	// --- Build Kubernetes client ---
	restConfig, err := buildRESTConfig(kubeconfig)
	if err != nil {
		return fmt.Errorf("building kubernetes config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return fmt.Errorf("creating kubernetes clientset: %w", err)
	}

	// --- Initialize Prometheus metrics ---
	metricsRegistry := prometheus.NewRegistry()
	metricsRegistry.MustRegister(prometheus.NewProcessCollector(prometheus.ProcessCollectorOpts{}))
	metricsRegistry.MustRegister(prometheus.NewGoCollector())
	m := metrics.NewMetrics(metricsRegistry)

	// --- Initialize health handler ---
	healthHandler := health.NewHandler(health.WithLogger(logger))

	// --- Start health probe server ---
	healthSrv, err := health.NewServer(healthHandler, healthPort)
	if err != nil {
		return fmt.Errorf("creating health server: %w", err)
	}
	go func() {
		if serveErr := healthSrv.ListenAndServe(); serveErr != nil && serveErr != http.ErrServerClosed {
			logger.Error("health server failed", "error", serveErr)
		}
	}()

	// --- Start metrics server ---
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", promhttp.HandlerFor(metricsRegistry, promhttp.HandlerOpts{}))
	metricsSrv := &http.Server{
		Addr:              fmt.Sprintf(":%d", metricsPort),
		Handler:           metricsMux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if serveErr := metricsSrv.ListenAndServe(); serveErr != nil && serveErr != http.ErrServerClosed {
			logger.Error("metrics server failed", "error", serveErr)
		}
	}()

	logger.Info("servers started",
		"healthPort", healthPort,
		"metricsPort", metricsPort,
	)

	// --- Instantiate and run the controller ---
	ctrl, err := controller.New(controller.Options{
		Logger:        logger,
		Config:        cfg,
		Clientset:     clientset,
		HealthHandler: healthHandler,
		Metrics:       m,
	})
	if err != nil {
		return fmt.Errorf("creating controller: %w", err)
	}

	// Run the controller; it blocks until ctx is cancelled (signal received).
	if err := ctrl.Run(ctx); err != nil {
		logger.Error("controller run error", "error", err)
	}

	logger.Info("shutdown signal received, draining")

	// --- Graceful shutdown of HTTP servers ---
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

// buildLogger creates a slog.Logger from the loaded configuration.
func buildLogger(cfg *config.Config) (*slog.Logger, error) {
	level, err := config.ParseLogLevel(cfg.Logging.Level)
	if err != nil {
		return nil, err
	}
	opts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	switch cfg.Logging.Format {
	case "text":
		handler = slog.NewTextHandler(os.Stdout, opts)
	default:
		handler = slog.NewJSONHandler(os.Stdout, opts)
	}
	return slog.New(handler), nil
}

// buildRESTConfig builds a *rest.Config. If kubeconfig is non-empty, it loads
// from that file (for out-of-cluster development). Otherwise, it uses
// in-cluster config.
func buildRESTConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig != "" {
		cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
		if err != nil {
			return nil, fmt.Errorf("loading kubeconfig from %s: %w", kubeconfig, err)
		}
		return cfg, nil
	}
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("building in-cluster config: %w", err)
	}
	return cfg, nil
}

// envOrDefault returns the environment variable value if set, otherwise the
// default value.
func envOrDefault(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}
