# K8s Wormsign — Requirements Specification

**Version:** 1.0.0
**Date:** February 8, 2026
**Author:** Ryan

---

## 1. Overview

K8s Wormsign is a Kubernetes-native controller that detects cluster faults, gathers diagnostic context, analyzes root causes using LLM-powered reasoning, and surfaces actionable insights to operators through configurable integrations.

The system is designed around four composable stages that form a pipeline:

```
Detect → Gather → Analyze → Surface
```

Each stage is independently extensible. Users configure which modules are active at each stage via Helm values and Custom Resource Definitions (CRDs), and can add custom modules without modifying the controller's source code.

### 1.1 Design Principles

- **Kubernetes-native:** Installed via Helm, configured via CRDs, follows the operator/controller pattern.
- **Pluggable at every stage:** Built-in modules ship out of the box; custom modules can be added via CRD alone.
- **Enterprise-ready:** Supports air-gapped and compliance-constrained environments (e.g., AWS Bedrock, Azure OpenAI) for LLM integration. No data leaves the cluster unless explicitly configured to do so.
- **Scale to the largest clusters:** Designed to handle clusters with thousands of nodes and up to 100k pods via namespace sharding, metadata-only informers, and horizontal scaling.
- **Low operational overhead:** The controller itself is lightweight at small scale (single replica) and scales horizontally with cluster size.
- **Observable:** The controller emits Prometheus metrics and structured logs for its own health and performance.

### 1.2 Terminology

| Term | Definition |
|------|-----------|
| **Detector** | A module that watches cluster state and emits **Fault Events** when a condition is met. |
| **Gatherer** | A module that collects diagnostic context relevant to a Fault Event. |
| **Analyzer** | A module that processes gathered diagnostics and produces a **Root Cause Analysis (RCA)**. |
| **Sink** | A module that delivers the RCA and diagnostics to an external system. |
| **Fault Event** | An internal struct representing a detected fault, including metadata about what was detected and why. |
| **Super-Event** | A correlated group of Fault Events sharing a common root cause (e.g., node failure causing N pod failures). |
| **Diagnostic Bundle** | A structured collection of cluster state, events, logs, and resource descriptions relevant to a Fault Event or Super-Event. |
| **RCA Report** | The final output: a structured analysis containing root cause, severity, blast radius, and remediation steps. |
| **Shard** | A set of namespaces assigned to a single controller replica for detection and gathering. |

### 1.3 Minimum Requirements

- **Kubernetes:** 1.27+
- **Architecture:** amd64, arm64
- **Helm:** 3.12+

---

## 2. Technical Decisions

This section captures architectural decisions that inform the entire implementation.

### 2.1 Language and Module Path

- **Language:** Go 1.22+
- **Module path:** `github.com/k8s-wormsign/k8s-wormsign`

### 2.2 Project Structure

```
cmd/controller/main.go
internal/
  controller/              # Top-level controller lifecycle (start, shutdown, leader election)
  detector/                # Detector interface + built-in detectors
  detector/cel/            # CEL custom detector engine
  gatherer/                # Gatherer interface + built-in gatherers
  analyzer/                # Analyzer interface + LLM backends
  analyzer/prompt/         # Default system prompt and prompt construction
  sink/                    # Sink interface + built-in sinks
  pipeline/                # Orchestrates detect → correlate → gather → analyze → sink
  pipeline/correlator/     # Pre-LLM event correlation engine
  pipeline/queue/          # Work queue wrappers per pipeline stage
  config/                  # Helm values struct, CRD watchers
  model/                   # FaultEvent, SuperEvent, DiagnosticBundle, RCAReport structs
  filter/                  # WormsignPolicy evaluation engine
  redact/                  # Log/secret redaction
  metrics/                 # Prometheus metric definitions
  shard/                   # Namespace shard assignment and coordination
  health/                  # Health and readiness probe handlers
api/
  v1alpha1/                # CRD type definitions (WormsignDetector, WormsignGatherer, etc.)
  generated/               # Generated deepcopy, client, informer code
deploy/
  helm/wormsign/           # Helm chart
    crds/                  # CRD manifests (installed before chart)
    templates/
    values.yaml
hack/                      # Code generation scripts, dev tooling
test/
  e2e/                     # End-to-end tests with envtest
  fixtures/                # Sample CRDs, Helm values for testing
```

### 2.3 Controller Framework

The controller uses **raw client-go with shared informers**, not controller-runtime/kubebuilder. Reasoning: Wormsign is event-driven (detect anomalies in a stream of watch events), not reconciliation-driven (converge desired state). controller-runtime's reconciliation loop pattern adds overhead and abstractions that fight the architecture rather than help it.

Specifically:
- `k8s.io/client-go/informers` for shared informer factories.
- `k8s.io/client-go/tools/leaderelection` for leader election.
- `k8s.io/client-go/util/workqueue` for rate-limited work queues.
- CRD types are generated with `controller-gen` from `sigs.k8s.io/controller-tools` for deepcopy and CRD manifests only.

### 2.4 Key Dependencies

| Dependency | Version | Purpose |
|-----------|---------|---------|
| `k8s.io/client-go` | v0.30+ (K8s 1.30 libs, compatible with 1.27+ clusters) | API server interaction |
| `github.com/google/cel-go` | v0.20+ | CEL expression evaluation for custom detectors |
| `log/slog` (stdlib) | Go 1.22+ | Structured logging |
| `github.com/prometheus/client_golang` | v1.19+ | Prometheus metrics |
| `sigs.k8s.io/controller-tools` | v0.14+ | CRD manifest and deepcopy generation |
| `github.com/aws/aws-sdk-go-v2` | latest | Bedrock and S3 integration |

### 2.5 Configuration Validation

- **Startup (Helm values):** Validate all configuration at process start. If invalid, crash with a clear error message. Do not start informers or detectors with invalid config.
- **Runtime (CRD changes):** CRDs are validated at admission time via OpenAPI schema validation. Additionally, the controller validates CEL expressions and template syntax when it processes CRD watch events. If a CRD is syntactically valid but semantically invalid (e.g., a CEL expression references a nonexistent field), the controller sets `status.conditions[Ready]=False` with a descriptive message and does not load the module. Previous valid configuration remains active.

### 2.6 Graceful Shutdown

On SIGTERM:

1. Stop accepting new fault events (close detection queue).
2. Stop informers.
3. Drain the correlation window (flush pending correlated events immediately).
4. Wait for in-flight gathering workers to complete (with 30s timeout).
5. Wait for in-flight analyzer calls to complete (with 60s timeout).
6. Deliver all pending sink messages (with 30s timeout).
7. Exit.

Total maximum shutdown time: 120s. The Helm chart sets `terminationGracePeriodSeconds: 150` to provide headroom.

### 2.7 Health Endpoints

| Endpoint | Semantics |
|----------|-----------|
| `/healthz` | **Liveness.** Returns 200 if the main goroutine is alive (heartbeat updated within last 30s). Failure triggers pod restart. |
| `/readyz` | **Readiness.** Returns 200 if: (a) API server is reachable, (b) informers have synced, (c) at least one detector is running. Failure removes pod from service endpoints. |
| `/metrics` | Prometheus metrics endpoint. |

### 2.8 Testing Strategy

