package filter

import (
	"log/slog"
	"testing"
	"time"
)

// silentLogger returns a logger that discards all output.
func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(
		devNull{},
		&slog.HandlerOptions{Level: slog.LevelError + 1},
	))
}

type devNull struct{}

func (devNull) Write(p []byte) (int, error) { return len(p), nil }

// newTestEngine creates an engine with the given config, policies, and a fixed
// time function. It fails the test if engine creation returns an error.
func newTestEngine(t *testing.T, config GlobalFilterConfig, policies []Policy, now time.Time) *Engine {
	t.Helper()
	engine, err := NewEngine(config, silentLogger())
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	engine.nowFunc = func() time.Time { return now }
	if len(policies) > 0 {
		engine.SetPolicies(policies)
	}
	return engine
}

// baseInput returns a FilterInput with sensible defaults that doesn't match
// any filter. Tests override specific fields to trigger individual levels.
func baseInput() FilterInput {
	return FilterInput{
		DetectorName: "PodCrashLoop",
		Resource: ResourceMeta{
			Kind:      "Pod",
			Namespace: "production",
			Name:      "web-api-abc123",
			UID:       "uid-pod-1",
			Labels:    map[string]string{"app": "web-api"},
			Annotations: map[string]string{},
		},
		Namespace: NamespaceMeta{
			Name:        "production",
			Labels:      map[string]string{"environment": "prod"},
			Annotations: map[string]string{},
		},
		Owners: nil,
	}
}

// --- NewEngine tests ---

func TestNewEngine_Success(t *testing.T) {
	engine, err := NewEngine(GlobalFilterConfig{
		ExcludeNamespaces: []string{"kube-system", "kube-public"},
	}, silentLogger())
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	if engine == nil {
		t.Fatal("NewEngine() returned nil")
	}
	if len(engine.compiledPatterns) != 2 {
		t.Errorf("compiledPatterns length = %d, want 2", len(engine.compiledPatterns))
	}
}

func TestNewEngine_RegexPattern(t *testing.T) {
	engine, err := NewEngine(GlobalFilterConfig{
		ExcludeNamespaces: []string{".*-sandbox"},
	}, silentLogger())
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	if len(engine.compiledPatterns) != 1 {
		t.Fatalf("compiledPatterns length = %d, want 1", len(engine.compiledPatterns))
	}
	if engine.compiledPatterns[0].exact {
		t.Error("pattern should not be exact for regex")
	}
	if engine.compiledPatterns[0].re == nil {
		t.Error("regex should be compiled")
	}
}

func TestNewEngine_InvalidRegex(t *testing.T) {
	_, err := NewEngine(GlobalFilterConfig{
		ExcludeNamespaces: []string{"[invalid"},
	}, silentLogger())
	if err == nil {
		t.Fatal("expected error for invalid regex, got nil")
	}
}

func TestNewEngine_NilLogger(t *testing.T) {
	engine, err := NewEngine(GlobalFilterConfig{}, nil)
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	if engine.logger == nil {
		t.Error("logger should default to slog.Default(), got nil")
	}
}

func TestNewEngine_EmptyPatternSkipped(t *testing.T) {
	engine, err := NewEngine(GlobalFilterConfig{
		ExcludeNamespaces: []string{"", "kube-system", ""},
	}, silentLogger())
	if err != nil {
		t.Fatalf("NewEngine() error = %v", err)
	}
	if len(engine.compiledPatterns) != 1 {
		t.Errorf("compiledPatterns length = %d, want 1 (empty strings skipped)", len(engine.compiledPatterns))
	}
}

// --- Level 1: Global excludeNamespaces ---

func TestLevel1_ExactNamespaceExclusion(t *testing.T) {
	engine := newTestEngine(t, GlobalFilterConfig{
		ExcludeNamespaces: []string{"kube-system", "kube-public", "kube-node-lease"},
	}, nil, time.Now())

	tests := []struct {
		name      string
		namespace string
		excluded  bool
	}{
		{"match kube-system", "kube-system", true},
		{"match kube-public", "kube-public", true},
		{"match kube-node-lease", "kube-node-lease", true},
		{"no match production", "production", false},
		{"no match default", "default", false},
		{"partial match is not excluded", "kube-system-extra", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := baseInput()
			input.Namespace.Name = tt.namespace
			input.Resource.Namespace = tt.namespace
			result := engine.Evaluate(input)
			if result.Excluded != tt.excluded {
				t.Errorf("Excluded = %v, want %v", result.Excluded, tt.excluded)
			}
			if tt.excluded && result.Reason != ReasonNamespaceGlobal {
				t.Errorf("Reason = %q, want %q", result.Reason, ReasonNamespaceGlobal)
			}
		})
	}
}

func TestLevel1_RegexNamespaceExclusion(t *testing.T) {
	engine := newTestEngine(t, GlobalFilterConfig{
		ExcludeNamespaces: []string{".*-sandbox", "test-.*"},
	}, nil, time.Now())

	tests := []struct {
		name      string
		namespace string
		excluded  bool
	}{
		{"match dev-sandbox", "dev-sandbox", true},
		{"match staging-sandbox", "staging-sandbox", true},
		{"match test-alpha", "test-alpha", true},
		{"match test-beta", "test-beta", true},
		{"no match production", "production", false},
		{"no match sandbox (no prefix dash)", "sandbox", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := baseInput()
			input.Namespace.Name = tt.namespace
			result := engine.Evaluate(input)
			if result.Excluded != tt.excluded {
				t.Errorf("Excluded = %v, want %v", result.Excluded, tt.excluded)
			}
			if tt.excluded && result.Reason != ReasonNamespaceGlobal {
				t.Errorf("Reason = %q, want %q", result.Reason, ReasonNamespaceGlobal)
			}
		})
	}
}

// --- Level 2: Global excludeNamespaceSelector ---

func TestLevel2_NamespaceLabelSelector(t *testing.T) {
	engine := newTestEngine(t, GlobalFilterConfig{
		ExcludeNamespaceSelector: &LabelSelector{
			MatchLabels: map[string]string{"wormsign.io/exclude": "true"},
		},
	}, nil, time.Now())

	tests := []struct {
		name     string
		labels   map[string]string
		excluded bool
	}{
		{
			name:     "matching label",
			labels:   map[string]string{"wormsign.io/exclude": "true"},
			excluded: true,
		},
		{
			name:     "wrong value",
			labels:   map[string]string{"wormsign.io/exclude": "false"},
			excluded: false,
		},
		{
			name:     "label absent",
			labels:   map[string]string{"other": "value"},
			excluded: false,
		},
		{
			name:     "nil labels",
			labels:   nil,
			excluded: false,
		},
		{
			name:     "extra labels still matches",
			labels:   map[string]string{"wormsign.io/exclude": "true", "team": "platform"},
			excluded: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := baseInput()
			input.Namespace.Labels = tt.labels
			result := engine.Evaluate(input)
			if result.Excluded != tt.excluded {
				t.Errorf("Excluded = %v, want %v", result.Excluded, tt.excluded)
			}
			if tt.excluded && result.Reason != ReasonNamespaceLabel {
				t.Errorf("Reason = %q, want %q", result.Reason, ReasonNamespaceLabel)
			}
		})
	}
}

func TestLevel2_NamespaceLabelSelectorWithExpressions(t *testing.T) {
	engine := newTestEngine(t, GlobalFilterConfig{
		ExcludeNamespaceSelector: &LabelSelector{
			MatchExpressions: []LabelSelectorRequirement{
				{
					Key:      "environment",
					Operator: SelectorOpIn,
					Values:   []string{"sandbox", "test"},
				},
			},
		},
	}, nil, time.Now())

	tests := []struct {
		name     string
		labels   map[string]string
		excluded bool
	}{
		{
			name:     "sandbox excluded",
			labels:   map[string]string{"environment": "sandbox"},
			excluded: true,
		},
		{
			name:     "test excluded",
			labels:   map[string]string{"environment": "test"},
			excluded: true,
		},
		{
			name:     "production not excluded",
			labels:   map[string]string{"environment": "production"},
			excluded: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := baseInput()
			input.Namespace.Labels = tt.labels
			result := engine.Evaluate(input)
			if result.Excluded != tt.excluded {
				t.Errorf("Excluded = %v, want %v", result.Excluded, tt.excluded)
			}
		})
	}
}

