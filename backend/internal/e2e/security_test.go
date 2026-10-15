package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/socialos/backend/internal/config"
	"github.com/socialos/backend/internal/domain/errs"
	"github.com/socialos/backend/internal/domain/identity"
	"github.com/socialos/backend/internal/domain/post"
	"github.com/socialos/backend/internal/domain/user"
	"github.com/socialos/backend/internal/infrastructure/postgres"
)

func TestAPIKeyScopes(t *testing.T) {
	e := newEnv(t, envOpts{})
	c := e.browser()
	c.register("keys@example.com")
	acc := c.connectMock()
	draft := c.must("POST", "/api/v1/posts", map[string]any{"content": "agent post", "social_account_ids": []string{acc}}, 201)
	postID := draft["id"].(string)

	raw := c.createKey("agent", "posts:read", "posts:write", "social:read")
	if !strings.HasPrefix(raw, "sk_live_") {
		t.Fatalf("bad key %q", raw)
	}
	k := e.apiKeyClient(raw)

	me := k.must("GET", "/api/v1/me", nil, 200)
	if me["auth_type"] != "api_key" || len(me["scopes"].([]any)) != 3 || me["csrf_token"] != nil {
		t.Fatalf("unexpected /me for key: %v", me)
	}
	k.must("GET", "/api/v1/posts", nil, 200)
	// API keys are CSRF-exempt and can write drafts with posts:write.
	k.must("POST", "/api/v1/posts", map[string]any{"content": "from agent", "social_account_ids": []string{acc}}, 201)

	for _, tc := range []struct{ method, path string }{
		{"POST", "/api/v1/posts/" + postID + "/publish"},
		{"DELETE", "/api/v1/posts/" + postID},
		{"DELETE", "/api/v1/social/accounts/" + acc},
		{"POST", "/api/v1/posts/" + postID + "/schedule"},
		{"GET", "/api/v1/analytics"},
	} {
		body := map[string]any{"scheduled_at": "2099-01-01T00:00:00Z"}
		r := k.do(tc.method, tc.path, body)
		if r.status != http.StatusForbidden || r.errCode(t) != "INSUFFICIENT_SCOPE" {
			t.Errorf("%s %s: want 403 INSUFFICIENT_SCOPE, got %d %s", tc.method, tc.path, r.status, r.body)
		}
	}
	// API keys can never manage API keys.
	r := k.do("POST", "/api/v1/developer/api-keys", map[string]any{"name": "escalate", "scopes": []string{"posts:publish"}})
	if r.status != http.StatusForbidden || r.errCode(t) != "FORBIDDEN" {
		t.Fatalf("key minted a key: %d %s", r.status, r.body)
	}
	// A key with posts:publish may publish.
	pub := e.apiKeyClient(c.createTrustedKey("publisher", "posts:publish", "posts:read")) // approvals have their own tests
	pub.must("POST", "/api/v1/posts/"+postID+"/publish", map[string]any{"confirm": true}, 202)

	// Every API-key request is audited with actor api_key.
	logs := c.must("GET", "/api/v1/audit-logs?limit=100", nil, 200)["items"].([]any)
	var keyEntries int
	for _, l := range logs {
		m := l.(map[string]any)
		if m["actor_type"] == "api_key" && m["action"] == "api_key.request" {
			keyEntries++
		}
	}
	if keyEntries < 8 {
		t.Fatalf("expected api key request audit entries, got %d", keyEntries)
	}
	usage := c.must("GET", "/api/v1/developer/usage", nil, 200)["items"].([]any)
	if len(usage) != 2 {
		t.Fatalf("usage: %v", usage)
	}

	// Revoked keys stop working immediately.
	keys := c.must("GET", "/api/v1/developer/api-keys", nil, 200)["items"].([]any)
	for _, kk := range keys {
		c.must("DELETE", "/api/v1/developer/api-keys/"+kk.(map[string]any)["id"].(string), nil, 204)
	}
	if r := k.do("GET", "/api/v1/posts", nil); r.status != http.StatusUnauthorized || r.errCode(t) != "UNAUTHENTICATED" {
		t.Fatalf("revoked key still works: %d", r.status)
	}
	if r := e.apiKeyClient("sk_live_notarealkey").do("GET", "/api/v1/me", nil); r.status != 401 {
		t.Fatalf("garbage key accepted: %d", r.status)
	}
}

