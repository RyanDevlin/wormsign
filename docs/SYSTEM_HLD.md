# K8s Wormsign — System High-Level Design

**Version:** 1.0.0
**Date:** February 9, 2026

---

## 1. Executive Summary

K8s Wormsign is a Kubernetes-native fault detection controller that watches cluster state for anomalies, collects diagnostic context, performs root cause analysis using LLM-powered reasoning, and delivers actionable insights to operators through configurable integrations.

The system is implemented in Go, uses raw `client-go` shared informers (not controller-runtime), and deploys via Helm. It is designed to scale from small clusters (100 pods) to enterprise-scale clusters (100k+ pods) without architectural changes.

### Key Characteristics

| Aspect | Detail |
|--------|--------|
| Language | Go 1.25, module path `github.com/k8s-wormsign/k8s-wormsign` |
| Framework | Raw `client-go` with shared informers |
| Architecture | Event-driven 5-stage pipeline |
| Deployment | Helm chart into `wormsign-system` namespace |
| Scaling | Namespace sharding, metadata-only informers, HPA support |
| Extensibility | 4 CRDs for custom detectors, gatherers, sinks, and policies |
| Kubernetes | 1.27+ required |

---

## 2. System Architecture Overview

Wormsign implements a five-stage event-driven pipeline. Each stage is independently configurable, horizontally scalable, and connected through rate-limited work queues.

```
                    Kubernetes Cluster
                          │
                    Watch Events (informers)
                          │
                          ▼
┌─────────────────────────────────────────────────────────────────────┐
│  STAGE 1: DETECTION                                                 │
│                                                                     │
│  Informer callbacks → Detector evaluation → Filter engine check     │
│  7 built-in detectors + CEL custom detectors via CRD                │
│  Output: FaultEvent                                                 │
│                                                                     │
│  Workers: 1 per replica (informer-driven)                           │
└────────────────────────────┬────────────────────────────────────────┘
                             │ rate-limited work queue
                             ▼
┌─────────────────────────────────────────────────────────────────────┐
│  STAGE 2: CORRELATION                                               │
│                                                                     │
│  Time-windowed event buffer (2m default)                            │
│  4 built-in rules: NodeCascade, DeploymentRollout,                  │
│                    StorageCascade, NamespaceStorm                    │
│  Groups related events into SuperEvents                             │
│  Output: SuperEvent (correlated) or FaultEvent (uncorrelated)       │
│                                                                     │
│  Workers: 1 per replica (stateful, single-threaded)                 │
└────────────────────────────┬────────────────────────────────────────┘
                             │ rate-limited work queue
                             ▼
┌─────────────────────────────────────────────────────────────────────┐
│  STAGE 3: GATHERING                                                 │
│                                                                     │
│  Concurrent diagnostic data collection via errgroup                 │
│  9 built-in gatherers + custom gatherers via CRD                    │
│  Output: DiagnosticBundle (sections from each gatherer)             │
│                                                                     │
│  Workers: 5 per replica (default, I/O-bound)                        │
└────────────────────────────┬────────────────────────────────────────┘
                             │ priority queue (critical-first)
                             ▼
┌─────────────────────────────────────────────────────────────────────┐
│  STAGE 4: ANALYSIS                                                  │
│                                                                     │
│  LLM-powered root cause analysis                                    │
│  5 backends: Claude, Claude Bedrock, OpenAI, Azure OpenAI, Noop     │
│  Circuit breaker + token budget rate limiter                        │
│  Output: RCAReport                                                  │
│                                                                     │
│  Workers: 2 per replica (default, LLM-throughput bound)             │
└────────────────────────────┬────────────────────────────────────────┘
                             │ work queue
                             ▼
┌─────────────────────────────────────────────────────────────────────┐
│  STAGE 5: SINKS                                                     │
│                                                                     │
│  Delivery to external systems with retry + backoff                  │
│  Built-in: log (always on), Slack, PagerDuty, S3, webhook, K8s     │
│  Custom sinks via CRD (webhook with Go templates)                   │
│  Each sink independent — one failure does not block others          │
│                                                                     │
│  Workers: 3 per replica (default)                                   │
└─────────────────────────────────────────────────────────────────────┘
```

