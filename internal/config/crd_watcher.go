// Package config implements CRD watchers for runtime loading/unloading of
// custom Wormsign resources. See Section 2.5 and Section 4 of the project spec.
package config

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"text/template"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	v1alpha1 "github.com/k8s-wormsign/k8s-wormsign/api/v1alpha1"
	celengine "github.com/k8s-wormsign/k8s-wormsign/internal/detector/cel"
	"github.com/k8s-wormsign/k8s-wormsign/internal/filter"
)

// DetectorRegistry manages custom CEL-based detectors at runtime.
type DetectorRegistry interface {
	// RegisterDetector registers a validated custom detector for use in the
	// detection pipeline. The key is namespace/name of the CRD.
	RegisterDetector(key string, detector RegisteredDetector)

	// UnregisterDetector removes a custom detector from the detection pipeline.
	UnregisterDetector(key string)
}

// RegisteredDetector holds a validated and compiled detector ready for use.
type RegisteredDetector struct {
	Name              string
	Namespace         string
	Resource          string
	Condition         string // original CEL expression
	Params            map[string]string
	Severity          v1alpha1.Severity
	Cooldown          string
	NamespaceSelector *metav1.LabelSelector
	Description       string
}

// GathererRegistry manages custom gatherers at runtime.
type GathererRegistry interface {
	// RegisterGatherer registers a validated custom gatherer.
	RegisterGatherer(key string, gatherer RegisteredGatherer)

	// UnregisterGatherer removes a custom gatherer.
	UnregisterGatherer(key string)
}

// RegisteredGatherer holds a validated gatherer ready for use.
type RegisteredGatherer struct {
	Name        string
	Namespace   string
	Description string
	TriggerOn   v1alpha1.GathererTrigger
	Collect     []v1alpha1.CollectAction
}

// SinkRegistry manages custom webhook sinks at runtime.
type SinkRegistry interface {
	// RegisterSink registers a validated custom sink.
	RegisterSink(key string, sink RegisteredSink)

	// UnregisterSink removes a custom sink.
	UnregisterSink(key string)
}

// RegisteredSink holds a validated sink ready for use.
type RegisteredSink struct {
	Name           string
	Namespace      string
	Description    string
	SinkType       v1alpha1.SinkType
	Webhook        *v1alpha1.WebhookConfig
	SeverityFilter []v1alpha1.Severity
	BodyTemplate   *template.Template // pre-parsed template
}

// StatusUpdater persists status condition changes back to the API server.
type StatusUpdater interface {
	UpdateDetectorStatus(ctx context.Context, namespace, name string, status v1alpha1.WormsignDetectorStatus) error
	UpdateGathererStatus(ctx context.Context, namespace, name string, status v1alpha1.WormsignGathererStatus) error
	UpdateSinkStatus(ctx context.Context, namespace, name string, status v1alpha1.WormsignSinkStatus) error
	UpdatePolicyStatus(ctx context.Context, namespace, name string, status v1alpha1.WormsignPolicyStatus) error
}

// CRDWatcher watches all four Wormsign CRD types and manages their lifecycle.
// On create/update it validates the resource and registers it with the
// appropriate registry. On delete it unregisters. Invalid CRDs get
// status.conditions[Ready]=False with a descriptive message while previous
// valid configuration remains active.
type CRDWatcher struct {
	celEvaluator     *celengine.Evaluator
	detectorRegistry DetectorRegistry
	gathererRegistry GathererRegistry
	sinkRegistry     SinkRegistry
	filterEngine     *filter.Engine
	statusUpdater    StatusUpdater
	logger           *slog.Logger

	// mu protects the tracked state maps.
	mu sync.RWMutex
	// Track which CRDs are currently valid and registered, so we can keep
	// previous valid config when an update makes a CRD invalid.
	validDetectors map[string]bool
	validGatherers map[string]bool
	validSinks     map[string]bool
	validPolicies  map[string]bool
	// policyStore tracks all known policy CRDs for rebuilding the filter
	// engine's complete policy set.
	policyStore map[string]v1alpha1.WormsignPolicy
}

