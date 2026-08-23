#!/usr/bin/env bash
#
# kind-fleet.sh - local kind fleet (hub + N spokes) for k8s-r8r development.
#
# Usage:
#   hack/kind-fleet.sh up            # create r8r-hub + r8r-spoke-1..N (default N=1)
#   hack/kind-fleet.sh down          # delete all fleet clusters
#   hack/kind-fleet.sh kubeconfigs   # export per-cluster kubeconfigs to ./bin/kubeconfigs/
#
# Environment:
#   K8S_R8R_SPOKES   number of spoke clusters (default: 1)
#   KIND             kind binary to use (default: kind)
#
# All commands are idempotent: existing clusters are left untouched by `up`,
# missing clusters are skipped by `down`.

set -euo pipefail

KIND="${KIND:-kind}"
SPOKES="${K8S_R8R_SPOKES:-1}"
HUB_CLUSTER="r8r-hub"
SPOKE_PREFIX="r8r-spoke"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/.." && pwd)"
KUBECONFIG_DIR="${REPO_ROOT}/bin/kubeconfigs"

usage() {
  sed -n '2,15p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
  exit 1
}

require_kind() {
  if ! command -v "${KIND}" >/dev/null 2>&1; then
    echo "error: kind binary '${KIND}' not found. Install kind: https://kind.sigs.k8s.io" >&2
    exit 1
  fi
}

validate_spokes() {
  if ! [[ "${SPOKES}" =~ ^[0-9]+$ ]]; then
    echo "error: K8S_R8R_SPOKES must be a non-negative integer, got '${SPOKES}'" >&2
    exit 1
  fi
}

fleet_clusters() {
  echo "${HUB_CLUSTER}"
  local i
  for ((i = 1; i <= SPOKES; i++)); do
    echo "${SPOKE_PREFIX}-${i}"
  done
}

cluster_exists() {
  "${KIND}" get clusters 2>/dev/null | grep -Fxq "$1"
}

cmd_up() {
  local cluster
  while IFS= read -r cluster; do
    if cluster_exists "${cluster}"; then
      echo "cluster '${cluster}' already exists, skipping"
    else
      echo "creating cluster '${cluster}'..."
      "${KIND}" create cluster --name "${cluster}" --wait 120s
    fi
  done < <(fleet_clusters)
  echo "fleet up: ${HUB_CLUSTER} + ${SPOKES} spoke(s)"
}

cmd_down() {
  # Delete every existing cluster matching the fleet naming scheme,
  # regardless of the current K8S_R8R_SPOKES value.
  local cluster
  local found=0
  while IFS= read -r cluster; do
    case "${cluster}" in
      "${HUB_CLUSTER}" | "${SPOKE_PREFIX}"-*)
        found=1
        echo "deleting cluster '${cluster}'..."
        "${KIND}" delete cluster --name "${cluster}"
        ;;
    esac
  done < <("${KIND}" get clusters 2>/dev/null || true)
  if [[ "${found}" -eq 0 ]]; then
    echo "no fleet clusters found, nothing to delete"
  fi
}

cmd_kubeconfigs() {
  mkdir -p "${KUBECONFIG_DIR}"
  local cluster
  while IFS= read -r cluster; do
    if cluster_exists "${cluster}"; then
      "${KIND}" get kubeconfig --name "${cluster}" >"${KUBECONFIG_DIR}/${cluster}.kubeconfig"
      echo "wrote ${KUBECONFIG_DIR}/${cluster}.kubeconfig"
    else
      echo "cluster '${cluster}' does not exist, skipping (run 'up' first)" >&2
    fi
  done < <(fleet_clusters)
}

main() {
  [[ $# -eq 1 ]] || usage
  require_kind
  validate_spokes
  case "$1" in
    up) cmd_up ;;
    down) cmd_down ;;
    kubeconfigs) cmd_kubeconfigs ;;
    *) usage ;;
  esac
}

main "$@"