func TestMCPConnection(t *testing.T) {
	e := newEnv(t, envOpts{})
	c := e.browser()
	c.register("mcp@example.com")
	m := c.must("POST", "/api/v1/developer/mcp-connections", map[string]any{"name": "Claude Desktop", "client_name": "claude"}, 201)
	raw := m["key"].(string)
	cfg := m["config"].(map[string]any)["mcpServers"].(map[string]any)["steerpost"].(map[string]any)
	if cfg["url"] != "http://mcp.test/mcp" || !strings.Contains(cfg["headers"].(map[string]any)["Authorization"].(string), raw) {
		t.Fatalf("bad config %v", cfg)
	}
	k := e.apiKeyClient(raw)
	scopes := k.must("GET", "/api/v1/me", nil, 200)["scopes"].([]any)
	for _, s := range scopes {
		if s == "posts:publish" || s == "posts:delete" || s == "social:disconnect" {
			t.Fatalf("default MCP scopes include dangerous %s", s)
		}
	}
	list := c.must("GET", "/api/v1/developer/mcp-connections", nil, 200)["items"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["last_seen_at"] == nil {
		t.Fatalf("connection not listed / last_seen not updated: %v", list)
	}
	c.must("DELETE", "/api/v1/developer/mcp-connections/"+list[0].(map[string]any)["id"].(string), nil, 204)
	if r := k.do("GET", "/api/v1/me", nil); r.status != 401 {
		t.Fatalf("revoked MCP key works: %d", r.status)
	}
}

func TestCSRFAndAuthErrors(t *testing.T) {
	e := newEnv(t, envOpts{})
	anon := e.browser()
	r := anon.do("GET", "/api/v1/posts", nil)
	if r.status != 401 || r.errCode(t) != "UNAUTHENTICATED" || r.json(t)["error"].(map[string]any)["request_id"] == "" {
		t.Fatalf("anon access: %d %s", r.status, r.body)
	}
	c := e.browser()
	c.register("csrf@example.com")
	token := c.csrf
	c.csrf = ""
	if r := c.do("POST", "/api/v1/posts", map[string]any{"content": "x"}); r.status != 403 || r.errCode(t) != "FORBIDDEN" {
		t.Fatalf("missing CSRF accepted: %d %s", r.status, r.body)
	}
	c.csrf = "wrong"
	if r := c.do("POST", "/api/v1/posts", map[string]any{"content": "x"}); r.status != 403 {
		t.Fatalf("wrong CSRF accepted: %d", r.status)
	}
	c.csrf = token
	c.must("POST", "/api/v1/posts", map[string]any{"content": "draft without accounts"}, 201)

	if r := c.do("POST", "/api/v1/auth/register", map[string]any{"email": "csrf@example.com", "password": "another long password", "accept_terms": true}); r.status != 409 {
		t.Fatalf("duplicate email: %d %s", r.status, r.body)
	}
	if r := anon.do("POST", "/api/v1/auth/login", map[string]any{"email": "csrf@example.com", "password": "wrong password!!"}); r.status != 401 {
		t.Fatalf("bad login: %d", r.status)
	}
	if r := anon.do("POST", "/api/v1/auth/register", map[string]any{"email": "bad", "password": "short", "accept_terms": true}); r.status != 400 || r.errCode(t) != "VALIDATION_ERROR" {
		t.Fatalf("validation: %d %s", r.status, r.body)
	}
	c.must("POST", "/api/v1/auth/logout", nil, 204)
	if r := c.do("GET", "/api/v1/me", nil); r.status != 401 {
		t.Fatalf("session survived logout: %d", r.status)
	}
	login := anon.must("POST", "/api/v1/auth/login", map[string]any{"email": "CSRF@example.com", "password": "correct horse battery"}, 200)
	if login["user"].(map[string]any)["email"] != "csrf@example.com" {
		t.Fatalf("login: %v", login)
	}
	if r := anon.do("GET", "/health", nil); r.status != 200 || r.header.Get("X-Request-ID") == "" {
		t.Fatalf("health: %d", r.status)
	}
	if r := anon.do("GET", "/ready", nil); r.status != 200 {
		t.Fatalf("ready: %d %s", r.status, r.body)
	}
	if r := anon.do("GET", "/metrics", nil); r.status != 200 || !strings.Contains(string(r.body), "socialos_http_requests_total") {
		t.Fatalf("metrics: %d", r.status)
	}
}

func TestTenantIsolationHTTP(t *testing.T) {
	e := newEnv(t, envOpts{})
	alice, bob := e.browser(), e.browser()
	alice.register("alice@tenant.test")
	bob.register("bob@tenant.test")
	acc := alice.connectMock()
	p := alice.must("POST", "/api/v1/posts", map[string]any{"content": "private", "social_account_ids": []string{acc}}, 201)
	id := p["id"].(string)

	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/v1/posts/" + id}, {"PATCH", "/api/v1/posts/" + id}, {"DELETE", "/api/v1/posts/" + id},
		{"POST", "/api/v1/posts/" + id + "/publish"}, {"GET", "/api/v1/social/accounts/" + acc}, {"DELETE", "/api/v1/social/accounts/" + acc},
	} {
		if r := bob.do(tc.method, tc.path, map[string]any{"content": "hijack"}); r.status != 404 {
			t.Errorf("bob %s %s: want 404 got %d %s", tc.method, tc.path, r.status, r.body)
		}
	}
	if r := bob.do("POST", "/api/v1/posts", map[string]any{"content": "x", "social_account_ids": []string{acc}}); r.status != 400 {
		t.Fatalf("bob used alice's account: %d %s", r.status, r.body)
	}
	if n := len(bob.must("GET", "/api/v1/posts", nil, 200)["items"].([]any)); n != 0 {
		t.Fatalf("bob sees %d posts", n)
	}
	if n := len(bob.must("GET", "/api/v1/social/accounts", nil, 200)["items"].([]any)); n != 0 {
		t.Fatalf("bob sees %d accounts", n)
	}
}

