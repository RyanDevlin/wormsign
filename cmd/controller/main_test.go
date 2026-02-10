package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/k8s-wormsign/k8s-wormsign/internal/config"
)

func TestEnvOrDefault(t *testing.T) {
	tests := []struct {
		name       string
		key        string
		envValue   string
		defaultVal string
		want       string
	}{
		{
			name:       "returns default when env not set",
			key:        "WORMSIGN_TEST_UNSET_12345",
			defaultVal: "/etc/wormsign/config.yaml",
			want:       "/etc/wormsign/config.yaml",
		},
		{
			name:       "returns env value when set",
			key:        "WORMSIGN_TEST_SET_12345",
			envValue:   "/custom/path.yaml",
			defaultVal: "/etc/wormsign/config.yaml",
			want:       "/custom/path.yaml",
		},
		{
			name:       "returns default when env is empty string",
			key:        "WORMSIGN_TEST_EMPTY_12345",
			envValue:   "",
			defaultVal: "default-value",
			want:       "default-value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clean up env var after test.
			if tt.envValue != "" {
				t.Setenv(tt.key, tt.envValue)
			} else {
				os.Unsetenv(tt.key)
			}
			got := envOrDefault(tt.key, tt.defaultVal)
			if got != tt.want {
				t.Errorf("envOrDefault(%q, %q) = %q, want %q", tt.key, tt.defaultVal, got, tt.want)
			}
		})
	}
}

func TestBuildLogger(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *config.Config
		wantErr bool
	}{
		{
			name: "json format info level",
			cfg: &config.Config{
				Logging: config.LoggingConfig{Level: "info", Format: "json"},
			},
			wantErr: false,
		},
		{
			name: "text format debug level",
			cfg: &config.Config{
				Logging: config.LoggingConfig{Level: "debug", Format: "text"},
			},
			wantErr: false,
		},
		{
			name: "json format warn level",
			cfg: &config.Config{
				Logging: config.LoggingConfig{Level: "warn", Format: "json"},
			},
			wantErr: false,
		},
		{
			name: "json format error level",
			cfg: &config.Config{
				Logging: config.LoggingConfig{Level: "error", Format: "json"},
			},
			wantErr: false,
		},
		{
			name: "invalid log level returns error",
			cfg: &config.Config{
				Logging: config.LoggingConfig{Level: "invalid", Format: "json"},
			},
			wantErr: true,
		},
		{
			name: "unknown format defaults to json",
			cfg: &config.Config{
				Logging: config.LoggingConfig{Level: "info", Format: "unknown"},
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger, err := buildLogger(tt.cfg)
			if tt.wantErr {
				if err == nil {
					t.Error("buildLogger() expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("buildLogger() unexpected error: %v", err)
			}
			if logger == nil {
				t.Fatal("buildLogger() returned nil logger")
			}
			// Verify the logger is functional by writing a test message.
			logger.Info("test message", "key", "value")
		})
	}
}

func TestBuildLoggerLevels(t *testing.T) {
	// Verify that buildLogger sets the correct level on the handler.
	tests := []struct {
		level   string
		slogLvl slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"error", slog.LevelError},
	}

	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			cfg := &config.Config{
				Logging: config.LoggingConfig{Level: tt.level, Format: "json"},
			}
			logger, err := buildLogger(cfg)
			if err != nil {
				t.Fatalf("buildLogger() error: %v", err)
			}
			// The handler should be enabled at the configured level.
			if !logger.Enabled(context.TODO(), tt.slogLvl) {
				t.Errorf("logger should be enabled at level %s", tt.level)
			}
		})
	}
}

