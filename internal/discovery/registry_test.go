package discovery

import (
	"context"
	"testing"
)

// stubProvider is a minimal Provider for registry tests.
type stubProvider struct {
	name    string
	records []ClusterRecord
}

func (s *stubProvider) Name() string           { return s.name }
func (s *stubProvider) Subscribe(EventHandler) {}
func (s *stubProvider) Start(ctx context.Context) error {
	<-ctx.Done()
	return nil
}
func (s *stubProvider) List() []ClusterRecord { return s.records }
func (s *stubProvider) Watching() bool        { return true }

func TestRegistry(t *testing.T) {
	r := NewRegistry()
	factory := func(o Options) (Provider, error) {
		return &stubProvider{name: "stub"}, nil
	}

	if err := r.Register("stub", factory); err != nil {
		t.Fatalf("Register: %v", err)
	}
	if err := r.Register("stub", factory); err == nil {
		t.Fatal("duplicate Register succeeded, want error")
	}
	if err := r.Register("", factory); err == nil {
		t.Fatal("empty-name Register succeeded, want error")
	}
	if err := r.Register("nilf", nil); err == nil {
		t.Fatal("nil-factory Register succeeded, want error")
	}

	p, err := r.New("stub", Options{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if p.Name() != "stub" {
		t.Fatalf("Name = %q, want stub", p.Name())
	}

	if _, err := r.New("unknown", Options{}); err == nil {
		t.Fatal("New(unknown) succeeded, want error")
	}

	names := r.Names()
	if len(names) != 1 || names[0] != "stub" {
		t.Fatalf("Names = %v, want [stub]", names)
	}
}

func TestOptionsSetting(t *testing.T) {
	o := Options{Settings: map[string]string{"namespace": "fleet"}}
	if got := o.Setting("namespace", "def"); got != "fleet" {
		t.Fatalf("Setting = %q, want fleet", got)
	}
	if got := o.Setting("missing", "def"); got != "def" {
		t.Fatalf("Setting default = %q, want def", got)
	}
	var empty Options
	if got := empty.Setting("any", "def"); got != "def" {
		t.Fatalf("Setting on empty Options = %q, want def", got)
	}
}

func TestClusterRecordClone(t *testing.T) {
	orig := ClusterRecord{
		Name:          "c1",
		Labels:        map[string]string{"env": "prod"},
		Ready:         true,
		CredentialRef: CredentialRef{Namespace: "fleet", Name: "c1-kubeconfig"},
	}
	clone := orig.Clone()
	clone.Labels["env"] = "dev"
	if orig.Labels["env"] != "prod" {
		t.Fatal("Clone aliased the labels map")
	}
}

func TestCredentialRefString(t *testing.T) {
	ref := CredentialRef{Namespace: "fleet", Name: "c1-kubeconfig"}
	if got := ref.String(); got != "fleet/c1-kubeconfig" {
		t.Fatalf("String = %q", got)
	}
}