// CRDWatcherConfig holds the dependencies for creating a CRDWatcher.
type CRDWatcherConfig struct {
	CELEvaluator     *celengine.Evaluator
	DetectorRegistry DetectorRegistry
	GathererRegistry GathererRegistry
	SinkRegistry     SinkRegistry
	FilterEngine     *filter.Engine
	StatusUpdater    StatusUpdater
	Logger           *slog.Logger
}

// NewCRDWatcher creates a new CRDWatcher with the given dependencies.
func NewCRDWatcher(cfg CRDWatcherConfig) (*CRDWatcher, error) {
	if cfg.CELEvaluator == nil {
		return nil, fmt.Errorf("crd watcher: CEL evaluator must not be nil")
	}
	if cfg.DetectorRegistry == nil {
		return nil, fmt.Errorf("crd watcher: detector registry must not be nil")
	}
	if cfg.GathererRegistry == nil {
		return nil, fmt.Errorf("crd watcher: gatherer registry must not be nil")
	}
	if cfg.SinkRegistry == nil {
		return nil, fmt.Errorf("crd watcher: sink registry must not be nil")
	}
	if cfg.FilterEngine == nil {
		return nil, fmt.Errorf("crd watcher: filter engine must not be nil")
	}
	if cfg.StatusUpdater == nil {
		return nil, fmt.Errorf("crd watcher: status updater must not be nil")
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}

	return &CRDWatcher{
		celEvaluator:     cfg.CELEvaluator,
		detectorRegistry: cfg.DetectorRegistry,
		gathererRegistry: cfg.GathererRegistry,
		sinkRegistry:     cfg.SinkRegistry,
		filterEngine:     cfg.FilterEngine,
		statusUpdater:    cfg.StatusUpdater,
		logger:           cfg.Logger,
		validDetectors:   make(map[string]bool),
		validGatherers:   make(map[string]bool),
		validSinks:       make(map[string]bool),
		validPolicies:    make(map[string]bool),
		policyStore:      make(map[string]v1alpha1.WormsignPolicy),
	}, nil
}

// CRDCounts holds the number of registered CRDs by type.
type CRDCounts struct {
	Detectors int
	Gatherers int
	Sinks     int
	Policies  int
}

// Counts returns the number of currently valid and registered CRDs by type.
// This is useful for readiness checks and metrics.
func (w *CRDWatcher) Counts() CRDCounts {
	w.mu.RLock()
	defer w.mu.RUnlock()

	counts := CRDCounts{}
	for _, v := range w.validDetectors {
		if v {
			counts.Detectors++
		}
	}
	for _, v := range w.validGatherers {
		if v {
			counts.Gatherers++
		}
	}
	for _, v := range w.validSinks {
		if v {
			counts.Sinks++
		}
	}
	for _, v := range w.validPolicies {
		if v {
			counts.Policies++
		}
	}
	return counts
}

// crdKey returns the namespace/name key for a CRD object.
func crdKey(namespace, name string) string {
	if namespace == "" {
		return name
	}
	return namespace + "/" + name
}

// --- WormsignDetector handlers ---

// OnDetectorAdd handles a new WormsignDetector CRD being created.
func (w *CRDWatcher) OnDetectorAdd(ctx context.Context, detector *v1alpha1.WormsignDetector) {
	w.handleDetector(ctx, detector)
}

// OnDetectorUpdate handles a WormsignDetector CRD being modified.
func (w *CRDWatcher) OnDetectorUpdate(ctx context.Context, detector *v1alpha1.WormsignDetector) {
	w.handleDetector(ctx, detector)
}

