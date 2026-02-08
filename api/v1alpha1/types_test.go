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
