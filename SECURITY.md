# Security Policy

k8s-r8r handles Secrets across cluster fleets — security reports are taken seriously.

## Reporting a vulnerability

**Do not open a public issue.** Use [GitHub private vulnerability reporting](https://github.com/moeritze/k8s-r8r/security/advisories/new) (Security → Report a vulnerability).

You can expect an acknowledgment within a few days. Please include: affected version/commit, reproduction steps, and impact assessment if you have one.

## Scope of interest

Particularly relevant given the threat model (see [docs/security.md](docs/security.md)):

- Policy bypass: any way a replication request reaches a destination no `ReplicationPolicy` allows
- Privilege escalation via the operator (hub or spoke ServiceAccounts)
- Secret data leaking into logs, events, conditions, or metrics
- Webhook-related bypasses that violate the documented advisory-only doctrine
- Replica takeover of objects k8s-r8r does not manage (conflict-handling bypasses)

## Supported versions

Pre-1.0: only the latest release / `main` receives fixes.