// OnDetectorDelete handles a WormsignDetector CRD being deleted.
func (w *CRDWatcher) OnDetectorDelete(key string) {
	w.mu.Lock()
	delete(w.validDetectors, key)
	w.mu.Unlock()

	w.detectorRegistry.UnregisterDetector(key)
	w.logger.Info("unregistered custom detector",
		"key", key,
	)
}

func (w *CRDWatcher) handleDetector(ctx context.Context, detector *v1alpha1.WormsignDetector) {
	key := crdKey(detector.Namespace, detector.Name)
	logger := w.logger.With("detector", key)

	// Validate: resource field is required.
	if detector.Spec.Resource == "" {
		w.setDetectorNotReady(ctx, detector, "spec.resource must not be empty")
		logger.Warn("invalid detector: empty resource field")
		return
	}

	// Validate: condition (CEL expression) is required.
	if detector.Spec.Condition == "" {
		w.setDetectorNotReady(ctx, detector, "spec.condition must not be empty")
		logger.Warn("invalid detector: empty condition field")
		return
	}

	// Validate: compile the CEL expression.
	_, err := w.celEvaluator.Compile(detector.Spec.Condition)
	if err != nil {
		msg := fmt.Sprintf("CEL expression failed to compile: %v", err)
		w.setDetectorNotReady(ctx, detector, msg)
		logger.Warn("invalid detector: CEL compilation failed",
			"condition", detector.Spec.Condition,
			"error", err,
		)
		return
	}

	// Validate: severity must be valid.
	if detector.Spec.Severity != v1alpha1.SeverityCritical &&
		detector.Spec.Severity != v1alpha1.SeverityWarning &&
		detector.Spec.Severity != v1alpha1.SeverityInfo {
		msg := fmt.Sprintf("invalid severity %q: must be critical, warning, or info", detector.Spec.Severity)
		w.setDetectorNotReady(ctx, detector, msg)
		logger.Warn("invalid detector: bad severity", "severity", detector.Spec.Severity)
		return
	}

	// Validation passed — register the detector.
	registered := RegisteredDetector{
		Name:              detector.Name,
		Namespace:         detector.Namespace,
		Resource:          detector.Spec.Resource,
		Condition:         detector.Spec.Condition,
		Params:            detector.Spec.Params,
		Severity:          detector.Spec.Severity,
		Cooldown:          detector.Spec.Cooldown,
		NamespaceSelector: detector.Spec.NamespaceSelector,
		Description:       detector.Spec.Description,
	}
	w.detectorRegistry.RegisterDetector(key, registered)

	w.mu.Lock()
	w.validDetectors[key] = true
	w.mu.Unlock()

	// Set status Ready=True.
	w.setDetectorReady(ctx, detector)
	logger.Info("registered custom detector",
		"resource", detector.Spec.Resource,
		"severity", detector.Spec.Severity,
	)
}

func (w *CRDWatcher) setDetectorReady(ctx context.Context, detector *v1alpha1.WormsignDetector) {
	status := detector.Status
	setCondition(&status.Conditions, v1alpha1.ConditionReady, metav1.ConditionTrue, "Validated", "CEL expression compiled successfully")
	setCondition(&status.Conditions, v1alpha1.ConditionActive, metav1.ConditionTrue, "Registered", "Detector is active in pipeline")
	if err := w.statusUpdater.UpdateDetectorStatus(ctx, detector.Namespace, detector.Name, status); err != nil {
		w.logger.Error("failed to update detector status",
			"detector", crdKey(detector.Namespace, detector.Name),
			"error", err,
		)
	}
}