// A post target is only ever reached through its post, and every route is keyed by the post id. Bob must not be able
// to read, change or lock Alice's target by any of them, nor by presenting her target id where a post id is expected.
func TestTargetIsolation(t *testing.T) {
	e := newEnv(t, envOpts{})
	alice, bob := e.browser(), e.browser()
	aliceID := alice.register("alice@target.test")["user"].(map[string]any)["id"].(string)
	bobID := bob.register("bob@target.test")["user"].(map[string]any)["id"].(string)
	acc := alice.connectMock()
	p := alice.must("POST", "/api/v1/posts", map[string]any{"content": "alice only", "social_account_ids": []string{acc}}, 201)
	postID := p["id"].(string)
	targetID := p["targets"].([]any)[0].(map[string]any)["id"].(string)

	for _, id := range []string{postID, targetID} { // Alice's post id, and her target id passed as if it were a post id
		for _, tc := range []struct{ method, path string }{
			{"GET", "/api/v1/posts/" + id}, {"PATCH", "/api/v1/posts/" + id}, {"DELETE", "/api/v1/posts/" + id},
			{"GET", "/api/v1/posts/" + id + "/status"}, {"POST", "/api/v1/posts/" + id + "/publish"},
			{"POST", "/api/v1/posts/" + id + "/schedule"}, {"POST", "/api/v1/posts/" + id + "/unschedule"},
			{"POST", "/api/v1/posts/" + id + "/cancel"}, {"POST", "/api/v1/posts/" + id + "/retry"},
		} {
			// A valid body, so that a 404 can only come from the ownership check.
			body := map[string]any{"content": "hijack", "scheduled_at": time.Now().Add(48 * time.Hour).UTC().Format(time.RFC3339), "confirm": true}
			if r := bob.do(tc.method, tc.path, body); r.status != http.StatusNotFound {
				t.Errorf("bob %s %s: want 404 got %d %s", tc.method, tc.path, r.status, r.body)
			}
		}
	}
	// None of that touched Alice's post or its target.
	got := alice.must("GET", "/api/v1/posts/"+postID, nil, 200)
	gt := got["targets"].([]any)[0].(map[string]any)
	if got["status"] != "draft" || got["content"] != "alice only" || gt["id"] != targetID || gt["status"] != "pending" || gt["content"] != "alice only" {
		t.Fatalf("alice's post changed: %v", got)
	}
	if n := len(bob.must("GET", "/api/v1/posts", nil, 200)["items"].([]any)); n != 0 {
		t.Fatalf("bob lists %d posts", n)
	}

	// The same holds below the HTTP layer, for every repository method that takes a user.
	ctx, repo := context.Background(), postgres.NewPosts(e.app.DB)
	alicePost, aliceUser, bobUser := uuid.MustParse(postID), uuid.MustParse(aliceID), uuid.MustParse(bobID)
	tid := uuid.MustParse(targetID)
	if ts, err := repo.ListTargets(ctx, bobUser, alicePost); err != nil || len(ts) != 0 {
		t.Fatalf("bob lists alice's targets: %v %v", ts, err)
	}
	if as, err := repo.Attempts(ctx, bobUser, alicePost); err != nil || len(as) != 0 {
		t.Fatalf("bob lists alice's attempts: %v %v", as, err)
	}
	if err := repo.LockTargetWait(ctx, bobUser, tid); !errs.Is(err, errs.NotFound) {
		t.Fatalf("bob locked alice's target: %v", err)
	}
	if err := repo.LockTargetWait(ctx, aliceUser, tid); err != nil {
		t.Fatalf("the owner must be able to lock: %v", err)
	}
	ts, err := repo.ListTargets(ctx, aliceUser, alicePost)
	if err != nil || len(ts) != 1 {
		t.Fatalf("alice's targets: %v %v", ts, err)
	}
	forged := ts[0]
	forged.UserID, forged.Content, forged.Status = bobUser, "forged", post.TargetPublished
	if err := repo.UpdateTarget(ctx, &forged); !errs.Is(err, errs.NotFound) {
		t.Fatalf("bob updated alice's target: %v", err)
	}
	var content, status string
	if err := e.app.DB.Pool.QueryRow(ctx, `SELECT content, status FROM post_targets WHERE id = $1`, tid).Scan(&content, &status); err != nil ||
		content != "alice only" || status != "pending" {
		t.Fatalf("alice's target row: %q %q %v", content, status, err)
	}
}

