package v1alpha1

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// TestSchemeRegistration verifies that all CRD types are properly registered
// with the runtime scheme via AddToScheme.
func TestSchemeRegistration(t *testing.T) {
	scheme := runtime.NewScheme()
	if err := AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme() returned error: %v", err)
	}

	tests := []struct {
		name string
		obj  runtime.Object
	}{
		{"WormsignDetector", &WormsignDetector{}},
		{"WormsignDetectorList", &WormsignDetectorList{}},
		{"WormsignGatherer", &WormsignGatherer{}},
		{"WormsignGathererList", &WormsignGathererList{}},
		{"WormsignSink", &WormsignSink{}},
		{"WormsignSinkList", &WormsignSinkList{}},
		{"WormsignPolicy", &WormsignPolicy{}},
		{"WormsignPolicyList", &WormsignPolicyList{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gvks, _, err := scheme.ObjectKinds(tt.obj)
			if err != nil {
				t.Fatalf("scheme.ObjectKinds(%T) returned error: %v", tt.obj, err)
			}
			if len(gvks) == 0 {
				t.Fatalf("no GVKs registered for %T", tt.obj)
			}
			found := false
			for _, gvk := range gvks {
				if gvk.Group == GroupName && gvk.Version == Version {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("expected GVK with group=%q version=%q for %T, got %v", GroupName, Version, tt.obj, gvks)
			}
		})
	}
}

// TestSchemeGroupVersion verifies the SchemeGroupVersion constant.
func TestSchemeGroupVersion(t *testing.T) {
	if SchemeGroupVersion.Group != "wormsign.io" {
		t.Errorf("expected group %q, got %q", "wormsign.io", SchemeGroupVersion.Group)
	}
	if SchemeGroupVersion.Version != "v1alpha1" {
		t.Errorf("expected version %q, got %q", "v1alpha1", SchemeGroupVersion.Version)
	}
}

// TestResource verifies the Resource helper function.
func TestResource(t *testing.T) {
	gr := Resource("wormsigndetectors")
	if gr.Group != "wormsign.io" {
		t.Errorf("expected group %q, got %q", "wormsign.io", gr.Group)
	}
	if gr.Resource != "wormsigndetectors" {
		t.Errorf("expected resource %q, got %q", "wormsigndetectors", gr.Resource)
	}
}

// TestSeverityConstants verifies Severity constants have the correct string values.
func TestSeverityConstants(t *testing.T) {
	tests := []struct {
		severity Severity
		expected string
	}{
		{SeverityCritical, "critical"},
		{SeverityWarning, "warning"},
		{SeverityInfo, "info"},
	}
	for _, tt := range tests {
		if string(tt.severity) != tt.expected {
			t.Errorf("expected severity %q, got %q", tt.expected, string(tt.severity))
		}
	}
}

// TestConditionConstants verifies condition type string constants.
func TestConditionConstants(t *testing.T) {
	if ConditionReady != "Ready" {
		t.Errorf("expected %q, got %q", "Ready", ConditionReady)
	}
	if ConditionValid != "Valid" {
		t.Errorf("expected %q, got %q", "Valid", ConditionValid)
	}
	if ConditionActive != "Active" {
		t.Errorf("expected %q, got %q", "Active", ConditionActive)
	}
}

// TestPolicyActionConstants verifies PolicyAction constants.
func TestPolicyActionConstants(t *testing.T) {
	if PolicyActionExclude != "Exclude" {
		t.Errorf("expected %q, got %q", "Exclude", PolicyActionExclude)
	}
	if PolicyActionSuppress != "Suppress" {
		t.Errorf("expected %q, got %q", "Suppress", PolicyActionSuppress)
	}
}

// TestCollectTypeConstants verifies CollectType constants.
func TestCollectTypeConstants(t *testing.T) {
	if CollectTypeResource != "resource" {
		t.Errorf("expected %q, got %q", "resource", CollectTypeResource)
	}
	if CollectTypeLogs != "logs" {
		t.Errorf("expected %q, got %q", "logs", CollectTypeLogs)
	}
}

// TestSinkTypeConstants verifies SinkType constants.
func TestSinkTypeConstants(t *testing.T) {
	if SinkTypeWebhook != "webhook" {
		t.Errorf("expected %q, got %q", "webhook", SinkTypeWebhook)
	}
}

// --- DeepCopy tests ---

// TestWormsignDetectorDeepCopy verifies DeepCopy produces an independent copy.
func TestWormsignDetectorDeepCopy(t *testing.T) {
	original := &WormsignDetector{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "wormsign.io/v1alpha1",
			Kind:       "WormsignDetector",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-detector",
			Namespace: "default",
		},
		Spec: WormsignDetectorSpec{
			Description: "test",
			Resource:    "pods",
			Condition:   "pod.status.phase == 'Failed'",
			Params:      map[string]string{"maxRestarts": "10"},
			Severity:    SeverityWarning,
			Cooldown:    "30m",
			NamespaceSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"env": "prod"},
			},
		},
		Status: WormsignDetectorStatus{
			WormsignStatus: WormsignStatus{
				Conditions: []metav1.Condition{
					{Type: ConditionReady, Status: metav1.ConditionTrue},
				},
			},
			MatchedNamespaces: 5,
			FaultEventsTotal:  42,
		},
	}

	copied := original.DeepCopy()

	// Modify the copy and verify original is unchanged.
	copied.Spec.Params["newKey"] = "newValue"
	if _, ok := original.Spec.Params["newKey"]; ok {
		t.Error("modifying DeepCopy affected original Params map")
	}

	copied.Spec.NamespaceSelector.MatchLabels["added"] = "true"
	if _, ok := original.Spec.NamespaceSelector.MatchLabels["added"]; ok {
		t.Error("modifying DeepCopy affected original NamespaceSelector")
	}

	copied.Status.Conditions[0].Status = metav1.ConditionFalse
	if original.Status.Conditions[0].Status != metav1.ConditionTrue {
		t.Error("modifying DeepCopy affected original Conditions")
	}
}

// TestWormsignDetectorDeepCopyObject verifies DeepCopyObject returns a runtime.Object.
func TestWormsignDetectorDeepCopyObject(t *testing.T) {
	original := &WormsignDetector{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
	}
	obj := original.DeepCopyObject()
	if obj == nil {
		t.Fatal("DeepCopyObject returned nil")
	}
	if _, ok := obj.(*WormsignDetector); !ok {
		t.Fatalf("DeepCopyObject returned %T, expected *WormsignDetector", obj)
	}
}