func TestLevel2_NilSelector(t *testing.T) {
	engine := newTestEngine(t, GlobalFilterConfig{
		ExcludeNamespaceSelector: nil,
	}, nil, time.Now())

	input := baseInput()
	input.Namespace.Labels = map[string]string{"wormsign.io/exclude": "true"}
	result := engine.Evaluate(input)
	if result.Excluded {
		t.Error("should not be excluded when selector is nil")
	}
}

// --- Level 3: WormsignPolicy Suppress with schedule ---

func TestLevel3_SuppressPolicyNoSchedule(t *testing.T) {
	policies := []Policy{
		{
			Name:      "maintenance",
			Namespace: "production",
			Action:    PolicyActionSuppress,
		},
	}
	engine := newTestEngine(t, GlobalFilterConfig{}, policies, time.Now())

	input := baseInput()
	result := engine.Evaluate(input)
	if !result.Excluded {
		t.Error("should be excluded by suppress policy without schedule")
	}
	if result.Reason != ReasonSuppressPolicy {
		t.Errorf("Reason = %q, want %q", result.Reason, ReasonSuppressPolicy)
	}
}

func TestLevel3_SuppressPolicyWithActiveSchedule(t *testing.T) {
	now := time.Date(2026, 2, 8, 23, 0, 0, 0, time.UTC)
	policies := []Policy{
		{
			Name:      "maintenance-window",
			Namespace: "production",
			Action:    PolicyActionSuppress,
			Schedule: &PolicySchedule{
				Start: time.Date(2026, 2, 8, 22, 0, 0, 0, time.UTC),
				End:   time.Date(2026, 2, 9, 2, 0, 0, 0, time.UTC),
			},
		},
	}
	engine := newTestEngine(t, GlobalFilterConfig{}, policies, now)

	input := baseInput()
	result := engine.Evaluate(input)
	if !result.Excluded {
		t.Error("should be excluded during active maintenance window")
	}
	if result.Reason != ReasonSuppressPolicy {
		t.Errorf("Reason = %q, want %q", result.Reason, ReasonSuppressPolicy)
	}
}

func TestLevel3_SuppressPolicyBeforeSchedule(t *testing.T) {
	now := time.Date(2026, 2, 8, 21, 0, 0, 0, time.UTC)
	policies := []Policy{
		{
			Name:      "maintenance-window",
			Namespace: "production",
			Action:    PolicyActionSuppress,
			Schedule: &PolicySchedule{
				Start: time.Date(2026, 2, 8, 22, 0, 0, 0, time.UTC),
				End:   time.Date(2026, 2, 9, 2, 0, 0, 0, time.UTC),
			},
		},
	}
	engine := newTestEngine(t, GlobalFilterConfig{}, policies, now)

	input := baseInput()
	result := engine.Evaluate(input)
	if result.Excluded {
		t.Error("should not be excluded before maintenance window starts")
	}
}

func TestLevel3_SuppressPolicyAfterSchedule(t *testing.T) {
	now := time.Date(2026, 2, 9, 3, 0, 0, 0, time.UTC)
	policies := []Policy{
		{
			Name:      "maintenance-window",
			Namespace: "production",
			Action:    PolicyActionSuppress,
			Schedule: &PolicySchedule{
				Start: time.Date(2026, 2, 8, 22, 0, 0, 0, time.UTC),
				End:   time.Date(2026, 2, 9, 2, 0, 0, 0, time.UTC),
			},
		},
	}
	engine := newTestEngine(t, GlobalFilterConfig{}, policies, now)

	input := baseInput()
	result := engine.Evaluate(input)
	if result.Excluded {
		t.Error("should not be excluded after maintenance window ends")
	}
}

func TestLevel3_SuppressPolicyAtExactStart(t *testing.T) {
	start := time.Date(2026, 2, 8, 22, 0, 0, 0, time.UTC)
	policies := []Policy{
		{
			Name:      "maintenance-window",
			Namespace: "production",
			Action:    PolicyActionSuppress,
			Schedule: &PolicySchedule{
				Start: start,
				End:   time.Date(2026, 2, 9, 2, 0, 0, 0, time.UTC),
			},
		},
	}
	engine := newTestEngine(t, GlobalFilterConfig{}, policies, start)

	input := baseInput()
	result := engine.Evaluate(input)
	if !result.Excluded {
		t.Error("should be excluded at exact start of maintenance window")
	}
}

func TestLevel3_SuppressPolicyAtExactEnd(t *testing.T) {
	end := time.Date(2026, 2, 9, 2, 0, 0, 0, time.UTC)
	policies := []Policy{
		{
			Name:      "maintenance-window",
			Namespace: "production",
			Action:    PolicyActionSuppress,
			Schedule: &PolicySchedule{
				Start: time.Date(2026, 2, 8, 22, 0, 0, 0, time.UTC),
				End:   end,
			},
		},
	}
	engine := newTestEngine(t, GlobalFilterConfig{}, policies, end)

	input := baseInput()
	result := engine.Evaluate(input)
	// now.After(end) is false when now == end, so this should be excluded
	if !result.Excluded {
		t.Error("should be excluded at exact end of maintenance window (end is inclusive)")
	}
}

func TestLevel3_SuppressPolicyWrongNamespace(t *testing.T) {
	policies := []Policy{
		{
			Name:      "maintenance",
			Namespace: "staging",
			Action:    PolicyActionSuppress,
		},
	}
	engine := newTestEngine(t, GlobalFilterConfig{}, policies, time.Now())

	input := baseInput() // namespace is "production"
	result := engine.Evaluate(input)
	if result.Excluded {
		t.Error("suppress policy in different namespace should not match")
	}
}

func TestLevel3_SuppressPolicyWithDetectorScope(t *testing.T) {
	policies := []Policy{
		{
			Name:      "suppress-crashloop-only",
			Namespace: "production",
			Action:    PolicyActionSuppress,
			Detectors: []string{"PodCrashLoop"},
		},
	}
	engine := newTestEngine(t, GlobalFilterConfig{}, policies, time.Now())

	// Matching detector
	input := baseInput()
	input.DetectorName = "PodCrashLoop"
	result := engine.Evaluate(input)
	if !result.Excluded {
		t.Error("should be excluded for matching detector")
	}

	// Non-matching detector
	input.DetectorName = "PodFailed"
	result = engine.Evaluate(input)
	if result.Excluded {
		t.Error("should not be excluded for non-matching detector")
	}
}

func TestLevel3_SuppressPolicyWithNamespaceSelector(t *testing.T) {
	policies := []Policy{
		{
			Name:      "suppress-sandbox",
			Namespace: "wormsign-system",
			Action:    PolicyActionSuppress,
			NamespaceSelector: &LabelSelector{
				MatchLabels: map[string]string{"environment": "sandbox"},
			},
		},
	}
	engine := newTestEngine(t, GlobalFilterConfig{}, policies, time.Now())

	// Matching namespace labels
	input := baseInput()
	input.Namespace.Labels = map[string]string{"environment": "sandbox"}
	result := engine.Evaluate(input)
	if !result.Excluded {
		t.Error("should be excluded by namespace selector on suppress policy")
	}

	// Non-matching namespace labels
	input.Namespace.Labels = map[string]string{"environment": "production"}
	result = engine.Evaluate(input)
	if result.Excluded {
		t.Error("should not be excluded when namespace labels don't match selector")
	}
}