// A linked sign-in identity belongs to one user. Bob must not list, touch or unlink Alice's, nor claim the same
// external account, and a password-less account must not be usable through the password login.
func TestIdentityIsolation(t *testing.T) {
	e := newEnv(t, envOpts{})
	alice, bob := e.browser(), e.browser()
	aliceID := uuid.MustParse(alice.register("alice@identity.test")["user"].(map[string]any)["id"].(string))
	bobID := uuid.MustParse(bob.register("bob@identity.test")["user"].(map[string]any)["id"].(string))

	ctx, repo := context.Background(), postgres.NewIdentities(e.app.DB)
	linkedAt := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	if err := repo.Create(ctx, &identity.Identity{UserID: aliceID, Provider: identity.GitHub, Subject: "1001", Email: "alice@identity.test", EmailVerified: true, LinkedAt: linkedAt}); err != nil {
		t.Fatal(err)
	}
	if ids, err := repo.ListByUser(ctx, bobID); err != nil || len(ids) != 0 {
		t.Fatalf("bob lists alice's identities: %v %v", ids, err)
	}
	if err := repo.Delete(ctx, bobID, identity.GitHub); !errs.Is(err, errs.NotFound) {
		t.Fatalf("bob unlinked alice's identity: %v", err)
	}
	if err := repo.TouchLogin(ctx, bobID, identity.GitHub, "bob@identity.test", true, linkedAt); !errs.Is(err, errs.NotFound) {
		t.Fatalf("bob touched alice's identity: %v", err)
	}
	if err := repo.Create(ctx, &identity.Identity{UserID: bobID, Provider: identity.GitHub, Subject: "1001", LinkedAt: linkedAt}); !errs.Is(err, errs.Conflict) {
		t.Fatalf("bob claimed alice's external account: %v", err)
	}
	got, err := repo.GetBySubject(ctx, identity.GitHub, "1001")
	if err != nil || got.UserID != aliceID || got.LastLoginAt != nil || got.Email != "alice@identity.test" {
		t.Fatalf("alice's identity changed: %+v %v", got, err)
	}

	// A user created without a password (a social sign-up) cannot sign in with any password, and the answer is the
	// same as for a wrong password.
	if err := postgres.NewUsers(e.app.DB).Create(ctx, &user.User{Email: "social@identity.test", Status: user.StatusActive}); err != nil {
		t.Fatal(err)
	}
	anon := e.browser()
	for _, pw := range []string{"", "correct horse battery"} {
		r := anon.do("POST", "/api/v1/auth/login", map[string]any{"email": "social@identity.test", "password": pw})
		w := anon.do("POST", "/api/v1/auth/login", map[string]any{"email": "alice@identity.test", "password": "wrong password!!"})
		if r.status != 401 || r.errCode(t) != "UNAUTHENTICATED" || w.status != 401 ||
			r.json(t)["error"].(map[string]any)["message"] != w.json(t)["error"].(map[string]any)["message"] {
			t.Fatalf("password-less login: %d %s (wrong password: %d %s)", r.status, r.body, w.status, w.body)
		}
	}
}

// Through the HTTP API: Bob sees none of Alice's sign-in methods, cannot unlink them, and cannot connect the external
// account Alice already owns (identity_in_use), whichever way he asks.
func TestSignInMethodsAreTenantScoped(t *testing.T) {
	r := newSocialRig(t, envOpts{})
	alice := r.githubSignUp(t, 9001, "alice", "alice@signin.test")
	bob := r.e.browser()
	bob.register("bob@signin.test")

	list := bob.must("GET", identitiesPath, nil, 200)
	if len(list["identities"].([]any)) != 0 || list["has_password"] != true {
		t.Fatalf("bob sees %v", list)
	}
	pw := map[string]any{"current_password": "correct horse battery"}
	bob.must("DELETE", identitiesPath+"/github", pw, 404)

	f := r.startLinkWith(t, bob, "github", pw)
	loc := r.callback(t, bob, "github", r.githubCode(f, 9001, "alice", "alice@signin.test"), f.state)
	if loc.Path != "/settings" || loc.Query().Get("error") != "identity_in_use" || loc.Query().Get("provider") != "github" {
		t.Fatalf("bob linking alice's account: %s", loc)
	}
	if got := providersOf(bob.must("GET", identitiesPath, nil, 200)); len(got) != 0 {
		t.Fatalf("bob now has %v", got)
	}
	if got := providersOf(alice.must("GET", identitiesPath, nil, 200)); len(got) != 1 || got[0] != "github" {
		t.Fatalf("alice's identity changed: %v", got)
	}
	// Alice's password-less account is untouched by Bob's session too.
	if n := r.countRows(`SELECT count(*) FROM users WHERE email = 'alice@signin.test' AND password_hash IS NULL`); n != 1 {
		t.Fatal("alice's account changed")
	}
}

