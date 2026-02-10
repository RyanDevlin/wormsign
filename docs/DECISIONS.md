# Architecture Decisions

Decisions made where the PROJECT_SPEC.md was ambiguous or required interpretation.

## D1: Code Generation Strategy

The spec references `controller-gen` for deepcopy and CRD manifests but doesn't specify exactly when/how code generation runs. **Decision:** We generate deepcopy functions and CRD YAML manifests via a `hack/codegen.sh` script that invokes `controller-gen`. Generated code lives in `api/generated/` and `deploy/helm/wormsign/crds/`. The script is idempotent and re-runnable.

## D2: Informer Factory Setup

The spec says to use raw `client-go` shared informers with namespace-scoped informers via `cache.Options.DefaultNamespaces`. However, `DefaultNamespaces` is a `controller-runtime` cache concept. For raw `client-go`, we use `informers.NewSharedInformerFactoryWithOptions` with `informers.WithNamespace()` per shard. **Decision:** Create one `SharedInformerFactory` per assigned namespace, and a single cluster-scoped factory for nodes, PVs, and CRDs (leader only).

## D3: Metadata-Only Informers for Pods

The spec references `k8s.io/client-go/metadata` for metadata-only pod informers. **Decision:** Use `metadata.NewFilteredMetadataInformer` or the metadata informer factory from `k8s.io/client-go/metadata/metadatainformer` for pod watches. Full pod objects are fetched via direct GET during gathering.

## D4: CEL Environment Setup

The spec describes a CEL environment with Kubernetes resource objects, `now()`, `duration()`, `timestamp()`, and `params`. **Decision:** Use `cel-go` with Kubernetes type adapters. The CEL environment is constructed once per detector CRD, with the resource type determined by the `spec.resource` field. Params are injected as a `map[string, string]` variable.

## D5: Go Template Security in Custom Sinks

Custom sink `bodyTemplate` uses `text/template`. **Decision:** Use `text/template` (not `html/template`) since sink payloads are JSON, not HTML. Template execution has a timeout of 5 seconds to prevent runaway templates.

## D6: S3 Sink Authentication

The spec says S3 uses IRSA. **Decision:** Use the default AWS SDK credential chain (`aws-sdk-go-v2/config.LoadDefaultConfig`), which automatically picks up IRSA tokens. No explicit credential configuration needed.

## D7: Webhook SSRF Protection

The spec mentions `allowedDomains` for webhook sinks with glob patterns. **Decision:** Implement domain matching with `filepath.Match`-style globs applied to the URL hostname. URLs that don't match any allowed domain are rejected. For built-in sinks (Slack, PagerDuty), domains are hardcoded and always allowed.

## D8: Correlation Engine Statefulness

The spec says the correlation engine is stateful and single-threaded per replica. **Decision:** The correlator maintains an in-memory buffer of fault events indexed by time window. On leader failover, correlation state is lost (acceptable per spec — events replay from informer re-list). The correlation window is flushed on shutdown.

## D9: Test Framework

The spec references `envtest` for integration tests. **Decision:** Use `sigs.k8s.io/controller-runtime/pkg/envtest` solely as a test dependency for its fake API server (etcd + kube-apiserver binaries). This does not mean we use controller-runtime for the main controller logic.

## D10: Helm Chart Scope

The spec describes a full Helm chart. **Decision:** The Helm chart is generated as static YAML templates with Go template conditionals. We do not use Helmify or any Helm generation tool. The chart is hand-crafted following Helm best practices.

## D11: Configuration Struct

The spec shows `values.yaml` but doesn't define a Go config struct. **Decision:** Create a `Config` struct in `internal/config/` that mirrors `values.yaml` structure exactly. Configuration is loaded from a YAML file mounted via ConfigMap, with environment variable overrides for secrets.

## D12: Concurrent Gatherer Execution

The spec says gatherers within a single event run concurrently via `errgroup`. **Decision:** Use `golang.org/x/sync/errgroup` with a context. Individual gatherer failures populate the `DiagnosticSection.Error` field without canceling other gatherers.

## D13: Circuit Breaker Implementation

The spec describes a closed → open → half-open → closed circuit breaker. **Decision:** Implement a simple state-machine-based circuit breaker in `internal/analyzer/` rather than importing a third-party library. This keeps the dependency footprint small for such a simple state machine.

## D14: Graceful Shutdown Coordination

The spec describes a 7-step ordered shutdown. **Decision:** Use a `context.Context` with cascading cancellation. Each pipeline stage has its own context derived from the parent. Shutdown proceeds by canceling contexts in reverse pipeline order, with per-stage timeouts enforced via `context.WithTimeout`.
