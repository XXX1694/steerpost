package middleware_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/socialos/backend/internal/domain/actor"
	"github.com/socialos/backend/internal/transport/middleware"
)

// An IPv6 client owns a whole /64, so the limiter must treat the /64 as one client, or a single host could mint a
// fresh bucket per request. IPv4 keeps one bucket per address.
func TestRateLimitBucketsIPv6ByPrefix(t *testing.T) {
	newHandler := func(trusted middleware.TrustedProxies) http.Handler {
		return middleware.RequestID(middleware.RateLimit(middleware.NewLimiter(0.001, 1), trusted, nil, "t:")(http.HandlerFunc(ok)))
	}
	hit := func(h http.Handler, remote string, xff ...string) int {
		return do(h, "GET", "/", func(r *http.Request) {
			r.RemoteAddr = remote
			for _, v := range xff {
				r.Header.Add("X-Forwarded-For", v)
			}
		}).Code
	}
	for name, tc := range map[string]struct {
		trusted middleware.TrustedProxies
		send    func(h http.Handler, addr string) int
	}{
		"direct peer":      {nil, func(h http.Handler, a string) int { return hit(h, "["+a+"]:443") }},
		"behind a proxy":   {privateProxies, func(h http.Handler, a string) int { return hit(h, "10.0.0.1:1", a) }},
		"bracketed in XFF": {privateProxies, func(h http.Handler, a string) int { return hit(h, "10.0.0.1:1", "6.6.6.6, ["+a+"]:5555") }},
	} {
		t.Run(name, func(t *testing.T) {
			h := newHandler(tc.trusted)
			if c := tc.send(h, "2001:db8:0:1::1"); c != 204 {
				t.Fatalf("first request: %d", c)
			}
			for _, same := range []string{"2001:db8:0:1::2", "2001:db8:0:1:ffff:ffff:ffff:ffff", "2001:DB8:0:1:abcd::7"} {
				if c := tc.send(h, same); c != 429 {
					t.Errorf("%s is in the same /64 and must share the bucket, got %d", same, c)
				}
			}
			for _, other := range []string{"2001:db8:0:2::1", "2001:db8:1:1::1", "2001:db9:0:1::1"} {
				if c := tc.send(h, other); c != 204 {
					t.Errorf("%s is another /64 and must have its own bucket, got %d", other, c)
				}
			}
		})
	}

	t.Run("IPv4 is one bucket per address", func(t *testing.T) {
		h := newHandler(nil)
		if hit(h, "192.0.2.1:1") != 204 || hit(h, "192.0.2.1:2") != 429 {
			t.Fatal("the same address must share a bucket")
		}
		for _, other := range []string{"192.0.2.2:1", "192.0.2.255:1", "192.0.3.1:1", "198.51.100.1:1"} {
			if c := hit(h, other); c != 204 {
				t.Errorf("%s must have its own bucket, got %d", other, c)
			}
		}
	})
	t.Run("IPv4-mapped IPv6 is the IPv4 address, not a /64", func(t *testing.T) {
		h := newHandler(nil)
		if hit(h, "192.0.2.1:1") != 204 {
			t.Fatal("first request")
		}
		if c := hit(h, "[::ffff:192.0.2.1]:1"); c != 429 {
			t.Errorf("mapped spelling of the same address got %d, want 429", c)
		}
		if c := hit(h, "[::ffff:192.0.2.2]:1"); c != 204 {
			t.Errorf("a neighbouring mapped address must not share the bucket, got %d", c)
		}
	})
}