func (w *CRDWatcher) setDetectorNotReady(ctx context.Context, detector *v1alpha1.WormsignDetector, message string) {
	key := crdKey(detector.Namespace, detector.Name)

	// If we have a previous valid version registered, keep it. Only update
	// status to indicate the new spec is invalid.
	w.mu.RLock()
	wasValid := w.validDetectors[key]
	w.mu.RUnlock()

	status := detector.Status
	setCondition(&status.Conditions, v1alpha1.ConditionReady, metav1.ConditionFalse, "ValidationFailed", message)
	if wasValid {
		setCondition(&status.Conditions, v1alpha1.ConditionActive, metav1.ConditionTrue, "PreviousVersionActive", "Previous valid configuration remains active")
	} else {
		setCondition(&status.Conditions, v1alpha1.ConditionActive, metav1.ConditionFalse, "NotRegistered", "No valid configuration available")
	}
	if err := w.statusUpdater.UpdateDetectorStatus(ctx, detector.Namespace, detector.Name, status); err != nil {
		w.logger.Error("failed to update detector status",
			"detector", key,
			"error", err,
		)
	}
}

// --- WormsignGatherer handlers ---

// OnGathererAdd handles a new WormsignGatherer CRD being created.
func (w *CRDWatcher) OnGathererAdd(ctx context.Context, gatherer *v1alpha1.WormsignGatherer) {
	w.handleGatherer(ctx, gatherer)
}

// OnGathererUpdate handles a WormsignGatherer CRD being modified.
func (w *CRDWatcher) OnGathererUpdate(ctx context.Context, gatherer *v1alpha1.WormsignGatherer) {
	w.handleGatherer(ctx, gatherer)
}

// OnGathererDelete handles a WormsignGatherer CRD being deleted.
func (w *CRDWatcher) OnGathererDelete(key string) {
	w.mu.Lock()
	delete(w.validGatherers, key)
	w.mu.Unlock()

	w.gathererRegistry.UnregisterGatherer(key)
	w.logger.Info("unregistered custom gatherer",
		"key", key,
	)
}

func (w *CRDWatcher) handleGatherer(ctx context.Context, gatherer *v1alpha1.WormsignGatherer) {
	key := crdKey(gatherer.Namespace, gatherer.Name)
	logger := w.logger.With("gatherer", key)

	// Validate: triggerOn must have at least one trigger criterion.
	if len(gatherer.Spec.TriggerOn.ResourceKinds) == 0 && len(gatherer.Spec.TriggerOn.Detectors) == 0 {
		w.setGathererNotReady(ctx, gatherer, "spec.triggerOn must specify at least one resourceKind or detector")
		logger.Warn("invalid gatherer: empty triggerOn")
		return
	}

	// Validate: collect must have at least one action.
	if len(gatherer.Spec.Collect) == 0 {
		w.setGathererNotReady(ctx, gatherer, "spec.collect must contain at least one collection action")
		logger.Warn("invalid gatherer: empty collect actions")
		return
	}

	// Validate each collect action.
	for i, action := range gatherer.Spec.Collect {
		if err := validateCollectAction(action); err != nil {
			msg := fmt.Sprintf("spec.collect[%d]: %v", i, err)
			w.setGathererNotReady(ctx, gatherer, msg)
			logger.Warn("invalid gatherer: collect action validation failed",
				"index", i,
				"error", err,
			)
			return
		}
	}

	// Validation passed — register the gatherer.
	registered := RegisteredGatherer{
		Name:        gatherer.Name,
		Namespace:   gatherer.Namespace,
		Description: gatherer.Spec.Description,
		TriggerOn:   gatherer.Spec.TriggerOn,
		Collect:     gatherer.Spec.Collect,
	}
	w.gathererRegistry.RegisterGatherer(key, registered)

	w.mu.Lock()
	w.validGatherers[key] = true
	w.mu.Unlock()

	w.setGathererReady(ctx, gatherer)
	logger.Info("registered custom gatherer",
		"collectActions", len(gatherer.Spec.Collect),
	)
}

