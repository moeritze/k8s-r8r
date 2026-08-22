package cluster

import (
	"context"
	"fmt"
	"sync"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	crcluster "sigs.k8s.io/controller-runtime/pkg/cluster"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// State classifies per-cluster connectivity.
type State string

const (
	// StateReachable: the last probe succeeded.
	StateReachable State = "Reachable"
	// StateDegraded: recent probe failures below the unreachable
	// threshold.
	StateDegraded State = "Degraded"
	// StateUnreachable: consecutive failures reached the threshold.
	StateUnreachable State = "Unreachable"
)

// Connectivity is the externally visible connectivity snapshot for one
// cluster (consumed later by metrics and Replication status).
type Connectivity struct {
	// State is the current connectivity classification.
	State State
	// Since is when the current state was entered. For Unreachable it is
	// the time of the first failure of the ongoing failure streak
	// ("unreachable-since").
	Since time.Time
	// LastError is the message of the most recent probe failure (empty
	// when reachable). Probe errors never contain credentials.
	LastError string
}

// Runtime is the per-cluster runtime handle: a client and a (not yet
// informer-populated) cache bound to the SA-token config. Informer/watch
// setup is deliberately the engine's job; the manager only provides the
// cache for the engine to inject metadata-only informers into.
// *cluster.Cluster from controller-runtime satisfies this interface.
type Runtime interface {
	// Start runs the runtime (cache) until ctx is done.
	Start(ctx context.Context) error
	// GetClient returns the cluster-scoped client.
	GetClient() client.Client
	// GetCache returns the cluster-scoped cache.
	GetCache() cache.Cache
}

// RuntimeFactory builds a Runtime for a cluster from its SA-token config.
type RuntimeFactory func(name string, cfg *rest.Config) (Runtime, error)

// DefaultRuntimeFactory returns a factory producing controller-runtime
// cluster.Cluster runtimes with the given options (scheme etc.).
func DefaultRuntimeFactory(opts ...crcluster.Option) RuntimeFactory {
	return func(name string, cfg *rest.Config) (Runtime, error) {
		c, err := crcluster.New(cfg, opts...)
		if err != nil {
			return nil, fmt.Errorf("cluster: building runtime for %q: %w", name, err)
		}
		return c, nil
	}
}

// ProbeFunc checks reachability of a cluster's API server.
type ProbeFunc func(ctx context.Context, cfg *rest.Config) error

// DefaultProbe hits the API server's version endpoint with a short timeout.
func DefaultProbe(timeout time.Duration) ProbeFunc {
	return func(ctx context.Context, cfg *rest.Config) error {
		cs, err := kubernetes.NewForConfig(cfg)
		if err != nil {
			return err
		}
		pctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		_, err = cs.Discovery().RESTClient().Get().AbsPath("/version").DoRaw(pctx)
		return err
	}
}

// backoffTracker is the pure connectivity state machine: it turns a sequence
// of probe results into (state, next probe delay). Extracted for
// table-driven testing.
type backoffTracker struct {
	baseDelay       time.Duration // first retry delay after a failure
	maxDelay        time.Duration // exponential backoff cap
	healthyInterval time.Duration // probe interval while reachable
	threshold       int           // consecutive failures until Unreachable

	failures int
	delay    time.Duration
	state    State
	since    time.Time
	lastErr  string
}

func newBackoffTracker(base, max, healthy time.Duration, threshold int) *backoffTracker {
	return &backoffTracker{
		baseDelay:       base,
		maxDelay:        max,
		healthyInterval: healthy,
		threshold:       threshold,
	}
}

// Observe records one probe result and returns the new state and the delay
// until the next probe (exponential with cap while failing).
func (t *backoffTracker) Observe(err error, now time.Time) (State, time.Duration) {
	if err == nil {
		if t.state != StateReachable {
			t.since = now
		}
		t.state = StateReachable
		t.failures = 0
		t.delay = 0
		t.lastErr = ""
		return t.state, t.healthyInterval
	}
	t.failures++
	t.lastErr = err.Error()
	if t.failures == 1 {
		t.since = now // start of the failure streak
		t.delay = t.baseDelay
	} else {
		t.delay *= 2
		if t.delay > t.maxDelay {
			t.delay = t.maxDelay
		}
	}
	if t.failures >= t.threshold {
		t.state = StateUnreachable
	} else {
		t.state = StateDegraded
	}
	return t.state, t.delay
}

// Connectivity renders the tracker as an external snapshot.
func (t *backoffTracker) Connectivity() Connectivity {
	return Connectivity{State: t.state, Since: t.since, LastError: t.lastErr}
}

// ManagerOption customizes a Manager.
type ManagerOption func(*Manager)

// WithRuntimeFactory injects the runtime constructor (tests use stubs).
func WithRuntimeFactory(f RuntimeFactory) ManagerOption {
	return func(m *Manager) { m.factory = f }
}

// WithProbe injects the connectivity probe.
func WithProbe(p ProbeFunc) ManagerOption {
	return func(m *Manager) { m.probe = p }
}

// WithBackoff tunes probing: healthy interval, base/max failure backoff, and
// the consecutive-failure threshold for Unreachable.
func WithBackoff(healthy, base, max time.Duration, unreachableAfter int) ManagerOption {
	return func(m *Manager) {
		m.healthyInterval = healthy
		m.baseDelay = base
		m.maxDelay = max
		m.threshold = unreachableAfter
	}
}

// Manager maintains exactly one Runtime per registered ready cluster:
// started on Register, stopped (context cancel + cache drain) on Deregister.
// Every cluster gets its own probe/lifecycle goroutine, so one cluster's
// outage never blocks the others.
type Manager struct {
	factory         RuntimeFactory
	probe           ProbeFunc
	healthyInterval time.Duration
	baseDelay       time.Duration
	maxDelay        time.Duration
	threshold       int

	mu      sync.RWMutex
	rootCtx context.Context //nolint:containedctx // stored so Register can derive per-cluster contexts after Start
	entries map[string]*entry
	onReady []func(name string, rt Runtime)
	onGone  []func(name string)
	wg      sync.WaitGroup
}

// entry is the internal per-cluster bookkeeping.
type entry struct {
	name   string
	cfg    *rest.Config
	cancel context.CancelFunc
	done   chan struct{} // closed when the lifecycle loop has fully exited

	// mu guards the mutable fields below (runtime state + tracker).
	mu          sync.Mutex
	runtime     Runtime
	started     bool
	runtimeDone chan struct{}
	tracker     *backoffTracker
}

// NewManager builds a cluster runtime manager. Without options it uses
// controller-runtime cluster runtimes and a /version probe.
func NewManager(opts ...ManagerOption) *Manager {
	m := &Manager{
		factory:         DefaultRuntimeFactory(),
		probe:           DefaultProbe(5 * time.Second),
		healthyInterval: 30 * time.Second,
		baseDelay:       2 * time.Second,
		maxDelay:        5 * time.Minute,
		threshold:       3,
		entries:         map[string]*entry{},
	}
	for _, o := range opts {
		o(m)
	}
	return m
}

// OnClusterReady registers a hook fired (once per runtime start) when a
// cluster's runtime is up — the engine uses it to wire informers and
// workqueues. Must be set before the cluster registers.
func (m *Manager) OnClusterReady(f func(name string, rt Runtime)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onReady = append(m.onReady, f)
}

// OnClusterGone registers a hook fired after a cluster's runtime has fully
// stopped on Deregister — the engine uses it for inventory release.
func (m *Manager) OnClusterGone(f func(name string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onGone = append(m.onGone, f)
}

// Start makes the manager live and blocks until ctx is done, then stops all
// runtimes and waits for their caches to drain. Clusters registered before
// Start begin their lifecycle when Start runs.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	m.rootCtx = ctx
	pending := make([]*entry, 0, len(m.entries))
	for _, e := range m.entries {
		pending = append(pending, e)
	}
	m.mu.Unlock()
	for _, e := range pending {
		m.launch(ctx, e)
	}
	<-ctx.Done()
	m.wg.Wait()
	return nil
}

// Register adds a cluster with its SA-token rest config and starts its
// lifecycle (probing + runtime start) immediately if the manager is running.
// Registering an existing name with a new config replaces the old runtime.
func (m *Manager) Register(name string, cfg *rest.Config) {
	m.mu.Lock()
	if old, exists := m.entries[name]; exists {
		m.mu.Unlock()
		m.stopEntry(old, true)
		m.mu.Lock()
	}
	e := &entry{
		name:    name,
		cfg:     cfg,
		done:    make(chan struct{}),
		tracker: newBackoffTracker(m.baseDelay, m.maxDelay, m.healthyInterval, m.threshold),
	}
	m.entries[name] = e
	ctx := m.rootCtx
	m.mu.Unlock()
	if ctx != nil {
		m.launch(ctx, e)
	}
}

// Deregister stops a cluster's runtime (cancelling its context and waiting
// for the cache to drain), removes it, and fires OnClusterGone hooks.
func (m *Manager) Deregister(name string) {
	m.mu.Lock()
	e, exists := m.entries[name]
	if !exists {
		m.mu.Unlock()
		return
	}
	delete(m.entries, name)
	hooks := append([]func(string){}, m.onGone...)
	m.mu.Unlock()

	m.stopEntry(e, true)
	for _, h := range hooks {
		h(name)
	}
}

// GetClient returns the client for a cluster whose runtime is started.
func (m *Manager) GetClient(name string) (client.Client, bool) {
	if rt, ok := m.runtimeOf(name); ok {
		return rt.GetClient(), true
	}
	return nil, false
}

// GetCache returns the cache for a cluster whose runtime is started. The
// engine injects metadata-only informers into it.
func (m *Manager) GetCache(name string) (cache.Cache, bool) {
	if rt, ok := m.runtimeOf(name); ok {
		return rt.GetCache(), true
	}
	return nil, false
}

// Snapshot returns the current connectivity state of every registered
// cluster (consumed by metrics and status writers).
func (m *Manager) Snapshot() map[string]Connectivity {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make(map[string]Connectivity, len(m.entries))
	for name, e := range m.entries {
		e.mu.Lock()
		out[name] = e.tracker.Connectivity()
		e.mu.Unlock()
	}
	return out
}

func (m *Manager) runtimeOf(name string) (Runtime, bool) {
	m.mu.RLock()
	e, ok := m.entries[name]
	m.mu.RUnlock()
	if !ok {
		return nil, false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if !e.started || e.runtime == nil {
		return nil, false
	}
	return e.runtime, true
}

// launch starts the per-cluster lifecycle goroutine.
func (m *Manager) launch(parent context.Context, e *entry) {
	ctx, cancel := context.WithCancel(parent)
	e.cancel = cancel
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer close(e.done)
		m.lifecycle(ctx, e)
	}()
}

// lifecycle probes the cluster with backoff, starts the runtime once
// reachable, and keeps updating connectivity state until ctx is cancelled.
func (m *Manager) lifecycle(ctx context.Context, e *entry) {
	logger := log.FromContext(ctx).WithValues("cluster", e.name)
	for {
		err := m.probe(ctx, e.cfg)
		if ctx.Err() != nil {
			return
		}
		e.mu.Lock()
		_, delay := e.tracker.Observe(err, time.Now())
		shouldStart := err == nil && !e.started
		e.mu.Unlock()
		if err != nil {
			logger.V(1).Info("cluster probe failed", "error", err.Error())
		}
		if shouldStart {
			if startErr := m.startRuntime(ctx, e); startErr != nil {
				logger.Error(startErr, "starting cluster runtime")
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
	}
}

// startRuntime constructs and starts the runtime and fires OnClusterReady.
func (m *Manager) startRuntime(ctx context.Context, e *entry) error {
	rt, err := m.factory(e.name, e.cfg)
	if err != nil {
		return err
	}
	runtimeDone := make(chan struct{})
	e.mu.Lock()
	e.runtime = rt
	e.started = true
	e.runtimeDone = runtimeDone
	e.mu.Unlock()

	go func() {
		defer close(runtimeDone)
		if err := rt.Start(ctx); err != nil && ctx.Err() == nil {
			log.FromContext(ctx).WithValues("cluster", e.name).Error(err, "cluster runtime exited")
			// Allow the lifecycle loop to start a fresh runtime on
			// the next successful probe.
			e.mu.Lock()
			e.started = false
			e.runtime = nil
			e.mu.Unlock()
		}
	}()

	m.mu.RLock()
	hooks := append([]func(string, Runtime){}, m.onReady...)
	m.mu.RUnlock()
	for _, h := range hooks {
		h(e.name, rt)
	}
	return nil
}

// stopEntry cancels an entry's context and, when wait is true, blocks until
// its lifecycle loop and runtime (cache drain) have finished.
func (m *Manager) stopEntry(e *entry, wait bool) {
	if e.cancel != nil {
		e.cancel()
	}
	if !wait {
		return
	}
	if e.cancel != nil {
		<-e.done
	}
	e.mu.Lock()
	runtimeDone := e.runtimeDone
	e.mu.Unlock()
	if runtimeDone != nil {
		<-runtimeDone
	}
}