| Level | Scope | Tooling |
|-------|-------|---------|
| Unit | Each detector, gatherer, analyzer, sink, filter evaluator, correlator | Standard `go test`, table-driven tests |
| Integration | Informer-based detection loop, CRD watch/load, pipeline end-to-end | `sigs.k8s.io/controller-runtime/pkg/envtest` (fake API server) |
| Analyzer | LLM prompt construction and response parsing | Mock HTTP server returning canned LLM responses |
| E2E | Full Helm install, fault injection, verify sink output | Kind cluster + Helm install + test harness |

Test coverage targets: 80%+ for `internal/`, with explicit test cases for every built-in detector's trigger condition and edge cases.

---

## 3. Scaling Architecture

Wormsign is designed to scale from small clusters (100 pods) to enterprise-scale clusters (100k+ pods) without architectural changes. Scaling is achieved through three mechanisms: namespace sharding, metadata-only informers, and hierarchical event correlation.

### 3.1 Namespace Sharding

At scale, a single replica cannot efficiently watch all resources across all namespaces. Wormsign supports horizontal scaling via namespace sharding: each replica is assigned a subset of namespaces and only creates informers for those namespaces.

```
┌──────────────────────────────────────────────────────────┐
│                   Coordinator (Leader Replica)            │
│  - Watches replica set membership via Lease objects       │
│  - Assigns namespace shards using consistent hashing      │
│  - Stores assignments in ConfigMap wormsign-shard-map     │
│  - Rebalances on replica scale-up/down                    │
│  - Runs cluster-scoped detectors (NodeNotReady)           │
└────────────┬────────────────────┬────────────────────────┘
             │                    │
  ┌──────────▼──────┐  ┌─────────▼───────┐
  │   Replica A      │  │   Replica B      │
  │  Namespaces:     │  │  Namespaces:     │
  │   default,       │  │   payments,      │
  │   orders,        │  │   shipping,      │
  │   inventory      │  │   analytics      │
  │                  │  │                  │
  │  Own informers   │  │  Own informers   │
  │  Own detectors   │  │  Own detectors   │
  │  Own gatherers   │  │  Own gatherers   │
  └──────────────────┘  └──────────────────┘
         │                       │
         └───────┬───────────────┘
                 ▼
        Shared analyzer pool (rate-limited)
        Shared sinks (all replicas deliver independently)
```

**Shard assignment:**
- The coordinator uses consistent hashing (jump hash or rendezvous hashing) on namespace names to assign shards. This minimizes reassignment when replicas are added or removed.
- Shard assignments are published in a ConfigMap (`wormsign-shard-map` in the controller namespace). All replicas watch this ConfigMap and adjust their informers accordingly.
- When a replica starts or the shard map changes, the replica tears down informers for namespaces it no longer owns and creates informers for newly assigned namespaces.

**Cluster-scoped resources:**
- Node-scoped detectors (`NodeNotReady`) and cluster-scoped resource watches (nodes, PVs) run only on the leader replica to avoid duplicate events.
- If the leader fails over, the new leader starts cluster-scoped detectors as part of leadership acquisition.

**Single-replica mode:**
- With one replica (the default), there is no sharding. The single replica watches all namespaces and runs all detectors. The sharding code path is still active but assigns all namespaces to the single replica. This ensures the same code path is exercised at all scales.

#### 3.1.1 Informer Strategy

Each replica creates namespace-scoped informers using `cache.Options.DefaultNamespaces` (client-go 1.27+). Only namespaces in the replica's shard are watched.

**Metadata-only informers for pods:**

Pod objects in large clusters are expensive to cache in full (3-8KB per pod × 100k pods = 300MB-800MB). Detectors only need metadata and status fields, not the full pod spec. Wormsign uses metadata-only informers (`k8s.io/client-go/metadata`) for the pod watch during detection:

| Resource | Informer Type | Approximate Per-Object Size | Rationale |
|----------|--------------|----------------------------|-----------|
| Pods | Metadata-only | ~500 bytes | Detection only needs phase, conditions, container statuses, restart counts |
| Nodes | Full object | ~2-5KB | Low cardinality (thousands, not tens of thousands), full spec needed for capacity analysis |
| Events | Full object | ~1-2KB | Content is the value; filtering by involved object |
| PVCs | Full object | ~1KB | Low cardinality |
| CRDs (Wormsign) | Full object | ~1KB | Low cardinality |

When a fault event fires and gathering begins, the gatherer fetches the full pod object with a direct `GET` call. This trades one extra API call per fault event (rare) for massive memory savings on the hot path (constant).

**Estimated memory footprint at 100k pods (3 replicas, ~33k pods per shard):**

| Component | Per-Replica Memory |
|-----------|-------------------|
| Pod metadata cache (33k pods × 500B) | ~16MB |
| Event cache (namespace-scoped, last 1h) | ~50-100MB |
| Node cache (leader only, ~5k nodes × 5KB) | ~25MB |
| PVC cache | ~5-10MB |
| CRD cache | <1MB |
| Pipeline buffers (in-flight bundles, queues) | ~50-100MB |
| **Total estimated** | **~150-250MB** |

### 3.2 Hierarchical Event Correlation

Before Fault Events reach the LLM analyzer, a local rule-based pre-correlator groups related events into Super-Events. This dramatically reduces LLM calls during incident storms and improves analysis quality by giving the LLM the full picture.

```
Raw Fault Events (e.g., 100 pod failures)
         │
         ▼
┌──────────────────────────────────────────────────────┐
│  Pre-Correlation Engine                               │
│                                                       │
│  1. Buffer events for the correlation window          │
│  2. Apply correlation rules to identify groups         │
│  3. Emit Super-Events for correlated groups            │
│  4. Pass through uncorrelated events as-is             │
└───────────────────────┬──────────────────────────────┘
                        │
                        ▼
          Correlated output (e.g., 3 Super-Events)
                        │
                        ▼
                  LLM Analyzer
```

#### 3.2.1 Built-in Correlation Rules

| Rule | Condition | Result |
|------|-----------|--------|
| `NodeCascade` | `NodeNotReady` fires AND ≥2 pod failures on that node within the correlation window | Single Super-Event with the node as primary resource. All affected pod events attached. |
| `DeploymentRollout` | ≥3 pods from the same Deployment (resolved via owner references) fail within the correlation window | Single Super-Event with the Deployment as primary resource. |
| `StorageCascade` | `PVCStuckBinding` fires AND ≥1 pod referencing that PVC fails | Single Super-Event with the PVC as primary resource. |
| `NamespaceStorm` | >20 fault events in the same namespace within the correlation window AND no other rule matched | Single Super-Event at namespace level. Prevents unbounded LLM calls. |

#### 3.2.2 Correlation Configuration

```yaml
# values.yaml
correlation:
  enabled: true
  windowDuration: 2m       # how long to buffer before evaluating rules
  rules:
    nodeCascade:
      enabled: true
      minPodFailures: 2    # minimum pod failures to correlate with a node event
    deploymentRollout:
      enabled: true
      minPodFailures: 3
    storageCascade:
      enabled: true
    namespaceStorm:
      enabled: true
      threshold: 20
```

#### 3.2.3 Super-Event Schema

