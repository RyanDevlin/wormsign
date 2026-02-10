package config

import (
	"fmt"
	"regexp"
	"strings"
)

// validAnalyzerBackends is the set of recognized analyzer backend names.
var validAnalyzerBackends = map[string]bool{
	"claude":         true,
	"claude-bedrock": true,
	"openai":         true,
	"azure-openai":   true,
	"rules":          true,
	"noop":           true,
}

// validSeverities is the set of valid severity filter values.
var validSeverities = map[string]bool{
	"critical": true,
	"warning":  true,
	"info":     true,
}

// Validate checks the config for invalid or contradictory settings.
// It should be called after ApplyDefaults. On the first error encountered,
// it returns a descriptive error. The controller should crash with this
// error at startup per Section 2.5 of the project specification.
func (c *Config) Validate() error {
	if err := c.validateLogging(); err != nil {
		return err
	}
	if err := c.validateLeaderElection(); err != nil {
		return err
	}
	if err := c.validateFilters(); err != nil {
		return err
	}
	if err := c.validateDetectors(); err != nil {
		return err
	}
	if err := c.validateCorrelation(); err != nil {
		return err
	}
	if err := c.validateGatherers(); err != nil {
		return err
	}
	if err := c.validateAnalyzer(); err != nil {
		return err
	}
	if err := c.validateSinks(); err != nil {
		return err
	}
	if err := c.validatePipeline(); err != nil {
		return err
	}
	if err := c.validateMetrics(); err != nil {
		return err
	}
	if err := c.validateHealth(); err != nil {
		return err
	}
	if err := c.validateControllerTuning(); err != nil {
		return err
	}
	return nil
}

func (c *Config) validateLogging() error {
	if _, err := ParseLogLevel(c.Logging.Level); err != nil {
		return err
	}
	format := strings.ToLower(c.Logging.Format)
	if format != "json" && format != "text" {
		return fmt.Errorf("invalid log format %q: must be json or text", c.Logging.Format)
	}
	return nil
}

func (c *Config) validateLeaderElection() error {
	if !c.LeaderElection.Enabled {
		return nil
	}
	if c.LeaderElection.LeaseDuration <= 0 {
		return fmt.Errorf("leaderElection.leaseDuration must be positive, got %s", c.LeaderElection.LeaseDuration)
	}
	if c.LeaderElection.RenewDeadline <= 0 {
		return fmt.Errorf("leaderElection.renewDeadline must be positive, got %s", c.LeaderElection.RenewDeadline)
	}
	if c.LeaderElection.RetryPeriod <= 0 {
		return fmt.Errorf("leaderElection.retryPeriod must be positive, got %s", c.LeaderElection.RetryPeriod)
	}
	if c.LeaderElection.RenewDeadline >= c.LeaderElection.LeaseDuration {
		return fmt.Errorf("leaderElection.renewDeadline (%s) must be less than leaseDuration (%s)",
			c.LeaderElection.RenewDeadline, c.LeaderElection.LeaseDuration)
	}
	if c.LeaderElection.RetryPeriod >= c.LeaderElection.RenewDeadline {
		return fmt.Errorf("leaderElection.retryPeriod (%s) must be less than renewDeadline (%s)",
			c.LeaderElection.RetryPeriod, c.LeaderElection.RenewDeadline)
	}
	return nil
}

func (c *Config) validateFilters() error {
	for i, pattern := range c.Filters.ExcludeNamespaces {
		if pattern == "" {
			return fmt.Errorf("filters.excludeNamespaces[%d]: pattern must not be empty", i)
		}
		// Validate regex patterns by attempting to compile them.
		if _, err := regexp.Compile(pattern); err != nil {
			return fmt.Errorf("filters.excludeNamespaces[%d]: invalid regex %q: %w", i, pattern, err)
		}
	}
	return nil
}

