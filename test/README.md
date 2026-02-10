# Wormsign Local Test Harness

End-to-end testing of the Wormsign controller on a local Kind cluster.

## Prerequisites

| Tool | Version | Install |
|------|---------|---------|
| [Kind](https://kind.sigs.k8s.io/) | v0.27.0+ | `go install sigs.k8s.io/kind@v0.27.0` |
| [kubectl](https://kubernetes.io/docs/tasks/tools/) | v1.31+ | See Kubernetes docs |
| [Helm](https://helm.sh/) | v3.17+ | See Helm docs |
| Docker | any | System package or Docker Desktop |

Tool paths default to `~/go/bin/kind`, `/tmp/kubectl`, and `/tmp/linux-amd64/helm`. Override with environment variables:

```bash
export KIND=/usr/local/bin/kind
export KUBECTL=/usr/local/bin/kubectl
export HELM=/usr/local/bin/helm
```

## Quick Start

```bash
# 1. Stand up the cluster and deploy Wormsign
./hack/local-test.sh up

# 2. Deploy faulty workloads to trigger detectors
./hack/local-test.sh inject

# 3. Wait ~2 minutes for detector thresholds/cooldowns, then check logs
./hack/local-test.sh logs

# 4. Tear down when done
./hack/local-test.sh down
```

## Commands

| Command | Description |
|---------|-------------|
| `up` | Create a 3-node Kind cluster (1 control-plane, 2 workers), build the controller image, install CRDs, and deploy via Helm |
| `inject` | Apply all fault-injection manifests from `test/fixtures/fault-injection/` |
| `logs` | Tail the controller pod logs (`-f --tail=200`) |
| `status` | Show controller pods, fault-injection pods, recent cluster events, and CRD resource counts |
| `rebuild` | Rebuild the Docker image and rolling-restart the deployment (fast iteration loop) |
| `down` | Delete the Kind cluster |

## What `up` Does

1. Creates a Kind cluster named `wormsign-test` using `hack/kind-config.yaml` (3 nodes)
2. Builds the controller Docker image (`wormsign-controller:dev`)
3. Loads the image into all Kind nodes (with snap Docker fallback — see below)
4. Installs CRDs from `deploy/helm/wormsign/crds/`
5. Deploys the Helm chart with test-friendly overrides:
   - `analyzer.backend=noop` (no LLM calls)
   - `logging.level=debug`
   - `detectors.podStuckPending.threshold=1m` (default is 15m)
   - `detectors.*.cooldown=2m` (default is 30m)
   - `detectors.jobDeadlineExceeded.enabled=true` (disabled by default)
6. Waits for the controller deployment to become ready

## Fault-Injection Workloads

All manifests live in `test/fixtures/fault-injection/` and deploy to the `default` namespace.

| Manifest | Pod/Job Name | Detector Triggered | How It Works |
|----------|-------------|-------------------|-------------|
| `crashloop.yaml` | `fault-crashloop` | PodCrashLoop | Exits immediately with code 1; enters CrashLoopBackOff after ~3 restarts |
| `oom-killed.yaml` | `fault-oom-killed` | PodFailed, PodCrashLoop | Allocates memory until OOM-killed (exit 137); 16Mi memory limit |
| `stuck-pending.yaml` | `fault-stuck-pending` | PodStuckPending | Requests 100 CPUs / 512Gi memory — can never be scheduled |
| `failing-job.yaml` | `fault-deadline-job` | JobDeadlineExceeded | Job with 5s `activeDeadlineSeconds`; task sleeps for 30s |
| `bad-image.yaml` | `fault-bad-image` | PodFailed | References `registry.example.com/nonexistent/image:v999.999.999` |

All pods carry the label `app=wormsign-fault-test` for easy listing:

```bash
kubectl get pods -l app=wormsign-fault-test
```

## Pipeline Flow

After injection, events flow through the 5-stage pipeline:

```
Detect → Correlate → Gather → Analyze → Sink
```

1. **Detect**: Informer events trigger detector `Check()` methods. Time-based detectors (PodStuckPending) also run on a 30-second periodic scan.
2. **Correlate**: Events are buffered for 2 minutes, then correlation rules check for patterns (node cascade, deployment rollout, namespace storm).
3. **Gather**: Diagnostic data is collected (pod logs, node conditions, events).
4. **Analyze**: With `backend=noop`, no LLM call is made — the event passes through.
5. **Sink**: Reports are delivered to enabled sinks (KubernetesEvent sink is on by default in the test config).

## What to Look for in Logs

With `logging.level=debug`, you'll see structured JSON logs. Key messages:

```bash
# Detector firing
"msg":"fault detected","detector":"PodCrashLoop","pod":"fault-crashloop"

# Event entering pipeline
"msg":"fault event submitted"

# Correlation window flush
"msg":"correlation window flushed"

# Gathering
"msg":"gathering diagnostic data"

# Sink delivery
"msg":"delivering report"
```

Filter logs for detector activity:

```bash
./hack/local-test.sh logs | grep -E '"detector"|"fault detected"|"fault event"'
```

## Timing

With the test-friendly thresholds set by `up`:

| Detector | Fires After |
|----------|-------------|
| PodCrashLoop | ~1-2 min (needs 3+ restarts with backoff) |
| PodFailed | Immediately on pod failure (2 min cooldown between re-fires) |
| PodStuckPending | ~1 min (threshold) + up to 30s (scan interval) |
| JobDeadlineExceeded | ~5s (activeDeadlineSeconds) |

The correlation window adds a 2-minute buffer before events flow downstream.

## Snap Docker Workaround

On Ubuntu with snap-installed Docker, `kind load docker-image` fails due to `/tmp` permission restrictions. The test harness handles this automatically: if `kind load` fails, it falls back to piping the image through containerd on each node:

```bash
docker save <image> | docker exec -i <node> ctr --namespace=k8s.io images import --all-platforms -
```

If you see "0.0 B" in the containerd import output, the image layers already exist (content-addressable dedup). To force a fresh load, rebuild with `--no-cache`:

```bash
docker build --no-cache -t wormsign-controller:dev .
./hack/local-test.sh rebuild
```

## Development Loop

For iterating on controller code changes:

```bash
# Make code changes, then:
./hack/local-test.sh rebuild

# Check logs
./hack/local-test.sh logs

# If fault-injection pods were cleaned up, re-inject:
./hack/local-test.sh inject
```

## Cleanup

Delete all fault-injection pods without tearing down the cluster:

```bash
kubectl delete pods -l app=wormsign-fault-test
kubectl delete job fault-deadline-job
```

Full teardown:

```bash
./hack/local-test.sh down
```

## Directory Structure

```
test/
├── README.md                    # This file
├── e2e/
│   ├── suite_test.go            # E2E test suite setup
│   └── integration_test.go      # Integration tests
└── fixtures/
    ├── fault-injection/
    │   ├── bad-image.yaml       # ImagePullBackOff → PodFailed
    │   ├── crashloop.yaml       # CrashLoopBackOff → PodCrashLoop
    │   ├── failing-job.yaml     # DeadlineExceeded → JobDeadlineExceeded
    │   ├── oom-killed.yaml      # OOM kill → PodFailed + PodCrashLoop
    │   └── stuck-pending.yaml   # Unschedulable → PodStuckPending
    ├── sample-detector.yaml     # Example WorksignDetector CR
    ├── sample-gatherer.yaml     # Example WorksignGatherer CR
    ├── sample-policy.yaml       # Example WorksignPolicy CR
    ├── sample-sink.yaml         # Example WorksignSink CR
    ├── test-namespace.yaml      # Test namespace manifest
    ├── test-pod-crashloop.yaml  # Standalone crashloop pod fixture
    └── test-pod-pending.yaml    # Standalone pending pod fixture
```
