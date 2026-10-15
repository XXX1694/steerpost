package e2e

import (
	"context"
	"net/http"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/socialos/backend/internal/config"
)

const (
	agentSignupPath = "/api/v1/auth/agent-signups"
	agentScopes     = "analytics:read media:write posts:read posts:schedule posts:write social:connect social:read"
)

func agentSignupBody(email string) map[string]any {
	return map[string]any{"email": email, "agent_name": "Claude Desktop", "accept_terms": true}
}

// signUpAgent asks for a key for email the way the MCP sign_up tool does and returns the raw key.
func signUpAgent(t *testing.T, e *env, email string) string {
	t.Helper()
	m := e.browser().must("POST", agentSignupPath, agentSignupBody(email), http.StatusCreated)
	key, _ := m["api_key"].(string)
	if !strings.HasPrefix(key, "sk_live_") {
		t.Fatalf("no key in %v", m)
	}
	return key
}

// wantPending checks the KEY_PENDING_CONFIRMATION answer a pending key gets on any route.
func wantPending(t *testing.T, r resp, masked string) {
	t.Helper()
	if r.status != http.StatusForbidden || r.errCode(t) != "KEY_PENDING_CONFIRMATION" {
		t.Fatalf("want 403 KEY_PENDING_CONFIRMATION, got %d %s", r.status, r.body)
	}
	f, _ := r.errBody(t)["fields"].(map[string]any)
	if f["email_masked"] != masked || f["scopes"] != agentScopes || !rfc3339Re.MatchString(f["expires_at"].(string)) {
		t.Fatalf("pending details: %v", f)
	}
}

func agentLinkToken(t *testing.T, cm *captureMailer, n int) string {
	t.Helper()
	cm.waitMail(t, n)
	m := cm.last(t, "agent_access")
	if !strings.Contains(m.Text, "http://web.test/agent-access#token=") {
		t.Fatalf("agent mail link: %q", m.Text)
	}
	return token(t, m)
}

func TestAgentSignupCreatesAnAccountAndAWorkingKey(t *testing.T) {
	cm := &captureMailer{}
	e := newEnv(t, envOpts{startWorker: true, mailer: cm, mutate: enforce})
	res := e.browser().do("POST", agentSignupPath, agentSignupBody("Agent.Owner@Example.com"))
	if res.status != http.StatusCreated {
		t.Fatalf("sign-up: %d %s", res.status, res.body)
	}
	body := res.json(t)
	if body["status"] != "pending_confirmation" || body["dangerous_policy"] != "trusted_connect" || body["email_masked"] != "a***@example.com" ||
		body["delivery"] != "smtp" || !strings.HasPrefix(body["api_key"].(string), body["key_prefix"].(string)) {
		t.Fatalf("sign-up body: %v", body)
	}
	agent := e.apiKeyClient(body["api_key"].(string))
	wantPending(t, agent.do("GET", "/api/v1/me", nil), "a***@example.com")
	wantPending(t, agent.do("GET", "/api/v1/posts", nil), "a***@example.com")

	tok := agentLinkToken(t, cm, 1)
	owner := e.browser()
	if p := owner.must("POST", agentSignupPath+"/preview", map[string]any{"token": tok}, 200); p["account_exists"] != false ||
		p["email"] != "agent.owner@example.com" || p["agent_name"] != "Claude Desktop" || p["dangerous_policy"] != "trusted_connect" {
		t.Fatalf("preview: %v", p)
	}
	confirm := agentSignupPath + "/confirm"
	if r := owner.do("POST", confirm, map[string]any{"token": tok}); r.status != 400 || r.errBody(t)["fields"].(map[string]any)["accept_terms"] == nil {
		t.Fatalf("terms refused: %d %s", r.status, r.body)
	}
	// A body a cross-site page could send without a preflight is refused, and the link survives it.
	if r := owner.doWith("POST", confirm, map[string]any{"token": tok, "accept_terms": true}, map[string]string{"Content-Type": "text/plain"}); r.status != 400 {
		t.Fatalf("text/plain confirm: %d %s", r.status, r.body)
	}
	me := owner.must("POST", confirm, map[string]any{"token": tok, "accept_terms": true, "display_name": "Owner"}, 200)
	owner.csrf, _ = me["csrf_token"].(string)
	if me["account_created"] != true || me["auth_type"] != "session" || owner.csrf == "" || me["user"].(map[string]any)["email_verified"] != true {
		t.Fatalf("confirm: %v", me)
	}
	if r := owner.do("POST", confirm, map[string]any{"token": tok, "accept_terms": true}); r.status != 400 {
		t.Fatalf("reused link: %d %s", r.status, r.body)
	}
	// The fresh session lets the password-less owner set a password.
	owner.must("POST", "/api/v1/auth/password/set", map[string]any{"new_password": "a brand new password"}, 204)

	agentMe := agent.must("GET", "/api/v1/me", nil, 200)
	if agentMe["auth_type"] != "api_key" || agentMe["id"] != me["id"] || len(agentMe["scopes"].([]any)) != 7 {
		t.Fatalf("agent /me: %v", agentMe)
	}
	acc := agent.must("POST", tokenPath, tokenBody(goodKey), http.StatusCreated)["id"].(string) // no approval for connect
	postID := agent.must("POST", "/api/v1/posts", map[string]any{"title": "Agent post", "content": "from the agent", "social_account_ids": []string{acc}}, 201)["id"].(string)
	sched := "/api/v1/posts/" + postID + "/schedule"
	needApproval(t, agent.do("POST", sched, map[string]any{"scheduled_at": fmtTime(time.Now().Add(time.Minute))}), "post.schedule_soon")
	agent.must("POST", sched, map[string]any{"scheduled_at": fmtTime(time.Now().Add(10 * time.Minute))}, 200)
	if r := agent.do("POST", "/api/v1/posts/"+postID+"/publish", nil); r.status != 403 || r.errCode(t) != "INSUFFICIENT_SCOPE" {
		t.Fatalf("publish now: %d %s", r.status, r.body)
	}

	conns := owner.must("GET", "/api/v1/developer/mcp-connections", nil, 200)["items"].([]any)
	keys := owner.must("GET", "/api/v1/developer/api-keys", nil, 200)["items"].([]any)
	if len(conns) != 1 || conns[0].(map[string]any)["name"] != "Claude Desktop" || len(keys) != 1 ||
		keys[0].(map[string]any)["dangerous_policy"] != "trusted_connect" {
		t.Fatalf("developer page: %v / %v", conns, keys)
	}
	acts := auditActions(t, owner)
	for _, a := range []string{"agent_signup.confirmed", "api_key.created", "user.registered"} {
		if acts[a] != 1 {
			t.Fatalf("audit %s x%d: %v", a, acts[a], acts)
		}
	}
	if blob := string(mustMarshal(owner.must("GET", "/api/v1/audit-logs?limit=100", nil, 200))); strings.Contains(blob, tok) ||
		strings.Contains(blob, body["api_key"].(string)) {
		t.Fatal("the audit log holds the link token or the key")
	}
	// Revoking the connection under Developer stops the key.
	owner.must("DELETE", "/api/v1/developer/mcp-connections/"+conns[0].(map[string]any)["id"].(string), nil, 204)
	if r := agent.do("GET", "/api/v1/me", nil); r.status != 401 {
		t.Fatalf("revoked agent key: %d", r.status)
	}
}