---

## 3. Component Architecture

### 3.1 Process Entry Point

`cmd/controller/main.go` — The single binary entry point that:

1. Parses flags (`--config`, `--kubeconfig`, `--dry-run`, `--version`)
2. Loads and validates configuration from YAML (`/etc/wormsign/config.yaml`)
3. Configures structured logging (`slog`, JSON format)
4. Builds the Kubernetes `client-go` REST config (in-cluster or kubeconfig)
5. Initializes Prometheus metrics registry
6. Starts the health probe HTTP server (`:8081`)
7. Starts the metrics HTTP server (`:8080`)
8. Creates and runs the `Controller`
9. On SIGTERM/SIGINT: gracefully shuts down all servers

### 3.2 Controller (`internal/controller/`)

The `Controller` is the top-level orchestrator. It wires together all subsystems and manages their lifecycle.

```
Controller
├── LeaderElector        — Kubernetes Lease-based leader election
├── ShardManager         — Namespace assignment via consistent hashing
├── InformerManager      — Per-namespace informer factories
├── FilterEngine         — 9-level detection-time exclusion evaluation
├── Pipeline             — The 5-stage processing pipeline
│   ├── Correlator       — Time-windowed event grouping
│   ├── WorkQueues       — Per-stage rate-limited queues
│   ├── Gatherers[]      — Diagnostic data collectors
│   ├── Analyzer         — LLM backend
│   └── Sinks[]          — Report delivery targets
└── HealthHandler        — Liveness + readiness probes
```

**Lifecycle:**

```
New() → Run(ctx) ─┬─ Initialize filter engine
                   ├─ Initialize shard manager
                   ├─ Initialize informer manager
                   ├─ Wire shard changes → informer manager
                   ├─ Create pipeline (correlator, queues, workers)
                   ├─ Start pipeline
                   ├─ Start leader election
                   ├─ Start heartbeat goroutine
                   ├─ Block on ctx.Done()
                   └─ shutdown() → 7-step graceful shutdown
```

### 3.3 Leader Election

- **Implementation:** `client-go` `leaderelection` package with Kubernetes `Lease` object
- **Lease name:** `wormsign-leader` in the controller namespace
- **Timing:** 15s lease duration, 10s renew deadline, 2s retry period
- **Always enabled**, even for single-replica deployments (zero-config HA upgrade path)

**Leader responsibilities:**
- Runs namespace shard coordination (computes and publishes shard assignments)
- Runs cluster-scoped detectors (`NodeNotReady`)
- Runs the correlation engine's global state

**All replicas run:**
- Namespace-scoped detectors for their assigned shard
- Gathering, analysis, and sink delivery independently

### 3.4 Namespace Sharding (`internal/shard/`)

Enables horizontal scaling by partitioning namespaces across replicas.

```
┌─────────────────────────────────────────────────┐
│             Coordinator (Leader)                 │
│  Watches Lease objects for replica membership    │
│  Assigns namespaces via consistent hashing       │
│  Publishes to ConfigMap wormsign-shard-map       │
└──────────┬──────────────────┬───────────────────┘
           │                  │
    ┌──────▼──────┐    ┌──────▼──────┐
    │  Replica A   │    │  Replica B   │
    │  ns: default │    │  ns: payments│
    │      orders  │    │      shipping│
    │      inventory│   │      analytics│
    │              │    │              │
    │  Own informers│   │  Own informers│
    │  Own detectors│   │  Own detectors│
    └──────────────┘    └──────────────┘
```

- **Shard assignment** uses consistent hashing on namespace names to minimize reassignment during scale events
- **Shard map** is published as a ConfigMap; all replicas watch and adjust their informers
- **Single-replica mode:** All namespaces assigned to one replica; same code path exercised at all scales

### 3.5 Informer Strategy (`internal/shard/informer_manager.go`)

| Resource | Informer Type | Per-Object Size | Rationale |
|----------|--------------|-----------------|-----------|
| Pods | **Metadata-only** | ~500 bytes | Detection needs only phase, conditions, restart counts |
| Nodes | Full object | ~2-5KB | Low cardinality, full spec needed for capacity analysis |
| Events | Full object | ~1-2KB | Content is the value |
| PVCs | Full object | ~1KB | Low cardinality |
| CRDs (Wormsign) | Full object | ~1KB | Low cardinality |

