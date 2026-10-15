package middleware

import (
	"container/list"
	"math"
	"net/http"
	"net/netip"
	"strconv"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/socialos/backend/internal/domain/actor"
	"github.com/socialos/backend/internal/domain/errs"
	"github.com/socialos/backend/internal/observability"
	"github.com/socialos/backend/internal/transport/httpx"
)

// MaxTrackedKeys is how many keys a Limiter tracks at once. A caller that rotates keys (addresses, or anything else an
// attacker controls) could otherwise grow the map until the process runs out of memory.
const MaxTrackedKeys = 50_000

// Limiter is an in-memory token bucket per key (per API instance). It tracks at most maxKeys keys: when a new key
// arrives at the cap, the least recently seen key is dropped (it simply starts over with a full bucket if it returns).
// A new key is always limited like any other; the cap never lets a request skip the limit.
type Limiter struct {
	rps     rate.Limit
	burst   int
	maxKeys int
	mu      sync.Mutex
	keys    map[string]*list.Element // key -> element of order holding a *bucket
	order   *list.List               // most recently seen at the front
	idle    time.Duration
}

type bucket struct {
	key  string
	lim  *rate.Limiter
	seen time.Time
}

// NewLimiter creates a limiter with rps tokens/second and burst capacity that tracks up to MaxTrackedKeys keys.
func NewLimiter(rps float64, burst int) *Limiter {
	return NewLimiterWithCap(rps, burst, MaxTrackedKeys)
}

// NewLimiterWithCap is NewLimiter with an explicit cap on the number of tracked keys (minimum 1).
func NewLimiterWithCap(rps float64, burst, maxKeys int) *Limiter {
	return &Limiter{rps: rate.Limit(rps), burst: burst, maxKeys: max(maxKeys, 1), keys: map[string]*list.Element{},
		order: list.New(), idle: 10 * time.Minute}
}

// Len is the number of keys currently tracked.
func (l *Limiter) Len() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.keys)
}

// Allow consumes a token for key; it returns the suggested retry delay when denied.
func (l *Limiter) Allow(key string) (bool, time.Duration) {
	now := time.Now()
	l.mu.Lock()
	var b *bucket
	if el, ok := l.keys[key]; ok {
		l.order.MoveToFront(el)
		b = el.Value.(*bucket)
	} else {
		for len(l.keys) >= l.maxKeys {
			l.drop(l.order.Back())
		}
		b = &bucket{key: key, lim: rate.NewLimiter(l.rps, l.burst)}
		l.keys[key] = l.order.PushFront(b)
	}
	b.seen = now
	l.mu.Unlock()
	r := b.lim.ReserveN(now, 1)
	if !r.OK() {
		return false, time.Minute
	}
	if d := r.DelayFrom(now); d > 0 {
		r.CancelAt(now)
		return false, d
	}
	return true, 0
}

// drop forgets one tracked key; the caller holds l.mu.
func (l *Limiter) drop(el *list.Element) {
	delete(l.keys, el.Value.(*bucket).key)
	l.order.Remove(el)
}

// Sweep drops idle buckets; call periodically. The list is ordered by last use, so it stops at the first fresh one.
func (l *Limiter) Sweep() {
	cutoff := time.Now().Add(-l.idle)
	l.mu.Lock()
	for el := l.order.Back(); el != nil && el.Value.(*bucket).seen.Before(cutoff); el = l.order.Back() {
		l.drop(el)
	}
	l.mu.Unlock()
}

// RateLimit applies the limiter keyed by actor (user/API key) or, for anonymous callers, the client IP as resolved by
// ClientIP (so a forged X-Forwarded-For cannot pick the bucket).
func RateLimit(l *Limiter, trusted TrustedProxies, m *observability.Metrics, prefix string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key := prefix + "ip:" + bucketAddr(limitIP(r, trusted))
			if a, ok := actor.From(r.Context()); ok {
				key = prefix + string(a.Type) + ":" + a.ID
			}
			if ok, wait := l.Allow(key); !ok {
				if m != nil {
					m.RateLimited.Inc()
				}
				w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
				httpx.ErrorCode(w, r, errs.RateLimited, "too many requests")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// limitIP is the address an anonymous request is throttled by: the end client's as vouched for by the MCP gateway
// (Gateway, with MCP_GATEWAY_SECRET), so agents behind one MCP server do not share a single bucket, and otherwise the
// one resolved through TRUSTED_PROXIES. A forged gateway header without the secret changes nothing.
func limitIP(r *http.Request, trusted TrustedProxies) string {
	if ip, ok := gatewayIP(r.Context()); ok {
		return ip
	}
	return ClientIP(r, trusted)
}

// bucketAddr maps a client address to the unit that shares a bucket: an IPv4 address is its own unit, but an IPv6
// client normally owns a whole /64, so it could mint a fresh /128 per request and never be throttled. Only the
// limiter uses this; audit logs and sessions keep the full address.
func bucketAddr(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	a = a.Unmap()
	if a.Is6() && !a.Is4In6() {
		return netip.PrefixFrom(a, 64).Masked().Addr().String()
	}
	return a.String()
}

// AgentRateLimit caps the requests of all API keys and MCP connections of one user together (QUOTA_AGENT_RPM), so
// minting more keys does not raise the cap. Browser sessions are not counted; a nil limiter switches it off.
func AgentRateLimit(l *Limiter, m *observability.Metrics) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if l == nil {
			return next
		}
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if a, ok := actor.From(r.Context()); ok && a.Type == actor.TypeAPIKey {
				if ok, wait := l.Allow("agent:user:" + a.UserID.String()); !ok {
					if m != nil {
						m.RateLimited.Inc()
					}
					w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(wait.Seconds()))))
					httpx.ErrorCode(w, r, errs.RateLimited, "agent request limit reached for your plan; retry after the pause in Retry-After")
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}