// TestWormsignDetectorDeepCopyNil verifies DeepCopy on nil returns nil.
func TestWormsignDetectorDeepCopyNil(t *testing.T) {
	var d *WormsignDetector
	if d.DeepCopy() != nil {
		t.Error("DeepCopy on nil should return nil")
	}
}

// TestWormsignGathererDeepCopy verifies DeepCopy for WormsignGatherer.
func TestWormsignGathererDeepCopy(t *testing.T) {
	tailLines := int64(100)
	original := &WormsignGatherer{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-gatherer",
			Namespace: "default",
		},
		Spec: WormsignGathererSpec{
			Description: "test gatherer",
			TriggerOn: GathererTrigger{
				ResourceKinds: []string{"Pod"},
				Detectors:     []string{"PodCrashLoop"},
			},
			Collect: []CollectAction{
				{
					Type:       CollectTypeResource,
					APIVersion: "v1",
					Resource:   "pods",
					Name:       "{resource.name}",
					Namespace:  "{resource.namespace}",
				},
				{
					Type:      CollectTypeLogs,
					Container: "main",
					TailLines: &tailLines,
					Previous:  true,
				},
			},
		},
	}

	copied := original.DeepCopy()

	// Modify copy's slices and verify original is unaffected.
	copied.Spec.TriggerOn.ResourceKinds[0] = "Node"
	if original.Spec.TriggerOn.ResourceKinds[0] != "Pod" {
		t.Error("modifying DeepCopy affected original ResourceKinds")
	}

	copied.Spec.TriggerOn.Detectors[0] = "NodeNotReady"
	if original.Spec.TriggerOn.Detectors[0] != "PodCrashLoop" {
		t.Error("modifying DeepCopy affected original Detectors")
	}

	copied.Spec.Collect[0].Name = "changed"
	if original.Spec.Collect[0].Name != "{resource.name}" {
		t.Error("modifying DeepCopy affected original Collect")
	}

	// Verify pointer field is independently copied.
	*copied.Spec.Collect[1].TailLines = 50
	if *original.Spec.Collect[1].TailLines != 100 {
		t.Error("modifying DeepCopy affected original TailLines pointer")
	}
}

// TestWormsignGathererDeepCopyObject verifies DeepCopyObject returns a runtime.Object.
func TestWormsignGathererDeepCopyObject(t *testing.T) {
	original := &WormsignGatherer{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
	}
	obj := original.DeepCopyObject()
	if obj == nil {
		t.Fatal("DeepCopyObject returned nil")
	}
	if _, ok := obj.(*WormsignGatherer); !ok {
		t.Fatalf("DeepCopyObject returned %T, expected *WormsignGatherer", obj)
	}
}

// TestWormsignSinkDeepCopy verifies DeepCopy for WormsignSink.
func TestWormsignSinkDeepCopy(t *testing.T) {
	original := &WormsignSink{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-sink",
			Namespace: "default",
		},
		Spec: WormsignSinkSpec{
			Description: "test sink",
			Type:        SinkTypeWebhook,
			Webhook: &WebhookConfig{
				URL:    "https://example.com/webhook",
				Method: "POST",
				Headers: map[string]string{
					"Content-Type": "application/json",
				},
				BodyTemplate: `{"msg": "{{ .RootCause }}"}`,
			},
			SeverityFilter: []Severity{SeverityCritical, SeverityWarning},
		},
		Status: WormsignSinkStatus{
			WormsignStatus: WormsignStatus{
				Conditions: []metav1.Condition{
					{Type: ConditionReady, Status: metav1.ConditionTrue},
				},
			},
			DeliveriesTotal: 42,
		},
	}

	copied := original.DeepCopy()

	// Modify webhook headers in copy.
	copied.Spec.Webhook.Headers["Authorization"] = "Bearer token"
	if _, ok := original.Spec.Webhook.Headers["Authorization"]; ok {
		t.Error("modifying DeepCopy affected original Webhook Headers")
	}

	// Modify severity filter in copy.
	copied.Spec.SeverityFilter[0] = SeverityInfo
	if original.Spec.SeverityFilter[0] != SeverityCritical {
		t.Error("modifying DeepCopy affected original SeverityFilter")
	}

	// Verify webhook pointer is independently copied.
	copied.Spec.Webhook.URL = "https://changed.com"
	if original.Spec.Webhook.URL != "https://example.com/webhook" {
		t.Error("modifying DeepCopy affected original Webhook URL")
	}
}

// TestWormsignSinkDeepCopyWithSecretRef verifies DeepCopy with URLSecretRef.
func TestWormsignSinkDeepCopyWithSecretRef(t *testing.T) {
	original := &WormsignSink{
		Spec: WormsignSinkSpec{
			Type: SinkTypeWebhook,
			Webhook: &WebhookConfig{
				URLSecretRef: &SecretReference{
					Name: "my-secret",
					Key:  "webhook-url",
				},
			},
		},
	}

	copied := original.DeepCopy()
	copied.Spec.Webhook.URLSecretRef.Name = "changed"
	if original.Spec.Webhook.URLSecretRef.Name != "my-secret" {
		t.Error("modifying DeepCopy affected original URLSecretRef")
	}
}

// TestWormsignSinkDeepCopyObject verifies DeepCopyObject for WormsignSink.
func TestWormsignSinkDeepCopyObject(t *testing.T) {
	original := &WormsignSink{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
	}
	obj := original.DeepCopyObject()
	if obj == nil {
		t.Fatal("DeepCopyObject returned nil")
	}
	if _, ok := obj.(*WormsignSink); !ok {
		t.Fatalf("DeepCopyObject returned %T, expected *WormsignSink", obj)
	}
}

