package config

import "time"

// Default returns a Config populated with production defaults matching
// the Helm values.yaml reference in Section 10 of the project specification.
func Default() *Config {
	return &Config{
		ReplicaCount: 1,

		Resources: ResourcesConfig{
			Requests: ResourceRequirements{
				CPU:    "100m",
				Memory: "128Mi",
			},
			Limits: ResourceRequirements{
				CPU:    "500m",
				Memory: "512Mi",
			},
		},

		LeaderElection: LeaderElectionConfig{
			Enabled:       true,
			LeaseDuration: 15 * time.Second,
			RenewDeadline: 10 * time.Second,
			RetryPeriod:   2 * time.Second,
		},

		Logging: LoggingConfig{
			Level:  "info",
			Format: "json",
		},

		Filters: FiltersConfig{
			ExcludeNamespaces: []string{
				"kube-system",
				"kube-public",
				"kube-node-lease",
			},
		},

		Detectors: DetectorsConfig{
			PodStuckPending: PodStuckPendingDetectorConfig{
				Enabled:   true,
				Threshold: 15 * time.Minute,
				Cooldown:  30 * time.Minute,
			},
			PodCrashLoop: PodCrashLoopDetectorConfig{
				Enabled:   true,
				Threshold: 3,
				Cooldown:  30 * time.Minute,
			},
			PodFailed: PodFailedDetectorConfig{
				Enabled:         true,
				IgnoreExitCodes: []int{0},
				Cooldown:        30 * time.Minute,
			},
			NodeNotReady: NodeNotReadyDetectorConfig{
				Enabled:   true,
				Threshold: 5 * time.Minute,
				Cooldown:  30 * time.Minute,
			},
			PVCStuckBinding: PVCStuckBindingDetectorConfig{
				Enabled:   false,
				Threshold: 10 * time.Minute,
				Cooldown:  30 * time.Minute,
			},
			HighPodCount: HighPodCountDetectorConfig{
				Enabled:   false,
				Threshold: 200,
				Cooldown:  1 * time.Hour,
			},
			JobDeadlineExceeded: JobDeadlineDetectorConfig{
				Enabled:  false,
				Cooldown: 30 * time.Minute,
			},
		},

		Correlation: CorrelationConfig{
			Enabled:        true,
			WindowDuration: 2 * time.Minute,
			Rules: CorrelationRulesConfig{
				NodeCascade: NodeCascadeRuleConfig{
					Enabled:        true,
					MinPodFailures: 2,
				},
				DeploymentRollout: DeploymentRolloutRuleConfig{
					Enabled:        true,
					MinPodFailures: 3,
				},
				StorageCascade: StorageCascadeRuleConfig{
					Enabled: true,
				},
				NamespaceStorm: NamespaceStormRuleConfig{
					Enabled:   true,
					Threshold: 20,
				},
			},
		},

		Gatherers: GatherersConfig{
			PodLogs: PodLogsGathererConfig{
				Enabled:         true,
				TailLines:       100,
				IncludePrevious: true,
			},
			KarpenterState: KarpenterStateGathererConfig{
				Enabled: true,
			},
			NodeConditions: NodeConditionsGathererConfig{
				Enabled:            true,
				IncludeAllocatable: true,
			},
			SuperEvent: SuperEventGathererConfig{
				MaxAffectedResources: 5,
			},
		},

		Analyzer: AnalyzerConfig{
			Backend: "claude",
			RateLimiting: RateLimitingConfig{
				DailyTokenBudget:  1_000_000,
				HourlyTokenBudget: 100_000,
			},
			CircuitBreaker: CircuitBreakerConfig{
				ConsecutiveFailures: 5,
				OpenDuration:        10 * time.Minute,
			},
			Claude: ClaudeConfig{
				Model: "claude-sonnet-4-5-20250929",
				APIKeySecret: SecretKeyRef{
					Name: "wormsign-api-keys",
					Key:  "anthropic-api-key",
				},
				MaxTokens:   4096,
				Temperature: 0.0,
			},
			ClaudeBedrock: ClaudeBedrockConfig{
				Region:  "us-east-1",
				ModelID: "anthropic.claude-sonnet-4-5-20250929-v1:0",
			},
			OpenAI: OpenAIConfig{
				Model: "gpt-4o",
				APIKeySecret: SecretKeyRef{
					Name: "wormsign-api-keys",
					Key:  "openai-api-key",
				},
				MaxTokens:   4096,
				Temperature: 0.0,
			},
			AzureOpenAI: AzureOpenAIConfig{
				APIKeySecret: SecretKeyRef{
					Name: "wormsign-api-keys",
					Key:  "azure-openai-key",
				},
			},
		},

		Sinks: SinksConfig{
			Slack: SlackSinkConfig{
				Enabled: false,
				WebhookSecret: SecretKeyRef{
					Name: "wormsign-sink-secrets",
					Key:  "slack-webhook-url",
				},
				SeverityFilter: []string{"critical", "warning"},
			},
			PagerDuty: PagerDutySinkConfig{
				Enabled: false,
				RoutingKeySecret: SecretKeyRef{
					Name: "wormsign-sink-secrets",
					Key:  "pagerduty-routing-key",
				},
				SeverityFilter: []string{"critical"},
			},
			S3: S3SinkConfig{
				Enabled: false,
				Prefix:  "wormsign/reports/",
			},
			Webhook: WebhookSinkConfig{
				Enabled:        false,
				SeverityFilter: []string{"critical", "warning", "info"},
			},
			KubernetesEvent: KubernetesEventSinkConfig{
				Enabled:        true,
				SeverityFilter: []string{"critical", "warning"},
			},
		},

		Pipeline: PipelineConfig{
			Workers: WorkersConfig{
				Gathering: 5,
				Analysis:  2,
				Sink:      3,
			},
		},

		Metrics: MetricsConfig{
			Enabled: true,
			Port:    8080,
			ServiceMonitor: ServiceMonitorConfig{
				Enabled:  false,
				Interval: 30 * time.Second,
			},
		},

		Health: HealthConfig{
			Port: 8081,
		},

		ControllerTuning: ControllerTuningConfig{
			DetectorScanInterval:   30 * time.Second,
			ShardReconcileInterval: 30 * time.Second,
			InformerResyncPeriod:   30 * time.Minute,
		},
	}
}

