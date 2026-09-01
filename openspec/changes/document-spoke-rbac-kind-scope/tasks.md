## 1. Verify the claims against the code

- [x] 1.1 Confirm the bootstrap `RBACScope` comes from `--allowed-kinds` and never from a `ReplicationPolicy` (`cmd/main.go:82-112`, `cmd/main.go:312`, `cmd/main.go:456`)
- [x] 1.2 Confirm the granted verbs, the absence of `resourceNames`, and the cluster-wide binding (`internal/cluster/bootstrap.go:52`, `:89-104`, `:193-212`)
- [x] 1.3 Confirm the `managed-by` selector is a cache filter, not an authorization boundary (`cmd/main.go:446-451`)
- [x] 1.4 Confirm the allowlist-shrink re-narrow path exists: full rule replacement (`internal/cluster/bootstrap.go:168`), restart replays bootstrap for every cluster present at startup (`internal/discovery/discovery.go:101`), covered by `TestUpdateRBACReNarrows`
- [x] 1.5 Confirm `DefaultRBACScope` is test-only and not on the production path

## 2. Spec delta

- [x] 2.1 Restate *Minimal-privilege credential bootstrap* to the implemented kind-allowlist scoping
- [x] 2.2 Record policy-derived scoping as deferred, with the reason and issue #29 as tracker
- [x] 2.3 Add the `Configured kind allowlist shrinks` scenario
- [x] 2.4 `openspec validate document-spoke-rbac-kind-scope --strict` passes

## 3. Docs

- [x] 3.1 `docs/security.md` D5 section: state the real verb list and the all-namespaces ClusterRole scope where the promise is made
- [x] 3.2 `docs/security.md`: replace the "bounded and auditable" blast-radius claim with an accurate one that keeps the non-escalation context
- [x] 3.3 `docs/security.md`: add the policy-derivation gap to "What k8s-r8r does not protect against"
- [x] 3.4 `internal/cluster/bootstrap.go`: extend the `RBACScope` doc comment with the policy-derivation limitation (comment only)
- [x] 3.5 `obsidian/security-model.md`: match the corrected wording
- [x] 3.6 `charts/k8s-r8r/templates/NOTES.txt`: state that installing bootstraps credentials on every discovered ready cluster

## 4. Verification

- [x] 4.1 `make lint` green
- [x] 4.2 `make test` green
- [x] 4.3 `helm lint charts/k8s-r8r` green

## 5. Out of scope (tracked in #29)

- [ ] 5.1 Derive the bootstrap scope from the policy universe
- [ ] 5.2 Decide whether the provider admin credential stays one-shot, or the spoke SA narrows its own ClusterRole
- [ ] 5.3 Re-narrow on policy-universe change without a restart