```go
type SuperEvent struct {
    ID              string          // Unique ID (UUID)
    CorrelationRule string          // e.g., "NodeCascade"
    PrimaryResource ResourceRef     // The root cause resource (node, deployment, PVC)
    FaultEvents     []FaultEvent    // All correlated fault events
    Timestamp       time.Time       // When the correlation was finalized
    Severity        Severity        // Highest severity among constituent events
}
```

The analyzer receives either a single `FaultEvent` (uncorrelated) or a `SuperEvent` (correlated). In the `SuperEvent` case, the LLM is asked to produce a single RCA Report that addresses the root cause, with the blast radius reflecting all affected resources. Sinks that target individual resources (e.g., the `kubernetes-event` sink) fan out the RCA to each affected resource.

### 3.3 Pipeline Work Queues

Each pipeline stage has a dedicated work queue using `client-go`'s `workqueue` package:

```go
// Detection → Correlation
detectionQueue    workqueue.RateLimitingInterface

// Correlation → Gathering
gatheringQueue    workqueue.RateLimitingInterface

// Gathering → Analysis
analysisQueue     workqueue.TypedRateLimitingInterface[AnalysisItem]  // priority by severity

// Analysis → Sinks
sinkQueue         workqueue.Interface
```

**Worker concurrency per stage (configurable):**

| Stage | Default Workers | Behavior |
|-------|----------------|----------|
| Detection | 1 per replica | Informer callbacks enqueue; single worker evaluates detector conditions |
| Correlation | 1 per replica | Stateful (time-windowed accumulation). Must be single-threaded. |
| Gathering | 5 per replica | I/O-bound (API calls for logs, events, pod details). Gatherers within a single event run concurrently via `errgroup`. |
| Analysis | 2 per replica | Bounded by LLM throughput. Critical-severity events are dequeued first. |
| Sink delivery | 3 per replica | Per-sink concurrency. One sink's failure does not block others. |

### 3.4 Resource Sizing Guide

| Cluster Size | Replicas | Per-Replica Requests | Per-Replica Limits |
|-------------|----------|---------------------|-------------------|
| Small (<1k pods) | 1 | 100m CPU / 128Mi mem | 500m CPU / 512Mi mem |
| Medium (1k–10k pods) | 1–2 | 250m CPU / 256Mi mem | 1 CPU / 1Gi mem |
| Large (10k–50k pods) | 2–3 | 500m CPU / 512Mi mem | 2 CPU / 2Gi mem |
| XL (50k–100k pods) | 3–5 | 500m CPU / 1Gi mem | 2 CPU / 4Gi mem |

### 3.5 HorizontalPodAutoscaler Support

The controller exposes a custom metric for autoscaling:

```yaml
wormsign_pipeline_queue_depth{stage="gathering|analysis|sink"}
```

Example HPA configuration:

```yaml
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
metadata:
  name: wormsign
  namespace: wormsign-system
spec:
  scaleTargetRef:
    apiVersion: apps/v1
    kind: Deployment
    name: wormsign-controller
  minReplicas: 2
  maxReplicas: 5
  metrics:
    - type: Pods
      pods:
        metric:
          name: wormsign_pipeline_queue_depth
        target:
          type: AverageValue
          averageValue: "50"
```

When replicas scale up or down, the coordinator rebalances namespace shards automatically. New replicas begin watching their assigned namespaces within seconds of joining.

### 3.6 Leader Election

Leader election is always enabled, even for single-replica deployments. This ensures the HA upgrade path requires no configuration changes.

- Implementation: `client-go` `leaderelection` package with a Lease object in the controller namespace.
- Lease name: `wormsign-leader`
- Lease duration: 15s, renew deadline: 10s, retry period: 2s.
- Only the leader runs: cluster-scoped detectors (NodeNotReady), namespace shard coordination, and the pre-correlation engine's global state.
- All replicas run: namespace-scoped detectors for their shard, gathering, analysis, and sink delivery.

---

## 4. Custom Resource Definitions

Wormsign uses CRDs for all user-extensible configuration. CRDs provide schema validation at admission time, status reporting, namespaced RBAC, and `kubectl` discoverability.

### 4.1 CRD Overview

| CRD | API Group | Scope | Purpose |
|-----|-----------|-------|---------|
| `WormsignDetector` | `wormsign.io/v1alpha1` | Namespaced | Custom CEL-based fault detectors |
| `WormsignGatherer` | `wormsign.io/v1alpha1` | Namespaced | Custom diagnostic data collection |
| `WormsignSink` | `wormsign.io/v1alpha1` | Namespaced | Custom sink integrations |
| `WormsignPolicy` | `wormsign.io/v1alpha1` | Namespaced | Exclusion filters and suppression rules |

All CRDs include a `status` subresource with standardized conditions:

```go
type WormsignStatus struct {
    Conditions []metav1.Condition  // "Ready", "Valid", "Active"
    // CRD-specific status fields below
}
```

**Namespaced CRDs and scope:** A `WormsignDetector` created in namespace `payments` only watches resources in the `payments` namespace by default. This lets teams create their own detectors without affecting other namespaces and without needing access to the `wormsign-system` namespace. To create a detector that watches across namespaces, it must be created in the `wormsign-system` namespace with an explicit `namespaceSelector`.

**No FaultEvent CRD.** Fault events and RCA reports are not persisted as CRDs. They are high-volume and ephemeral. Persisting them in etcd would create storage pressure at scale. The log sink (always enabled) and S3 sink are the archival strategies.

### 4.2 WormsignDetector

Defines a custom detector using CEL expressions.

```yaml
apiVersion: wormsign.io/v1alpha1
kind: WormsignDetector
metadata:
  name: high-restart-rate
  namespace: wormsign-system    # cross-namespace scope
spec:
  description: "Fires when a pod has restarted more than N times in the last hour"
  resource: pods
  namespaceSelector:
    matchLabels:
      wormsign.io/watch: "true"
  condition: >
    pod.status.containerStatuses.exists(c,
      c.restartCount > params.maxRestarts &&
      timestamp(c.state.running.startedAt) > now() - duration("1h")
    )
  params:
    maxRestarts: "10"
  severity: warning
  cooldown: 1h
status:
  conditions:
    - type: Ready
      status: "True"
      message: "CEL expression compiled successfully"
    - type: Active
      status: "True"
      message: "Watching 12 namespaces"
  matchedNamespaces: 12
  lastFired: "2026-02-08T14:30:00Z"
  faultEventsTotal: 47
```

**CEL environment** exposes:
- The resource object (e.g., `pod`, `node`, `pvc`) with full Kubernetes API fields
- `now()` — current timestamp
- `duration(string)` — duration parsing
- `timestamp(string)` — timestamp parsing
- `params` — user-defined parameters from the spec (all string values, type-coerced in CEL)
- Standard CEL functions: `size()`, `exists()`, `filter()`, `map()`, `matches()`, `startsWith()`, `endsWith()`

**CEL security:** CEL expressions are evaluated in a sandboxed environment. They cannot perform I/O, call external services, or access the filesystem. Expression evaluation has a cost budget (configurable, default: 1000 CEL cost units) to prevent runaway expressions.

### 4.3 WormsignGatherer

Defines a custom data collection module.