// --- Level 4: Namespace annotation wormsign.io/suppress ---

func TestLevel4_NamespaceSuppressAnnotation(t *testing.T) {
	engine := newTestEngine(t, GlobalFilterConfig{}, nil, time.Now())

	tests := []struct {
		name        string
		annotations map[string]string
		excluded    bool
	}{
		{
			name:        "suppress true",
			annotations: map[string]string{AnnotationSuppress: "true"},
			excluded:    true,
		},
		{
			name:        "suppress TRUE (case insensitive)",
			annotations: map[string]string{AnnotationSuppress: "TRUE"},
			excluded:    true,
		},
		{
			name:        "suppress True (mixed case)",
			annotations: map[string]string{AnnotationSuppress: "True"},
			excluded:    true,
		},
		{
			name:        "suppress false",
			annotations: map[string]string{AnnotationSuppress: "false"},
			excluded:    false,
		},
		{
			name:        "suppress empty",
			annotations: map[string]string{AnnotationSuppress: ""},
			excluded:    false,
		},
		{
			name:        "annotation absent",
			annotations: map[string]string{},
			excluded:    false,
		},
		{
			name:        "nil annotations",
			annotations: nil,
			excluded:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := baseInput()
			input.Namespace.Annotations = tt.annotations
			result := engine.Evaluate(input)
			if result.Excluded != tt.excluded {
				t.Errorf("Excluded = %v, want %v", result.Excluded, tt.excluded)
			}
			if tt.excluded && result.Reason != ReasonNamespaceAnnotation {
				t.Errorf("Reason = %q, want %q", result.Reason, ReasonNamespaceAnnotation)
			}
		})
	}
}

// --- Level 5: Resource annotation wormsign.io/exclude ---

func TestLevel5_ResourceExcludeAnnotation(t *testing.T) {
	engine := newTestEngine(t, GlobalFilterConfig{}, nil, time.Now())

	tests := []struct {
		name        string
		annotations map[string]string
		excluded    bool
	}{
		{
			name:        "exclude true",
			annotations: map[string]string{AnnotationExclude: "true"},
			excluded:    true,
		},
		{
			name:        "exclude TRUE (case insensitive)",
			annotations: map[string]string{AnnotationExclude: "TRUE"},
			excluded:    true,
		},
		{
			name:        "exclude false",
			annotations: map[string]string{AnnotationExclude: "false"},
			excluded:    false,
		},
		{
			name:        "annotation absent",
			annotations: map[string]string{},
			excluded:    false,
		},
		{
			name:        "nil annotations",
			annotations: nil,
			excluded:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := baseInput()
			input.Resource.Annotations = tt.annotations
			result := engine.Evaluate(input)
			if result.Excluded != tt.excluded {
				t.Errorf("Excluded = %v, want %v", result.Excluded, tt.excluded)
			}
			if tt.excluded && result.Reason != ReasonResourceAnnotation {
				t.Errorf("Reason = %q, want %q", result.Reason, ReasonResourceAnnotation)
			}
		})
	}
}

// --- Level 6: Owner annotation wormsign.io/exclude ---

func TestLevel6_OwnerExcludeAnnotation(t *testing.T) {
	engine := newTestEngine(t, GlobalFilterConfig{}, nil, time.Now())

	tests := []struct {
		name     string
		owners   []OwnerMeta
		excluded bool
	}{
		{
			name: "owner has exclude true",
			owners: []OwnerMeta{
				{
					OwnerRef:    OwnerRef{Kind: "Deployment", Name: "web-api", UID: "deploy-uid"},
					Annotations: map[string]string{AnnotationExclude: "true"},
				},
			},
			excluded: true,
		},
		{
			name: "owner has exclude false",
			owners: []OwnerMeta{
				{
					OwnerRef:    OwnerRef{Kind: "Deployment", Name: "web-api", UID: "deploy-uid"},
					Annotations: map[string]string{AnnotationExclude: "false"},
				},
			},
			excluded: false,
		},
		{
			name: "multiple owners one excludes",
			owners: []OwnerMeta{
				{
					OwnerRef:    OwnerRef{Kind: "ReplicaSet", Name: "web-api-rs", UID: "rs-uid"},
					Annotations: map[string]string{},
				},
				{
					OwnerRef:    OwnerRef{Kind: "Deployment", Name: "web-api", UID: "deploy-uid"},
					Annotations: map[string]string{AnnotationExclude: "true"},
				},
			},
			excluded: true,
		},
		{
			name: "owner with nil annotations",
			owners: []OwnerMeta{
				{
					OwnerRef:    OwnerRef{Kind: "Deployment", Name: "web-api", UID: "deploy-uid"},
					Annotations: nil,
				},
			},
			excluded: false,
		},
		{
			name:     "no owners",
			owners:   nil,
			excluded: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := baseInput()
			input.Owners = tt.owners
			result := engine.Evaluate(input)
			if result.Excluded != tt.excluded {
				t.Errorf("Excluded = %v, want %v", result.Excluded, tt.excluded)
			}
			if tt.excluded && result.Reason != ReasonOwnerAnnotation {
				t.Errorf("Reason = %q, want %q", result.Reason, ReasonOwnerAnnotation)
			}
		})
	}
}

// --- Level 7: Resource annotation wormsign.io/exclude-detectors ---

func TestLevel7_ResourceExcludeDetectors(t *testing.T) {
	engine := newTestEngine(t, GlobalFilterConfig{}, nil, time.Now())

	tests := []struct {
		name         string
		annotations  map[string]string
		detectorName string
		excluded     bool
	}{
		{
			name:         "single detector match",
			annotations:  map[string]string{AnnotationExcludeDetectors: "PodCrashLoop"},
			detectorName: "PodCrashLoop",
			excluded:     true,
		},
		{
			name:         "comma-separated match",
			annotations:  map[string]string{AnnotationExcludeDetectors: "PodCrashLoop,PodFailed"},
			detectorName: "PodFailed",
			excluded:     true,
		},
		{
			name:         "comma-separated with spaces",
			annotations:  map[string]string{AnnotationExcludeDetectors: " PodCrashLoop , PodFailed "},
			detectorName: "PodFailed",
			excluded:     true,
		},
		{
			name:         "detector not in list",
			annotations:  map[string]string{AnnotationExcludeDetectors: "PodCrashLoop,PodFailed"},
			detectorName: "NodeNotReady",
			excluded:     false,
		},
		{
			name:         "empty annotation value",
			annotations:  map[string]string{AnnotationExcludeDetectors: ""},
			detectorName: "PodCrashLoop",
			excluded:     false,
		},
		{
			name:         "annotation absent",
			annotations:  map[string]string{},
			detectorName: "PodCrashLoop",
			excluded:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := baseInput()
			input.Resource.Annotations = tt.annotations
			input.DetectorName = tt.detectorName
			result := engine.Evaluate(input)
			if result.Excluded != tt.excluded {
				t.Errorf("Excluded = %v, want %v", result.Excluded, tt.excluded)
			}
			if tt.excluded {
				if result.Reason != ReasonResourceAnnotation {
					t.Errorf("Reason = %q, want %q", result.Reason, ReasonResourceAnnotation)
				}
				if !result.DetectorExcluded {
					t.Error("DetectorExcluded should be true")
				}
				if len(result.ExcludedDetectors) == 0 {
					t.Error("ExcludedDetectors should not be empty")
				}
			}
		})
	}
}

// --- Level 8: Owner annotation wormsign.io/exclude-detectors ---

