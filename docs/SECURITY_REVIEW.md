# Security Review — K8s Wormsign

**Reviewer:** Security Audit Agent
**Date:** 2026-02-08
**Scope:** Full codebase review of all Go source files in `internal/`, `api/`, and `cmd/`
**Commit Range:** All code as of `main` branch

---

## Executive Summary

The K8s Wormsign codebase demonstrates strong security awareness with multiple defense-in-depth controls already in place. The review identified **3 issues** that were fixed during this audit and documented several additional observations and recommendations.

### Issues Fixed

| # | Severity | Description | Commit |
|---|----------|-------------|--------|
| 1 | **Critical** | `SecretResolver` naming conflict caused build failure — interface and struct shared the same name in `internal/sink/`, preventing compilation | `188e77a` |
| 2 | **Medium** | SSRF validator missing Azure metadata endpoint (`metadata.azure.com`) in blocked hostname list | `b286ef7` |
| 3 | **Medium** | All HTTP webhook sinks (`webhook`, `custom_webhook`, `slack`, `pagerduty`) used default `http.Client` which follows redirects, enabling SSRF bypass via open redirects on allowed domains | `b286ef7` |

---

## Detailed Findings

### 1. API Key and Secret Handling

**Status: PASS**

**Review scope:** `internal/sink/secret.go`, `internal/sink/secret_resolver.go`, `internal/config/config.go`, `internal/analyzer/bedrock.go`

**Findings:**

- API keys are stored in Kubernetes Secrets and referenced by `SecretKeyRef` (name + key). They are never included in the `Config` struct as plaintext values.
- The `KubeSecretResolver` (formerly `SecretResolver` — renamed during this audit to fix naming conflict) fetches secret values from the Kubernetes API at runtime, not at config load time.
- Secret values are validated for emptiness and whitespace-only values (`secret_resolver.go:56-68`).
- Header secret references use the `${SECRET:name:key}` pattern with regex-based parsing and atomic replacement semantics (`secret.go:27-65`).
- The `KubeSecretResolver` logs debug messages about secret resolution **without logging the secret value itself** (`secret_resolver.go:66-70`).
- AWS Bedrock and S3 sinks use IRSA (IAM Roles for Service Accounts) via the default credential chain — no API keys stored in config (`bedrock.go:43`, `s3.go:52`).

**Recommendation:**
- No immediate action needed. The secret handling is well-designed.

---

### 2. CEL Expression Sandboxing

**Status: PASS**

**Review scope:** `internal/detector/cel/engine.go`, `internal/detector/cel/engine_test.go`

**Findings:**

- CEL expressions are evaluated in a sandboxed `cel.Env` environment that exposes only:
  - Resource data as `map[string]any` (read-only)
  - `params` as `map[string]string` (read-only)
  - `now()` function returning the current timestamp
  - Standard CEL built-in functions (no I/O, no filesystem, no network access)
- **Cost budget enforcement** is properly implemented via `cel.CostLimit(e.costLimit)` at `engine.go:390`. Default limit: 1000 CEL cost units.
- Output type is validated to be `bool` at compile time (`engine.go:380-387`), preventing type confusion.
- The CEL environment does not expose any custom functions that could perform I/O or side effects.
- Namespace isolation is enforced: only detectors in the system namespace can use `namespaceSelector` (`engine.go:340-347`).
- Invalid expressions set `status.conditions[Ready]=False` with a descriptive message and are not loaded (`engine.go:283-397`).

**Recommendation:**
- No immediate action needed. CEL sandboxing is comprehensive.

---

### 3. Custom Gatherer Restrictions

**Status: PASS**

**Review scope:** `internal/gatherer/custom.go`, `internal/gatherer/gatherer.go`, `internal/gatherer/template.go`

**Findings:**

