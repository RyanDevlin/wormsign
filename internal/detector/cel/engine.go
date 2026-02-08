// Package cel implements a CEL-based custom detector engine for evaluating
// WormsignDetector CRD expressions against Kubernetes resource objects.
// See Section 4.2 of the project spec and DECISIONS.md D4.
package cel

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/common/types/ref"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"

	v1alpha1 "github.com/k8s-wormsign/k8s-wormsign/api/v1alpha1"
	"github.com/k8s-wormsign/k8s-wormsign/internal/detector"
	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

// DefaultCostLimit is the maximum CEL evaluation cost budget per expression.
const DefaultCostLimit uint64 = 1000

// defaultCooldown is the default cooldown duration when not specified in the CRD.
const defaultCooldown = 30 * time.Minute

// supportedResources maps resource type strings to the Kind used in ResourceRef.
var supportedResources = map[string]string{
	"pods":                     "Pod",
	"nodes":                    "Node",
	"persistentvolumeclaims":   "PersistentVolumeClaim",
	"jobs":                     "Job",
	"deployments":              "Deployment",
	"statefulsets":             "StatefulSet",
	"daemonsets":               "DaemonSet",
	"replicasets":              "ReplicaSet",
	"namespaces":               "Namespace",
}

// CompiledDetector holds a compiled CEL program for a single WormsignDetector CRD,
// along with its runtime metadata needed for evaluation and event emission.
type CompiledDetector struct {
	// Name is the namespaced name of the WormsignDetector CRD (namespace/name).
	Name string

	// Program is the compiled CEL program ready for evaluation.
	Program cel.Program

	// Env is the CEL environment used for compilation (retained for re-compilation).
	Env *cel.Env

	// Severity is the severity level for emitted fault events.
	Severity model.Severity

	// ResourceKind is the Kubernetes kind being watched (e.g., "Pod").
	ResourceKind string

	// ResourceType is the plural resource type from the CRD spec (e.g., "pods").
	ResourceType string

	// Params are the user-defined parameters from the CRD spec.
	Params map[string]string

	// NamespaceSelector restricts which namespaces the detector watches.
	// Nil means the detector only watches its own namespace.
	NamespaceSelector labels.Selector

	// DetectorNamespace is the namespace the CRD was created in.
	DetectorNamespace string

	// Cooldown is the minimum duration between events for the same resource.
	Cooldown time.Duration

	// base provides cooldown and deduplication logic.
	base *detector.BaseDetector
}

// CELDetectorEngine manages the lifecycle of CEL-based custom detectors defined
// by WormsignDetector CRDs. It handles compilation, evaluation, namespace matching,
// and CRD lifecycle events (create, update, delete).
type CELDetectorEngine struct {
	mu        sync.RWMutex
	detectors map[string]*CompiledDetector // key: namespace/name

	costLimit uint64
	callback  detector.EventCallback
	logger    *slog.Logger

	// systemNamespace is the controller's namespace (e.g., "wormsign-system").
	// CRDs in this namespace can use namespaceSelector for cross-namespace detection.
	systemNamespace string

	// nowFunc allows tests to control the clock.
	nowFunc func() time.Time
}

// EngineConfig holds configuration for creating a CELDetectorEngine.
type EngineConfig struct {
	// CostLimit is the maximum CEL evaluation cost budget. 0 uses DefaultCostLimit.
	CostLimit uint64

	// Callback receives emitted fault events from all CEL detectors.
	Callback detector.EventCallback

	// Logger for structured logging.
	Logger *slog.Logger

	// SystemNamespace is the controller's namespace (e.g., "wormsign-system").
	SystemNamespace string
}

// NewCELDetectorEngine creates a new CEL detector engine.
func NewCELDetectorEngine(cfg EngineConfig) (*CELDetectorEngine, error) {
	if cfg.Callback == nil {
		return nil, fmt.Errorf("callback must not be nil")
	}
	if cfg.SystemNamespace == "" {
		return nil, fmt.Errorf("system namespace must not be empty")
	}

	costLimit := cfg.CostLimit
	if costLimit == 0 {
		costLimit = DefaultCostLimit
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &CELDetectorEngine{
		detectors:       make(map[string]*CompiledDetector),
		costLimit:       costLimit,
		callback:        cfg.Callback,
		logger:          logger,
		systemNamespace: cfg.SystemNamespace,
		nowFunc:         time.Now,
	}, nil
}

// SetNowFunc overrides the clock for testing.
func (e *CELDetectorEngine) SetNowFunc(fn func() time.Time) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.nowFunc = fn
}

// now returns the current time using the test hook if set.
func (e *CELDetectorEngine) now() time.Time {
	if e.nowFunc != nil {
		return e.nowFunc()
	}
	return time.Now()
}

