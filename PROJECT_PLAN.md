# Wormsign — Project Plan

**Goal:** Enterprise adoption of Wormsign as the standard Kubernetes fault detection and root cause analysis controller.

**Last updated:** February 2026

---

## Current State

Wormsign is feature-complete per its original specification. The 5-stage pipeline (Detect → Correlate → Gather → Analyze → Sink) is fully implemented with 7 detectors, 9 gatherers, 5 LLM analyzer backends + a rules-based engine, and 7 output sinks. Unit test coverage is ~95% median across 20 packages. All tests pass.

What's missing is everything *around* the code: documentation for users, CI/CD, real-world validation, distribution, and the trust signals that enterprises require before adopting infrastructure software.

---

## Phase 1: Documentation & README

**Why first:** The README is the front door. No one will evaluate the software if they can't understand what it does in 30 seconds. This is the highest-leverage work we can do and it costs nothing but time.

### 1.1 README Rewrite

The current README is a logo and one sentence. The new README should include:

- **Problem statement** — 2-3 sentences on why Kubernetes fault detection is hard and what Wormsign solves ("your clusters are screaming but nobody's listening")
- **Architecture diagram** — the 5-stage pipeline as a visual (ASCII or SVG). The HLD already has the narrative; we need a clean visual for the README
- **Feature matrix** — table showing: built-in detectors, analyzer backends, output sinks, scaling model
- **Quickstart** — 5-line Helm install that gets a user from zero to first fault detection:
  ```
  helm repo add wormsign ...
  helm install wormsign wormsign/wormsign -n wormsign-system --create-namespace
  kubectl apply -f examples/fault-injection/crashloop.yaml
  kubectl get events -l wormsign.io/category
  ```
- **Example output** — screenshot or formatted log showing an actual RCA event with labels/annotations, so users can see the value before installing
- **Configuration highlights** — link to full docs, but show the most important knobs (analyzer backend, enabled detectors, sink configuration)
- **Badge row** — CI status, Go version, license, Helm chart version (once CI exists)

### 1.2 Getting Started Guide (`docs/getting-started.md`)

Full walkthrough from empty cluster to first fault detection:

1. Prerequisites (K8s 1.27+, Helm 3.x)
2. Install CRDs and Helm chart
3. Verify controller is running and healthy
4. Inject a fault (stuck-pending pod)
5. Observe the pipeline: detector fires → correlation window → gathering → analysis → K8s Event created
6. Read the RCA event with `kubectl get events -l wormsign.io/category`
7. Inspect labels, annotations, and remediation steps
8. Configure a Slack/webhook sink for real-time alerts
9. (Optional) Enable LLM-powered analysis with Claude/Bedrock

### 1.3 Configuration Reference (`docs/configuration.md`)

Annotated reference for every Helm value. The current `values.yaml` has 200+ lines of configuration across detectors, sinks, analyzers, filters, sharding, health, and tuning. Enterprise users need to understand every knob before deploying. This doc should be auto-generatable from `values.yaml` comments in the long run, but start with a hand-written version that explains *why* you'd change each setting.

Key sections:
- Detector configuration (thresholds, cooldowns, enable/disable)
- Analyzer backend selection and LLM provider setup
- Sink configuration (Slack webhooks, PagerDuty routing keys, S3 buckets, custom webhooks)
- Namespace filtering and exclusion rules
- Sharding and multi-replica tuning
- Controller tuning (scan intervals, resync periods)
- Security settings (RBAC, pod security, secret references)

### 1.4 Architecture Deep-Dive (`docs/architecture.md`)

Distill the HLD into a more accessible architecture doc for contributors and advanced users. Focus on:

- Pipeline stage interactions and data flow
- How namespace sharding works with consistent hashing
- Leader election and follower behavior
- Informer lifecycle and per-namespace factory management
- Correlation window mechanics (2-minute buffer, flush rules, pattern matching)
- How CRD-based custom detectors/gatherers/sinks work

### 1.5 CRD Examples & Tutorials

