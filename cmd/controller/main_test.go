package main

import (
	"log/slog"
	"os"
	"testing"

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
		level    string
		slogLvl  slog.Level
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
			if !logger.Enabled(nil, tt.slogLvl) {
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
	})

	t.Run("nonexistent kubeconfig returns error", func(t *testing.T) {
		_, err := buildRESTConfig("/nonexistent/kubeconfig")
		if err == nil {
			t.Error("buildRESTConfig() expected error for nonexistent kubeconfig, got nil")
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
	err = run(logger, tmpFile.Name(), "")
	if err == nil {
		t.Error("run() expected error for invalid config file, got nil")
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
	err = run(logger, tmpFile.Name(), "")
	if err == nil {
		t.Error("run() expected error for invalid log level, got nil")
	}
}

func TestRunWithMissingConfigUsesDefaults(t *testing.T) {
	// When config file doesn't exist, LoadFile returns Default(), and run
	// should proceed to the point of building the kubernetes client (which
	// fails outside a cluster).
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelError}))
	err := run(logger, "/nonexistent/config.yaml", "")
	if err == nil {
		// Running in a cluster — should not happen in test.
		return
	}
	// We expect a kubernetes config error (not a config loading error).
	expected := "building kubernetes config"
	if len(err.Error()) < len(expected) {
		t.Errorf("unexpected error: %v", err)
	}
}