func TestAgentSignupForAnExistingAccountNeedsItsSession(t *testing.T) {
	cm := &captureMailer{}
	e := newEnv(t, envOpts{startWorker: true, mailer: cm})
	owner := e.browser()
	owner.register("existing@example.com")
	other := e.browser()
	other.register("other@example.com")
	cm.waitMail(t, 2)
	agent := e.apiKeyClient(signUpAgent(t, e, "existing@example.com"))
	tok := agentLinkToken(t, cm, 3)
	if m := cm.last(t, "agent_access"); !strings.Contains(m.Text, "You will be asked to sign in") || m.To != "existing@example.com" {
		t.Fatalf("existing-account mail: %s", m.Text)
	}
	confirm := agentSignupPath + "/confirm"
	body := map[string]any{"token": tok}
	if r := e.browser().do("POST", confirm, body); r.status != 401 || r.errCode(t) != "UNAUTHENTICATED" {
		t.Fatalf("signed out: %d %s", r.status, r.body)
	}
	if r := other.do("POST", confirm, body); r.status != 403 || r.errCode(t) != "FORBIDDEN" {
		t.Fatalf("another user's session: %d %s", r.status, r.body)
	}
	wantPending(t, agent.do("GET", "/api/v1/me", nil), "e***@example.com")
	csrf := owner.csrf
	owner.csrf = ""
	if r := owner.do("POST", confirm, body); r.status != 403 {
		t.Fatalf("confirm without CSRF: %d %s", r.status, r.body)
	}
	owner.csrf = csrf
	r := owner.do("POST", confirm, body)
	if r.status != 200 || r.json(t)["account_created"] != false || len(r.header.Values("Set-Cookie")) != 0 {
		t.Fatalf("own session: %d %s %v", r.status, r.body, r.header.Values("Set-Cookie"))
	}
	if me := agent.must("GET", "/api/v1/me", nil, 200); me["email"] != "existing@example.com" {
		t.Fatalf("agent belongs to the wrong account: %v", me)
	}
}