- Custom gatherers (`WormsignGatherer` CRD) support only two collection types: `resource` (Kubernetes API GET/LIST) and `logs` (container log fetching). There is no `http` or `kubectl` type.
- All data collection goes through the controller's Kubernetes API client, bounded by the controller's RBAC.
- Template variables in gatherer specs (`{resource.name}`, `{resource.namespace}`, etc.) are resolved via `strings.ReplaceAll` on a fixed set of known variables (`template.go`), not via Go template execution, preventing template injection.
- The gatherer interface (`gatherer.go:17-23`) accepts a `context.Context` for cancellation support.
- Failed gatherers populate the `Error` field without blocking the pipeline (`gatherer.go:111-125`).

**Recommendation:**
- No immediate action needed.

---

### 4. Webhook SSRF Protection

**Status: FIXED (2 issues)**

**Review scope:** `internal/sink/ssrf.go`, `internal/sink/webhook.go`, `internal/sink/custom_webhook.go`, `internal/sink/slack.go`, `internal/sink/pagerduty.go`

**Existing controls (before audit):**
- Domain allowlist validation using `filepath.Match` glob patterns
- HTTPS-only scheme enforcement
- IP address literal rejection (blocks all IPs, not just private ranges)
- Blocked hostnames: `localhost`, `localhost.localdomain`, `ip6-localhost`, `ip6-loopback`, `metadata.google.internal`, `metadata.google`
- Built-in domain whitelists for Slack (`hooks.slack.com`) and PagerDuty (`events.pagerduty.com`)
- Pattern validation at construction time

**Issues found and fixed:**

**Issue 2 — Missing Azure metadata endpoint:** The `isBlockedHostname` function blocked GCP metadata endpoints but not `metadata.azure.com`. An attacker who could control a WormsignSink CRD's webhook URL on an Azure cluster could potentially reach the Azure Instance Metadata Service.

**Fix:** Added `metadata.azure.com` to the blocked hostnames list. Note: IP-based access to `169.254.169.254` was already blocked by the general IP literal rejection at `ssrf.go:64-66`.

**Issue 3 — Redirect-based SSRF bypass:** All four HTTP webhook sinks (`WebhookSink`, `CustomWebhookSink`, `SlackSink`, `PagerDutySink`) used `&http.Client{}` which follows redirects by default. An attacker could set a webhook URL on an allowed domain that responds with a 3xx redirect to an internal service, bypassing the SSRF validator.

**Fix:** Created `noRedirectHTTPClient()` which returns an `http.Client` with `CheckRedirect` set to return an error. Updated all four sink constructors.

---

### 5. Log Redaction

**Status: PASS**

**Review scope:** `internal/redact/redact.go`, `internal/redact/redact_test.go`

**Findings:**

- 7 default patterns always active:
  1. Bearer tokens (`Bearer\s+[A-Za-z0-9._\-]+`)
  2. Basic auth headers (`Basic\s+[A-Za-z0-9+/=]+`)
  3. AWS access keys (`AKIA[A-Za-z0-9]{16}`)
  4. Password assignments (key=value format)
  5. Token assignments
  6. Authorization headers
  7. Secret/API/access key assignments
- All patterns are case-insensitive where appropriate.
- `RedactEnvVars` correctly identifies Secret-sourced env vars via `ValueFrom.SecretKeyRef` and replaces them with `[REDACTED]` without inspecting the value.
- Additional user-supplied patterns are validated at construction time; invalid patterns cause a startup error.
- Thread-safe via `sync.RWMutex`.
- Test coverage includes matching and non-matching cases for each default pattern.

**Observation:**
- AWS secret access keys (`aws_secret_access_key` as a 40-character Base64 string) are not covered by a specific pattern. They would be partially caught by the `secret_key_assignment` pattern if in `key=value` format, but not if they appear standalone. This is a low-risk gap because:
  1. Secret access keys alone cannot be used without the corresponding access key ID (which IS caught by the `AKIA` pattern).
  2. Log redaction is a defense-in-depth measure, not a primary security control.