**Key insight:** Pods use `metadata.NewFilteredMetadataInformer` for the watch (saving ~90% memory vs full objects at scale). When a fault fires, the gatherer fetches the full pod via a direct `GET` call — one extra API call per fault (rare) trades for massive memory savings on the hot path (constant).

**Estimated memory at 100k pods / 3 replicas (~33k pods per shard):**

| Component | Per-Replica |
|-----------|------------|
| Pod metadata cache | ~16MB |
| Event cache | ~50-100MB |
| Node cache | ~25MB |
| PVC + CRD cache | ~6-11MB |
| Pipeline buffers | ~50-100MB |
| **Total** | **~150-250MB** |

---

## 4. Pipeline Deep Dive

### 4.1 Detection Stage

Detectors watch Kubernetes state via informer callbacks and emit `FaultEvent` objects when anomalous conditions are met.

**Built-in Detectors (7):**

| Detector | Trigger Condition | Default | Severity | Leader-Only |
|----------|-------------------|---------|----------|-------------|
| `PodStuckPending` | Pod in Pending >15m | Enabled | Warning | No |
| `PodCrashLoop` | CrashLoopBackOff, restarts >3 | Enabled | Warning | No |
| `PodFailed` | Pod phase Failed or container exits non-zero | Enabled | Warning | No |
| `NodeNotReady` | Node Ready=False/Unknown >5m | Enabled | Critical | **Yes** |
| `PVCStuckBinding` | PVC Pending >10m | Disabled | Warning | No |
| `HighPodCount` | Namespace pod count >threshold | Disabled | Info | No |
| `JobDeadlineExceeded` | Job `activeDeadlineSeconds` exceeded | Disabled | Warning | No |

**Custom Detectors:** Defined via `WormsignDetector` CRD using CEL expressions evaluated in a sandboxed environment with cost budgets. CEL cannot perform I/O.

**Deduplication:** Each detector maintains an in-memory cooldown map keyed by `(DetectorName, Resource.UID)`. Default cooldown: 30 minutes. Resets on controller restart.

**Filter evaluation** occurs at detection time, before any FaultEvent enters the pipeline. See Section 6 for the 9-level filter chain.

### 4.2 Correlation Stage

The pre-LLM correlation engine buffers events within a configurable time window (default: 2 minutes) and applies rule-based grouping to reduce LLM calls during incident storms.

**Built-in Correlation Rules (4):**

| Rule | Condition | Result |
|------|-----------|--------|
| `NodeCascade` | `NodeNotReady` + >=2 pod failures on that node | Single SuperEvent with node as primary resource |
| `DeploymentRollout` | >=3 pods from same Deployment fail | Single SuperEvent with Deployment as primary resource |
| `StorageCascade` | `PVCStuckBinding` + >=1 pod referencing that PVC | Single SuperEvent with PVC as primary resource |
| `NamespaceStorm` | >20 events in same namespace, no other rule matched | Single SuperEvent at namespace level (catch-all) |

**Evaluation order:** Rules are evaluated in order; events consumed by one rule are not available to subsequent rules. NamespaceStorm runs last as a catch-all to prevent unbounded LLM calls.

**State:** In-memory buffer, lost on leader failover (acceptable — events replay from informer re-list).

### 4.3 Gathering Stage

When a FaultEvent or SuperEvent is dequeued, the gathering stage concurrently collects diagnostic data using `errgroup`. Individual gatherer failures populate the `DiagnosticSection.Error` field without blocking other gatherers.

**Built-in Gatherers (9):**