The `test/fixtures/` directory has sample CRDs but they're written for testing, not for users. Create a `examples/` directory with well-commented examples:

- `examples/custom-detector.yaml` — CEL-based detector that fires when a pod has been in ImagePullBackOff for > 5 minutes
- `examples/custom-sink-slack.yaml` — Custom webhook sink with Go template body
- `examples/custom-gatherer.yaml` — Gather additional context (e.g., HPA status) during diagnosis
- `examples/policy-exclude-kube-system.yaml` — Policy that excludes system namespaces from detection

---

## Phase 2: CI/CD Pipeline

**Why second:** Before investing in more testing, features, or distribution, we need release infrastructure. Without it, every improvement is manual to ship. CI also provides the trust signals (green badges, automated testing) that enterprises expect.

### 2.1 GitHub Actions — CI

**Pull request workflow (`.github/workflows/ci.yml`):**

1. **Lint** — `golangci-lint run ./...` (the project already has a linter config)
2. **Unit tests** — `go test -race -coverprofile=coverage.out ./...`
3. **Coverage gate** — fail if coverage drops below 90% (current median is ~95%)
4. **Build** — `go build ./...` (verify compilation)
5. **Helm lint** — `helm lint deploy/helm/wormsign/`
6. **Security scan** — `trivy fs .` for vulnerability scanning of Go dependencies

**E2e workflow (`.github/workflows/e2e.yml`):**

Triggered on PRs that touch `internal/`, `cmd/`, `deploy/`, or `test/`:

1. Build `Dockerfile.e2e` image
2. Run with `--privileged` (DinD for Kind)
3. Execute full e2e test suite
4. Upload test results as artifact

This can use the existing `Dockerfile.e2e` which already encapsulates the entire test environment (Kind, kubectl, Helm, Go toolchain).

### 2.2 GitHub Actions — Release

**Tag-triggered workflow (`.github/workflows/release.yml`):**

Triggered on `v*` tags:

1. **Build multi-arch Docker image** — `linux/amd64`, `linux/arm64` via `docker buildx`
2. **Push to GHCR** — `ghcr.io/<org>/wormsign-controller:<tag>`
3. **Sign image** — cosign/Sigstore for supply chain security
4. **Generate SBOM** — `syft` or `trivy` for software bill of materials
5. **Package Helm chart** — `helm package deploy/helm/wormsign/`
6. **Push Helm chart to GHCR OCI registry** — `helm push`
7. **Create GitHub Release** — with changelog auto-generated from commit history
8. **Publish to Artifact Hub** — update `artifacthub-repo.yml` metadata

### 2.3 Makefile Improvements

The current Makefile has `test-e2e` and `test-e2e-local` but is missing common targets:

```makefile
docker-build        # Build controller Docker image
docker-push         # Push to registry
helm-package        # Package Helm chart
helm-push           # Push chart to OCI registry
lint                # Run golangci-lint
test                # Run unit tests with race detector
test-coverage       # Run tests and enforce coverage threshold
generate            # Run code generation (deepcopy, CRD manifests)
```

---

## Phase 3: Real Cluster Validation

**Why third:** Unit tests and Kind-based e2e tests validate correctness but not *usefulness*. Deploying to a real cluster will reveal issues that automated tests can't catch and will generate the most valuable bug list for prioritization.

### 3.1 Deploy to a Real Cluster

Start with a small managed K8s cluster (EKS, GKE, or AKS) — 3-5 nodes is sufficient. The goal is not scale testing; it's validating the full operational experience:

- **Install experience** — Does the Helm chart work cleanly on a real cluster? Are the RBAC roles sufficient? Does the ServiceAccount have the right permissions?
- **Fault detection accuracy** — Deploy real workloads (a simple web app, a cron job, a stateful set) and introduce real faults (kill nodes, exhaust memory, misconfigure images). Does Wormsign detect them reliably? Are there false positives?
- **Event quality** — Do the K8s Events show up cleanly in `kubectl get events`? Are the labels queryable? Do the annotations contain useful information? Do they integrate well with event forwarding tools (Kubernetes Event Exporter, Datadog, etc.)?
- **Correlation accuracy** — When multiple pods fail due to a node going down, does the NodeCascade correlation rule fire? Is the blast radius calculation correct?
- **Resource consumption** — What's the memory/CPU footprint on a small cluster? How does it grow as more namespaces are watched?
- **Upgrade path** — Can you `helm upgrade` without downtime? Does leader election re-establish cleanly?

