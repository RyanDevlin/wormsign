#!/usr/bin/env bash
#
# local-test.sh — Build, deploy, and test Wormsign on a local Kind cluster.
#
# Usage:
#   ./hack/local-test.sh up        Create cluster, build image, deploy controller
#   ./hack/local-test.sh inject    Deploy fault-injection workloads
#   ./hack/local-test.sh logs      Tail controller logs
#   ./hack/local-test.sh status    Show controller pod status and recent events
#   ./hack/local-test.sh down      Tear down the Kind cluster
#   ./hack/local-test.sh rebuild   Rebuild image and redeploy (fast iteration)
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"

# Tool paths — use locally installed versions if available
KIND="${KIND:-${HOME}/go/bin/kind}"
KUBECTL="${KUBECTL:-/tmp/kubectl}"
HELM="${HELM:-/tmp/linux-amd64/helm}"
DOCKER="${DOCKER:-docker}"

CLUSTER_NAME="wormsign-test"
IMAGE_NAME="wormsign-controller"
IMAGE_TAG="dev"
NAMESPACE="wormsign-system"
RELEASE_NAME="wormsign"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log()  { echo -e "${GREEN}[wormsign]${NC} $*"; }
warn() { echo -e "${YELLOW}[wormsign]${NC} $*"; }
err()  { echo -e "${RED}[wormsign]${NC} $*" >&2; }

check_tools() {
    local missing=0
    for tool in "$KIND" "$KUBECTL" "$HELM" "$DOCKER"; do
        if ! command -v "$tool" &>/dev/null && [ ! -x "$tool" ]; then
            err "Required tool not found: $tool"
            missing=1
        fi
    done
    if [ $missing -ne 0 ]; then
        err "Install missing tools first. See hack/README or run:"
        err "  go install sigs.k8s.io/kind@v0.27.0"
        exit 1
    fi
}

cluster_exists() {
    "$KIND" get clusters 2>/dev/null | grep -q "^${CLUSTER_NAME}$"
}

# Load a Docker image into all Kind nodes. Uses `kind load` by default;
# falls back to piping through containerd when snap Docker is detected.
load_image() {
    log "Loading image into Kind cluster..."

    # Try `kind load` first — works on non-snap Docker installations.
    if "$KIND" load docker-image "${IMAGE_NAME}:${IMAGE_TAG}" --name "${CLUSTER_NAME}" 2>/dev/null; then
        log "Image loaded via kind"
        return
    fi

    # Fallback: pipe through containerd on each node (snap Docker workaround).
    warn "kind load failed (snap Docker?), loading via containerd..."
    local nodes
    nodes=$("$KIND" get nodes --name "${CLUSTER_NAME}" 2>/dev/null)
    for node in $nodes; do
        log "  Loading into ${node}..."
        "$DOCKER" save "${IMAGE_NAME}:${IMAGE_TAG}" | \
            "$DOCKER" exec -i "${node}" ctr --namespace=k8s.io images import --all-platforms -
    done
    log "Image loaded into all nodes"
}

cmd_up() {
    check_tools

    # Step 1: Create Kind cluster
    if cluster_exists; then
        log "Cluster '${CLUSTER_NAME}' already exists, reusing it"
    else
        log "Creating Kind cluster '${CLUSTER_NAME}' (3 nodes)..."
        "$KIND" create cluster \
            --config "${SCRIPT_DIR}/kind-config.yaml" \
            --name "${CLUSTER_NAME}" \
            --wait 60s
        log "Cluster created"
    fi

    # Point kubectl at the cluster
    export KUBECONFIG="$("$KIND" get kubeconfig-path --name="${CLUSTER_NAME}" 2>/dev/null || echo "${HOME}/.kube/config")"
    "$KIND" export kubeconfig --name "${CLUSTER_NAME}"

    # Step 2: Build the Docker image
    log "Building controller image ${IMAGE_NAME}:${IMAGE_TAG}..."
    "$DOCKER" build -t "${IMAGE_NAME}:${IMAGE_TAG}" "${PROJECT_ROOT}"
    log "Image built"

    # Step 3: Load image into Kind
    load_image

    # Step 4: Install CRDs
    log "Installing CRDs..."
    "$KUBECTL" apply -f "${PROJECT_ROOT}/deploy/helm/wormsign/crds/"
    log "CRDs installed"

    # Step 5: Create namespace
    "$KUBECTL" create namespace "${NAMESPACE}" --dry-run=client -o yaml | "$KUBECTL" apply -f -

    # Step 6: Install Helm chart
    log "Installing Helm chart..."
    "$HELM" upgrade --install "${RELEASE_NAME}" "${PROJECT_ROOT}/deploy/helm/wormsign" \
        --namespace "${NAMESPACE}" \
        --set image.repository="${IMAGE_NAME}" \
        --set image.tag="${IMAGE_TAG}" \
        --set image.pullPolicy=Never \
        --set analyzer.backend=noop \
        --set logging.level=debug \
        --set sinks.kubernetesEvent.enabled=true \
        --set "detectors.jobDeadlineExceeded.enabled=true" \
        --set "detectors.podStuckPending.threshold=1m" \
        --set "detectors.podStuckPending.cooldown=2m" \
        --set "detectors.podCrashLoop.cooldown=2m" \
        --set "detectors.podFailed.cooldown=2m" \
        --wait --timeout 120s
    log "Helm chart installed"

    # Step 7: Wait for controller pod
    log "Waiting for controller pod to be ready..."
    "$KUBECTL" -n "${NAMESPACE}" rollout status deployment "${RELEASE_NAME}-k8s-wormsign-controller" --timeout=90s

    echo ""
    log "=== Wormsign is running! ==="
    echo ""
    "$KUBECTL" -n "${NAMESPACE}" get pods
    echo ""
    log "Next steps:"
    log "  ./hack/local-test.sh inject   — deploy faulty workloads to trigger detectors"
    log "  ./hack/local-test.sh logs     — tail controller logs"
    log "  ./hack/local-test.sh status   — show pod status and events"
    log "  ./hack/local-test.sh down     — tear everything down"
}

