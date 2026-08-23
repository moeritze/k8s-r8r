# k8s-r8r — project instructions

Public repository. Kubernetes operator for policy-gated cross-cluster object replication. Entry points: `README.md`, `obsidian/k8s-r8r.md` (documentation hub), `openspec/` (behavior specs).

## Workflow contract (every change)

1. **Spec first**: behavior changes go through OpenSpec (`openspec new change`, `/opsx:*` flow). Specs in `openspec/specs` are the source of truth; keep them in sync with implementation.
2. **Docs are part of done**: after any functional change, update the affected note(s) in `obsidian/` (curated vault — one note per functionality, `[[wikilinks]]`, hub `obsidian/k8s-r8r.md`) and the relevant `docs/*.md` in the same session. Never leave doc updates as follow-ups.
3. **Knowledge graph**: regenerate incrementally after substantive changes — `/graphify --update`, then `graphify export obsidian --dir obsidian/graph`. Code-only changes are covered by the post-commit hook.
4. **Tests before push**: `make lint && make test` minimum; e2e (`make test-e2e`, needs docker) for engine/discovery/transport changes.

## Hard rules

- **No sensitive data ever** — real credentials, kubeconfigs, tokens, personal data. Pre-push gitleaks hook (`make install-hooks`) and CI enforce this; docs/tests use obviously-fake placeholders.
- **Secret-safe telemetry**: no payload data in logs/events/conditions/metrics — hashes only. The AST audit test enforces it; don't fight the ratchet.
- **Commit trailer**: end commits with `Co-Authored-By: Claude <model name> <noreply@anthropic.com>` — deliberate AI-transparency policy for this repo.
- Webhook stays `failurePolicy: Ignore`; policy enforcement stays reconcile-time authoritative (design D6).
- `config/webhook/manifests.yaml` is hand-maintained (controller-gen can't express matchConditions) — keep in sync with `internal/webhook`.

## graphify

This project has a knowledge graph at graphify-out/ with god nodes, community structure, and cross-file relationships.

Rules:
- For codebase questions, first run `graphify query "<question>"` when graphify-out/graph.json exists. Use `graphify path "<A>" "<B>"` for relationships and `graphify explain "<concept>"` for focused concepts. These return a scoped subgraph, usually much smaller than GRAPH_REPORT.md or raw grep output.
- If graphify-out/wiki/index.md exists, use it for broad navigation instead of raw source browsing.
- Read graphify-out/GRAPH_REPORT.md only for broad architecture review or when query/path/explain do not surface enough context.
- After modifying code, run `graphify update .` to keep the graph current (AST-only, no API cost).
