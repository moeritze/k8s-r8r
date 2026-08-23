# Contributing to k8s-r8r

Thanks for your interest! k8s-r8r replicates Kubernetes objects across fleets — annotation-driven, policy-gated. Before contributing, skim the [README](README.md) and the documentation hub at [`obsidian/k8s-r8r.md`](obsidian/k8s-r8r.md).

## Development setup

```bash
git clone https://github.com/moeritze/k8s-r8r && cd k8s-r8r
make install-hooks   # REQUIRED: installs the gitleaks pre-push secret scan
make test            # unit + envtest
make test-e2e        # full e2e on a local kind fleet (needs docker + kind)
```

Toolchain: Go (see `go.mod`), docker, kind, helm, gitleaks. `hack/kind-fleet.sh` manages a local hub + spokes fleet.

## Workflow

1. **Specs first**: behavior changes go through [OpenSpec](openspec/) — propose a change (`openspec new change`), get the spec delta agreed, then implement. Small fixes can skip this; anything touching the replication/policy/discovery contracts cannot.
2. **Tests are the contract**: every spec scenario maps to a test. New behavior needs unit coverage; engine/discovery/transport changes need e2e coverage.
3. **Docs are part of done**: update the affected note in `obsidian/` and the relevant `docs/*.md` in the same PR.
4. **Lint**: `make lint` (builds the repo's custom golangci-lint — CI runs the identical binary).

## Hard rules

- **Never commit sensitive data.** Public repo. The pre-push hook and CI gitleaks job will block you; don't fight them. Docs/tests use obviously-fake placeholders.
- **Secret-safe telemetry**: no payload data in logs/events/conditions/metrics — hashes only. An AST audit test enforces this.
- The admission webhook stays `failurePolicy: Ignore`; policy enforcement stays reconcile-time authoritative (see `docs/security.md`).

## Developer Certificate of Origin (DCO)

This project uses the [Developer Certificate of Origin](https://developercertificate.org/) instead of a CLA. Every commit must carry a `Signed-off-by` trailer certifying you have the right to submit the change under Apache-2.0:

```bash
git commit -s   # adds: Signed-off-by: Your Name <you@example.com>
```

Forgot it? `git commit --amend -s` (single commit) or `git rebase --signoff main` (a branch). The CI `DCO` check blocks PRs containing unsigned commits (merge commits are exempt).

## AI usage disclosure

This project is developed with substantial AI assistance (Claude). We are transparent about it: AI-assisted commits carry a `Co-Authored-By: Claude ...` trailer. Contributors are welcome to use AI tools; you remain fully responsible for what you submit — review it, test it, understand it. Disclose substantial AI assistance the same way (co-author trailer or PR note).

## Pull requests

- Branch from `main`, conventional-commit style messages (`feat:`, `fix:`, `docs:`, …)
- CI must be green (lint, tests, build, e2e, secret scan)
- Keep PRs focused; link the OpenSpec change if one exists

## Reporting issues

Use the issue templates. For security vulnerabilities, see [SECURITY.md](SECURITY.md) — do **not** open a public issue.