// ApplyDefaults fills in zero-valued fields with production defaults.
// It should be called after loading configuration from a file to ensure
// all fields have sensible values.
func (c *Config) ApplyDefaults() {
	d := Default()

	if c.ReplicaCount == 0 {
		c.ReplicaCount = d.ReplicaCount
	}

	// Resources
	if c.Resources.Requests.CPU == "" {
		c.Resources.Requests.CPU = d.Resources.Requests.CPU
	}
	if c.Resources.Requests.Memory == "" {
		c.Resources.Requests.Memory = d.Resources.Requests.Memory
	}
	if c.Resources.Limits.CPU == "" {
		c.Resources.Limits.CPU = d.Resources.Limits.CPU
	}
	if c.Resources.Limits.Memory == "" {
		c.Resources.Limits.Memory = d.Resources.Limits.Memory
	}

	// Leader election
	if c.LeaderElection.LeaseDuration == 0 {
		c.LeaderElection.LeaseDuration = d.LeaderElection.LeaseDuration
		// If lease duration wasn't set, set all leader election defaults together
		// to keep them internally consistent.
		if c.LeaderElection.RenewDeadline == 0 {
			c.LeaderElection.RenewDeadline = d.LeaderElection.RenewDeadline
		}
		if c.LeaderElection.RetryPeriod == 0 {
			c.LeaderElection.RetryPeriod = d.LeaderElection.RetryPeriod
		}
	}
	if c.LeaderElection.RenewDeadline == 0 {
		c.LeaderElection.RenewDeadline = d.LeaderElection.RenewDeadline
	}
	if c.LeaderElection.RetryPeriod == 0 {
		c.LeaderElection.RetryPeriod = d.LeaderElection.RetryPeriod
	}

	// Logging
	if c.Logging.Level == "" {
		c.Logging.Level = d.Logging.Level
	}
	if c.Logging.Format == "" {
		c.Logging.Format = d.Logging.Format
	}

	// Filters — excludeNamespaces default only if not set at all
	if c.Filters.ExcludeNamespaces == nil {
		c.Filters.ExcludeNamespaces = d.Filters.ExcludeNamespaces
	}

	// Detector defaults — cooldowns and thresholds
	applyDetectorDefaults(&c.Detectors, &d.Detectors)

	// Correlation
	if c.Correlation.WindowDuration == 0 {
		c.Correlation.WindowDuration = d.Correlation.WindowDuration
	}
	applyCorrelationRuleDefaults(&c.Correlation.Rules, &d.Correlation.Rules)

	// Gatherers
	if c.Gatherers.PodLogs.TailLines == 0 {
		c.Gatherers.PodLogs.TailLines = d.Gatherers.PodLogs.TailLines
	}
	if c.Gatherers.SuperEvent.MaxAffectedResources == 0 {
		c.Gatherers.SuperEvent.MaxAffectedResources = d.Gatherers.SuperEvent.MaxAffectedResources
	}

	// Analyzer
	if c.Analyzer.Backend == "" {
		c.Analyzer.Backend = d.Analyzer.Backend
	}
	if c.Analyzer.RateLimiting.DailyTokenBudget == 0 {
		c.Analyzer.RateLimiting.DailyTokenBudget = d.Analyzer.RateLimiting.DailyTokenBudget
	}
	if c.Analyzer.RateLimiting.HourlyTokenBudget == 0 {
		c.Analyzer.RateLimiting.HourlyTokenBudget = d.Analyzer.RateLimiting.HourlyTokenBudget
	}
	if c.Analyzer.CircuitBreaker.ConsecutiveFailures == 0 {
		c.Analyzer.CircuitBreaker.ConsecutiveFailures = d.Analyzer.CircuitBreaker.ConsecutiveFailures
	}
	if c.Analyzer.CircuitBreaker.OpenDuration == 0 {
		c.Analyzer.CircuitBreaker.OpenDuration = d.Analyzer.CircuitBreaker.OpenDuration
	}
	if c.Analyzer.Claude.MaxTokens == 0 {
		c.Analyzer.Claude.MaxTokens = d.Analyzer.Claude.MaxTokens
	}
	if c.Analyzer.OpenAI.MaxTokens == 0 {
		c.Analyzer.OpenAI.MaxTokens = d.Analyzer.OpenAI.MaxTokens
	}

	// Pipeline workers
	if c.Pipeline.Workers.Gathering == 0 {
		c.Pipeline.Workers.Gathering = d.Pipeline.Workers.Gathering
	}
	if c.Pipeline.Workers.Analysis == 0 {
		c.Pipeline.Workers.Analysis = d.Pipeline.Workers.Analysis
	}
	if c.Pipeline.Workers.Sink == 0 {
		c.Pipeline.Workers.Sink = d.Pipeline.Workers.Sink
	}

	// Metrics
	if c.Metrics.Port == 0 {
		c.Metrics.Port = d.Metrics.Port
	}
	if c.Metrics.ServiceMonitor.Interval == 0 {
		c.Metrics.ServiceMonitor.Interval = d.Metrics.ServiceMonitor.Interval
	}

	// Health
	if c.Health.Port == 0 {
		c.Health.Port = d.Health.Port
	}

	// Controller tuning
	if c.ControllerTuning.DetectorScanInterval == 0 {
		c.ControllerTuning.DetectorScanInterval = d.ControllerTuning.DetectorScanInterval
	}
	if c.ControllerTuning.ShardReconcileInterval == 0 {
		c.ControllerTuning.ShardReconcileInterval = d.ControllerTuning.ShardReconcileInterval
	}
	if c.ControllerTuning.InformerResyncPeriod == 0 {
		c.ControllerTuning.InformerResyncPeriod = d.ControllerTuning.InformerResyncPeriod
	}

	// Sinks — apply default severity filters if enabled but not configured
	if c.Sinks.Slack.Enabled && c.Sinks.Slack.SeverityFilter == nil {
		c.Sinks.Slack.SeverityFilter = d.Sinks.Slack.SeverityFilter
	}
	if c.Sinks.PagerDuty.Enabled && c.Sinks.PagerDuty.SeverityFilter == nil {
		c.Sinks.PagerDuty.SeverityFilter = d.Sinks.PagerDuty.SeverityFilter
	}
	if c.Sinks.S3.Enabled && c.Sinks.S3.Prefix == "" {
		c.Sinks.S3.Prefix = d.Sinks.S3.Prefix
	}
	if c.Sinks.Webhook.Enabled && c.Sinks.Webhook.SeverityFilter == nil {
		c.Sinks.Webhook.SeverityFilter = d.Sinks.Webhook.SeverityFilter
	}
	if c.Sinks.KubernetesEvent.SeverityFilter == nil {
		c.Sinks.KubernetesEvent.SeverityFilter = d.Sinks.KubernetesEvent.SeverityFilter
	}
}

