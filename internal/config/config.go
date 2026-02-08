// Package config defines the configuration struct for the Wormsign controller.
// Configuration is loaded from a YAML file mounted via ConfigMap and mirrors
// the Helm values.yaml structure (see DECISIONS.md D11).
package config

import (
	"fmt"
	"io"
	"time"

	"gopkg.in/yaml.v3"
)

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