| Gatherer | Collects | Triggered By |
|----------|----------|-------------|
| `PodDescribe` | Full pod spec, status, conditions, container statuses | Any pod fault |
| `PodEvents` | K8s events for the pod (last 1h) | Any pod fault |
| `PodLogs` | Container logs (tail N lines + previous terminated) | Pod failures/crashloops |
| `NodeConditions` | Node status, allocatable vs capacity, taints | Scheduling failures, node issues |
| `PVCStatus` | PVC/PV status, events, EBS volume ID, AZ | PVC-related or scheduling faults |
| `KarpenterState` | NodePool/NodeClaim status, Karpenter events | Scheduling failures (auto-disabled if Karpenter absent) |
| `NamespaceEvents` | K8s events in namespace (last 30m) | All faults |
| `ReplicaSetStatus` | Owning ReplicaSet/Deployment status, replica counts | Pod failures |
| `OwnerChain` | Walk `ownerReferences` to top-level controller | All pod faults |

**Custom Gatherers:** Defined via `WormsignGatherer` CRD. Limited to Kubernetes API operations (no shell, no arbitrary HTTP). Supports `resource` and `logs` collection types with template variables.

**Sensitive Data:** Gatherers redact Secret values. Container logs are filtered through configurable redaction patterns (default patterns for Bearer tokens, passwords, AWS keys always active). See `internal/redact/`.

**Super-Event gathering:** Runs for the primary resource plus a configurable sample of affected resources (default: up to 5) to bound gathering cost.

### 4.4 Analysis Stage

The analyzer receives a `DiagnosticBundle` and produces an `RCAReport`. The analysis queue is priority-ordered: critical-severity items are processed before warning and info.

**Backends (5):**

| Backend | Provider | Auth | Notes |
|---------|----------|------|-------|
| `claude` | Anthropic API direct | API key in Secret | Default. Claude Sonnet 4.5 |
| `claude-bedrock` | AWS Bedrock | IRSA (no API key) | For AWS data residency |
| `openai` | OpenAI API | API key in Secret | GPT-4o |
| `azure-openai` | Azure OpenAI Service | API key in Secret | For Azure data residency |
| `noop` | None | N/A | Pass-through, raw diagnostics only |

**System prompt:** Ships a detailed default prompt optimized for Kubernetes diagnostics. Users can override entirely or append domain-specific context. The prompt instructs the LLM to return a structured JSON response.

**Response validation:**
1. Parse as JSON matching `RCAReport` schema
2. If that fails, extract JSON from markdown code fences
3. If that fails, create a fallback report with `Confidence: 0.0` and the raw LLM response attached

Fallback reports still flow through sinks so operators always receive diagnostic data.

**Fault tolerance:**

- **Rate limiter** (`internal/analyzer/rate_limiter.go`): Configurable daily and hourly token budgets. When exceeded, falls back to noop mode.
- **Circuit breaker** (`internal/analyzer/circuit_breaker.go`): After N consecutive failures (default: 5), the circuit opens for a configurable duration (default: 10 minutes). States: closed → open → half-open (probe one request) → closed.

### 4.5 Sink Stage

Sinks deliver `RCAReport` objects to external systems. Multiple sinks run concurrently; one sink's failure does not block others.

**Built-in Sinks (6):**

| Sink | Target | Notes |
|------|--------|-------|
| `log` | Controller stdout | Structured JSON. **Always enabled, non-optional.** |
| `slack` | Slack webhook | Formatted message with severity color-coding |
| `pagerduty` | PagerDuty Events API v2 | Creates incidents for critical severity |
| `s3` | AWS S3 bucket | Archives full bundle + report as JSON. IRSA auth. |
| `webhook` | Arbitrary HTTP endpoint | Configurable headers and auth |
| `kubernetes-event` | K8s Event on resource | Warning/Normal events. Fan-out for SuperEvents. |

**Custom Sinks:** Defined via `WormsignSink` CRD. Webhook delivery with Go `text/template` body templates. URL validated against domain allowlist (SSRF protection).

**Retry policy:** 3 attempts with exponential backoff (1s, 5s, 25s). Failures are logged and counted in Prometheus metrics.

**Severity filtering:** Each sink declares which severities it accepts. The pipeline skips delivery for non-matching severities.

---

## 5. Core Data Types

The pipeline's data flow is defined by four core types in `internal/model/types.go`:

```
FaultEvent ──► SuperEvent ──► DiagnosticBundle ──► RCAReport
 (detected)    (correlated)    (gathered)           (analyzed + delivered)
```

