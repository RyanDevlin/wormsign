// Package config defines the configuration struct for the Wormsign controller.
// Configuration is loaded from a YAML file mounted via ConfigMap and mirrors
// the Helm values.yaml structure (see DECISIONS.md D11).
package config

import "time"

// DefaultConfigPath is the default filesystem path for the controller config
// file, typically mounted via ConfigMap.
const DefaultConfigPath = "/etc/wormsign/config.yaml"

// Config is the top-level configuration for the Wormsign controller,
// corresponding to the Helm values.yaml structure (Section 10).
type Config struct {
	// ReplicaCount is the number of controller replicas.
	ReplicaCount int `yaml:"replicaCount"`

	// Resources configures CPU/memory requests and limits.
	Resources ResourcesConfig `yaml:"resources"`

	// LeaderElection configures leader election parameters.
	LeaderElection LeaderElectionConfig `yaml:"leaderElection"`

	// Logging configures structured log output.
	Logging LoggingConfig `yaml:"logging"`

	// Filters configures global namespace and label-based exclusions.
	Filters FiltersConfig `yaml:"filters"`

	// Detectors configures built-in fault detectors.
	Detectors DetectorsConfig `yaml:"detectors"`

	// Correlation configures the pre-LLM event correlation engine.
	Correlation CorrelationConfig `yaml:"correlation"`

	// Gatherers configures built-in diagnostic data gatherers.
	Gatherers GatherersConfig `yaml:"gatherers"`

	// Analyzer configures the LLM analysis backend.
	Analyzer AnalyzerConfig `yaml:"analyzer"`

	// Sinks configures where RCA reports are delivered.
	Sinks SinksConfig `yaml:"sinks"`

	// Pipeline configures worker concurrency per stage.
	Pipeline PipelineConfig `yaml:"pipeline"`

	// Metrics configures the Prometheus metrics endpoint.
	Metrics MetricsConfig `yaml:"metrics"`

	// Health configures the health probe port.
	Health HealthConfig `yaml:"health"`
}

// ResourceRequirements holds CPU and memory values for requests or limits.
type ResourceRequirements struct {
	CPU    string `yaml:"cpu"`
	Memory string `yaml:"memory"`
}

// ResourcesConfig holds pod resource requests and limits.
type ResourcesConfig struct {
	Requests ResourceRequirements `yaml:"requests"`
	Limits   ResourceRequirements `yaml:"limits"`
}

// LeaderElectionConfig holds leader election tuning parameters.
type LeaderElectionConfig struct {
	Enabled       bool          `yaml:"enabled"`
	LeaseDuration time.Duration `yaml:"leaseDuration"`
	RenewDeadline time.Duration `yaml:"renewDeadline"`
	RetryPeriod   time.Duration `yaml:"retryPeriod"`
}

// LoggingConfig controls the logging behavior.
type LoggingConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

// FiltersConfig holds global namespace exclusion filters.
type FiltersConfig struct {
	// ExcludeNamespaces lists namespace names or regex patterns to exclude.
	ExcludeNamespaces []string `yaml:"excludeNamespaces"`

	// ExcludeNamespaceSelector excludes namespaces matching these labels.
	ExcludeNamespaceSelector *LabelSelector `yaml:"excludeNamespaceSelector,omitempty"`
}

// LabelSelector mirrors a simplified Kubernetes label selector for config use.
type LabelSelector struct {
	MatchLabels map[string]string `yaml:"matchLabels,omitempty"`
}

// DetectorsConfig holds per-detector configuration for all built-in detectors.
type DetectorsConfig struct {
	PodStuckPending      PodStuckPendingDetectorConfig `yaml:"podStuckPending"`
	PodCrashLoop         PodCrashLoopDetectorConfig    `yaml:"podCrashLoop"`
	PodFailed            PodFailedDetectorConfig       `yaml:"podFailed"`
	NodeNotReady         NodeNotReadyDetectorConfig    `yaml:"nodeNotReady"`
	PVCStuckBinding      PVCStuckBindingDetectorConfig `yaml:"pvcStuckBinding"`
	HighPodCount         HighPodCountDetectorConfig    `yaml:"highPodCount"`
	JobDeadlineExceeded  JobDeadlineDetectorConfig     `yaml:"jobDeadlineExceeded"`
}

// PodStuckPendingDetectorConfig configures the PodStuckPending detector.
type PodStuckPendingDetectorConfig struct {
	Enabled   bool          `yaml:"enabled"`
	Threshold time.Duration `yaml:"threshold"`
	Cooldown  time.Duration `yaml:"cooldown"`
}

// PodCrashLoopDetectorConfig configures the PodCrashLoop detector.
type PodCrashLoopDetectorConfig struct {
	Enabled   bool          `yaml:"enabled"`
	Threshold int           `yaml:"threshold"`
	Cooldown  time.Duration `yaml:"cooldown"`
}