// validateCollectAction validates a single collect action from a gatherer spec.
func validateCollectAction(action v1alpha1.CollectAction) error {
	switch action.Type {
	case v1alpha1.CollectTypeResource:
		if action.APIVersion == "" {
			return fmt.Errorf("apiVersion is required for resource collection type")
		}
		if action.Resource == "" {
			return fmt.Errorf("resource is required for resource collection type")
		}
		if !action.ListAll && action.Name == "" {
			return fmt.Errorf("name is required for resource collection type when listAll is false")
		}
	case v1alpha1.CollectTypeLogs:
		// Logs collection doesn't require mandatory fields beyond type.
		// Container is optional (defaults to first container).
	default:
		return fmt.Errorf("unknown collection type %q: must be 'resource' or 'logs'", action.Type)
	}
	return nil
}

func (w *CRDWatcher) setGathererReady(ctx context.Context, gatherer *v1alpha1.WormsignGatherer) {
	status := gatherer.Status
	setCondition(&status.Conditions, v1alpha1.ConditionReady, metav1.ConditionTrue, "Validated", "Gatherer configuration validated successfully")
	if err := w.statusUpdater.UpdateGathererStatus(ctx, gatherer.Namespace, gatherer.Name, status); err != nil {
		w.logger.Error("failed to update gatherer status",
			"gatherer", crdKey(gatherer.Namespace, gatherer.Name),
			"error", err,
		)
	}
}

func (w *CRDWatcher) setGathererNotReady(ctx context.Context, gatherer *v1alpha1.WormsignGatherer, message string) {
	key := crdKey(gatherer.Namespace, gatherer.Name)

	w.mu.RLock()
	wasValid := w.validGatherers[key]
	w.mu.RUnlock()

	status := gatherer.Status
	setCondition(&status.Conditions, v1alpha1.ConditionReady, metav1.ConditionFalse, "ValidationFailed", message)
	if wasValid {
		// Keep previous valid configuration active — do not unregister.
	}
	if err := w.statusUpdater.UpdateGathererStatus(ctx, gatherer.Namespace, gatherer.Name, status); err != nil {
		w.logger.Error("failed to update gatherer status",
			"gatherer", key,
			"error", err,
		)
	}
}

// --- WormsignSink handlers ---

// OnSinkAdd handles a new WormsignSink CRD being created.
func (w *CRDWatcher) OnSinkAdd(ctx context.Context, sink *v1alpha1.WormsignSink) {
	w.handleSink(ctx, sink)
}

// OnSinkUpdate handles a WormsignSink CRD being modified.
func (w *CRDWatcher) OnSinkUpdate(ctx context.Context, sink *v1alpha1.WormsignSink) {
	w.handleSink(ctx, sink)
}

// OnSinkDelete handles a WormsignSink CRD being deleted.
func (w *CRDWatcher) OnSinkDelete(key string) {
	w.mu.Lock()
	delete(w.validSinks, key)
	w.mu.Unlock()

	w.sinkRegistry.UnregisterSink(key)
	w.logger.Info("unregistered custom sink",
		"key", key,
	)
}