// TestWormsignPolicyDeepCopy verifies DeepCopy for WormsignPolicy.
func TestWormsignPolicyDeepCopy(t *testing.T) {
	now := metav1.Now()
	later := metav1.NewTime(now.Add(3600e9)) // 1 hour later
	original := &WormsignPolicy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-policy",
			Namespace: "default",
		},
		Spec: WormsignPolicySpec{
			Action:    PolicyActionSuppress,
			Detectors: []string{"PodCrashLoop", "PodFailed"},
			NamespaceSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"env": "sandbox"},
			},
			Match: &PolicyMatch{
				ResourceSelector: &metav1.LabelSelector{
					MatchLabels: map[string]string{"team": "qa"},
				},
				OwnerNames: []string{"canary-*", "test-*"},
			},
			Schedule: &PolicySchedule{
				Start: now,
				End:   later,
			},
		},
		Status: WormsignPolicyStatus{
			WormsignStatus: WormsignStatus{
				Conditions: []metav1.Condition{
					{Type: ConditionActive, Status: metav1.ConditionTrue},
				},
			},
			MatchedResources: 47,
		},
	}

	copied := original.DeepCopy()

	// Modify copy's detectors list.
	copied.Spec.Detectors[0] = "NodeNotReady"
	if original.Spec.Detectors[0] != "PodCrashLoop" {
		t.Error("modifying DeepCopy affected original Detectors")
	}

	// Modify copy's namespace selector.
	copied.Spec.NamespaceSelector.MatchLabels["added"] = "true"
	if _, ok := original.Spec.NamespaceSelector.MatchLabels["added"]; ok {
		t.Error("modifying DeepCopy affected original NamespaceSelector")
	}

	// Modify copy's match criteria.
	copied.Spec.Match.OwnerNames[0] = "changed-*"
	if original.Spec.Match.OwnerNames[0] != "canary-*" {
		t.Error("modifying DeepCopy affected original OwnerNames")
	}

	copied.Spec.Match.ResourceSelector.MatchLabels["extra"] = "value"
	if _, ok := original.Spec.Match.ResourceSelector.MatchLabels["extra"]; ok {
		t.Error("modifying DeepCopy affected original ResourceSelector")
	}
}

// TestWormsignPolicyDeepCopyObject verifies DeepCopyObject for WormsignPolicy.
func TestWormsignPolicyDeepCopyObject(t *testing.T) {
	original := &WormsignPolicy{
		ObjectMeta: metav1.ObjectMeta{Name: "test"},
	}
	obj := original.DeepCopyObject()
	if obj == nil {
		t.Fatal("DeepCopyObject returned nil")
	}
	if _, ok := obj.(*WormsignPolicy); !ok {
		t.Fatalf("DeepCopyObject returned %T, expected *WormsignPolicy", obj)
	}
}

// --- List type tests ---

// TestDetectorListDeepCopy verifies DeepCopy for list types.
func TestDetectorListDeepCopy(t *testing.T) {
	original := &WormsignDetectorList{
		Items: []WormsignDetector{
			{ObjectMeta: metav1.ObjectMeta{Name: "d1"}},
			{ObjectMeta: metav1.ObjectMeta{Name: "d2"}},
		},
	}

	copied := original.DeepCopy()
	copied.Items[0].Name = "changed"
	if original.Items[0].Name != "d1" {
		t.Error("modifying DeepCopy list affected original items")
	}
}

// TestGathererListDeepCopy verifies DeepCopy for WormsignGathererList.
func TestGathererListDeepCopy(t *testing.T) {
	original := &WormsignGathererList{
		Items: []WormsignGatherer{
			{ObjectMeta: metav1.ObjectMeta{Name: "g1"}},
		},
	}
	copied := original.DeepCopy()
	if len(copied.Items) != 1 || copied.Items[0].Name != "g1" {
		t.Error("DeepCopy did not properly copy gatherer list items")
	}
}

// TestSinkListDeepCopy verifies DeepCopy for WormsignSinkList.
func TestSinkListDeepCopy(t *testing.T) {
	original := &WormsignSinkList{
		Items: []WormsignSink{
			{ObjectMeta: metav1.ObjectMeta{Name: "s1"}},
		},
	}
	copied := original.DeepCopy()
	if len(copied.Items) != 1 || copied.Items[0].Name != "s1" {
		t.Error("DeepCopy did not properly copy sink list items")
	}
}

// TestPolicyListDeepCopy verifies DeepCopy for WormsignPolicyList.
func TestPolicyListDeepCopy(t *testing.T) {
	original := &WormsignPolicyList{
		Items: []WormsignPolicy{
			{ObjectMeta: metav1.ObjectMeta{Name: "p1"}},
		},
	}
	copied := original.DeepCopy()
	if len(copied.Items) != 1 || copied.Items[0].Name != "p1" {
		t.Error("DeepCopy did not properly copy policy list items")
	}
}

// TestListDeepCopyObject verifies DeepCopyObject for all list types.
func TestListDeepCopyObject(t *testing.T) {
	tests := []struct {
		name string
		obj  runtime.Object
	}{
		{"WormsignDetectorList", &WormsignDetectorList{}},
		{"WormsignGathererList", &WormsignGathererList{}},
		{"WormsignSinkList", &WormsignSinkList{}},
		{"WormsignPolicyList", &WormsignPolicyList{}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			copied := tt.obj.DeepCopyObject()
			if copied == nil {
				t.Fatal("DeepCopyObject returned nil")
			}
		})
	}
}

// --- Struct field presence tests ---
// Ensure all required fields exist as expected by the spec.

// TestDetectorSpecFields verifies that WormsignDetectorSpec has all
// fields required by Section 4.2 of the project spec.
func TestDetectorSpecFields(t *testing.T) {
	spec := WormsignDetectorSpec{
		Description: "Test detector",
		Resource:    "pods",
		NamespaceSelector: &metav1.LabelSelector{
			MatchLabels: map[string]string{"wormsign.io/watch": "true"},
		},
		Condition: `pod.status.containerStatuses.exists(c, c.restartCount > params.maxRestarts)`,
		Params:    map[string]string{"maxRestarts": "10"},
		Severity:  SeverityWarning,
		Cooldown:  "1h",
	}

	if spec.Description != "Test detector" {
		t.Error("Description field not set correctly")
	}
	if spec.Resource != "pods" {
		t.Error("Resource field not set correctly")
	}
	if spec.NamespaceSelector == nil {
		t.Error("NamespaceSelector should not be nil")
	}
	if spec.Condition == "" {
		t.Error("Condition field should not be empty")
	}
	if len(spec.Params) != 1 {
		t.Error("Params should have one entry")
	}
	if spec.Severity != SeverityWarning {
		t.Error("Severity field not set correctly")
	}
	if spec.Cooldown != "1h" {
		t.Error("Cooldown field not set correctly")
	}
}

// TestDetectorStatusFields verifies WormsignDetectorStatus fields per spec.
func TestDetectorStatusFields(t *testing.T) {
	now := metav1.Now()
	status := WormsignDetectorStatus{
		WormsignStatus: WormsignStatus{
			Conditions: []metav1.Condition{
				{Type: ConditionReady, Status: metav1.ConditionTrue, Reason: "OK", Message: "All good"},
			},
		},
		MatchedNamespaces: 12,
		LastFired:         &now,
		FaultEventsTotal:  47,
	}

	if len(status.Conditions) != 1 {
		t.Error("expected 1 condition")
	}
	if status.MatchedNamespaces != 12 {
		t.Error("MatchedNamespaces not set correctly")
	}
	if status.LastFired == nil {
		t.Error("LastFired should not be nil")
	}
	if status.FaultEventsTotal != 47 {
		t.Error("FaultEventsTotal not set correctly")
	}
}

