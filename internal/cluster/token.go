package cluster

import (
	"context"
	"fmt"
	"sync"
	"time"

	authenticationv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/utils/ptr"

	"github.com/moeritze/k8s-r8r/internal/telemetry"
)

// DefaultTokenTTL is the requested ServiceAccount token lifetime.
const DefaultTokenTTL = time.Hour

// DefaultRefreshFraction is the fraction of a token's lifetime after which
// it is refreshed (~80%, i.e. a 1h token refreshes after 48m).
const DefaultRefreshFraction = 0.8

// Minter obtains a short-lived ServiceAccount token using the credentials in
// cfg. Implementations must never place token material in error strings.
type Minter func(ctx context.Context, cfg *rest.Config, ttl time.Duration) (token string, expiresAt time.Time, err error)

// TokenRequestMinter mints tokens for the k8s-r8r ServiceAccount via the
// TokenRequest API.
func TokenRequestMinter() Minter {
	return func(ctx context.Context, cfg *rest.Config, ttl time.Duration) (string, time.Time, error) {
		cs, err := kubernetes.NewForConfig(cfg)
		if err != nil {
			return "", time.Time{}, fmt.Errorf("cluster: building token client: %w", err)
		}
		req := &authenticationv1.TokenRequest{
			Spec: authenticationv1.TokenRequestSpec{
				ExpirationSeconds: ptr.To(int64(ttl / time.Second)),
			},
		}
		resp, err := cs.CoreV1().ServiceAccounts(Namespace).CreateToken(ctx, ServiceAccountName, req, metav1.CreateOptions{})
		if err != nil {
			return "", time.Time{}, fmt.Errorf("cluster: requesting token for %s/%s: %w", Namespace, ServiceAccountName, err)
		}
		return resp.Status.Token, resp.Status.ExpirationTimestamp.Time, nil
	}
}

// RotatorOption customizes a Rotator.
type RotatorOption func(*Rotator)

// WithTTL sets the requested token lifetime (default DefaultTokenTTL).
func WithTTL(ttl time.Duration) RotatorOption {
	return func(r *Rotator) { r.ttl = ttl }
}

// WithRefreshFraction sets the lifetime fraction after which a token is
// refreshed (default DefaultRefreshFraction).
func WithRefreshFraction(f float64) RotatorOption {
	return func(r *Rotator) { r.refreshFraction = f }
}

// WithClock injects a clock for tests.
func WithClock(now func() time.Time) RotatorOption {
	return func(r *Rotator) { r.now = now }
}

// WithBootstrapConfig provides the admin credential used to mint the first
// token and, as a last resort, to re-mint after a hard expiry (operator down
// longer than the token lifetime) — a re-bootstrap-class event. Without it
// the Rotator can only self-renew from a still-valid token.
func WithBootstrapConfig(cfg *rest.Config) RotatorOption {
	return func(r *Rotator) { r.bootstrap = cfg }
}

// Rotator hands out rest configs authenticated with a short-lived k8s-r8r
// ServiceAccount token, transparently refreshing the token before it expires
// (at ~80% of its lifetime by default).
//
// Renewal authenticates as the ServiceAccount itself (the bootstrap grants it
// create on its own token subresource), so after the first mint the admin
// credential is never used again unless the token hard-expires.
type Rotator struct {
	base            *rest.Config // credential-free copy of the spoke config
	minter          Minter
	bootstrap       *rest.Config // optional admin credential (first mint / hard expiry)
	ttl             time.Duration
	refreshFraction float64
	now             func() time.Time

	mu        sync.Mutex
	token     string
	issuedAt  time.Time
	expiresAt time.Time
}

// NewRotator builds a Rotator. base carries the spoke's host/TLS parameters;
// its authentication material (if any) is stripped and never used.
func NewRotator(base *rest.Config, minter Minter, opts ...RotatorOption) *Rotator {
	r := &Rotator{
		base:            rest.AnonymousClientConfig(base),
		minter:          minter,
		ttl:             DefaultTokenTTL,
		refreshFraction: DefaultRefreshFraction,
		now:             time.Now,
	}
	for _, o := range opts {
		o(r)
	}
	return r
}