### 3.2 LLM Analyzer Validation

The rules-based analyzer handles common patterns, but the LLM backends (Claude, Bedrock, OpenAI) are the differentiating feature. Test with a real LLM provider:

- Quality of root cause analysis for common faults (OOM, CrashLoop, scheduling failures)
- Quality for complex/unusual faults (resource quota exhaustion, webhook interference, PDB violations)
- Token budget effectiveness — does the daily/hourly limit work correctly?
- Circuit breaker behavior — does it trip correctly on provider outages?
- Prompt quality — are the system prompts producing actionable remediation steps?
- Cost analysis — what does it cost per fault event with each provider?

### 3.3 Operational Runbook

Based on real cluster experience, document:

- How to debug when Wormsign isn't detecting faults (common: RBAC, namespace filter misconfiguration)
- How to interpret controller logs
- How to verify shard assignments across replicas
- How to manually trigger a diagnostic gather for a specific resource
- Common failure modes and recovery procedures

---

## Phase 4: Test Coverage Expansion

**Why fourth:** Informed by real cluster experience (Phase 3), we'll know exactly what needs testing. This phase focuses on the gaps that matter rather than speculative coverage.

### 4.1 Sink Integration Tests

The sink implementations (Slack, PagerDuty, S3, custom webhook) have unit tests for their construction and configuration, but lack end-to-end delivery tests against mock servers. Build a **mock sink server** — a lightweight HTTP server in the e2e suite that:

- Impersonates Slack's webhook API (accepts JSON payloads, validates format)
- Impersonates PagerDuty's Events API v2 (validates routing key, severity mapping)
- Impersonates S3 (MinIO in the Kind cluster, validates object key format and content)
- Acts as a generic webhook receiver (captures payloads for assertion)

This lets us test the full pipeline end-to-end: fault injection → detection → correlation → gathering → analysis → sink delivery → verify payload arrived at mock server.

### 4.2 Analyzer Backend Tests

The LLM analyzer backends are hard to test meaningfully (mocking an LLM response tests the mock, not the analyzer). Focus on:

- **Prompt construction tests** — verify the prompt builder produces correct prompts for each fault type, including the diagnostic bundle, system prompt overrides, and token budget enforcement
- **Response parsing tests** — feed known LLM response formats through the parser and verify RCAReport construction (especially edge cases: malformed JSON, partial responses, empty fields)
- **Circuit breaker tests** — verify the circuit breaker trips after N consecutive failures and enters half-open state after the cooldown
- **Token budget tests** — verify the daily/hourly rate limiter correctly blocks requests when budget is exhausted
- **Error handling tests** — verify graceful degradation when the LLM provider returns 429, 500, timeout, or malformed responses

### 4.3 Controller Integration Tests

