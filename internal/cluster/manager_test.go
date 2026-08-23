package cluster

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

func TestBackoffTrackerTransitions(t *testing.T) {
	base := 100 * time.Millisecond
	max := 400 * time.Millisecond
	healthy := time.Second
	boom := errors.New("boom")
	t0 := time.Unix(1000, 0)

	type step struct {
		err       error
		at        time.Time
		wantState State
		wantDelay time.Duration
		wantSince time.Time
	}
	tests := []struct {
		name  string
		steps []step
	}{
		{
			name: "success is reachable",
			steps: []step{
				{err: nil, at: t0, wantState: StateReachable, wantDelay: healthy, wantSince: t0},
			},
		},
		{
			name: "single failure degrades",
			steps: []step{
				{err: nil, at: t0, wantState: StateReachable, wantDelay: healthy, wantSince: t0},
				{err: boom, at: t0.Add(1 * time.Second), wantState: StateDegraded, wantDelay: base, wantSince: t0.Add(1 * time.Second)},
			},
		},
		{
			name: "threshold failures become unreachable, since preserved, delay doubles and caps",
			steps: []step{
				{err: boom, at: t0, wantState: StateDegraded, wantDelay: base, wantSince: t0},
				{err: boom, at: t0.Add(time.Second), wantState: StateDegraded, wantDelay: 2 * base, wantSince: t0},
				{err: boom, at: t0.Add(2 * time.Second), wantState: StateUnreachable, wantDelay: 4 * base, wantSince: t0},
				{err: boom, at: t0.Add(3 * time.Second), wantState: StateUnreachable, wantDelay: max, wantSince: t0},
				{err: boom, at: t0.Add(4 * time.Second), wantState: StateUnreachable, wantDelay: max, wantSince: t0},
			},
		},
		{
			name: "recovery resets to reachable and base delay",
			steps: []step{
				{err: boom, at: t0, wantState: StateDegraded, wantDelay: base, wantSince: t0},
				{err: boom, at: t0.Add(time.Second), wantState: StateDegraded, wantDelay: 2 * base, wantSince: t0},
				{err: nil, at: t0.Add(2 * time.Second), wantState: StateReachable, wantDelay: healthy, wantSince: t0.Add(2 * time.Second)},
				{err: boom, at: t0.Add(3 * time.Second), wantState: StateDegraded, wantDelay: base, wantSince: t0.Add(3 * time.Second)},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tr := newBackoffTracker(base, max, healthy, 3)
			for i, s := range tc.steps {
				state, delay := tr.Observe(s.err, s.at)
				if state != s.wantState {
					t.Fatalf("step %d: state = %v, want %v", i, state, s.wantState)
				}
				if delay != s.wantDelay {
					t.Fatalf("step %d: delay = %v, want %v", i, delay, s.wantDelay)
				}
				conn := tr.Connectivity()
				if !conn.Since.Equal(s.wantSince) {
					t.Fatalf("step %d: since = %v, want %v", i, conn.Since, s.wantSince)
				}
				if s.err != nil && conn.LastError == "" {
					t.Fatalf("step %d: LastError empty after failure", i)
				}
				if s.err == nil && conn.LastError != "" {
					t.Fatalf("step %d: LastError = %q after success", i, conn.LastError)
				}
			}
		})
	}
}

// stubRuntime is a Runtime whose Start blocks until context cancel.
type stubRuntime struct {
	started atomic.Bool
	stopped atomic.Bool
}

func (s *stubRuntime) Start(ctx context.Context) error {
	s.started.Store(true)
	<-ctx.Done()
	s.stopped.Store(true)
	return nil
}
func (s *stubRuntime) GetClient() client.Client { return nil }
func (s *stubRuntime) GetCache() cache.Cache    { return nil }

// probeSwitch is a ProbeFunc whose result is flippable per cluster.
type probeSwitch struct {
	mu   sync.Mutex
	fail map[string]bool // keyed by cfg.Host
}

func (p *probeSwitch) set(host string, failing bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail == nil {
		p.fail = map[string]bool{}
	}
	p.fail[host] = failing
}

func (p *probeSwitch) probe(_ context.Context, cfg *rest.Config) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.fail[cfg.Host] {
		return errors.New("probe failed: " + cfg.Host)
	}
	return nil
}