// TestGathererSpecFields verifies WormsignGathererSpec has fields per Section 4.3.
func TestGathererSpecFields(t *testing.T) {
	tailLines := int64(50)
	spec := WormsignGathererSpec{
		Description: "Collects Istio sidecar config",
		TriggerOn: GathererTrigger{
			ResourceKinds: []string{"Pod"},
			Detectors:     []string{"PodCrashLoop", "PodFailed"},
		},
		Collect: []CollectAction{
			{
				Type:       CollectTypeResource,
				APIVersion: "v1",
				Resource:   "pods",
				Name:       "{resource.name}",
				Namespace:  "{resource.namespace}",
				JSONPath:   ".metadata.annotations",
			},
			{
				Type:       CollectTypeResource,
				APIVersion: "networking.istio.io/v1",
				Resource:   "destinationrules",
				Namespace:  "{resource.namespace}",
				ListAll:    true,
			},
			{
				Type:      CollectTypeLogs,
				Container: "istio-proxy",
				TailLines: &tailLines,
				Previous:  false,
			},
		},
	}

	if spec.Description == "" {
		t.Error("Description should not be empty")
	}
	if len(spec.TriggerOn.ResourceKinds) != 1 {
		t.Error("expected 1 resource kind")
	}
	if len(spec.TriggerOn.Detectors) != 2 {
		t.Error("expected 2 detectors")
	}
	if len(spec.Collect) != 3 {
		t.Error("expected 3 collect actions")
	}
	// Verify resource collection action.
	if spec.Collect[0].Type != CollectTypeResource {
		t.Error("first collect should be resource type")
	}
	if spec.Collect[0].JSONPath != ".metadata.annotations" {
		t.Error("JSONPath not set correctly")
	}
	// Verify listAll action.
	if !spec.Collect[1].ListAll {
		t.Error("second collect should have listAll=true")
	}
	// Verify logs collection action.
	if spec.Collect[2].Type != CollectTypeLogs {
		t.Error("third collect should be logs type")
	}
	if *spec.Collect[2].TailLines != 50 {
		t.Error("TailLines not set correctly")
	}
}

// TestSinkSpecFields verifies WormsignSinkSpec fields per Section 4.4.
func TestSinkSpecFields(t *testing.T) {
	spec := WormsignSinkSpec{
		Description: "Posts to Teams via webhook",
		Type:        SinkTypeWebhook,
		Webhook: &WebhookConfig{
			URLSecretRef: &SecretReference{
				Name: "wormsign-sink-secrets",
				Key:  "teams-webhook-url",
			},
			Method:  "POST",
			Headers: map[string]string{"Content-Type": "application/json"},
			BodyTemplate: `{
				"summary": "{{ .RootCause }}"
			}`,
		},
		SeverityFilter: []Severity{SeverityCritical, SeverityWarning},
	}

	if spec.Type != SinkTypeWebhook {
		t.Error("Type should be webhook")
	}
	if spec.Webhook == nil {
		t.Fatal("Webhook should not be nil")
	}
	if spec.Webhook.URLSecretRef == nil {
		t.Fatal("URLSecretRef should not be nil")
	}
	if spec.Webhook.URLSecretRef.Name != "wormsign-sink-secrets" {
		t.Error("SecretRef Name not set correctly")
	}
	if spec.Webhook.URLSecretRef.Key != "teams-webhook-url" {
		t.Error("SecretRef Key not set correctly")
	}
	if spec.Webhook.Method != "POST" {
		t.Error("Method not set correctly")
	}
	if len(spec.Webhook.Headers) != 1 {
		t.Error("expected 1 header")
	}
	if spec.Webhook.BodyTemplate == "" {
		t.Error("BodyTemplate should not be empty")
	}
	if len(spec.SeverityFilter) != 2 {
		t.Error("expected 2 severity filter entries")
	}
}

// TestSinkStatusFields verifies WormsignSinkStatus fields per Section 4.4.
func TestSinkStatusFields(t *testing.T) {
	now := metav1.Now()
	status := WormsignSinkStatus{
		WormsignStatus: WormsignStatus{
			Conditions: []metav1.Condition{
				{Type: ConditionReady, Status: metav1.ConditionTrue},
			},
		},
		DeliveriesTotal:    142,
		LastDelivery:       &now,
		LastDeliveryStatus: "success",
	}

	if status.DeliveriesTotal != 142 {
		t.Error("DeliveriesTotal not set correctly")
	}
	if status.LastDelivery == nil {
		t.Error("LastDelivery should not be nil")
	}
	if status.LastDeliveryStatus != "success" {
		t.Error("LastDeliveryStatus not set correctly")
	}
}

// TestPolicySpecFields verifies WormsignPolicySpec fields per Section 4.5 and 6.
func TestPolicySpecFields(t *testing.T) {
	now := metav1.Now()
	later := metav1.NewTime(now.Add(4 * 3600e9))
	spec := WormsignPolicySpec{
		Action:    PolicyActionExclude,
		Detectors: []string{"PodCrashLoop", "PodFailed"},
		NamespaceSelector: &metav1.LabelSelector{
			MatchLabels: map[string]string{"environment": "sandbox"},
		},
		Match: &PolicyMatch{
			ResourceSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"app.kubernetes.io/part-of": "integration-tests"},
				MatchExpressions: []metav1.LabelSelectorRequirement{
					{
						Key:      "team",
						Operator: metav1.LabelSelectorOpIn,
						Values:   []string{"platform-test", "qa"},
					},
				},
			},
			OwnerNames: []string{"canary-deploy-*", "load-test-*"},
		},
		Schedule: &PolicySchedule{
			Start: now,
			End:   later,
		},
	}

	if spec.Action != PolicyActionExclude {
		t.Error("Action not set correctly")
	}
	if len(spec.Detectors) != 2 {
		t.Error("expected 2 detectors")
	}
	if spec.NamespaceSelector == nil {
		t.Error("NamespaceSelector should not be nil")
	}
	if spec.Match == nil {
		t.Fatal("Match should not be nil")
	}
	if spec.Match.ResourceSelector == nil {
		t.Error("ResourceSelector should not be nil")
	}
	if len(spec.Match.ResourceSelector.MatchExpressions) != 1 {
		t.Error("expected 1 match expression")
	}
	if len(spec.Match.OwnerNames) != 2 {
		t.Error("expected 2 owner name patterns")
	}
	if spec.Schedule == nil {
		t.Error("Schedule should not be nil")
	}
	if !spec.Schedule.End.After(spec.Schedule.Start.Time) {
		t.Error("Schedule End should be after Start")
	}
}