```yaml
apiVersion: wormsign.io/v1alpha1
kind: WormsignGatherer
metadata:
  name: istio-proxy-config
  namespace: payments
spec:
  description: "Collects Istio sidecar configuration for networking diagnosis"
  triggerOn:
    resourceKinds: ["Pod"]
    detectors: ["PodCrashLoop", "PodFailed"]
  collect:
    - type: resource
      apiVersion: v1
      resource: pods
      name: "{resource.name}"
      namespace: "{resource.namespace}"
      jsonPath: ".metadata.annotations.sidecar\\.istio\\.io/*"
    - type: resource
      apiVersion: networking.istio.io/v1
      resource: destinationrules
      namespace: "{resource.namespace}"
      listAll: true
    - type: logs
      container: istio-proxy
      tailLines: 50
  # No 'http' collection type. Custom gatherers are limited to
  # Kubernetes API operations using the controller's RBAC.
status:
  conditions:
    - type: Ready
      status: "True"
```

**Collection types:**
- `resource` — fetches a Kubernetes resource using the controller's API client. Supports `jsonPath` for field extraction and `listAll` for listing all resources of a type.
- `logs` — fetches container logs. Supports `container`, `tailLines`, and `previous` fields.

Template variables: `{resource.name}`, `{resource.namespace}`, `{resource.kind}`, `{resource.uid}`, `{node.name}`.

**Removed `http` and `kubectl` types.** For security, custom gatherers cannot make arbitrary HTTP calls or execute shell commands. They are limited to Kubernetes API operations executed through the controller's API client, which is bounded by the controller's RBAC. This prevents privilege escalation.

### 4.4 WormsignSink

Defines a custom sink using webhook delivery with Go templates.

```yaml
apiVersion: wormsign.io/v1alpha1
kind: WormsignSink
metadata:
  name: teams-notification
  namespace: wormsign-system
spec:
  description: "Posts to Microsoft Teams via incoming webhook"
  type: webhook
  webhook:
    url:
      secretRef:
        name: wormsign-sink-secrets
        key: teams-webhook-url
    method: POST
    headers:
      Content-Type: application/json
    bodyTemplate: |
      {
        "@type": "MessageCard",
        "themeColor": "{{ if eq .Severity "critical" }}FF0000{{ else }}FFA500{{ end }}",
        "summary": "K8s Wormsign: {{ .RootCause }}",
        "sections": [{
          "activityTitle": "{{ .Resource.Kind }}/{{ .Resource.Name }}",
          "facts": [
            { "name": "Namespace", "value": "{{ .Resource.Namespace }}" },
            { "name": "Root Cause", "value": "{{ .RootCause }}" },
            { "name": "Category", "value": "{{ .Category }}" },
            { "name": "Systemic", "value": "{{ .Systemic }}" }
          ]
        }]
      }
  severityFilter: [critical, warning]
status:
  conditions:
    - type: Ready
      status: "True"
  deliveriesTotal: 142
  lastDelivery: "2026-02-08T14:30:00Z"
  lastDeliveryStatus: success
```

Custom sinks use Go `text/template` syntax with the RCA Report fields available as template variables. Webhook URLs are validated against a configurable allowlist (`sinks.webhook.allowedDomains` in Helm values) to prevent SSRF.

### 4.5 WormsignPolicy

Defines exclusion filters and suppression rules. See Section 6 for full details.

---

## 5. Pipeline Architecture

### 5.1 Stage 1 — Detection

Detectors watch Kubernetes state and emit Fault Events when anomalous conditions are identified. Multiple detectors run concurrently. Each detector is independently configurable and can be enabled/disabled.

#### 5.1.1 Built-in Detectors

| Detector | Trigger Condition | Default |
|----------|-------------------|---------|
| `PodStuckPending` | Pod remains in `Pending` phase beyond a configurable threshold (default: 15m). | Enabled |
| `PodCrashLoop` | Pod enters `CrashLoopBackOff` with restart count exceeding a configurable threshold (default: 3). | Enabled |
| `PodFailed` | Pod phase transitions to `Failed` or any container exits non-zero. | Enabled |
| `NodeNotReady` | Node condition `Ready` transitions to `False` or `Unknown` for longer than a configurable threshold (default: 5m). Runs on leader replica only. | Enabled |
| `PVCStuckBinding` | PersistentVolumeClaim remains in `Pending` state beyond a configurable threshold (default: 10m). | Disabled |
| `HighPodCount` | Number of pods in a namespace exceeds a configurable threshold. | Disabled |
| `JobDeadlineExceeded` | A Job's `activeDeadlineSeconds` is reached without completion. | Disabled |

#### 5.1.2 Detector Configuration

Each built-in detector is configured via Helm values:

```yaml
# values.yaml
detectors:
  podStuckPending:
    enabled: true
    threshold: 15m
  podCrashLoop:
    enabled: true
    threshold: 3
  podFailed:
    enabled: true
    ignoreExitCodes: [0]
  nodeNotReady:
    enabled: true
    threshold: 5m
  pvcStuckBinding:
    enabled: false
    threshold: 10m
  highPodCount:
    enabled: false
    threshold: 200
  jobDeadlineExceeded:
    enabled: false
```

Common configuration fields across all detectors:
- `enabled` (bool): Whether the detector is active.
- `threshold` (varies): The trigger condition parameter (duration, count, etc.).
- `cooldown` (duration, default: 30m): Minimum time between Fault Events for the same resource, to prevent alert storms.

Namespace and resource filtering is handled by `WormsignPolicy` CRDs (Section 6), not by per-detector selectors. This centralizes filter management.

#### 5.1.3 Fault Event Schema

```go
type FaultEvent struct {
    ID            string            // Unique event ID (UUID)
    DetectorName  string            // e.g., "PodStuckPending"
    Severity      Severity          // Critical, Warning, Info
    Timestamp     time.Time
    Resource      ResourceRef       // Kind, Namespace, Name, UID
    Description   string            // Human-readable summary
    Labels        map[string]string // Propagated from the triggering resource
    Annotations   map[string]string // Detector-specific metadata
}

type ResourceRef struct {
    Kind      string
    Namespace string
    Name      string
    UID       string
}

type Severity string

const (
    SeverityCritical Severity = "critical"
    SeverityWarning  Severity = "warning"
    SeverityInfo     Severity = "info"
)
```

#### 5.1.4 Deduplication and Suppression

- Fault Events are deduplicated by `(DetectorName, Resource.UID)` within the cooldown window. Cooldown state is in-memory and resets on controller restart. This is acceptable for v1 — a controller restart (rare) may cause a brief burst of re-detected events that the correlation engine and LLM batching will absorb.
- If the same resource triggers multiple detectors (e.g., `PodStuckPending` and `PVCStuckBinding`), both events fire but are correlated by the correlation engine (Section 3.2) for downstream grouping.
- Namespace-level suppression via annotation: `wormsign.io/suppress: "true"` on a namespace object suppresses all detection in that namespace. Intended for maintenance windows.

---

### 5.2 Stage 2 — Gathering

When a Fault Event (or Super-Event) is dequeued by a gathering worker, the Gathering stage collects relevant diagnostic data. Gatherers are scoped to the context of the fault — resource kind, namespace, and related resources.

For Super-Events, gathering runs for the primary resource and a representative sample of affected resources (configurable, default: up to 5 affected resources) to bound gathering cost.

#### 5.2.1 Built-in Gatherers

