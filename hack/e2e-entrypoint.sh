#!/usr/bin/env bash
#
# e2e-entrypoint.sh — Start Docker daemon, then run e2e tests.
#
# Used as the entrypoint for Dockerfile.e2e. Starts dockerd in the background
# (required for Kind to create cluster nodes), waits for it to be ready, then
# runs the Go e2e test suite.
#
# Extra arguments are passed through to `go test`, e.g.:
#   docker run --rm --privileged wormsign-e2e-runner -run TestE2E_SingleReplica
#
set -eo pipefail

# Raise inotify limits — Kind nodes each run containerd + kubelet + systemd,
# all of which create inotify watches. The default max_user_instances (128)
# is too low for 3 nested nodes inside DinD.
sysctl -w fs.inotify.max_user_watches=1048576 >/dev/null 2>&1 || true
sysctl -w fs.inotify.max_user_instances=8192 >/dev/null 2>&1 || true

echo "=== Starting Docker daemon ==="
# Redirect dockerd output to a log file to avoid interleaving container
# lifecycle messages (shim disconnect, restart cancel, etc.) with test output.
dockerd-entrypoint.sh dockerd > /var/log/dockerd.log 2>&1 &
DOCKERD_PID=$!

# Wait for Docker daemon to be ready (up to 30s).
echo "Waiting for Docker daemon..."
for i in $(seq 1 30); do
    if docker info >/dev/null 2>&1; then
        echo "Docker daemon ready"
        break
    fi
    sleep 1
done

if ! docker info >/dev/null 2>&1; then
    echo "ERROR: Docker daemon failed to start within 30s"
    echo "--- dockerd log ---"
    tail -30 /var/log/dockerd.log 2>/dev/null || true
    exit 1
fi

# --- Pre-flight diagnostics ---
echo ""
echo "=== Environment Diagnostics ==="
echo "--- Memory ---"
free -h 2>/dev/null || head -5 /proc/meminfo
echo ""
echo "--- Inotify ---"
echo "max_user_instances: $(cat /proc/sys/fs/inotify/max_user_instances)"
echo "max_user_watches:   $(cat /proc/sys/fs/inotify/max_user_watches)"
echo ""
echo "--- Cgroups ---"
if [ -f /sys/fs/cgroup/cgroup.controllers ]; then
    echo "cgroup version: v2"
    echo "controllers: $(cat /sys/fs/cgroup/cgroup.controllers)"
else
    echo "cgroup version: v1"
fi
mount | grep cgroup | head -5 || true
echo ""
echo "--- Docker Info ---"
docker info --format '{{.Driver}} | cgroup={{.CgroupDriver}} | cgroupv={{.CgroupVersion}} | runtime={{.DefaultRuntime}}'
echo ""
echo "--- Kernel ---"
uname -r
echo ""

echo "=== Running e2e tests ==="
TEST_LOG="/tmp/test-output.log"

# Run tests, showing output in real time while also saving to file.
go test -tags e2e -v -timeout 45m ./test/e2e/ "$@" 2>&1 | tee "$TEST_LOG"
exit_code=${PIPESTATUS[0]}

# --- Test summary ---
echo ""
echo "========================================"
echo "  E2E Test Summary"
echo "========================================"

# Extract individual test results.
if grep -qE '^\s*--- (PASS|FAIL|SKIP):' "$TEST_LOG"; then
    # Print FAIL results first (most important), then PASS, then SKIP.
    # Each grep needs || true because pipefail + set -e would kill the
    # script when grep finds no matches (exit 1).
    grep -E '^\s*--- FAIL:' "$TEST_LOG" | while IFS= read -r line; do
        echo "  FAIL  ${line#*FAIL: }"
    done || true
    grep -E '^\s*--- PASS:' "$TEST_LOG" | while IFS= read -r line; do
        echo "  PASS  ${line#*PASS: }"
    done || true
    grep -E '^\s*--- SKIP:' "$TEST_LOG" | while IFS= read -r line; do
        echo "  SKIP  ${line#*SKIP: }"
    done || true
fi

passed=$(grep -cE '^\s*--- PASS:' "$TEST_LOG" 2>/dev/null) || passed=0
failed=$(grep -cE '^\s*--- FAIL:' "$TEST_LOG" 2>/dev/null) || failed=0
skipped=$(grep -cE '^\s*--- SKIP:' "$TEST_LOG" 2>/dev/null) || skipped=0
total=$((passed + failed + skipped))

echo "----------------------------------------"
echo "  Total: $total  Passed: $passed  Failed: $failed  Skipped: $skipped"
echo "========================================"
echo ""

exit $exit_code