// newCELEnv creates a new CEL environment with the standard Wormsign variables
// and custom functions: now(), duration(), timestamp(), params.
func newCELEnv(resourceVar string) (*cel.Env, error) {
	return cel.NewEnv(
		// Resource object exposed as a dynamic-typed map.
		// This allows CEL expressions to access arbitrary fields from
		// Kubernetes resource objects without requiring protobuf definitions.
		cel.Variable(resourceVar, cel.MapType(cel.StringType, cel.DynType)),

		// Params from the CRD spec, available as a string-keyed map.
		cel.Variable("params", cel.MapType(cel.StringType, cel.StringType)),

		// now() returns the current timestamp.
		cel.Function("now",
			cel.Overload("now_zero",
				[]*cel.Type{},
				cel.TimestampType,
				cel.FunctionBinding(func(args ...ref.Val) ref.Val {
					return types.Timestamp{Time: time.Now()}
				}),
			),
		),
	)
}

// resourceVarName returns the CEL variable name for a given resource type.
// e.g., "pods" -> "pod", "nodes" -> "node", "persistentvolumeclaims" -> "pvc"
func resourceVarName(resourceType string) string {
	switch resourceType {
	case "pods":
		return "pod"
	case "nodes":
		return "node"
	case "persistentvolumeclaims":
		return "pvc"
	case "jobs":
		return "job"
	case "deployments":
		return "deployment"
	case "statefulsets":
		return "statefulset"
	case "daemonsets":
		return "daemonset"
	case "replicasets":
		return "replicaset"
	case "namespaces":
		return "namespace"
	default:
		return resourceType
	}
}

// LoadDetectorResult contains the outcome of loading a WormsignDetector CRD.
type LoadDetectorResult struct {
	// Ready indicates the CRD was compiled and loaded successfully.
	Ready bool

	// ReadyMessage describes the Ready condition.
	ReadyMessage string

	// MatchedNamespaces is set if the detector has a namespace selector.
	MatchedNamespaces int32
}

