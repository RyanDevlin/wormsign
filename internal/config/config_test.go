package config

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ---- Default() tests ----

func TestDefault_ReturnsValidConfig(t *testing.T) {
	cfg := Default()
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Default() config should be valid, got: %v", err)
	}
}

func TestDefault_ReplicaCount(t *testing.T) {
	cfg := Default()
	if cfg.ReplicaCount != 1 {
		t.Errorf("expected ReplicaCount=1, got %d", cfg.ReplicaCount)
	}
}

func TestDefault_LeaderElection(t *testing.T) {
	cfg := Default()
	if !cfg.LeaderElection.Enabled {
		t.Error("expected leader election enabled by default")
	}
	if cfg.LeaderElection.LeaseDuration != 15*time.Second {
		t.Errorf("expected LeaseDuration=15s, got %s", cfg.LeaderElection.LeaseDuration)
	}
	if cfg.LeaderElection.RenewDeadline != 10*time.Second {
		t.Errorf("expected RenewDeadline=10s, got %s", cfg.LeaderElection.RenewDeadline)
	}
	if cfg.LeaderElection.RetryPeriod != 2*time.Second {
		t.Errorf("expected RetryPeriod=2s, got %s", cfg.LeaderElection.RetryPeriod)
	}
}

func TestDefault_Logging(t *testing.T) {
	cfg := Default()
	if cfg.Logging.Level != "info" {
		t.Errorf("expected log level 'info', got %q", cfg.Logging.Level)
	}
	if cfg.Logging.Format != "json" {
		t.Errorf("expected log format 'json', got %q", cfg.Logging.Format)
	}
}

func TestDefault_Filters(t *testing.T) {
	cfg := Default()
	expected := []string{"kube-system", "kube-public", "kube-node-lease"}
	if len(cfg.Filters.ExcludeNamespaces) != len(expected) {
		t.Fatalf("expected %d exclude namespaces, got %d", len(expected), len(cfg.Filters.ExcludeNamespaces))
	}
	for i, ns := range expected {
		if cfg.Filters.ExcludeNamespaces[i] != ns {
			t.Errorf("expected excludeNamespaces[%d]=%q, got %q", i, ns, cfg.Filters.ExcludeNamespaces[i])
		}
	}
}

func TestDefault_Detectors(t *testing.T) {
	cfg := Default()
	// Enabled by default
	if !cfg.Detectors.PodStuckPending.Enabled {
		t.Error("PodStuckPending should be enabled")
	}
	if cfg.Detectors.PodStuckPending.Threshold != 15*time.Minute {
		t.Errorf("PodStuckPending threshold: got %s, want 15m", cfg.Detectors.PodStuckPending.Threshold)
	}
	if !cfg.Detectors.PodCrashLoop.Enabled {
		t.Error("PodCrashLoop should be enabled")
	}
	if cfg.Detectors.PodCrashLoop.Threshold != 3 {
		t.Errorf("PodCrashLoop threshold: got %d, want 3", cfg.Detectors.PodCrashLoop.Threshold)
	}
	if !cfg.Detectors.PodFailed.Enabled {
		t.Error("PodFailed should be enabled")
	}
	if len(cfg.Detectors.PodFailed.IgnoreExitCodes) != 1 || cfg.Detectors.PodFailed.IgnoreExitCodes[0] != 0 {
		t.Errorf("PodFailed ignoreExitCodes: got %v, want [0]", cfg.Detectors.PodFailed.IgnoreExitCodes)
	}
	if !cfg.Detectors.NodeNotReady.Enabled {
		t.Error("NodeNotReady should be enabled")
	}
	if cfg.Detectors.NodeNotReady.Threshold != 5*time.Minute {
		t.Errorf("NodeNotReady threshold: got %s, want 5m", cfg.Detectors.NodeNotReady.Threshold)
	}

	// Disabled by default
	if cfg.Detectors.PVCStuckBinding.Enabled {
		t.Error("PVCStuckBinding should be disabled")
	}
	if cfg.Detectors.HighPodCount.Enabled {
		t.Error("HighPodCount should be disabled")
	}
	if cfg.Detectors.JobDeadlineExceeded.Enabled {
		t.Error("JobDeadlineExceeded should be disabled")
	}
}

func TestDefault_Correlation(t *testing.T) {
	cfg := Default()
	if !cfg.Correlation.Enabled {
		t.Error("correlation should be enabled by default")
	}
	if cfg.Correlation.WindowDuration != 2*time.Minute {
		t.Errorf("windowDuration: got %s, want 2m", cfg.Correlation.WindowDuration)
	}
	if !cfg.Correlation.Rules.NodeCascade.Enabled {
		t.Error("NodeCascade rule should be enabled")
	}
	if cfg.Correlation.Rules.NodeCascade.MinPodFailures != 2 {
		t.Errorf("NodeCascade minPodFailures: got %d, want 2", cfg.Correlation.Rules.NodeCascade.MinPodFailures)
	}
	if cfg.Correlation.Rules.NamespaceStorm.Threshold != 20 {
		t.Errorf("NamespaceStorm threshold: got %d, want 20", cfg.Correlation.Rules.NamespaceStorm.Threshold)
	}
}