func pngBytes(t *testing.T) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 4, 3))
	img.Set(1, 1, color.RGBA{255, 0, 0, 255})
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func (c *client) upload(name string, data []byte) resp {
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", name)
	_, _ = fw.Write(data)
	_ = mw.Close()
	req, _ := http.NewRequest("POST", c.e.srv.URL+"/api/v1/media", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	return c.send(req)
}

func TestMediaUploadAndPublish(t *testing.T) {
	e := newEnv(t, envOpts{startWorker: true})
	c := e.browser()
	c.register("media@example.com")
	acc := c.connectMock()

	r := c.upload("photo.png", pngBytes(t))
	if r.status != 201 {
		t.Fatalf("upload: %d %s", r.status, r.body)
	}
	m := r.json(t)
	if m["mime_type"] != "image/png" || m["width"].(float64) != 4 || m["height"].(float64) != 3 || len(m["sha256"].(string)) != 64 {
		t.Fatalf("media meta: %v", m)
	}
	// MIME is sniffed, not trusted from the filename.
	if r := c.upload("evil.png", []byte("#!/bin/sh\necho pwned\n")); r.status != 400 || r.errCode(t) != "VALIDATION_ERROR" {
		t.Fatalf("script accepted: %d %s", r.status, r.body)
	}
	if r := c.upload("fake.jpg", append([]byte{0xFF, 0xD8, 0xFF, 0xE0}, bytes.Repeat([]byte{0}, 100)...)); r.status != 400 {
		t.Fatalf("corrupt jpeg accepted: %d %s", r.status, r.body)
	}
	mid := m["id"].(string)
	got := c.must("GET", "/api/v1/media/"+mid, nil, 200)
	if !strings.HasPrefix(got["url"].(string), "memory://") {
		t.Fatalf("no url: %v", got)
	}
	p := c.must("POST", "/api/v1/posts", map[string]any{"content": "with image", "social_account_ids": []string{acc}, "media_ids": []string{mid}}, 201)
	if r := c.do("DELETE", "/api/v1/media/"+mid, nil); r.status != 409 {
		t.Fatalf("referenced media deleted: %d", r.status)
	}
	c.must("POST", "/api/v1/posts/"+p["id"].(string)+"/publish", nil, 202)
	c.waitStatus(p["id"].(string), "published", 15*time.Second)
}

// toolCall performs a request the way the MCP server forwards a tool call.
func (c *client) toolCall(tool, method, path, gateway, ip string) resp {
	c.e.t.Helper()
	req, err := http.NewRequest(method, c.e.srv.URL+path, nil)
	if err != nil {
		c.e.t.Fatal(err)
	}
	req.Header.Set("X-MCP-Tool", tool)
	if gateway != "" {
		req.Header.Set("X-SocialOS-Gateway", gateway)
		req.Header.Set("X-SocialOS-Client-IP", ip)
	}
	return c.send(req)
}

func TestMCPToolCallAuditIsTenantScoped(t *testing.T) {
	const secret = "e2e-gateway-secret-0123456789abcdef0123456789"
	e := newEnv(t, envOpts{mutate: func(c *config.Config) { c.GatewaySecret = secret }})
	alice, bob := e.browser(), e.browser()
	alice.register("alice-audit@tenant.test")
	bob.register("bob-audit@tenant.test")
	ak := e.apiKeyClient(alice.createKey("alice agent", "posts:read"))
	bk := e.apiKeyClient(bob.createKey("bob agent", "posts:read"))

	if r := ak.toolCall("list_posts", "GET", "/api/v1/posts?limit=5&q=private", secret, "203.0.113.50"); r.status != 200 {
		t.Fatalf("tool call: %d %s", r.status, r.body)
	}
	ak.toolCall("list_posts", "GET", "/api/v1/posts", "", "")                                                  // direct: header absent, IP is the peer
	ak.toolCall("list_posts", "GET", "/api/v1/posts", "wrong-secret-0123456789abcdef01234567", "203.0.113.99") // forged
	bk.toolCall("bobs_tool", "GET", "/api/v1/posts", secret, "203.0.113.77")

	rows := func(c *client, query string) []map[string]any {
		var out []map[string]any
		for _, it := range c.must("GET", "/api/v1/audit-logs?limit=100"+query, nil, 200)["items"].([]any) {
			out = append(out, it.(map[string]any))
		}
		return out
	}
	mine := rows(alice, "&action=mcp.tool_call")
	if len(mine) != 3 {
		t.Fatalf("alice should see her 3 tool calls, got %d", len(mine))
	}
	ips := map[string]bool{}
	for _, r := range mine {
		meta := r["metadata"].(map[string]any)
		if r["action"] != "mcp.tool_call" || meta["tool"] != "list_posts" || meta["route"] != "/api/v1/posts" || r["actor_type"] != "api_key" ||
			r["actor_label"] != "alice agent" || meta["status"] != float64(200) {
			t.Fatalf("row: %v", r)
		}
		ips[r["ip"].(string)] = true
		if strings.Contains(string(mustMarshal(r)), "private") || strings.Contains(string(mustMarshal(r)), "sk_live_") {
			t.Fatalf("request data or key leaked into the row: %v", r)
		}
	}
	if !ips["203.0.113.50"] || ips["203.0.113.99"] || ips["203.0.113.77"] || len(ips) != 2 {
		t.Fatalf("only the correctly authenticated gateway header may set the ip: %v", ips)
	}
	for _, r := range rows(bob, "") {
		if r["action"] == "mcp.tool_call" && r["actor_label"] != "bob agent" {
			t.Fatalf("bob sees another tenant's tool call: %v", r)
		}
	}
	theirs := rows(bob, "&action=mcp.tool_call")
	if len(theirs) != 1 || theirs[0]["metadata"].(map[string]any)["tool"] != "bobs_tool" {
		t.Fatalf("bob must see exactly his own tool call: %v", theirs)
	}
	if n := len(rows(alice, "&action=nonexistent.action")); n != 0 {
		t.Fatalf("filter must be exact, got %d rows", n)
	}
}

func mustMarshal(v any) []byte { b, _ := json.Marshal(v); return b }

// A mailed token belongs to the user it was issued for, to one purpose, and works once.
func TestEmailTokensAreBoundToOwnerAndPurpose(t *testing.T) {
	cm := &captureMailer{}
	e := newEnv(t, envOpts{startWorker: true, mailer: cm, mutate: enforce})
	a, b := e.browser(), e.browser()
	a.register("tok-a@example.com")
	b.register("tok-b@example.com")
	cm.waitMail(t, 2)
	var verifyA string
	for _, m := range cm.got {
		if m.To == "tok-a@example.com" {
			verifyA = token(t, m)
		}
	}
	e.browser().must("POST", "/api/v1/auth/password/forgot", map[string]any{"email": "tok-a@example.com"}, 202)
	cm.waitMail(t, 3)
	resetA := token(t, cm.last(t, "reset_password"))

	// Purposes do not mix.
	if r := b.do("POST", "/api/v1/auth/verify-email", map[string]any{"token": resetA}); r.status != 400 {
		t.Fatalf("reset token verified an email: %d %s", r.status, r.body)
	}
	if r := b.do("POST", "/api/v1/auth/password/reset", map[string]any{"token": verifyA, "password": "attacker chosen password"}); r.status != 400 {
		t.Fatalf("verification token reset a password: %d %s", r.status, r.body)
	}

	// B redeeming A's link verifies A's address, never B's, and never changes who B is.
	b.must("POST", "/api/v1/auth/verify-email", map[string]any{"token": verifyA}, 200)
	if got := userField(t, b.do("GET", "/api/v1/me", nil), "email_verified"); got != false {
		t.Fatalf("B was verified through A's token: %v", got)
	}
	if got := userField(t, a.do("GET", "/api/v1/me", nil), "email_verified"); got != true {
		t.Fatalf("A not verified: %v", got)
	}
	// Reuse is refused, for the same and for a different session.
	for _, c := range []*client{a, b, e.browser()} {
		if r := c.do("POST", "/api/v1/auth/verify-email", map[string]any{"token": verifyA}); r.status != 400 {
			t.Fatalf("reused token accepted: %d %s", r.status, r.body)
		}
	}
	// B stays gated and B's password is untouched by A's reset flow.
	if r := b.do("POST", "/api/v1/developer/api-keys", map[string]any{"name": "k", "scopes": []string{"posts:read"}}); r.status != 403 {
		t.Fatalf("B ungated: %d", r.status)
	}
	e.browser().must("POST", "/api/v1/auth/password/reset", map[string]any{"token": resetA, "password": "a brand new password"}, 204)
	if r := e.browser().login("tok-b@example.com", goodPassword); r.status != 200 {
		t.Fatalf("B's password changed by A's reset: %d", r.status)
	}
	if r := e.browser().login("tok-a@example.com", "a brand new password"); r.status != 200 {
		t.Fatalf("A's reset failed: %d", r.status)
	}
}

// A token-connected account is as private as any other: user B cannot see, use
// or remove it, and the credential never appears in any response, for anyone.
func TestTokenAccountIsolation(t *testing.T) {
	e := newEnv(t, envOpts{})
	alice, bob := e.browser(), e.browser()
	alice.register("alice@token.test")
	bob.register("bob@token.test")
	const secret = "mt_isolation_secret_0123456789" // gitleaks:allow (fake test value)
	acc := alice.connectToken(secret)

	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/v1/social/accounts/" + acc}, {"DELETE", "/api/v1/social/accounts/" + acc},
	} {
		if r := bob.do(tc.method, tc.path, nil); r.status != 404 {
			t.Errorf("bob %s %s: want 404, got %d %s", tc.method, tc.path, r.status, r.body)
		}
	}
	if r := bob.do("POST", "/api/v1/posts", map[string]any{"title": "T", "content": "x", "social_account_ids": []string{acc}}); r.status != 400 {
		t.Fatalf("bob used alice's token account: %d %s", r.status, r.body)
	}
	if n := len(bob.must("GET", "/api/v1/social/accounts", nil, 200)["items"].([]any)); n != 0 {
		t.Fatalf("bob sees %d accounts", n)
	}
	// Connecting the same credential as bob creates bob's own account, never alice's.
	bobAcc := bob.connectToken(secret)
	if bobAcc == acc {
		t.Fatal("two users share one account row")
	}
	if got := alice.must("GET", "/api/v1/social/accounts/"+acc, nil, 200); got["status"] != "active" {
		t.Fatalf("alice's account changed: %v", got)
	}

	// The secret is in no response of either user: accounts, audit trail, providers.
	for _, c := range []*client{alice, bob} {
		for _, path := range []string{"/api/v1/social/accounts", "/api/v1/audit-logs?limit=100", "/api/v1/social/providers", "/api/v1/me", "/api/v1/dashboard/summary"} {
			if r := c.do("GET", path, nil); strings.Contains(string(r.body), secret) {
				t.Errorf("GET %s leaks the credential", path)
			}
		}
	}
	// And it is not stored in clear text.
	var plain int
	if err := e.app.DB.Pool.QueryRow(context.Background(),
		`SELECT count(*) FROM oauth_credentials WHERE access_token_enc LIKE '%' || $1 || '%'`, secret).Scan(&plain); err != nil || plain != 0 {
		t.Fatalf("clear-text credentials in the database: %d %v", plain, err)
	}
}