func TestLevel8_OwnerExcludeDetectors(t *testing.T) {
	engine := newTestEngine(t, GlobalFilterConfig{}, nil, time.Now())

	tests := []struct {
		name         string
		owners       []OwnerMeta
		detectorName string
		excluded     bool
	}{
		{
			name: "owner excludes matching detector",
			owners: []OwnerMeta{
				{
					OwnerRef:    OwnerRef{Kind: "Deployment", Name: "web-api", UID: "uid"},
					Annotations: map[string]string{AnnotationExcludeDetectors: "PodCrashLoop,PodFailed"},
				},
			},
			detectorName: "PodCrashLoop",
			excluded:     true,
		},
		{
			name: "owner excludes non-matching detector",
			owners: []OwnerMeta{
				{
					OwnerRef:    OwnerRef{Kind: "Deployment", Name: "web-api", UID: "uid"},
					Annotations: map[string]string{AnnotationExcludeDetectors: "PodCrashLoop"},
				},
			},
			detectorName: "PodFailed",
			excluded:     false,
		},
		{
			name: "multiple owners union of detectors",
			owners: []OwnerMeta{
				{
					OwnerRef:    OwnerRef{Kind: "ReplicaSet", Name: "web-api-rs", UID: "uid1"},
					Annotations: map[string]string{AnnotationExcludeDetectors: "PodCrashLoop"},
				},
				{
					OwnerRef:    OwnerRef{Kind: "Deployment", Name: "web-api", UID: "uid2"},
					Annotations: map[string]string{AnnotationExcludeDetectors: "PodFailed"},
				},
			},
			detectorName: "PodFailed",
			excluded:     true,
		},
		{
			name:         "no owners",
			owners:       nil,
			detectorName: "PodCrashLoop",
			excluded:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := baseInput()
			input.Owners = tt.owners
			input.DetectorName = tt.detectorName
			result := engine.Evaluate(input)
			if result.Excluded != tt.excluded {
				t.Errorf("Excluded = %v, want %v", result.Excluded, tt.excluded)
			}
			if tt.excluded {
				if result.Reason != ReasonOwnerAnnotation {
					t.Errorf("Reason = %q, want %q", result.Reason, ReasonOwnerAnnotation)
				}
				if !result.DetectorExcluded {
					t.Error("DetectorExcluded should be true")
				}
			}
		})
	}
}

func TestLevel8_OwnerExcludeDetectorsDeduplicated(t *testing.T) {
	engine := newTestEngine(t, GlobalFilterConfig{}, nil, time.Now())

	input := baseInput()
	input.DetectorName = "PodCrashLoop"
	input.Owners = []OwnerMeta{
		{
			OwnerRef:    OwnerRef{Kind: "ReplicaSet", Name: "web-api-rs", UID: "uid1"},
			Annotations: map[string]string{AnnotationExcludeDetectors: "PodCrashLoop,PodFailed"},
		},
		{
			OwnerRef:    OwnerRef{Kind: "Deployment", Name: "web-api", UID: "uid2"},
			Annotations: map[string]string{AnnotationExcludeDetectors: "PodCrashLoop"},
		},
	}
	result := engine.Evaluate(input)
	if !result.Excluded {
		t.Fatal("should be excluded")
	}

	// Verify deduplication: PodCrashLoop appears in both owners but should
	// only appear once in ExcludedDetectors.
	count := 0
	for _, d := range result.ExcludedDetectors {
		if d == "PodCrashLoop" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("PodCrashLoop appears %d times in ExcludedDetectors, want 1", count)
	}
}

// --- Level 9: WormsignPolicy Exclude ---

func TestLevel9_ExcludePolicyResourceSelector(t *testing.T) {
	policies := []Policy{
		{
			Name:      "exclude-test-workloads",
			Namespace: "production",
			Action:    PolicyActionExclude,
			ResourceSelector: &LabelSelector{
				MatchLabels: map[string]string{"app.kubernetes.io/part-of": "integration-tests"},
			},
		},
	}
	engine := newTestEngine(t, GlobalFilterConfig{}, policies, time.Now())

	// Matching labels
	input := baseInput()
	input.Resource.Labels = map[string]string{
		"app.kubernetes.io/part-of": "integration-tests",
		"app":                       "test-runner",
	}
	result := engine.Evaluate(input)
	if !result.Excluded {
		t.Error("should be excluded by resource selector")
	}
	if result.Reason != ReasonPolicyExclude {
		t.Errorf("Reason = %q, want %q", result.Reason, ReasonPolicyExclude)
	}

	// Non-matching labels
	input.Resource.Labels = map[string]string{"app": "web-api"}
	result = engine.Evaluate(input)
	if result.Excluded {
		t.Error("should not be excluded with non-matching labels")
	}
}

func TestLevel9_ExcludePolicyResourceSelectorWithExpressions(t *testing.T) {
	policies := []Policy{
		{
			Name:      "exclude-by-team",
			Namespace: "production",
			Action:    PolicyActionExclude,
			ResourceSelector: &LabelSelector{
				MatchExpressions: []LabelSelectorRequirement{
					{
						Key:      "team",
						Operator: SelectorOpIn,
						Values:   []string{"platform-test", "qa"},
					},
				},
			},
		},
	}
	engine := newTestEngine(t, GlobalFilterConfig{}, policies, time.Now())

	tests := []struct {
		name     string
		labels   map[string]string
		excluded bool
	}{
		{
			name:     "team=platform-test matches In",
			labels:   map[string]string{"team": "platform-test"},
			excluded: true,
		},
		{
			name:     "team=qa matches In",
			labels:   map[string]string{"team": "qa"},
			excluded: true,
		},
		{
			name:     "team=backend does not match In",
			labels:   map[string]string{"team": "backend"},
			excluded: false,
		},
		{
			name:     "no team label",
			labels:   map[string]string{"app": "test"},
			excluded: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := baseInput()
			input.Resource.Labels = tt.labels
			result := engine.Evaluate(input)
			if result.Excluded != tt.excluded {
				t.Errorf("Excluded = %v, want %v", result.Excluded, tt.excluded)
			}
		})
	}
}

func TestLevel9_ExcludePolicyOwnerNames(t *testing.T) {
	policies := []Policy{
		{
			Name:       "exclude-canary",
			Namespace:  "production",
			Action:     PolicyActionExclude,
			OwnerNames: []string{"canary-deploy-*", "load-test-*"},
		},
	}
	engine := newTestEngine(t, GlobalFilterConfig{}, policies, time.Now())

	tests := []struct {
		name     string
		owners   []OwnerMeta
		excluded bool
	}{
		{
			name: "canary deploy matches glob",
			owners: []OwnerMeta{
				{OwnerRef: OwnerRef{Kind: "Deployment", Name: "canary-deploy-v2", UID: "uid"}},
			},
			excluded: true,
		},
		{
			name: "load test matches glob",
			owners: []OwnerMeta{
				{OwnerRef: OwnerRef{Kind: "Deployment", Name: "load-test-payment", UID: "uid"}},
			},
			excluded: true,
		},
		{
			name: "no match",
			owners: []OwnerMeta{
				{OwnerRef: OwnerRef{Kind: "Deployment", Name: "web-api", UID: "uid"}},
			},
			excluded: false,
		},
		{
			name:     "no owners",
			owners:   nil,
			excluded: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := baseInput()
			input.Owners = tt.owners
			result := engine.Evaluate(input)
			if result.Excluded != tt.excluded {
				t.Errorf("Excluded = %v, want %v", result.Excluded, tt.excluded)
			}
			if tt.excluded && result.Reason != ReasonPolicyExclude {
				t.Errorf("Reason = %q, want %q", result.Reason, ReasonPolicyExclude)
			}
		})
	}
}