| Gatherer | Collects | Triggered By |
|----------|----------|-------------|
| `PodDescribe` | Full pod spec, status, conditions, container statuses, exit codes, resource requests/limits. Fetches full object via direct GET (since detection uses metadata-only informer). | Any pod-related Fault Event |
| `PodEvents` | Kubernetes events for the pod (last 1h) | Any pod-related Fault Event |
| `PodLogs` | Last N lines (configurable, default: 100) of container logs, including previous terminated container | Pod failures / crash loops |
| `NodeConditions` | Node status, conditions, allocatable vs capacity, taints | Pod scheduling failures, node issues |
| `PVCStatus` | PVC phase, bound PV details, PV events, EBS volume ID, AZ | PVC-related or pod-scheduling Fault Events |
| `KarpenterState` | NodePool status, NodeClaim conditions, Karpenter events | Pod scheduling failures (when Karpenter CRDs are detected at startup) |
| `NamespaceEvents` | Kubernetes events in the affected namespace (last 30m) | All Fault Events |
| `ReplicaSetStatus` | Owning ReplicaSet/Deployment status, replica counts, rollout conditions | Pod failures |
| `OwnerChain` | Walk ownerReferences to find the top-level controller (Deployment, StatefulSet, Job, DaemonSet) | All pod-related Fault Events |

#### 5.2.2 Gatherer Configuration

```yaml
# values.yaml
gatherers:
  podLogs:
    enabled: true
    tailLines: 100
    includePrevious: true
  karpenterState:
    enabled: true         # auto-disabled at startup if Karpenter CRDs not found
  nodeConditions:
    enabled: true
    includeAllocatable: true
  superEvent:
    maxAffectedResources: 5  # max resources to gather for in a Super-Event
```

#### 5.2.3 Diagnostic Bundle Schema

```go
type DiagnosticBundle struct {
    FaultEvent    *FaultEvent    // Set if single event
    SuperEvent    *SuperEvent    // Set if correlated event
    Timestamp     time.Time
    Sections      []DiagnosticSection
}

type DiagnosticSection struct {
    GathererName  string  // e.g., "PodEvents"
    Title         string  // Human-readable title
    Content       string  // The collected data as structured text
    Format        string  // "text", "json", "yaml"
    Error         string  // Non-empty if gathering failed
}
```

Gatherers that fail (e.g., insufficient RBAC, resource already deleted) populate the `Error` field and do not block the pipeline. The Diagnostic Bundle is passed to the Analyzer with whatever data was successfully collected.

#### 5.2.4 Sensitive Data Handling

- Gatherers MUST redact Secret values. Environment variables sourced from Secrets show `[REDACTED]`.
- Container logs may contain sensitive data. Users configure log redaction patterns:

```yaml
# values.yaml
gatherers:
  podLogs:
    redactPatterns:
      - "password=.*"
      - "token=[A-Za-z0-9+/=]+"
      - "Bearer [A-Za-z0-9._-]+"
```

- Default redaction patterns are always active for: Bearer tokens, basic auth headers, AWS access keys, and common password patterns.

---

### 5.3 Stage 3 — Analysis

The Analyzer receives a Diagnostic Bundle and produces an RCA Report. The primary analyzer uses an LLM, but the interface supports alternative implementations.

#### 5.3.1 Analyzer Interface

```go
type Analyzer interface {
    Name() string
    Analyze(ctx context.Context, bundle DiagnosticBundle) (*RCAReport, error)
    Healthy(ctx context.Context) bool
}
```

#### 5.3.2 Built-in Analyzers

| Analyzer | Backend | Notes |
|----------|---------|-------|
| `claude` | Anthropic API (direct) | Default. Uses Claude Sonnet 4.5 or configurable model. |
| `claude-bedrock` | AWS Bedrock (Anthropic models) | For enterprises requiring data residency within AWS. Uses IRSA. |
| `openai` | OpenAI API | GPT-4o or configurable model. |
| `azure-openai` | Azure OpenAI Service | For enterprises on Azure. |
| `noop` | None | Passes the Diagnostic Bundle through without analysis. Useful for users who only want raw data forwarded to sinks. |

#### 5.3.3 Analyzer Configuration

```yaml
# values.yaml
analyzer:
  backend: claude-bedrock
  claude:
    model: claude-sonnet-4-5-20250929
    apiKeySecret:
      name: wormsign-api-keys
      key: anthropic-api-key
    maxTokens: 4096
    temperature: 0.0
  claudeBedrock:
    region: us-east-1
    modelId: anthropic.claude-sonnet-4-5-20250929-v1:0
    # Uses IRSA — no API key needed
  openai:
    model: gpt-4o
    apiKeySecret:
      name: wormsign-api-keys
      key: openai-api-key
  azureOpenai:
    endpoint: https://mycompany.openai.azure.com
    deploymentName: gpt-4o
    apiKeySecret:
      name: wormsign-api-keys
      key: azure-openai-key
```

#### 5.3.4 LLM System Prompt

The controller ships a default system prompt optimized for Kubernetes diagnostics. Users can override or extend it:

```yaml
# values.yaml
analyzer:
  systemPromptOverride: ""    # replace entirely (not recommended)
  systemPromptAppend: |       # append domain-specific context
    Additional context for this environment:
    - This cluster uses Karpenter v1.1 for node provisioning.
    - Batch jobs are orchestrated by AWS Step Functions.
    - EBS volumes are provisioned in us-east-1a and us-east-1b.
```

**Default system prompt:**

```
You are an expert Kubernetes diagnostics engine embedded in a fault-detection
controller called Wormsign. Your role is to analyze diagnostic data from a
Kubernetes cluster fault and produce a structured Root Cause Analysis.

## Your Input

You will receive a Diagnostic Bundle containing some or all of:
- The fault event that triggered this analysis (detector name, severity,
  affected resource, timestamp)
- Pod spec, status, conditions, and container exit codes
- Recent Kubernetes events for the affected resource and its namespace
- Container logs (possibly redacted)
- Node conditions, capacity, and allocatable resources
- PVC/PV status and storage events
- Karpenter NodePool and NodeClaim status (if applicable)
- Owning controller (Deployment, StatefulSet, Job) status and replica counts
- For correlated events: multiple related fault events grouped under a primary
  resource

Some sections may be missing or contain errors. Work with what is available.
Do not hallucinate information that is not in the diagnostic data.

## Your Output

Respond ONLY with a JSON object matching this exact schema. Do not include any
text before or after the JSON. Do not wrap it in markdown code fences.

{
  "rootCause": "<concise 1-2 sentence root cause statement>",
  "severity": "<critical|warning|info>",
  "category": "<scheduling|storage|application|networking|resources|node|configuration|unknown>",
  "systemic": <true|false>,
  "blastRadius": "<description of what else is or could be affected>",
  "remediation": [
    "<step 1: most impactful action>",
    "<step 2: next action>",
    "<step 3: if applicable>"
  ],
  "relatedResources": [
    {"kind": "<Kind>", "namespace": "<ns>", "name": "<name>"}
  ],
  "confidence": <0.0-1.0>
}

## Analysis Guidelines

1. Start with the most specific evidence. Exit codes, OOMKilled status, and
   event messages are more diagnostic than general pod phase.

2. Distinguish between symptoms and root causes:
   - Pod in CrashLoopBackOff is a symptom. The root cause might be a missing
     ConfigMap, a bad image tag, or an OOM condition.
   - Pod stuck Pending is a symptom. The root cause might be insufficient
     node capacity, a taint/toleration mismatch, a topology constraint, or
     a PVC that cannot bind.

3. Check for cascading failures. If a node is NotReady, all pods on that
   node will fail — the node is the root cause, not the individual pods.

4. For scheduling failures, check in order:
   a. Resource requests vs node allocatable
   b. NodeSelector / nodeAffinity / topology spread constraints
   c. Taints and tolerations
   d. PVC availability zone constraints
   e. Karpenter NodePool constraints and limits

5. For application failures, check in order:
   a. Container exit code (137 = OOMKilled or SIGKILL, 1 = app error,
      126 = permission denied, 127 = command not found)
   b. Liveness/readiness probe failures in events
   c. Image pull errors in events
   d. Volume mount failures
   e. Application-level errors in logs

6. Set "systemic" to true if:
   - The issue affects or will likely affect multiple pods
   - The root cause is at the node, storage, or networking layer
   - A Deployment rollout is failing across replicas

7. Set confidence based on evidence strength:
   - 0.9-1.0: Clear evidence (OOMKilled exit code, explicit error event)
   - 0.7-0.8: Strong circumstantial evidence (timing correlation, resource
     pressure)
   - 0.5-0.6: Reasonable inference with incomplete data
   - 0.3-0.4: Best guess, multiple possible causes
   - Below 0.3: Insufficient data, say so in rootCause

8. Remediation steps should be concrete and actionable. Include specific
   kubectl commands, resource adjustments, or configuration changes.
   Order from most impactful to least.

9. For correlated events (Super-Events), focus on the primary resource as
   the likely root cause. Reference the affected resources in blastRadius
   and relatedResources.
```