// An approval is tenant data: another user can neither see nor decide it, and their key cannot spend it.
func TestApprovalsAreTenantScoped(t *testing.T) {
	e := newEnv(t, envOpts{})
	alice, bob := e.browser(), e.browser()
	alice.register("alice-approvals@tenant.test")
	bob.register("bob-approvals@tenant.test")
	accA, accB := alice.connectMock(), bob.connectMock()
	scopes := []string{"posts:read", "posts:write", "posts:publish"}
	ak, bk := e.apiKeyClient(alice.createKey("alice agent", scopes...)), e.apiKeyClient(bob.createKey("bob agent", scopes...))
	postA := alice.must("POST", "/api/v1/posts", map[string]any{"content": "alice", "social_account_ids": []string{accA}}, 201)["id"].(string)
	postB := bob.must("POST", "/api/v1/posts", map[string]any{"content": "bob", "social_account_ids": []string{accB}}, 201)["id"].(string)

	id := needApproval(t, ak.do("POST", "/api/v1/posts/"+postA+"/publish", nil), "post.publish")
	alice.must("POST", "/api/v1/approvals/"+id+"/approve", nil, 200)

	if n := len(bob.must("GET", "/api/v1/approvals?status=all", nil, 200)["items"].([]any)); n != 0 {
		t.Fatalf("bob lists %d of alice's approvals", n)
	}
	for _, path := range []string{"", "/approve", "/deny"} {
		method := "POST"
		if path == "" {
			method = "GET"
		}
		if r := bob.do(method, "/api/v1/approvals/"+id+path, nil); r.status != http.StatusNotFound {
			t.Errorf("bob %s approvals/{id}%s: want 404, got %d %s", method, path, r.status, r.body)
		}
	}
	// Bob's key presenting alice's approved id gets a fresh request on his own post and 404 on hers, never a publish.
	if bobsID := needApproval(t, bk.doWith("POST", "/api/v1/posts/"+postB+"/publish", nil, withApproval(id)), "post.publish"); bobsID == id {
		t.Fatal("bob was handed alice's approval")
	}
	if r := bk.doWith("POST", "/api/v1/posts/"+postA+"/publish", nil, withApproval(id)); r.status != http.StatusNotFound {
		t.Fatalf("bob's key on alice's post: want 404, got %d %s", r.status, r.body)
	}
	if st := bob.must("GET", "/api/v1/posts/"+postB+"/status", nil, 200); st["status"] != "draft" {
		t.Fatalf("bob's post was published with alice's approval: %v", st)
	}
	if st := alice.must("GET", "/api/v1/approvals/"+id, nil, 200); st["status"] != "approved" {
		t.Fatalf("alice's approval was spent by bob: %v", st)
	}
	// Alice's own key can still use it.
	if r := ak.doWith("POST", "/api/v1/posts/"+postA+"/publish", nil, withApproval(id)); r.status != http.StatusAccepted {
		t.Fatalf("alice's approved publish: %d %s", r.status, r.body)
	}
}