func TestDefault_Gatherers(t *testing.T) {
	cfg := Default()
	if !cfg.Gatherers.PodLogs.Enabled {
		t.Error("PodLogs gatherer should be enabled")
	}
	if cfg.Gatherers.PodLogs.TailLines != 100 {
		t.Errorf("PodLogs tailLines: got %d, want 100", cfg.Gatherers.PodLogs.TailLines)
	}
	if !cfg.Gatherers.PodLogs.IncludePrevious {
		t.Error("PodLogs includePrevious should be true")
	}
	if cfg.Gatherers.SuperEvent.MaxAffectedResources != 5 {
		t.Errorf("SuperEvent maxAffectedResources: got %d, want 5", cfg.Gatherers.SuperEvent.MaxAffectedResources)
	}
}

func TestDefault_Analyzer(t *testing.T) {
	cfg := Default()
	if cfg.Analyzer.Backend != "claude" {
		t.Errorf("analyzer backend: got %q, want 'claude'", cfg.Analyzer.Backend)
	}
	if cfg.Analyzer.RateLimiting.DailyTokenBudget != 1_000_000 {
		t.Errorf("daily token budget: got %d, want 1000000", cfg.Analyzer.RateLimiting.DailyTokenBudget)
	}
	if cfg.Analyzer.CircuitBreaker.ConsecutiveFailures != 5 {
		t.Errorf("circuit breaker consecutive failures: got %d, want 5", cfg.Analyzer.CircuitBreaker.ConsecutiveFailures)
	}
	if cfg.Analyzer.CircuitBreaker.OpenDuration != 10*time.Minute {
		t.Errorf("circuit breaker open duration: got %s, want 10m", cfg.Analyzer.CircuitBreaker.OpenDuration)
	}
}

func TestDefault_Sinks(t *testing.T) {
	cfg := Default()
	if cfg.Sinks.Slack.Enabled {
		t.Error("Slack sink should be disabled by default")
	}
	if cfg.Sinks.PagerDuty.Enabled {
		t.Error("PagerDuty sink should be disabled by default")
	}
	if cfg.Sinks.S3.Enabled {
		t.Error("S3 sink should be disabled by default")
	}
	if cfg.Sinks.Webhook.Enabled {
		t.Error("Webhook sink should be disabled by default")
	}
	if !cfg.Sinks.KubernetesEvent.Enabled {
		t.Error("KubernetesEvent sink should be enabled by default")
	}
}

func TestDefault_Pipeline(t *testing.T) {
	cfg := Default()
	if cfg.Pipeline.Workers.Gathering != 5 {
		t.Errorf("gathering workers: got %d, want 5", cfg.Pipeline.Workers.Gathering)
	}
	if cfg.Pipeline.Workers.Analysis != 2 {
		t.Errorf("analysis workers: got %d, want 2", cfg.Pipeline.Workers.Analysis)
	}
	if cfg.Pipeline.Workers.Sink != 3 {
		t.Errorf("sink workers: got %d, want 3", cfg.Pipeline.Workers.Sink)
	}
}

func TestDefault_MetricsAndHealth(t *testing.T) {
	cfg := Default()
	if !cfg.Metrics.Enabled {
		t.Error("metrics should be enabled by default")
	}
	if cfg.Metrics.Port != 8080 {
		t.Errorf("metrics port: got %d, want 8080", cfg.Metrics.Port)
	}
	if cfg.Health.Port != 8081 {
		t.Errorf("health port: got %d, want 8081", cfg.Health.Port)
	}
}

// ---- ApplyDefaults() tests ----

func TestApplyDefaults_FillsZeroValues(t *testing.T) {
	cfg := &Config{}
	cfg.ApplyDefaults()

	if cfg.ReplicaCount != 1 {
		t.Errorf("expected ReplicaCount=1 after defaults, got %d", cfg.ReplicaCount)
	}
	if cfg.Logging.Level != "info" {
		t.Errorf("expected log level 'info' after defaults, got %q", cfg.Logging.Level)
	}
	if cfg.Pipeline.Workers.Gathering != 5 {
		t.Errorf("expected gathering workers=5 after defaults, got %d", cfg.Pipeline.Workers.Gathering)
	}
	if cfg.Analyzer.Backend != "claude" {
		t.Errorf("expected analyzer backend 'claude' after defaults, got %q", cfg.Analyzer.Backend)
	}
}

func TestApplyDefaults_PreservesExplicitValues(t *testing.T) {
	cfg := &Config{
		ReplicaCount: 3,
		Logging: LoggingConfig{
			Level:  "debug",
			Format: "text",
		},
		Analyzer: AnalyzerConfig{
			Backend: "noop",
		},
		Pipeline: PipelineConfig{
			Workers: WorkersConfig{
				Gathering: 10,
				Analysis:  4,
				Sink:      6,
			},
		},
	}
	cfg.ApplyDefaults()

	if cfg.ReplicaCount != 3 {
		t.Errorf("expected ReplicaCount=3 (preserved), got %d", cfg.ReplicaCount)
	}
	if cfg.Logging.Level != "debug" {
		t.Errorf("expected log level 'debug' (preserved), got %q", cfg.Logging.Level)
	}
	if cfg.Logging.Format != "text" {
		t.Errorf("expected log format 'text' (preserved), got %q", cfg.Logging.Format)
	}
	if cfg.Analyzer.Backend != "noop" {
		t.Errorf("expected analyzer backend 'noop' (preserved), got %q", cfg.Analyzer.Backend)
	}
	if cfg.Pipeline.Workers.Gathering != 10 {
		t.Errorf("expected gathering workers=10 (preserved), got %d", cfg.Pipeline.Workers.Gathering)
	}
}