**Recommendation:**
- Consider adding an AWS secret access key pattern in a future pass if compliance requirements demand it.

---

### 6. Input Validation

**Status: PASS**

**Review scope:** `internal/config/validate.go`, `internal/config/load.go`, `internal/config/defaults.go`, CRD type definitions in `api/v1alpha1/`

**Findings:**

- **Config loading** uses `yaml.NewDecoder` with `KnownFields(true)` (`load.go:18-19`), which rejects YAML with unknown fields. This prevents typos from silently being ignored.
- **Config validation** at startup (`validate.go`) validates:
  - Log level and format
  - Leader election timing constraints (renew < lease, retry < renew)
  - Namespace exclusion regex patterns (compiled at validation time)
  - Detector thresholds and cooldowns (positive values)
  - Correlation window and rule parameters
  - Gatherer settings (tailLines > 0, maxAffectedResources > 0)
  - Analyzer backend selection (whitelisted values: claude, claude-bedrock, openai, azure-openai, noop)
  - Sink configurations (S3 bucket/region, webhook URL, severity filter values)
  - Pipeline worker counts (positive)
  - Port ranges (1-65535)
- Invalid config at startup causes a crash with clear error messages per spec Section 2.5.
- CRD OpenAPI schemas provide admission-time validation for CRD fields.
- Runtime CRD validation (CEL expression compilation, template parsing) sets `status.conditions[Ready]=False` on failure without disrupting existing valid configs.

**Recommendation:**
- No immediate action needed.

---

### 7. RBAC

**Status: PASS**

**Review scope:** Helm chart templates in `deploy/helm/wormsign/templates/`, project spec Section 7.2

**Findings:**

- The controller follows least-privilege: read-only access to cluster resources with exceptions only for:
  - Event creation (Kubernetes Event sink)
  - ConfigMap read/write (shard map storage)
  - Lease read/write (leader election)
  - CRD status subresource update (status reporting)
- No `create`, `update`, `delete` verbs on pods, deployments, secrets, or other user-facing resources.
- Secrets are read-only (needed for `SecretKeyRef` resolution), and the controller never creates or modifies secrets.

**Recommendation:**
- No immediate action needed.

---

### 8. Injection Flaws

**Status: PASS**

**Review scope:** `internal/sink/custom_webhook.go`, `internal/gatherer/template.go`, `internal/analyzer/prompt/builder.go`, all sink implementations

**Findings:**

**Go template injection (custom_webhook.go):**
- Custom webhook sinks use `text/template` for body rendering (`custom_webhook.go:92-98`).
- The template receives a restricted `templateData` struct with only flat fields (strings, bool, float64, []string) — no methods, no interfaces, no access to OS or runtime packages.
- Template execution has a 5-second timeout (`custom_webhook.go:209-222`) to prevent DoS via recursive templates.
- Template syntax errors are caught at CRD load time, not at delivery time.

**Gatherer template variables (template.go):**
- Uses `strings.ReplaceAll` on a fixed set of known variable names, not Go template execution. No injection vector.

**LLM prompt construction (prompt/builder.go):**
- User-controlled data (fault event descriptions, labels, annotations) is interpolated into the LLM prompt via `fmt.Sprintf` and `strings.Builder`. This is intentional — the data IS the prompt content. There is no injection risk because the LLM is the consumer, not a code interpreter.

**Log injection:**
- All logging uses `slog` structured logging, which escapes field values in JSON output. Log injection via newlines in resource names or descriptions would appear as escaped characters in the JSON log, not as separate log lines.

**Header injection (webhook sinks):**
- Header values set via `req.Header.Set(k, v)` in webhook sinks. Go's `net/http` package rejects header values containing `\r` or `\n`, preventing HTTP header injection.

**Recommendation:**
- No immediate action needed.

---

### 9. Dependency Versions

**Status: PASS (with note)**