// PodFailedDetectorConfig configures the PodFailed detector.
type PodFailedDetectorConfig struct {
	Enabled        bool          `yaml:"enabled"`
	IgnoreExitCodes []int        `yaml:"ignoreExitCodes"`
	Cooldown       time.Duration `yaml:"cooldown"`
}

// NodeNotReadyDetectorConfig configures the NodeNotReady detector.
type NodeNotReadyDetectorConfig struct {
	Enabled   bool          `yaml:"enabled"`
	Threshold time.Duration `yaml:"threshold"`
	Cooldown  time.Duration `yaml:"cooldown"`
}

// PVCStuckBindingDetectorConfig configures the PVCStuckBinding detector.
type PVCStuckBindingDetectorConfig struct {
	Enabled   bool          `yaml:"enabled"`
	Threshold time.Duration `yaml:"threshold"`
	Cooldown  time.Duration `yaml:"cooldown"`
}

// HighPodCountDetectorConfig configures the HighPodCount detector.
type HighPodCountDetectorConfig struct {
	Enabled   bool          `yaml:"enabled"`
	Threshold int           `yaml:"threshold"`
	Cooldown  time.Duration `yaml:"cooldown"`
}

// JobDeadlineDetectorConfig configures the JobDeadlineExceeded detector.
type JobDeadlineDetectorConfig struct {
	Enabled  bool          `yaml:"enabled"`
	Cooldown time.Duration `yaml:"cooldown"`
}

// CorrelationConfig configures the event correlation engine.
type CorrelationConfig struct {
	Enabled        bool                   `yaml:"enabled"`
	WindowDuration time.Duration          `yaml:"windowDuration"`
	Rules          CorrelationRulesConfig `yaml:"rules"`
}

// CorrelationRulesConfig holds per-rule configuration for the correlator.
type CorrelationRulesConfig struct {
	NodeCascade       NodeCascadeRuleConfig       `yaml:"nodeCascade"`
	DeploymentRollout DeploymentRolloutRuleConfig `yaml:"deploymentRollout"`
	StorageCascade    StorageCascadeRuleConfig    `yaml:"storageCascade"`
	NamespaceStorm    NamespaceStormRuleConfig    `yaml:"namespaceStorm"`
}

// NodeCascadeRuleConfig configures the NodeCascade correlation rule.
type NodeCascadeRuleConfig struct {
	Enabled        bool `yaml:"enabled"`
	MinPodFailures int  `yaml:"minPodFailures"`
}

// DeploymentRolloutRuleConfig configures the DeploymentRollout correlation rule.
type DeploymentRolloutRuleConfig struct {
	Enabled        bool `yaml:"enabled"`
	MinPodFailures int  `yaml:"minPodFailures"`
}

// StorageCascadeRuleConfig configures the StorageCascade correlation rule.
type StorageCascadeRuleConfig struct {
	Enabled bool `yaml:"enabled"`
}

// NamespaceStormRuleConfig configures the NamespaceStorm correlation rule.
type NamespaceStormRuleConfig struct {
	Enabled   bool `yaml:"enabled"`
	Threshold int  `yaml:"threshold"`
}

// GatherersConfig configures built-in diagnostic data gatherers.
type GatherersConfig struct {
	PodLogs        PodLogsGathererConfig        `yaml:"podLogs"`
	KarpenterState KarpenterStateGathererConfig `yaml:"karpenterState"`
	NodeConditions NodeConditionsGathererConfig `yaml:"nodeConditions"`
	SuperEvent     SuperEventGathererConfig     `yaml:"superEvent"`
}

// PodLogsGathererConfig configures the PodLogs gatherer.
type PodLogsGathererConfig struct {
	Enabled         bool     `yaml:"enabled"`
	TailLines       int      `yaml:"tailLines"`
	IncludePrevious bool     `yaml:"includePrevious"`
	RedactPatterns  []string `yaml:"redactPatterns"`
}

// KarpenterStateGathererConfig configures the KarpenterState gatherer.
type KarpenterStateGathererConfig struct {
	Enabled bool `yaml:"enabled"`
}

// NodeConditionsGathererConfig configures the NodeConditions gatherer.
type NodeConditionsGathererConfig struct {
	Enabled            bool `yaml:"enabled"`
	IncludeAllocatable bool `yaml:"includeAllocatable"`
}

// SuperEventGathererConfig configures gathering limits for super-events.
type SuperEventGathererConfig struct {
	MaxAffectedResources int `yaml:"maxAffectedResources"`
}

// AnalyzerConfig configures the LLM analysis backend.
type AnalyzerConfig struct {
	Backend              string               `yaml:"backend"`
	SystemPromptOverride string               `yaml:"systemPromptOverride"`
	SystemPromptAppend   string               `yaml:"systemPromptAppend"`
	RateLimiting         RateLimitingConfig   `yaml:"rateLimiting"`
	CircuitBreaker       CircuitBreakerConfig `yaml:"circuitBreaker"`
	Claude               ClaudeConfig         `yaml:"claude"`
	ClaudeBedrock        ClaudeBedrockConfig  `yaml:"claudeBedrock"`
	OpenAI               OpenAIConfig         `yaml:"openai"`
	AzureOpenAI          AzureOpenAIConfig    `yaml:"azureOpenai"`
}