#### 5.3.5 LLM Response Validation

When the LLM returns a response, the analyzer:

1. Attempts to parse it as JSON matching the `RCAReport` schema.
2. If parsing fails, attempts to extract JSON from markdown code fences (common LLM behavior despite instructions).
3. If extraction fails, creates a fallback report:

```go
RCAReport{
    RootCause:   "Automated analysis failed — raw diagnostics attached",
    Severity:    bundle.FaultEvent.Severity,  // preserve original
    Category:    "unknown",
    Confidence:  0.0,
    RawAnalysis: llmResponseText,  // full LLM response for debugging
    // DiagnosticBundle is always attached
}
```

The fallback report still flows through sinks so operators always receive diagnostic data even when the LLM misbehaves.

#### 5.3.6 RCA Report Schema

```go
type RCAReport struct {
    FaultEventID     string            // Source FaultEvent or SuperEvent ID
    Timestamp        time.Time
    RootCause        string
    Severity         Severity          // Reassessed after analysis
    Category         string            // scheduling, storage, application, networking, resources, node, configuration, unknown
    Systemic         bool
    BlastRadius      string
    Remediation      []string
    RelatedResources []ResourceRef
    Confidence       float64           // 0.0-1.0
    RawAnalysis      string            // Full LLM response for audit
    DiagnosticBundle DiagnosticBundle  // Attached for audit trail
    AnalyzerBackend  string            // Which backend produced this
    TokensUsed       TokenUsage        // Input/output token counts
}

type TokenUsage struct {
    Input  int
    Output int
}
```

#### 5.3.7 Rate Limiting and Cost Control

- **Token budget:** Configurable daily and hourly token budgets per analyzer backend. Once exceeded, the analyzer falls back to `noop` mode and surfaces raw diagnostics. Budget tracking is in-memory, reset on controller restart (acceptable for v1; over-budget by at most one analysis worth of tokens after restart).
- **Circuit breaker:** If the LLM backend returns errors for N consecutive calls (default: 5), the circuit opens for a configurable duration (default: 10m), falling back to `noop`. State: closed → open → half-open (allow one probe request) → closed.

```yaml
# values.yaml
analyzer:
  rateLimiting:
    dailyTokenBudget: 1000000
    hourlyTokenBudget: 100000
  circuitBreaker:
    consecutiveFailures: 5
    openDuration: 10m
```

---

### 5.4 Stage 4 — Sinks (Surfacing)

Sinks deliver the RCA Report to external systems. Multiple sinks can be active simultaneously.

#### 5.4.1 Built-in Sinks

| Sink | Delivers To | Notes |
|------|-------------|-------|
| `log` | Controller stdout | Structured JSON log line. **Always enabled, non-optional.** Ensures every RCA Report is captured. |
| `slack` | Slack channel via webhook | Formatted message with severity color-coding, root cause summary, and remediation steps. |
| `pagerduty` | PagerDuty Events API v2 | Creates incidents for Critical severity. Enriches with custom details from RCA. |
| `s3` | AWS S3 bucket | Archives full Diagnostic Bundle + RCA Report as JSON. Uses IRSA. |
| `webhook` | Arbitrary HTTP endpoint | POSTs the RCA Report as JSON. Headers and auth configurable. |
| `kubernetes-event` | Kubernetes Event on the resource | Creates a Warning/Normal event on the affected resource with the root cause summary. For Super-Events, creates events on each affected resource. |

#### 5.4.2 Sink Configuration

```yaml
# values.yaml
sinks:
  slack:
    enabled: true
    webhookSecret:
      name: wormsign-sink-secrets
      key: slack-webhook-url
    channel: "#k8s-incidents"
    severityFilter: [critical, warning]
    templateOverride: ""
  pagerduty:
    enabled: true
    routingKeySecret:
      name: wormsign-sink-secrets
      key: pagerduty-routing-key
    severityFilter: [critical]
  s3:
    enabled: true
    bucket: my-company-wormsign-reports
    region: us-east-1
    prefix: "wormsign/reports/"
    # Uses IRSA — no credentials needed
  webhook:
    enabled: false
    url: https://internal-api.mycompany.com/wormsign/events
    headers:
      Authorization: "Bearer ${SECRET:wormsign-sink-secrets:api-token}"
    severityFilter: [critical, warning, info]
    allowedDomains:       # SSRF protection for custom webhook sinks
      - "*.mycompany.com"
      - "hooks.slack.com"
      - "events.pagerduty.com"
  kubernetesEvent:
    enabled: true
    severityFilter: [critical, warning]
```

#### 5.4.3 Sink Delivery Guarantees

- Sinks are **best-effort with retry.** Each sink gets up to 3 delivery attempts with exponential backoff (1s, 5s, 25s).
- Delivery failures are logged and counted in Prometheus metrics. A sink that fails persistently does not block other sinks.
- The `log` sink is always enabled and non-optional, ensuring that every RCA Report is captured even if all external sinks are down.

---

## 6. Filtering and Exclusion

Filters prevent specific resources, namespaces, or workloads from triggering fault detection. Filters are evaluated at detection time — excluded resources never produce Fault Events and consume no pipeline resources.

### 6.1 Filter Mechanisms

Wormsign supports three complementary filter mechanisms, from broadest to most targeted:

#### 6.1.1 Helm Values (Global Filters)

For cluster-wide exclusions that rarely change:

```yaml
# values.yaml
filters:
  # Exclude entire namespaces by name (exact match or regex)
  excludeNamespaces:
    - kube-system
    - kube-public
    - kube-node-lease
    - ".*-sandbox"          # regex: any namespace ending in -sandbox

  # Exclude namespaces by label
  excludeNamespaceSelector:
    matchLabels:
      wormsign.io/exclude: "true"
```