func TestApplyDefaults_FiltersNilGetsDefaults(t *testing.T) {
	cfg := &Config{}
	cfg.ApplyDefaults()
	if len(cfg.Filters.ExcludeNamespaces) != 3 {
		t.Errorf("expected 3 default exclude namespaces, got %d", len(cfg.Filters.ExcludeNamespaces))
	}
}

func TestApplyDefaults_FiltersEmptySlicePreserved(t *testing.T) {
	cfg := &Config{
		Filters: FiltersConfig{
			ExcludeNamespaces: []string{},
		},
	}
	cfg.ApplyDefaults()
	// An explicitly empty slice should be preserved (user wants no exclusions).
	if len(cfg.Filters.ExcludeNamespaces) != 0 {
		t.Errorf("expected empty exclude namespaces (preserved), got %d", len(cfg.Filters.ExcludeNamespaces))
	}
}

// ---- Load() / LoadFromFile() tests ----

func TestLoad_ValidYAML(t *testing.T) {
	yaml := `
replicaCount: 2
logging:
  level: debug
  format: text
analyzer:
  backend: noop
pipeline:
  workers:
    gathering: 8
    analysis: 4
    sink: 6
`
	cfg, err := Load(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("Load() returned error: %v", err)
	}
	if cfg.ReplicaCount != 2 {
		t.Errorf("expected ReplicaCount=2, got %d", cfg.ReplicaCount)
	}
	if cfg.Logging.Level != "debug" {
		t.Errorf("expected log level 'debug', got %q", cfg.Logging.Level)
	}
	if cfg.Pipeline.Workers.Gathering != 8 {
		t.Errorf("expected gathering workers=8, got %d", cfg.Pipeline.Workers.Gathering)
	}
}

func TestLoad_UnknownFieldRejected(t *testing.T) {
	yaml := `
replicaCount: 1
unknownField: true
`
	_, err := Load(strings.NewReader(yaml))
	if err == nil {
		t.Fatal("expected error for unknown field, got nil")
	}
	if !strings.Contains(err.Error(), "unknownField") {
		t.Errorf("expected error to mention 'unknownField', got: %v", err)
	}
}

func TestLoad_InvalidYAML(t *testing.T) {
	yaml := `{invalid yaml content`
	_, err := Load(strings.NewReader(yaml))
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

func TestLoad_EmptyReader(t *testing.T) {
	_, err := Load(strings.NewReader(""))
	if err == nil {
		t.Fatal("expected error for empty input, got nil")
	}
}

func TestLoadFromFile_ExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := []byte(`
replicaCount: 3
logging:
  level: warn
  format: json
`)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	cfg, err := LoadFromFile(path)
	if err != nil {
		t.Fatalf("LoadFromFile() returned error: %v", err)
	}
	if cfg.ReplicaCount != 3 {
		t.Errorf("expected ReplicaCount=3, got %d", cfg.ReplicaCount)
	}
}

func TestLoadFromFile_MissingFileReturnsDefaults(t *testing.T) {
	cfg, err := LoadFromFile("/nonexistent/path/config.yaml")
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	def := Default()
	if cfg.ReplicaCount != def.ReplicaCount {
		t.Errorf("expected default ReplicaCount=%d, got %d", def.ReplicaCount, cfg.ReplicaCount)
	}
}

func TestLoadFromFile_InvalidYAMLReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	if err := os.WriteFile(path, []byte("{invalid"), 0o644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	_, err := LoadFromFile(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML file, got nil")
	}
}

func TestLoadFile_IsAliasForLoadFromFile(t *testing.T) {
	// LoadFile should return defaults for missing file, same as LoadFromFile.
	cfg, err := LoadFile("/nonexistent/path/config.yaml")
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	if cfg.ReplicaCount != 1 {
		t.Errorf("expected default ReplicaCount=1, got %d", cfg.ReplicaCount)
	}
}