cmd_rebuild() {
    check_tools

    if ! cluster_exists; then
        err "Cluster '${CLUSTER_NAME}' does not exist. Run './hack/local-test.sh up' first."
        exit 1
    fi

    "$KIND" export kubeconfig --name "${CLUSTER_NAME}"

    log "Rebuilding controller image..."
    "$DOCKER" build -t "${IMAGE_NAME}:${IMAGE_TAG}" "${PROJECT_ROOT}"

    load_image

    log "Restarting controller deployment..."
    "$KUBECTL" -n "${NAMESPACE}" rollout restart deployment "${RELEASE_NAME}-k8s-wormsign-controller"
    "$KUBECTL" -n "${NAMESPACE}" rollout status deployment "${RELEASE_NAME}-k8s-wormsign-controller" --timeout=90s

    log "Rebuild complete"
}

cmd_inject() {
    check_tools

    if ! cluster_exists; then
        err "Cluster '${CLUSTER_NAME}' does not exist. Run './hack/local-test.sh up' first."
        exit 1
    fi

    "$KIND" export kubeconfig --name "${CLUSTER_NAME}"

    log "Deploying fault-injection workloads..."
    "$KUBECTL" apply -f "${PROJECT_ROOT}/test/fixtures/fault-injection/"

    echo ""
    log "=== Fault-injection workloads deployed ==="
    echo ""
    log "What was deployed:"
    log "  1. crashloop-pod      — exits immediately with code 1 (triggers PodCrashLoop)"
    log "  2. oom-killed-pod     — allocates memory until OOM-killed (triggers PodFailed)"
    log "  3. stuck-pending-pod  — requests 100 CPUs, can never schedule (triggers PodStuckPending)"
    log "  4. failing-job        — job with 5s deadline that always fails (triggers JobDeadlineExceeded)"
    log "  5. bad-image-pod      — references nonexistent image (triggers PodFailed)"
    echo ""
    log "Detectors have cooldowns and thresholds. Give it a few minutes, then check:"
    log "  ./hack/local-test.sh logs"
}

cmd_logs() {
    check_tools

    if ! cluster_exists; then
        err "Cluster '${CLUSTER_NAME}' does not exist."
        exit 1
    fi

    "$KIND" export kubeconfig --name "${CLUSTER_NAME}"

    log "Tailing controller logs (Ctrl+C to stop)..."
    echo ""
    "$KUBECTL" -n "${NAMESPACE}" logs -l app.kubernetes.io/name=k8s-wormsign -f --tail=200
}

cmd_status() {
    check_tools

    if ! cluster_exists; then
        err "Cluster '${CLUSTER_NAME}' does not exist."
        exit 1
    fi

    "$KIND" export kubeconfig --name "${CLUSTER_NAME}"

    echo ""
    log "=== Controller Pods ==="
    "$KUBECTL" -n "${NAMESPACE}" get pods -o wide
    echo ""

    log "=== Controller Deployment ==="
    "$KUBECTL" -n "${NAMESPACE}" get deployment -o wide
    echo ""

    log "=== Fault-Injection Pods (default namespace) ==="
    "$KUBECTL" -n default get pods -o wide 2>/dev/null || true
    echo ""

    log "=== Recent Wormsign Events ==="
    "$KUBECTL" get events --all-namespaces --field-selector reason!=Pulling,reason!=Pulled,reason!=Created,reason!=Started,reason!=Scheduled \
        --sort-by='.lastTimestamp' 2>/dev/null | tail -20 || true
    echo ""

    log "=== Wormsign CRDs ==="
    for crd in wormsigndetectors wormsigngatherers wormsignsinks wormsignpolicies; do
        count=$("$KUBECTL" get "$crd" --all-namespaces --no-headers 2>/dev/null | wc -l)
        echo "  ${crd}: ${count} resources"
    done
}

cmd_down() {
    check_tools

    if ! cluster_exists; then
        warn "Cluster '${CLUSTER_NAME}' does not exist, nothing to do."
        return
    fi

    log "Deleting Kind cluster '${CLUSTER_NAME}'..."
    "$KIND" delete cluster --name "${CLUSTER_NAME}"
    log "Cluster deleted"
}

# --- Main ---
case "${1:-help}" in
    up)      cmd_up ;;
    rebuild) cmd_rebuild ;;
    inject)  cmd_inject ;;
    logs)    cmd_logs ;;
    status)  cmd_status ;;
    down)    cmd_down ;;
    *)
        echo "Usage: $0 {up|rebuild|inject|logs|status|down}"
        echo ""
        echo "Commands:"
        echo "  up        Create Kind cluster, build image, deploy Wormsign"
        echo "  rebuild   Rebuild image and redeploy (for code changes)"
        echo "  inject    Deploy faulty workloads to trigger detectors"
        echo "  logs      Tail controller logs"
        echo "  status    Show controller and test pod status"
        echo "  down      Delete the Kind cluster"
        exit 1
        ;;
esac