func (c *Config) validateDetectors() error {
	// Validate durations are positive when detectors are enabled.
	if c.Detectors.PodStuckPending.Enabled {
		if c.Detectors.PodStuckPending.Threshold <= 0 {
			return fmt.Errorf("detectors.podStuckPending.threshold must be positive when enabled")
		}
	}
	if c.Detectors.PodStuckPending.Cooldown < 0 {
		return fmt.Errorf("detectors.podStuckPending.cooldown must not be negative")
	}

	if c.Detectors.PodCrashLoop.Enabled {
		if c.Detectors.PodCrashLoop.Threshold < 1 {
			return fmt.Errorf("detectors.podCrashLoop.threshold must be >= 1 when enabled, got %d", c.Detectors.PodCrashLoop.Threshold)
		}
	}
	if c.Detectors.PodCrashLoop.Cooldown < 0 {
		return fmt.Errorf("detectors.podCrashLoop.cooldown must not be negative")
	}

	if c.Detectors.PodFailed.Cooldown < 0 {
		return fmt.Errorf("detectors.podFailed.cooldown must not be negative")
	}

	if c.Detectors.NodeNotReady.Enabled {
		if c.Detectors.NodeNotReady.Threshold <= 0 {
			return fmt.Errorf("detectors.nodeNotReady.threshold must be positive when enabled")
		}
	}
	if c.Detectors.NodeNotReady.Cooldown < 0 {
		return fmt.Errorf("detectors.nodeNotReady.cooldown must not be negative")
	}

	if c.Detectors.PVCStuckBinding.Enabled {
		if c.Detectors.PVCStuckBinding.Threshold <= 0 {
			return fmt.Errorf("detectors.pvcStuckBinding.threshold must be positive when enabled")
		}
	}
	if c.Detectors.PVCStuckBinding.Cooldown < 0 {
		return fmt.Errorf("detectors.pvcStuckBinding.cooldown must not be negative")
	}

	if c.Detectors.HighPodCount.Enabled {
		if c.Detectors.HighPodCount.Threshold < 1 {
			return fmt.Errorf("detectors.highPodCount.threshold must be >= 1 when enabled, got %d", c.Detectors.HighPodCount.Threshold)
		}
	}
	if c.Detectors.HighPodCount.Cooldown < 0 {
		return fmt.Errorf("detectors.highPodCount.cooldown must not be negative")
	}

	if c.Detectors.JobDeadlineExceeded.Cooldown < 0 {
		return fmt.Errorf("detectors.jobDeadlineExceeded.cooldown must not be negative")
	}

	return nil
}

func (c *Config) validateCorrelation() error {
	if !c.Correlation.Enabled {
		return nil
	}
	if c.Correlation.WindowDuration <= 0 {
		return fmt.Errorf("correlation.windowDuration must be positive when correlation is enabled")
	}
	if c.Correlation.Rules.NodeCascade.Enabled && c.Correlation.Rules.NodeCascade.MinPodFailures < 1 {
		return fmt.Errorf("correlation.rules.nodeCascade.minPodFailures must be >= 1, got %d",
			c.Correlation.Rules.NodeCascade.MinPodFailures)
	}
	if c.Correlation.Rules.DeploymentRollout.Enabled && c.Correlation.Rules.DeploymentRollout.MinPodFailures < 1 {
		return fmt.Errorf("correlation.rules.deploymentRollout.minPodFailures must be >= 1, got %d",
			c.Correlation.Rules.DeploymentRollout.MinPodFailures)
	}
	if c.Correlation.Rules.NamespaceStorm.Enabled && c.Correlation.Rules.NamespaceStorm.Threshold < 1 {
		return fmt.Errorf("correlation.rules.namespaceStorm.threshold must be >= 1, got %d",
			c.Correlation.Rules.NamespaceStorm.Threshold)
	}
	return nil
}