func applyDetectorDefaults(c *DetectorsConfig, d *DetectorsConfig) {
	// PodStuckPending
	if c.PodStuckPending.Threshold == 0 {
		c.PodStuckPending.Threshold = d.PodStuckPending.Threshold
	}
	if c.PodStuckPending.Cooldown == 0 {
		c.PodStuckPending.Cooldown = d.PodStuckPending.Cooldown
	}

	// PodCrashLoop
	if c.PodCrashLoop.Threshold == 0 {
		c.PodCrashLoop.Threshold = d.PodCrashLoop.Threshold
	}
	if c.PodCrashLoop.Cooldown == 0 {
		c.PodCrashLoop.Cooldown = d.PodCrashLoop.Cooldown
	}

	// PodFailed
	if c.PodFailed.Cooldown == 0 {
		c.PodFailed.Cooldown = d.PodFailed.Cooldown
	}
	if c.PodFailed.IgnoreExitCodes == nil {
		c.PodFailed.IgnoreExitCodes = d.PodFailed.IgnoreExitCodes
	}

	// NodeNotReady
	if c.NodeNotReady.Threshold == 0 {
		c.NodeNotReady.Threshold = d.NodeNotReady.Threshold
	}
	if c.NodeNotReady.Cooldown == 0 {
		c.NodeNotReady.Cooldown = d.NodeNotReady.Cooldown
	}

	// PVCStuckBinding
	if c.PVCStuckBinding.Threshold == 0 {
		c.PVCStuckBinding.Threshold = d.PVCStuckBinding.Threshold
	}
	if c.PVCStuckBinding.Cooldown == 0 {
		c.PVCStuckBinding.Cooldown = d.PVCStuckBinding.Cooldown
	}

	// HighPodCount
	if c.HighPodCount.Threshold == 0 {
		c.HighPodCount.Threshold = d.HighPodCount.Threshold
	}
	if c.HighPodCount.Cooldown == 0 {
		c.HighPodCount.Cooldown = d.HighPodCount.Cooldown
	}

	// JobDeadlineExceeded
	if c.JobDeadlineExceeded.Cooldown == 0 {
		c.JobDeadlineExceeded.Cooldown = d.JobDeadlineExceeded.Cooldown
	}
}

func applyCorrelationRuleDefaults(c *CorrelationRulesConfig, d *CorrelationRulesConfig) {
	if c.NodeCascade.MinPodFailures == 0 {
		c.NodeCascade.MinPodFailures = d.NodeCascade.MinPodFailures
	}
	if c.DeploymentRollout.MinPodFailures == 0 {
		c.DeploymentRollout.MinPodFailures = d.DeploymentRollout.MinPodFailures
	}
	if c.NamespaceStorm.Threshold == 0 {
		c.NamespaceStorm.Threshold = d.NamespaceStorm.Threshold
	}
}