func TestLoad_FullConfig(t *testing.T) {
	yaml := `
replicaCount: 3
resources:
  requests:
    cpu: 250m
    memory: 256Mi
  limits:
    cpu: "1"
    memory: 1Gi
leaderElection:
  enabled: true
  leaseDuration: 15s
  renewDeadline: 10s
  retryPeriod: 2s
logging:
  level: info
  format: json
filters:
  excludeNamespaces:
    - kube-system
    - kube-public
detectors:
  podStuckPending:
    enabled: true
    threshold: 15m
    cooldown: 30m
  podCrashLoop:
    enabled: true
    threshold: 5
    cooldown: 30m
  podFailed:
    enabled: true
    ignoreExitCodes: [0]
    cooldown: 30m
  nodeNotReady:
    enabled: true
    threshold: 5m
    cooldown: 30m
  pvcStuckBinding:
    enabled: false
    threshold: 10m
    cooldown: 30m
  highPodCount:
    enabled: false
    threshold: 200
    cooldown: 1h
  jobDeadlineExceeded:
    enabled: false
    cooldown: 30m
correlation:
  enabled: true
  windowDuration: 2m
  rules:
    nodeCascade:
      enabled: true
      minPodFailures: 2
    deploymentRollout:
      enabled: true
      minPodFailures: 3
    storageCascade:
      enabled: true
    namespaceStorm:
      enabled: true
      threshold: 20
gatherers:
  podLogs:
    enabled: true
    tailLines: 100
    includePrevious: true
    redactPatterns: []
  karpenterState:
    enabled: true
  nodeConditions:
    enabled: true
    includeAllocatable: true
  superEvent:
    maxAffectedResources: 5
analyzer:
  backend: claude
  systemPromptOverride: ""
  systemPromptAppend: ""
  rateLimiting:
    dailyTokenBudget: 1000000
    hourlyTokenBudget: 100000
  circuitBreaker:
    consecutiveFailures: 5
    openDuration: 10m
  claude:
    model: claude-sonnet-4-5-20250929
    apiKeySecret:
      name: wormsign-api-keys
      key: anthropic-api-key
    maxTokens: 4096
    temperature: 0.0
  claudeBedrock:
    region: us-east-1
    modelId: anthropic.claude-sonnet-4-5-20250929-v1:0
  openai:
    model: gpt-4o
    apiKeySecret:
      name: wormsign-api-keys
      key: openai-api-key
    maxTokens: 4096
    temperature: 0.0
  azureOpenai:
    endpoint: ""
    deploymentName: ""
    apiKeySecret:
      name: wormsign-api-keys
      key: azure-openai-key
sinks:
  slack:
    enabled: false
    webhookSecret:
      name: wormsign-sink-secrets
      key: slack-webhook-url
    channel: ""
    severityFilter: [critical, warning]
    templateOverride: ""
  pagerduty:
    enabled: false
    routingKeySecret:
      name: wormsign-sink-secrets
      key: pagerduty-routing-key
    severityFilter: [critical]
  s3:
    enabled: false
    bucket: ""
    region: ""
    prefix: "wormsign/reports/"
  webhook:
    enabled: false
    url: ""
    headers: {}
    severityFilter: [critical, warning, info]
    allowedDomains: []
  kubernetesEvent:
    enabled: true
    severityFilter: [critical, warning]
pipeline:
  workers:
    gathering: 5
    analysis: 2
    sink: 3
metrics:
  enabled: true
  port: 8080
  serviceMonitor:
    enabled: false
    interval: 30s
health:
  port: 8081
`
	cfg, err := Load(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("Load() full config returned error: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("full config validation failed: %v", err)
	}
	if cfg.ReplicaCount != 3 {
		t.Errorf("expected ReplicaCount=3, got %d", cfg.ReplicaCount)
	}
	if cfg.Detectors.PodCrashLoop.Threshold != 5 {
		t.Errorf("expected PodCrashLoop threshold=5, got %d", cfg.Detectors.PodCrashLoop.Threshold)
	}
}

// ---- ParseLogLevel() tests ----

func TestParseLogLevel_ValidLevels(t *testing.T) {
	tests := []struct {
		input string
		want  slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"DEBUG", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"INFO", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"WARN", slog.LevelWarn},
		{"error", slog.LevelError},
		{"Error", slog.LevelError},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got, err := ParseLogLevel(tt.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("ParseLogLevel(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestParseLogLevel_Invalid(t *testing.T) {
	_, err := ParseLogLevel("trace")
	if err == nil {
		t.Fatal("expected error for invalid log level, got nil")
	}
	if !strings.Contains(err.Error(), "invalid log level") {
		t.Errorf("expected 'invalid log level' in error, got: %v", err)
	}
}

// ---- Validate() tests ----

func TestValidate_DefaultConfigIsValid(t *testing.T) {
	cfg := Default()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Default config should be valid: %v", err)
	}
}

func TestValidate_InvalidLogLevel(t *testing.T) {
	cfg := Default()
	cfg.Logging.Level = "trace"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error for invalid log level")
	}
}

func TestValidate_InvalidLogFormat(t *testing.T) {
	cfg := Default()
	cfg.Logging.Format = "xml"
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for invalid log format")
	}
	if !strings.Contains(err.Error(), "log format") {
		t.Errorf("expected 'log format' in error, got: %v", err)
	}
}

func TestValidate_LeaderElection_RenewDeadlineTooLarge(t *testing.T) {
	cfg := Default()
	cfg.LeaderElection.RenewDeadline = 20 * time.Second // > leaseDuration (15s)
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for renewDeadline > leaseDuration")
	}
	if !strings.Contains(err.Error(), "renewDeadline") {
		t.Errorf("expected 'renewDeadline' in error, got: %v", err)
	}
}

func TestValidate_LeaderElection_RetryPeriodTooLarge(t *testing.T) {
	cfg := Default()
	cfg.LeaderElection.RetryPeriod = 15 * time.Second // > renewDeadline (10s)
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for retryPeriod > renewDeadline")
	}
	if !strings.Contains(err.Error(), "retryPeriod") {
		t.Errorf("expected 'retryPeriod' in error, got: %v", err)
	}
}

func TestValidate_LeaderElection_DisabledSkipsValidation(t *testing.T) {
	cfg := Default()
	cfg.LeaderElection.Enabled = false
	cfg.LeaderElection.LeaseDuration = 0 // Would be invalid if enabled
	if err := cfg.Validate(); err != nil {
		t.Fatalf("disabled leader election should skip validation: %v", err)
	}
}

func TestValidate_InvalidRegexInExcludeNamespaces(t *testing.T) {
	cfg := Default()
	cfg.Filters.ExcludeNamespaces = []string{"valid-ns", "[invalid"}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for invalid regex")
	}
	if !strings.Contains(err.Error(), "invalid regex") {
		t.Errorf("expected 'invalid regex' in error, got: %v", err)
	}
}