### FaultEvent

```go
type FaultEvent struct {
    ID            string            // UUID v4
    DetectorName  string            // e.g., "PodStuckPending"
    Severity      Severity          // critical | warning | info
    Timestamp     time.Time
    Resource      ResourceRef       // Kind, Namespace, Name, UID
    Description   string
    Labels        map[string]string // From triggering resource
    Annotations   map[string]string // Detector-specific metadata
}
```

### SuperEvent

```go
type SuperEvent struct {
    ID              string          // UUID v4
    CorrelationRule string          // e.g., "NodeCascade"
    PrimaryResource ResourceRef     // Root cause resource
    FaultEvents     []FaultEvent    // All correlated events
    Timestamp       time.Time
    Severity        Severity        // Highest among constituent events
}
```

### DiagnosticBundle

```go
type DiagnosticBundle struct {
    FaultEvent *FaultEvent         // Set for single events
    SuperEvent *SuperEvent         // Set for correlated events
    Timestamp  time.Time
    Sections   []DiagnosticSection // One per gatherer
}

type DiagnosticSection struct {
    GathererName string  // e.g., "PodEvents"
    Title        string
    Content      string  // Collected data as structured text
    Format       string  // "text", "json", "yaml"
    Error        string  // Non-empty if gathering failed
}
```

### RCAReport

```go
type RCAReport struct {
    FaultEventID     string
    Timestamp        time.Time
    RootCause        string            // 1-2 sentence root cause
    Severity         Severity          // Reassessed after analysis
    Category         string            // scheduling|storage|application|networking|resources|node|configuration|unknown
    Systemic         bool              // Affects multiple pods or infra-layer
    BlastRadius      string
    Remediation      []string          // Ordered actionable steps
    RelatedResources []ResourceRef
    Confidence       float64           // 0.0-1.0
    RawAnalysis      string            // Full LLM response (audit)
    DiagnosticBundle DiagnosticBundle  // Full diagnostic data (audit)
    AnalyzerBackend  string            // e.g., "claude-bedrock"
    TokensUsed       TokenUsage        // Input + output tokens
}
```

---

## 6. Filtering and Exclusion

Filters prevent specific resources from entering the pipeline. They are evaluated at detection time before any FaultEvent is created, consuming zero pipeline resources for excluded items.

### 9-Level Evaluation Order (Short-Circuits on First Match)

```
Level  Mechanism                                          Reason Label
─────  ─────────────────────────────────────────────────  ──────────────────
  1    Global excludeNamespaces (Helm, exact or regex)    namespace_global
  2    Global excludeNamespaceSelector (Helm, labels)     namespace_label
  3    WormsignPolicy action:Suppress + active schedule   suppress_policy
  4    Namespace annotation wormsign.io/suppress           namespace_annotation
  5    Resource annotation wormsign.io/exclude:"true"      resource_annotation
  6    Owner annotation wormsign.io/exclude:"true"         owner_annotation
  7    Resource annotation wormsign.io/exclude-detectors   resource_annotation
  8    Owner annotation wormsign.io/exclude-detectors      owner_annotation
  9    WormsignPolicy action:Exclude (selector + globs)    policy_exclude
```

**Key behaviors:**
- Global namespace exclusions (levels 1-2) prevent informers from being created — zero resource consumption
- Owner annotations (levels 6, 8) check the owning Deployment/StatefulSet/etc. via `ownerReferences`, so annotating a Deployment excludes all its pods
- WormsignPolicy CRDs are namespaced — a policy in namespace `test` only affects `test` unless created in `wormsign-system` with a `namespaceSelector`

---

## 7. Custom Resource Definitions

All CRDs belong to API group `wormsign.io/v1alpha1` and are namespaced.

### 7.1 WormsignDetector

Defines custom CEL-based fault detectors.

```yaml
apiVersion: wormsign.io/v1alpha1
kind: WormsignDetector
spec:
  description: string
  resource: string                # "pods", "nodes", etc.
  namespaceSelector: LabelSelector  # cross-namespace (wormsign-system only)
  condition: string               # CEL expression
  params: map[string]string
  severity: Severity
  cooldown: Duration
status:
  conditions: []Condition         # Ready, Active
  matchedNamespaces: int32
  lastFired: Time
  faultEventsTotal: int64
```