// TestPolicyStatusFields verifies WormsignPolicyStatus fields.
func TestPolicyStatusFields(t *testing.T) {
	now := metav1.Now()
	status := WormsignPolicyStatus{
		WormsignStatus: WormsignStatus{
			Conditions: []metav1.Condition{
				{Type: ConditionActive, Status: metav1.ConditionTrue, Reason: "Active", Message: "Policy is active, matching 47 resources"},
			},
		},
		MatchedResources: 47,
		LastEvaluated:    &now,
	}

	if status.MatchedResources != 47 {
		t.Error("MatchedResources not set correctly")
	}
	if status.LastEvaluated == nil {
		t.Error("LastEvaluated should not be nil")
	}
	if len(status.Conditions) != 1 {
		t.Error("expected 1 condition")
	}
}

// --- DeepCopyInto tests for generated types ---
// These exercise DeepCopyInto paths in zz_generated.deepcopy.go that aren't
// hit by the DeepCopy() wrapper tests above.

func TestDeepCopyInto_AllTypes(t *testing.T) {
	now := metav1.Now()

	t.Run("CollectAction with TailLines", func(t *testing.T) {
		tl := int64(100)
		original := CollectAction{Type: CollectTypeLogs, Container: "app", TailLines: &tl, Previous: true}
		var out CollectAction
		original.DeepCopyInto(&out)
		if out.TailLines == original.TailLines {
			t.Error("TailLines pointer should be a new allocation")
		}
		if *out.TailLines != 100 {
			t.Errorf("TailLines value = %d, want 100", *out.TailLines)
		}
		*out.TailLines = 50
		if *original.TailLines != 100 {
			t.Error("mutating copy affected original")
		}
	})

	t.Run("CollectAction without TailLines", func(t *testing.T) {
		original := CollectAction{Type: CollectTypeResource, Resource: "pods"}
		copied := original.DeepCopy()
		if copied.TailLines != nil {
			t.Error("TailLines should be nil")
		}
	})

	t.Run("CollectAction nil", func(t *testing.T) {
		var ca *CollectAction
		if ca.DeepCopy() != nil {
			t.Error("DeepCopy of nil CollectAction should return nil")
		}
	})

	t.Run("GathererTrigger", func(t *testing.T) {
		original := GathererTrigger{
			ResourceKinds: []string{"Pod", "Node"},
			Detectors:     []string{"PodCrashLoop"},
		}
		var out GathererTrigger
		original.DeepCopyInto(&out)
		out.ResourceKinds[0] = "Changed"
		if original.ResourceKinds[0] != "Pod" {
			t.Error("mutating DeepCopyInto output affected original ResourceKinds")
		}
		out.Detectors[0] = "Changed"
		if original.Detectors[0] != "PodCrashLoop" {
			t.Error("mutating DeepCopyInto output affected original Detectors")
		}
	})

	t.Run("GathererTrigger nil", func(t *testing.T) {
		var gt *GathererTrigger
		if gt.DeepCopy() != nil {
			t.Error("DeepCopy of nil GathererTrigger should return nil")
		}
	})

	t.Run("GathererTrigger empty slices", func(t *testing.T) {
		original := GathererTrigger{}
		copied := original.DeepCopy()
		if copied.ResourceKinds != nil {
			t.Error("nil ResourceKinds should remain nil")
		}
		if copied.Detectors != nil {
			t.Error("nil Detectors should remain nil")
		}
	})

	t.Run("PolicyMatch", func(t *testing.T) {
		original := PolicyMatch{
			ResourceSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"team": "qa"},
			},
			OwnerNames: []string{"canary-*"},
		}
		var out PolicyMatch
		original.DeepCopyInto(&out)
		out.OwnerNames[0] = "changed-*"
		if original.OwnerNames[0] != "canary-*" {
			t.Error("mutating DeepCopyInto output affected original OwnerNames")
		}
		out.ResourceSelector.MatchLabels["extra"] = "value"
		if _, ok := original.ResourceSelector.MatchLabels["extra"]; ok {
			t.Error("mutating DeepCopyInto output affected original ResourceSelector")
		}
	})

	t.Run("PolicyMatch nil", func(t *testing.T) {
		var pm *PolicyMatch
		if pm.DeepCopy() != nil {
			t.Error("DeepCopy of nil PolicyMatch should return nil")
		}
	})

	t.Run("PolicyMatch empty", func(t *testing.T) {
		original := PolicyMatch{}
		copied := original.DeepCopy()
		if copied.ResourceSelector != nil {
			t.Error("nil ResourceSelector should remain nil")
		}
		if copied.OwnerNames != nil {
			t.Error("nil OwnerNames should remain nil")
		}
	})

	t.Run("PolicySchedule", func(t *testing.T) {
		later := metav1.NewTime(now.Add(3600e9))
		original := PolicySchedule{Start: now, End: later}
		var out PolicySchedule
		original.DeepCopyInto(&out)
		if out.Start.Time != now.Time {
			t.Error("Start time mismatch")
		}
		if out.End.Time != later.Time {
			t.Error("End time mismatch")
		}
	})

	t.Run("PolicySchedule nil", func(t *testing.T) {
		var ps *PolicySchedule
		if ps.DeepCopy() != nil {
			t.Error("DeepCopy of nil PolicySchedule should return nil")
		}
	})

	t.Run("SecretReference nil", func(t *testing.T) {
		var sr *SecretReference
		if sr.DeepCopy() != nil {
			t.Error("DeepCopy of nil SecretReference should return nil")
		}
	})

	t.Run("WebhookConfig", func(t *testing.T) {
		original := WebhookConfig{
			URL: "https://example.com",
			URLSecretRef: &SecretReference{
				Name: "secret",
				Key:  "url",
			},
			Headers:      map[string]string{"Auth": "Bearer token"},
			Method:       "POST",
			BodyTemplate: "template",
		}
		var out WebhookConfig
		original.DeepCopyInto(&out)
		out.URLSecretRef.Name = "changed"
		if original.URLSecretRef.Name != "secret" {
			t.Error("mutating DeepCopyInto output affected original URLSecretRef")
		}
		out.Headers["new"] = "header"
		if _, ok := original.Headers["new"]; ok {
			t.Error("mutating DeepCopyInto output affected original Headers")
		}
	})

	t.Run("WebhookConfig nil", func(t *testing.T) {
		var wc *WebhookConfig
		if wc.DeepCopy() != nil {
			t.Error("DeepCopy of nil WebhookConfig should return nil")
		}
	})

	t.Run("WebhookConfig empty", func(t *testing.T) {
		original := WebhookConfig{URL: "https://test.com"}
		copied := original.DeepCopy()
		if copied.URLSecretRef != nil {
			t.Error("nil URLSecretRef should remain nil")
		}
		if copied.Headers != nil {
			t.Error("nil Headers should remain nil")
		}
	})

	t.Run("WormsignDetectorSpec", func(t *testing.T) {
		original := WormsignDetectorSpec{
			Resource:  "pods",
			Condition: "true",
			NamespaceSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"env": "prod"},
			},
			Params: map[string]string{"key": "val"},
		}
		var out WormsignDetectorSpec
		original.DeepCopyInto(&out)
		out.Params["new"] = "param"
		if _, ok := original.Params["new"]; ok {
			t.Error("mutating DeepCopyInto output affected original Params")
		}
		out.NamespaceSelector.MatchLabels["new"] = "label"
		if _, ok := original.NamespaceSelector.MatchLabels["new"]; ok {
			t.Error("mutating DeepCopyInto output affected original NamespaceSelector")
		}
	})

	t.Run("WormsignDetectorSpec nil", func(t *testing.T) {
		var ds *WormsignDetectorSpec
		if ds.DeepCopy() != nil {
			t.Error("DeepCopy of nil WormsignDetectorSpec should return nil")
		}
	})

	t.Run("WormsignDetectorSpec empty", func(t *testing.T) {
		original := WormsignDetectorSpec{Resource: "pods", Condition: "true"}
		copied := original.DeepCopy()
		if copied.NamespaceSelector != nil {
			t.Error("nil NamespaceSelector should remain nil")
		}
		if copied.Params != nil {
			t.Error("nil Params should remain nil")
		}
	})

	t.Run("WormsignDetectorStatus", func(t *testing.T) {
		original := WormsignDetectorStatus{
			WormsignStatus: WormsignStatus{
				Conditions: []metav1.Condition{
					{Type: ConditionReady, Status: metav1.ConditionTrue},
				},
			},
			MatchedNamespaces: 5,
			LastFired:         &now,
			FaultEventsTotal:  42,
		}
		var out WormsignDetectorStatus
		original.DeepCopyInto(&out)
		out.Conditions[0].Status = metav1.ConditionFalse
		if original.Conditions[0].Status != metav1.ConditionTrue {
			t.Error("mutating DeepCopyInto affected original Conditions")
		}
	})

	t.Run("WormsignDetectorStatus nil", func(t *testing.T) {
		var ds *WormsignDetectorStatus
		if ds.DeepCopy() != nil {
			t.Error("DeepCopy of nil WormsignDetectorStatus should return nil")
		}
	})

	t.Run("WormsignDetectorStatus no LastFired", func(t *testing.T) {
		original := WormsignDetectorStatus{MatchedNamespaces: 3}
		copied := original.DeepCopy()
		if copied.LastFired != nil {
			t.Error("nil LastFired should remain nil")
		}
	})

	t.Run("WormsignGathererSpec", func(t *testing.T) {
		tl := int64(50)
		original := WormsignGathererSpec{
			TriggerOn: GathererTrigger{ResourceKinds: []string{"Pod"}, Detectors: []string{"PodFailed"}},
			Collect:   []CollectAction{{Type: CollectTypeLogs, TailLines: &tl}},
		}
		var out WormsignGathererSpec
		original.DeepCopyInto(&out)
		out.Collect[0].Type = CollectTypeResource
		if original.Collect[0].Type != CollectTypeLogs {
			t.Error("mutating DeepCopyInto affected original Collect")
		}
	})

	t.Run("WormsignGathererSpec nil", func(t *testing.T) {
		var gs *WormsignGathererSpec
		if gs.DeepCopy() != nil {
			t.Error("DeepCopy of nil WormsignGathererSpec should return nil")
		}
	})

	t.Run("WormsignGathererStatus nil", func(t *testing.T) {
		var gs *WormsignGathererStatus
		if gs.DeepCopy() != nil {
			t.Error("DeepCopy of nil WormsignGathererStatus should return nil")
		}
	})

	t.Run("WormsignGathererStatus DeepCopyInto", func(t *testing.T) {
		original := WormsignGathererStatus{
			WormsignStatus: WormsignStatus{
				Conditions: []metav1.Condition{{Type: ConditionReady, Status: metav1.ConditionTrue}},
			},
		}
		var out WormsignGathererStatus
		original.DeepCopyInto(&out)
		out.Conditions[0].Status = metav1.ConditionFalse
		if original.Conditions[0].Status != metav1.ConditionTrue {
			t.Error("mutating DeepCopyInto affected original")
		}
	})

	t.Run("WormsignSinkSpec", func(t *testing.T) {
		original := WormsignSinkSpec{
			Type:           SinkTypeWebhook,
			Webhook:        &WebhookConfig{URL: "https://test.com"},
			SeverityFilter: []Severity{SeverityCritical},
		}
		var out WormsignSinkSpec
		original.DeepCopyInto(&out)
		out.SeverityFilter[0] = SeverityInfo
		if original.SeverityFilter[0] != SeverityCritical {
			t.Error("mutating DeepCopyInto affected original SeverityFilter")
		}
	})

	t.Run("WormsignSinkSpec nil", func(t *testing.T) {
		var ss *WormsignSinkSpec
		if ss.DeepCopy() != nil {
			t.Error("DeepCopy of nil WormsignSinkSpec should return nil")
		}
	})

	t.Run("WormsignSinkSpec empty", func(t *testing.T) {
		original := WormsignSinkSpec{Type: SinkTypeWebhook}
		copied := original.DeepCopy()
		if copied.Webhook != nil {
			t.Error("nil Webhook should remain nil")
		}
		if copied.SeverityFilter != nil {
			t.Error("nil SeverityFilter should remain nil")
		}
	})

	t.Run("WormsignSinkStatus", func(t *testing.T) {
		original := WormsignSinkStatus{
			WormsignStatus: WormsignStatus{
				Conditions: []metav1.Condition{{Type: ConditionReady, Status: metav1.ConditionTrue}},
			},
			DeliveriesTotal:    10,
			LastDelivery:       &now,
			LastDeliveryStatus: "success",
		}
		var out WormsignSinkStatus
		original.DeepCopyInto(&out)
		out.Conditions[0].Status = metav1.ConditionFalse
		if original.Conditions[0].Status != metav1.ConditionTrue {
			t.Error("mutating DeepCopyInto affected original")
		}
	})

	t.Run("WormsignSinkStatus nil", func(t *testing.T) {
		var ss *WormsignSinkStatus
		if ss.DeepCopy() != nil {
			t.Error("DeepCopy of nil WormsignSinkStatus should return nil")
		}
	})

	t.Run("WormsignSinkStatus no LastDelivery", func(t *testing.T) {
		original := WormsignSinkStatus{DeliveriesTotal: 5}
		copied := original.DeepCopy()
		if copied.LastDelivery != nil {
			t.Error("nil LastDelivery should remain nil")
		}
	})

	t.Run("WormsignPolicySpec", func(t *testing.T) {
		later := metav1.NewTime(now.Add(3600e9))
		original := WormsignPolicySpec{
			Action:    PolicyActionExclude,
			Detectors: []string{"PodCrashLoop"},
			NamespaceSelector: &metav1.LabelSelector{
				MatchLabels: map[string]string{"env": "prod"},
			},
			Match: &PolicyMatch{
				OwnerNames: []string{"test-*"},
			},
			Schedule: &PolicySchedule{Start: now, End: later},
		}
		var out WormsignPolicySpec
		original.DeepCopyInto(&out)
		out.Detectors[0] = "Changed"
		if original.Detectors[0] != "PodCrashLoop" {
			t.Error("mutating DeepCopyInto affected original Detectors")
		}
	})

	t.Run("WormsignPolicySpec nil", func(t *testing.T) {
		var ps *WormsignPolicySpec
		if ps.DeepCopy() != nil {
			t.Error("DeepCopy of nil WormsignPolicySpec should return nil")
		}
	})

	t.Run("WormsignPolicySpec empty", func(t *testing.T) {
		original := WormsignPolicySpec{Action: PolicyActionExclude}
		copied := original.DeepCopy()
		if copied.Detectors != nil {
			t.Error("nil Detectors should remain nil")
		}
		if copied.NamespaceSelector != nil {
			t.Error("nil NamespaceSelector should remain nil")
		}
		if copied.Match != nil {
			t.Error("nil Match should remain nil")
		}
		if copied.Schedule != nil {
			t.Error("nil Schedule should remain nil")
		}
	})

	t.Run("WormsignPolicyStatus", func(t *testing.T) {
		original := WormsignPolicyStatus{
			WormsignStatus: WormsignStatus{
				Conditions: []metav1.Condition{{Type: ConditionActive, Status: metav1.ConditionTrue}},
			},
			MatchedResources: 10,
			LastEvaluated:    &now,
		}
		var out WormsignPolicyStatus
		original.DeepCopyInto(&out)
		out.Conditions[0].Status = metav1.ConditionFalse
		if original.Conditions[0].Status != metav1.ConditionTrue {
			t.Error("mutating DeepCopyInto affected original")
		}
	})

	t.Run("WormsignPolicyStatus nil", func(t *testing.T) {
		var ps *WormsignPolicyStatus
		if ps.DeepCopy() != nil {
			t.Error("DeepCopy of nil WormsignPolicyStatus should return nil")
		}
	})

	t.Run("WormsignPolicyStatus no LastEvaluated", func(t *testing.T) {
		original := WormsignPolicyStatus{MatchedResources: 3}
		copied := original.DeepCopy()
		if copied.LastEvaluated != nil {
			t.Error("nil LastEvaluated should remain nil")
		}
	})

	t.Run("WormsignStatus nil", func(t *testing.T) {
		var ws *WormsignStatus
		if ws.DeepCopy() != nil {
			t.Error("DeepCopy of nil WormsignStatus should return nil")
		}
	})

	t.Run("WormsignStatus empty conditions", func(t *testing.T) {
		original := WormsignStatus{}
		copied := original.DeepCopy()
		if copied.Conditions != nil {
			t.Error("nil Conditions should remain nil")
		}
	})
}