func TestLevel9_ExcludePolicyWithDetectorScope(t *testing.T) {
	policies := []Policy{
		{
			Name:      "exclude-crashloop-for-tests",
			Namespace: "production",
			Action:    PolicyActionExclude,
			Detectors: []string{"PodCrashLoop"},
			ResourceSelector: &LabelSelector{
				MatchLabels: map[string]string{"test": "true"},
			},
		},
	}
	engine := newTestEngine(t, GlobalFilterConfig{}, policies, time.Now())

	// Matching detector and labels
	input := baseInput()
	input.DetectorName = "PodCrashLoop"
	input.Resource.Labels = map[string]string{"test": "true"}
	result := engine.Evaluate(input)
	if !result.Excluded {
		t.Error("should be excluded for matching detector and labels")
	}

	// Wrong detector
	input.DetectorName = "PodFailed"
	result = engine.Evaluate(input)
	if result.Excluded {
		t.Error("should not be excluded for non-matching detector")
	}
}

func TestLevel9_ExcludePolicyNamespaceScoping(t *testing.T) {
	policies := []Policy{
		{
			Name:      "exclude-in-test",
			Namespace: "test",
			Action:    PolicyActionExclude,
		},
	}
	engine := newTestEngine(t, GlobalFilterConfig{}, policies, time.Now())

	// Resource in "production" namespace - policy is in "test"
	input := baseInput()
	result := engine.Evaluate(input)
	if result.Excluded {
		t.Error("policy in 'test' namespace should not affect 'production' namespace")
	}

	// Resource in "test" namespace
	input.Namespace.Name = "test"
	result = engine.Evaluate(input)
	if !result.Excluded {
		t.Error("policy in 'test' namespace should affect resources in 'test'")
	}
}

func TestLevel9_ExcludePolicyWithNamespaceSelector(t *testing.T) {
	policies := []Policy{
		{
			Name:      "exclude-sandbox",
			Namespace: "wormsign-system",
			Action:    PolicyActionExclude,
			NamespaceSelector: &LabelSelector{
				MatchLabels: map[string]string{"environment": "sandbox"},
			},
		},
	}
	engine := newTestEngine(t, GlobalFilterConfig{}, policies, time.Now())

	// Namespace with sandbox label
	input := baseInput()
	input.Namespace.Labels = map[string]string{"environment": "sandbox"}
	result := engine.Evaluate(input)
	if !result.Excluded {
		t.Error("should be excluded by namespace selector")
	}

	// Namespace without sandbox label
	input.Namespace.Labels = map[string]string{"environment": "production"}
	result = engine.Evaluate(input)
	if result.Excluded {
		t.Error("should not be excluded when namespace labels don't match")
	}
}

func TestLevel9_ExcludePolicyBothSelectorAndOwnerNames(t *testing.T) {
	// When both resourceSelector and ownerNames are specified, both must match.
	policies := []Policy{
		{
			Name:      "exclude-canary-tests",
			Namespace: "production",
			Action:    PolicyActionExclude,
			ResourceSelector: &LabelSelector{
				MatchLabels: map[string]string{"test": "true"},
			},
			OwnerNames: []string{"canary-*"},
		},
	}
	engine := newTestEngine(t, GlobalFilterConfig{}, policies, time.Now())

	// Both match
	input := baseInput()
	input.Resource.Labels = map[string]string{"test": "true"}
	input.Owners = []OwnerMeta{
		{OwnerRef: OwnerRef{Kind: "Deployment", Name: "canary-v3", UID: "uid"}},
	}
	result := engine.Evaluate(input)
	if !result.Excluded {
		t.Error("should be excluded when both selector and owner match")
	}

	// Only labels match, owner doesn't
	input.Owners = []OwnerMeta{
		{OwnerRef: OwnerRef{Kind: "Deployment", Name: "web-api", UID: "uid"}},
	}
	result = engine.Evaluate(input)
	if result.Excluded {
		t.Error("should not be excluded when only labels match")
	}

	// Only owner matches, labels don't
	input.Resource.Labels = map[string]string{"test": "false"}
	input.Owners = []OwnerMeta{
		{OwnerRef: OwnerRef{Kind: "Deployment", Name: "canary-v3", UID: "uid"}},
	}
	result = engine.Evaluate(input)
	if result.Excluded {
		t.Error("should not be excluded when only owner matches")
	}
}

// --- Short-circuit behavior ---

func TestShortCircuit_Level1StopsEvaluation(t *testing.T) {
	// A resource that would match multiple levels should only report
	// the first matching level.
	policies := []Policy{
		{
			Name:      "suppress-all",
			Namespace: "kube-system",
			Action:    PolicyActionSuppress,
		},
		{
			Name:      "exclude-all",
			Namespace: "kube-system",
			Action:    PolicyActionExclude,
		},
	}
	engine := newTestEngine(t, GlobalFilterConfig{
		ExcludeNamespaces: []string{"kube-system"},
	}, policies, time.Now())

	input := baseInput()
	input.Namespace.Name = "kube-system"
	input.Resource.Namespace = "kube-system"
	input.Namespace.Annotations = map[string]string{AnnotationSuppress: "true"}
	input.Resource.Annotations = map[string]string{AnnotationExclude: "true"}

	result := engine.Evaluate(input)
	if !result.Excluded {
		t.Fatal("should be excluded")
	}
	// Level 1 should win
	if result.Reason != ReasonNamespaceGlobal {
		t.Errorf("Reason = %q, want %q (level 1 should short-circuit)", result.Reason, ReasonNamespaceGlobal)
	}
}

func TestShortCircuit_Level4BeforeLevel5(t *testing.T) {
	engine := newTestEngine(t, GlobalFilterConfig{}, nil, time.Now())

	input := baseInput()
	// Level 4 match
	input.Namespace.Annotations = map[string]string{AnnotationSuppress: "true"}
	// Level 5 would also match
	input.Resource.Annotations = map[string]string{AnnotationExclude: "true"}

	result := engine.Evaluate(input)
	if !result.Excluded {
		t.Fatal("should be excluded")
	}
	if result.Reason != ReasonNamespaceAnnotation {
		t.Errorf("Reason = %q, want %q (level 4 should short-circuit before level 5)", result.Reason, ReasonNamespaceAnnotation)
	}
}

func TestShortCircuit_Level5BeforeLevel6(t *testing.T) {
	engine := newTestEngine(t, GlobalFilterConfig{}, nil, time.Now())

	input := baseInput()
	// Level 5 match
	input.Resource.Annotations = map[string]string{AnnotationExclude: "true"}
	// Level 6 would also match
	input.Owners = []OwnerMeta{
		{
			OwnerRef:    OwnerRef{Kind: "Deployment", Name: "web-api", UID: "uid"},
			Annotations: map[string]string{AnnotationExclude: "true"},
		},
	}

	result := engine.Evaluate(input)
	if !result.Excluded {
		t.Fatal("should be excluded")
	}
	if result.Reason != ReasonResourceAnnotation {
		t.Errorf("Reason = %q, want %q (level 5 should short-circuit before level 6)", result.Reason, ReasonResourceAnnotation)
	}
}

func TestShortCircuit_Level6BeforeLevel7(t *testing.T) {
	engine := newTestEngine(t, GlobalFilterConfig{}, nil, time.Now())

	input := baseInput()
	input.DetectorName = "PodCrashLoop"
	// Level 6: owner has full exclude
	input.Owners = []OwnerMeta{
		{
			OwnerRef: OwnerRef{Kind: "Deployment", Name: "web-api", UID: "uid"},
			Annotations: map[string]string{
				AnnotationExclude:          "true",
				AnnotationExcludeDetectors: "PodCrashLoop",
			},
		},
	}
	// Level 7 would also match (same resource has exclude-detectors)
	input.Resource.Annotations = map[string]string{
		AnnotationExcludeDetectors: "PodCrashLoop",
	}

	result := engine.Evaluate(input)
	if !result.Excluded {
		t.Fatal("should be excluded")
	}
	// Level 6 (owner exclude: true) should win over level 7 (resource exclude-detectors)
	if result.Reason != ReasonOwnerAnnotation {
		t.Errorf("Reason = %q, want %q", result.Reason, ReasonOwnerAnnotation)
	}
	// Full exclude, not detector-specific
	if result.DetectorExcluded {
		t.Error("DetectorExcluded should be false for full owner exclude")
	}
}