Global filters are applied **before** informers are created for a namespace. Excluded namespaces are never watched, consuming zero resources. Regex patterns are compiled at startup and validated; invalid regex causes startup failure.

#### 6.1.2 Resource Annotations (Inline Opt-Out)

Any Kubernetes resource can opt out of detection by annotation. This is the most ergonomic mechanism for individual teams:

```yaml
apiVersion: apps/v1
kind: Deployment
metadata:
  name: chaos-test-app
  namespace: test
  annotations:
    # Exclude from ALL detectors
    wormsign.io/exclude: "true"

    # Or exclude from SPECIFIC detectors (comma-separated)
    wormsign.io/exclude-detectors: "PodCrashLoop,PodFailed"
```

Annotations are evaluated at detection time by checking the triggering resource's annotations. If the resource has `wormsign.io/exclude: "true"`, no Fault Event is emitted. If `wormsign.io/exclude-detectors` is set, only the named detectors are suppressed for that resource.

For pod-related detectors, the controller also checks annotations on the owning controller (Deployment, StatefulSet, DaemonSet, Job) resolved via `ownerReferences`. This allows annotating a Deployment to suppress detection for all its pods without annotating each pod template.

#### 6.1.3 WormsignPolicy CRD (Dynamic Filters)

For team-specific, dynamic, or complex exclusion rules:

```yaml
apiVersion: wormsign.io/v1alpha1
kind: WormsignPolicy
metadata:
  name: exclude-test-workloads
  namespace: test               # applies only within 'test' namespace
spec:
  action: Exclude
  # Which detectors this policy applies to (empty = all)
  detectors: ["PodCrashLoop", "PodFailed"]
  # Resource matching rules (all conditions are ANDed)
  match:
    resourceSelector:
      matchLabels:
        app.kubernetes.io/part-of: "integration-tests"
      matchExpressions:
        - key: team
          operator: In
          values: ["platform-test", "qa"]
    # Match by owner name (glob patterns)
    ownerNames:
      - "canary-deploy-*"
      - "load-test-*"
status:
  conditions:
    - type: Active
      status: "True"
      message: "Policy is active, matching 47 resources"
  matchedResources: 47
  lastEvaluated: "2026-02-08T14:30:00Z"
```

**Namespace scoping:** A `WormsignPolicy` in namespace `test` only affects resources in `test`. To create a policy that spans namespaces, create it in `wormsign-system` with an explicit `namespaceSelector`:

```yaml
apiVersion: wormsign.io/v1alpha1
kind: WormsignPolicy
metadata:
  name: exclude-all-sandbox-namespaces
  namespace: wormsign-system
spec:
  action: Exclude
  namespaceSelector:
    matchLabels:
      environment: sandbox
  match: {}    # all resources in matching namespaces
```

**Suppression windows** (maintenance mode):

```yaml
apiVersion: wormsign.io/v1alpha1
kind: WormsignPolicy
metadata:
  name: maintenance-window
  namespace: wormsign-system
spec:
  action: Suppress
  match: {}                     # all resources
  # Suppress only applies when the policy exists.
  # Create it before maintenance, delete it after.
  # Or use a time window:
  schedule:
    start: "2026-02-08T22:00:00Z"
    end: "2026-02-09T02:00:00Z"
```

### 6.2 Filter Evaluation Order

Filters are evaluated in this order. Evaluation short-circuits on the first match.

```
1. Global excludeNamespaces (Helm values)         → skip entire namespace
2. Global excludeNamespaceSelector (Helm values)   → skip if namespace labels match
3. WormsignPolicy with action: Suppress + active schedule → skip all in scope
4. Namespace annotation wormsign.io/suppress       → skip all in namespace
5. Resource annotation wormsign.io/exclude: "true" → skip all detectors
6. Owner annotation wormsign.io/exclude: "true"    → skip all detectors
7. Resource annotation wormsign.io/exclude-detectors → skip named detectors
8. Owner annotation wormsign.io/exclude-detectors  → skip named detectors
9. WormsignPolicy with action: Exclude             → skip if match criteria met
```

**Exclusion always wins.** If a resource matches any exclusion filter, it is excluded regardless of any other configuration.

### 6.3 Filter Metrics

```
wormsign_events_filtered_total{detector, namespace, reason}
```

Reason values: `namespace_global`, `namespace_label`, `suppress_policy`, `namespace_annotation`, `resource_annotation`, `owner_annotation`, `policy_exclude`.

These metrics let operators verify filters are working correctly and detect cases where filters are accidentally too broad.

---

## 7. Deployment and Operations

### 7.1 Installation

```bash
helm repo add wormsign https://k8s-wormsign.github.io/charts
helm install wormsign wormsign/k8s-wormsign \
  --namespace wormsign-system \
  --create-namespace \
  --values my-values.yaml
```

The Helm chart installs:
- The controller Deployment (1 replica by default, configurable for HA)
- ServiceAccount with RBAC (see 7.2)
- CRDs in the `crds/` directory (installed by Helm before templates)
- Default configuration via Helm values
- Prometheus ServiceMonitor (optional, `metrics.serviceMonitor.enabled: true`)

### 7.2 RBAC Requirements

The controller requires cluster-level read access:

| Resource | Verbs | Reason |
|----------|-------|--------|
| `pods`, `pods/log` | get, list, watch | Detection (metadata-only informers) and log gathering |
| `events` | get, list, watch, create | Diagnostic gathering and Kubernetes Event sink |
| `nodes` | get, list, watch | Node condition detection and gathering |
| `persistentvolumeclaims`, `persistentvolumes` | get, list, watch | PVC/PV status gathering |
| `namespaces` | get, list, watch | Namespace filtering and shard assignment |
| `configmaps` | get, list, watch, create, update | Shard map storage |
| `leases` | get, list, watch, create, update | Leader election |
| `jobs`, `replicasets`, `deployments`, `statefulsets`, `daemonsets` | get, list, watch | Owner reference resolution and status gathering |
| `wormsigndetectors.wormsign.io` | get, list, watch | Custom detector loading |
| `wormsigndetectors.wormsign.io/status` | update | Status reporting |
| `wormsigngatherers.wormsign.io` | get, list, watch | Custom gatherer loading |
| `wormsigngatherers.wormsign.io/status` | update | Status reporting |
| `wormsignsinks.wormsign.io` | get, list, watch | Custom sink loading |
| `wormsignsinks.wormsign.io/status` | update | Status reporting |
| `wormsignpolicies.wormsign.io` | get, list, watch | Filter policy loading |
| `wormsignpolicies.wormsign.io/status` | update | Status reporting |

If Karpenter is installed:

| Resource | Verbs | Reason |
|----------|-------|--------|
| `nodepools.karpenter.sh` | get, list, watch | Karpenter state gathering |
| `nodeclaims.karpenter.sh` | get, list, watch | Karpenter state gathering |

### 7.3 Observability

#### 7.3.1 Prometheus Metrics