// Config returns a rest config carrying a currently valid SA token,
// refreshing first when the active token passed its refresh deadline.
func (r *Rotator) Config(ctx context.Context) (*rest.Config, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.needsRefreshLocked(r.now()) {
		if err := r.refreshLocked(ctx); err != nil {
			return nil, err
		}
	}
	cfg := rest.CopyConfig(r.base)
	cfg.BearerToken = r.token
	return cfg, nil
}

// Run proactively refreshes the token in the background until ctx is done,
// so Config calls on the hot path never block on a mint. Optional — Config
// alone also keeps the token fresh lazily.
func (r *Rotator) Run(ctx context.Context) error {
	for {
		r.mu.Lock()
		wait := r.refreshDeadlineLocked().Sub(r.now())
		r.mu.Unlock()
		if wait < time.Second {
			wait = time.Second
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(wait):
		}
		r.mu.Lock()
		if r.needsRefreshLocked(r.now()) {
			// Errors are retried on the next tick; Config's lazy
			// refresh also covers gaps.
			_ = r.refreshLocked(ctx)
		}
		r.mu.Unlock()
	}
}

// refreshDeadlineLocked is the instant at which the active token should be
// replaced: issuance + refreshFraction * lifetime.
func (r *Rotator) refreshDeadlineLocked() time.Time {
	if r.token == "" {
		return r.now()
	}
	lifetime := r.expiresAt.Sub(r.issuedAt)
	return r.issuedAt.Add(time.Duration(float64(lifetime) * r.refreshFraction))
}

func (r *Rotator) needsRefreshLocked(now time.Time) bool {
	return r.token == "" || !now.Before(r.refreshDeadlineLocked())
}

// refreshLocked mints a new token. It authenticates with the current token
// while that is still valid; otherwise it falls back to the bootstrap
// credential when configured.
func (r *Rotator) refreshLocked(ctx context.Context) error {
	now := r.now()
	var mintCfg *rest.Config
	switch {
	case r.token != "" && now.Before(r.expiresAt):
		mintCfg = rest.CopyConfig(r.base)
		mintCfg.BearerToken = r.token
	case r.bootstrap != nil:
		mintCfg = r.bootstrap
	default:
		return fmt.Errorf("cluster: token for %s/%s expired and no bootstrap credential available", Namespace, ServiceAccountName)
	}
	token, expiresAt, err := r.minter(ctx, mintCfg, r.ttl)
	if err != nil {
		telemetry.IncTokenRotation(false)
		return fmt.Errorf("cluster: refreshing token for %s/%s: %w", Namespace, ServiceAccountName, err)
	}
	telemetry.IncTokenRotation(true)
	r.token = token
	r.issuedAt = now
	r.expiresAt = expiresAt
	return nil
}

// BootstrapResult bundles the outputs of a completed spoke bootstrap.
type BootstrapResult struct {
	// Rotator hands out SA-token rest configs for all steady-state
	// traffic to the spoke.
	Rotator *Rotator
}

// BootstrapSpoke performs the full one-shot bootstrap against a spoke using
// the provider's admin config: ensure namespace/SA/RBAC, mint the first
// ServiceAccount token, and return a Rotator primed with it. After this call
// all traffic must go through the Rotator's configs; adminCfg is retained
// only as the hard-expiry fallback.
func BootstrapSpoke(ctx context.Context, adminCfg *rest.Config, scope RBACScope, opts ...RotatorOption) (*BootstrapResult, error) {
	b, err := NewBootstrapper(adminCfg)
	if err != nil {
		return nil, err
	}
	if err := b.Bootstrap(ctx, scope); err != nil {
		return nil, err
	}
	opts = append([]RotatorOption{WithBootstrapConfig(adminCfg)}, opts...)
	rotator := NewRotator(adminCfg, TokenRequestMinter(), opts...)
	// Prime the first token (minted via the bootstrap credential — the
	// one-shot admin use).
	if _, err := rotator.Config(ctx); err != nil {
		return nil, err
	}
	return &BootstrapResult{Rotator: rotator}, nil
}