**CEL environment:** Exposes the resource object, `now()`, `duration()`, `timestamp()`, `params`, and standard CEL functions. Sandboxed — no I/O, cost budget enforced.

### 7.2 WormsignGatherer

Defines custom diagnostic data collection modules.

```yaml
apiVersion: wormsign.io/v1alpha1
kind: WormsignGatherer
spec:
  description: string
  triggerOn:
    resourceKinds: []string       # "Pod", "Node"
    detectors: []string           # detector names
  collect:
    - type: resource|logs
      apiVersion: string
      resource: string
      name: string                # templates: {resource.name}
      jsonPath: string
      listAll: bool
      container: string           # for logs
      tailLines: int
      previous: bool
status:
  conditions: []Condition
```

**Security:** Limited to Kubernetes API operations only — no shell, no arbitrary HTTP.

### 7.3 WormsignSink

Defines custom webhook-based sinks.

```yaml
apiVersion: wormsign.io/v1alpha1
kind: WormsignSink
spec:
  description: string
  type: webhook
  webhook:
    url: SecretKeyRef             # URL stored in Secret
    method: string
    headers: map[string]string
    bodyTemplate: string          # Go text/template with RCAReport fields
  severityFilter: []Severity
status:
  conditions: []Condition
  deliveriesTotal: int64
  lastDelivery: Time
  lastDeliveryStatus: string
```

**SSRF protection:** Webhook URLs validated against `sinks.webhook.allowedDomains` glob patterns.

### 7.4 WormsignPolicy

Defines exclusion filters and suppression rules.

```yaml
apiVersion: wormsign.io/v1alpha1
kind: WormsignPolicy
spec:
  action: Exclude|Suppress
  detectors: []string             # empty = all
  namespaceSelector: LabelSelector  # cross-namespace (wormsign-system only)
  match:
    resourceSelector: LabelSelector
    ownerNames: []string          # glob patterns: "canary-*"
  schedule:
    start: Time
    end: Time
status:
  conditions: []Condition
  matchedResources: int32
  lastEvaluated: Time
```

---

## 8. Observability

### 8.1 Prometheus Metrics

| Metric | Type | Labels | Description |
|--------|------|--------|-------------|
| `wormsign_fault_events_total` | Counter | detector, severity, namespace | Fault events emitted |
| `wormsign_super_events_total` | Counter | correlation_rule, severity | SuperEvents emitted |
| `wormsign_events_filtered_total` | Counter | detector, namespace, reason | Events excluded by filters |
| `wormsign_diagnostic_gather_duration_seconds` | Histogram | gatherer | Gathering latency |
| `wormsign_analyzer_requests_total` | Counter | backend, status | LLM API calls |
| `wormsign_analyzer_tokens_used_total` | Counter | backend, direction | Token consumption |
| `wormsign_analyzer_latency_seconds` | Histogram | — | LLM response latency |
| `wormsign_sink_deliveries_total` | Counter | sink, status | Sink delivery outcomes |
| `wormsign_circuit_breaker_state` | Gauge | — | 0=closed, 1=open, 2=half-open |
| `wormsign_pipeline_queue_depth` | Gauge | stage | Items in each queue |
| `wormsign_shard_namespaces` | Gauge | — | Namespaces assigned to replica |
| `wormsign_leader_is_leader` | Gauge | — | 1 if leader |

### 8.2 Structured Logging

- **Library:** Go stdlib `log/slog` with JSON output
- **Levels:** debug, info, warn, error (configurable via Helm)
- **Standard fields:** `detector`, `namespace`, `resource`, `fault_event_id`, `duration`, `error`, `sink`, `backend`

### 8.3 Health Endpoints

| Endpoint | Type | Semantics |
|----------|------|-----------|
| `/healthz` | Liveness | 200 if heartbeat updated within 30s. Failure triggers pod restart. |
| `/readyz` | Readiness | 200 if API server reachable, informers synced, >=1 detector running. Failure removes pod from endpoints. |
| `/metrics` | Prometheus | Standard Prometheus scrape endpoint. |

