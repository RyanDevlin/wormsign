#!/usr/bin/env bash
set -euo pipefail

# init.sh — Install project-specific dependencies for K8s Wormsign
# Target environment: Ubuntu 24.04 with git, jq, curl, Node.js 22 pre-installed

echo "=== Installing Go 1.22+ ==="
if command -v go &>/dev/null && go version | grep -qE 'go1\.(2[2-9]|[3-9][0-9])'; then
    echo "Go $(go version | awk '{print $3}') already installed, skipping."
else
    GO_VERSION="1.22.10"
    GO_ARCHIVE="go${GO_VERSION}.linux-$(dpkg --print-architecture).tar.gz"
    curl -fsSL "https://go.dev/dl/${GO_ARCHIVE}" -o "/tmp/${GO_ARCHIVE}"
    rm -rf /usr/local/go
    tar -C /usr/local -xzf "/tmp/${GO_ARCHIVE}"
    rm -f "/tmp/${GO_ARCHIVE}"
    # Ensure Go is on PATH for this script and future shells
    export PATH="/usr/local/go/bin:${PATH}"
    if ! grep -q '/usr/local/go/bin' /etc/profile.d/go.sh 2>/dev/null; then
        echo 'export PATH="/usr/local/go/bin:${HOME}/go/bin:${PATH}"' > /etc/profile.d/go.sh
    fi
    echo "Go $(go version | awk '{print $3}') installed."
fi

# Ensure GOPATH/bin is on PATH for tools installed via go install
export PATH="${HOME}/go/bin:/usr/local/go/bin:${PATH}"

echo "=== Installing controller-gen (sigs.k8s.io/controller-tools) ==="
if command -v controller-gen &>/dev/null; then
    echo "controller-gen already installed, skipping."
else
    go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.14.0
    echo "controller-gen installed."
fi

echo "=== Installing Helm 3.12+ ==="
if command -v helm &>/dev/null && helm version --short | grep -qE 'v3\.(1[2-9]|[2-9][0-9])'; then
    echo "Helm $(helm version --short) already installed, skipping."
else
    curl -fsSL https://raw.githubusercontent.com/helm/helm/main/scripts/get-helm-3 | bash
    echo "Helm $(helm version --short) installed."
fi

echo "=== Installing golangci-lint ==="
if command -v golangci-lint &>/dev/null; then
    echo "golangci-lint already installed, skipping."
else
    curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b "$(go env GOPATH)/bin" v1.57.2
    echo "golangci-lint installed."
fi

echo "=== Installing envtest binaries (kube-apiserver, etcd) ==="
if [ -d "${HOME}/.local/share/kubebuilder-envtest" ] || command -v setup-envtest &>/dev/null; then
    echo "setup-envtest may already be available, ensuring latest."
fi
go install sigs.k8s.io/controller-runtime/tools/setup-envtest@latest
setup-envtest use 1.30.x --bin-dir /usr/local/kubebuilder/bin || true
if [ -d "/usr/local/kubebuilder/bin" ]; then
    echo 'export KUBEBUILDER_ASSETS="/usr/local/kubebuilder/bin"' > /etc/profile.d/kubebuilder.sh
    export KUBEBUILDER_ASSETS="/usr/local/kubebuilder/bin"
    echo "envtest binaries installed to ${KUBEBUILDER_ASSETS}."
else
    echo "Warning: envtest binaries may not have installed correctly. Integration tests may need manual setup."
fi

echo "=== All dependencies installed ==="
echo "Go:             $(go version)"
echo "controller-gen: $(controller-gen --version 2>/dev/null || echo 'not found')"
echo "Helm:           $(helm version --short 2>/dev/null || echo 'not found')"
echo "golangci-lint:  $(golangci-lint --version 2>/dev/null || echo 'not found')"