// Quotas are per user: one user's usage never counts against, or shows in, another user's.
func TestQuotaIsPerUser(t *testing.T) {
	e := newEnv(t, withQuota(config.QuotaConfig{QuotaAccounts: 1, QuotaPostsPerMonth: 1, QuotaMediaMB: 1, QuotaAgentRPM: -1}))
	alice, bob := e.browser(), e.browser()
	alice.register("alice@quota.test")
	bob.register("bob@quota.test")
	alice.connectToken(tokenKey(1))
	if r := alice.upload("a.png", noisePNG(t, 1)); r.status != 201 {
		t.Fatalf("alice upload: %d %s", r.status, r.body)
	}
	// Alice is full; Bob still has everything.
	requireQuotaExceeded(t, alice.do("POST", tokenPath, tokenBody(tokenKey(2))), "connected_accounts")
	bob.connectToken(tokenKey(1))
	// The same provider identity connected by both is two rows and two slots.
	if used, _ := usageOf(t, bob, "connected_accounts"); used != 1 {
		t.Fatalf("bob sees %v accounts used", used)
	}
	if used, _ := usageOf(t, bob, "media_bytes"); used != 0 {
		t.Fatalf("bob's storage includes alice's files: %v", used)
	}
	if r := bob.upload("b.png", noisePNG(t, 2)); r.status != 201 {
		t.Fatalf("bob upload: %d %s", r.status, r.body)
	}
	if used, _ := usageOf(t, alice, "media_bytes"); used == 0 {
		t.Fatal("alice's storage is empty")
	}
	// Alice's agent key sees Alice's usage, not Bob's.
	k := e.apiKeyClient(alice.createKey("r", "analytics:read"))
	if q := k.must("GET", "/api/v1/account/usage", nil, 200)["quotas"].(map[string]any)["connected_accounts"].(map[string]any); q["used"].(float64) != 1 {
		t.Fatalf("key usage: %v", q)
	}
}

// User B can neither see nor download user A's export, and A's archive holds only A's rows.
func TestExportIsTenantScoped(t *testing.T) {
	e := newEnv(t, envOpts{startWorker: true})
	alice, bob := e.browser(), e.browser()
	alice.register("alice@export.test")
	bob.register("bob@export.test")
	alice.connectMock()
	bob.connectMock()
	alice.must("POST", "/api/v1/posts", map[string]any{"content": "alice secret plan", "social_account_ids": []string{alice.accounts()[0]["id"].(string)}}, 201)
	bob.must("POST", "/api/v1/posts", map[string]any{"content": "bob secret plan", "social_account_ids": []string{bob.accounts()[0]["id"].(string)}}, 201)

	id := alice.must("POST", "/api/v1/account/exports", nil, 202)["id"].(string)
	alice.waitExport(id, "ready")

	if r := bob.do("GET", "/api/v1/account/exports/"+id, nil); r.status != http.StatusNotFound {
		t.Fatalf("bob fetched alice's export: %d %s", r.status, r.body)
	}
	if items := bob.must("GET", "/api/v1/account/exports", nil, 200)["items"].([]any); len(items) != 0 {
		t.Fatalf("bob lists alice's exports: %v", items)
	}
	// Bob's own export is a separate row and archive, and holds none of Alice's data.
	bid := bob.must("POST", "/api/v1/account/exports", nil, 202)["id"].(string)
	bob.waitExport(bid, "ready")
	if r := alice.do("GET", "/api/v1/account/exports/"+bid, nil); r.status != http.StatusNotFound {
		t.Fatalf("alice fetched bob's export: %d", r.status)
	}
	var aliceID, bobID string
	row := e.app.DB.Pool.QueryRow(context.Background(), `SELECT id::text FROM users WHERE email = 'alice@export.test'`)
	_ = row.Scan(&aliceID)
	row = e.app.DB.Pool.QueryRow(context.Background(), `SELECT id::text FROM users WHERE email = 'bob@export.test'`)
	_ = row.Scan(&bobID)
	a, b := e.zipOf(aliceID, id), e.zipOf(bobID, bid)
	if bytes.Contains(a["posts.json"], []byte("bob secret plan")) || !bytes.Contains(a["posts.json"], []byte("alice secret plan")) {
		t.Fatalf("alice's posts.json: %s", a["posts.json"])
	}
	for _, name := range []string{"posts.json", "audit_logs.json", "profile.json", "social_accounts.json"} {
		if bytes.Contains(b[name], []byte("alice")) {
			t.Fatalf("bob's %s mentions alice: %s", name, b[name])
		}
	}
}