---

## 9. Graceful Shutdown

On SIGTERM, the controller performs a 7-step ordered shutdown with per-stage timeouts:

```
Step  Action                                           Timeout
────  ──────────────────────────────────────────────   ───────
  1   Stop accepting new fault events                  immediate
  2   Stop informers                                   5s
  3   Flush correlation window (emit pending events)   —
  4   Drain in-flight gathering workers                30s
  5   Drain in-flight analyzer calls                   60s
  6   Deliver pending sink messages                    30s
  7   Exit                                             —
                                              Total:   ~125s max
```

The Helm chart sets `terminationGracePeriodSeconds: 150` to provide headroom.

---

## 10. Security Model

### 10.1 RBAC (Least Privilege)

**Read-only** cluster access for:
- pods, pods/log, events, nodes, PVCs, PVs, namespaces, jobs, replicasets, deployments, statefulsets, daemonsets
- All 4 Wormsign CRDs

**Write access** limited to:
- `events` — K8s Event sink
- `configmaps` — shard map storage
- `leases` — leader election
- CRD `/status` subresources — status reporting

### 10.2 Secret Management

- API keys stored in Kubernetes Secrets, referenced by name/key
- Never logged, never included in DiagnosticBundles
- Resolved at runtime by `internal/analyzer/secret.go` and `internal/sink/secret.go`

### 10.3 Data Protection

- **Log redaction** (`internal/redact/`): Default patterns for Bearer tokens, basic auth, AWS keys, passwords. Configurable additional patterns.
- **LLM data residency:** Bedrock and Azure OpenAI keep data within cloud accounts. `noop` backend makes zero external calls.
- **CEL sandboxing:** Custom detector expressions cannot perform I/O; cost budget prevents runaway evaluation.
- **Custom gatherer restrictions:** Limited to Kubernetes API — no shell execution, no arbitrary HTTP.
- **Webhook SSRF protection:** Domain allowlist with glob matching on hostname.

### 10.4 Pod Security

Default Helm values enforce a hardened pod security posture:
- `runAsNonRoot: true`, `runAsUser: 65534`
- `readOnlyRootFilesystem: true`
- `allowPrivilegeEscalation: false`
- All capabilities dropped
- `seccompProfile: RuntimeDefault`

---

## 11. Deployment Architecture

### 11.1 Helm Chart Structure

```
deploy/helm/wormsign/
├── Chart.yaml                    # Chart metadata
├── values.yaml                   # All configurable options with defaults
├── crds/                         # CRD manifests (installed before templates)
│   ├── wormsign.io_wormsigndetectors.yaml
│   ├── wormsign.io_wormsigngatherers.yaml
│   ├── wormsign.io_wormsignsinks.yaml
│   └── wormsign.io_wormsignpolicies.yaml
└── templates/
    ├── deployment.yaml           # Controller Deployment
    ├── configmap.yaml            # Config from values.yaml
    ├── serviceaccount.yaml
    ├── clusterrole.yaml          # RBAC
    ├── clusterrolebinding.yaml
    ├── service.yaml              # For metrics scraping
    └── servicemonitor.yaml       # Prometheus Operator (optional)
```

### 11.2 Resource Sizing Guide

| Cluster Size | Replicas | CPU Request/Limit | Memory Request/Limit |
|-------------|----------|-------------------|---------------------|
| Small (<1k pods) | 1 | 100m / 500m | 128Mi / 512Mi |
| Medium (1k-10k) | 1-2 | 250m / 1 CPU | 256Mi / 1Gi |
| Large (10k-50k) | 2-3 | 500m / 2 CPU | 512Mi / 2Gi |
| XL (50k-100k) | 3-5 | 500m / 2 CPU | 1Gi / 4Gi |

### 11.3 HPA Support

The controller exposes `wormsign_pipeline_queue_depth` for autoscaling:

```yaml
apiVersion: autoscaling/v2
kind: HorizontalPodAutoscaler
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

When replicas scale, the coordinator rebalances namespace shards automatically.

---

## 12. Project Structure

```
wormsign/
├── api/
│   └── v1alpha1/                 # CRD type definitions + generated deepcopy
├── cmd/
│   └── controller/
│       └── main.go               # Binary entry point
├── internal/
│   ├── analyzer/                 # LLM backends, circuit breaker, rate limiter
│   │   └── prompt/               # System prompt construction
│   ├── config/                   # Config loading and validation
│   ├── controller/               # Top-level lifecycle orchestration
│   ├── detector/                 # Detector interface + 7 built-in detectors
│   │   └── cel/                  # CEL custom detector engine
│   ├── filter/                   # 9-level exclusion filter engine
│   ├── gatherer/                 # Gatherer interface + 9 built-in gatherers
│   ├── health/                   # Liveness + readiness HTTP server
│   ├── metrics/                  # Prometheus metric definitions
│   ├── model/                    # Core data types (FaultEvent, RCAReport, etc.)
│   ├── pipeline/                 # Pipeline orchestrator
│   │   ├── correlator/           # Pre-LLM event correlation engine
│   │   └── queue/                # Per-stage work queues
│   ├── redact/                   # Log/secret redaction
│   ├── shard/                    # Namespace sharding + informer management
│   └── sink/                     # Sink interface + 6 built-in sinks
├── deploy/
│   └── helm/wormsign/            # Helm chart
├── hack/                         # Code generation scripts
└── test/
    ├── e2e/                      # End-to-end tests (Kind cluster)
    └── fixtures/                 # Sample CRDs and test YAML
```

---

## 13. Key Dependencies

| Dependency | Purpose |
|-----------|---------|
| `k8s.io/client-go` v0.30+ | API server interaction, informers, work queues, leader election |
| `github.com/google/cel-go` v0.20+ | CEL expression evaluation for custom detectors |
| `log/slog` (stdlib) | Structured logging |
| `github.com/prometheus/client_golang` v1.19+ | Prometheus metrics |
| `sigs.k8s.io/controller-tools` v0.14+ | CRD manifest + deepcopy generation |
| `github.com/aws/aws-sdk-go-v2` | Bedrock + S3 integration |
| `golang.org/x/sync/errgroup` | Concurrent gatherer execution |

---

## 14. Architectural Decisions

Key decisions made during implementation (documented in `DECISIONS.md`):

| ID | Decision | Rationale |
|----|----------|-----------|
| D1 | `controller-gen` for deepcopy + CRDs via `hack/codegen.sh` | Spec called for generated code; idempotent script approach |
| D2 | One `SharedInformerFactory` per namespace + cluster-scoped factory | Raw client-go doesn't have `DefaultNamespaces` like controller-runtime |
| D3 | `metadata.NewFilteredMetadataInformer` for pods | Memory-efficient pod watching at scale |
| D4 | CEL environment with K8s type adapters, per-detector env | Resource-specific type safety for CEL expressions |
| D5 | `text/template` for sink payloads with 5s timeout | JSON output, not HTML; timeout prevents runaway templates |
| D6 | AWS SDK default credential chain for S3 | Automatic IRSA pickup |
| D7 | `filepath.Match` globs for webhook SSRF protection | Simple, standard library approach |
| D8 | In-memory correlation buffer, lost on failover | Acceptable — informer re-list replays events |
| D9 | `envtest` for integration tests only | Fake API server without controller-runtime in production code |
| D13 | Custom circuit breaker state machine | Lightweight, avoids external dependency for simple 3-state machine |
| D14 | Cascading `context.Context` with per-stage timeouts | Clean 7-step shutdown with bounded total time |

---

## 15. Future Roadmap (Out of Scope for v1)

These features are not implemented but informed the architecture:

- **Historical correlation:** Store RCA Reports in a time-series DB; use as few-shot examples for the LLM
- **Auto-remediation:** Sinks trigger actions (cordon node, restart pod, scale NodePool) with approval workflows
- **Multi-cluster:** Federated deployments aggregating events across clusters
- **Web dashboard:** Lightweight UI for browsing incidents, reports, and trends
- **Adaptive correlation windows:** Auto-tune window duration based on cluster event velocity
- **Cost reporting:** Per-namespace/per-team LLM cost tracking