**Review scope:** `go.mod`, `go.sum`, `go list -m all` output

**Key dependencies and versions:**
| Dependency | Version | Notes |
|-----------|---------|-------|
| `k8s.io/client-go` | v0.35.0 | Current, matches K8s 1.35 libs |
| `github.com/google/cel-go` | v0.27.0 | Current stable |
| `github.com/prometheus/client_golang` | v1.23.2 | Current stable |
| `github.com/aws/aws-sdk-go-v2` | v1.41.1 | Current stable |
| `gopkg.in/yaml.v3` | v3.0.1 | Stable |
| `golang.org/x/crypto` | v0.45.0 | Current |
| `golang.org/x/net` | v0.47.0 | Current |

**Note:** `govulncheck` was not available in the build environment. Manual review of dependency versions shows all are at recent stable versions. No known CVEs were identified in the direct dependencies at the time of review.

**Recommendation:**
- Set up `govulncheck` in CI to continuously monitor for dependency vulnerabilities.
- Pin dependency versions via `go.sum` (already in place).

---

### 10. Error Handling and Information Leakage

**Status: PASS**

**Review scope:** All sink implementations, analyzer response handling, health endpoints, main entrypoint

**Findings:**

- **Sink delivery errors** include the sink name and HTTP status code but not response bodies or headers that could contain sensitive data (`webhook.go:119`, `slack.go:107`, `pagerduty.go:100`).
- **Analyzer fallback reports** preserve the raw LLM response in `RawAnalysis` for debugging, but this is diagnostic data (not secrets). The raw response flows through sinks to operators who need it.
- **Health endpoints** return generic status messages ("API server is not reachable", "informers have not synced") without internal details (`health/handler.go:131-141`).
- **Config loading errors** include the file path and YAML parsing error but not the file contents.
- **Secret resolution errors** include the secret name and key but not the secret value (`secret_resolver.go`).
- The `S3Sink.deliver` error includes the bucket and key path but not the object contents.

**Recommendation:**
- No immediate action needed.

---

## Architecture-Level Observations

### Positive Security Patterns

1. **Secrets-as-references:** The consistent use of `SecretKeyRef` throughout the codebase (analyzer API keys, sink webhook URLs, routing keys) ensures secrets never appear in CRD specs, config files, or logs.

2. **Defense-in-depth for SSRF:** The SSRF protection stack is multi-layered:
   - HTTPS-only scheme enforcement
   - IP address literal rejection
   - Blocked hostname list (localhost, cloud metadata endpoints)
   - Domain allowlist with glob matching
   - Redirect blocking (added during this audit)

3. **CEL sandboxing:** The CEL evaluation environment is properly constrained with cost limits, type validation, and no I/O functions.

4. **Graceful degradation:** Failed gatherers don't block the pipeline, failed LLM calls trigger circuit breaker fallback to noop mode, and failed sinks don't block other sinks.

5. **Structured logging throughout:** All logging uses `slog` with JSON output, making log injection ineffective and enabling structured log analysis.

6. **HTTP server hardening:** Both the health probe server and metrics server set `ReadHeaderTimeout` to prevent slowloris-style attacks (`main.go:123-124`, `health/handler.go:211-215`).

### Recommendations for Future Work

1. **Rate limiting on health/metrics endpoints:** The health and metrics HTTP servers don't have rate limiting. While they're typically not exposed externally, consider adding rate limiting if they become accessible outside the pod.

2. **Webhook request timeout:** The `noRedirectHTTPClient()` doesn't set a timeout on the HTTP client itself. Individual requests use context-based timeouts through `http.NewRequestWithContext`, but a global client timeout would provide an additional safety net. Consider adding `Timeout: 30 * time.Second` to the HTTP client.