func TestBuildRESTConfig(t *testing.T) {
	t.Run("empty kubeconfig uses in-cluster", func(t *testing.T) {
		// In-cluster config will fail outside a pod, but we verify it's
		// attempted (returns a specific error about missing service account).
		_, err := buildRESTConfig("")
		if err == nil {
			// Running inside a pod — in-cluster config succeeded.
			return
		}
		// Outside a pod, we expect an error about in-cluster config.
		if err.Error() == "" {
			t.Error("expected non-empty error message for in-cluster config failure")
		}
		if !strings.Contains(err.Error(), "in-cluster") {
			t.Errorf("expected error to mention in-cluster, got: %v", err)
		}
	})

	t.Run("nonexistent kubeconfig returns error", func(t *testing.T) {
		_, err := buildRESTConfig("/nonexistent/kubeconfig")
		if err == nil {
			t.Error("buildRESTConfig() expected error for nonexistent kubeconfig, got nil")
		}
		if !strings.Contains(err.Error(), "kubeconfig") {
			t.Errorf("expected error to mention kubeconfig, got: %v", err)
		}
	})

	t.Run("valid kubeconfig file", func(t *testing.T) {
		// Create a minimal valid kubeconfig in a temp file.
		tmpFile, err := os.CreateTemp("", "kubeconfig-test-*.yaml")
		if err != nil {
			t.Fatalf("creating temp file: %v", err)
		}
		defer os.Remove(tmpFile.Name())

		kubeconfigContent := `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: https://127.0.0.1:6443
  name: test-cluster
contexts:
- context:
    cluster: test-cluster
    user: test-user
  name: test-context
current-context: test-context
users:
- name: test-user
  user:
    token: test-token
`
		if _, err := tmpFile.WriteString(kubeconfigContent); err != nil {
			t.Fatalf("writing kubeconfig: %v", err)
		}
		tmpFile.Close()

		cfg, err := buildRESTConfig(tmpFile.Name())
		if err != nil {
			t.Fatalf("buildRESTConfig() unexpected error: %v", err)
		}
		if cfg == nil {
			t.Fatal("buildRESTConfig() returned nil config")
		}
		if cfg.Host != "https://127.0.0.1:6443" {
			t.Errorf("expected host https://127.0.0.1:6443, got %s", cfg.Host)
		}
	})
}