func TestShortCircuit_NoMatch(t *testing.T) {
	policies := []Policy{
		{
			Name:      "exclude-test",
			Namespace: "test",
			Action:    PolicyActionExclude,
			ResourceSelector: &LabelSelector{
				MatchLabels: map[string]string{"test": "true"},
			},
		},
	}
	engine := newTestEngine(t, GlobalFilterConfig{
		ExcludeNamespaces: []string{"kube-system"},
	}, policies, time.Now())

	input := baseInput() // "production" namespace, no special annotations
	result := engine.Evaluate(input)
	if result.Excluded {
		t.Error("should not be excluded when nothing matches")
	}
	if result.Reason != "" {
		t.Errorf("Reason = %q, want empty string", result.Reason)
	}
	if result.DetectorExcluded {
		t.Error("DetectorExcluded should be false")
	}
	if len(result.ExcludedDetectors) != 0 {
		t.Errorf("ExcludedDetectors should be empty, got %v", result.ExcludedDetectors)
	}
}

// --- FilterResult reason strings match spec ---

func TestFilterReasonStrings(t *testing.T) {
	// Verify that all reason constants match the spec exactly.
	// From Section 6.3: namespace_global, namespace_label, suppress_policy,
	// namespace_annotation, resource_annotation, owner_annotation, policy_exclude.
	expected := map[FilterReason]string{
		ReasonNamespaceGlobal:     "namespace_global",
		ReasonNamespaceLabel:      "namespace_label",
		ReasonSuppressPolicy:      "suppress_policy",
		ReasonNamespaceAnnotation: "namespace_annotation",
		ReasonResourceAnnotation:  "resource_annotation",
		ReasonOwnerAnnotation:     "owner_annotation",
		ReasonPolicyExclude:       "policy_exclude",
	}

	for reason, want := range expected {
		if string(reason) != want {
			t.Errorf("FilterReason %v = %q, want %q", reason, string(reason), want)
		}
	}
}

// --- SetPolicies ---

func TestSetPolicies(t *testing.T) {
	engine := newTestEngine(t, GlobalFilterConfig{}, nil, time.Now())

	// Initially no policies
	input := baseInput()
	input.Namespace.Name = "test"
	input.Resource.Labels = map[string]string{"exclude": "true"}

	result := engine.Evaluate(input)
	if result.Excluded {
		t.Error("should not be excluded before policies are set")
	}

	// Add an exclude policy for the "test" namespace
	engine.SetPolicies([]Policy{
		{
			Name:      "exclude-labeled",
			Namespace: "test",
			Action:    PolicyActionExclude,
			ResourceSelector: &LabelSelector{
				MatchLabels: map[string]string{"exclude": "true"},
			},
		},
	})

	result = engine.Evaluate(input)
	if !result.Excluded {
		t.Error("should be excluded after policies are set")
	}

	// Replace policies with empty set
	engine.SetPolicies(nil)
	result = engine.Evaluate(input)
	if result.Excluded {
		t.Error("should not be excluded after policies are cleared")
	}
}

// --- Label selector edge cases ---

func TestMatchesLabelSelector_NotIn(t *testing.T) {
	policies := []Policy{
		{
			Name:      "exclude-non-prod",
			Namespace: "production",
			Action:    PolicyActionExclude,
			ResourceSelector: &LabelSelector{
				MatchExpressions: []LabelSelectorRequirement{
					{
						Key:      "environment",
						Operator: SelectorOpNotIn,
						Values:   []string{"production", "staging"},
					},
				},
			},
		},
	}
	engine := newTestEngine(t, GlobalFilterConfig{}, policies, time.Now())

	tests := []struct {
		name     string
		labels   map[string]string
		excluded bool
	}{
		{
			name:     "production not excluded by NotIn",
			labels:   map[string]string{"environment": "production"},
			excluded: false,
		},
		{
			name:     "staging not excluded by NotIn",
			labels:   map[string]string{"environment": "staging"},
			excluded: false,
		},
		{
			name:     "development excluded by NotIn",
			labels:   map[string]string{"environment": "development"},
			excluded: true,
		},
		{
			name:     "label absent matches NotIn",
			labels:   map[string]string{},
			excluded: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := baseInput()
			input.Resource.Labels = tt.labels
			result := engine.Evaluate(input)
			if result.Excluded != tt.excluded {
				t.Errorf("Excluded = %v, want %v", result.Excluded, tt.excluded)
			}
		})
	}
}

func TestMatchesLabelSelector_Exists(t *testing.T) {
	policies := []Policy{
		{
			Name:      "exclude-labeled",
			Namespace: "production",
			Action:    PolicyActionExclude,
			ResourceSelector: &LabelSelector{
				MatchExpressions: []LabelSelectorRequirement{
					{
						Key:      "wormsign.io/skip",
						Operator: SelectorOpExists,
					},
				},
			},
		},
	}
	engine := newTestEngine(t, GlobalFilterConfig{}, policies, time.Now())

	// Label exists
	input := baseInput()
	input.Resource.Labels = map[string]string{"wormsign.io/skip": "anything"}
	result := engine.Evaluate(input)
	if !result.Excluded {
		t.Error("should be excluded when label exists")
	}

	// Label absent
	input.Resource.Labels = map[string]string{"app": "web-api"}
	result = engine.Evaluate(input)
	if result.Excluded {
		t.Error("should not be excluded when label is absent")
	}
}

func TestMatchesLabelSelector_DoesNotExist(t *testing.T) {
	policies := []Policy{
		{
			Name:      "exclude-unlabeled",
			Namespace: "production",
			Action:    PolicyActionExclude,
			ResourceSelector: &LabelSelector{
				MatchExpressions: []LabelSelectorRequirement{
					{
						Key:      "monitoring",
						Operator: SelectorOpDoesNotExist,
					},
				},
			},
		},
	}
	engine := newTestEngine(t, GlobalFilterConfig{}, policies, time.Now())

	// Label absent — matches DoesNotExist
	input := baseInput()
	input.Resource.Labels = map[string]string{"app": "web-api"}
	result := engine.Evaluate(input)
	if !result.Excluded {
		t.Error("should be excluded when label does not exist")
	}

	// Label present — does not match
	input.Resource.Labels = map[string]string{"monitoring": "enabled"}
	result = engine.Evaluate(input)
	if result.Excluded {
		t.Error("should not be excluded when label exists")
	}
}

func TestMatchesLabelSelector_MultipleExpressions(t *testing.T) {
	// All expressions must match (ANDed)
	policies := []Policy{
		{
			Name:      "exclude-complex",
			Namespace: "production",
			Action:    PolicyActionExclude,
			ResourceSelector: &LabelSelector{
				MatchLabels: map[string]string{"app": "test"},
				MatchExpressions: []LabelSelectorRequirement{
					{
						Key:      "environment",
						Operator: SelectorOpIn,
						Values:   []string{"test", "dev"},
					},
				},
			},
		},
	}
	engine := newTestEngine(t, GlobalFilterConfig{}, policies, time.Now())

	// Both match
	input := baseInput()
	input.Resource.Labels = map[string]string{"app": "test", "environment": "test"}
	result := engine.Evaluate(input)
	if !result.Excluded {
		t.Error("should be excluded when all conditions match")
	}

	// Only matchLabels matches
	input.Resource.Labels = map[string]string{"app": "test", "environment": "production"}
	result = engine.Evaluate(input)
	if result.Excluded {
		t.Error("should not be excluded when expression doesn't match")
	}

	// Only expression matches
	input.Resource.Labels = map[string]string{"app": "web-api", "environment": "test"}
	result = engine.Evaluate(input)
	if result.Excluded {
		t.Error("should not be excluded when matchLabels doesn't match")
	}
}

// --- hasRegexMeta ---

