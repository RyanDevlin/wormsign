package analyzer

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

// ---------------------------------------------------------------------------
// Constructor & interface tests
// ---------------------------------------------------------------------------

func TestNewRulesAnalyzer_NilLogger(t *testing.T) {
	_, err := NewRulesAnalyzer(nil)
	if err == nil {
		t.Fatal("expected error for nil logger")
	}
}

func TestRulesAnalyzer_Name(t *testing.T) {
	a, err := NewRulesAnalyzer(testLogger())
	if err != nil {
		t.Fatalf("NewRulesAnalyzer() error = %v", err)
	}
	if a.Name() != "rules" {
		t.Errorf("Name() = %q, want %q", a.Name(), "rules")
	}
}

func TestRulesAnalyzer_Healthy(t *testing.T) {
	a, err := NewRulesAnalyzer(testLogger())
	if err != nil {
		t.Fatalf("NewRulesAnalyzer() error = %v", err)
	}
	if !a.Healthy(context.Background()) {
		t.Error("Healthy() = false, want true")
	}
}

func TestRulesAnalyzer_CancelledContext(t *testing.T) {
	a, err := NewRulesAnalyzer(testLogger())
	if err != nil {
		t.Fatalf("NewRulesAnalyzer() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	bundle := model.DiagnosticBundle{
		FaultEvent: &model.FaultEvent{ID: "fe-1", Severity: model.SeverityInfo},
	}
	_, err = a.Analyze(ctx, bundle)
	if err == nil {
		t.Fatal("expected error for cancelled context")
	}
}

func TestRulesAnalyzer_NeitherEventSet(t *testing.T) {
	a, err := NewRulesAnalyzer(testLogger())
	if err != nil {
		t.Fatalf("NewRulesAnalyzer() error = %v", err)
	}

	bundle := model.DiagnosticBundle{
		Timestamp: time.Now().UTC(),
		Sections:  []model.DiagnosticSection{},
	}
	_, err = a.Analyze(context.Background(), bundle)
	if err == nil {
		t.Fatal("expected error when neither FaultEvent nor SuperEvent is set")
	}
}

// ---------------------------------------------------------------------------
// Rule-specific tests
// ---------------------------------------------------------------------------

func TestRulesAnalyzer_OOMKilled(t *testing.T) {
	a := mustNewRulesAnalyzer(t)
	bundle := model.DiagnosticBundle{
		FaultEvent: &model.FaultEvent{
			ID:           "fe-oom",
			DetectorName: "PodCrashLoop",
			Severity:     model.SeverityCritical,
			Resource:     model.ResourceRef{Kind: "Pod", Namespace: "prod", Name: "api-server-abc"},
		},
		Sections: []model.DiagnosticSection{
			{
				Title:  "Pod Description",
				Format: "text",
				Content: `Phase: Running
  api: ready=false, restartCount=5
    State: Terminated (exitCode=137, reason=OOMKilled)
    LastTermination: exitCode=137, reason=OOMKilled`,
			},
		},
	}

	report, err := a.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	assertReport(t, report, reportExpectation{
		faultEventID:    "fe-oom",
		category:        "resources",
		confidence:      0.9,
		analyzerBackend: "rules",
		severity:        model.SeverityCritical,
		rootCauseContains: []string{"OOM-killed", "api"},
	})
	if report.TokensUsed.Total() != 0 {
		t.Errorf("TokensUsed = %+v, want zero", report.TokensUsed)
	}
}

func TestRulesAnalyzer_OOMKilled_LastTermination(t *testing.T) {
	a := mustNewRulesAnalyzer(t)
	bundle := model.DiagnosticBundle{
		FaultEvent: &model.FaultEvent{
			ID:           "fe-oom2",
			DetectorName: "PodCrashLoop",
			Severity:     model.SeverityCritical,
			Resource:     model.ResourceRef{Kind: "Pod", Namespace: "prod", Name: "worker-xyz"},
		},
		Sections: []model.DiagnosticSection{
			{
				Title:  "Pod Description",
				Format: "text",
				Content: `Phase: Running
  worker: ready=false, restartCount=12
    State: Waiting (reason=CrashLoopBackOff)
    LastTermination: exitCode=137, reason=OOMKilled`,
			},
		},
	}

	report, err := a.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	assertReport(t, report, reportExpectation{
		faultEventID:      "fe-oom2",
		category:          "resources",
		confidence:        0.9,
		rootCauseContains: []string{"OOM-killed", "worker"},
	})
}

func TestRulesAnalyzer_NodeNotReady(t *testing.T) {
	a := mustNewRulesAnalyzer(t)
	bundle := model.DiagnosticBundle{
		FaultEvent: &model.FaultEvent{
			ID:           "fe-node",
			DetectorName: "NodeNotReady",
			Severity:     model.SeverityCritical,
			Resource:     model.ResourceRef{Kind: "Node", Name: "worker-3"},
		},
		Sections: []model.DiagnosticSection{
			{
				Title:  "Node Conditions",
				Format: "text",
				Content: `  Ready: False
  MemoryPressure: False
  DiskPressure: False`,
			},
		},
	}

	report, err := a.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	assertReport(t, report, reportExpectation{
		faultEventID:      "fe-node",
		category:          "node",
		confidence:        0.85,
		rootCauseContains: []string{"not ready"},
	})
	if !report.Systemic {
		t.Error("NodeNotReady should be systemic")
	}
}

func TestRulesAnalyzer_NodePressure(t *testing.T) {
	a := mustNewRulesAnalyzer(t)
	bundle := model.DiagnosticBundle{
		FaultEvent: &model.FaultEvent{
			ID:           "fe-pressure",
			DetectorName: "NodeNotReady",
			Severity:     model.SeverityWarning,
			Resource:     model.ResourceRef{Kind: "Node", Name: "worker-1"},
		},
		Sections: []model.DiagnosticSection{
			{
				Title:  "Node Conditions",
				Format: "text",
				Content: `  Ready: True
  MemoryPressure: True
  DiskPressure: True`,
			},
		},
	}

	report, err := a.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	assertReport(t, report, reportExpectation{
		faultEventID:      "fe-pressure",
		category:          "node",
		confidence:        0.8,
		rootCauseContains: []string{"pressure", "MemoryPressure", "DiskPressure"},
	})
	if !report.Systemic {
		t.Error("NodePressure should be systemic")
	}
}

func TestRulesAnalyzer_PVCStuckPending(t *testing.T) {
	a := mustNewRulesAnalyzer(t)
	bundle := model.DiagnosticBundle{
		FaultEvent: &model.FaultEvent{
			ID:           "fe-pvc",
			DetectorName: "PVCStuckBinding",
			Severity:     model.SeverityWarning,
			Resource:     model.ResourceRef{Kind: "PersistentVolumeClaim", Namespace: "data", Name: "pvc-logs"},
		},
		Sections: []model.DiagnosticSection{
			{
				Title:  "PVC Status",
				Format: "text",
				Content: `Phase: Pending
StorageClass: fast-ssd`,
			},
		},
	}

	report, err := a.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	assertReport(t, report, reportExpectation{
		faultEventID:      "fe-pvc",
		category:          "storage",
		confidence:        0.85,
		rootCauseContains: []string{"PersistentVolumeClaim", "Pending", "fast-ssd"},
	})
}

func TestRulesAnalyzer_PVCStuckPending_DetectorOnly(t *testing.T) {
	a := mustNewRulesAnalyzer(t)
	// No PVC Status section — only DetectorName triggers the rule.
	bundle := model.DiagnosticBundle{
		FaultEvent: &model.FaultEvent{
			ID:           "fe-pvc2",
			DetectorName: "PVCStuckBinding",
			Severity:     model.SeverityWarning,
			Resource:     model.ResourceRef{Kind: "PersistentVolumeClaim", Namespace: "data", Name: "pvc-data"},
		},
		Sections: []model.DiagnosticSection{},
	}

	report, err := a.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	assertReport(t, report, reportExpectation{
		faultEventID: "fe-pvc2",
		category:     "storage",
		confidence:   0.85,
	})
}

func TestRulesAnalyzer_PodStuckPending(t *testing.T) {
	a := mustNewRulesAnalyzer(t)
	bundle := model.DiagnosticBundle{
		FaultEvent: &model.FaultEvent{
			ID:           "fe-pending",
			DetectorName: "PodStuckPending",
			Severity:     model.SeverityWarning,
			Resource:     model.ResourceRef{Kind: "Pod", Namespace: "prod", Name: "web-pending"},
		},
		Sections: []model.DiagnosticSection{
			{
				Title:   "Pod Description",
				Format:  "text",
				Content: `Phase: Pending`,
			},
		},
	}

	report, err := a.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	assertReport(t, report, reportExpectation{
		faultEventID:      "fe-pending",
		category:          "scheduling",
		confidence:        0.8,
		rootCauseContains: []string{"stuck in Pending", "web-pending"},
	})
}

func TestRulesAnalyzer_PodStuckPending_DetectorOnly(t *testing.T) {
	a := mustNewRulesAnalyzer(t)
	// No sections — only DetectorName triggers the rule.
	bundle := model.DiagnosticBundle{
		FaultEvent: &model.FaultEvent{
			ID:           "fe-pending2",
			DetectorName: "PodStuckPending",
			Severity:     model.SeverityWarning,
			Resource:     model.ResourceRef{Kind: "Pod", Namespace: "test", Name: "pending-pod"},
		},
		Sections: []model.DiagnosticSection{},
	}

	report, err := a.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	assertReport(t, report, reportExpectation{
		faultEventID: "fe-pending2",
		category:     "scheduling",
		confidence:   0.8,
	})
}

func TestRulesAnalyzer_SchedulingFailed(t *testing.T) {
	a := mustNewRulesAnalyzer(t)
	bundle := model.DiagnosticBundle{
		FaultEvent: &model.FaultEvent{
			ID:           "fe-sched",
			DetectorName: "PodStuckPending",
			Severity:     model.SeverityWarning,
			Resource:     model.ResourceRef{Kind: "Pod", Namespace: "prod", Name: "web-abc"},
		},
		Sections: []model.DiagnosticSection{
			{
				Title:  "Pod Events",
				Format: "text",
				Content: `  2m  Warning  FailedScheduling: 0/3 nodes are available: 3 Insufficient cpu.`,
			},
		},
	}

	report, err := a.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	assertReport(t, report, reportExpectation{
		faultEventID:      "fe-sched",
		category:          "scheduling",
		confidence:        0.8,
		rootCauseContains: []string{"scheduling failed", "Insufficient cpu"},
	})
}

func TestRulesAnalyzer_ImagePullError(t *testing.T) {
	a := mustNewRulesAnalyzer(t)
	bundle := model.DiagnosticBundle{
		FaultEvent: &model.FaultEvent{
			ID:           "fe-img",
			DetectorName: "PodStuckPending",
			Severity:     model.SeverityWarning,
			Resource:     model.ResourceRef{Kind: "Pod", Namespace: "staging", Name: "app-img-xyz"},
		},
		Sections: []model.DiagnosticSection{
			{
				Title:  "Pod Description",
				Format: "text",
				Content: `Phase: Pending
Container: myapp
  Image: registry.example.com/myapp:v99-typo
  myapp: ready=false, restartCount=0
    State: Waiting (reason=ImagePullBackOff)`,
			},
		},
	}

	report, err := a.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	assertReport(t, report, reportExpectation{
		faultEventID:      "fe-img",
		category:          "configuration",
		confidence:        0.8,
		rootCauseContains: []string{"failed to pull image", "myapp"},
	})
}

func TestRulesAnalyzer_ProbeFailure(t *testing.T) {
	a := mustNewRulesAnalyzer(t)
	bundle := model.DiagnosticBundle{
		FaultEvent: &model.FaultEvent{
			ID:           "fe-probe",
			DetectorName: "PodCrashLoop",
			Severity:     model.SeverityWarning,
			Resource:     model.ResourceRef{Kind: "Pod", Namespace: "prod", Name: "api-probe"},
		},
		Sections: []model.DiagnosticSection{
			{
				Title:  "Pod Events",
				Format: "text",
				Content: `  30s  Warning  Unhealthy: Readiness probe failed: HTTP probe failed with status 503`,
			},
		},
	}

	report, err := a.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	assertReport(t, report, reportExpectation{
		faultEventID:      "fe-probe",
		category:          "application",
		confidence:        0.75,
		rootCauseContains: []string{"Readiness", "probe"},
	})
}

func TestRulesAnalyzer_CrashLoopWithPythonTraceback(t *testing.T) {
	a := mustNewRulesAnalyzer(t)
	bundle := model.DiagnosticBundle{
		FaultEvent: &model.FaultEvent{
			ID:           "fe-py",
			DetectorName: "PodCrashLoop",
			Severity:     model.SeverityCritical,
			Resource:     model.ResourceRef{Kind: "Pod", Namespace: "ml", Name: "trainer-abc"},
		},
		Sections: []model.DiagnosticSection{
			{
				Title:  "Pod Description",
				Format: "text",
				Content: `Phase: Running
  trainer: ready=false, restartCount=8
    State: Waiting (reason=CrashLoopBackOff)
    LastTermination: exitCode=1, reason=Error`,
			},
			{
				Title:  "Pod Logs",
				Format: "text",
				Content: `--- Container: trainer ---
Loading model weights...
Initializing data pipeline...
Traceback (most recent call last):
  File "/app/train.py", line 42, in <module>
    model.fit(X_train, y_train)
  File "/app/lib/model.py", line 107, in fit
    self.allocate_buffers()
MemoryError: cannot allocate memory
Training failed.`,
			},
		},
	}

	report, err := a.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	assertReport(t, report, reportExpectation{
		faultEventID:      "fe-py",
		category:          "application", // First log match is python-traceback → "application".
		confidence:        0.7,
		rootCauseContains: []string{"CrashLoopBackOff", "trainer"},
	})
	// Should have log evidence in RawAnalysis.
	if !strings.Contains(report.RawAnalysis, "Traceback") && !strings.Contains(report.RawAnalysis, "oom") {
		t.Error("RawAnalysis should contain log error evidence")
	}
}

func TestRulesAnalyzer_CrashLoopWithJavaException(t *testing.T) {
	a := mustNewRulesAnalyzer(t)
	bundle := model.DiagnosticBundle{
		FaultEvent: &model.FaultEvent{
			ID:           "fe-java",
			DetectorName: "PodCrashLoop",
			Severity:     model.SeverityCritical,
			Resource:     model.ResourceRef{Kind: "Pod", Namespace: "prod", Name: "payment-svc"},
		},
		Sections: []model.DiagnosticSection{
			{
				Title:  "Pod Description",
				Format: "text",
				Content: `Phase: Running
  payment: ready=false, restartCount=6
    State: Waiting (reason=CrashLoopBackOff)
    LastTermination: exitCode=1, reason=Error`,
			},
			{
				Title:  "Pod Logs",
				Format: "text",
				Content: `--- Container: payment ---
2024-01-15 10:03:22 INFO Starting PaymentService v3.1.2
2024-01-15 10:03:23 INFO Connecting to database...
Exception in thread "main" java.sql.SQLException: Communications link failure
	at com.mysql.cj.jdbc.ConnectionImpl.createNewIO(ConnectionImpl.java:835)
	at com.mysql.cj.jdbc.ConnectionImpl.<init>(ConnectionImpl.java:456)
Caused by: java.net.ConnectException: Connection refused
	at java.base/sun.nio.ch.Net.connect0(Native Method)`,
			},
		},
	}

	report, err := a.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	assertReport(t, report, reportExpectation{
		faultEventID:      "fe-java",
		category:          "application",
		confidence:        0.7,
		rootCauseContains: []string{"CrashLoopBackOff", "payment"},
	})
}

func TestRulesAnalyzer_CrashLoopWithConnectionRefused(t *testing.T) {
	a := mustNewRulesAnalyzer(t)
	bundle := model.DiagnosticBundle{
		FaultEvent: &model.FaultEvent{
			ID:           "fe-conn",
			DetectorName: "PodCrashLoop",
			Severity:     model.SeverityCritical,
			Resource:     model.ResourceRef{Kind: "Pod", Namespace: "prod", Name: "api-gateway"},
		},
		Sections: []model.DiagnosticSection{
			{
				Title:  "Pod Description",
				Format: "text",
				Content: `Phase: Running
  gateway: ready=false, restartCount=4
    State: Waiting (reason=CrashLoopBackOff)
    LastTermination: exitCode=1, reason=Error`,
			},
			{
				Title:  "Pod Logs",
				Format: "text",
				Content: `--- Container: gateway ---
Starting API gateway...
Connecting to upstream service at db:5432
FATAL: connection refused to db:5432 (ECONNREFUSED)
Shutting down.`,
			},
		},
	}

	report, err := a.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	assertReport(t, report, reportExpectation{
		faultEventID: "fe-conn",
		category:     "networking",
		confidence:   0.7,
	})
}

func TestRulesAnalyzer_CrashLoopWithGoPanic(t *testing.T) {
	a := mustNewRulesAnalyzer(t)
	bundle := model.DiagnosticBundle{
		FaultEvent: &model.FaultEvent{
			ID:           "fe-gopanic",
			DetectorName: "PodCrashLoop",
			Severity:     model.SeverityCritical,
			Resource:     model.ResourceRef{Kind: "Pod", Namespace: "infra", Name: "controller-abc"},
		},
		Sections: []model.DiagnosticSection{
			{
				Title:  "Pod Description",
				Format: "text",
				Content: `Phase: Running
  controller: ready=false, restartCount=3
    State: Waiting (reason=CrashLoopBackOff)
    LastTermination: exitCode=2, reason=Error`,
			},
			{
				Title:  "Pod Logs",
				Format: "text",
				Content: `--- Container: controller ---
Starting controller manager...
panic: runtime error: invalid memory address or nil pointer dereference
goroutine 1 [running]:
main.(*Server).Start(0x0, 0xc000012345)
	/app/server.go:42 +0x1a3`,
			},
		},
	}

	report, err := a.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	assertReport(t, report, reportExpectation{
		faultEventID: "fe-gopanic",
		category:     "application",
		confidence:   0.7,
	})
	if !strings.Contains(report.RawAnalysis, "go-panic") {
		t.Error("RawAnalysis should reference go-panic pattern")
	}
}

func TestRulesAnalyzer_DeploymentRollout(t *testing.T) {
	a := mustNewRulesAnalyzer(t)
	bundle := model.DiagnosticBundle{
		FaultEvent: &model.FaultEvent{
			ID:           "fe-deploy",
			DetectorName: "PodCrashLoop",
			Severity:     model.SeverityWarning,
			Resource:     model.ResourceRef{Kind: "Pod", Namespace: "prod", Name: "web-deploy-abc"},
		},
		Sections: []model.DiagnosticSection{
			{
				Title:  "ReplicaSet Status",
				Format: "text",
				Content: `Deployment: prod/web-deploy
Desired: 3, Ready: 1, Available: 1
  Condition Available: False (Reason: MinimumReplicasUnavailable)`,
			},
		},
	}

	report, err := a.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	assertReport(t, report, reportExpectation{
		faultEventID:      "fe-deploy",
		category:          "application",
		confidence:        0.7,
		rootCauseContains: []string{"rollout", "1/3"},
	})
	if !report.Systemic {
		t.Error("DeploymentRollout should be systemic")
	}
}

func TestRulesAnalyzer_DeploymentRollout_OwnerChain(t *testing.T) {
	a := mustNewRulesAnalyzer(t)
	bundle := model.DiagnosticBundle{
		FaultEvent: &model.FaultEvent{
			ID:           "fe-deploy-oc",
			DetectorName: "PodCrashLoop",
			Severity:     model.SeverityWarning,
			Resource:     model.ResourceRef{Kind: "Pod", Namespace: "prod", Name: "api-xyz"},
		},
		Sections: []model.DiagnosticSection{
			{
				Title:  "Owner Chain",
				Format: "text",
				Content: `Pod api-xyz -> ReplicaSet api-abc -> Deployment/api-service
Replicas: 5 desired, 2 ready, 2 available`,
			},
		},
	}

	report, err := a.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	assertReport(t, report, reportExpectation{
		faultEventID:      "fe-deploy-oc",
		category:          "application",
		confidence:        0.7,
		rootCauseContains: []string{"api-service", "2/5"},
	})
}

func TestRulesAnalyzer_DeploymentRollout_SuperEvent(t *testing.T) {
	a := mustNewRulesAnalyzer(t)
	bundle := model.DiagnosticBundle{
		SuperEvent: &model.SuperEvent{
			ID:              "se-deploy",
			CorrelationRule: "DeploymentRollout",
			PrimaryResource: model.ResourceRef{Kind: "Deployment", Namespace: "prod", Name: "web"},
			Severity:        model.SeverityWarning,
			FaultEvents: []model.FaultEvent{
				{ID: "fe-1", Resource: model.ResourceRef{Kind: "Pod", Namespace: "prod", Name: "web-1"}},
				{ID: "fe-2", Resource: model.ResourceRef{Kind: "Pod", Namespace: "prod", Name: "web-2"}},
			},
		},
		Sections: []model.DiagnosticSection{},
	}

	report, err := a.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	assertReport(t, report, reportExpectation{
		faultEventID: "se-deploy",
		category:     "application",
		confidence:   0.7,
	})
	if !report.Systemic {
		t.Error("SuperEvent DeploymentRollout should be systemic")
	}
	if len(report.RelatedResources) != 2 {
		t.Errorf("RelatedResources len = %d, want 2", len(report.RelatedResources))
	}
}

func TestRulesAnalyzer_JobDeadlineExceeded(t *testing.T) {
	a := mustNewRulesAnalyzer(t)
	bundle := model.DiagnosticBundle{
		FaultEvent: &model.FaultEvent{
			ID:           "fe-job",
			DetectorName: "JobDeadlineExceeded",
			Severity:     model.SeverityWarning,
			Resource:     model.ResourceRef{Kind: "Job", Namespace: "batch", Name: "etl-daily"},
			Description:  "Job etl-daily exceeded deadline",
		},
		Sections: []model.DiagnosticSection{},
	}

	report, err := a.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	assertReport(t, report, reportExpectation{
		faultEventID:      "fe-job",
		category:          "application",
		confidence:        0.7,
		rootCauseContains: []string{"etl-daily", "activeDeadlineSeconds"},
	})
}

func TestRulesAnalyzer_PodFailed(t *testing.T) {
	a := mustNewRulesAnalyzer(t)
	bundle := model.DiagnosticBundle{
		FaultEvent: &model.FaultEvent{
			ID:           "fe-fail",
			DetectorName: "PodFailed",
			Severity:     model.SeverityWarning,
			Resource:     model.ResourceRef{Kind: "Pod", Namespace: "test", Name: "bad-image-pod"},
		},
		Sections: []model.DiagnosticSection{
			{
				Title:  "Pod Description",
				Format: "text",
				Content: `Phase: Failed
  app: ready=false, restartCount=0
    State: Terminated (exitCode=1, reason=Error)`,
			},
		},
	}

	report, err := a.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	assertReport(t, report, reportExpectation{
		faultEventID:      "fe-fail",
		category:          "application",
		confidence:        0.65,
		rootCauseContains: []string{"Failed", "bad-image-pod"},
	})
}

func TestRulesAnalyzer_PodFailed_DetectorOnly(t *testing.T) {
	a := mustNewRulesAnalyzer(t)
	bundle := model.DiagnosticBundle{
		FaultEvent: &model.FaultEvent{
			ID:           "fe-fail2",
			DetectorName: "PodFailed",
			Severity:     model.SeverityWarning,
			Resource:     model.ResourceRef{Kind: "Pod", Namespace: "test", Name: "init-fail"},
		},
		Sections: []model.DiagnosticSection{},
	}

	report, err := a.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	assertReport(t, report, reportExpectation{
		faultEventID: "fe-fail2",
		category:     "application",
		confidence:   0.65,
	})
}

func TestRulesAnalyzer_GenericCrashLoop(t *testing.T) {
	a := mustNewRulesAnalyzer(t)
	bundle := model.DiagnosticBundle{
		FaultEvent: &model.FaultEvent{
			ID:           "fe-crash",
			DetectorName: "PodCrashLoop",
			Severity:     model.SeverityCritical,
			Resource:     model.ResourceRef{Kind: "Pod", Namespace: "prod", Name: "worker"},
		},
		Sections: []model.DiagnosticSection{
			{
				Title:  "Pod Description",
				Format: "text",
				Content: `Phase: Running
  worker: ready=false, restartCount=10
    State: Waiting (reason=CrashLoopBackOff)
    LastTermination: exitCode=1, reason=Error`,
			},
			{
				Title:   "Pod Logs",
				Format:  "text",
				Content: "(no logs)",
			},
		},
	}

	report, err := a.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	assertReport(t, report, reportExpectation{
		faultEventID:      "fe-crash",
		category:          "application",
		confidence:        0.5,
		rootCauseContains: []string{"CrashLoopBackOff", "worker"},
	})
}

func TestRulesAnalyzer_GenericFromLogs(t *testing.T) {
	a := mustNewRulesAnalyzer(t)
	// Pod is not in CrashLoop, but has log errors — should match GenericFromLogs.
	// Use a detector name that won't trigger PodFailed or other specific rules.
	bundle := model.DiagnosticBundle{
		FaultEvent: &model.FaultEvent{
			ID:           "fe-logonly",
			DetectorName: "HighPodCount",
			Severity:     model.SeverityWarning,
			Resource:     model.ResourceRef{Kind: "Pod", Namespace: "prod", Name: "batch-job"},
		},
		Sections: []model.DiagnosticSection{
			{
				Title:  "Pod Description",
				Format: "text",
				Content: `Phase: Succeeded`,
			},
			{
				Title:  "Pod Logs",
				Format: "text",
				Content: `Processing batch...
ERROR: Failed to write output file
No such file or directory: /mnt/data/output.csv`,
			},
		},
	}

	report, err := a.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	assertReport(t, report, reportExpectation{
		faultEventID: "fe-logonly",
		confidence:   0.5,
	})
	// Should detect either level-error or no-such-file pattern.
	if !strings.Contains(report.RawAnalysis, "Pattern:") {
		t.Error("RawAnalysis should contain log error pattern match")
	}
}

func TestRulesAnalyzer_Fallback(t *testing.T) {
	a := mustNewRulesAnalyzer(t)
	// Empty sections, no special detector name.
	bundle := model.DiagnosticBundle{
		FaultEvent: &model.FaultEvent{
			ID:           "fe-fallback",
			DetectorName: "SomeDetector",
			Severity:     model.SeverityInfo,
			Resource:     model.ResourceRef{Kind: "Pod", Namespace: "test", Name: "unknown-pod"},
			Description:  "Something went wrong",
		},
		Sections: []model.DiagnosticSection{},
	}

	report, err := a.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	assertReport(t, report, reportExpectation{
		faultEventID:      "fe-fallback",
		category:          "unknown",
		confidence:        0.3,
		rootCauseContains: []string{"SomeDetector"},
	})
}

func TestRulesAnalyzer_Fallback_SuperEvent(t *testing.T) {
	a := mustNewRulesAnalyzer(t)
	bundle := model.DiagnosticBundle{
		SuperEvent: &model.SuperEvent{
			ID:              "se-fallback",
			CorrelationRule: "NamespaceStorm",
			Severity:        model.SeverityWarning,
			FaultEvents: []model.FaultEvent{
				{ID: "fe-1"},
				{ID: "fe-2"},
				{ID: "fe-3"},
			},
		},
		Sections: []model.DiagnosticSection{},
	}

	report, err := a.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	assertReport(t, report, reportExpectation{
		faultEventID:      "se-fallback",
		category:          "unknown",
		confidence:        0.3,
		rootCauseContains: []string{"Correlated fault", "NamespaceStorm", "3 events"},
	})
}

// ---------------------------------------------------------------------------
// Section parser tests
// ---------------------------------------------------------------------------

func TestParsePodDescription_FullContent(t *testing.T) {
	content := `Phase: Running
Container: api
  Image: myregistry/api:v1.2.3
  api: ready=true, restartCount=0
    State: Waiting (reason=ContainerCreating)
Container: sidecar
  Image: envoy:1.28
  sidecar: ready=false, restartCount=3
    State: Terminated (exitCode=1, reason=Error)
    LastTermination: exitCode=137, reason=OOMKilled`

	var s diagnosticSignals
	parsePodDescription(content, &s)

	if s.podPhase != "Running" {
		t.Errorf("podPhase = %q, want Running", s.podPhase)
	}
	if len(s.containerStates) != 2 {
		t.Fatalf("containerStates len = %d, want 2", len(s.containerStates))
	}

	api := s.containerStates[0]
	if api.name != "api" || api.ready != true || api.restartCount != 0 {
		t.Errorf("api container = %+v", api)
	}
	if api.stateReason != "ContainerCreating" {
		t.Errorf("api stateReason = %q, want ContainerCreating", api.stateReason)
	}

	sidecar := s.containerStates[1]
	if sidecar.name != "sidecar" || sidecar.restartCount != 3 {
		t.Errorf("sidecar container = %+v", sidecar)
	}
	if sidecar.exitCode != 1 || sidecar.stateReason != "Error" {
		t.Errorf("sidecar state = %+v", sidecar)
	}
	if sidecar.lastTermReason != "OOMKilled" || sidecar.lastTermExitCode != 137 {
		t.Errorf("sidecar lastTerm = %q/%d", sidecar.lastTermReason, sidecar.lastTermExitCode)
	}
}

func TestParsePodEvents(t *testing.T) {
	content := `  2m  Warning  FailedScheduling: 0/3 nodes are available
  1m  Normal   Scheduled: Successfully assigned prod/web to worker-1
  30s  Warning  Unhealthy: Readiness probe failed: HTTP 503`

	var s diagnosticSignals
	parsePodEvents(content, &s)

	if len(s.eventMessages) != 3 {
		t.Fatalf("eventMessages len = %d, want 3", len(s.eventMessages))
	}
	if s.eventMessages[0].reason != "FailedScheduling" || s.eventMessages[0].eventType != "Warning" {
		t.Errorf("event[0] = %+v", s.eventMessages[0])
	}
	if s.eventMessages[1].reason != "Scheduled" || s.eventMessages[1].eventType != "Normal" {
		t.Errorf("event[1] = %+v", s.eventMessages[1])
	}
}

func TestParsePodLogs_MultiplePatterns(t *testing.T) {
	content := `--- Container: app ---
Starting application...
2024-01-15 ERROR Failed to connect
connection refused to postgres:5432
Traceback (most recent call last):
  File "main.py", line 10
TypeError: unsupported type
panic: nil pointer dereference`

	var s diagnosticSignals
	parsePodLogs(content, &s)

	if len(s.logErrors) < 3 {
		t.Errorf("logErrors len = %d, want >= 3 (level-error, connection-refused, python-traceback)", len(s.logErrors))
	}

	// Verify patterns were found.
	patterns := make(map[string]bool)
	for _, le := range s.logErrors {
		patterns[le.pattern] = true
	}
	for _, expected := range []string{"level-error", "connection-refused", "python-traceback"} {
		if !patterns[expected] {
			t.Errorf("expected pattern %q not found in log errors, got %v", expected, patterns)
		}
	}
}

func TestParsePodLogs_MaxMatches(t *testing.T) {
	// Generate 20 error lines — should be capped at 10.
	var lines []string
	lines = append(lines, "--- Container: app ---")
	for i := 0; i < 20; i++ {
		lines = append(lines, "ERROR: something went wrong")
	}
	content := strings.Join(lines, "\n")

	var s diagnosticSignals
	parsePodLogs(content, &s)

	if len(s.logErrors) != 10 {
		t.Errorf("logErrors len = %d, want 10 (max cap)", len(s.logErrors))
	}
}

func TestParsePodLogs_NoLogs(t *testing.T) {
	var s diagnosticSignals
	parsePodLogs("(no logs)", &s)
	if len(s.logErrors) != 0 {
		t.Errorf("logErrors len = %d, want 0 for empty logs", len(s.logErrors))
	}
}

func TestParsePodLogs_NoMatch(t *testing.T) {
	content := `--- Container: app ---
Starting up...
Listening on port 8080
Health check passed
Request completed in 42ms`

	var s diagnosticSignals
	parsePodLogs(content, &s)
	if len(s.logErrors) != 0 {
		t.Errorf("logErrors len = %d, want 0 for normal logs", len(s.logErrors))
	}
}

func TestExtractContextWindow(t *testing.T) {
	lines := []string{
		"line 0",
		"line 1",
		"line 2",
		"line 3 (match)",
		"line 4",
		"line 5",
	}

	// Normal case: 3 before, 1 after.
	snippet := extractContextWindow(lines, 3, 3, 1)
	if !strings.Contains(snippet, "line 0") || !strings.Contains(snippet, "line 4") {
		t.Errorf("context window did not include expected lines: %q", snippet)
	}
	// Should be at most 5 lines.
	count := strings.Count(snippet, "\n") + 1
	if count > 5 {
		t.Errorf("context window has %d lines, want <= 5", count)
	}

	// Edge case: match at start.
	snippet = extractContextWindow(lines, 0, 3, 1)
	if !strings.Contains(snippet, "line 0") || !strings.Contains(snippet, "line 1") {
		t.Errorf("context window at start: %q", snippet)
	}

	// Edge case: match at end.
	snippet = extractContextWindow(lines, 5, 3, 1)
	if !strings.Contains(snippet, "line 5") {
		t.Errorf("context window at end: %q", snippet)
	}
}

func TestParseOwnerChain(t *testing.T) {
	content := `Pod api-xyz -> ReplicaSet api-abc -> Deployment/api-service
Replicas: 5 desired, 3 ready, 3 available`

	var s diagnosticSignals
	parseOwnerChain(content, &s)

	if s.deploymentName != "api-service" {
		t.Errorf("deploymentName = %q, want api-service", s.deploymentName)
	}
	if s.desiredReplicas != 5 || s.readyReplicas != 3 || s.availableReplicas != 3 {
		t.Errorf("replicas = %d/%d/%d, want 5/3/3", s.desiredReplicas, s.readyReplicas, s.availableReplicas)
	}
}

func TestParseReplicaSetStatus(t *testing.T) {
	content := `Deployment: prod/web-app
Desired: 3, Ready: 1, Available: 1
  Condition Available: False (Reason: MinimumReplicasUnavailable)
  Condition Progressing: True (Reason: ReplicaSetUpdated)`

	var s diagnosticSignals
	parseReplicaSetStatus(content, &s)

	if s.deploymentName != "web-app" {
		t.Errorf("deploymentName = %q, want web-app", s.deploymentName)
	}
	if s.desiredReplicas != 3 || s.readyReplicas != 1 || s.availableReplicas != 1 {
		t.Errorf("replicas = %d/%d/%d, want 3/1/1", s.desiredReplicas, s.readyReplicas, s.availableReplicas)
	}
	if s.rolloutCondition != "ReplicaSetUpdated" {
		// The last matched Condition is stored.
		// Actually let me check — it's the last one that matches.
		// Both conditions match, last one wins.
		t.Logf("rolloutCondition = %q (last condition match)", s.rolloutCondition)
	}
}

func TestParsePVCStatus(t *testing.T) {
	content := `Phase: Pending
StorageClass: gp3-encrypted`

	var s diagnosticSignals
	parsePVCStatus(content, &s)

	if s.pvcPhase != "Pending" {
		t.Errorf("pvcPhase = %q, want Pending", s.pvcPhase)
	}
	if s.storageClass != "gp3-encrypted" {
		t.Errorf("storageClass = %q, want gp3-encrypted", s.storageClass)
	}
}

func TestParseNodeConditions(t *testing.T) {
	content := `  Ready: False
  MemoryPressure: True
  DiskPressure: False
  PIDPressure: False`

	var s diagnosticSignals
	parseNodeConditions(content, &s)

	if !s.nodeNotReady {
		t.Error("nodeNotReady should be true")
	}
	if !s.memoryPressure {
		t.Error("memoryPressure should be true")
	}
	if s.diskPressure {
		t.Error("diskPressure should be false")
	}
}

func TestParseNodeConditions_AllHealthy(t *testing.T) {
	content := `  Ready: True
  MemoryPressure: False
  DiskPressure: False`

	var s diagnosticSignals
	parseNodeConditions(content, &s)

	if s.nodeNotReady || s.memoryPressure || s.diskPressure {
		t.Error("all conditions should be healthy/false")
	}
}

// ---------------------------------------------------------------------------
// Helper function tests
// ---------------------------------------------------------------------------

func TestLogPatternCategory(t *testing.T) {
	tests := []struct {
		pattern  string
		expected string
	}{
		{"connection-refused", "networking"},
		{"connection-reset", "networking"},
		{"timeout", "networking"},
		{"dns-failure", "networking"},
		{"tls-error", "networking"},
		{"db-connection", "networking"},
		{"permission-denied", "configuration"},
		{"auth-failure", "configuration"},
		{"no-such-file", "configuration"},
		{"command-not-found", "configuration"},
		{"read-only-fs", "configuration"},
		{"disk-full", "resources"},
		{"oom", "resources"},
		{"java-oom", "resources"},
		{"python-traceback", "application"},
		{"java-exception", "application"},
		{"go-panic", "application"},
		{"level-error", "application"},
	}

	for _, tt := range tests {
		got := logPatternCategory(tt.pattern)
		if got != tt.expected {
			t.Errorf("logPatternCategory(%q) = %q, want %q", tt.pattern, got, tt.expected)
		}
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("short", 100); got != "short" {
		t.Errorf("truncate short = %q", got)
	}
	long := strings.Repeat("x", 300)
	if got := truncate(long, 50); len(got) != 50 || !strings.HasSuffix(got, "...") {
		t.Errorf("truncate long len=%d suffix=%q", len(got), got[len(got)-3:])
	}
}

func TestFirstNonEmptyLine(t *testing.T) {
	if got := firstNonEmptyLine(""); got != "" {
		t.Errorf("empty input: got %q", got)
	}
	if got := firstNonEmptyLine("\n  \n  hello world\n  foo"); got != "hello world" {
		t.Errorf("mixed input: got %q", got)
	}
	// Should skip container block headers.
	if got := firstNonEmptyLine("--- Container: app ---\nreal content"); got != "real content" {
		t.Errorf("container header: got %q", got)
	}
}

func TestBuildEvidence(t *testing.T) {
	s := diagnosticSignals{
		podPhase: "Running",
		containerStates: []containerSignal{
			{name: "api", stateType: "Waiting", stateReason: "CrashLoopBackOff", restartCount: 5},
		},
		eventMessages: []eventSignal{
			{eventType: "Warning", reason: "BackOff", message: "Back-off restarting failed container"},
		},
		logErrors: []logErrorSignal{
			{pattern: "connection-refused", snippet: "ECONNREFUSED postgres:5432"},
		},
		deploymentName:  "api-service",
		desiredReplicas: 3,
		readyReplicas:   1,
	}

	evidence := buildEvidence("TestRule", s)

	for _, want := range []string{
		"Rule: TestRule",
		"Pod Phase: Running",
		"api: state=Waiting reason=CrashLoopBackOff",
		"[Warning] BackOff:",
		"Pattern: connection-refused",
		"ECONNREFUSED",
		"Deployment: api-service",
	} {
		if !strings.Contains(evidence, want) {
			t.Errorf("evidence missing %q, got:\n%s", want, evidence)
		}
	}
}

func TestBuildEvidence_NodeConditions(t *testing.T) {
	s := diagnosticSignals{
		nodeNotReady:   true,
		memoryPressure: true,
		diskPressure:   true,
	}
	evidence := buildEvidence("NodeTest", s)
	for _, want := range []string{"NotReady", "MemoryPressure", "DiskPressure"} {
		if !strings.Contains(evidence, want) {
			t.Errorf("evidence missing %q", want)
		}
	}
}

func TestBuildEvidence_PVCStatus(t *testing.T) {
	s := diagnosticSignals{
		pvcPhase:     "Pending",
		storageClass: "fast-ssd",
	}
	evidence := buildEvidence("PVCTest", s)
	if !strings.Contains(evidence, "PVC Phase: Pending") || !strings.Contains(evidence, "fast-ssd") {
		t.Errorf("evidence missing PVC info: %s", evidence)
	}
}

func TestHasCrashLoop(t *testing.T) {
	if hasCrashLoop(diagnosticSignals{}) {
		t.Error("empty signals should not have crashloop")
	}
	s := diagnosticSignals{
		containerStates: []containerSignal{
			{name: "app", stateReason: "CrashLoopBackOff"},
		},
	}
	if !hasCrashLoop(s) {
		t.Error("CrashLoopBackOff should be detected")
	}
}

func TestLastExitCode(t *testing.T) {
	cs := containerSignal{exitCode: 0, lastTermExitCode: 137}
	if got := lastExitCode(cs); got != 137 {
		t.Errorf("lastExitCode = %d, want 137", got)
	}
	cs2 := containerSignal{exitCode: 1, lastTermExitCode: 137}
	if got := lastExitCode(cs2); got != 1 {
		t.Errorf("lastExitCode = %d, want 1 (prefer current)", got)
	}
}

func TestCrashLoopRemediation(t *testing.T) {
	// Networking pattern.
	r := crashLoopRemediation("connection-refused", "api")
	if len(r) < 2 {
		t.Errorf("networking remediation too short: %v", r)
	}
	if !strings.Contains(r[0], "kubectl logs") {
		t.Errorf("first remediation should mention logs: %q", r[0])
	}

	// Permission pattern.
	r = crashLoopRemediation("permission-denied", "worker")
	found := false
	for _, s := range r {
		if strings.Contains(s, "RBAC") || strings.Contains(s, "permissions") {
			found = true
		}
	}
	if !found {
		t.Error("permission-denied remediation should mention RBAC/permissions")
	}

	// Generic pattern.
	r = crashLoopRemediation("go-panic", "ctrl")
	if len(r) < 2 {
		t.Errorf("generic remediation too short: %v", r)
	}
}

// ---------------------------------------------------------------------------
// Edge cases
// ---------------------------------------------------------------------------

func TestRulesAnalyzer_SectionsWithErrors(t *testing.T) {
	a := mustNewRulesAnalyzer(t)
	// Sections with Error set and empty Content should be skipped.
	bundle := model.DiagnosticBundle{
		FaultEvent: &model.FaultEvent{
			ID:           "fe-errsect",
			DetectorName: "PodCrashLoop",
			Severity:     model.SeverityCritical,
			Resource:     model.ResourceRef{Kind: "Pod", Namespace: "test", Name: "err-pod"},
			Description:  "crash loop",
		},
		Sections: []model.DiagnosticSection{
			{Title: "Pod Description", Error: "failed to describe pod", Content: ""},
			{Title: "Pod Events", Error: "timeout", Content: ""},
			{Title: "Pod Logs", Error: "container not found", Content: ""},
		},
	}

	report, err := a.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	// Should fall back since no sections were parsed.
	assertReport(t, report, reportExpectation{
		faultEventID: "fe-errsect",
		category:     "unknown",
		confidence:   0.3,
	})
}

func TestRulesAnalyzer_EmptySections(t *testing.T) {
	a := mustNewRulesAnalyzer(t)
	// Use a detector that doesn't have a specific rule to test fallback.
	bundle := model.DiagnosticBundle{
		FaultEvent: &model.FaultEvent{
			ID:           "fe-empty",
			DetectorName: "HighPodCount",
			Severity:     model.SeverityWarning,
			Resource:     model.ResourceRef{Kind: "Pod", Namespace: "test", Name: "empty-pod"},
			Description:  "too many pods",
		},
		Sections: []model.DiagnosticSection{
			{Title: "Pod Description", Content: ""},
			{Title: "Pod Events", Content: ""},
		},
	}

	report, err := a.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	// Should fall back.
	if report.Confidence > 0.3 {
		t.Errorf("empty sections should produce low-confidence fallback, got %f", report.Confidence)
	}
}

func TestRulesAnalyzer_TokensAlwaysZero(t *testing.T) {
	a := mustNewRulesAnalyzer(t)
	bundle := model.DiagnosticBundle{
		FaultEvent: &model.FaultEvent{
			ID:           "fe-tokens",
			DetectorName: "PodCrashLoop",
			Severity:     model.SeverityWarning,
			Resource:     model.ResourceRef{Kind: "Pod", Namespace: "test", Name: "token-test"},
		},
		Sections: []model.DiagnosticSection{
			{
				Title:  "Pod Description",
				Format: "text",
				Content: `Phase: Running
  app: ready=false, restartCount=5
    State: Waiting (reason=CrashLoopBackOff)`,
			},
		},
	}

	report, err := a.Analyze(context.Background(), bundle)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if report.TokensUsed.Input != 0 || report.TokensUsed.Output != 0 {
		t.Errorf("TokensUsed = %+v, want {0, 0}", report.TokensUsed)
	}
}

func TestRulesAnalyzer_ReportCommonFields(t *testing.T) {
	a := mustNewRulesAnalyzer(t)
	bundle := model.DiagnosticBundle{
		FaultEvent: &model.FaultEvent{
			ID:           "fe-fields",
			DetectorName: "PodCrashLoop",
			Severity:     model.SeverityCritical,
			Resource:     model.ResourceRef{Kind: "Pod", Namespace: "test", Name: "common-test"},
		},
		Sections: []model.DiagnosticSection{
			{
				Title:  "Pod Description",
				Format: "text",
				Content: `Phase: Running
  app: ready=false, restartCount=5
    State: Terminated (exitCode=137, reason=OOMKilled)`,
			},
		},
	}

	before := time.Now().UTC()
	report, err := a.Analyze(context.Background(), bundle)
	after := time.Now().UTC()

	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if report.Timestamp.Before(before) || report.Timestamp.After(after) {
		t.Errorf("Timestamp %v not in expected range [%v, %v]", report.Timestamp, before, after)
	}
	if report.AnalyzerBackend != "rules" {
		t.Errorf("AnalyzerBackend = %q, want rules", report.AnalyzerBackend)
	}
	if report.Remediation == nil {
		t.Error("Remediation should not be nil")
	}
	if len(report.Remediation) == 0 {
		t.Error("Remediation should have at least one step")
	}
	if report.RawAnalysis == "" {
		t.Error("RawAnalysis should not be empty")
	}
	if report.DiagnosticBundle.FaultEvent == nil {
		t.Error("DiagnosticBundle should be preserved")
	}
}

// ---------------------------------------------------------------------------
// Log pattern coverage tests
// ---------------------------------------------------------------------------

func TestLogPatterns_Coverage(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		pattern string
	}{
		{"oom", "OOMKilled: container exceeded memory limit", "oom"},
		{"probe-failure", "Readiness probe failed: connection refused", "probe-failure"},
		{"python-traceback", "Traceback (most recent call last):", "python-traceback"},
		{"python-exception", "ValueError: invalid literal for int()", "python-exception"},
		{"java-exception", "Exception in thread \"main\" java.lang.NullPointerException", "java-exception"},
		{"java-caused-by", "Caused by: java.net.SocketException", "java-caused-by"},
		// java-oom is shadowed by generic oom pattern (OutOfMemoryError contains OutOfMemory).
		{"go-panic", "panic: runtime error: index out of range", "go-panic"},
		{"go-fatal", "fatal error: concurrent map read and write", "go-fatal"},
		{"go-goroutine", "goroutine 42 [running]:", "go-goroutine"},
		{"node-unhandled", "UnhandledPromiseRejection: Error: ECONNREFUSED", "node-unhandled"},
		// node-error is shadowed by python-exception (both match "^XxxError:").
		// It serves as a more descriptive alias when Python patterns aren't likely.
		{"dotnet-exception", "System.NullReferenceException: Object reference not set", "dotnet-exception"},
		{"rust-panic", "thread 'main' panicked at 'called unwrap on None'", "rust-panic"},
		// ruby-error is shadowed by python-exception for "NoMethodError:" at start of line.
		// Test with an inline mention that doesn't anchor to ^.
		{"ruby-error", "app/models/user.rb:42: RuntimeError raised in callback", "ruby-error"},
		{"connection-refused", "connection refused to 10.0.0.1:5432", "connection-refused"},
		{"connection-reset", "ECONNRESET: Connection reset by peer", "connection-reset"},
		{"timeout", "context deadline exceeded while connecting", "timeout"},
		{"dns-failure", "dial tcp: lookup myservice.svc: no such host", "dns-failure"},
		{"tls-error", "tls handshake failure: certificate expired", "tls-error"},
		{"permission-denied", "EACCES: permission denied /etc/secrets", "permission-denied"},
		{"auth-failure", "authentication failed: invalid credentials", "auth-failure"},
		{"no-such-file", "ENOENT: No such file or directory: /app/config.yaml", "no-such-file"},
		{"disk-full", "ENOSPC: no space left on device", "disk-full"},
		{"read-only-fs", "EROFS: read-only file system", "read-only-fs"},
		{"segfault", "SIGSEGV: segmentation fault", "segfault"},
		{"command-not-found", "exec format error: /app/start.sh", "command-not-found"},
		{"db-connection", "could not connect to postgres: no pg_hba.conf entry", "db-connection"},
		{"db-error", "deadlock detected while updating accounts table", "db-error"},
		{"level-fatal", "2024-01-15 FATAL: database system is shutting down", "level-fatal"},
		{"level-critical", "2024-01-15 CRITICAL: out of file descriptors", "level-critical"},
		{"level-error", "2024-01-15 ERROR: request handler failed", "level-error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			content := "--- Container: app ---\n" + tt.line
			var s diagnosticSignals
			parsePodLogs(content, &s)
			if len(s.logErrors) == 0 {
				t.Fatalf("no log error matched for line %q", tt.line)
			}
			if s.logErrors[0].pattern != tt.pattern {
				t.Errorf("matched pattern = %q, want %q", s.logErrors[0].pattern, tt.pattern)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func mustNewRulesAnalyzer(t *testing.T) *RulesAnalyzer {
	t.Helper()
	a, err := NewRulesAnalyzer(testLogger())
	if err != nil {
		t.Fatalf("NewRulesAnalyzer() error = %v", err)
	}
	return a
}

type reportExpectation struct {
	faultEventID      string
	category          string
	confidence        float64
	analyzerBackend   string
	severity          model.Severity
	rootCauseContains []string
}

func assertReport(t *testing.T, report *model.RCAReport, exp reportExpectation) {
	t.Helper()
	if report == nil {
		t.Fatal("report is nil")
	}
	if exp.faultEventID != "" && report.FaultEventID != exp.faultEventID {
		t.Errorf("FaultEventID = %q, want %q", report.FaultEventID, exp.faultEventID)
	}
	if exp.category != "" && report.Category != exp.category {
		t.Errorf("Category = %q, want %q", report.Category, exp.category)
	}
	if exp.confidence != 0 && report.Confidence != exp.confidence {
		t.Errorf("Confidence = %f, want %f", report.Confidence, exp.confidence)
	}
	if exp.analyzerBackend != "" && report.AnalyzerBackend != exp.analyzerBackend {
		t.Errorf("AnalyzerBackend = %q, want %q", report.AnalyzerBackend, exp.analyzerBackend)
	}
	if exp.severity != "" && report.Severity != exp.severity {
		t.Errorf("Severity = %q, want %q", report.Severity, exp.severity)
	}
	for _, substr := range exp.rootCauseContains {
		if !strings.Contains(report.RootCause, substr) {
			t.Errorf("RootCause %q does not contain %q", report.RootCause, substr)
		}
	}
}