func TestValidate_EmptyNamespacePattern(t *testing.T) {
	cfg := Default()
	cfg.Filters.ExcludeNamespaces = []string{"kube-system", ""}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for empty namespace pattern")
	}
	if !strings.Contains(err.Error(), "must not be empty") {
		t.Errorf("expected 'must not be empty' in error, got: %v", err)
	}
}

func TestValidate_ValidRegexInExcludeNamespaces(t *testing.T) {
	cfg := Default()
	cfg.Filters.ExcludeNamespaces = []string{"kube-system", ".*-sandbox", "test-[0-9]+"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("valid regex patterns should pass: %v", err)
	}
}

func TestValidate_Detectors_InvalidThreshold(t *testing.T) {
	tests := []struct {
		name   string
		modify func(*Config)
	}{
		{
			name: "PodStuckPending zero threshold when enabled",
			modify: func(c *Config) {
				c.Detectors.PodStuckPending.Enabled = true
				c.Detectors.PodStuckPending.Threshold = 0
			},
		},
		{
			name: "PodCrashLoop zero threshold when enabled",
			modify: func(c *Config) {
				c.Detectors.PodCrashLoop.Enabled = true
				c.Detectors.PodCrashLoop.Threshold = 0
			},
		},
		{
			name: "NodeNotReady zero threshold when enabled",
			modify: func(c *Config) {
				c.Detectors.NodeNotReady.Enabled = true
				c.Detectors.NodeNotReady.Threshold = 0
			},
		},
		{
			name: "PVCStuckBinding zero threshold when enabled",
			modify: func(c *Config) {
				c.Detectors.PVCStuckBinding.Enabled = true
				c.Detectors.PVCStuckBinding.Threshold = 0
			},
		},
		{
			name: "HighPodCount zero threshold when enabled",
			modify: func(c *Config) {
				c.Detectors.HighPodCount.Enabled = true
				c.Detectors.HighPodCount.Threshold = 0
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			tt.modify(cfg)
			if err := cfg.Validate(); err == nil {
				t.Error("expected validation error")
			}
		})
	}
}

func TestValidate_Detectors_NegativeCooldown(t *testing.T) {
	cfg := Default()
	cfg.Detectors.PodCrashLoop.Cooldown = -1 * time.Minute
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for negative cooldown")
	}
	if !strings.Contains(err.Error(), "cooldown") {
		t.Errorf("expected 'cooldown' in error, got: %v", err)
	}
}

func TestValidate_Correlation_InvalidWindowDuration(t *testing.T) {
	cfg := Default()
	cfg.Correlation.Enabled = true
	cfg.Correlation.WindowDuration = 0
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for zero window duration")
	}
}

func TestValidate_Correlation_DisabledSkipsValidation(t *testing.T) {
	cfg := Default()
	cfg.Correlation.Enabled = false
	cfg.Correlation.WindowDuration = 0
	if err := cfg.Validate(); err != nil {
		t.Fatalf("disabled correlation should skip validation: %v", err)
	}
}

func TestValidate_Correlation_InvalidMinPodFailures(t *testing.T) {
	cfg := Default()
	cfg.Correlation.Rules.NodeCascade.MinPodFailures = 0
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for minPodFailures=0")
	}
}

func TestValidate_Gatherers_InvalidTailLines(t *testing.T) {
	cfg := Default()
	cfg.Gatherers.PodLogs.TailLines = 0
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for tailLines=0")
	}
}

func TestValidate_Gatherers_InvalidRedactPattern(t *testing.T) {
	cfg := Default()
	cfg.Gatherers.PodLogs.RedactPatterns = []string{"valid.*", "[invalid"}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for invalid redact pattern")
	}
	if !strings.Contains(err.Error(), "invalid regex") {
		t.Errorf("expected 'invalid regex' in error, got: %v", err)
	}
}

func TestValidate_Gatherers_EmptyRedactPattern(t *testing.T) {
	cfg := Default()
	cfg.Gatherers.PodLogs.RedactPatterns = []string{""}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for empty redact pattern")
	}
}

func TestValidate_Gatherers_InvalidMaxAffectedResources(t *testing.T) {
	cfg := Default()
	cfg.Gatherers.SuperEvent.MaxAffectedResources = 0
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for maxAffectedResources=0")
	}
}

func TestValidate_Analyzer_InvalidBackend(t *testing.T) {
	cfg := Default()
	cfg.Analyzer.Backend = "gemini"
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for invalid analyzer backend")
	}
	if !strings.Contains(err.Error(), "analyzer.backend") {
		t.Errorf("expected 'analyzer.backend' in error, got: %v", err)
	}
}

func TestValidate_Analyzer_AllValidBackends(t *testing.T) {
	backends := []string{"claude", "claude-bedrock", "openai", "azure-openai", "noop"}
	for _, backend := range backends {
		t.Run(backend, func(t *testing.T) {
			cfg := Default()
			cfg.Analyzer.Backend = backend
			// Set required fields for backends that need them.
			switch backend {
			case "azure-openai":
				cfg.Analyzer.AzureOpenAI.Endpoint = "https://example.openai.azure.com"
				cfg.Analyzer.AzureOpenAI.DeploymentName = "gpt-4o"
			}
			if err := cfg.Validate(); err != nil {
				t.Errorf("backend %q should be valid: %v", backend, err)
			}
		})
	}
}

func TestValidate_Analyzer_CircuitBreaker_InvalidConsecutiveFailures(t *testing.T) {
	cfg := Default()
	cfg.Analyzer.CircuitBreaker.ConsecutiveFailures = 0
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for consecutiveFailures=0")
	}
}