// User B can only ever delete B: the request has no user id, the password and typed email must be B's own, and a
// purge of B leaves A's data alone.
func TestAccountDeletionIsTenantScoped(t *testing.T) {
	e, clk := deletionEnv(t, &captureMailer{})
	alice, bob := e.browser(), e.browser()
	alice.register("alice@del-scope.test")
	bobMe := bob.register("bob@del-scope.test")
	bobID := bobMe["user"].(map[string]any)["id"].(string)
	alice.connectMock()
	bob.connectMock()

	// Bob cannot name Alice: her email as confirmation is refused, and nothing is scheduled for either of them.
	if r := bob.deleteAccount("correct horse battery", "alice@del-scope.test"); r.status != 400 {
		t.Fatalf("bob confirmed with alice's email: %d %s", r.status, r.body)
	}
	if n := e.count(`SELECT count(*) FROM users WHERE deletion_scheduled_at IS NOT NULL`); n != 0 {
		t.Fatalf("%d accounts scheduled after refused requests", n)
	}
	// Bob cannot cancel Alice's deletion: cancel acts on the session user only.
	if r := alice.deleteAccount("correct horse battery", "alice@del-scope.test"); r.status != 202 {
		t.Fatalf("alice's request: %d %s", r.status, r.body)
	}
	if r := bob.do("POST", "/api/v1/account/delete/cancel", nil); r.status != http.StatusConflict {
		t.Fatalf("bob cancelling: %d %s", r.status, r.body)
	}
	if n := e.count(`SELECT count(*) FROM users WHERE deletion_scheduled_at IS NOT NULL`); n != 1 {
		t.Fatalf("bob's cancel changed the schedule: %d scheduled", n)
	}
	// Purging Alice leaves Bob's account, sessions and data.
	clk.set(clk.Now().Add(8 * 24 * time.Hour))
	if n, _ := e.app.Services.Deletion.Sweep(context.Background()); n != 1 {
		t.Fatalf("sweep queued %d", n)
	}
	deadline := time.Now().Add(20 * time.Second)
	for e.count(`SELECT count(*) FROM users`) != 1 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	bob.must("GET", "/api/v1/me", nil, 200)
	if n := e.count(`SELECT count(*) FROM social_accounts WHERE user_id = $1`, bobID); n != 1 {
		t.Fatalf("bob has %d accounts", n)
	}
}

// A key from a confirmed agent sign-up is an ordinary key of its owner: it never reaches another tenant's data, and a
// pending one reaches nothing at all.
func TestAgentSignupKeyIsTenantScoped(t *testing.T) {
	cm := &captureMailer{}
	e := newEnv(t, envOpts{startWorker: true, mailer: cm})
	a := e.browser()
	a.register("tenant-a@example.com")
	accA := a.connectMock()
	postA := a.must("POST", "/api/v1/posts", map[string]any{"content": "A's draft", "social_account_ids": []string{accA}}, 201)["id"].(string)
	cm.waitMail(t, 1)

	agentB := e.apiKeyClient(signUpAgent(t, e, "tenant-b@example.com"))
	for _, path := range []string{"/api/v1/posts/" + postA, "/api/v1/social/accounts/" + accA} {
		wantPending(t, agentB.do("GET", path, nil), "t***@example.com")
	}
	e.browser().must("POST", agentSignupPath+"/confirm", map[string]any{"token": agentLinkToken(t, cm, 2), "accept_terms": true}, 200)

	for _, tc := range []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/v1/posts/" + postA, nil},
		{"PATCH", "/api/v1/posts/" + postA, map[string]any{"content": "hijacked"}},
		{"POST", "/api/v1/posts/" + postA + "/schedule", map[string]any{"scheduled_at": fmtTime(time.Now().Add(time.Hour))}},
		{"GET", "/api/v1/social/accounts/" + accA, nil},
	} {
		if r := agentB.do(tc.method, tc.path, tc.body); r.status != http.StatusNotFound {
			t.Errorf("%s %s with B's agent key: %d %s", tc.method, tc.path, r.status, r.body)
		}
	}
	if items := agentB.must("GET", "/api/v1/posts", nil, 200)["items"].([]any); len(items) != 0 {
		t.Fatalf("B's agent lists A's posts: %v", items)
	}
	if p := a.must("GET", "/api/v1/posts/"+postA, nil, 200); p["content"] != "A's draft" {
		t.Fatalf("A's post changed: %v", p)
	}
}
