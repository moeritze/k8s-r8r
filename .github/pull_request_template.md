## Summary

<!-- What does this PR do and why? Link the OpenSpec change if one exists. -->

## Checklist

- [ ] **Spec**: behavior change went through [OpenSpec](../openspec/) (spec delta agreed before implementation), or this is a small fix that doesn't touch the replication/policy/discovery contracts
- [ ] **Tests**: new behavior has unit coverage; engine/discovery/transport changes have e2e coverage
- [ ] **Docs**: affected `obsidian/` note and relevant `docs/*.md` updated in this PR
- [ ] **Lint & tests green**: `make lint && make test` pass locally
- [ ] **No sensitive data**: nothing secret in code, tests, docs, or fixtures — placeholders are obviously fake; no payload data in logs/events/conditions/metrics (hashes only)
- [ ] **AI disclosure**: substantial AI assistance is disclosed (`Co-Authored-By` trailer or note below), per [CONTRIBUTING.md](../CONTRIBUTING.md#ai-usage-disclosure)