func TestValidate_Analyzer_CircuitBreaker_InvalidOpenDuration(t *testing.T) {
	cfg := Default()
	cfg.Analyzer.CircuitBreaker.OpenDuration = 0
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for openDuration=0")
	}
}

func TestValidate_Analyzer_Claude_InvalidTemperature(t *testing.T) {
	cfg := Default()
	cfg.Analyzer.Backend = "claude"
	cfg.Analyzer.Claude.Temperature = 1.5
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for temperature > 1")
	}
}

func TestValidate_Analyzer_Claude_InvalidMaxTokens(t *testing.T) {
	cfg := Default()
	cfg.Analyzer.Backend = "claude"
	cfg.Analyzer.Claude.MaxTokens = 0
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for maxTokens=0")
	}
}

func TestValidate_Analyzer_ClaudeBedrock_MissingRegion(t *testing.T) {
	cfg := Default()
	cfg.Analyzer.Backend = "claude-bedrock"
	cfg.Analyzer.ClaudeBedrock.Region = ""
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for missing bedrock region")
	}
}

func TestValidate_Analyzer_ClaudeBedrock_MissingModelID(t *testing.T) {
	cfg := Default()
	cfg.Analyzer.Backend = "claude-bedrock"
	cfg.Analyzer.ClaudeBedrock.ModelID = ""
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for missing bedrock modelId")
	}
}

func TestValidate_Analyzer_AzureOpenAI_MissingEndpoint(t *testing.T) {
	cfg := Default()
	cfg.Analyzer.Backend = "azure-openai"
	cfg.Analyzer.AzureOpenAI.DeploymentName = "gpt-4o"
	cfg.Analyzer.AzureOpenAI.Endpoint = ""
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for missing azure endpoint")
	}
}

func TestValidate_Analyzer_AzureOpenAI_MissingDeploymentName(t *testing.T) {
	cfg := Default()
	cfg.Analyzer.Backend = "azure-openai"
	cfg.Analyzer.AzureOpenAI.Endpoint = "https://example.openai.azure.com"
	cfg.Analyzer.AzureOpenAI.DeploymentName = ""
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for missing azure deployment name")
	}
}

func TestValidate_Sinks_InvalidSeverity(t *testing.T) {
	cfg := Default()
	cfg.Sinks.Slack.Enabled = true
	cfg.Sinks.Slack.SeverityFilter = []string{"critical", "urgent"}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for invalid severity in sink filter")
	}
	if !strings.Contains(err.Error(), "invalid severity") {
		t.Errorf("expected 'invalid severity' in error, got: %v", err)
	}
}

func TestValidate_Sinks_S3_MissingBucket(t *testing.T) {
	cfg := Default()
	cfg.Sinks.S3.Enabled = true
	cfg.Sinks.S3.Region = "us-east-1"
	cfg.Sinks.S3.Bucket = ""
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for missing S3 bucket")
	}
}

func TestValidate_Sinks_S3_MissingRegion(t *testing.T) {
	cfg := Default()
	cfg.Sinks.S3.Enabled = true
	cfg.Sinks.S3.Bucket = "my-bucket"
	cfg.Sinks.S3.Region = ""
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for missing S3 region")
	}
}

func TestValidate_Sinks_Webhook_MissingURL(t *testing.T) {
	cfg := Default()
	cfg.Sinks.Webhook.Enabled = true
	cfg.Sinks.Webhook.URL = ""
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for missing webhook URL")
	}
}

func TestValidate_Sinks_DisabledSkipsRequiredFields(t *testing.T) {
	cfg := Default()
	// S3 and webhook disabled, missing required fields should not error.
	cfg.Sinks.S3.Enabled = false
	cfg.Sinks.S3.Bucket = ""
	cfg.Sinks.Webhook.Enabled = false
	cfg.Sinks.Webhook.URL = ""
	if err := cfg.Validate(); err != nil {
		t.Fatalf("disabled sinks should not require fields: %v", err)
	}
}

func TestValidate_Pipeline_InvalidWorkers(t *testing.T) {
	tests := []struct {
		name   string
		modify func(*Config)
	}{
		{"gathering=0", func(c *Config) { c.Pipeline.Workers.Gathering = 0 }},
		{"analysis=0", func(c *Config) { c.Pipeline.Workers.Analysis = 0 }},
		{"sink=0", func(c *Config) { c.Pipeline.Workers.Sink = 0 }},
		{"gathering=-1", func(c *Config) { c.Pipeline.Workers.Gathering = -1 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			tt.modify(cfg)
			if err := cfg.Validate(); err == nil {
				t.Error("expected validation error for invalid worker count")
			}
		})
	}
}

func TestValidate_Metrics_InvalidPort(t *testing.T) {
	cfg := Default()
	cfg.Metrics.Port = 0
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for metrics port=0")
	}
}

func TestValidate_Metrics_PortTooLarge(t *testing.T) {
	cfg := Default()
	cfg.Metrics.Port = 70000
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for metrics port > 65535")
	}
}

func TestValidate_Health_InvalidPort(t *testing.T) {
	cfg := Default()
	cfg.Health.Port = 0
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for health port=0")
	}
}

func TestValidate_Health_SameAsMetricsPort(t *testing.T) {
	cfg := Default()
	cfg.Health.Port = cfg.Metrics.Port
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for health port == metrics port")
	}
	if !strings.Contains(err.Error(), "must not equal") {
		t.Errorf("expected 'must not equal' in error, got: %v", err)
	}
}