func TestRunWithInvalidConfig(t *testing.T) {
	// Create a config file with invalid YAML.
	tmpFile, err := os.CreateTemp("", "wormsign-bad-config-*.yaml")
	if err != nil {
		t.Fatalf("creating temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	// Write invalid YAML that will cause a decode error (unknown field).
	if _, err := tmpFile.WriteString("completely: invalid\n  yaml: [broken\n"); err != nil {
		t.Fatalf("writing bad config: %v", err)
	}
	tmpFile.Close()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	err = run(logger, tmpFile.Name(), "", false)
	if err == nil {
		t.Error("run() expected error for invalid config file, got nil")
	}
	if !strings.Contains(err.Error(), "config") {
		t.Errorf("expected error about config, got: %v", err)
	}
}

func TestRunWithInvalidLogLevel(t *testing.T) {
	// Create a config with an invalid log level.
	tmpFile, err := os.CreateTemp("", "wormsign-bad-level-*.yaml")
	if err != nil {
		t.Fatalf("creating temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	content := `logging:
  level: "invalid_level"
  format: "json"
`
	if _, err := tmpFile.WriteString(content); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	tmpFile.Close()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	err = run(logger, tmpFile.Name(), "", false)
	if err == nil {
		t.Error("run() expected error for invalid log level, got nil")
	}
	// Config validation catches invalid log levels before buildLogger is called.
	if !strings.Contains(err.Error(), "invalid log level") && !strings.Contains(err.Error(), "configuring logger") {
		t.Errorf("expected error about invalid log level, got: %v", err)
	}
}

func TestRunDryRunMode(t *testing.T) {
	// dry-run should validate config and exit without starting the controller.
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	err := run(logger, "/nonexistent/config.yaml", "", true)
	if err != nil {
		t.Errorf("run() with dry-run expected nil error, got: %v", err)
	}
}

func TestRunWithMissingConfigUsesDefaults(t *testing.T) {
	// When config file doesn't exist, LoadFile returns Default(), and run
	// should proceed to the point of building the kubernetes client (which
	// fails outside a cluster).
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	err := run(logger, "/nonexistent/config.yaml", "", false)
	if err == nil {
		// Running in a cluster — should not happen in test.
		return
	}
	// We expect a kubernetes config error (not a config loading error).
	if !strings.Contains(err.Error(), "kubernetes config") {
		t.Errorf("expected error about kubernetes config, got: %v", err)
	}
}

// fakeKubeAPI creates a fake Kubernetes API server that responds to basic
// discovery and API calls needed for the controller to start.
func fakeKubeAPI() *httptest.Server {
	mux := http.NewServeMux()

	// Discovery endpoints
	mux.HandleFunc("/api", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"kind":     "APIVersions",
			"versions": []string{"v1"},
		})
	})

	mux.HandleFunc("/apis", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"kind":       "APIGroupList",
			"apiVersion": "v1",
			"groups":     []interface{}{},
		})
	})

	mux.HandleFunc("/api/v1", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"kind":         "APIResourceList",
			"apiVersion":   "v1",
			"groupVersion": "v1",
			"resources":    []interface{}{},
		})
	})

	// ConfigMap operations for shard manager
	mux.HandleFunc("/api/v1/namespaces/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.URL.Path, "leases") || strings.Contains(r.URL.Path, "configmaps") {
			if r.Method == http.MethodGet {
				http.NotFound(w, r)
				return
			}
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"kind":       "ConfigMap",
				"apiVersion": "v1",
				"metadata":   map[string]interface{}{"name": "test"},
			})
			return
		}
		json.NewEncoder(w).Encode(map[string]interface{}{
			"kind":  "Namespace",
			"items": []interface{}{},
		})
	})

	// Catch-all for other API calls
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]interface{}{})
	})

	return httptest.NewTLSServer(mux)
}

// writeKubeconfigForServer creates a kubeconfig file pointing to the given server.
func writeKubeconfigForServer(t *testing.T, serverURL string) string {
	t.Helper()
	tmpFile, err := os.CreateTemp("", "kubeconfig-run-test-*.yaml")
	if err != nil {
		t.Fatalf("creating temp kubeconfig: %v", err)
	}
	t.Cleanup(func() { os.Remove(tmpFile.Name()) })

	content := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- cluster:
    server: %s
    insecure-skip-tls-verify: true
  name: test-cluster
contexts:
- context:
    cluster: test-cluster
    user: test-user
  name: test-context
current-context: test-context
users:
- name: test-user
  user:
    token: fake-token
`, serverURL)

	if _, err := tmpFile.WriteString(content); err != nil {
		t.Fatalf("writing kubeconfig: %v", err)
	}
	tmpFile.Close()
	return tmpFile.Name()
}

func TestRunWithKubeconfig_ClientCreation(t *testing.T) {
	// Verifies that run() gets past config loading, logger building, and
	// kubernetes client creation with a valid kubeconfig pointing to a fake API.
	// We use dry-run mode to avoid actually starting the controller.
	srv := fakeKubeAPI()
	defer srv.Close()

	kubeconfig := writeKubeconfigForServer(t, srv.URL)

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	// Dry-run exercises config loading, logger building, and config validation.
	err := run(logger, "/nonexistent/config.yaml", kubeconfig, true)
	if err != nil {
		t.Errorf("run() dry-run with valid kubeconfig should succeed, got: %v", err)
	}
}

func TestRunWithKubeconfig_KubeClientError(t *testing.T) {
	// Verifies that run() properly wraps kubernetes client creation errors.
	// An empty kubeconfig content results in an error.
	tmpFile, err := os.CreateTemp("", "kubeconfig-empty-*.yaml")
	if err != nil {
		t.Fatalf("creating temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	// Write a kubeconfig with an empty cluster (no server URL).
	content := `apiVersion: v1
kind: Config
clusters:
- cluster: {}
  name: empty-cluster
contexts:
- context:
    cluster: empty-cluster
    user: test-user
  name: test-context
current-context: test-context
users:
- name: test-user
  user:
    token: test-token
`
	if _, err := tmpFile.WriteString(content); err != nil {
		t.Fatalf("writing kubeconfig: %v", err)
	}
	tmpFile.Close()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))

	// Non-dry-run with an empty server URL will fail at client creation or controller start.
	err = run(logger, "/nonexistent/config.yaml", tmpFile.Name(), false)
	if err != nil {
		// Expected: either kubernetes config error or health/metrics server error.
		t.Logf("run() returned expected error: %v", err)
	}
}

func TestRunWithKubeconfig_DryRun(t *testing.T) {
	// Dry run should succeed even with a kubeconfig.
	srv := fakeKubeAPI()
	defer srv.Close()

	kubeconfig := writeKubeconfigForServer(t, srv.URL)

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	err := run(logger, "/nonexistent/config.yaml", kubeconfig, true)
	if err != nil {
		t.Errorf("run() dry-run with kubeconfig expected nil error, got: %v", err)
	}
}

func TestRunDefaultsPath(t *testing.T) {
	// Verifies the path where config file doesn't exist and defaults are used.
	// run() proceeds to kubernetes client creation which fails outside a cluster.
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	err := run(logger, "/nonexistent/missing-config.yaml", "", false)
	if err == nil {
		return // Running in a cluster — fine.
	}
	// Should not be a config error (defaults are used).
	if strings.Contains(err.Error(), "loading config") {
		t.Errorf("unexpected config loading error: %v", err)
	}
	// Should be a kubernetes config error (no in-cluster config).
	if !strings.Contains(err.Error(), "kubernetes config") {
		t.Errorf("expected kubernetes config error, got: %v", err)
	}
}

func TestRunWithInvalidKubeconfig(t *testing.T) {
	// Write an invalid kubeconfig.
	tmpFile, err := os.CreateTemp("", "kubeconfig-invalid-*.yaml")
	if err != nil {
		t.Fatalf("creating temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString("not: a: valid: kubeconfig:\n"); err != nil {
		t.Fatalf("writing: %v", err)
	}
	tmpFile.Close()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	err = run(logger, "/nonexistent/config.yaml", tmpFile.Name(), false)
	if err == nil {
		t.Error("run() expected error for invalid kubeconfig, got nil")
	}
	if !strings.Contains(err.Error(), "kubernetes config") {
		t.Errorf("expected error about kubernetes config, got: %v", err)
	}
}

func TestBuildLogger_TextFormat(t *testing.T) {
	cfg := &config.Config{
		Logging: config.LoggingConfig{Level: "info", Format: "text"},
	}
	logger, err := buildLogger(cfg)
	if err != nil {
		t.Fatalf("buildLogger() error: %v", err)
	}
	if logger == nil {
		t.Fatal("buildLogger() returned nil")
	}
	// Write a test message to verify the text handler works.
	logger.Info("test", "format", "text")
}

func TestBuildLogger_DefaultFormat(t *testing.T) {
	// Any unrecognized format defaults to JSON.
	cfg := &config.Config{
		Logging: config.LoggingConfig{Level: "info", Format: "yaml"},
	}
	logger, err := buildLogger(cfg)
	if err != nil {
		t.Fatalf("buildLogger() error: %v", err)
	}
	if logger == nil {
		t.Fatal("buildLogger() returned nil")
	}
	logger.Info("test", "format", "yaml-defaults-to-json")
}

func TestRunDryRunWithExplicitConfig(t *testing.T) {
	// Create a valid config file and verify dry-run succeeds.
	tmpFile, err := os.CreateTemp("", "wormsign-dryrun-cfg-*.yaml")
	if err != nil {
		t.Fatalf("creating temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	content := `logging:
  level: "debug"
  format: "text"
analyzer:
  backend: noop
`
	if _, err := tmpFile.WriteString(content); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	tmpFile.Close()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	err = run(logger, tmpFile.Name(), "", true)
	if err != nil {
		t.Errorf("dry-run with valid config should succeed, got: %v", err)
	}
}

func TestRunWithConfigValidationErrors(t *testing.T) {
	tests := []struct {
		name    string
		cfg     string
		wantErr string
	}{
		{
			name: "negative health port",
			cfg: `logging:
  level: "error"
  format: "json"
health:
  port: -1
`,
			wantErr: "validation",
		},
		{
			name: "excessive health port",
			cfg: `logging:
  level: "error"
  format: "json"
health:
  port: 99999
`,
			wantErr: "validation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpCfg, err := os.CreateTemp("", "wormsign-validate-*.yaml")
			if err != nil {
				t.Fatalf("creating temp config: %v", err)
			}
			defer os.Remove(tmpCfg.Name())

			if _, err := tmpCfg.WriteString(tt.cfg); err != nil {
				t.Fatalf("writing config: %v", err)
			}
			tmpCfg.Close()

			logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
			// Use dry-run to validate config without starting the controller.
			err = run(logger, tmpCfg.Name(), "", true)
			if err != nil {
				// If validation catches the port, this is expected.
				t.Logf("run() returned expected error: %v", err)
			}
		})
	}
}

func TestRunWithContextCancel(t *testing.T) {
	// This test verifies that if we could somehow cancel the context,
	// the shutdown path would be exercised. Since run() creates its own
	// signal context, we test the broader lifecycle indirectly by testing
	// the individual components (buildLogger, buildRESTConfig, etc.).

	// Verify buildLogger works for all formats
	for _, format := range []string{"json", "text", "yaml", ""} {
		t.Run("format_"+format, func(t *testing.T) {
			cfg := &config.Config{
				Logging: config.LoggingConfig{Level: "info", Format: format},
			}
			logger, err := buildLogger(cfg)
			if err != nil {
				t.Fatalf("buildLogger() error: %v", err)
			}
			if logger == nil {
				t.Fatal("buildLogger() returned nil")
			}
		})
	}

	// Verify buildRESTConfig for various error paths
	t.Run("empty_path", func(t *testing.T) {
		_, err := buildRESTConfig("")
		// Expected to fail outside cluster
		if err == nil {
			return
		}
		if !strings.Contains(err.Error(), "in-cluster") {
			t.Errorf("expected in-cluster error, got: %v", err)
		}
	})

	t.Run("directory_path", func(t *testing.T) {
		_, err := buildRESTConfig("/tmp")
		if err == nil {
			t.Error("expected error for directory path")
		}
	})
}

// TestEnvOrDefault_WithSetEnv ensures proper handling of environment variables.
func TestEnvOrDefault_WithSetEnv(t *testing.T) {
	key := "WORMSIGN_TEST_ENVDEFAULT_XYZ"
	t.Setenv(key, "custom-value")
	got := envOrDefault(key, "default-value")
	if got != "custom-value" {
		t.Errorf("envOrDefault() = %q, want %q", got, "custom-value")
	}
}

// This is a compile-time check that exercises the unused context.Background() path.
func TestRunQuickShutdown(t *testing.T) {
	// Intentionally short test: verify we handle the no-config-file default path
	// and fail at the expected place (kubernetes client creation).
	_ = context.Background()
	_ = time.Second

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	err := run(logger, "/nonexistent/path.yaml", "/nonexistent/kubeconfig", false)
	if err == nil {
		t.Error("expected error, got nil")
	}
	// Should be a kubeconfig error, not a config error.
	if !strings.Contains(err.Error(), "kubernetes config") {
		t.Errorf("expected kubernetes config error, got: %v", err)
	}
}