// TestListDeepCopyInto exercises DeepCopyInto for all List types.
func TestListDeepCopyInto(t *testing.T) {
	t.Run("WormsignDetectorList", func(t *testing.T) {
		original := WormsignDetectorList{
			Items: []WormsignDetector{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "d1"},
					Spec: WormsignDetectorSpec{
						Resource:  "pods",
						Condition: "true",
						Params:    map[string]string{"key": "val"},
					},
				},
			},
		}
		var out WormsignDetectorList
		original.DeepCopyInto(&out)
		out.Items[0].Spec.Params["new"] = "param"
		if _, ok := original.Items[0].Spec.Params["new"]; ok {
			t.Error("mutating DeepCopyInto affected original")
		}
	})

	t.Run("WormsignDetectorList nil", func(t *testing.T) {
		var dl *WormsignDetectorList
		if dl.DeepCopy() != nil {
			t.Error("DeepCopy of nil should return nil")
		}
	})

	t.Run("WormsignDetectorList empty items", func(t *testing.T) {
		original := WormsignDetectorList{}
		copied := original.DeepCopy()
		if copied.Items != nil {
			t.Error("nil Items should remain nil")
		}
	})

	t.Run("WormsignGathererList", func(t *testing.T) {
		original := WormsignGathererList{
			Items: []WormsignGatherer{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "g1"},
					Spec: WormsignGathererSpec{
						TriggerOn: GathererTrigger{ResourceKinds: []string{"Pod"}},
						Collect:   []CollectAction{{Type: CollectTypeResource}},
					},
				},
			},
		}
		var out WormsignGathererList
		original.DeepCopyInto(&out)
		out.Items[0].Spec.TriggerOn.ResourceKinds[0] = "Changed"
		if original.Items[0].Spec.TriggerOn.ResourceKinds[0] != "Pod" {
			t.Error("mutating DeepCopyInto affected original")
		}
	})

	t.Run("WormsignGathererList nil", func(t *testing.T) {
		var gl *WormsignGathererList
		if gl.DeepCopy() != nil {
			t.Error("DeepCopy of nil should return nil")
		}
	})

	t.Run("WormsignGathererList empty items", func(t *testing.T) {
		original := WormsignGathererList{}
		copied := original.DeepCopy()
		if copied.Items != nil {
			t.Error("nil Items should remain nil")
		}
	})

	t.Run("WormsignSinkList", func(t *testing.T) {
		original := WormsignSinkList{
			Items: []WormsignSink{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "s1"},
					Spec: WormsignSinkSpec{
						Type:           SinkTypeWebhook,
						SeverityFilter: []Severity{SeverityCritical},
					},
				},
			},
		}
		var out WormsignSinkList
		original.DeepCopyInto(&out)
		out.Items[0].Spec.SeverityFilter[0] = SeverityInfo
		if original.Items[0].Spec.SeverityFilter[0] != SeverityCritical {
			t.Error("mutating DeepCopyInto affected original")
		}
	})

	t.Run("WormsignSinkList nil", func(t *testing.T) {
		var sl *WormsignSinkList
		if sl.DeepCopy() != nil {
			t.Error("DeepCopy of nil should return nil")
		}
	})

	t.Run("WormsignSinkList empty items", func(t *testing.T) {
		original := WormsignSinkList{}
		copied := original.DeepCopy()
		if copied.Items != nil {
			t.Error("nil Items should remain nil")
		}
	})

	t.Run("WormsignPolicyList", func(t *testing.T) {
		original := WormsignPolicyList{
			Items: []WormsignPolicy{
				{
					ObjectMeta: metav1.ObjectMeta{Name: "p1"},
					Spec: WormsignPolicySpec{
						Action:    PolicyActionExclude,
						Detectors: []string{"PodCrashLoop"},
					},
				},
			},
		}
		var out WormsignPolicyList
		original.DeepCopyInto(&out)
		out.Items[0].Spec.Detectors[0] = "Changed"
		if original.Items[0].Spec.Detectors[0] != "PodCrashLoop" {
			t.Error("mutating DeepCopyInto affected original")
		}
	})

	t.Run("WormsignPolicyList nil", func(t *testing.T) {
		var pl *WormsignPolicyList
		if pl.DeepCopy() != nil {
			t.Error("DeepCopy of nil should return nil")
		}
	})

	t.Run("WormsignPolicyList empty items", func(t *testing.T) {
		original := WormsignPolicyList{}
		copied := original.DeepCopy()
		if copied.Items != nil {
			t.Error("nil Items should remain nil")
		}
	})
}

