# Security model and threat model

k8s-r8r moves Secrets across clusters — the security posture is the
product's core, not an add-on. This document records the threat model and
the reasoning behind the architecture decisions that shape it (design
decisions D2, D4, D5, D6 in the project design).

> **Alpha notice:** the mechanisms below are implemented and unit/envtest
> covered, but the project is v0/alpha and the cross-cluster path has not
> yet been exercised end-to-end in CI. Review accordingly.

## Trust boundaries at a glance

```
   developers ──(annotations; no new privileges)──▶ hub API server
   admins ─────(ReplicationPolicy: cluster-scoped)──▶ hub API server
                                │
                     k8s-r8r operator (hub)
                     - reads allowlisted kinds
                     - reads CAPI kubeconfig Secrets (bootstrap only)
                                │  short-lived SA tokens, narrow RBAC
                     ┌──────────┼──────────┐
                     ▼          ▼          ▼
                  spoke A    spoke B    spoke C
```

## Default deny

With no `ReplicationPolicy` objects present, **nothing replicates**.
Policies are allowlist-only (no deny rules), cluster-scoped, and combine
by union; a target is permitted only when a single policy matches every
dimension. Policy is re-evaluated on every reconcile, so revoking a
policy acts on existing replicas (default: delete them). Crucially,
replication grants developers **zero new privileges**: the operator only
fans out objects the requester could already write, to destinations
policy allows. See [policies.md](policies.md).

## Why push (v1), and what it costs — design D2

v1 pushes replicas from the hub to spokes rather than running pull agents
on each spoke. Reasoning: ClusterAPI already hands the hub discovery and
credentials, and push is dramatically simpler than the alternative —
shipping and upgrading a per-cluster agent, exposing a hub API for it,
and running a registration protocol, each of which is attack surface of
its own. The security cost is explicit: **the hub can write objects
fleet-wide**, making it a high-value target. That cost is mitigated by
credential minimization (next section) and bounded by policy scope.
Pull-agent transport remains a planned future `Transport` implementation
for environments where hub-initiated egress or hub-held credentials are
unacceptable.

## Hub blast radius and narrow-SA bootstrap — design D5

CAPI kubeconfigs are cluster-admin. Using them for steady-state traffic
would make the hub a fleet-admin credential store — hub compromise would
equal fleet-admin compromise. Instead:

1. On cluster registration, the operator reads the provider's admin
   kubeconfig (`<cluster>-kubeconfig` Secret) with an **uncached read**
   and uses it **exactly once per bootstrap** to create on the spoke:
   the `k8s-r8r-system` namespace, the `k8s-r8r` ServiceAccount, the
   `k8s-r8r-replicator` ClusterRole (verbs on exactly the allowlisted
   resource kinds, plus `get`/`create` on namespaces — never delete),
   its binding, and a namespaced Role that lets the SA mint its own
   tokens.
2. All steady-state traffic authenticates with **short-lived, rotated SA
   tokens**; the spoke rest config carries no static credential at all
   (every request is re-signed with a currently valid token). Token
   renewal authenticates as the SA itself, not the admin credential.
3. Spoke RBAC is **re-narrowed** when the configured kind allowlist
   shrinks.

Result: hub compromise blast radius drops from "fleet admin" to "write
allowlisted kinds in spoke namespaces" — still serious, but bounded and
auditable. Known v1 limitation (documented in the code): the spoke grant
is a ClusterRole, so it applies in all namespaces of the spoke;
per-namespace narrowing is a planned refinement.

Hub-side hardening that complements this: keep CAPI kubeconfig Secrets in
an isolated namespace with tight RBAC — they are the crown jewels of a
CAPI hub with or without k8s-r8r.

## Advisory webhook doctrine — design D6

The validating admission webhook exists purely for apply-time UX. Its
configuration is `failurePolicy: Ignore`, `timeoutSeconds: 2`, and CEL
`matchConditions` that scope it to objects carrying an `r8r.io/`
annotation. The design reasoning, verbatim:

> `failurePolicy: Fail` would make operator downtime block all secret
> writes cluster-wide — unacceptable. Therefore the webhook is strictly
> UX (apply-time error messages), scoped via CEL matchConditions to
> annotated objects only, and its absence can never cause unauthorized
> replication because the controller re-checks policy on every
> reconcile.

Consequences:

- The webhook is **never an availability dependency**: ordinary
  Secret/ConfigMap traffic never even reaches it, and annotated writes
  proceed if it is down.
- The webhook is **never a security dependency**: bypassing it (or
  disabling it entirely — it is optional in the Helm chart) only delays
  feedback to reconcile time. Internal webhook failures deliberately
  fail *open*.
- The Helm chart intentionally exposes **no value** to switch the policy
  to `Fail`.
- The webhook decodes object *metadata only* — payload data never enters
  the admission path.

## Secret-safe telemetry

No log line, event, metric, condition message, or error string contains
secret payload data. All content comparison in user-visible output uses
the `sha256:<hex>` source hash. Supporting mechanics:

- Drift detection uses **metadata-only** informers on spokes — replica
  payloads are never cached on the hub.
- Bootstrap errors reference credential Secrets **by name only**;
  kubeconfig bytes and tokens never appear in logs or errors.
- Metric labels are bounded (cluster, namespace, kind — never
  unbounded object names).

## RBAC personas

The chart ships three separated privilege sets:

| Role | Who | Scope |
|---|---|---|
| operator ClusterRole | the operator SA | read/write allowlisted kinds, read `ReplicationPolicy`, own `Replication` objects, read CAPI `Cluster` objects |
| `k8s-r8r-policy-admin` | cluster admins (bind deliberately — the chart never binds it) | full CRUD on `ReplicationPolicy` |
| `k8s-r8r-replication-viewer` | everyone with `view` (aggregated) | read-only `Replication` |

Users never write `Replication` objects; hand-authored ones are marked
`NotAuthoritative` and ignored. The operator's read access to hub Secrets
is required by its function (it must read what it replicates) — scope
what it can *do* with them via `ReplicationPolicy`.

## Minimum supported Kubernetes version

**Kubernetes 1.30.** The webhook configuration uses CEL
`matchConditions` (see `charts/k8s-r8r/templates/webhook.yaml` and
`config/webhook/manifests.yaml`), which are beta (on by default) since
1.28 and GA since 1.30; the project declares 1.30 as the supported floor
and the Helm chart pins `kubeVersion: ">=1.30.0-0"`. The short-lived
TokenRequest API used for spoke tokens is comfortably older. On 1.28/1.29
the manifests may work but are unsupported.

## Conflict handling as a security control — design D7

`Overwrite` can replace a victim cluster's existing secret, so it
requires both the request and a policy to permit it (default is `Fail`).
`Adopt` only takes ownership when content hashes already match. Automatic
renaming does not exist: a silent rename would break workloads that mount
by name with no error surfaced anywhere.

## What k8s-r8r does not protect against

- A compromised hub API server or hub cluster admin — the hub is the
  root of trust.
- Malicious `ReplicationPolicy` authors — policy admin is a cluster-admin
  grade privilege; bind `k8s-r8r-policy-admin` accordingly.
- Readers of replicas on target clusters — replicas are ordinary
  Secrets/ConfigMaps there; spoke-side RBAC governs who reads them.