func TestHasRegexMeta(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"kube-system", false},
		{"my-namespace", false},
		{"simple", false},
		{".*-sandbox", true},
		{"test-.*", true},
		{"ns[0-9]", true},
		{"a+b", true},
		{"a?b", true},
		{"(group)", true},
		{"^start", true},
		{"end$", true},
		{"a|b", true},
		{"a{2}", true},
		{`a\b`, true},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := hasRegexMeta(tt.input)
			if got != tt.want {
				t.Errorf("hasRegexMeta(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// --- compileNamespacePatterns ---

func TestCompileNamespacePatterns(t *testing.T) {
	patterns, err := compileNamespacePatterns([]string{
		"kube-system",   // exact
		".*-sandbox",    // regex
		"kube-public",   // exact
	})
	if err != nil {
		t.Fatalf("compileNamespacePatterns() error = %v", err)
	}
	if len(patterns) != 3 {
		t.Fatalf("length = %d, want 3", len(patterns))
	}

	if !patterns[0].exact || patterns[0].re != nil {
		t.Error("kube-system should be exact match")
	}
	if patterns[1].exact || patterns[1].re == nil {
		t.Error(".*-sandbox should be regex match")
	}
	if !patterns[2].exact || patterns[2].re != nil {
		t.Error("kube-public should be exact match")
	}
}

func TestCompileNamespacePatterns_Anchoring(t *testing.T) {
	patterns, err := compileNamespacePatterns([]string{"test-.*"})
	if err != nil {
		t.Fatalf("compileNamespacePatterns() error = %v", err)
	}
	// The regex should be anchored: ^test-.*$
	// It should match "test-alpha" but not "my-test-alpha"
	if patterns[0].re.MatchString("my-test-alpha") {
		t.Error("anchored regex should not match 'my-test-alpha'")
	}
	if !patterns[0].re.MatchString("test-alpha") {
		t.Error("anchored regex should match 'test-alpha'")
	}
}

func TestCompileNamespacePatterns_AlreadyAnchored(t *testing.T) {
	patterns, err := compileNamespacePatterns([]string{"^test-.*$"})
	if err != nil {
		t.Fatalf("compileNamespacePatterns() error = %v", err)
	}
	// Should not double-anchor
	if !patterns[0].re.MatchString("test-alpha") {
		t.Error("should match test-alpha")
	}
	if patterns[0].re.MatchString("my-test-alpha") {
		t.Error("should not match my-test-alpha")
	}
}

// --- isAnnotationTrue ---

func TestIsAnnotationTrue(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		key         string
		want        bool
	}{
		{"true lowercase", map[string]string{"k": "true"}, "k", true},
		{"TRUE uppercase", map[string]string{"k": "TRUE"}, "k", true},
		{"True mixed", map[string]string{"k": "True"}, "k", true},
		{"false", map[string]string{"k": "false"}, "k", false},
		{"empty value", map[string]string{"k": ""}, "k", false},
		{"key absent", map[string]string{}, "k", false},
		{"nil map", nil, "k", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isAnnotationTrue(tt.annotations, tt.key)
			if got != tt.want {
				t.Errorf("isAnnotationTrue() = %v, want %v", got, tt.want)
			}
		})
	}
}

// --- getExcludedDetectors ---

