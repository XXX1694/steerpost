package e2e

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/socialos/backend/internal/testutil/githubfake"
)

// A user without a password deletes the account with a session from the last 10 minutes; an older one is asked to
// sign in again (403 REAUTH_REQUIRED) instead of hitting a 500 or being locked out of deletion (AGENTS section 7).
func TestSocialUserWithoutPasswordCanDeleteTheAccount(t *testing.T) {
	clk := &steppingClock{}
	// Start at the real time: the session cookie's Max-Age is counted on the real clock, so a start in the past expires
	// the cookie before the first request.
	clk.set(time.Now().UTC())
	r := newSocialRig(t, withClock(clk))
	const email = "pat@example.com"
	pat := r.githubSignUp(t, 1501, "pat", email)

	// Fresh: right after sign-up.
	if res := pat.deleteAccount("", "someone-else@example.com"); res.status != 400 || res.errBody(t)["fields"].(map[string]any)["confirm"] == nil {
		t.Fatalf("the typed email is still required: %d %s", res.status, res.body)
	}

	// Stale: 11 minutes later the same session must be renewed.
	clk.set(clk.Now().Add(11 * time.Minute))
	res := pat.deleteAccount("", email)
	if res.status != 403 || res.errCode(t) != "REAUTH_REQUIRED" {
		t.Fatalf("stale session: %d %s", res.status, res.body)
	}
	if n := r.countRows(`SELECT count(*) FROM users WHERE deletion_scheduled_at IS NOT NULL`); n != 0 {
		t.Fatalf("a refused request scheduled a deletion")
	}
	pat.must("GET", "/api/v1/me", nil, 200) // the session was left alone

	// Signing in again with the provider gives a fresh session, and deletion goes through.
	again := r.e.browser()
	if loc := r.githubSignIn(t, again, 1501, "pat", email, ""); errorOf(loc) != "" {
		t.Fatalf("sign in again: %s", loc)
	}
	me := again.must("GET", "/api/v1/me", nil, 200)
	again.csrf, _ = me["csrf_token"].(string)
	if res := again.deleteAccount("", email); res.status != 202 {
		t.Fatalf("fresh session: %d %s", res.status, res.body)
	}
	if n := r.countRows(`SELECT count(*) FROM users WHERE deletion_scheduled_at IS NOT NULL`); n != 1 {
		t.Fatalf("deletion not scheduled: %d", n)
	}
	// The account is leaving, but the owner can still sign in with the provider to cancel (no password to fall back on).
	back := r.e.browser()
	if loc := r.githubSignIn(t, back, 1501, "pat", email, ""); errorOf(loc) != "" {
		t.Fatalf("sign-in during the grace period: %s", loc)
	}
	me = back.must("GET", "/api/v1/me", nil, 200)
	back.csrf, _ = me["csrf_token"].(string)
	if me["user"].(map[string]any)["deletion_scheduled_at"] == nil {
		t.Fatal("/me must show the pending deletion")
	}
	back.must("POST", "/api/v1/account/delete/cancel", nil, 204)
	if n := r.countRows(`SELECT count(*) FROM users WHERE deletion_scheduled_at IS NOT NULL`); n != 0 {
		t.Fatal("deletion not cancelled")
	}
}

// A user with a password still has to type it, however fresh the session.
func TestSocialFreshSessionDoesNotWaiveTheOwnersPassword(t *testing.T) {
	r := newSocialRig(t, envOpts{})
	c := r.e.browser()
	c.register("pw-owner@example.com")
	if res := c.deleteAccount("", "pw-owner@example.com"); res.status != 400 || res.errBody(t)["fields"].(map[string]any)["password"] == nil {
		t.Fatalf("%d %s", res.status, res.body)
	}
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.String()
}

// Nothing a person or a provider handed over ends up in the logs or in audit metadata: no address, no token, no code,
// no state and no ticket.
func TestSocialLeavesNoSecretsInLogsOrAudit(t *testing.T) {
	logs := &lockedBuffer{}
	r := newSocialRig(t, envOpts{logger: slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))})
	const email = "secret-person@example.com"
	var seen []string

	c := r.e.browser()
	f := r.start(t, c, "github", "")
	code := r.githubCode(f, 1601, "quiet", email)
	seen = append(seen, f.state, f.nonce, code)
	if loc := r.callback(t, c, "github", code, f.state); loc.Path != "/signup/complete" {
		t.Fatal(loc)
	}
	ticket := ""
	if ck := c.http.Jar.Cookies(mustURL(t, r.e.srv.URL+apiOAuth)); len(ck) > 0 {
		for _, k := range ck {
			if k.Name == ticketCookie {
				ticket = k.Value
			}
		}
	}
	if ticket == "" {
		t.Fatal("no ticket cookie")
	}
	seen = append(seen, ticket)
	c.must("POST", apiOAuth+"complete", map[string]any{"display_name": "Quiet", "accept_terms": true}, 201)

	// A provider failure and a returning sign-in as well.
	f = r.start(t, c, "github", "")
	bad := r.gh.Code(&githubfake.Account{ID: 1601, TokenError: true})
	seen = append(seen, f.state, bad)
	r.callback(t, r.e.browser(), "github", bad, f.state) // wrong browser: state invalid
	f = r.start(t, c, "github", "")
	bad = r.gh.Code(&githubfake.Account{ID: 1601, TokenError: true})
	seen = append(seen, f.state, bad)
	if got := errorOf(r.callback(t, c, "github", bad, f.state)); got != "oauth_provider_error" {
		t.Fatalf("provider failure: %q", got)
	}
	f = r.start(t, c, "github", "")
	code = r.githubCode(f, 1601, "quiet", email)
	seen = append(seen, f.state, code)
	r.callback(t, c, "github", code, f.state)

	out := logs.String()
	if !strings.Contains(out, "social sign-in") {
		t.Fatalf("the failed flow should have been logged:\n%s", out)
	}
	for _, secret := range append(seen, email, "gho_") {
		if secret != "" && strings.Contains(out, secret) {
			t.Fatalf("logs contain %q", secret)
		}
	}
	rows, err := r.e.app.DB.Pool.Query(context.Background(), `SELECT metadata::text FROM audit_logs`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var meta string
		if err := rows.Scan(&meta); err != nil {
			t.Fatal(err)
		}
		for _, secret := range append(seen, "@", "gho_") {
			if secret != "" && strings.Contains(meta, secret) {
				t.Fatalf("audit metadata %s contains %q", meta, secret)
			}
		}
	}
}
