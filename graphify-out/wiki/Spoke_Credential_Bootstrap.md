# Spoke Credential Bootstrap

> 29 nodes

## Key Concepts

- **Bootstrapper** (11 connections) — `internal/cluster/bootstrap.go`
- **.Bootstrap()** (9 connections) — `internal/cluster/bootstrap.go`
- **NewBootstrapperFromClient()** (8 connections) — `internal/cluster/bootstrap.go`
- **managedMeta()** (8 connections) — `internal/cluster/bootstrap.go`
- **bootstrap.go** (7 connections) — `internal/cluster/bootstrap.go`
- **DefaultRBACScope()** (7 connections) — `internal/cluster/bootstrap.go`
- **Context** (7 connections) — `internal/cluster/bootstrap.go`
- **RBACScope** (6 connections) — `internal/cluster/bootstrap.go`
- **.UpdateRBAC()** (6 connections) — `internal/cluster/bootstrap.go`
- **bootstrap_test.go** (6 connections) — `internal/cluster/bootstrap_test.go`
- **T** (6 connections) — `internal/cluster/bootstrap_test.go`
- **NewBootstrapper()** (4 connections) — `internal/cluster/bootstrap.go`
- **.ensureNamespace()** (4 connections) — `internal/cluster/bootstrap.go`
- **.ensureServiceAccount()** (4 connections) — `internal/cluster/bootstrap.go`
- **.ensureClusterRoleBinding()** (4 connections) — `internal/cluster/bootstrap.go`
- **.ensureTokenRole()** (4 connections) — `internal/cluster/bootstrap.go`
- **.ensureTokenRoleBinding()** (4 connections) — `internal/cluster/bootstrap.go`
- **TestBootstrapCreatesAllObjects()** (4 connections) — `internal/cluster/bootstrap_test.go`
- **TestBootstrapIsIdempotent()** (4 connections) — `internal/cluster/bootstrap_test.go`
- **TestUpdateRBACReNarrows()** (4 connections) — `internal/cluster/bootstrap_test.go`
- **TestUpdateRBACCreatesWhenMissing()** (4 connections) — `internal/cluster/bootstrap_test.go`
- **.Rules()** (3 connections) — `internal/cluster/bootstrap.go`
- **TestRBACScopeRules()** (3 connections) — `internal/cluster/bootstrap_test.go`
- **ScopedResource** (2 connections) — `internal/cluster/bootstrap.go`
- **Interface** (2 connections) — `internal/cluster/bootstrap.go`
- *... and 4 more nodes in this community*

## Relationships

- No strong cross-community connections detected

## Source Files

- `internal/cluster/bootstrap.go`
- `internal/cluster/bootstrap_test.go`

## Audit Trail

- EXTRACTED: 118 (87%)
- INFERRED: 18 (13%)
- AMBIGUOUS: 0 (0%)

---

*Part of the graphify knowledge wiki. See [[index]] to navigate.*