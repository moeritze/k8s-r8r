# Reconciler

> God node · 52 connections · `internal/engine/reconciler.go`

**Community:** [[Engine Reconcile Loop]]

## Connections by Relation

### contains
- [[Reconciler]] `EXTRACTED`
- [[addInventoryRevocations()]] `EXTRACTED`
- [[hasEntry()]] `EXTRACTED`
- [[minDelay()]] `EXTRACTED`
- [[clusterSet()]] `EXTRACTED`
- [[policiesByName()]] `EXTRACTED`
- [[replicaCountsByCluster()]] `EXTRACTED`
- [[slotInfo]] `EXTRACTED`
- [[nsKey]] `EXTRACTED`
- [[ProviderInventory]] `EXTRACTED`
- [[classifyTransportErr()]] `EXTRACTED`

### method
- [[.Reconcile()]] `EXTRACTED`
- [[.applyTarget()]] `EXTRACTED`
- [[.collectGarbage()]] `EXTRACTED`
- [[.applyRevocations()]] `EXTRACTED`
- [[.reconcileDeletion()]] `EXTRACTED`
- [[.deniedState()]] `EXTRACTED`
- [[.effectiveRevocationPolicy()]] `EXTRACTED`
- [[.finishSimple()]] `EXTRACTED`
- [[.flattenTargets()]] `EXTRACTED`
- [[.writeStatusIfChanged()]] `EXTRACTED`
- [[.event()]] `EXTRACTED`
- [[.SetupWithManager()]] `EXTRACTED`
- [[.emitTransitionEvents()]] `EXTRACTED`
- [[.init()]] `EXTRACTED`
- [[.applyWithRecreate()]] `EXTRACTED`
- [[.ensureNamespace()]] `EXTRACTED`
- [[.gvkForEntry()]] `EXTRACTED`
- [[.mapPolicy()]] `EXTRACTED`
- [[.mapSource()]] `EXTRACTED`
- [[.previousResult()]] `EXTRACTED`

### references
- [[Result]] `EXTRACTED`
- [[Options]] `EXTRACTED`
- [[UID]] `EXTRACTED`
- [[backoffTracker]] `EXTRACTED`
- [[ClusterEvents]] `EXTRACTED`
- [[ClusterInventory]] `EXTRACTED`
- [[EventLimiter]] `EXTRACTED`
- [[Client]] `EXTRACTED`
- [[DriftDetector]] `EXTRACTED`
- [[EventRecorder]] `EXTRACTED`
- [[Mutex]] `EXTRACTED`
- [[Once]] `EXTRACTED`
- [[Scheme]] `EXTRACTED`
- [[Renderer]] `EXTRACTED`
- [[Transport]] `EXTRACTED`

---

*Part of the graphify knowledge wiki. See [[index]] to navigate.*