func (c *Config) validateGatherers() error {
	if c.Gatherers.PodLogs.Enabled && c.Gatherers.PodLogs.TailLines < 1 {
		return fmt.Errorf("gatherers.podLogs.tailLines must be >= 1 when enabled, got %d",
			c.Gatherers.PodLogs.TailLines)
	}
	if c.Gatherers.SuperEvent.MaxAffectedResources < 1 {
		return fmt.Errorf("gatherers.superEvent.maxAffectedResources must be >= 1, got %d",
			c.Gatherers.SuperEvent.MaxAffectedResources)
	}

	// Validate redact patterns are valid regex.
	for i, pattern := range c.Gatherers.PodLogs.RedactPatterns {
		if pattern == "" {
			return fmt.Errorf("gatherers.podLogs.redactPatterns[%d]: pattern must not be empty", i)
		}
		if _, err := regexp.Compile(pattern); err != nil {
			return fmt.Errorf("gatherers.podLogs.redactPatterns[%d]: invalid regex %q: %w", i, pattern, err)
		}
	}
	return nil
}

func (c *Config) validateAnalyzer() error {
	if !validAnalyzerBackends[c.Analyzer.Backend] {
		return fmt.Errorf("analyzer.backend %q is not valid: must be one of claude, claude-bedrock, openai, azure-openai, rules, noop",
			c.Analyzer.Backend)
	}

	if c.Analyzer.RateLimiting.DailyTokenBudget < 0 {
		return fmt.Errorf("analyzer.rateLimiting.dailyTokenBudget must not be negative, got %d",
			c.Analyzer.RateLimiting.DailyTokenBudget)
	}
	if c.Analyzer.RateLimiting.HourlyTokenBudget < 0 {
		return fmt.Errorf("analyzer.rateLimiting.hourlyTokenBudget must not be negative, got %d",
			c.Analyzer.RateLimiting.HourlyTokenBudget)
	}

	if c.Analyzer.CircuitBreaker.ConsecutiveFailures < 1 {
		return fmt.Errorf("analyzer.circuitBreaker.consecutiveFailures must be >= 1, got %d",
			c.Analyzer.CircuitBreaker.ConsecutiveFailures)
	}
	if c.Analyzer.CircuitBreaker.OpenDuration <= 0 {
		return fmt.Errorf("analyzer.circuitBreaker.openDuration must be positive, got %s",
			c.Analyzer.CircuitBreaker.OpenDuration)
	}

	// Validate backend-specific config when that backend is selected.
	switch c.Analyzer.Backend {
	case "claude":
		if c.Analyzer.Claude.MaxTokens < 1 {
			return fmt.Errorf("analyzer.claude.maxTokens must be >= 1, got %d", c.Analyzer.Claude.MaxTokens)
		}
		if c.Analyzer.Claude.Temperature < 0 || c.Analyzer.Claude.Temperature > 1 {
			return fmt.Errorf("analyzer.claude.temperature must be between 0 and 1, got %f", c.Analyzer.Claude.Temperature)
		}
	case "claude-bedrock":
		if c.Analyzer.ClaudeBedrock.Region == "" {
			return fmt.Errorf("analyzer.claudeBedrock.region must be set when backend is claude-bedrock")
		}
		if c.Analyzer.ClaudeBedrock.ModelID == "" {
			return fmt.Errorf("analyzer.claudeBedrock.modelId must be set when backend is claude-bedrock")
		}
	case "openai":
		if c.Analyzer.OpenAI.MaxTokens < 1 {
			return fmt.Errorf("analyzer.openai.maxTokens must be >= 1, got %d", c.Analyzer.OpenAI.MaxTokens)
		}
		if c.Analyzer.OpenAI.Temperature < 0 || c.Analyzer.OpenAI.Temperature > 1 {
			return fmt.Errorf("analyzer.openai.temperature must be between 0 and 1, got %f", c.Analyzer.OpenAI.Temperature)
		}
	case "azure-openai":
		if c.Analyzer.AzureOpenAI.Endpoint == "" {
			return fmt.Errorf("analyzer.azureOpenai.endpoint must be set when backend is azure-openai")
		}
		if c.Analyzer.AzureOpenAI.DeploymentName == "" {
			return fmt.Errorf("analyzer.azureOpenai.deploymentName must be set when backend is azure-openai")
		}
	case "rules":
		// No additional validation needed for rules analyzer.
	case "noop":
		// No additional validation needed for noop.
	}

	return nil
}