func TestGetExcludedDetectors(t *testing.T) {
	tests := []struct {
		name        string
		annotations map[string]string
		want        []string
	}{
		{
			name:        "single detector",
			annotations: map[string]string{AnnotationExcludeDetectors: "PodCrashLoop"},
			want:        []string{"PodCrashLoop"},
		},
		{
			name:        "multiple detectors",
			annotations: map[string]string{AnnotationExcludeDetectors: "PodCrashLoop,PodFailed,NodeNotReady"},
			want:        []string{"PodCrashLoop", "PodFailed", "NodeNotReady"},
		},
		{
			name:        "with spaces",
			annotations: map[string]string{AnnotationExcludeDetectors: " PodCrashLoop , PodFailed "},
			want:        []string{"PodCrashLoop", "PodFailed"},
		},
		{
			name:        "empty value",
			annotations: map[string]string{AnnotationExcludeDetectors: ""},
			want:        nil,
		},
		{
			name:        "absent key",
			annotations: map[string]string{},
			want:        nil,
		},
		{
			name:        "nil map",
			annotations: nil,
			want:        nil,
		},
		{
			name:        "trailing comma",
			annotations: map[string]string{AnnotationExcludeDetectors: "PodCrashLoop,"},
			want:        []string{"PodCrashLoop"},
		},
		{
			name:        "only commas",
			annotations: map[string]string{AnnotationExcludeDetectors: ",,"},
			want:        nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getExcludedDetectors(tt.annotations)
			if len(got) != len(tt.want) {
				t.Fatalf("getExcludedDetectors() length = %d, want %d (got %v)", len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("getExcludedDetectors()[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// --- getOwnerExcludedDetectors ---

func TestGetOwnerExcludedDetectors(t *testing.T) {
	tests := []struct {
		name   string
		owners []OwnerMeta
		want   int // expected count, since order may vary
	}{
		{
			name:   "no owners",
			owners: nil,
			want:   0,
		},
		{
			name: "single owner with detectors",
			owners: []OwnerMeta{
				{
					OwnerRef:    OwnerRef{Kind: "Deployment", Name: "d", UID: "uid"},
					Annotations: map[string]string{AnnotationExcludeDetectors: "PodCrashLoop,PodFailed"},
				},
			},
			want: 2,
		},
		{
			name: "two owners with overlapping detectors",
			owners: []OwnerMeta{
				{
					OwnerRef:    OwnerRef{Kind: "ReplicaSet", Name: "rs", UID: "uid1"},
					Annotations: map[string]string{AnnotationExcludeDetectors: "PodCrashLoop,PodFailed"},
				},
				{
					OwnerRef:    OwnerRef{Kind: "Deployment", Name: "d", UID: "uid2"},
					Annotations: map[string]string{AnnotationExcludeDetectors: "PodCrashLoop,NodeNotReady"},
				},
			},
			want: 3, // PodCrashLoop (deduped), PodFailed, NodeNotReady
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := getOwnerExcludedDetectors(tt.owners)
			if len(got) != tt.want {
				t.Errorf("getOwnerExcludedDetectors() length = %d, want %d (got %v)", len(got), tt.want, got)
			}
		})
	}
}

// --- containsDetector ---

func TestContainsDetector(t *testing.T) {
	detectors := []string{"PodCrashLoop", "PodFailed", "NodeNotReady"}

	if !containsDetector(detectors, "PodCrashLoop") {
		t.Error("should find PodCrashLoop")
	}
	if !containsDetector(detectors, "NodeNotReady") {
		t.Error("should find NodeNotReady")
	}
	if containsDetector(detectors, "PVCStuckBinding") {
		t.Error("should not find PVCStuckBinding")
	}
	if containsDetector(nil, "PodCrashLoop") {
		t.Error("should not find in nil list")
	}
	if containsDetector([]string{}, "PodCrashLoop") {
		t.Error("should not find in empty list")
	}
}

// --- matchesOwnerNames ---

func TestMatchesOwnerNames(t *testing.T) {
	tests := []struct {
		name     string
		owners   []OwnerMeta
		patterns []string
		want     bool
	}{
		{
			name: "exact match",
			owners: []OwnerMeta{
				{OwnerRef: OwnerRef{Name: "web-api"}},
			},
			patterns: []string{"web-api"},
			want:     true,
		},
		{
			name: "glob wildcard",
			owners: []OwnerMeta{
				{OwnerRef: OwnerRef{Name: "canary-deploy-v3"}},
			},
			patterns: []string{"canary-deploy-*"},
			want:     true,
		},
		{
			name: "no match",
			owners: []OwnerMeta{
				{OwnerRef: OwnerRef{Name: "web-api"}},
			},
			patterns: []string{"canary-*"},
			want:     false,
		},
		{
			name:     "no owners",
			owners:   nil,
			patterns: []string{"*"},
			want:     false,
		},
		{
			name: "empty patterns",
			owners: []OwnerMeta{
				{OwnerRef: OwnerRef{Name: "web-api"}},
			},
			patterns: nil,
			want:     false,
		},
		{
			name: "single char wildcard",
			owners: []OwnerMeta{
				{OwnerRef: OwnerRef{Name: "test-a"}},
			},
			patterns: []string{"test-?"},
			want:     true,
		},
		{
			name: "invalid pattern skipped",
			owners: []OwnerMeta{
				{OwnerRef: OwnerRef{Name: "web-api"}},
			},
			patterns: []string{"[invalid", "web-api"},
			want:     true, // invalid pattern skipped, second matches
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := matchesOwnerNames(tt.owners, tt.patterns)
			if got != tt.want {
				t.Errorf("matchesOwnerNames() = %v, want %v", got, tt.want)
			}
		})
	}
}

// --- matchesLabelSelector ---

func TestMatchesLabelSelector_NilSelector(t *testing.T) {
	// nil selector matches everything
	if !matchesLabelSelector(map[string]string{"a": "b"}, nil) {
		t.Error("nil selector should match any labels")
	}
	if !matchesLabelSelector(nil, nil) {
		t.Error("nil selector should match nil labels")
	}
}

func TestMatchesLabelSelector_EmptySelector(t *testing.T) {
	// Empty selector (no requirements) matches everything
	selector := &LabelSelector{}
	if !matchesLabelSelector(map[string]string{"a": "b"}, selector) {
		t.Error("empty selector should match any labels")
	}
}

func TestMatchesLabelSelector_NilLabels(t *testing.T) {
	// Non-empty selector should not match nil labels
	selector := &LabelSelector{
		MatchLabels: map[string]string{"a": "b"},
	}
	if matchesLabelSelector(nil, selector) {
		t.Error("non-empty selector should not match nil labels")
	}
}

// --- matchesLabelRequirement ---

func TestMatchesLabelRequirement_UnknownOperator(t *testing.T) {
	req := LabelSelectorRequirement{
		Key:      "key",
		Operator: "UnknownOp",
		Values:   []string{"val"},
	}
	if matchesLabelRequirement(map[string]string{"key": "val"}, req) {
		t.Error("unknown operator should not match")
	}
}

// --- Integration: full pipeline scenarios ---

func TestIntegration_ExcludePolicyIgnoresSuppressAction(t *testing.T) {
	// Ensure an Exclude policy does not fire at level 3 (Suppress),
	// and a Suppress policy does not fire at level 9 (Exclude).
	now := time.Now()
	policies := []Policy{
		{
			Name:      "suppress-maintenance",
			Namespace: "staging",
			Action:    PolicyActionSuppress,
		},
		{
			Name:      "exclude-tests",
			Namespace: "production",
			Action:    PolicyActionExclude,
			ResourceSelector: &LabelSelector{
				MatchLabels: map[string]string{"test": "true"},
			},
		},
	}
	engine := newTestEngine(t, GlobalFilterConfig{}, policies, now)

	// Production resource with test label: should be excluded by Exclude policy (level 9)
	input := baseInput()
	input.Resource.Labels = map[string]string{"test": "true"}
	result := engine.Evaluate(input)
	if !result.Excluded {
		t.Fatal("test resource should be excluded")
	}
	if result.Reason != ReasonPolicyExclude {
		t.Errorf("Reason = %q, want %q", result.Reason, ReasonPolicyExclude)
	}

	// Staging resource without test label: should be suppressed by Suppress policy (level 3)
	input = baseInput()
	input.Namespace.Name = "staging"
	input.Resource.Namespace = "staging"
	result = engine.Evaluate(input)
	if !result.Excluded {
		t.Fatal("staging resource should be suppressed")
	}
	if result.Reason != ReasonSuppressPolicy {
		t.Errorf("Reason = %q, want %q", result.Reason, ReasonSuppressPolicy)
	}
}

func TestIntegration_MultiplePoliciesFirstMatchWins(t *testing.T) {
	policies := []Policy{
		{
			Name:      "exclude-policy-1",
			Namespace: "production",
			Action:    PolicyActionExclude,
			ResourceSelector: &LabelSelector{
				MatchLabels: map[string]string{"app": "web-api"},
			},
		},
		{
			Name:      "exclude-policy-2",
			Namespace: "production",
			Action:    PolicyActionExclude,
			ResourceSelector: &LabelSelector{
				MatchLabels: map[string]string{"app": "web-api"},
			},
		},
	}
	engine := newTestEngine(t, GlobalFilterConfig{}, policies, time.Now())

	input := baseInput()
	result := engine.Evaluate(input)
	// Both policies match, but only the first should fire (short-circuit).
	if !result.Excluded {
		t.Fatal("should be excluded")
	}
	if result.Reason != ReasonPolicyExclude {
		t.Errorf("Reason = %q, want %q", result.Reason, ReasonPolicyExclude)
	}
}

func TestIntegration_ComplexScenario(t *testing.T) {
	// Simulate a real-world scenario: maintenance window active for staging,
	// global namespace exclusion for kube-system, annotation-based exclusion
	// for a specific resource, and policy-based exclusion for test workloads.
	now := time.Date(2026, 2, 8, 23, 30, 0, 0, time.UTC)
	policies := []Policy{
		{
			Name:      "staging-maintenance",
			Namespace: "staging",
			Action:    PolicyActionSuppress,
			Schedule: &PolicySchedule{
				Start: time.Date(2026, 2, 8, 22, 0, 0, 0, time.UTC),
				End:   time.Date(2026, 2, 9, 4, 0, 0, 0, time.UTC),
			},
		},
		{
			Name:      "exclude-test-workloads",
			Namespace: "production",
			Action:    PolicyActionExclude,
			ResourceSelector: &LabelSelector{
				MatchLabels: map[string]string{"app.kubernetes.io/part-of": "integration-tests"},
			},
		},
	}
	engine := newTestEngine(t, GlobalFilterConfig{
		ExcludeNamespaces: []string{"kube-system", "kube-public"},
	}, policies, now)

	// Case 1: kube-system resource (level 1)
	input := baseInput()
	input.Namespace.Name = "kube-system"
	input.Resource.Namespace = "kube-system"
	result := engine.Evaluate(input)
	if !result.Excluded || result.Reason != ReasonNamespaceGlobal {
		t.Errorf("case 1: Excluded=%v, Reason=%q, want Excluded=true, Reason=%q",
			result.Excluded, result.Reason, ReasonNamespaceGlobal)
	}

	// Case 2: staging resource during maintenance (level 3)
	input = baseInput()
	input.Namespace.Name = "staging"
	input.Resource.Namespace = "staging"
	result = engine.Evaluate(input)
	if !result.Excluded || result.Reason != ReasonSuppressPolicy {
		t.Errorf("case 2: Excluded=%v, Reason=%q, want Excluded=true, Reason=%q",
			result.Excluded, result.Reason, ReasonSuppressPolicy)
	}

	// Case 3: production test workload (level 9)
	input = baseInput()
	input.Resource.Labels = map[string]string{"app.kubernetes.io/part-of": "integration-tests"}
	result = engine.Evaluate(input)
	if !result.Excluded || result.Reason != ReasonPolicyExclude {
		t.Errorf("case 3: Excluded=%v, Reason=%q, want Excluded=true, Reason=%q",
			result.Excluded, result.Reason, ReasonPolicyExclude)
	}

	// Case 4: normal production resource (not excluded)
	input = baseInput()
	result = engine.Evaluate(input)
	if result.Excluded {
		t.Errorf("case 4: should not be excluded, got Reason=%q", result.Reason)
	}
}