func (w *CRDWatcher) handleSink(ctx context.Context, sink *v1alpha1.WormsignSink) {
	key := crdKey(sink.Namespace, sink.Name)
	logger := w.logger.With("sink", key)

	// Validate: type must be webhook.
	if sink.Spec.Type != v1alpha1.SinkTypeWebhook {
		msg := fmt.Sprintf("unsupported sink type %q: must be 'webhook'", sink.Spec.Type)
		w.setSinkNotReady(ctx, sink, msg)
		logger.Warn("invalid sink: unsupported type", "type", sink.Spec.Type)
		return
	}

	// Validate: webhook config is required for webhook type.
	if sink.Spec.Webhook == nil {
		w.setSinkNotReady(ctx, sink, "spec.webhook is required when type is 'webhook'")
		logger.Warn("invalid sink: missing webhook config")
		return
	}

	// Validate: URL or URLSecretRef must be specified.
	if sink.Spec.Webhook.URL == "" && sink.Spec.Webhook.URLSecretRef == nil {
		w.setSinkNotReady(ctx, sink, "spec.webhook must specify either url or secretRef")
		logger.Warn("invalid sink: no URL or secretRef")
		return
	}

	// Validate: URL and URLSecretRef cannot both be specified.
	if sink.Spec.Webhook.URL != "" && sink.Spec.Webhook.URLSecretRef != nil {
		w.setSinkNotReady(ctx, sink, "spec.webhook must specify either url or secretRef, not both")
		logger.Warn("invalid sink: both URL and secretRef specified")
		return
	}

	// Validate: severity filter values.
	for _, sev := range sink.Spec.SeverityFilter {
		if sev != v1alpha1.SeverityCritical && sev != v1alpha1.SeverityWarning && sev != v1alpha1.SeverityInfo {
			msg := fmt.Sprintf("invalid severity %q in severityFilter: must be critical, warning, or info", sev)
			w.setSinkNotReady(ctx, sink, msg)
			logger.Warn("invalid sink: bad severity in filter", "severity", sev)
			return
		}
	}

	// Validate and parse the body template if provided.
	var parsedTemplate *template.Template
	if sink.Spec.Webhook.BodyTemplate != "" {
		var err error
		parsedTemplate, err = template.New(key).Parse(sink.Spec.Webhook.BodyTemplate)
		if err != nil {
			msg := fmt.Sprintf("body template parse error: %v", err)
			w.setSinkNotReady(ctx, sink, msg)
			logger.Warn("invalid sink: template parse failed",
				"error", err,
			)
			return
		}
	}

	// Validation passed — register the sink.
	registered := RegisteredSink{
		Name:           sink.Name,
		Namespace:      sink.Namespace,
		Description:    sink.Spec.Description,
		SinkType:       sink.Spec.Type,
		Webhook:        sink.Spec.Webhook,
		SeverityFilter: sink.Spec.SeverityFilter,
		BodyTemplate:   parsedTemplate,
	}
	w.sinkRegistry.RegisterSink(key, registered)

	w.mu.Lock()
	w.validSinks[key] = true
	w.mu.Unlock()

	w.setSinkReady(ctx, sink)
	logger.Info("registered custom sink",
		"type", sink.Spec.Type,
	)
}

func (w *CRDWatcher) setSinkReady(ctx context.Context, sink *v1alpha1.WormsignSink) {
	status := sink.Status
	setCondition(&status.Conditions, v1alpha1.ConditionReady, metav1.ConditionTrue, "Validated", "Sink configuration validated successfully")
	if err := w.statusUpdater.UpdateSinkStatus(ctx, sink.Namespace, sink.Name, status); err != nil {
		w.logger.Error("failed to update sink status",
			"sink", crdKey(sink.Namespace, sink.Name),
			"error", err,
		)
	}
}

func (w *CRDWatcher) setSinkNotReady(ctx context.Context, sink *v1alpha1.WormsignSink, message string) {
	key := crdKey(sink.Namespace, sink.Name)

	w.mu.RLock()
	wasValid := w.validSinks[key]
	w.mu.RUnlock()

	status := sink.Status
	setCondition(&status.Conditions, v1alpha1.ConditionReady, metav1.ConditionFalse, "ValidationFailed", message)
	if wasValid {
		// Keep previous valid configuration active — do not unregister.
	}
	if err := w.statusUpdater.UpdateSinkStatus(ctx, sink.Namespace, sink.Name, status); err != nil {
		w.logger.Error("failed to update sink status",
			"sink", key,
			"error", err,
		)
	}
}

// --- WormsignPolicy handlers ---

// OnPolicyAdd handles a new WormsignPolicy CRD being created.
func (w *CRDWatcher) OnPolicyAdd(ctx context.Context, policy *v1alpha1.WormsignPolicy) {
	w.handlePolicy(ctx, policy)
}