func TestValidate_Analyzer_NegativeTokenBudget(t *testing.T) {
	cfg := Default()
	cfg.Analyzer.RateLimiting.DailyTokenBudget = -1
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for negative daily token budget")
	}
}

func TestValidate_Analyzer_OpenAI_InvalidTemperature(t *testing.T) {
	cfg := Default()
	cfg.Analyzer.Backend = "openai"
	cfg.Analyzer.OpenAI.Temperature = -0.5
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for negative temperature")
	}
}

// ---- Integration: Load + ApplyDefaults + Validate ----

func TestLoadApplyDefaultsValidate_MinimalConfig(t *testing.T) {
	// A minimal config that only sets a few fields should work with defaults.
	yaml := `
logging:
  level: warn
  format: json
analyzer:
  backend: noop
`
	cfg, err := Load(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("minimal config should be valid after defaults: %v", err)
	}
	if cfg.Logging.Level != "warn" {
		t.Errorf("expected log level 'warn', got %q", cfg.Logging.Level)
	}
	if cfg.Pipeline.Workers.Gathering != 5 {
		t.Errorf("expected default gathering workers=5, got %d", cfg.Pipeline.Workers.Gathering)
	}
}

func TestLoadApplyDefaultsValidate_PartialDetectors(t *testing.T) {
	yaml := `
detectors:
  podCrashLoop:
    enabled: true
    threshold: 10
analyzer:
  backend: noop
`
	cfg, err := Load(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("partial detector config should be valid: %v", err)
	}
	if cfg.Detectors.PodCrashLoop.Threshold != 10 {
		t.Errorf("expected threshold=10, got %d", cfg.Detectors.PodCrashLoop.Threshold)
	}
	if cfg.Detectors.PodCrashLoop.Cooldown != 30*time.Minute {
		t.Errorf("expected default cooldown=30m, got %s", cfg.Detectors.PodCrashLoop.Cooldown)
	}
}

func TestLoad_SinksWithHeaders(t *testing.T) {
	yaml := `
analyzer:
  backend: noop
sinks:
  webhook:
    enabled: true
    url: https://example.com/webhook
    headers:
      Content-Type: application/json
      X-Custom: value
    severityFilter: [critical]
`
	cfg, err := Load(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("webhook config should be valid: %v", err)
	}
	if len(cfg.Sinks.Webhook.Headers) != 2 {
		t.Errorf("expected 2 headers, got %d", len(cfg.Sinks.Webhook.Headers))
	}
	if cfg.Sinks.Webhook.Headers["Content-Type"] != "application/json" {
		t.Errorf("unexpected Content-Type header: %q", cfg.Sinks.Webhook.Headers["Content-Type"])
	}
}

func TestLoad_FiltersWithLabelSelector(t *testing.T) {
	yaml := `
analyzer:
  backend: noop
filters:
  excludeNamespaces:
    - kube-system
  excludeNamespaceSelector:
    matchLabels:
      wormsign.io/exclude: "true"
`
	cfg, err := Load(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("filter config should be valid: %v", err)
	}
	if cfg.Filters.ExcludeNamespaceSelector == nil {
		t.Fatal("expected excludeNamespaceSelector to be set")
	}
	if cfg.Filters.ExcludeNamespaceSelector.MatchLabels["wormsign.io/exclude"] != "true" {
		t.Error("expected label selector to contain wormsign.io/exclude=true")
	}
}

// ---- LoadAndValidate() tests ----

func TestLoadAndValidate_ValidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := []byte(`
logging:
  level: info
  format: json
analyzer:
  backend: noop
`)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	cfg, err := LoadAndValidate(path)
	if err != nil {
		t.Fatalf("LoadAndValidate() returned error: %v", err)
	}
	// Verify defaults were applied.
	if cfg.Pipeline.Workers.Gathering != 5 {
		t.Errorf("expected gathering workers=5 (from defaults), got %d", cfg.Pipeline.Workers.Gathering)
	}
}

func TestLoadAndValidate_MissingFileReturnsDefaults(t *testing.T) {
	cfg, err := LoadAndValidate("/nonexistent/path/config.yaml")
	if err != nil {
		t.Fatalf("expected no error for missing file, got: %v", err)
	}
	if cfg.ReplicaCount != 1 {
		t.Errorf("expected default ReplicaCount=1, got %d", cfg.ReplicaCount)
	}
}

func TestLoadAndValidate_InvalidConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	content := []byte(`
logging:
  level: invalid_level
  format: json
`)
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	_, err := LoadAndValidate(path)
	if err == nil {
		t.Fatal("expected validation error for invalid log level")
	}
	if !strings.Contains(err.Error(), "config validation failed") {
		t.Errorf("expected 'config validation failed' prefix in error, got: %v", err)
	}
}

func TestLoadAndValidate_InvalidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "broken.yaml")
	if err := os.WriteFile(path, []byte("{invalid"), 0o644); err != nil {
		t.Fatalf("writing test file: %v", err)
	}

	_, err := LoadAndValidate(path)
	if err == nil {
		t.Fatal("expected error for invalid YAML, got nil")
	}
}

// ---- Additional edge case tests ----

func TestValidate_ReplicaCount_Zero(t *testing.T) {
	// After ApplyDefaults, ReplicaCount should be filled in.
	cfg := &Config{}
	cfg.ApplyDefaults()
	if cfg.ReplicaCount != 1 {
		t.Errorf("expected ReplicaCount=1 after defaults, got %d", cfg.ReplicaCount)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("default config should be valid: %v", err)
	}
}