The controller package has 53.7% coverage — the lowest in the project. This is expected (it's integration-heavy), but we should improve it:

- **Informer lifecycle tests** — start/stop/restart the informer manager, verify namespaces are watched/unwatched correctly
- **Shard change handling** — simulate shard reassignment and verify informers are stopped for old namespaces and started for new ones
- **Detector bridge tests** — verify that informer events are correctly converted to detector state types and fed into the pipeline
- **Health check integration** — verify that detector count, informer sync status, and API server reachability are reported correctly

### 4.4 CRD Custom Resource Tests

The CEL-based custom detector engine is complex and under-tested at the integration level:

- **CEL expression evaluation** — test various expression patterns against real K8s objects
- **Cost limit enforcement** — verify that expensive CEL expressions are rejected
- **Custom gatherer template resolution** — test variable substitution in gather commands
- **Custom sink template rendering** — test Go template execution with real RCAReport data
- **CRD validation** — verify that invalid CRDs are rejected by OpenAPI schema validation

### 4.5 Chaos & Resilience Tests

- **Leader failover** — kill the leader pod, verify a follower takes over within the lease duration
- **Shard rebalancing during scaling** — scale from 1→3→1 replicas, verify all namespaces remain covered with no gaps or overlaps
- **API server disconnect** — use network policies to temporarily block API server access, verify graceful degradation and recovery
- **Informer re-list storm** — disconnect and reconnect to force a full re-list, verify the pipeline handles the burst without dropping events

---

## Phase 5: Performance & Load Testing

**Why fifth:** Scale validation is important for enterprise credibility but depends on having a stable, well-tested codebase (Phases 1-4). The good news is that meaningful load testing doesn't require expensive infrastructure.

### 5.1 Synthetic Informer Stress Test

Write a Go benchmark that bypasses the K8s API entirely:

- Generate fake watch events at configurable throughput (100, 1000, 10000 events/sec)
- Feed them directly into the detector bridge
- Measure: detector evaluation latency (p50/p99), pipeline throughput, correlation window memory usage, end-to-end latency from event to sink delivery
- This tests the hot path (detector → correlator → gatherer → analyzer → sink) without any cluster overhead

Target: sustain 1000 fault events/sec with p99 pipeline latency < 500ms.

### 5.2 kwok-Based Large Cluster Simulation

[kwok](https://kwok.sigs.k8s.io/) simulates thousands of fake nodes and pods with near-zero resource cost. Use it to validate:

- **Namespace sharding at scale** — 500 namespaces across 3 replicas, verify even distribution
- **Informer memory model** — measure RSS growth per watched namespace (target: < 5MB per namespace)
- **Shard rebalancing at scale** — add/remove replicas with 500 namespaces, measure rebalancing time and event gaps
- **Metadata informer efficiency** — verify that metadata-only pod informers use significantly less memory than full-object informers

This runs on a laptop and gives confidence for 10k+ node claims.

### 5.3 Realistic Load Test on Kind

A step up from kwok — use Kind with real workloads:

- Create 50 namespaces with 20 pods each (1000 pods total)
- Inject failures in 10% of pods (100 failing pods generating continuous events)
- Run for 30 minutes and measure:
  - Memory growth over time (detect leaks)
  - Event processing lag (does the pipeline fall behind?)
  - Correlation accuracy (are related events grouped correctly?)
  - Sink delivery success rate
- Profile with `go tool pprof` to identify hot spots

### 5.4 Memory Profiling

The most likely performance issue at scale is memory from per-namespace informer factories. Each factory holds caches for watched resource types. Profile:

- Memory per informer factory (namespace)
- Cache growth over time for active namespaces
- Garbage collection after namespace unassignment (when a shard is reassigned)
- Total RSS under various namespace counts (10, 50, 100, 500)

Document the memory model: "Wormsign uses approximately X MB per watched namespace, plus Y MB base overhead."

---

## Phase 6: Production Readiness & Enterprise Trust

**Why last in implementation order (but arguably most important):** Enterprise adoption requires trust signals beyond code quality. This phase covers security, compliance, distribution, and community building.

### 6.1 Supply Chain Security

Enterprises increasingly require supply chain security attestations:

- **Container image signing** — sign all release images with cosign/Sigstore. This lets users verify image provenance
- **SBOM generation** — generate Software Bill of Materials with every release (syft or trivy). Many enterprises now require SBOMs for procurement
- **SLSA Level 2+** — GitHub Actions with artifact attestation provides SLSA Level 2 provenance. Consider progressing to Level 3 with hermetic builds
- **Dependency scanning** — automated Dependabot/Renovate for Go module updates with security advisory alerts
- **`SECURITY.md`** — vulnerability disclosure policy with a dedicated security contact (even if it's just a GitHub Security Advisory)

### 6.2 Third-Party Security Audit

The existing internal security review is thorough and identified real issues. For enterprise credibility, consider:

- **Option A: Paid audit** — firms like Trail of Bits, NCC Group, or Cure53 specialize in K8s/Go security audits. Cost: $30k-$80k depending on scope. Result: a published audit report that enterprises can reference
- **Option B: CNCF-sponsored audit** — if Wormsign were accepted as a CNCF Sandbox project, it would be eligible for a CNCF-funded security audit through OSTIF. This is free but requires CNCF acceptance
- **Option C: Community audit** — publish a security bounty program (even modest amounts) and invite the K8s security community to review. Lower credibility than a professional audit but better than nothing
- **Option D: Marketplace audit** — AWS Marketplace and similar platforms have their own security review processes as part of listing requirements. This provides an independent validation signal

Recommendation: Start with Option D (get the marketplace listing process started, which includes a review) and pursue Option B (CNCF sandbox) in parallel. Consider Option A when revenue justifies it.

### 6.3 Distribution Channels

In order of effort and impact:

1. **GHCR (GitHub Container Registry)** — free, immediate, automated via CI. This is the baseline
2. **Artifact Hub** (artifacthub.io) — where the K8s ecosystem discovers Helm charts. Add an `artifacthub-repo.yml` to the repo, register on the platform. Free, high visibility, takes an afternoon
3. **OperatorHub.io** — the discovery platform for OpenShift/OLM-managed clusters. Requires wrapping the Helm chart in an OLM operator bundle. Moderate effort but critical for Red Hat/OpenShift shops
4. **AWS Marketplace** — enterprise procurement channel. Requires a legal entity, pricing model (free tier + paid support?), and their security review process. Significant paperwork but unlocks enterprise procurement workflows
5. **GCP Marketplace / Azure Marketplace** — same concept, different clouds. Pursue after AWS if there's demand
6. **ECR Public Gallery** — AWS-native image registry, useful for AWS-heavy enterprises

### 6.4 Observability of Wormsign Itself

Enterprise SREs won't deploy infrastructure they can't monitor. Ship:

- **Prometheus metrics** — the metrics package already exists. Ensure it covers: events detected/sec (by detector), pipeline latency p50/p99 (by stage), sink delivery success/failure rate (by sink), correlation window size, informer cache size, shard count
- **Grafana dashboard JSON** — ship a pre-built dashboard alongside the Helm chart (`deploy/helm/wormsign/dashboards/`). Include panels for: pipeline health, detection rate, analysis quality (confidence distribution), sink delivery SLA, resource consumption
- **Alerting rules** — PrometheusRule CRD with alerts for: pipeline backlog > threshold, sink delivery failure rate > 5%, leader election gap > 30s, memory usage growing unbounded
- **Structured log schema documentation** — enterprise log aggregation tools (Splunk, Datadog, ELK) need to know the log fields to parse. Document the JSON log schema

### 6.5 Multi-Tenancy Documentation & Testing

Large enterprises run multi-tenant clusters. The namespace sharding architecture already supports this, but we need to prove it:

- **RBAC isolation documentation** — show that Wormsign respects namespace-scoped RBAC and doesn't leak information between tenants
- **Per-namespace sink routing** — document how to configure different Slack channels or PagerDuty services for different namespaces/teams using policies
- **Policy-based exclusions** — demonstrate excluding specific teams' namespaces from detection (e.g., `kube-system`, `monitoring`)
- **Audit logging** — ensure all sink deliveries are logged with the source namespace for compliance

### 6.6 Upgrade & Migration Strategy

Before declaring v1.0, commit to:

- **CRD versioning strategy** — the current CRDs are `v1alpha1`. Define the path to `v1beta1` and `v1`. Once users deploy CRDs, schema changes become painful
- **Helm chart backwards compatibility policy** — which values can change between minor versions? Document any breaking changes
- **Data migration** — if CRD schemas change, provide a migration tool or conversion webhook
- **Zero-downtime upgrades** — document and test `helm upgrade` behavior: does leader election re-establish? Are in-flight events lost? Is there a detection gap during rollout?

---

## Phase 7: Community & Adoption

### 7.1 Open Source Community

- **`CONTRIBUTING.md`** — how to set up a dev environment, run tests, submit PRs. Include architecture overview for new contributors
- **Issue labels** — `good-first-issue`, `help-wanted`, `bug`, `enhancement`, `documentation`
- **Issue templates** — bug report, feature request, question
- **PR template** — checklist (tests pass, docs updated, no breaking changes)
- **Code of Conduct** — standard Contributor Covenant

### 7.2 Content & Visibility

- **Blog post** — "Why We Built Wormsign: Automated Root Cause Analysis for Kubernetes" — problem statement, architecture overview, demo
- **Demo video** (3-5 minutes) — "Deploy Wormsign, break a pod, see the RCA" — this single video will do more for adoption than any documentation page. Show the full flow: helm install → inject fault → wait → kubectl get events showing the RCA with labels and remediation steps
- **Conference talks** — KubeCon (CFP), CNCF meetups, local K8s meetups. Even a 5-minute lightning talk creates awareness
- **CNCF Landscape** — submit for inclusion in the CNCF Landscape under "Observability and Analysis" → "Monitoring". This is a visibility signal that enterprises check
- **CNCF Sandbox application** — longer-term goal, but starts the process of CNCF endorsement

### 7.3 Integration Ecosystem

Enterprises adopt tools that integrate with their existing stack:

- **Webhook sink + Go template** — the existing custom webhook sink with Go template support already enables arbitrary integrations. But we should provide pre-built templates for popular tools:
  - OpsGenie
  - Microsoft Teams
  - Jira (auto-create tickets from RCA reports)
  - Datadog Events
  - Splunk HEC
- **`wormsign test-sink` CLI command** — a CLI subcommand that sends a synthetic RCA event through a configured sink to verify the integration works. This saves users hours of debugging webhook configurations
- **Kubernetes Event Exporter compatibility** — verify that Wormsign's K8s Events (with labels and annotations) are correctly captured by popular event forwarding tools

---

## Suggested Execution Order

| Priority | Phase | Estimated Effort | Unlocks |
|----------|-------|-----------------|---------|
| **P0** | 1. Documentation & README | 1-2 weeks | All adoption |
| **P0** | 2. CI/CD Pipeline | 1 week | Safe iteration, trust signals |
| **P1** | 3. Real Cluster Validation | 1-2 weeks | Informed prioritization |
| **P1** | 4.1-4.2 Sink & Analyzer Tests | 1 week | Confidence in integrations |
| **P2** | 5.1-5.2 Synthetic + kwok Load Tests | 1 week | Scale credibility |
| **P2** | 6.1 Supply Chain Security | 2-3 days | Enterprise trust |
| **P2** | 6.4 Grafana Dashboard | 2-3 days | Operational confidence |
| **P3** | 4.3-4.5 Controller + Chaos Tests | 1-2 weeks | Resilience confidence |
| **P3** | 5.3-5.4 Realistic Load + Profiling | 1 week | Performance documentation |
| **P3** | 6.2-6.3 Security Audit + Distribution | Weeks-months | Enterprise procurement |
| **P3** | 6.5-6.6 Multi-tenancy + Upgrade Path | 1 week | Enterprise deployment |
| **P4** | 7. Community & Adoption | Ongoing | Growth |

---

## Success Metrics

How we'll know the project is ready for enterprise adoption:

- [ ] A new user can go from zero to first fault detection in under 10 minutes using only the README and getting started guide
- [ ] CI pipeline is green on every PR with >90% coverage enforcement
- [ ] All 7 sinks have end-to-end integration tests with mock servers
- [ ] Load test results published showing sustained 1000 events/sec with <500ms p99 latency
- [ ] At least one real cluster deployment running for 7+ days with zero false positives
- [ ] Container images are signed, SBOMs are generated, and Artifact Hub listing is live
- [ ] Grafana dashboard ships with the Helm chart
- [ ] Demo video published and linked from README
- [ ] At least one external security review completed (marketplace or professional audit)