func newTestManager(probe ProbeFunc, runtimes map[string]*stubRuntime, mu *sync.Mutex) *Manager {
	return NewManager(
		WithProbe(probe),
		WithRuntimeFactory(func(name string, _ *rest.Config) (Runtime, error) {
			rt := &stubRuntime{}
			mu.Lock()
			runtimes[name] = rt
			mu.Unlock()
			return rt, nil
		}),
		WithBackoff(20*time.Millisecond, 10*time.Millisecond, 40*time.Millisecond, 3),
	)
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestManagerLifecycle(t *testing.T) {
	probes := &probeSwitch{}
	runtimes := map[string]*stubRuntime{}
	var mu sync.Mutex
	m := newTestManager(probes.probe, runtimes, &mu)

	var readyNames []string
	var goneNames []string
	var hookMu sync.Mutex
	m.OnClusterReady(func(name string, rt Runtime) {
		hookMu.Lock()
		defer hookMu.Unlock()
		readyNames = append(readyNames, name)
	})
	m.OnClusterGone(func(name string) {
		hookMu.Lock()
		defer hookMu.Unlock()
		goneNames = append(goneNames, name)
	})

	// Register before Start: lifecycle begins when Start runs.
	m.Register("alpha", &rest.Config{Host: "alpha"})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = m.Start(ctx) }()
	defer func() { cancel(); <-done }()

	waitFor(t, "alpha runtime started", func() bool {
		mu.Lock()
		defer mu.Unlock()
		rt := runtimes["alpha"]
		return rt != nil && rt.started.Load()
	})
	hookMu.Lock()
	if len(readyNames) != 1 || readyNames[0] != "alpha" {
		hookMu.Unlock()
		t.Fatalf("readyNames = %v", readyNames)
	}
	hookMu.Unlock()

	if _, ok := m.GetClient("alpha"); !ok {
		t.Fatal("GetClient(alpha) not available after start")
	}
	if _, ok := m.GetCache("alpha"); !ok {
		t.Fatal("GetCache(alpha) not available after start")
	}
	if _, ok := m.GetClient("missing"); ok {
		t.Fatal("GetClient(missing) available")
	}

	snap := m.Snapshot()
	if snap["alpha"].State != StateReachable {
		t.Fatalf("alpha state = %v", snap["alpha"].State)
	}

	// Register after Start also works.
	m.Register("beta", &rest.Config{Host: "beta"})
	waitFor(t, "beta runtime started", func() bool {
		mu.Lock()
		defer mu.Unlock()
		rt := runtimes["beta"]
		return rt != nil && rt.started.Load()
	})

	// Deregister stops the runtime and fires OnClusterGone.
	m.Deregister("alpha")
	mu.Lock()
	alphaRT := runtimes["alpha"]
	mu.Unlock()
	if !alphaRT.stopped.Load() {
		t.Fatal("alpha runtime not stopped after Deregister")
	}
	hookMu.Lock()
	if len(goneNames) != 1 || goneNames[0] != "alpha" {
		hookMu.Unlock()
		t.Fatalf("goneNames = %v", goneNames)
	}
	hookMu.Unlock()
	if _, ok := m.GetClient("alpha"); ok {
		t.Fatal("GetClient(alpha) still available after Deregister")
	}
	if _, ok := m.Snapshot()["alpha"]; ok {
		t.Fatal("alpha still in Snapshot after Deregister")
	}

	// Deregistering an unknown cluster is a no-op.
	m.Deregister("unknown")
}

func TestManagerOutageIsolationAndStates(t *testing.T) {
	probes := &probeSwitch{}
	probes.set("down", true)
	runtimes := map[string]*stubRuntime{}
	var mu sync.Mutex
	m := newTestManager(probes.probe, runtimes, &mu)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = m.Start(ctx) }()
	defer func() { cancel(); <-done }()

	m.Register("up", &rest.Config{Host: "up"})
	m.Register("down", &rest.Config{Host: "down"})

	// The healthy cluster starts despite the other being down.
	waitFor(t, "up runtime started", func() bool {
		mu.Lock()
		defer mu.Unlock()
		rt := runtimes["up"]
		return rt != nil && rt.started.Load()
	})

	// The failing cluster degrades then becomes unreachable; never starts.
	waitFor(t, "down cluster unreachable", func() bool {
		return m.Snapshot()["down"].State == StateUnreachable
	})
	mu.Lock()
	_, downStarted := runtimes["down"]
	mu.Unlock()
	if downStarted {
		t.Fatal("runtime factory called for unreachable cluster")
	}
	conn := m.Snapshot()["down"]
	if conn.Since.IsZero() {
		t.Fatal("unreachable-since not set")
	}
	if conn.LastError == "" {
		t.Fatal("LastError empty for unreachable cluster")
	}
	if m.Snapshot()["up"].State != StateReachable {
		t.Fatalf("up state = %v", m.Snapshot()["up"].State)
	}

	// Recovery: probe starts succeeding, runtime starts, state flips.
	probes.set("down", false)
	waitFor(t, "down runtime started after recovery", func() bool {
		mu.Lock()
		defer mu.Unlock()
		rt := runtimes["down"]
		return rt != nil && rt.started.Load()
	})
	waitFor(t, "down reachable after recovery", func() bool {
		return m.Snapshot()["down"].State == StateReachable
	})
}

func TestManagerShutdownStopsAllRuntimes(t *testing.T) {
	probes := &probeSwitch{}
	runtimes := map[string]*stubRuntime{}
	var mu sync.Mutex
	m := newTestManager(probes.probe, runtimes, &mu)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = m.Start(ctx) }()

	m.Register("a", &rest.Config{Host: "a"})
	m.Register("b", &rest.Config{Host: "b"})
	waitFor(t, "both runtimes started", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return runtimes["a"] != nil && runtimes["a"].started.Load() &&
			runtimes["b"] != nil && runtimes["b"].started.Load()
	})

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Start did not return after context cancel")
	}
}