// OnPolicyUpdate handles a WormsignPolicy CRD being modified.
func (w *CRDWatcher) OnPolicyUpdate(ctx context.Context, policy *v1alpha1.WormsignPolicy) {
	w.handlePolicy(ctx, policy)
}

// OnPolicyDelete handles a WormsignPolicy CRD being deleted.
func (w *CRDWatcher) OnPolicyDelete(key string) {
	w.mu.Lock()
	delete(w.validPolicies, key)
	delete(w.policyStore, key)
	w.mu.Unlock()

	// Rebuild the filter engine policy set without the deleted policy.
	w.rebuildPolicies()
	w.logger.Info("unregistered policy",
		"key", key,
	)
}

func (w *CRDWatcher) handlePolicy(ctx context.Context, policy *v1alpha1.WormsignPolicy) {
	key := crdKey(policy.Namespace, policy.Name)
	logger := w.logger.With("policy", key)

	// Validate: action must be Exclude or Suppress.
	if policy.Spec.Action != v1alpha1.PolicyActionExclude && policy.Spec.Action != v1alpha1.PolicyActionSuppress {
		msg := fmt.Sprintf("invalid action %q: must be 'Exclude' or 'Suppress'", policy.Spec.Action)
		w.setPolicyNotReady(ctx, policy, msg)
		logger.Warn("invalid policy: bad action", "action", policy.Spec.Action)
		return
	}

	// Validate: schedule is only valid for Suppress action.
	if policy.Spec.Schedule != nil && policy.Spec.Action != v1alpha1.PolicyActionSuppress {
		w.setPolicyNotReady(ctx, policy, "schedule is only valid when action is 'Suppress'")
		logger.Warn("invalid policy: schedule on non-Suppress action")
		return
	}

	// Validate: schedule end must be after start.
	if policy.Spec.Schedule != nil {
		if !policy.Spec.Schedule.End.After(policy.Spec.Schedule.Start.Time) {
			w.setPolicyNotReady(ctx, policy, "schedule.end must be after schedule.start")
			logger.Warn("invalid policy: schedule end before start")
			return
		}
	}

	// Validate: owner name glob patterns.
	if policy.Spec.Match != nil {
		for _, pattern := range policy.Spec.Match.OwnerNames {
			if pattern == "" {
				w.setPolicyNotReady(ctx, policy, "ownerNames patterns must not be empty strings")
				logger.Warn("invalid policy: empty owner name pattern")
				return
			}
		}
	}

	// Validation passed — store, mark as valid, and rebuild policies.
	w.mu.Lock()
	w.policyStore[key] = *policy
	w.validPolicies[key] = true
	w.mu.Unlock()

	w.rebuildPolicies()

	w.setPolicyReady(ctx, policy)
	logger.Info("registered policy",
		"action", policy.Spec.Action,
	)
}

func (w *CRDWatcher) setPolicyReady(ctx context.Context, policy *v1alpha1.WormsignPolicy) {
	status := policy.Status
	setCondition(&status.Conditions, v1alpha1.ConditionReady, metav1.ConditionTrue, "Validated", "Policy configuration validated successfully")
	setCondition(&status.Conditions, v1alpha1.ConditionActive, metav1.ConditionTrue, "Active", "Policy is active")
	if err := w.statusUpdater.UpdatePolicyStatus(ctx, policy.Namespace, policy.Name, status); err != nil {
		w.logger.Error("failed to update policy status",
			"policy", crdKey(policy.Namespace, policy.Name),
			"error", err,
		)
	}
}

