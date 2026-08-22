package discovery

import (
	"fmt"
	"sort"
	"sync"

	"k8s.io/client-go/rest"
)

// Options carries everything a provider Factory may need to construct a
// Provider. Fields irrelevant to a given provider are ignored by it.
type Options struct {
	// HubConfig is the rest config for the hub cluster (where fleet
	// management objects such as ClusterAPI Clusters live).
	HubConfig *rest.Config
	// Settings holds provider-specific string settings from operator
	// deployment configuration (e.g. a watch namespace).
	Settings map[string]string
}

// Setting returns the named setting or the given default when unset.
func (o Options) Setting(key, def string) string {
	if v, ok := o.Settings[key]; ok {
		return v
	}
	return def
}

// Factory constructs a Provider from deployment options.
type Factory func(Options) (Provider, error)

// Registry maps provider names to factories. Provider selection is a plain
// config value (the provider name), so swapping or adding providers requires
// no code changes outside the provider package itself.
type Registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{factories: map[string]Factory{}}
}

// Register adds a factory under name. Registering a duplicate name is a
// programming error and returns an error.
func (r *Registry) Register(name string, f Factory) error {
	if name == "" || f == nil {
		return fmt.Errorf("discovery: invalid registration for provider %q", name)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.factories[name]; dup {
		return fmt.Errorf("discovery: provider %q already registered", name)
	}
	r.factories[name] = f
	return nil
}

// MustRegister is Register but panics on error. Intended for provider
// package init functions where a duplicate name is a build-time bug.
func (r *Registry) MustRegister(name string, f Factory) {
	if err := r.Register(name, f); err != nil {
		panic(err)
	}
}

// New constructs the provider selected by name.
func (r *Registry) New(name string, o Options) (Provider, error) {
	r.mu.RLock()
	f, ok := r.factories[name]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("discovery: unknown provider %q (registered: %v)", name, r.Names())
	}
	return f(o)
}

// Names returns the sorted names of all registered providers.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.factories))
	for n := range r.factories {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Default is the process-wide registry that provider packages register into
// from their init functions.
var Default = NewRegistry()

// Register registers a factory in the Default registry.
func Register(name string, f Factory) error { return Default.Register(name, f) }

// MustRegister registers a factory in the Default registry, panicking on error.
func MustRegister(name string, f Factory) { Default.MustRegister(name, f) }

// New constructs a provider by name from the Default registry.
func New(name string, o Options) (Provider, error) { return Default.New(name, o) }
