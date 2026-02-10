// Package analyzer — rules.go implements a rules-based analyzer backend that
// produces RCA reports by pattern-matching against gathered diagnostic data.
// It requires no LLM or external service — all analysis is algorithmic.
package analyzer

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/k8s-wormsign/k8s-wormsign/internal/model"
)

// RulesAnalyzer produces RCA reports by parsing gathered diagnostic sections
// and matching against known Kubernetes failure patterns.
type RulesAnalyzer struct {
	logger *slog.Logger
}

// NewRulesAnalyzer creates a new rules-based analyzer.
func NewRulesAnalyzer(logger *slog.Logger) (*RulesAnalyzer, error) {
	if logger == nil {
		return nil, fmt.Errorf("rules: logger must not be nil")
	}
	return &RulesAnalyzer{logger: logger}, nil
}

// Name returns the analyzer backend identifier.
func (r *RulesAnalyzer) Name() string { return "rules" }

// Healthy always returns true since the rules analyzer has no backend dependency.
func (r *RulesAnalyzer) Healthy(_ context.Context) bool { return true }

// Analyze parses the diagnostic bundle's gathered sections, extracts signals,
// and evaluates a rule chain to produce an RCA report.
func (r *RulesAnalyzer) Analyze(ctx context.Context, bundle model.DiagnosticBundle) (*model.RCAReport, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if bundle.SuperEvent == nil && bundle.FaultEvent == nil {
		return nil, fmt.Errorf("rules: bundle has neither FaultEvent nor SuperEvent")
	}

	signals := extractSignals(bundle)

	for _, rule := range rulesChain {
		if report := rule(bundle, signals); report != nil {
			r.logger.Info("rules analyzer matched",
				"fault_event_id", report.FaultEventID,
				"category", report.Category,
				"confidence", report.Confidence,
			)
			return report, nil
		}
	}

	// Unreachable — fallback always matches.
	return ruleFallback(bundle, signals), nil
}

// ---------------------------------------------------------------------------
// Signal types — normalized data extracted from DiagnosticSections
// ---------------------------------------------------------------------------

type diagnosticSignals struct {
	podPhase        string
	containerStates []containerSignal
	eventMessages   []eventSignal
	logErrors       []logErrorSignal

	// Owner chain / ReplicaSet status
	deploymentName    string
	desiredReplicas   int
	readyReplicas     int
	availableReplicas int
	rolloutCondition  string // e.g. "MinimumReplicasUnavailable"

	// PVC status
	pvcPhase     string
	storageClass string

	// Node conditions
	nodeNotReady   bool
	memoryPressure bool
	diskPressure   bool
}

type containerSignal struct {
	name             string
	ready            bool
	restartCount     int
	stateType        string // Waiting, Running, Terminated
	stateReason      string // CrashLoopBackOff, OOMKilled, Error, etc.
	exitCode         int
	lastTermReason   string
	lastTermExitCode int
	image            string
}

type eventSignal struct {
	eventType string // Warning, Normal
	reason    string // FailedScheduling, BackOff, Unhealthy, etc.
	message   string
}

type logErrorSignal struct {
	pattern string // Pattern name that matched
	snippet string // Up to 5 lines of context
}

// ---------------------------------------------------------------------------
// Signal extraction — parse each DiagnosticSection by title
// ---------------------------------------------------------------------------

func extractSignals(bundle model.DiagnosticBundle) diagnosticSignals {
	var s diagnosticSignals
	for _, section := range bundle.Sections {
		if section.Error != "" && section.Content == "" {
			continue
		}
		switch section.Title {
		case "Pod Description":
			parsePodDescription(section.Content, &s)
		case "Pod Events":
			parsePodEvents(section.Content, &s)
		case "Pod Logs":
			parsePodLogs(section.Content, &s)
		case "Owner Chain":
			parseOwnerChain(section.Content, &s)
		case "ReplicaSet Status":
			parseReplicaSetStatus(section.Content, &s)
		case "PVC Status":
			parsePVCStatus(section.Content, &s)
		case "Node Conditions":
			parseNodeConditions(section.Content, &s)
		}
	}
	return s
}

// --- Pod Description parser ---

var (
	rePodPhase         = regexp.MustCompile(`^Phase:\s+(\S+)`)
	reContainerStatus  = regexp.MustCompile(`^\s+(\S+):\s+ready=(true|false),\s+restartCount=(\d+)`)
	reStateWaiting     = regexp.MustCompile(`State:\s+Waiting\s+\(reason=(\w+)`)
	reStateTerminated  = regexp.MustCompile(`State:\s+Terminated\s+\(exitCode=(\d+),\s+reason=(\w+)`)
	reLastTermination  = regexp.MustCompile(`LastTermination:\s+exitCode=(\d+),\s+reason=(\w+)`)
	reContainerImage   = regexp.MustCompile(`^\s+Image:\s+(\S+)`)
	reContainerSection = regexp.MustCompile(`^Container:\s+(\S+)`)
)

