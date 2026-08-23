#!/usr/bin/env bash
# Install k8s-r8r git hooks into .git/hooks:
#   pre-push    — gitleaks secret scan (public repo gate)
#   post-commit — graphify knowledge-graph incremental rebuild (if graphify is installed)
# Usage: make install-hooks   (or: bash hack/install-git-hooks.sh)
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
hooks_dir="$repo_root/.git/hooks"

install -m 0755 "$repo_root/hack/git-hooks/pre-push" "$hooks_dir/pre-push"
echo "installed: pre-push (gitleaks)"

if command -v graphify >/dev/null 2>&1; then
  (cd "$repo_root" && graphify hook install) && echo "installed: post-commit (graphify)"
else
  echo "skipped: graphify post-commit hook (graphify not installed)"
fi

if ! command -v gitleaks >/dev/null 2>&1; then
  echo "WARNING: gitleaks not installed — pre-push hook will refuse pushes until it is (brew install gitleaks)"
fi
