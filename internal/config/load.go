package config

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Load reads YAML configuration from r and returns a parsed Config.
// Unknown fields in the YAML are rejected to catch typos early.
func Load(r io.Reader) (*Config, error) {
	var cfg Config
	decoder := yaml.NewDecoder(r)
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("decoding config: %w", err)
	}
	return &cfg, nil
}

// LoadFromFile reads and parses the YAML configuration from the given path.
// If the file does not exist, it returns Default() without error, allowing
// the controller to start with default values when no config file is mounted.
func LoadFromFile(path string) (*Config, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			slog.Info("config file not found, using defaults", "path", path)
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

// LoadFile is an alias for LoadFromFile, retained for backward compatibility
// with existing callers (e.g., cmd/controller/main.go).
func LoadFile(path string) (*Config, error) {
	return LoadFromFile(path)
}

// LoadAndValidate loads configuration from the given path, applies defaults
// for any unset fields, and validates the result. This is the recommended
// entrypoint for controller startup per Section 2.5: if the configuration
// is invalid, the returned error should cause a crash with a clear message.
func LoadAndValidate(path string) (*Config, error) {
	cfg, err := LoadFromFile(path)
	if err != nil {
		return nil, err
	}
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}
	return cfg, nil
}

// validLogLevels is the set of accepted log level strings.
var validLogLevels = map[string]slog.Level{
	"debug": slog.LevelDebug,
	"info":  slog.LevelInfo,
	"warn":  slog.LevelWarn,
	"error": slog.LevelError,
}

// ParseLogLevel converts the configured log level string to a slog.Level.
// Returns an error for unrecognized values.
func ParseLogLevel(s string) (slog.Level, error) {
	level, ok := validLogLevels[strings.ToLower(s)]
	if !ok {
		return slog.LevelInfo, fmt.Errorf("invalid log level %q: must be one of debug, info, warn, error", s)
	}
	return level, nil
}