| Metric | Type | Description |
|--------|------|-------------|
| `wormsign_fault_events_total` | Counter | Fault events emitted, by `detector`, `severity`, `namespace` |
| `wormsign_super_events_total` | Counter | Super-events emitted, by `correlation_rule`, `severity` |
| `wormsign_events_filtered_total` | Counter | Events excluded by filters, by `detector`, `namespace`, `reason` |
| `wormsign_diagnostic_gather_duration_seconds` | Histogram | Time to gather diagnostics, by `gatherer` |
| `wormsign_analyzer_requests_total` | Counter | LLM API calls, by `backend`, `status` |
| `wormsign_analyzer_tokens_used_total` | Counter | Tokens consumed, by `backend`, `direction` (input/output) |
| `wormsign_analyzer_latency_seconds` | Histogram | LLM response latency |
| `wormsign_sink_deliveries_total` | Counter | Sink deliveries, by `sink`, `status` (success/failure) |
| `wormsign_circuit_breaker_state` | Gauge | 0 = closed, 1 = open, 2 = half-open |
| `wormsign_pipeline_queue_depth` | Gauge | Items in queue, by `stage` |
| `wormsign_shard_namespaces` | Gauge | Namespaces assigned to this replica |
| `wormsign_leader_is_leader` | Gauge | 1 if this replica is the leader |

#### 7.3.2 Structured Logging

All logs use `slog` (Go stdlib) with JSON output. Log level configurable via Helm (`logging.level`, default: `info`).

Log fields include: `detector`, `namespace`, `resource`, `fault_event_id`, `duration`, `error`, `sink`, `backend`.

---

## 8. Security Considerations

- **API keys** are stored in Kubernetes Secrets and referenced by name/key. They are never logged or included in Diagnostic Bundles.
- **RBAC** follows least-privilege. The controller has read-only access to cluster resources except: Event creation (K8s Event sink), ConfigMap read/write (shard map), and Lease read/write (leader election).
- **Log redaction** is configurable and enabled by default for common patterns (Bearer tokens, passwords, AWS keys).
- **LLM data residency:** By supporting Bedrock and Azure OpenAI, enterprises can ensure diagnostic data stays within their cloud account. The `noop` analyzer allows running without any external LLM calls.
- **CRD security:**
  - CEL expressions in `WormsignDetector` run in a sandboxed environment with a cost budget. They cannot perform I/O.
  - Custom gatherers (`WormsignGatherer`) are limited to Kubernetes API operations through the controller's API client. No shell execution, no HTTP calls to arbitrary endpoints.
  - Custom sink webhooks (`WormsignSink`) are validated against a configurable domain allowlist to prevent SSRF.
- **Namespace isolation:** CRDs created in a user namespace only affect resources in that namespace. Cross-namespace CRDs require creation in `wormsign-system`, which can be RBAC-restricted.

---

## 9. Roadmap (Out of Scope for v1)

These features are not included in v1 but inform the architecture:

- **Historical correlation:** Store RCA Reports in a time-series database and use them as few-shot examples for the LLM, improving analysis accuracy over time.
- **Auto-remediation:** Allow sinks to trigger remediation actions (e.g., cordon a node, restart a pod, scale a NodePool) with approval workflows.
- **Multi-cluster:** Federated deployments that aggregate Fault Events across clusters.
- **Web dashboard:** A lightweight UI for browsing historical incidents, RCA Reports, and trends.
- **Adaptive correlation windows:** Automatically adjust the correlation window duration based on cluster event velocity.
- **Cost reporting:** Track and surface per-namespace/per-team LLM costs.

---

## 10. Appendix: Complete Helm Values Reference

```yaml
# values.yaml — full reference with defaults

# --- Replica and scaling ---
replicaCount: 1
resources:
  requests:
    cpu: 100m
    memory: 128Mi
  limits:
    cpu: 500m
    memory: 512Mi

# --- Leader election ---
leaderElection:
  enabled: true
  leaseDuration: 15s
  renewDeadline: 10s
  retryPeriod: 2s

# --- Logging ---
logging:
  level: info           # debug, info, warn, error
  format: json

# --- Global filters ---
filters:
  excludeNamespaces:
    - kube-system
    - kube-public
    - kube-node-lease
  excludeNamespaceSelector: {}

# --- Detectors ---
detectors:
  podStuckPending:
    enabled: true
    threshold: 15m
    cooldown: 30m
  podCrashLoop:
    enabled: true
    threshold: 3
    cooldown: 30m
  podFailed:
    enabled: true
    ignoreExitCodes: [0]
    cooldown: 30m
  nodeNotReady:
    enabled: true
    threshold: 5m
    cooldown: 30m
  pvcStuckBinding:
    enabled: false
    threshold: 10m
    cooldown: 30m
  highPodCount:
    enabled: false
    threshold: 200
    cooldown: 1h
  jobDeadlineExceeded:
    enabled: false
    cooldown: 30m

# --- Correlation ---
correlation:
  enabled: true
  windowDuration: 2m
  rules:
    nodeCascade:
      enabled: true
      minPodFailures: 2
    deploymentRollout:
      enabled: true
      minPodFailures: 3
    storageCascade:
      enabled: true
    namespaceStorm:
      enabled: true
      threshold: 20

# --- Gatherers ---
gatherers:
  podLogs:
    enabled: true
    tailLines: 100
    includePrevious: true
    redactPatterns: []        # additional patterns; defaults always active
  karpenterState:
    enabled: true             # auto-disabled if Karpenter CRDs not found
  nodeConditions:
    enabled: true
    includeAllocatable: true
  superEvent:
    maxAffectedResources: 5

# --- Analyzer ---
analyzer:
  backend: claude             # claude, claude-bedrock, openai, azure-openai, noop
  systemPromptOverride: ""
  systemPromptAppend: ""
  rateLimiting:
    dailyTokenBudget: 1000000
    hourlyTokenBudget: 100000
  circuitBreaker:
    consecutiveFailures: 5
    openDuration: 10m
  claude:
    model: claude-sonnet-4-5-20250929
    apiKeySecret:
      name: wormsign-api-keys
      key: anthropic-api-key
    maxTokens: 4096
    temperature: 0.0
  claudeBedrock:
    region: us-east-1
    modelId: anthropic.claude-sonnet-4-5-20250929-v1:0
  openai:
    model: gpt-4o
    apiKeySecret:
      name: wormsign-api-keys
      key: openai-api-key
    maxTokens: 4096
    temperature: 0.0
  azureOpenai:
    endpoint: ""
    deploymentName: ""
    apiKeySecret:
      name: wormsign-api-keys
      key: azure-openai-key

# --- Sinks ---
sinks:
  # log sink is always enabled, not configurable
  slack:
    enabled: false
    webhookSecret:
      name: wormsign-sink-secrets
      key: slack-webhook-url
    channel: ""
    severityFilter: [critical, warning]
    templateOverride: ""
  pagerduty:
    enabled: false
    routingKeySecret:
      name: wormsign-sink-secrets
      key: pagerduty-routing-key
    severityFilter: [critical]
  s3:
    enabled: false
    bucket: ""
    region: ""
    prefix: "wormsign/reports/"
  webhook:
    enabled: false
    url: ""
    headers: {}
    severityFilter: [critical, warning, info]
    allowedDomains: []
  kubernetesEvent:
    enabled: true
    severityFilter: [critical, warning]

# --- Pipeline workers ---
pipeline:
  workers:
    gathering: 5
    analysis: 2
    sink: 3

# --- Metrics ---
metrics:
  enabled: true
  port: 8080
  serviceMonitor:
    enabled: false
    interval: 30s

# --- Health probes ---
health:
  port: 8081
```
