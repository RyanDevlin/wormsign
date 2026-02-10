# Agent Guide

This file helps AI agents (Claude, Copilot, Cursor, etc.) quickly orient in the Wormsign codebase.

## What is Wormsign?

A Kubernetes-native fault detection controller written in Go using raw `client-go`. It watches cluster state for anomalies, collects diagnostic context, performs root cause analysis, and delivers actionable insights to operators.

**Pipeline:** Detect → Correlate → Gather → Analyze → Sink

## Project Layout

```
cmd/controller/          Entry point (main.go)
internal/
  analyzer/              RCA analysis backends (rules, noop, claude, bedrock)
  config/                YAML config parsing and validation
  controller/            Main controller loop, detector bridge, informer wiring
  correlator/            Event correlation (time-window buffering, pattern rules)
  detect/                Fault detectors (CrashLoop, OOM, StuckPending, etc.)
  filter/                Namespace/annotation-based exclusion engine
  gather/                Diagnostic data collection (logs, events, descriptions)
  health/                Liveness/readiness probe server
  metrics/               Prometheus metrics
  model/                 Shared types (FaultEvent, RCAReport, ResourceRef, etc.)
  pipeline/              Stage-based pipeline orchestration
  shard/                 Namespace sharding with consistent hashing
  sink/                  Output sinks (K8s Events, S3, Slack, PagerDuty, webhook)
api/                     CRD types and generated deepcopy
deploy/helm/wormsign/    Helm chart (templates, values, CRDs)
hack/                    Dev scripts (codegen, kind config, local test harness)
test/
  e2e/                   End-to-end tests (run against Kind cluster)
  fixtures/              K8s manifests for fault injection and CRD examples
```

## Documentation

Documentation is split into two directories:

### `docs/` — Human-facing design docs

These are standard project documentation written for human engineers.

| File | Description |
|------|-------------|
| [SYSTEM_HLD.md](docs/SYSTEM_HLD.md) | High-level system design and architecture |
| [DECISIONS.md](docs/DECISIONS.md) | Architecture decisions where the spec was ambiguous |
| [SECURITY_REVIEW.md](docs/SECURITY_REVIEW.md) | Security audit findings and mitigations |
| [test/README.md](test/README.md) | Local test harness and e2e testing guide |

### `docs/agents/` — Agent-specific context and harness artifacts

These files are from the original agent harness that built this project. They contain the full requirements spec, task definitions, and validation results. They're preserved for auditing and to give agents deep project context when resuming work.

| File | Description |
|------|-------------|
| [PROJECT_SPEC.md](docs/agents/PROJECT_SPEC.md) | Full requirements specification (60KB) — the original task spec |
| [tasks.json](docs/agents/tasks.json) | Task definitions and completion status from the build harness |
| [init.sh](docs/agents/init.sh) | Project initialization script used by the harness |
| [VALIDATION_PASSED](docs/agents/VALIDATION_PASSED) | Validation results from the build harness |
| [.validation_round](docs/agents/.validation_round) | Validation round counter |

**Agents:** If you need the full requirements spec or want to understand the original project scope, start with `docs/agents/PROJECT_SPEC.md`.

## Build & Test

```bash
go build ./...                          # Build all packages
go test ./...                           # Run unit tests
go test -tags e2e ./test/e2e/           # Run e2e tests (requires Kind cluster)
make docker-build                       # Build Docker image
./hack/local-test.sh up                 # Stand up local Kind cluster + deploy
```

## Key Conventions

- **No controller-runtime** — uses raw `client-go` shared informers
- Detectors have `Check()` methods called externally; `Start()` blocks on ctx
- One `SharedInformerFactory` per assigned namespace (namespace sharding)
- Correlation window buffers events for 2 minutes before flushing downstream
- Leader election via `coordination.k8s.io/v1` Lease objects
- Helm values control all runtime configuration (detectors, sinks, tuning, etc.)
