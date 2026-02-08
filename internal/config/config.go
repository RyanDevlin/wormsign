// Package config defines the configuration struct for the Wormsign controller.
// Configuration is loaded from a YAML file mounted via ConfigMap and mirrors
// the Helm values.yaml structure (see DECISIONS.md D11).
package config

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// DefaultConfigPath is the default filesystem path for the controller config
// file, typically mounted via ConfigMap.
const DefaultConfigPath = "/etc/wormsign/config.yaml"

// Config is the top-level configuration for the Wormsign controller,
// corresponding to the Helm values.yaml structure.
type Config struct {
	// ReplicaCount is the number of controller replicas.
	ReplicaCount int `yaml:"replicaCount"`

	// Logging configures structured log output.
	Logging LoggingConfig `yaml:"logging"`

	// Detectors configures built-in fault detectors.
	Detectors DetectorsConfig `yaml:"detectors"`

	// Correlation configures the pre-LLM event correlation engine.
	Correlation CorrelationConfig `yaml:"correlation"`

	// Analyzer configures the LLM analysis backend.
	Analyzer AnalyzerConfig `yaml:"analyzer"`

	// Pipeline configures worker concurrency per stage.
	Pipeline PipelineConfig `yaml:"pipeline"`
}

// LoggingConfig controls the logging behavior.
type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// DetectorsConfig holds per-detector configuration.
type DetectorsConfig struct {
	PodCrashLoop DetectorConfig `yaml:"podCrashLoop"`
	PodFailed    DetectorConfig `yaml:"podFailed"`
	NodeNotReady DetectorConfig `yaml:"nodeNotReady"`
}

// DetectorConfig holds common detector settings.
type DetectorConfig struct {
	Enabled  bool          `yaml:"enabled"`
	Cooldown time.Duration `yaml:"cooldown"`
}

// CorrelationConfig configures the event correlation engine.
type CorrelationConfig struct {
	Enabled        bool          `yaml:"enabled"`
	WindowDuration time.Duration `yaml:"windowDuration"`
}

// AnalyzerConfig configures the LLM backend.
type AnalyzerConfig struct {
	Backend string `yaml:"backend"`
}

// PipelineConfig configures worker counts per pipeline stage.
type PipelineConfig struct {
	Workers WorkersConfig `yaml:"workers"`
}

// WorkersConfig specifies concurrency per pipeline stage.
type WorkersConfig struct {
	Gathering int `yaml:"gathering"`
	Analysis  int `yaml:"analysis"`
	Sink      int `yaml:"sink"`
}

// Load reads YAML configuration from r and returns a parsed Config.
func Load(r io.Reader) (*Config, error) {
	var cfg Config
	decoder := yaml.NewDecoder(r)
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decoding config: %w", err)
	}
	return &cfg, nil
}

// LoadFile reads and parses the YAML configuration from the given path.
// If the file does not exist, it returns Default() without error.
func LoadFile(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Default(), nil
		}
		return nil, fmt.Errorf("opening config file %s: %w", path, err)
	}
	defer f.Close()

	cfg, err := Load(f)
	if err != nil {
		return nil, fmt.Errorf("loading config from %s: %w", path, err)
	}
	return cfg, nil
}

// Default returns a Config populated with production defaults matching
// the Helm values.yaml reference in the project specification.
func Default() *Config {
	return &Config{
		ReplicaCount: 1,
		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
		},
		Detectors: DetectorsConfig{
			PodCrashLoop: DetectorConfig{Enabled: true, Cooldown: 30 * time.Minute},
			PodFailed:    DetectorConfig{Enabled: true, Cooldown: 30 * time.Minute},
			NodeNotReady: DetectorConfig{Enabled: true, Cooldown: 30 * time.Minute},
		},
		Correlation: CorrelationConfig{
			Enabled:        true,
			WindowDuration: 2 * time.Minute,
		},
		Analyzer: AnalyzerConfig{
			Backend: "noop",
		},
		Pipeline: PipelineConfig{
			Workers: WorkersConfig{
				Gathering: 5,
				Analysis:  2,
				Sink:      3,
			},
		},
	}
}

// ApplyDefaults fills in zero-valued fields with production defaults.
func (c *Config) ApplyDefaults() {
	d := Default()
	if c.Logging.Level == "" {
		c.Logging.Level = d.Logging.Level
	}
	if c.Logging.Format == "" {
		c.Logging.Format = d.Logging.Format
	}
	if c.Pipeline.Workers.Gathering == 0 {
		c.Pipeline.Workers.Gathering = d.Pipeline.Workers.Gathering
	}
	if c.Pipeline.Workers.Analysis == 0 {
		c.Pipeline.Workers.Analysis = d.Pipeline.Workers.Analysis
	}
	if c.Pipeline.Workers.Sink == 0 {
		c.Pipeline.Workers.Sink = d.Pipeline.Workers.Sink
	}
	if c.Analyzer.Backend == "" {
		c.Analyzer.Backend = d.Analyzer.Backend
	}
}

// validLogLevels is the set of accepted log level strings.
var validLogLevels = map[string]slog.Level{
	"debug": slog.LevelDebug,
	"info":  slog.LevelInfo,
	"warn":  slog.LevelWarn,
	"error": slog.LevelError,
}

// ParseLogLevel converts the configured log level string to a slog.Level.
// Returns an error for unrecognised values.
func ParseLogLevel(s string) (slog.Level, error) {
	level, ok := validLogLevels[strings.ToLower(s)]
	if !ok {
		return slog.LevelInfo, fmt.Errorf("invalid log level %q: must be one of debug, info, warn, error", s)
	}
	return level, nil
}

// Validate checks the config for invalid or contradictory settings.
// It should be called after ApplyDefaults.
func (c *Config) Validate() error {
	if _, err := ParseLogLevel(c.Logging.Level); err != nil {
		return err
	}
	format := strings.ToLower(c.Logging.Format)
	if format != "json" && format != "text" {
		return fmt.Errorf("invalid log format %q: must be json or text", c.Logging.Format)
	}
	if c.Pipeline.Workers.Gathering < 1 {
		return fmt.Errorf("pipeline.workers.gathering must be >= 1, got %d", c.Pipeline.Workers.Gathering)
	}
	if c.Pipeline.Workers.Analysis < 1 {
		return fmt.Errorf("pipeline.workers.analysis must be >= 1, got %d", c.Pipeline.Workers.Analysis)
	}
	if c.Pipeline.Workers.Sink < 1 {
		return fmt.Errorf("pipeline.workers.sink must be >= 1, got %d", c.Pipeline.Workers.Sink)
	}
	return nil
}