// RateLimitingConfig configures token budget limits for the analyzer.
type RateLimitingConfig struct {
	DailyTokenBudget  int `yaml:"dailyTokenBudget"`
	HourlyTokenBudget int `yaml:"hourlyTokenBudget"`
}

// CircuitBreakerConfig configures the analyzer circuit breaker.
type CircuitBreakerConfig struct {
	ConsecutiveFailures int           `yaml:"consecutiveFailures"`
	OpenDuration        time.Duration `yaml:"openDuration"`
}

// SecretKeyRef references a Kubernetes Secret by name and key.
type SecretKeyRef struct {
	Name string `yaml:"name"`
	Key  string `yaml:"key"`
}

// ClaudeConfig configures the direct Anthropic Claude backend.
type ClaudeConfig struct {
	Model        string       `yaml:"model"`
	APIKeySecret SecretKeyRef `yaml:"apiKeySecret"`
	MaxTokens    int          `yaml:"maxTokens"`
	Temperature  float64      `yaml:"temperature"`
}

// ClaudeBedrockConfig configures the AWS Bedrock Claude backend.
type ClaudeBedrockConfig struct {
	Region  string `yaml:"region"`
	ModelID string `yaml:"modelId"`
}

// OpenAIConfig configures the OpenAI backend.
type OpenAIConfig struct {
	Model        string       `yaml:"model"`
	APIKeySecret SecretKeyRef `yaml:"apiKeySecret"`
	MaxTokens    int          `yaml:"maxTokens"`
	Temperature  float64      `yaml:"temperature"`
}

// AzureOpenAIConfig configures the Azure OpenAI backend.
type AzureOpenAIConfig struct {
	Endpoint       string       `yaml:"endpoint"`
	DeploymentName string       `yaml:"deploymentName"`
	APIKeySecret   SecretKeyRef `yaml:"apiKeySecret"`
}

// SinksConfig configures all sink integrations.
type SinksConfig struct {
	Slack           SlackSinkConfig           `yaml:"slack"`
	PagerDuty       PagerDutySinkConfig       `yaml:"pagerduty"`
	S3              S3SinkConfig              `yaml:"s3"`
	Webhook         WebhookSinkConfig         `yaml:"webhook"`
	KubernetesEvent KubernetesEventSinkConfig `yaml:"kubernetesEvent"`
}

// SlackSinkConfig configures the Slack webhook sink.
type SlackSinkConfig struct {
	Enabled          bool         `yaml:"enabled"`
	WebhookSecret    SecretKeyRef `yaml:"webhookSecret"`
	Channel          string       `yaml:"channel"`
	SeverityFilter   []string     `yaml:"severityFilter"`
	TemplateOverride string       `yaml:"templateOverride"`
}

// PagerDutySinkConfig configures the PagerDuty sink.
type PagerDutySinkConfig struct {
	Enabled        bool         `yaml:"enabled"`
	RoutingKeySecret SecretKeyRef `yaml:"routingKeySecret"`
	SeverityFilter []string     `yaml:"severityFilter"`
}

// S3SinkConfig configures the AWS S3 archival sink.
type S3SinkConfig struct {
	Enabled bool   `yaml:"enabled"`
	Bucket  string `yaml:"bucket"`
	Region  string `yaml:"region"`
	Prefix  string `yaml:"prefix"`
}

// WebhookSinkConfig configures the generic webhook sink.
type WebhookSinkConfig struct {
	Enabled        bool              `yaml:"enabled"`
	URL            string            `yaml:"url"`
	Headers        map[string]string `yaml:"headers"`
	SeverityFilter []string          `yaml:"severityFilter"`
	AllowedDomains []string          `yaml:"allowedDomains"`
}

// KubernetesEventSinkConfig configures the Kubernetes Event sink.
type KubernetesEventSinkConfig struct {
	Enabled        bool     `yaml:"enabled"`
	SeverityFilter []string `yaml:"severityFilter"`
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

// MetricsConfig configures the Prometheus metrics endpoint.
type MetricsConfig struct {
	Enabled        bool                 `yaml:"enabled"`
	Port           int                  `yaml:"port"`
	ServiceMonitor ServiceMonitorConfig `yaml:"serviceMonitor"`
}

// ServiceMonitorConfig configures the Prometheus ServiceMonitor resource.
type ServiceMonitorConfig struct {
	Enabled  bool          `yaml:"enabled"`
	Interval time.Duration `yaml:"interval"`
}

// HealthConfig configures the health probe endpoint.
type HealthConfig struct {
	Port int `yaml:"port"`
}