func parsePodDescription(content string, s *diagnosticSignals) {
	lines := strings.Split(content, "\n")
	var currentContainer *containerSignal
	var specContainerName string

	for _, line := range lines {
		if m := rePodPhase.FindStringSubmatch(line); m != nil {
			s.podPhase = m[1]
			continue
		}

		// Track current container in spec section for image extraction.
		if m := reContainerSection.FindStringSubmatch(line); m != nil {
			specContainerName = m[1]
			continue
		}
		if m := reContainerImage.FindStringSubmatch(line); m != nil && specContainerName != "" {
			// Associate image with container signal if it exists.
			for i := range s.containerStates {
				if s.containerStates[i].name == specContainerName {
					s.containerStates[i].image = m[1]
				}
			}
			continue
		}

		// Container status section.
		if m := reContainerStatus.FindStringSubmatch(line); m != nil {
			rc, _ := strconv.Atoi(m[3])
			cs := containerSignal{
				name:         m[1],
				ready:        m[2] == "true",
				restartCount: rc,
			}
			s.containerStates = append(s.containerStates, cs)
			currentContainer = &s.containerStates[len(s.containerStates)-1]
			continue
		}

		if currentContainer != nil {
			if m := reStateWaiting.FindStringSubmatch(line); m != nil {
				currentContainer.stateType = "Waiting"
				currentContainer.stateReason = m[1]
				continue
			}
			if m := reStateTerminated.FindStringSubmatch(line); m != nil {
				ec, _ := strconv.Atoi(m[1])
				currentContainer.stateType = "Terminated"
				currentContainer.stateReason = m[2]
				currentContainer.exitCode = ec
				continue
			}
			if m := reLastTermination.FindStringSubmatch(line); m != nil {
				ec, _ := strconv.Atoi(m[1])
				currentContainer.lastTermReason = m[2]
				currentContainer.lastTermExitCode = ec
				continue
			}
		}
	}
}

// --- Pod Events parser ---

var reEvent = regexp.MustCompile(`^\s+\S+\s+(Warning|Normal)\s+(\w+):\s+(.*)$`)

func parsePodEvents(content string, s *diagnosticSignals) {
	for _, line := range strings.Split(content, "\n") {
		if m := reEvent.FindStringSubmatch(line); m != nil {
			s.eventMessages = append(s.eventMessages, eventSignal{
				eventType: m[1],
				reason:    m[2],
				message:   m[3],
			})
		}
	}
}

// --- Pod Logs parser (enterprise error pattern scanner) ---

var logErrorPatterns = []struct {
	name    string
	pattern *regexp.Regexp
}{
	// OOM / memory
	{"oom", regexp.MustCompile(`(?i)OOMKilled|OutOfMemory|cannot allocate memory`)},
	// Kubernetes probes
	{"probe-failure", regexp.MustCompile(`(?i)(Readiness|Liveness|Startup) probe failed`)},
	// Python
	{"python-traceback", regexp.MustCompile(`^Traceback \(most recent call last\):`)},
	{"python-exception", regexp.MustCompile(`^\w*(Error|Exception):`)},
	// Java/JVM
	{"java-exception", regexp.MustCompile(`Exception in thread`)},
	{"java-caused-by", regexp.MustCompile(`^Caused by:`)},
	{"java-oom", regexp.MustCompile(`java\.lang\.OutOfMemoryError`)},
	{"java-stacktrace", regexp.MustCompile(`^\s+at\s+[\w.$]+\([\w.]+:\d+\)`)},
	// Go
	{"go-panic", regexp.MustCompile(`^panic:`)},
	{"go-fatal", regexp.MustCompile(`^fatal error:`)},
	{"go-goroutine", regexp.MustCompile(`^goroutine \d+ \[running\]:`)},
	// Node.js
	{"node-unhandled", regexp.MustCompile(`UnhandledPromiseRejection`)},
	{"node-error", regexp.MustCompile(`^(Type|Reference|Syntax|Range)Error:`)},
	// .NET/C#
	{"dotnet-exception", regexp.MustCompile(`System\.\w+Exception|Unhandled exception`)},
	// Rust
	{"rust-panic", regexp.MustCompile(`thread '.*' panicked at`)},
	// Ruby
	{"ruby-error", regexp.MustCompile(`(RuntimeError|NoMethodError|ArgumentError|LoadError)`)},
	// Network/connectivity
	{"connection-refused", regexp.MustCompile(`(?i)connection refused|ECONNREFUSED`)},
	{"connection-reset", regexp.MustCompile(`(?i)connection reset|ECONNRESET`)},
	{"timeout", regexp.MustCompile(`(?i)timeout exceeded|dial tcp.*timeout|context deadline exceeded`)},
	{"dns-failure", regexp.MustCompile(`(?i)no such host|Name or service not known|NXDOMAIN`)},
	{"tls-error", regexp.MustCompile(`(?i)tls handshake|certificate (expired|not valid|unknown)`)},
	// Auth/permissions
	{"permission-denied", regexp.MustCompile(`(?i)permission denied|EACCES|403 Forbidden|Unauthorized`)},
	{"auth-failure", regexp.MustCompile(`(?i)authentication failed|invalid credentials|token expired`)},
	// Filesystem
	{"no-such-file", regexp.MustCompile(`(?i)No such file or directory|ENOENT`)},
	{"disk-full", regexp.MustCompile(`(?i)no space left on device|ENOSPC`)},
	{"read-only-fs", regexp.MustCompile(`(?i)read-only file system|EROFS`)},
	// Process signals
	{"segfault", regexp.MustCompile(`(?i)segmentation fault|SIGSEGV|SIGABRT|signal: killed`)},
	{"command-not-found", regexp.MustCompile(`(?i)command not found|exec format error`)},
	// Database
	{"db-connection", regexp.MustCompile(`(?i)could not connect to (database|server|postgres|mysql|redis|mongo)`)},
	{"db-error", regexp.MustCompile(`(?i)deadlock|duplicate key|constraint violation|table .* doesn't exist`)},
	// Generic log levels (lowest priority — last)
	{"level-fatal", regexp.MustCompile(`(?i)^.*\bFATAL\b`)},
	{"level-critical", regexp.MustCompile(`(?i)^.*\bCRITICAL\b`)},
	{"level-error", regexp.MustCompile(`(?i)^.*\bERROR\b`)},
}