// LoadDetector compiles and registers a WormsignDetector CRD. If a detector
// with the same namespace/name already exists, it is replaced.
// Returns the result for status condition updates.
func (e *CELDetectorEngine) LoadDetector(ctx context.Context, wd *v1alpha1.WormsignDetector) (*LoadDetectorResult, error) {
	if wd == nil {
		return nil, fmt.Errorf("WormsignDetector must not be nil")
	}

	key := detectorKey(wd.Namespace, wd.Name)
	logger := e.logger.With(
		"detector", key,
		"resource", wd.Spec.Resource,
	)

	// Validate resource type.
	resourceKind, ok := supportedResources[wd.Spec.Resource]
	if !ok {
		msg := fmt.Sprintf("unsupported resource type %q", wd.Spec.Resource)
		logger.Warn("failed to load detector", "error", msg)
		return &LoadDetectorResult{
			Ready:        false,
			ReadyMessage: msg,
		}, nil
	}

	// Validate severity.
	severity := model.Severity(wd.Spec.Severity)
	if !severity.IsValid() {
		msg := fmt.Sprintf("invalid severity %q", wd.Spec.Severity)
		logger.Warn("failed to load detector", "error", msg)
		return &LoadDetectorResult{
			Ready:        false,
			ReadyMessage: msg,
		}, nil
	}

	// Parse cooldown.
	cooldown := defaultCooldown
	if wd.Spec.Cooldown != "" {
		parsed, err := time.ParseDuration(wd.Spec.Cooldown)
		if err != nil {
			msg := fmt.Sprintf("invalid cooldown %q: %v", wd.Spec.Cooldown, err)
			logger.Warn("failed to load detector", "error", msg)
			return &LoadDetectorResult{
				Ready:        false,
				ReadyMessage: msg,
			}, nil
		}
		if parsed <= 0 {
			msg := fmt.Sprintf("cooldown must be positive, got %s", wd.Spec.Cooldown)
			logger.Warn("failed to load detector", "error", msg)
			return &LoadDetectorResult{
				Ready:        false,
				ReadyMessage: msg,
			}, nil
		}
		cooldown = parsed
	}

	// Validate condition is not empty.
	if wd.Spec.Condition == "" {
		msg := "CEL condition must not be empty"
		logger.Warn("failed to load detector", "error", msg)
		return &LoadDetectorResult{
			Ready:        false,
			ReadyMessage: msg,
		}, nil
	}

	// Parse namespace selector.
	var nsSelector labels.Selector
	if wd.Spec.NamespaceSelector != nil {
		// Only detectors in the system namespace can use namespace selectors.
		if wd.Namespace != e.systemNamespace {
			msg := fmt.Sprintf("namespaceSelector is only allowed for detectors in the %s namespace", e.systemNamespace)
			logger.Warn("failed to load detector", "error", msg)
			return &LoadDetectorResult{
				Ready:        false,
				ReadyMessage: msg,
			}, nil
		}

		selector, err := metav1.LabelSelectorAsSelector(wd.Spec.NamespaceSelector)
		if err != nil {
			msg := fmt.Sprintf("invalid namespaceSelector: %v", err)
			logger.Warn("failed to load detector", "error", msg)
			return &LoadDetectorResult{
				Ready:        false,
				ReadyMessage: msg,
			}, nil
		}
		nsSelector = selector
	}

	// Create CEL environment.
	varName := resourceVarName(wd.Spec.Resource)
	env, err := newCELEnv(varName)
	if err != nil {
		return nil, fmt.Errorf("creating CEL environment for detector %s: %w", key, err)
	}

	// Compile the expression.
	ast, issues := env.Compile(wd.Spec.Condition)
	if issues != nil && issues.Err() != nil {
		msg := fmt.Sprintf("CEL compilation error: %v", issues.Err())
		logger.Warn("failed to compile detector expression", "error", issues.Err())
		return &LoadDetectorResult{
			Ready:        false,
			ReadyMessage: msg,
		}, nil
	}

	// Verify the output type is boolean.
	if ast.OutputType() != cel.BoolType {
		msg := fmt.Sprintf("CEL expression must return bool, got %s", ast.OutputType())
		logger.Warn("failed to load detector", "error", msg)
		return &LoadDetectorResult{
			Ready:        false,
			ReadyMessage: msg,
		}, nil
	}

	// Create the program with cost limit.
	prog, err := env.Program(ast, cel.CostLimit(e.costLimit))
	if err != nil {
		msg := fmt.Sprintf("CEL program creation error: %v", err)
		logger.Warn("failed to create detector program", "error", err)
		return &LoadDetectorResult{
			Ready:        false,
			ReadyMessage: msg,
		}, nil
	}

	// Create base detector for cooldown/dedup.
	base, err := detector.NewBaseDetector(detector.BaseDetectorConfig{
		Name:     fmt.Sprintf("cel:%s/%s", wd.Namespace, wd.Name),
		Severity: severity,
		Cooldown: cooldown,
		Callback: e.callback,
		Logger:   logger,
	})
	if err != nil {
		return nil, fmt.Errorf("creating base detector for %s: %w", key, err)
	}

	compiled := &CompiledDetector{
		Name:              key,
		Program:           prog,
		Env:               env,
		Severity:          severity,
		ResourceKind:      resourceKind,
		ResourceType:      wd.Spec.Resource,
		Params:            wd.Spec.Params,
		NamespaceSelector: nsSelector,
		DetectorNamespace: wd.Namespace,
		Cooldown:          cooldown,
		base:              base,
	}

	// Register the detector.
	e.mu.Lock()
	e.detectors[key] = compiled
	e.mu.Unlock()

	logger.Info("detector loaded",
		"severity", string(severity),
		"cooldown", cooldown,
		"has_namespace_selector", nsSelector != nil,
	)

	result := &LoadDetectorResult{
		Ready:        true,
		ReadyMessage: "CEL expression compiled successfully",
	}

	return result, nil
}

// UnloadDetector removes a previously loaded detector by namespace/name.
// Returns true if the detector existed and was removed.
func (e *CELDetectorEngine) UnloadDetector(namespace, name string) bool {
	key := detectorKey(namespace, name)

	e.mu.Lock()
	_, existed := e.detectors[key]
	delete(e.detectors, key)
	e.mu.Unlock()

	if existed {
		e.logger.Info("detector unloaded", "detector", key)
	}
	return existed
}

