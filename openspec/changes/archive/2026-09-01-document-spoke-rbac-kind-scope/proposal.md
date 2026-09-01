# Document spoke RBAC kind scope

## Why

`openspec/specs/cluster-discovery/spec.md:28` (requirement *Minimal-privilege
credential bootstrap*) promises spoke RBAC "limited to the verbs, kinds, and
namespace-creation rights **the policy universe** requires" and re-narrowing
"when **the policy universe** shrinks". Neither half is implemented, and the
spec has read that way since the operator was bootstrapped.

What is implemented is scoping to the operator's *configured kind allowlist*:
`parseAllowedKinds` (`cmd/main.go:82-112`) turns the `--allowed-kinds` flag
(`cmd/main.go:312`, default `secrets,configmaps`) into the `cluster.RBACScope`
handed to the spoke wirer (`cmd/main.go:456`). Nothing in the tree reads a
`ReplicationPolicy` to build that scope. So a hub whose only policy permits
`Secret` still grants cluster-wide ConfigMap `delete` on every discovered
spoke, and the grant is created at install time, before any policy exists.

The re-narrowing half is *partially* implemented and undocumented as such:
`UpdateRBAC` replaces the ClusterRole rules wholesale
(`internal/cluster/bootstrap.go:168`), and because `--allowed-kinds` can only
change through a Deployment edit — which restarts the pod, after which the
discovery provider re-emits a Register event for every cluster present at
startup (`internal/discovery/discovery.go:101`) — every spoke re-bootstraps at
the new, narrower scope. `TestUpdateRBACReNarrows` covers the replacement.
This behavior has no scenario.

Closing the real gap (policy-derived scoping) is a 10-14h implementation that
first needs a deliberate decision about whether the provider admin credential
stays one-shot; widening a spoke grant on every policy edit would turn an
auditable bootstrap-class event into a routine one. That decision gets its own
change. This change makes the written contract match the code in the meantime,
so the drift is disclosed rather than implied to be fixed.

## What Changes

- **Spec (`cluster-discovery`)**: amend *Minimal-privilege credential
  bootstrap* to state that the spoke grant is scoped to the configured kind
  allowlist and re-narrowed when that allowlist shrinks; record policy-derived
  scoping and per-namespace narrowing as explicitly deferred, with issue #29 as
  the tracking issue. Add a scenario for the kind-allowlist shrink, which is
  implemented and previously had no scenario.
- **`docs/security.md`**: state the actual verb list and the all-namespaces
  ClusterRole scope at the point where the D5 promise is made; replace the
  "bounded and auditable" blast-radius claim with an accurate one; add the
  policy-derivation gap to "What k8s-r8r does not protect against".
- **`internal/cluster/bootstrap.go`**: extend the `RBACScope` doc comment,
  which today acknowledges only the namespace-wildcard limitation, to also
  record the policy-derivation gap — that comment is where a future implementer
  looks first. Comment only; no behavior change.
- **`obsidian/security-model.md`**: match the corrected blast-radius wording.
- **`charts/k8s-r8r/templates/NOTES.txt`**: say that installing bootstraps
  credentials on every discovered ready cluster, before any policy exists.
  Today the notes say "nothing replicates yet", which is true of replication
  and easy to misread as "nothing has happened on my clusters".

No operator behavior changes. Documentation and spec honesty only; the
implementation remains outstanding under #29.

## Capabilities

### New Capabilities

None.

### Modified Capabilities

- `cluster-discovery` — *Minimal-privilege credential bootstrap* restated to
  the implemented scoping, plus one new scenario.

## Impact

- `openspec/specs/cluster-discovery/spec.md` (via this change's delta)
- `docs/security.md`
- `internal/cluster/bootstrap.go` (doc comment only)
- `obsidian/security-model.md`
- `charts/k8s-r8r/templates/NOTES.txt`
- Tracking issue for the outstanding implementation: #29