// containerBlockHeader matches "--- Container: xxx ---" and "--- Container: xxx (previous) ---"
var containerBlockHeader = regexp.MustCompile(`^--- (?:Init )?Container:`)

func parsePodLogs(content string, s *diagnosticSignals) {
	lines := strings.Split(content, "\n")
	matched := 0
	const maxMatches = 10

	for i, line := range lines {
		if matched >= maxMatches {
			break
		}
		// Skip container block headers.
		if containerBlockHeader.MatchString(line) {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || trimmed == "(no logs)" {
			continue
		}

		for _, lp := range logErrorPatterns {
			if lp.pattern.MatchString(line) {
				snippet := extractContextWindow(lines, i, 3, 1)
				s.logErrors = append(s.logErrors, logErrorSignal{
					pattern: lp.name,
					snippet: snippet,
				})
				matched++
				break // One pattern match per line.
			}
		}
	}
}

func extractContextWindow(lines []string, matchIdx, before, after int) string {
	start := matchIdx - before
	if start < 0 {
		start = 0
	}
	end := matchIdx + after + 1
	if end > len(lines) {
		end = len(lines)
	}
	if end-start > 5 {
		end = start + 5
	}
	return strings.Join(lines[start:end], "\n")
}

// --- Owner Chain parser ---

var (
	reDeploymentOwner = regexp.MustCompile(`Deployment/(\S+)`)
	reReplicaCounts   = regexp.MustCompile(`Replicas:\s+(\d+)\s+desired,\s+(\d+)\s+ready,\s+(\d+)\s+available`)
)

func parseOwnerChain(content string, s *diagnosticSignals) {
	for _, line := range strings.Split(content, "\n") {
		if m := reDeploymentOwner.FindStringSubmatch(line); m != nil {
			s.deploymentName = m[1]
		}
		if m := reReplicaCounts.FindStringSubmatch(line); m != nil {
			s.desiredReplicas, _ = strconv.Atoi(m[1])
			s.readyReplicas, _ = strconv.Atoi(m[2])
			s.availableReplicas, _ = strconv.Atoi(m[3])
		}
	}
}

// --- ReplicaSet Status parser ---

var (
	reDeploymentLine    = regexp.MustCompile(`^Deployment:\s+\S+/(\S+)`)
	reDesiredReadyAvail = regexp.MustCompile(`Desired:\s+(\d+),\s+Ready:\s+(\d+),\s+Available:\s+(\d+)`)
	reCondition         = regexp.MustCompile(`Condition\s+(\w+):\s+\w+\s+\(Reason:\s+(\w+)\)`)
)

func parseReplicaSetStatus(content string, s *diagnosticSignals) {
	for _, line := range strings.Split(content, "\n") {
		if m := reDeploymentLine.FindStringSubmatch(line); m != nil {
			if s.deploymentName == "" {
				s.deploymentName = m[1]
			}
		}
		if m := reDesiredReadyAvail.FindStringSubmatch(line); m != nil {
			s.desiredReplicas, _ = strconv.Atoi(m[1])
			s.readyReplicas, _ = strconv.Atoi(m[2])
			s.availableReplicas, _ = strconv.Atoi(m[3])
		}
		if m := reCondition.FindStringSubmatch(line); m != nil {
			s.rolloutCondition = m[2]
		}
	}
}

// --- PVC Status parser ---

var (
	rePVCPhase   = regexp.MustCompile(`^Phase:\s+(\S+)`)
	reStorageClass = regexp.MustCompile(`^StorageClass:\s+(\S+)`)
)

func parsePVCStatus(content string, s *diagnosticSignals) {
	for _, line := range strings.Split(content, "\n") {
		if m := rePVCPhase.FindStringSubmatch(line); m != nil {
			s.pvcPhase = m[1]
		}
		if m := reStorageClass.FindStringSubmatch(line); m != nil {
			s.storageClass = m[1]
		}
	}
}

// --- Node Conditions parser ---

var reNodeCondition = regexp.MustCompile(`^\s+(\w+):\s+(True|False|Unknown)`)

func parseNodeConditions(content string, s *diagnosticSignals) {
	for _, line := range strings.Split(content, "\n") {
		if m := reNodeCondition.FindStringSubmatch(line); m != nil {
			switch m[1] {
			case "Ready":
				if m[2] != "True" {
					s.nodeNotReady = true
				}
			case "MemoryPressure":
				if m[2] == "True" {
					s.memoryPressure = true
				}
			case "DiskPressure":
				if m[2] == "True" {
					s.diskPressure = true
				}
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Rule chain
// ---------------------------------------------------------------------------

type ruleFunc func(bundle model.DiagnosticBundle, s diagnosticSignals) *model.RCAReport

var rulesChain = []ruleFunc{
	ruleOOMKilled,
	ruleNodeNotReady,
	ruleNodePressure,
	rulePVCStuckPending,
	ruleSchedulingFailed,
	ruleImagePullError,
	rulePodStuckPending,
	ruleProbeFailure,
	ruleCrashLoopWithLogs,
	ruleDeploymentRollout,
	ruleJobDeadlineExceeded,
	rulePodFailed,
	ruleGenericCrashLoop,
	ruleGenericFromLogs,
	ruleFallback,
}

// --- Rule: OOMKilled ---

func ruleOOMKilled(bundle model.DiagnosticBundle, s diagnosticSignals) *model.RCAReport {
	var oomContainer containerSignal
	found := false
	for _, cs := range s.containerStates {
		if cs.stateReason == "OOMKilled" || cs.lastTermReason == "OOMKilled" {
			oomContainer = cs
			found = true
			break
		}
		// Exit code 137 = SIGKILL, commonly OOM. Only flag if we also see
		// evidence in lastTermination or the state reason mentions it.
		if (cs.exitCode == 137 || cs.lastTermExitCode == 137) &&
			(cs.stateReason == "OOMKilled" || cs.lastTermReason == "OOMKilled") {
			oomContainer = cs
			found = true
			break
		}
	}
	if !found {
		return nil
	}

	rootCause := fmt.Sprintf("Container %q was OOM-killed (exit code 137)", oomContainer.name)
	if oomContainer.restartCount > 0 {
		rootCause += fmt.Sprintf("; %d restarts", oomContainer.restartCount)
	}

	return &model.RCAReport{
		FaultEventID: faultEventID(bundle),
		Timestamp:    time.Now().UTC(),
		RootCause:    rootCause,
		Severity:     model.SeverityCritical,
		Category:     "resources",
		Systemic:     isSuperEventSystemic(bundle),
		BlastRadius:  blastRadius(bundle),
		Remediation: []string{
			fmt.Sprintf("Increase the memory limit for container %q", oomContainer.name),
			"Profile the application's memory usage to identify leaks or unbounded caches",
			"Consider setting memory requests equal to limits to avoid node-level overcommit",
		},
		RelatedResources: bundleRelatedResources(bundle),
		Confidence:       0.9,
		RawAnalysis:      buildEvidence("OOMKilled", s),
		DiagnosticBundle: bundle,
		AnalyzerBackend:  "rules",
		TokensUsed:       model.TokenUsage{},
	}
}

// --- Rule: NodeNotReady ---

func ruleNodeNotReady(bundle model.DiagnosticBundle, s diagnosticSignals) *model.RCAReport {
	if !s.nodeNotReady {
		return nil
	}
	return &model.RCAReport{
		FaultEventID: faultEventID(bundle),
		Timestamp:    time.Now().UTC(),
		RootCause:    "Node is not ready — kubelet or container runtime may be unhealthy",
		Severity:     model.SeverityCritical,
		Category:     "node",
		Systemic:     true,
		BlastRadius:  "All pods scheduled on this node may be affected",
		Remediation: []string{
			"Check kubelet logs on the affected node: journalctl -u kubelet",
			"Verify container runtime (containerd/CRI-O) is running",
			"Check node resource usage (disk, memory, PIDs)",
			"If node is unreachable, consider cordoning and draining it",
		},
		RelatedResources: bundleRelatedResources(bundle),
		Confidence:       0.85,
		RawAnalysis:      buildEvidence("NodeNotReady", s),
		DiagnosticBundle: bundle,
		AnalyzerBackend:  "rules",
		TokensUsed:       model.TokenUsage{},
	}
}

// --- Rule: NodePressure ---

func ruleNodePressure(bundle model.DiagnosticBundle, s diagnosticSignals) *model.RCAReport {
	if !s.memoryPressure && !s.diskPressure {
		return nil
	}
	var pressures []string
	if s.memoryPressure {
		pressures = append(pressures, "MemoryPressure")
	}
	if s.diskPressure {
		pressures = append(pressures, "DiskPressure")
	}

	return &model.RCAReport{
		FaultEventID: faultEventID(bundle),
		Timestamp:    time.Now().UTC(),
		RootCause:    fmt.Sprintf("Node under pressure: %s", strings.Join(pressures, ", ")),
		Severity:     model.SeverityWarning,
		Category:     "node",
		Systemic:     true,
		BlastRadius:  "Pods on this node may be evicted",
		Remediation: []string{
			"Investigate resource consumption on the node",
			"Clean up unused images and containers: crictl rmi --prune",
			"Consider adding more nodes to distribute workload",
		},
		RelatedResources: bundleRelatedResources(bundle),
		Confidence:       0.8,
		RawAnalysis:      buildEvidence("NodePressure", s),
		DiagnosticBundle: bundle,
		AnalyzerBackend:  "rules",
		TokensUsed:       model.TokenUsage{},
	}
}

// --- Rule: PVCStuckPending ---

func rulePVCStuckPending(bundle model.DiagnosticBundle, s diagnosticSignals) *model.RCAReport {
	isPVC := s.pvcPhase == "Pending"
	if !isPVC {
		if bundle.FaultEvent != nil && bundle.FaultEvent.DetectorName == "PVCStuckBinding" {
			isPVC = true
		}
	}
	if !isPVC {
		return nil
	}

	rootCause := "PersistentVolumeClaim stuck in Pending state"
	if s.storageClass != "" {
		rootCause += fmt.Sprintf(" — StorageClass %q may not exist or has no provisioner", s.storageClass)
	}

	return &model.RCAReport{
		FaultEventID: faultEventID(bundle),
		Timestamp:    time.Now().UTC(),
		RootCause:    rootCause,
		Severity:     model.SeverityWarning,
		Category:     "storage",
		Systemic:     isSuperEventSystemic(bundle),
		BlastRadius:  blastRadius(bundle),
		Remediation: []string{
			"Verify the StorageClass exists: kubectl get storageclass",
			"Check if a dynamic provisioner is running for the storage class",
			"Ensure there is available capacity in the storage backend",
			"For static provisioning, create a matching PersistentVolume",
		},
		RelatedResources: bundleRelatedResources(bundle),
		Confidence:       0.85,
		RawAnalysis:      buildEvidence("PVCStuckPending", s),
		DiagnosticBundle: bundle,
		AnalyzerBackend:  "rules",
		TokensUsed:       model.TokenUsage{},
	}
}

// --- Rule: PodStuckPending ---

func rulePodStuckPending(bundle model.DiagnosticBundle, s diagnosticSignals) *model.RCAReport {
	isPending := s.podPhase == "Pending"
	if bundle.FaultEvent != nil && bundle.FaultEvent.DetectorName == "PodStuckPending" {
		isPending = true
	}
	if !isPending {
		return nil
	}

	rootCause := fmt.Sprintf("Pod %q stuck in Pending state", resourceName(bundle))

	// If there's a FailedScheduling event, include that context.
	for _, ev := range s.eventMessages {
		if ev.reason == "FailedScheduling" {
			rootCause += ": " + truncate(ev.message, 150)
			break
		}
	}

	return &model.RCAReport{
		FaultEventID: faultEventID(bundle),
		Timestamp:    time.Now().UTC(),
		RootCause:    rootCause,
		Severity:     model.SeverityWarning,
		Category:     "scheduling",
		Systemic:     isSuperEventSystemic(bundle),
		BlastRadius:  blastRadius(bundle),
		Remediation: []string{
			"Check node resource availability: kubectl describe nodes",
			"Review pod resource requests and node selectors/affinities",
			"Look for taints or topology constraints restricting placement",
			"Consider scaling up the cluster if all nodes are at capacity",
		},
		RelatedResources: bundleRelatedResources(bundle),
		Confidence:       0.8,
		RawAnalysis:      buildEvidence("PodStuckPending", s),
		DiagnosticBundle: bundle,
		AnalyzerBackend:  "rules",
		TokensUsed:       model.TokenUsage{},
	}
}

// --- Rule: SchedulingFailed ---

func ruleSchedulingFailed(bundle model.DiagnosticBundle, s diagnosticSignals) *model.RCAReport {
	var failMsg string
	for _, ev := range s.eventMessages {
		if ev.reason == "FailedScheduling" {
			failMsg = ev.message
			break
		}
	}
	if failMsg == "" {
		return nil
	}

	rootCause := "Pod scheduling failed: " + truncate(failMsg, 200)
	category := "scheduling"

	return &model.RCAReport{
		FaultEventID: faultEventID(bundle),
		Timestamp:    time.Now().UTC(),
		RootCause:    rootCause,
		Severity:     model.SeverityWarning,
		Category:     category,
		Systemic:     isSuperEventSystemic(bundle),
		BlastRadius:  blastRadius(bundle),
		Remediation: []string{
			"Check node resource availability: kubectl describe nodes",
			"Review pod resource requests and limits",
			"Check for taints, affinity rules, or topology constraints that may restrict placement",
			"Consider scaling up the cluster or reducing resource requests",
		},
		RelatedResources: bundleRelatedResources(bundle),
		Confidence:       0.8,
		RawAnalysis:      buildEvidence("SchedulingFailed", s),
		DiagnosticBundle: bundle,
		AnalyzerBackend:  "rules",
		TokensUsed:       model.TokenUsage{},
	}
}

// --- Rule: ImagePullError ---

func ruleImagePullError(bundle model.DiagnosticBundle, s diagnosticSignals) *model.RCAReport {
	var cs containerSignal
	found := false
	for _, c := range s.containerStates {
		if c.stateReason == "ImagePullBackOff" || c.stateReason == "ErrImagePull" || c.stateReason == "ErrImageNeverPull" {
			cs = c
			found = true
			break
		}
	}
	if !found {
		return nil
	}

	rootCause := fmt.Sprintf("Container %q failed to pull image", cs.name)
	if cs.image != "" {
		rootCause += fmt.Sprintf(" %q", cs.image)
	}

	return &model.RCAReport{
		FaultEventID: faultEventID(bundle),
		Timestamp:    time.Now().UTC(),
		RootCause:    rootCause,
		Severity:     model.SeverityWarning,
		Category:     "configuration",
		Systemic:     false,
		BlastRadius:  blastRadius(bundle),
		Remediation: []string{
			"Verify the image name and tag are correct",
			"Check image pull secrets: kubectl get secret -o yaml",
			"Test pulling the image manually: docker pull <image>",
			"If using a private registry, ensure credentials are configured",
		},
		RelatedResources: bundleRelatedResources(bundle),
		Confidence:       0.8,
		RawAnalysis:      buildEvidence("ImagePullError", s),
		DiagnosticBundle: bundle,
		AnalyzerBackend:  "rules",
		TokensUsed:       model.TokenUsage{},
	}
}

// --- Rule: ProbeFailure ---

func ruleProbeFailure(bundle model.DiagnosticBundle, s diagnosticSignals) *model.RCAReport {
	var probeMsg string
	for _, ev := range s.eventMessages {
		if ev.reason == "Unhealthy" {
			probeMsg = ev.message
			break
		}
	}
	if probeMsg == "" {
		return nil
	}

	probeType := "Health"
	msgLower := strings.ToLower(probeMsg)
	if strings.Contains(msgLower, "readiness") {
		probeType = "Readiness"
	} else if strings.Contains(msgLower, "liveness") {
		probeType = "Liveness"
	} else if strings.Contains(msgLower, "startup") {
		probeType = "Startup"
	}

	return &model.RCAReport{
		FaultEventID: faultEventID(bundle),
		Timestamp:    time.Now().UTC(),
		RootCause:    fmt.Sprintf("%s probe failing: %s", probeType, truncate(probeMsg, 200)),
		Severity:     model.SeverityWarning,
		Category:     "application",
		Systemic:     false,
		BlastRadius:  blastRadius(bundle),
		Remediation: []string{
			"Check if the application's health endpoint is responding correctly",
			"Review probe configuration (initialDelaySeconds, periodSeconds, failureThreshold)",
			"Check application logs for startup errors or dependency issues",
		},
		RelatedResources: bundleRelatedResources(bundle),
		Confidence:       0.75,
		RawAnalysis:      buildEvidence("ProbeFailure", s),
		DiagnosticBundle: bundle,
		AnalyzerBackend:  "rules",
		TokensUsed:       model.TokenUsage{},
	}
}

// --- Rule: CrashLoop with log evidence ---

func ruleCrashLoopWithLogs(bundle model.DiagnosticBundle, s diagnosticSignals) *model.RCAReport {
	if !hasCrashLoop(s) || len(s.logErrors) == 0 {
		return nil
	}

	// Find the most specific log error (first in the list, since patterns
	// are ordered by specificity).
	le := s.logErrors[0]
	category := logPatternCategory(le.pattern)

	// Extract a concise error summary from the snippet.
	errorLine := firstNonEmptyLine(le.snippet)

	var cs containerSignal
	for _, c := range s.containerStates {
		if c.stateReason == "CrashLoopBackOff" || c.stateType == "Terminated" {
			cs = c
			break
		}
	}

	rootCause := fmt.Sprintf("Container %q in CrashLoopBackOff (exit code %d)",
		cs.name, lastExitCode(cs))
	if errorLine != "" {
		rootCause += ". Last error: " + truncate(errorLine, 200)
	}

	return &model.RCAReport{
		FaultEventID: faultEventID(bundle),
		Timestamp:    time.Now().UTC(),
		RootCause:    rootCause,
		Severity:     originalSeverity(bundle),
		Category:     category,
		Systemic:     isSuperEventSystemic(bundle),
		BlastRadius:  blastRadius(bundle),
		Remediation:  crashLoopRemediation(le.pattern, cs.name),
		RelatedResources: bundleRelatedResources(bundle),
		Confidence:       0.7,
		RawAnalysis:      buildEvidence("CrashLoopWithLogs", s),
		DiagnosticBundle: bundle,
		AnalyzerBackend:  "rules",
		TokensUsed:       model.TokenUsage{},
	}
}

// --- Rule: DeploymentRollout ---

func ruleDeploymentRollout(bundle model.DiagnosticBundle, s diagnosticSignals) *model.RCAReport {
	isRolloutIssue := s.rolloutCondition == "MinimumReplicasUnavailable" ||
		(s.deploymentName != "" && s.desiredReplicas > 0 && s.readyReplicas < s.desiredReplicas)
	// Also match if this is a correlated DeploymentRollout SuperEvent.
	if bundle.SuperEvent != nil && bundle.SuperEvent.CorrelationRule == "DeploymentRollout" {
		isRolloutIssue = true
	}
	if !isRolloutIssue {
		return nil
	}

	name := s.deploymentName
	if name == "" {
		name = resourceName(bundle)
	}

	rootCause := fmt.Sprintf("Deployment %q rollout failing: %d/%d replicas ready",
		name, s.readyReplicas, s.desiredReplicas)

	return &model.RCAReport{
		FaultEventID: faultEventID(bundle),
		Timestamp:    time.Now().UTC(),
		RootCause:    rootCause,
		Severity:     originalSeverity(bundle),
		Category:     "application",
		Systemic:     true,
		BlastRadius:  fmt.Sprintf("Deployment %q — %d replicas affected", name, s.desiredReplicas-s.readyReplicas),
		Remediation: []string{
			fmt.Sprintf("Check pod status: kubectl get pods -l app=%s", name),
			"Review container logs for the failing pods",
			fmt.Sprintf("Rollback if needed: kubectl rollout undo deployment/%s", name),
			"Verify image tag, environment variables, and resource limits",
		},
		RelatedResources: bundleRelatedResources(bundle),
		Confidence:       0.7,
		RawAnalysis:      buildEvidence("DeploymentRollout", s),
		DiagnosticBundle: bundle,
		AnalyzerBackend:  "rules",
		TokensUsed:       model.TokenUsage{},
	}
}

// --- Rule: JobDeadlineExceeded ---

func ruleJobDeadlineExceeded(bundle model.DiagnosticBundle, s diagnosticSignals) *model.RCAReport {
	if bundle.FaultEvent == nil || bundle.FaultEvent.DetectorName != "JobDeadlineExceeded" {
		return nil
	}

	name := resourceName(bundle)
	return &model.RCAReport{
		FaultEventID: faultEventID(bundle),
		Timestamp:    time.Now().UTC(),
		RootCause:    fmt.Sprintf("Job %q exceeded its activeDeadlineSeconds", name),
		Severity:     model.SeverityWarning,
		Category:     "application",
		Systemic:     false,
		BlastRadius:  blastRadius(bundle),
		Remediation: []string{
			"Increase the job's activeDeadlineSeconds if the workload legitimately needs more time",
			"Optimize the job's runtime or reduce data volume",
			"Check job pod logs for errors that may have caused slow execution",
		},
		RelatedResources: bundleRelatedResources(bundle),
		Confidence:       0.7,
		RawAnalysis:      buildEvidence("JobDeadlineExceeded", s),
		DiagnosticBundle: bundle,
		AnalyzerBackend:  "rules",
		TokensUsed:       model.TokenUsage{},
	}
}

// --- Rule: PodFailed ---

func rulePodFailed(bundle model.DiagnosticBundle, s diagnosticSignals) *model.RCAReport {
	isPodFailed := s.podPhase == "Failed"
	if bundle.FaultEvent != nil && bundle.FaultEvent.DetectorName == "PodFailed" {
		isPodFailed = true
	}
	if !isPodFailed {
		return nil
	}

	rootCause := fmt.Sprintf("Pod %q entered Failed state", resourceName(bundle))
	// Try to add exit code info.
	for _, cs := range s.containerStates {
		if cs.stateType == "Terminated" && cs.exitCode != 0 {
			rootCause += fmt.Sprintf(" (container %q exited with code %d)", cs.name, cs.exitCode)
			break
		}
	}

	return &model.RCAReport{
		FaultEventID:     faultEventID(bundle),
		Timestamp:        time.Now().UTC(),
		RootCause:        rootCause,
		Severity:         originalSeverity(bundle),
		Category:         "application",
		Systemic:         isSuperEventSystemic(bundle),
		BlastRadius:      blastRadius(bundle),
		Remediation:      []string{"Check container logs for error details", "Review pod events for failure reasons"},
		RelatedResources: bundleRelatedResources(bundle),
		Confidence:       0.65,
		RawAnalysis:      buildEvidence("PodFailed", s),
		DiagnosticBundle: bundle,
		AnalyzerBackend:  "rules",
		TokensUsed:       model.TokenUsage{},
	}
}

// --- Rule: GenericCrashLoop ---

func ruleGenericCrashLoop(bundle model.DiagnosticBundle, s diagnosticSignals) *model.RCAReport {
	if !hasCrashLoop(s) {
		return nil
	}

	var cs containerSignal
	for _, c := range s.containerStates {
		if c.stateReason == "CrashLoopBackOff" || c.stateType == "Terminated" {
			cs = c
			break
		}
	}

	return &model.RCAReport{
		FaultEventID: faultEventID(bundle),
		Timestamp:    time.Now().UTC(),
		RootCause: fmt.Sprintf("Container %q in CrashLoopBackOff (exit code %d, %d restarts)",
			cs.name, lastExitCode(cs), cs.restartCount),
		Severity:    originalSeverity(bundle),
		Category:    "application",
		Systemic:    isSuperEventSystemic(bundle),
		BlastRadius: blastRadius(bundle),
		Remediation: []string{
			"Check container logs: kubectl logs <pod> -c " + cs.name + " --previous",
			"Review the container's command/entrypoint and environment variables",
			"Verify dependencies (databases, services) are reachable",
		},
		RelatedResources: bundleRelatedResources(bundle),
		Confidence:       0.5,
		RawAnalysis:      buildEvidence("GenericCrashLoop", s),
		DiagnosticBundle: bundle,
		AnalyzerBackend:  "rules",
		TokensUsed:       model.TokenUsage{},
	}
}

// --- Rule: GenericFromLogs ---

func ruleGenericFromLogs(bundle model.DiagnosticBundle, s diagnosticSignals) *model.RCAReport {
	if len(s.logErrors) == 0 {
		return nil
	}

	le := s.logErrors[0]
	errorLine := firstNonEmptyLine(le.snippet)
	rootCause := "Application error detected in logs"
	if errorLine != "" {
		rootCause += ": " + truncate(errorLine, 200)
	}

	return &model.RCAReport{
		FaultEventID:     faultEventID(bundle),
		Timestamp:        time.Now().UTC(),
		RootCause:        rootCause,
		Severity:         originalSeverity(bundle),
		Category:         logPatternCategory(le.pattern),
		Systemic:         isSuperEventSystemic(bundle),
		BlastRadius:      blastRadius(bundle),
		Remediation:      []string{"Review application logs for the full error context", "Check application configuration and dependencies"},
		RelatedResources: bundleRelatedResources(bundle),
		Confidence:       0.5,
		RawAnalysis:      buildEvidence("GenericFromLogs", s),
		DiagnosticBundle: bundle,
		AnalyzerBackend:  "rules",
		TokensUsed:       model.TokenUsage{},
	}
}

// --- Rule: Fallback (always matches) ---

func ruleFallback(bundle model.DiagnosticBundle, s diagnosticSignals) *model.RCAReport {
	rootCause := "Fault detected"
	if bundle.FaultEvent != nil {
		rootCause = fmt.Sprintf("Fault detected by %s: %s",
			bundle.FaultEvent.DetectorName, truncate(bundle.FaultEvent.Description, 200))
	}
	if bundle.SuperEvent != nil {
		rootCause = fmt.Sprintf("Correlated fault (%s) involving %d events",
			bundle.SuperEvent.CorrelationRule, len(bundle.SuperEvent.FaultEvents))
	}

	return &model.RCAReport{
		FaultEventID:     faultEventID(bundle),
		Timestamp:        time.Now().UTC(),
		RootCause:        rootCause,
		Severity:         originalSeverity(bundle),
		Category:         "unknown",
		Systemic:         isSuperEventSystemic(bundle),
		BlastRadius:      blastRadius(bundle),
		Remediation:      []string{"Review the attached diagnostic data for details"},
		RelatedResources: bundleRelatedResources(bundle),
		Confidence:       0.3,
		RawAnalysis:      buildEvidence("Fallback", s),
		DiagnosticBundle: bundle,
		AnalyzerBackend:  "rules",
		TokensUsed:       model.TokenUsage{},
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func hasCrashLoop(s diagnosticSignals) bool {
	for _, cs := range s.containerStates {
		if cs.stateReason == "CrashLoopBackOff" {
			return true
		}
	}
	return false
}

func lastExitCode(cs containerSignal) int {
	if cs.exitCode != 0 {
		return cs.exitCode
	}
	return cs.lastTermExitCode
}

func resourceName(bundle model.DiagnosticBundle) string {
	if bundle.SuperEvent != nil {
		return bundle.SuperEvent.PrimaryResource.Name
	}
	if bundle.FaultEvent != nil {
		return bundle.FaultEvent.Resource.Name
	}
	return ""
}

func isSuperEventSystemic(bundle model.DiagnosticBundle) bool {
	return bundle.SuperEvent != nil
}

func blastRadius(bundle model.DiagnosticBundle) string {
	if bundle.SuperEvent != nil {
		return fmt.Sprintf("%d resources affected", len(bundle.SuperEvent.FaultEvents))
	}
	return ""
}

func bundleRelatedResources(bundle model.DiagnosticBundle) []model.ResourceRef {
	if bundle.SuperEvent != nil {
		seen := make(map[string]bool)
		var refs []model.ResourceRef
		for _, fe := range bundle.SuperEvent.FaultEvents {
			key := fe.Resource.Kind + "/" + fe.Resource.Namespace + "/" + fe.Resource.Name
			if !seen[key] {
				seen[key] = true
				refs = append(refs, fe.Resource)
			}
		}
		return refs
	}
	return []model.ResourceRef{}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}

func firstNonEmptyLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed != "" && !containerBlockHeader.MatchString(trimmed) {
			return trimmed
		}
	}
	return ""
}

// logPatternCategory maps a log error pattern name to an RCA category.
func logPatternCategory(pattern string) string {
	switch pattern {
	case "connection-refused", "connection-reset", "timeout", "dns-failure", "tls-error",
		"db-connection":
		return "networking"
	case "permission-denied", "auth-failure", "no-such-file", "command-not-found",
		"read-only-fs":
		return "configuration"
	case "disk-full":
		return "resources"
	case "oom", "java-oom":
		return "resources"
	default:
		return "application"
	}
}

func crashLoopRemediation(pattern, containerName string) []string {
	base := []string{
		fmt.Sprintf("Check container logs: kubectl logs <pod> -c %s --previous", containerName),
	}
	switch pattern {
	case "connection-refused", "connection-reset", "timeout", "dns-failure", "tls-error",
		"db-connection":
		return append(base,
			"Verify the target service/database is running and reachable",
			"Check network policies and service endpoints",
		)
	case "permission-denied", "auth-failure":
		return append(base,
			"Verify service account permissions and RBAC roles",
			"Check mounted secrets and credentials",
		)
	case "no-such-file", "command-not-found":
		return append(base,
			"Verify the container image contains the expected binaries and files",
			"Check volume mounts and configmap references",
		)
	case "oom", "java-oom":
		return append(base,
			"Increase the memory limit for the container",
			"Profile the application's memory usage",
		)
	default:
		return append(base,
			"Review the application error and fix the underlying issue",
			"Check environment variables and configuration",
		)
	}
}

// buildEvidence produces a structured text summary of the diagnostic signals
// for the RawAnalysis field.
func buildEvidence(ruleName string, s diagnosticSignals) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Rule: %s\n", ruleName))

	if s.podPhase != "" {
		b.WriteString(fmt.Sprintf("Pod Phase: %s\n", s.podPhase))
	}

	if len(s.containerStates) > 0 {
		b.WriteString("\nContainer States:\n")
		for _, cs := range s.containerStates {
			b.WriteString(fmt.Sprintf("  %s: state=%s reason=%s exitCode=%d restarts=%d",
				cs.name, cs.stateType, cs.stateReason, cs.exitCode, cs.restartCount))
			if cs.lastTermReason != "" {
				b.WriteString(fmt.Sprintf(" lastTerm=%s(%d)", cs.lastTermReason, cs.lastTermExitCode))
			}
			b.WriteString("\n")
		}
	}

	if len(s.eventMessages) > 0 {
		b.WriteString("\nKubernetes Events:\n")
		for _, ev := range s.eventMessages {
			b.WriteString(fmt.Sprintf("  [%s] %s: %s\n", ev.eventType, ev.reason, truncate(ev.message, 200)))
		}
	}

	if len(s.logErrors) > 0 {
		b.WriteString("\nLog Error Patterns:\n")
		for _, le := range s.logErrors {
			b.WriteString(fmt.Sprintf("  Pattern: %s\n", le.pattern))
			b.WriteString("  ---\n")
			for _, line := range strings.Split(le.snippet, "\n") {
				b.WriteString("  " + line + "\n")
			}
			b.WriteString("  ---\n")
		}
	}

	if s.deploymentName != "" {
		b.WriteString(fmt.Sprintf("\nDeployment: %s (desired=%d, ready=%d, available=%d)\n",
			s.deploymentName, s.desiredReplicas, s.readyReplicas, s.availableReplicas))
		if s.rolloutCondition != "" {
			b.WriteString(fmt.Sprintf("  Condition: %s\n", s.rolloutCondition))
		}
	}

	if s.pvcPhase != "" {
		b.WriteString(fmt.Sprintf("\nPVC Phase: %s", s.pvcPhase))
		if s.storageClass != "" {
			b.WriteString(fmt.Sprintf(" (StorageClass: %s)", s.storageClass))
		}
		b.WriteString("\n")
	}

	if s.nodeNotReady || s.memoryPressure || s.diskPressure {
		b.WriteString("\nNode Conditions:")
		if s.nodeNotReady {
			b.WriteString(" NotReady")
		}
		if s.memoryPressure {
			b.WriteString(" MemoryPressure")
		}
		if s.diskPressure {
			b.WriteString(" DiskPressure")
		}
		b.WriteString("\n")
	}

	return b.String()
}
