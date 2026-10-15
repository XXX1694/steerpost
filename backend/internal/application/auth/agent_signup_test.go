package auth

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/socialos/backend/internal/domain/actor"
	"github.com/socialos/backend/internal/domain/agentsignup"
	"github.com/socialos/backend/internal/domain/apikey"
	"github.com/socialos/backend/internal/domain/approval"
	"github.com/socialos/backend/internal/domain/audit"
	"github.com/socialos/backend/internal/domain/errs"
	"github.com/socialos/backend/internal/domain/user"
	"github.com/socialos/backend/internal/infrastructure/crypto"
)

func validSignup(email string) StartAgentSignupInput {
	return StartAgentSignupInput{Email: email, AgentName: "Claude Desktop", AcceptTerms: true}
}

// signUp runs the HTTP side and then the worker side for whatever it queued; it returns the agent's answer.
func (r *rig) signUp(t *testing.T, email string) *AgentSignupStarted {
	t.Helper()
	got, err := r.svc.StartAgentSignup(ctx, validSignup(email))
	if err != nil {
		t.Fatal(err)
	}
	queued := r.agents.queue.ids
	r.agents.queue.ids = nil
	for _, id := range queued {
		if err := r.svc.ProcessAgentSignup(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	return got
}

func (r *rig) lastAgentToken(t *testing.T) string {
	t.Helper()
	m := r.mail.got[len(r.mail.got)-1]
	if m.Template != "agent_access" {
		t.Fatalf("last mail is %s", m.Template)
	}
	return tokenFrom(t, m)
}

func sessionOf(u *user.User) actor.Actor {
	return actor.Actor{UserID: u.ID, Type: actor.TypeUser, SessionID: uuid.New()}
}

func TestAgentSignupRequestNeverLooksUpTheAddress(t *testing.T) {
	r := newRig(t, true)
	known, err := r.svc.StartAgentSignup(ctx, validSignup("ann@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	unknown, err := r.svc.StartAgentSignup(ctx, validSignup("nobody@example.com"))
	if err != nil {
		t.Fatal(err)
	}
	if r.users.byEmailCalls != 0 {
		t.Fatalf("the request path looked the address up %d times", r.users.byEmailCalls)
	}
	if len(r.agents.queue.ids) != 2 || len(r.mail.got) != 0 {
		t.Fatalf("both requests must only be queued: %v, %d mails", r.agents.queue.ids, len(r.mail.got))
	}
	for _, got := range []*AgentSignupStarted{known, unknown} {
		if !crypto.LooksLikeAPIKey(got.RawKey) || !strings.HasPrefix(got.RawKey, got.KeyPrefix) ||
			got.DangerousPolicy != approval.PolicyTrustedConnect || !got.ExpiresAt.Equal(r.clock.now.Add(agentsignup.TTL)) {
			t.Fatalf("answer: %+v", got)
		}
	}
	if known.EmailMasked != "a***@example.com" || unknown.EmailMasked != "n***@example.com" {
		t.Fatalf("masked: %q %q", known.EmailMasked, unknown.EmailMasked)
	}
	// Only the key's hash is stored.
	for _, row := range r.agents.signups.rows {
		if row.KeyHash == known.RawKey || row.KeyHash == unknown.RawKey || len(row.KeyHash) != 64 || row.TokenHash != "" {
			t.Fatalf("stored row: %+v", row)
		}
	}
}

func TestAgentSignupReportsEveryInvalidField(t *testing.T) {
	r := newRig(t, true)
	_, err := r.svc.StartAgentSignup(ctx, StartAgentSignupInput{Email: "not-an-email", AgentName: "https://evil.test"})
	e, ok := errs.As(err)
	if !ok || e.Code != errs.Validation || e.Fields["email"] == "" || e.Fields["agent_name"] == "" || e.Fields["accept_terms"] == "" {
		t.Fatalf("want all three fields, got %v", err)
	}
	if len(r.agents.signups.rows) != 0 || len(r.agents.queue.ids) != 0 {
		t.Fatal("an invalid request was stored")
	}
}

func TestAgentSignupMailVariants(t *testing.T) {
	r := newRig(t, true)
	r.signUp(t, "new@example.com")
	r.signUp(t, "ann@example.com")
	if len(r.mail.got) != 2 {
		t.Fatalf("mails: %d", len(r.mail.got))
	}
	created, existing := r.mail.got[0], r.mail.got[1]
	if !strings.Contains(created.Text, "a new Steerpost account") || strings.Contains(created.Text, "asked to sign in") {
		t.Fatalf("new-account variant: %s", created.Text)
	}
	if !strings.Contains(existing.Text, "You will be asked to sign in") || existing.To != "ann@example.com" {
		t.Fatalf("existing-account variant: %s", existing.Text)
	}
	for _, m := range r.mail.got {
		if !strings.Contains(m.Text, "https://app.example/agent-access#token=") || !strings.Contains(m.Text, "Claude Desktop") {
			t.Fatalf("link or name missing: %s", m.Text)
		}
		if _, err := uuid.Parse(m.TokenID); err != nil || r.agents.signups.rows[uuid.MustParse(m.TokenID)] == nil {
			t.Fatalf("the mail must carry the request id: %q", m.TokenID)
		}
	}
}

func TestAgentSignupMailsNobodyForUnusableAccountsOrOverTheCap(t *testing.T) {
	r := newRig(t, true)
	r.u.Status = user.StatusDisabled
	r.signUp(t, "ann@example.com")
	r.u.Status = user.StatusActive
	at := r.clock.now
	r.u.DeletionScheduledAt = &at
	r.signUp(t, "ann@example.com")
	if len(r.mail.got) != 0 {
		t.Fatalf("mailed an unusable account: %d", len(r.mail.got))
	}
	for i := 0; i < agentsignup.MaxPerEmailPerDay+2; i++ {
		r.signUp(t, "flood@example.com")
	}
	if len(r.mail.got) != agentsignup.MaxPerEmailPerDay {
		t.Fatalf("cap: %d mails", len(r.mail.got))
	}
	// A day later the address can be mailed again.
	r.clock.now = r.clock.now.Add(agentsignup.Window + time.Second)
	r.signUp(t, "flood@example.com")
	if len(r.mail.got) != agentsignup.MaxPerEmailPerDay+1 {
		t.Fatalf("after the window: %d mails", len(r.mail.got))
	}
}

func TestPendingKeyAnswersKeyPendingConfirmation(t *testing.T) {
	r := newRig(t, true)
	got := r.signUp(t, "new@example.com")
	_, err := r.svc.AuthenticateAPIKey(ctx, got.RawKey, ClientInfo{})
	e, ok := errs.As(err)
	if !ok || e.Code != errs.KeyPendingConfirmation || e.Fields["email_masked"] != "n***@example.com" ||
		e.Fields["expires_at"] != got.ExpiresAt.UTC().Format(time.RFC3339) ||
		e.Fields["scopes"] != "analytics:read media:write posts:read posts:schedule posts:write social:connect social:read" {
		t.Fatalf("pending key: %v %+v", err, e)
	}
	other, _ := crypto.GenerateAPIKey()
	if _, err := r.svc.AuthenticateAPIKey(ctx, other.Raw, ClientInfo{}); !errs.Is(err, errs.Unauthenticated) {
		t.Fatalf("unknown key: %v", err)
	}
	r.clock.now = got.ExpiresAt
	if _, err := r.svc.AuthenticateAPIKey(ctx, got.RawKey, ClientInfo{}); !errs.Is(err, errs.Unauthenticated) {
		t.Fatalf("expired request: %v", err)
	}
}

func TestConfirmCreatesAccountKeyAndConnection(t *testing.T) {
	r := newRig(t, true)
	got := r.signUp(t, "new@example.com")
	tok := r.lastAgentToken(t)
	p, err := r.svc.PreviewAgentSignup(ctx, tok)
	if err != nil || p.AccountExists || p.Email != "new@example.com" || p.AgentName != "Claude Desktop" {
		t.Fatalf("preview: %+v %v", p, err)
	}
	// Refusing the Terms leaves the link alone.
	_, err = r.svc.ConfirmAgentSignup(ctx, actor.Actor{}, ConfirmAgentSignupInput{Token: tok}, ClientInfo{})
	if e, ok := errs.As(err); !ok || e.Fields["accept_terms"] == "" {
		t.Fatalf("terms: %v", err)
	}
	done, err := r.svc.ConfirmAgentSignup(ctx, actor.Actor{}, ConfirmAgentSignupInput{Token: tok, AcceptTerms: true, DisplayName: "New"}, ClientInfo{})
	if err != nil {
		t.Fatal(err)
	}
	u := done.User
	if !done.AccountCreated || done.Session.Token == "" || u.HasPassword() || !u.EmailVerified() || u.TermsAcceptedAt == nil || u.DisplayName != "New" {
		t.Fatalf("confirmed: %+v %+v", done, u)
	}
	keys := r.agents.keys.keys
	if len(keys) != 1 || keys[0].KeyHash != crypto.SHA256Hex(got.RawKey) || keys[0].UserID != u.ID ||
		keys[0].DangerousPolicy != approval.PolicyTrustedConnect || keys[0].Name != "MCP: Claude Desktop" {
		t.Fatalf("key: %+v", keys)
	}
	if c := r.agents.keys.conns; len(c) != 1 || c[0].APIKeyID != keys[0].ID || c[0].UserID != u.ID {
		t.Fatalf("connection: %+v", c)
	}
	wantAudit(t, r.audit, audit.ActionUserRegistered, audit.ActionAPIKeyCreated, audit.ActionAgentSignupConfirmed)
	if _, err := r.svc.ConfirmAgentSignup(ctx, actor.Actor{}, ConfirmAgentSignupInput{Token: tok, AcceptTerms: true}, ClientInfo{}); !errs.Is(err, errs.Validation) {
		t.Fatalf("a reused link must be invalid: %v", err)
	}
	if len(r.agents.keys.keys) != 1 {
		t.Fatal("a second key was created")
	}
}

func wantAudit(t *testing.T, a *auditFake, actions ...string) {
	t.Helper()
	have := map[string]map[string]any{}
	for _, e := range a.got {
		have[e.action] = e.meta
	}
	for _, act := range actions {
		meta, ok := have[act]
		if !ok {
			t.Fatalf("no %s audit entry in %+v", act, a.got)
		}
		for k, v := range meta {
			if s, isStr := v.(string); isStr && (strings.Contains(s, "@") || strings.Contains(s, "sk_live_") && len(s) > 20) {
				t.Fatalf("%s metadata %s holds a secret or an address: %q", act, k, s)
			}
		}
	}
}

func TestConfirmForExistingAccountNeedsItsOwnSession(t *testing.T) {
	r := newRig(t, true)
	r.signUp(t, "ann@example.com")
	tok := r.lastAgentToken(t)
	in := ConfirmAgentSignupInput{Token: tok}
	if _, err := r.svc.ConfirmAgentSignup(ctx, actor.Actor{}, in, ClientInfo{}); !errs.Is(err, errs.Unauthenticated) {
		t.Fatalf("anonymous: %v", err)
	}
	stranger := &user.User{ID: uuid.New()}
	if _, err := r.svc.ConfirmAgentSignup(ctx, sessionOf(stranger), in, ClientInfo{}); !errs.Is(err, errs.Forbidden) {
		t.Fatalf("another user's session: %v", err)
	}
	key := actor.Actor{UserID: r.u.ID, Type: actor.TypeAPIKey, APIKeyID: uuid.New()}
	if _, err := r.svc.ConfirmAgentSignup(ctx, key, in, ClientInfo{}); !errs.Is(err, errs.Forbidden) {
		t.Fatalf("an API key: %v", err)
	}
	r.u.Status = user.StatusDisabled
	if _, err := r.svc.ConfirmAgentSignup(ctx, sessionOf(r.u), in, ClientInfo{}); !errs.Is(err, errs.Conflict) {
		t.Fatalf("disabled account: %v", err)
	}
	r.u.Status = user.StatusActive
	if len(r.agents.keys.keys) != 0 {
		t.Fatal("a refusal created a key")
	}
	done, err := r.svc.ConfirmAgentSignup(ctx, sessionOf(r.u), in, ClientInfo{})
	if err != nil || done.AccountCreated || done.Session.Token != "" || done.User.ID != r.u.ID {
		t.Fatalf("own session: %+v %v", done, err)
	}
	if k := r.agents.keys.keys; len(k) != 1 || k[0].UserID != r.u.ID || !r.u.EmailVerified() {
		t.Fatalf("key: %+v", k)
	}
}

func TestConfirmNewAccountWhileSignedInElsewhereIsAConflict(t *testing.T) {
	r := newRig(t, true)
	r.signUp(t, "new@example.com")
	_, err := r.svc.ConfirmAgentSignup(ctx, sessionOf(r.u), ConfirmAgentSignupInput{Token: r.lastAgentToken(t), AcceptTerms: true}, ClientInfo{})
	if !errs.Is(err, errs.Conflict) {
		t.Fatalf("want CONFLICT, got %v", err)
	}
}

func TestExpiredOrDeclinedLinksAreInvalid(t *testing.T) {
	r := newRig(t, true)
	got := r.signUp(t, "new@example.com")
	tok := r.lastAgentToken(t)
	if err := r.svc.DeclineAgentSignup(ctx, tok); err != nil {
		t.Fatal(err)
	}
	if _, err := r.svc.AuthenticateAPIKey(ctx, got.RawKey, ClientInfo{}); !errs.Is(err, errs.Unauthenticated) {
		t.Fatalf("declined key: %v", err)
	}
	if err := r.svc.DeclineAgentSignup(ctx, tok); !errs.Is(err, errs.Validation) {
		t.Fatalf("second decline: %v", err)
	}

	r.signUp(t, "late@example.com")
	late := r.lastAgentToken(t)
	r.clock.now = r.clock.now.Add(agentsignup.TTL)
	if _, err := r.svc.PreviewAgentSignup(ctx, late); !errs.Is(err, errs.Validation) {
		t.Fatalf("expired preview: %v", err)
	}
	if _, err := r.svc.ConfirmAgentSignup(ctx, actor.Actor{}, ConfirmAgentSignupInput{Token: late, AcceptTerms: true}, ClientInfo{}); !errs.Is(err, errs.Validation) {
		t.Fatalf("expired confirm: %v", err)
	}
	if n, err := r.svc.PurgeExpiredAgentSignups(ctx); err != nil || n != 1 {
		t.Fatalf("purge: %d %v", n, err)
	}
	for _, bad := range []string{"", strings.Repeat("x", 129)} {
		if _, err := r.svc.PreviewAgentSignup(ctx, bad); !errs.Is(err, errs.Validation) {
			t.Fatalf("malformed token %q: %v", bad, err)
		}
	}
}

func TestUndeliverableAgentMailDropsTheRequest(t *testing.T) {
	r := newRig(t, true)
	got := r.signUp(t, "new@example.com")
	if err := r.svc.RetireMailToken(ctx, r.mail.got[0].TokenID); err != nil {
		t.Fatal(err)
	}
	if len(r.agents.signups.rows) != 0 {
		t.Fatal("request kept after its mail failed for good")
	}
	if _, err := r.svc.AuthenticateAPIKey(ctx, got.RawKey, ClientInfo{}); !errs.Is(err, errs.Unauthenticated) {
		t.Fatalf("key of a dropped request: %v", err)
	}
}

func TestAgentSignupScopesNeverIncludeDangerousOnes(t *testing.T) {
	r := newRig(t, true)
	got := r.signUp(t, "new@example.com")
	for _, s := range got.Scopes {
		if s == apikey.PostsPublish || s == apikey.PostsDelete || s == apikey.SocialDisconnect {
			t.Fatalf("agent key holds %s", s)
		}
	}
}

// A redelivered task must not replace the token of a link that was already mailed, nor mail a second link.
func TestProcessingASignupTwiceKeepsTheFirstLink(t *testing.T) {
	r := newRig(t, true)
	if _, err := r.svc.StartAgentSignup(ctx, validSignup("new@example.com")); err != nil {
		t.Fatal(err)
	}
	id := r.agents.queue.ids[0]
	for i := 0; i < 2; i++ {
		if err := r.svc.ProcessAgentSignup(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	if len(r.mail.got) != 1 {
		t.Fatalf("a redelivered task mailed again: %d mails", len(r.mail.got))
	}
	if _, err := r.svc.PreviewAgentSignup(ctx, tokenFrom(t, r.mail.got[0])); err != nil {
		t.Fatalf("the first link must keep working: %v", err)
	}
}

// A mail that could not be queued gives its token back, so the retried task mails a working link.
func TestFailedEnqueueLetsTheRetryMail(t *testing.T) {
	r := newRig(t, true)
	if _, err := r.svc.StartAgentSignup(ctx, validSignup("new@example.com")); err != nil {
		t.Fatal(err)
	}
	id := r.agents.queue.ids[0]
	r.mail.fail = errors.New("redis down")
	if err := r.svc.ProcessAgentSignup(ctx, id); err == nil {
		t.Fatal("a failed enqueue must be returned, so the task is retried")
	}
	r.mail.fail = nil
	if err := r.svc.ProcessAgentSignup(ctx, id); err != nil || len(r.mail.got) != 1 {
		t.Fatalf("retry: %v, %d mails", err, len(r.mail.got))
	}
	if _, err := r.svc.PreviewAgentSignup(ctx, tokenFrom(t, r.mail.got[0])); err != nil {
		t.Fatalf("the retried link must work: %v", err)
	}
}