func (c *Config) validateSinks() error {
	// Validate severity filter values for all sinks.
	if err := validateSeverityFilter("sinks.slack.severityFilter", c.Sinks.Slack.SeverityFilter); err != nil {
		return err
	}
	if err := validateSeverityFilter("sinks.pagerduty.severityFilter", c.Sinks.PagerDuty.SeverityFilter); err != nil {
		return err
	}
	if err := validateSeverityFilter("sinks.webhook.severityFilter", c.Sinks.Webhook.SeverityFilter); err != nil {
		return err
	}
	if err := validateSeverityFilter("sinks.kubernetesEvent.severityFilter", c.Sinks.KubernetesEvent.SeverityFilter); err != nil {
		return err
	}

	// S3 sink requires bucket and region when enabled.
	if c.Sinks.S3.Enabled {
		if c.Sinks.S3.Bucket == "" {
			return fmt.Errorf("sinks.s3.bucket must be set when s3 sink is enabled")
		}
		if c.Sinks.S3.Region == "" {
			return fmt.Errorf("sinks.s3.region must be set when s3 sink is enabled")
		}
	}

	// Webhook sink requires URL and allowedDomains when enabled.
	if c.Sinks.Webhook.Enabled {
		if c.Sinks.Webhook.URL == "" {
			return fmt.Errorf("sinks.webhook.url must be set when webhook sink is enabled")
		}
	}

	return nil
}

func (c *Config) validatePipeline() error {
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

func (c *Config) validateMetrics() error {
	if c.Metrics.Port < 1 || c.Metrics.Port > 65535 {
		return fmt.Errorf("metrics.port must be between 1 and 65535, got %d", c.Metrics.Port)
	}
	if c.Metrics.ServiceMonitor.Enabled && c.Metrics.ServiceMonitor.Interval <= 0 {
		return fmt.Errorf("metrics.serviceMonitor.interval must be positive when enabled")
	}
	return nil
}

func (c *Config) validateHealth() error {
	if c.Health.Port < 1 || c.Health.Port > 65535 {
		return fmt.Errorf("health.port must be between 1 and 65535, got %d", c.Health.Port)
	}
	if c.Health.Port == c.Metrics.Port {
		return fmt.Errorf("health.port (%d) must not equal metrics.port (%d)", c.Health.Port, c.Metrics.Port)
	}
	return nil
}

func (c *Config) validateControllerTuning() error {
	if c.ControllerTuning.DetectorScanInterval <= 0 {
		return fmt.Errorf("controllerTuning.detectorScanInterval must be positive, got %s", c.ControllerTuning.DetectorScanInterval)
	}
	if c.ControllerTuning.ShardReconcileInterval <= 0 {
		return fmt.Errorf("controllerTuning.shardReconcileInterval must be positive, got %s", c.ControllerTuning.ShardReconcileInterval)
	}
	if c.ControllerTuning.InformerResyncPeriod <= 0 {
		return fmt.Errorf("controllerTuning.informerResyncPeriod must be positive, got %s", c.ControllerTuning.InformerResyncPeriod)
	}
	return nil
}

// validateSeverityFilter checks that all values in a severity filter list
// are recognized severity strings.
func validateSeverityFilter(field string, values []string) error {
	for i, v := range values {
		if !validSeverities[v] {
			return fmt.Errorf("%s[%d]: invalid severity %q: must be critical, warning, or info", field, i, v)
		}
	}
	return nil
}