func TestAgentSignupAnswersTheSameForEveryAddress(t *testing.T) {
	cm := &captureMailer{}
	e := newEnv(t, envOpts{startWorker: true, mailer: cm})
	e.browser().register("known@example.com")
	cm.waitMail(t, 1)
	shape := func(email string) (int, []string) {
		r := e.browser().do("POST", agentSignupPath, agentSignupBody(email))
		var keys []string
		for k, v := range r.json(t) {
			keys = append(keys, k+":"+typeName(v))
		}
		sort.Strings(keys)
		return r.status, keys
	}
	ks, kk := shape("known@example.com")
	us, uk := shape("unknown@example.com")
	if ks != 201 || us != 201 || !slices.Equal(kk, uk) {
		t.Fatalf("known %d %v vs unknown %d %v", ks, kk, us, uk)
	}
	// Both get a mail (new-account and existing-account variants); a malformed address is a 400 for anyone.
	cm.waitMail(t, 3)
	if r := e.browser().do("POST", agentSignupPath, map[string]any{"email": "nope", "agent_name": "x://y"}); r.status != 400 {
		t.Fatalf("invalid: %d %s", r.status, r.body)
	} else if f := r.errBody(t)["fields"].(map[string]any); f["email"] == nil || f["agent_name"] == nil || f["accept_terms"] == nil {
		t.Fatalf("fields: %v", f)
	}
}

func typeName(v any) string {
	switch v.(type) {
	case string:
		return "string"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	case bool:
		return "bool"
	case float64:
		return "number"
	}
	return "null"
}

func TestDeclinedAgentSignupKeyNeverWorks(t *testing.T) {
	cm := &captureMailer{}
	e := newEnv(t, envOpts{startWorker: true, mailer: cm})
	agent := e.apiKeyClient(signUpAgent(t, e, "decline@example.com"))
	tok := agentLinkToken(t, cm, 1)
	e.browser().must("POST", agentSignupPath+"/decline", map[string]any{"token": tok}, 204)
	if r := agent.do("GET", "/api/v1/me", nil); r.status != 401 {
		t.Fatalf("declined key: %d %s", r.status, r.body)
	}
	if r := e.browser().do("POST", agentSignupPath+"/preview", map[string]any{"token": tok}); r.status != 400 {
		t.Fatalf("declined link: %d %s", r.status, r.body)
	}
	if n := e.count(`SELECT count(*) FROM users WHERE email = 'decline@example.com'`); n != 0 {
		t.Fatalf("a declined sign-up created a user")
	}
}

func TestAgentSignupIsRateLimitedPerVouchedClient(t *testing.T) {
	const secret = "0123456789abcdef0123456789abcdef"
	e := newEnv(t, envOpts{realMailLimit: true, mutate: func(c *config.Config) { c.GatewaySecret = secret }})
	c := e.browser()
	send := func(gateway, ip string, i int) int {
		return c.doWith("POST", agentSignupPath, agentSignupBody("rl"+strconv.Itoa(i)+"@example.com"),
			map[string]string{"X-SocialOS-Gateway": gateway, "X-SocialOS-Client-IP": ip}).status
	}
	var vouched []int
	for i := 0; i < 4; i++ {
		vouched = append(vouched, send(secret, "198.51.100.7", i))
	}
	if !slices.Equal(vouched, []int{201, 201, 201, 429}) {
		t.Fatalf("one vouched client: %v", vouched)
	}
	if s := send(secret, "198.51.100.8", 9); s != 201 {
		t.Fatalf("another vouched client has its own bucket: %d", s)
	}
	// Forged gateway headers all land in the caller's own bucket.
	var forged []int
	for i := 0; i < 4; i++ {
		forged = append(forged, send("forged", "203.0.113."+strconv.Itoa(i+1), 20+i))
	}
	if !slices.Equal(forged, []int{201, 201, 201, 429}) {
		t.Fatalf("forged gateway header: %v", forged)
	}
}

// The daily cap holds when many requests for one address are processed at the same time (several workers, a burst
// from many clients): the count and the claim of a mail slot are one atomic step.
func TestAgentSignupMailCapHoldsUnderConcurrency(t *testing.T) {
	e := newEnv(t, envOpts{}) // no worker: the test runs the worker side itself, all at once
	const n = 16
	ids := make([]uuid.UUID, 0, n)
	for i := 0; i < n; i++ {
		signUpAgent(t, e, "burst@example.com")
	}
	rows, err := e.app.DB.Pool.Query(context.Background(), `SELECT id FROM agent_signups WHERE email = 'burst@example.com'`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, id := range ids {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if err := e.app.Services.Auth.ProcessAgentSignup(context.Background(), id); err != nil {
				t.Error(err)
			}
		}()
	}
	close(start)
	wg.Wait()
	if got := e.count(`SELECT count(*) FROM agent_signups WHERE email = 'burst@example.com' AND token_hash IS NOT NULL`); got != 5 {
		t.Fatalf("mailed %d requests for one address, the cap is 5", got)
	}
}