// TestTopLevelDeepCopyInto exercises DeepCopyInto for top-level CRD types.
func TestTopLevelDeepCopyInto(t *testing.T) {
	t.Run("WormsignDetector", func(t *testing.T) {
		original := WormsignDetector{
			ObjectMeta: metav1.ObjectMeta{Name: "d1"},
			Spec:       WormsignDetectorSpec{Resource: "pods", Condition: "true", Params: map[string]string{"k": "v"}},
		}
		var out WormsignDetector
		original.DeepCopyInto(&out)
		out.Spec.Params["new"] = "val"
		if _, ok := original.Spec.Params["new"]; ok {
			t.Error("mutating DeepCopyInto affected original")
		}
	})

	t.Run("WormsignGatherer", func(t *testing.T) {
		original := WormsignGatherer{
			ObjectMeta: metav1.ObjectMeta{Name: "g1"},
			Spec: WormsignGathererSpec{
				TriggerOn: GathererTrigger{Detectors: []string{"det"}},
				Collect:   []CollectAction{{Type: CollectTypeResource}},
			},
		}
		var out WormsignGatherer
		original.DeepCopyInto(&out)
		out.Spec.TriggerOn.Detectors[0] = "changed"
		if original.Spec.TriggerOn.Detectors[0] != "det" {
			t.Error("mutating DeepCopyInto affected original")
		}
	})

	t.Run("WormsignSink", func(t *testing.T) {
		original := WormsignSink{
			ObjectMeta: metav1.ObjectMeta{Name: "s1"},
			Spec: WormsignSinkSpec{
				Type:           SinkTypeWebhook,
				SeverityFilter: []Severity{SeverityCritical},
			},
		}
		var out WormsignSink
		original.DeepCopyInto(&out)
		out.Spec.SeverityFilter[0] = SeverityInfo
		if original.Spec.SeverityFilter[0] != SeverityCritical {
			t.Error("mutating DeepCopyInto affected original")
		}
	})

	t.Run("WormsignPolicy", func(t *testing.T) {
		original := WormsignPolicy{
			ObjectMeta: metav1.ObjectMeta{Name: "p1"},
			Spec: WormsignPolicySpec{
				Action:    PolicyActionExclude,
				Detectors: []string{"det"},
			},
		}
		var out WormsignPolicy
		original.DeepCopyInto(&out)
		out.Spec.Detectors[0] = "changed"
		if original.Spec.Detectors[0] != "det" {
			t.Error("mutating DeepCopyInto affected original")
		}
	})
}

