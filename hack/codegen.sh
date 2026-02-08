#!/usr/bin/env bash

# Copyright 2026 The K8s Wormsign Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# codegen.sh generates deepcopy methods and CRD manifests using controller-gen.
# Usage: ./hack/codegen.sh
#
# This script is idempotent and safe to re-run at any time.

set -o errexit
set -o nounset
set -o pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "${SCRIPT_DIR}/.." && pwd)"

# controller-gen version to install if not found
CONTROLLER_GEN_VERSION="${CONTROLLER_GEN_VERSION:-v0.14.0}"

# Output directories
CRD_OUTPUT_DIR="${ROOT_DIR}/deploy/helm/wormsign/crds"

# Ensure controller-gen is available
if ! command -v controller-gen &>/dev/null; then
    echo "controller-gen not found, installing ${CONTROLLER_GEN_VERSION}..."
    go install "sigs.k8s.io/controller-tools/cmd/controller-gen@${CONTROLLER_GEN_VERSION}"
fi

CONTROLLER_GEN="$(command -v controller-gen)"
echo "Using controller-gen: ${CONTROLLER_GEN}"
echo "controller-gen version: $(${CONTROLLER_GEN} --version)"

# Generate deepcopy methods
echo ""
echo "==> Generating deepcopy methods..."
${CONTROLLER_GEN} \
    object:headerFile="${ROOT_DIR}/hack/boilerplate.go.txt" \
    paths="${ROOT_DIR}/api/v1alpha1/..."

echo "    Generated: api/v1alpha1/zz_generated.deepcopy.go"

# Create CRD output directory if it doesn't exist
mkdir -p "${CRD_OUTPUT_DIR}"

# Generate CRD manifests
echo ""
echo "==> Generating CRD manifests..."
${CONTROLLER_GEN} \
    crd:crdVersions=v1 \
    paths="${ROOT_DIR}/api/v1alpha1/..." \
    output:crd:dir="${CRD_OUTPUT_DIR}"

echo "    Generated CRDs in: deploy/helm/wormsign/crds/"

# List generated CRD files
echo ""
echo "==> Generated files:"
for f in "${CRD_OUTPUT_DIR}"/*.yaml; do
    if [ -f "$f" ]; then
        echo "    $(basename "$f")"
    fi
done

echo ""
echo "Code generation complete."