func (w *CRDWatcher) setPolicyNotReady(ctx context.Context, policy *v1alpha1.WormsignPolicy, message string) {
	key := crdKey(policy.Namespace, policy.Name)

	w.mu.RLock()
	wasValid := w.validPolicies[key]
	w.mu.RUnlock()

	status := policy.Status
	setCondition(&status.Conditions, v1alpha1.ConditionReady, metav1.ConditionFalse, "ValidationFailed", message)
	if wasValid {
		setCondition(&status.Conditions, v1alpha1.ConditionActive, metav1.ConditionTrue, "PreviousVersionActive", "Previous valid configuration remains active")
	} else {
		setCondition(&status.Conditions, v1alpha1.ConditionActive, metav1.ConditionFalse, "NotRegistered", "No valid configuration available")
	}
	if err := w.statusUpdater.UpdatePolicyStatus(ctx, policy.Namespace, policy.Name, status); err != nil {
		w.logger.Error("failed to update policy status",
			"policy", key,
			"error", err,
		)
	}
}

// --- Condition helpers ---

// setCondition sets a condition on the conditions slice. If the condition type
// already exists, it updates it; otherwise, it appends a new condition.
func setCondition(conditions *[]metav1.Condition, condType string, status metav1.ConditionStatus, reason, message string) {
	now := metav1.NewTime(time.Now())
	for i, c := range *conditions {
		if c.Type == condType {
			(*conditions)[i].Status = status
			(*conditions)[i].Reason = reason
			(*conditions)[i].Message = message
			(*conditions)[i].LastTransitionTime = now
			return
		}
	}
	*conditions = append(*conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: now,
	})
}

// --- Policy rebuild ---

// rebuildPolicies rebuilds the complete set of filter.Policy objects from
// all known valid policies and updates the filter engine. Policies are sorted
// by namespace/name for deterministic evaluation order.
func (w *CRDWatcher) rebuildPolicies() {
	w.mu.RLock()
	// Collect valid policy keys and sort for deterministic order.
	keys := make([]string, 0, len(w.policyStore))
	for key := range w.policyStore {
		if w.validPolicies[key] {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	policies := make([]filter.Policy, 0, len(keys))
	for _, key := range keys {
		p := w.policyStore[key]
		fp := convertToFilterPolicy(&p)
		policies = append(policies, fp)
	}
	w.mu.RUnlock()

	w.filterEngine.SetPolicies(policies)
	w.logger.Debug("rebuilt filter policies",
		"count", len(policies),
	)
}

// convertToFilterPolicy converts a v1alpha1.WormsignPolicy to filter.Policy.
func convertToFilterPolicy(policy *v1alpha1.WormsignPolicy) filter.Policy {
	fp := filter.Policy{
		Name:      policy.Name,
		Namespace: policy.Namespace,
		Action:    filter.PolicyAction(policy.Spec.Action),
		Detectors: policy.Spec.Detectors,
	}

	// Convert namespace selector.
	if policy.Spec.NamespaceSelector != nil {
		fp.NamespaceSelector = convertLabelSelector(policy.Spec.NamespaceSelector)
	}

	// Convert resource selector.
	if policy.Spec.Match != nil {
		if policy.Spec.Match.ResourceSelector != nil {
			fp.ResourceSelector = convertLabelSelector(policy.Spec.Match.ResourceSelector)
		}
		fp.OwnerNames = policy.Spec.Match.OwnerNames
	}

	// Convert schedule.
	if policy.Spec.Schedule != nil {
		fp.Schedule = &filter.PolicySchedule{
			Start: policy.Spec.Schedule.Start.Time,
			End:   policy.Spec.Schedule.End.Time,
		}
	}

	return fp
}

// convertLabelSelector converts a metav1.LabelSelector to filter.LabelSelector.
func convertLabelSelector(selector *metav1.LabelSelector) *filter.LabelSelector {
	if selector == nil {
		return nil
	}
	fs := &filter.LabelSelector{
		MatchLabels: selector.MatchLabels,
	}
	for _, expr := range selector.MatchExpressions {
		fs.MatchExpressions = append(fs.MatchExpressions, filter.LabelSelectorRequirement{
			Key:      expr.Key,
			Operator: filter.SelectorOperator(string(expr.Operator)),
			Values:   expr.Values,
		})
	}
	return fs
}