// TestNilDeepCopyObject verifies DeepCopyObject on nil receivers for all types.
func TestNilDeepCopyObject(t *testing.T) {
	t.Run("WormsignDetector nil", func(t *testing.T) {
		var d *WormsignDetector
		if d.DeepCopy() != nil {
			t.Error("DeepCopy of nil should return nil")
		}
	})
	t.Run("WormsignGatherer nil", func(t *testing.T) {
		var g *WormsignGatherer
		if g.DeepCopy() != nil {
			t.Error("DeepCopy of nil should return nil")
		}
	})
	t.Run("WormsignSink nil", func(t *testing.T) {
		var s *WormsignSink
		if s.DeepCopy() != nil {
			t.Error("DeepCopy of nil should return nil")
		}
	})
	t.Run("WormsignPolicy nil", func(t *testing.T) {
		var p *WormsignPolicy
		if p.DeepCopy() != nil {
			t.Error("DeepCopy of nil should return nil")
		}
	})
}

// TestWormsignStatusDeepCopy verifies shared status DeepCopy.
func TestWormsignStatusDeepCopy(t *testing.T) {
	original := &WormsignStatus{
		Conditions: []metav1.Condition{
			{Type: ConditionReady, Status: metav1.ConditionTrue},
			{Type: ConditionValid, Status: metav1.ConditionTrue},
		},
	}

	copied := original.DeepCopy()
	copied.Conditions[0].Status = metav1.ConditionFalse

	if original.Conditions[0].Status != metav1.ConditionTrue {
		t.Error("modifying DeepCopy affected original WormsignStatus conditions")
	}
}

// TestSecretReferenceDeepCopy verifies SecretReference DeepCopy.
func TestSecretReferenceDeepCopy(t *testing.T) {
	original := &SecretReference{
		Name: "my-secret",
		Key:  "api-key",
	}

	copied := original.DeepCopy()
	copied.Name = "changed"

	if original.Name != "my-secret" {
		t.Error("modifying DeepCopy affected original SecretReference")
	}
}