// EvaluateResource evaluates all loaded CEL detectors against a Kubernetes
// resource object represented as a map. The resource is evaluated against
// detectors matching the resource type and namespace.
//
// resourceType is the plural resource name (e.g., "pods").
// namespace is the namespace of the resource (empty for cluster-scoped).
// name is the resource name.
// uid is the resource UID.
// resourceData is the resource represented as a map[string]any.
// resourceLabels are the resource's labels for event propagation.
// namespaceLabels are the labels on the resource's namespace (for namespace selector matching).
//
// Returns the number of fault events emitted.
func (e *CELDetectorEngine) EvaluateResource(
	resourceType string,
	namespace string,
	name string,
	uid string,
	resourceData map[string]any,
	resourceLabels map[string]string,
	namespaceLabels map[string]string,
) int {
	e.mu.RLock()
	// Take a snapshot of matching detectors to avoid holding the lock during eval.
	var matching []*CompiledDetector
	for _, cd := range e.detectors {
		if cd.ResourceType == resourceType {
			matching = append(matching, cd)
		}
	}
	e.mu.RUnlock()

	if len(matching) == 0 {
		return 0
	}

	emitted := 0
	for _, cd := range matching {
		if !e.matchesNamespace(cd, namespace, namespaceLabels) {
			continue
		}

		result, err := e.evaluateDetector(cd, resourceData)
		if err != nil {
			e.logger.Warn("CEL evaluation error",
				"detector", cd.Name,
				"resource", fmt.Sprintf("%s/%s/%s", cd.ResourceKind, namespace, name),
				"error", err,
			)
			continue
		}

		if result {
			ref := model.ResourceRef{
				Kind:      cd.ResourceKind,
				Namespace: namespace,
				Name:      name,
				UID:       uid,
			}
			description := fmt.Sprintf("CEL detector %s triggered for %s/%s in namespace %s",
				cd.Name, cd.ResourceKind, name, namespace)

			if cd.base.Emit(ref, description, resourceLabels, map[string]string{
				"cel_detector": cd.Name,
			}) {
				emitted++
			}
		}
	}

	return emitted
}

// matchesNamespace checks whether a resource in the given namespace matches
// the detector's namespace scope rules.
func (e *CELDetectorEngine) matchesNamespace(cd *CompiledDetector, resourceNamespace string, namespaceLabels map[string]string) bool {
	// Detectors with a namespace selector (only allowed in system namespace)
	// match if the namespace labels match the selector.
	if cd.NamespaceSelector != nil {
		if namespaceLabels == nil {
			return false
		}
		return cd.NamespaceSelector.Matches(labels.Set(namespaceLabels))
	}

	// Detectors without a namespace selector only watch their own namespace.
	return cd.DetectorNamespace == resourceNamespace
}

// evaluateDetector runs the CEL program for a single detector against resource data.
func (e *CELDetectorEngine) evaluateDetector(cd *CompiledDetector, resourceData map[string]any) (bool, error) {
	varName := resourceVarName(cd.ResourceType)

	// Build activation variables.
	params := cd.Params
	if params == nil {
		params = make(map[string]string)
	}

	vars := map[string]any{
		varName:  resourceData,
		"params": params,
	}

	result, _, err := cd.Program.Eval(vars)
	if err != nil {
		return false, fmt.Errorf("evaluating CEL expression: %w", err)
	}

	boolVal, ok := result.Value().(bool)
	if !ok {
		return false, fmt.Errorf("CEL expression returned %T, expected bool", result.Value())
	}

	return boolVal, nil
}

// GetDetector returns a compiled detector by namespace/name, or nil if not found.
func (e *CELDetectorEngine) GetDetector(namespace, name string) *CompiledDetector {
	key := detectorKey(namespace, name)
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.detectors[key]
}

// DetectorCount returns the number of loaded detectors.
func (e *CELDetectorEngine) DetectorCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.detectors)
}

// DetectorNames returns the names of all loaded detectors.
func (e *CELDetectorEngine) DetectorNames() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	names := make([]string, 0, len(e.detectors))
	for name := range e.detectors {
		names = append(names, name)
	}
	return names
}

// BuildStatusConditions creates the metav1.Condition entries for a WormsignDetector
// based on the load result.
func BuildStatusConditions(result *LoadDetectorResult, generation int64) []metav1.Condition {
	now := metav1.Now()

	readyStatus := metav1.ConditionFalse
	if result.Ready {
		readyStatus = metav1.ConditionTrue
	}

	conditions := []metav1.Condition{
		{
			Type:               v1alpha1.ConditionReady,
			Status:             readyStatus,
			ObservedGeneration: generation,
			LastTransitionTime: now,
			Reason:             conditionReason(result.Ready),
			Message:            result.ReadyMessage,
		},
		{
			Type:               v1alpha1.ConditionValid,
			Status:             readyStatus,
			ObservedGeneration: generation,
			LastTransitionTime: now,
			Reason:             conditionReason(result.Ready),
			Message:            result.ReadyMessage,
		},
	}

	if result.Ready {
		conditions = append(conditions, metav1.Condition{
			Type:               v1alpha1.ConditionActive,
			Status:             metav1.ConditionTrue,
			ObservedGeneration: generation,
			LastTransitionTime: now,
			Reason:             "DetectorActive",
			Message:            "Detector is actively watching resources",
		})
	}

	return conditions
}

// conditionReason returns a CamelCase reason string for a condition status.
func conditionReason(ready bool) string {
	if ready {
		return "CompilationSucceeded"
	}
	return "CompilationFailed"
}

// detectorKey builds the lookup key for a detector from namespace and name.
func detectorKey(namespace, name string) string {
	return namespace + "/" + name
}
