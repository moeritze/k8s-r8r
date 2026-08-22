package cluster

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"k8s.io/client-go/rest"
)

// fakeClock is an injectable, manually advanced clock.
type fakeClock struct{ t time.Time }

func (c *fakeClock) Now() time.Time          { return c.t }
func (c *fakeClock) Advance(d time.Duration) { c.t = c.t.Add(d) }

// recordingMinter counts mints and records which credential was used.
type recordingMinter struct {
	clock  *fakeClock
	ttl    time.Duration
	mints  int
	usedBy []string // "sa-token" or "bootstrap" per mint
	err    error
}

func (m *recordingMinter) mint(bootstrapHost string) Minter {
	return func(_ context.Context, cfg *rest.Config, ttl time.Duration) (string, time.Time, error) {
		if m.err != nil {
			return "", time.Time{}, m.err
		}
		m.mints++
		if cfg.BearerToken != "" {
			m.usedBy = append(m.usedBy, "sa-token")
		} else if cfg.Host == bootstrapHost {
			m.usedBy = append(m.usedBy, "bootstrap")
		} else {
			m.usedBy = append(m.usedBy, "unknown")
		}
		return "tok-" + time.Duration(m.mints).String(), m.clock.Now().Add(ttl), nil
	}
}

func newTestRotator(t *testing.T, clock *fakeClock, minter Minter) *Rotator {
	t.Helper()
	base := &rest.Config{Host: "https://spoke.example:6443", BearerToken: "admin-should-be-stripped"}
	bootstrap := &rest.Config{Host: "https://bootstrap.example:6443"}
	return NewRotator(base, minter,
		WithTTL(time.Hour),
		WithClock(clock.Now),
		WithBootstrapConfig(bootstrap),
	)
}

func TestRotatorFirstMintUsesBootstrapCredential(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1000, 0)}
	m := &recordingMinter{clock: clock}
	r := newTestRotator(t, clock, m.mint("https://bootstrap.example:6443"))

	cfg, err := r.Config(context.Background())
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if m.mints != 1 || m.usedBy[0] != "bootstrap" {
		t.Fatalf("mints=%d usedBy=%v, want first mint via bootstrap credential", m.mints, m.usedBy)
	}
	if cfg.BearerToken == "" {
		t.Fatal("returned config has no bearer token")
	}
	if cfg.BearerToken == "admin-should-be-stripped" {
		t.Fatal("returned config carries the admin credential")
	}
	if cfg.Host != "https://spoke.example:6443" {
		t.Fatalf("Host = %q", cfg.Host)
	}
}

func TestRotatorRefreshTiming(t *testing.T) {
	tests := []struct {
		name      string
		advance   time.Duration
		wantMints int // total mints after the second Config call
	}{
		{name: "well before deadline", advance: 10 * time.Minute, wantMints: 1},
		{name: "just before 80% of 1h", advance: 47 * time.Minute, wantMints: 1},
		{name: "exactly at 80% (48m)", advance: 48 * time.Minute, wantMints: 2},
		{name: "past deadline", advance: 55 * time.Minute, wantMints: 2},
		{name: "past expiry", advance: 2 * time.Hour, wantMints: 2},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clock := &fakeClock{t: time.Unix(1000, 0)}
			m := &recordingMinter{clock: clock}
			r := newTestRotator(t, clock, m.mint("https://bootstrap.example:6443"))

			if _, err := r.Config(context.Background()); err != nil {
				t.Fatalf("first Config: %v", err)
			}
			clock.Advance(tc.advance)
			if _, err := r.Config(context.Background()); err != nil {
				t.Fatalf("second Config: %v", err)
			}
			if m.mints != tc.wantMints {
				t.Fatalf("mints = %d, want %d", m.mints, tc.wantMints)
			}
		})
	}
}

func TestRotatorSelfRenewsWithCurrentToken(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1000, 0)}
	m := &recordingMinter{clock: clock}
	r := newTestRotator(t, clock, m.mint("https://bootstrap.example:6443"))

	if _, err := r.Config(context.Background()); err != nil {
		t.Fatalf("Config: %v", err)
	}
	// Past refresh deadline but token still valid: renew as the SA.
	clock.Advance(50 * time.Minute)
	if _, err := r.Config(context.Background()); err != nil {
		t.Fatalf("Config: %v", err)
	}
	if len(m.usedBy) != 2 || m.usedBy[1] != "sa-token" {
		t.Fatalf("usedBy = %v, want second mint via sa-token", m.usedBy)
	}
}

func TestRotatorHardExpiryFallsBackToBootstrap(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1000, 0)}
	m := &recordingMinter{clock: clock}
	r := newTestRotator(t, clock, m.mint("https://bootstrap.example:6443"))

	if _, err := r.Config(context.Background()); err != nil {
		t.Fatalf("Config: %v", err)
	}
	// Token fully expired (operator down > TTL): only the bootstrap
	// credential can mint.
	clock.Advance(3 * time.Hour)
	if _, err := r.Config(context.Background()); err != nil {
		t.Fatalf("Config: %v", err)
	}
	if len(m.usedBy) != 2 || m.usedBy[1] != "bootstrap" {
		t.Fatalf("usedBy = %v, want second mint via bootstrap", m.usedBy)
	}
}

func TestRotatorHardExpiryWithoutBootstrapErrors(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1000, 0)}
	m := &recordingMinter{clock: clock}
	base := &rest.Config{Host: "https://spoke.example:6443"}
	r := NewRotator(base, m.mint(""), WithTTL(time.Hour), WithClock(clock.Now))

	if _, err := r.Config(context.Background()); err == nil {
		t.Fatal("first Config without bootstrap credential succeeded, want error")
	}
}

func TestRotatorMintErrorHasNoTokenMaterial(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1000, 0)}
	m := &recordingMinter{clock: clock, err: errors.New("connection refused")}
	r := newTestRotator(t, clock, m.mint("https://bootstrap.example:6443"))

	_, err := r.Config(context.Background())
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), Namespace+"/"+ServiceAccountName) {
		t.Fatalf("error should reference the SA by name: %v", err)
	}
}