func TestLimiterCapsTrackedKeys(t *testing.T) {
	t.Run("a rotating key never grows the map past the cap", func(t *testing.T) {
		const limit = 100
		l := middleware.NewLimiterWithCap(0.001, 1, limit)
		for i := 0; i < 10*limit; i++ {
			key := fmt.Sprintf("k%d", i)
			if ok, _ := l.Allow(key); !ok {
				t.Fatalf("the first request of a new key must pass its own fresh bucket (%s)", key)
			}
			if n := l.Len(); n > limit {
				t.Fatalf("tracking %d keys, cap is %d", n, limit)
			}
		}
		if n := l.Len(); n != limit {
			t.Fatalf("tracking %d keys after the flood, want exactly the cap %d", n, limit)
		}
	})

	t.Run("a new key at the cap is limited, never waved through", func(t *testing.T) {
		l := middleware.NewLimiterWithCap(0.001, 1, 2)
		l.Allow("a")
		l.Allow("b")
		if ok, _ := l.Allow("c"); !ok { // evicts the oldest ("a"), gets its own bucket
			t.Fatal("first request of c")
		}
		if ok, wait := l.Allow("c"); ok || wait <= 0 {
			t.Fatalf("c's second request at the cap must be throttled: ok=%v wait=%v", ok, wait)
		}
	})

	t.Run("least recently seen keys go first, busy keys stay throttled", func(t *testing.T) {
		l := middleware.NewLimiterWithCap(0.001, 1, 3)
		for _, k := range []string{"a", "b", "c"} {
			l.Allow(k)
		}
		if ok, _ := l.Allow("b"); ok { // refreshes b: the order is now a (oldest), c, b
			t.Fatal("b is throttled")
		}
		l.Allow("d") // evicts a
		if ok, _ := l.Allow("b"); ok {
			t.Error("b was recently used and must keep its (empty) bucket")
		}
		if ok, _ := l.Allow("c"); ok {
			t.Error("c is still tracked and must stay throttled")
		}
		if ok, _ := l.Allow("a"); !ok {
			t.Error("a was evicted as the least recently seen and starts over with a full bucket")
		}
		if n := l.Len(); n != 3 {
			t.Errorf("tracking %d keys, want 3", n)
		}
	})

	t.Run("the default cap is used by NewLimiter and a cap below 1 means 1", func(t *testing.T) {
		if middleware.MaxTrackedKeys != 50_000 {
			t.Fatalf("MaxTrackedKeys = %d", middleware.MaxTrackedKeys)
		}
		l := middleware.NewLimiterWithCap(0.001, 1, 0)
		l.Allow("x")
		l.Allow("y")
		if l.Len() != 1 {
			t.Fatalf("tracking %d keys", l.Len())
		}
	})

	t.Run("a flood of rotating anonymous addresses through the middleware stays bounded", func(t *testing.T) {
		l := middleware.NewLimiterWithCap(0.001, 1, 50)
		h := middleware.RequestID(middleware.RateLimit(l, nil, nil, "t:")(http.HandlerFunc(ok)))
		for i := 0; i < 500; i++ {
			do(h, "GET", "/", func(r *http.Request) { r.RemoteAddr = fmt.Sprintf("[2001:db8:%x::1]:1", i) })
		}
		if n := l.Len(); n != 50 {
			t.Fatalf("tracking %d keys, want 50", n)
		}
	})
}

func TestAgentRateLimit(t *testing.T) {
	user := uuid.New()
	as := func(typ actor.Type, id string, uid uuid.UUID) func(*http.Request) {
		return func(r *http.Request) {
			*r = *r.WithContext(actor.With(r.Context(), actor.Actor{UserID: uid, Type: typ, ID: id}))
		}
	}
	h := func(l *middleware.Limiter) http.Handler {
		return middleware.RequestID(middleware.AgentRateLimit(l, nil)(http.HandlerFunc(ok)))
	}
	l := middleware.NewLimiter(0.001, 2)
	// Two different keys of one user draw from the same budget.
	if c := do(h(l), "GET", "/", as(actor.TypeAPIKey, "k1", user)).Code; c != 204 {
		t.Fatal(c)
	}
	if c := do(h(l), "GET", "/", as(actor.TypeAPIKey, "k2", user)).Code; c != 204 {
		t.Fatal(c)
	}
	if r := do(h(l), "GET", "/", as(actor.TypeAPIKey, "k3", user)); r.Code != 429 || r.Header().Get("Retry-After") == "" {
		t.Fatalf("third request: %d", r.Code)
	}
	// Sessions are not agents, other users have their own budget, a nil limiter is off.
	if c := do(h(l), "GET", "/", as(actor.TypeUser, "u", user)).Code; c != 204 {
		t.Errorf("session: %d", c)
	}
	if c := do(h(l), "GET", "/", as(actor.TypeAPIKey, "k1", uuid.New())).Code; c != 204 {
		t.Errorf("other user: %d", c)
	}
	if c := do(h(nil), "GET", "/", as(actor.TypeAPIKey, "k1", user)).Code; c != 204 {
		t.Errorf("nil limiter: %d", c)
	}
}

// Anonymous agents reach the API through one MCP server. With a valid gateway secret each end client gets its own
// bucket; without it (or with a forged one) every request counts against the caller's own address.
func TestRateLimitUsesTheGatewayVouchedAddress(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef"
	h := middleware.RequestID(middleware.Gateway(secret)(middleware.RateLimit(middleware.NewLimiter(0.001, 1), nil, nil, "t:")(http.HandlerFunc(ok))))
	hit := func(gateway, clientIP string) int {
		return do(h, "POST", "/", func(r *http.Request) {
			r.RemoteAddr = "192.0.2.10:5000" // the MCP server
			r.Header.Set(middleware.GatewayHeader, gateway)
			r.Header.Set(middleware.ClientIPHeader, clientIP)
		}).Code
	}
	if a, b := hit(secret, "198.51.100.1"), hit(secret, "198.51.100.2"); a != 204 || b != 204 {
		t.Fatalf("two vouched clients must have their own buckets: %d %d", a, b)
	}
	if c := hit(secret, "198.51.100.1"); c != 429 {
		t.Fatalf("the same vouched client again: %d", c)
	}
	if a, b := hit("forged", "198.51.100.3"), hit("forged", "198.51.100.4"); a != 204 || b != 429 {
		t.Fatalf("a forged gateway header must fall back to the peer's bucket: %d %d", a, b)
	}
}