func TestValidate_Analyzer_NegativeHourlyTokenBudget(t *testing.T) {
	cfg := Default()
	cfg.Analyzer.RateLimiting.HourlyTokenBudget = -1
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for negative hourly token budget")
	}
	if !strings.Contains(err.Error(), "hourlyTokenBudget") {
		t.Errorf("expected 'hourlyTokenBudget' in error, got: %v", err)
	}
}

func TestValidate_Analyzer_OpenAI_InvalidMaxTokens(t *testing.T) {
	cfg := Default()
	cfg.Analyzer.Backend = "openai"
	cfg.Analyzer.OpenAI.MaxTokens = 0
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for openai maxTokens=0")
	}
}

func TestValidate_Sinks_KubernetesEvent_InvalidSeverity(t *testing.T) {
	cfg := Default()
	cfg.Sinks.KubernetesEvent.SeverityFilter = []string{"critical", "fatal"}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for invalid severity in kubernetesEvent sink filter")
	}
}

func TestValidate_Sinks_PagerDuty_InvalidSeverity(t *testing.T) {
	cfg := Default()
	cfg.Sinks.PagerDuty.SeverityFilter = []string{"high"}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for invalid severity in pagerduty sink filter")
	}
}

func TestValidate_Sinks_Webhook_InvalidSeverity(t *testing.T) {
	cfg := Default()
	cfg.Sinks.Webhook.SeverityFilter = []string{"critical", "low"}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for invalid severity in webhook sink filter")
	}
}

func TestValidate_Correlation_DeploymentRollout_InvalidMinPodFailures(t *testing.T) {
	cfg := Default()
	cfg.Correlation.Rules.DeploymentRollout.MinPodFailures = 0
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for deploymentRollout minPodFailures=0")
	}
}

func TestValidate_Correlation_NamespaceStorm_InvalidThreshold(t *testing.T) {
	cfg := Default()
	cfg.Correlation.Rules.NamespaceStorm.Threshold = 0
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for namespaceStorm threshold=0")
	}
}

func TestValidate_AllDetectorCooldowns_Negative(t *testing.T) {
	detectors := []struct {
		name   string
		modify func(*Config)
	}{
		{"PodStuckPending", func(c *Config) { c.Detectors.PodStuckPending.Cooldown = -time.Second }},
		{"PodFailed", func(c *Config) { c.Detectors.PodFailed.Cooldown = -time.Second }},
		{"NodeNotReady", func(c *Config) { c.Detectors.NodeNotReady.Cooldown = -time.Second }},
		{"PVCStuckBinding", func(c *Config) { c.Detectors.PVCStuckBinding.Cooldown = -time.Second }},
		{"HighPodCount", func(c *Config) { c.Detectors.HighPodCount.Cooldown = -time.Second }},
		{"JobDeadlineExceeded", func(c *Config) { c.Detectors.JobDeadlineExceeded.Cooldown = -time.Second }},
	}
	for _, tt := range detectors {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Default()
			tt.modify(cfg)
			if err := cfg.Validate(); err == nil {
				t.Errorf("expected validation error for %s negative cooldown", tt.name)
			}
		})
	}
}

func TestValidate_MetricsServiceMonitor_ZeroIntervalWhenEnabled(t *testing.T) {
	cfg := Default()
	cfg.Metrics.ServiceMonitor.Enabled = true
	cfg.Metrics.ServiceMonitor.Interval = 0
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for serviceMonitor interval=0 when enabled")
	}
}

func TestValidate_Health_PortTooLarge(t *testing.T) {
	cfg := Default()
	cfg.Health.Port = 70000
	err := cfg.Validate()
	if err == nil {
		t.Fatal("expected validation error for health port > 65535")
	}
}

func TestApplyDefaults_SinkDefaults(t *testing.T) {
	cfg := &Config{
		Sinks: SinksConfig{
			Slack: SlackSinkConfig{
				Enabled: true,
				// SeverityFilter nil — should get defaults
			},
			PagerDuty: PagerDutySinkConfig{
				Enabled: true,
				// SeverityFilter nil — should get defaults
			},
		},
	}
	cfg.ApplyDefaults()

	if len(cfg.Sinks.Slack.SeverityFilter) != 2 {
		t.Errorf("expected 2 default severity filters for Slack, got %d", len(cfg.Sinks.Slack.SeverityFilter))
	}
	if len(cfg.Sinks.PagerDuty.SeverityFilter) != 1 {
		t.Errorf("expected 1 default severity filter for PagerDuty, got %d", len(cfg.Sinks.PagerDuty.SeverityFilter))
	}
}

func TestLoad_GathererRedactPatterns(t *testing.T) {
	yaml := `
analyzer:
  backend: noop
gatherers:
  podLogs:
    enabled: true
    tailLines: 50
    includePrevious: false
    redactPatterns:
      - "password=.*"
      - "Bearer [A-Za-z0-9._-]+"
`
	cfg, err := Load(strings.NewReader(yaml))
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		t.Fatalf("redact patterns should be valid: %v", err)
	}
	if len(cfg.Gatherers.PodLogs.RedactPatterns) != 2 {
		t.Errorf("expected 2 redact patterns, got %d", len(cfg.Gatherers.PodLogs.RedactPatterns))
	}
}