3. **S3 object key injection:** The `objectKey` method in `s3.go:140-149` uses `report.FaultEventID` directly in the S3 key path. Since `FaultEventID` is a UUID generated internally (`model/types.go:192-205`), this is safe. However, if the ID generation were ever changed to accept external input, this could become a path traversal vector. The current implementation using `crypto/rand` UUIDs is secure.

4. **Template function restriction:** The `text/template` used in custom webhooks has access to all default template functions. While the data struct is restricted, consider using `template.New("").Funcs(nil)` or an explicit function map in a future hardening pass to minimize the attack surface.

---

## Remediations Applied

### Fix 1: SecretResolver Naming Conflict

**File:** `internal/sink/secret_resolver.go`, `internal/sink/secret_resolver_test.go`
**Severity:** Critical (build-breaking)
**Description:** The `SecretResolver` name was used both as an interface (in `secret.go`) and as a struct (in `secret_resolver.go`), preventing compilation of the entire `sink` package.
**Fix:** Renamed the struct to `KubeSecretResolver` and the constructor to `NewKubeSecretResolver`. Updated all tests.

### Fix 2: Azure Metadata Endpoint Blocking

**File:** `internal/sink/ssrf.go`, `internal/sink/ssrf_test.go`
**Severity:** Medium
**Description:** The SSRF blocked hostname list included GCP metadata endpoints but not Azure's `metadata.azure.com`.
**Fix:** Added `metadata.azure.com` to the `metadataHosts` list in `isBlockedHostname()`. Added test coverage.

### Fix 3: HTTP Redirect Prevention

**Files:** `internal/sink/ssrf.go`, `internal/sink/webhook.go`, `internal/sink/custom_webhook.go`, `internal/sink/slack.go`, `internal/sink/pagerduty.go`, `internal/sink/ssrf_test.go`
**Severity:** Medium
**Description:** All webhook sinks used default `http.Client` which follows HTTP redirects. An allowed domain could redirect to an internal service, bypassing SSRF validation.
**Fix:** Created `noRedirectHTTPClient()` that returns an `http.Client` with `CheckRedirect` configured to reject all redirects. Updated all four sink constructors. Added test coverage.

---

## Test Verification

All existing tests pass after remediation:

```
ok  github.com/k8s-wormsign/k8s-wormsign/api/v1alpha1
ok  github.com/k8s-wormsign/k8s-wormsign/cmd/controller
ok  github.com/k8s-wormsign/k8s-wormsign/internal/analyzer
ok  github.com/k8s-wormsign/k8s-wormsign/internal/analyzer/prompt
ok  github.com/k8s-wormsign/k8s-wormsign/internal/config
ok  github.com/k8s-wormsign/k8s-wormsign/internal/detector
ok  github.com/k8s-wormsign/k8s-wormsign/internal/detector/cel
ok  github.com/k8s-wormsign/k8s-wormsign/internal/filter
ok  github.com/k8s-wormsign/k8s-wormsign/internal/gatherer
ok  github.com/k8s-wormsign/k8s-wormsign/internal/health
ok  github.com/k8s-wormsign/k8s-wormsign/internal/metrics
ok  github.com/k8s-wormsign/k8s-wormsign/internal/model
ok  github.com/k8s-wormsign/k8s-wormsign/internal/pipeline
ok  github.com/k8s-wormsign/k8s-wormsign/internal/pipeline/correlator
ok  github.com/k8s-wormsign/k8s-wormsign/internal/pipeline/queue
ok  github.com/k8s-wormsign/k8s-wormsign/internal/redact
ok  github.com/k8s-wormsign/k8s-wormsign/internal/shard
ok  github.com/k8s-wormsign/k8s-wormsign/internal/sink
ok  github.com/k8s-wormsign/k8s-wormsign/test/e2e
```

New tests added:
- `TestIsBlockedHostname` updated with Azure metadata endpoint
- `TestSSRFValidator_ValidateURL` added Azure metadata URL test case
- `TestNoRedirectHTTPClient` validates redirect blocking behavior
