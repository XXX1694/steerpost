package auth

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/socialos/backend/internal/application/port"
	"github.com/socialos/backend/internal/domain/actor"
	"github.com/socialos/backend/internal/domain/apikey"
	"github.com/socialos/backend/internal/domain/emailtoken"
	"github.com/socialos/backend/internal/domain/errs"
	"github.com/socialos/backend/internal/domain/user"
)

type clockFake struct{ now time.Time }

func (c *clockFake) Now() time.Time { return c.now }

type inlineTx struct{}

func (inlineTx) InTx(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) }

type plainHasher struct{}

func (plainHasher) Hash(_ context.Context, p string) (string, error) { return "h:" + p, nil }
func (plainHasher) Verify(_ context.Context, p, enc string) (bool, error) {
	return enc == "h:"+p || enc == "old:"+p, nil
}

// NeedsRehash treats the "old:" prefix as outdated parameters.
func (plainHasher) NeedsRehash(enc string) bool { return strings.HasPrefix(enc, "old:") }

type auditEntry struct {
	action string
	meta   map[string]any
}

type auditFake struct{ got []auditEntry }

func (a *auditFake) Record(_ context.Context, _ actor.Actor, action, _, _ string, meta map[string]any) error {
	a.got = append(a.got, auditEntry{action, meta})
	return nil
}

type usersFake struct {
	byID map[uuid.UUID]*user.User
	// byEmailCalls counts GetByEmail, so tests can prove a request path never looks an address up.
	byEmailCalls int
}

func (f *usersFake) Create(_ context.Context, u *user.User) error {
	u.ID = uuid.New()
	f.byID[u.ID] = u
	return nil
}
func (f *usersFake) GetByEmail(_ context.Context, e string) (*user.User, error) {
	f.byEmailCalls++
	for _, u := range f.byID {
		if u.Email == e {
			return u, nil
		}
	}
	return nil, errs.NotFoundf("user")
}
func (f *usersFake) GetByID(_ context.Context, id uuid.UUID) (*user.User, error) {
	if u, ok := f.byID[id]; ok {
		return u, nil
	}
	return nil, errs.NotFoundf("user")
}
func (f *usersFake) SetPassword(_ context.Context, id uuid.UUID, h string) error {
	f.byID[id].PasswordHash = h
	return nil
}
func (f *usersFake) RehashPassword(_ context.Context, id uuid.UUID, old, h string) error {
	if u := f.byID[id]; u.PasswordHash == old {
		u.PasswordHash = h
	}
	return nil
}
func (f *usersFake) MarkEmailVerified(_ context.Context, id uuid.UUID, at time.Time) error {
	if u := f.byID[id]; u.EmailVerifiedAt == nil {
		u.EmailVerifiedAt = &at
	}
	return nil
}

type sessionsFake struct{ ids map[uuid.UUID]uuid.UUID } // session id -> user id

func (f *sessionsFake) Create(context.Context, *Session) error { return nil }
func (f *sessionsFake) GetByTokenHash(context.Context, string) (*Session, error) {
	return nil, errs.NotFoundf("session")
}
func (f *sessionsFake) Delete(context.Context, uuid.UUID, uuid.UUID) error      { return nil }
func (f *sessionsFake) DeleteExpired(context.Context, time.Time) (int64, error) { return 0, nil }
func (f *sessionsFake) DeleteAllForUser(_ context.Context, uid, except uuid.UUID) (int64, error) {
	var n int64
	for sid, owner := range f.ids {
		if owner == uid && sid != except {
			delete(f.ids, sid)
			n++
		}
	}
	return n, nil
}

type keysFake struct{ revoked int64 }

func (*keysFake) GetByHash(context.Context, string) (*apikey.Key, error) {
	return nil, errs.NotFoundf("key")
}
func (*keysFake) TouchLastUsed(context.Context, uuid.UUID, time.Time) error { return nil }
func (k *keysFake) RevokeAllForUser(context.Context, uuid.UUID, time.Time) (int64, error) {
	k.revoked += 3 // pretend the user owns three keys and connections
	return 3, nil
}

// forgotFake records what the HTTP side queued, like the real queue.
type forgotFake struct{ emails []string }

func (f *forgotFake) EnqueueForgot(_ context.Context, e string) error {
	f.emails = append(f.emails, e)
	return nil
}

// tokensFake keeps the documented EmailTokens semantics: newest-wins, single use, expiry, purpose.
type tokensFake struct {
	mu   sync.Mutex
	rows []*emailtoken.Token
}

func (f *tokensFake) Create(_ context.Context, t *emailtoken.Token) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.rows {
		if r.UserID == t.UserID && r.Purpose == t.Purpose && r.UsedAt == nil {
			r.UsedAt = &t.CreatedAt
		}
	}
	t.ID = uuid.New()
	c := *t
	f.rows = append(f.rows, &c)
	return nil
}
func (f *tokensFake) Consume(_ context.Context, p emailtoken.Purpose, hash string, now time.Time) (*emailtoken.Token, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.rows {
		if r.Hash == hash && r.Purpose == p && r.UsedAt == nil && r.ExpiresAt.After(now) {
			r.UsedAt = &now
			c := *r
			return &c, nil
		}
	}
	return nil, errs.NotFoundf("link")
}
func (f *tokensFake) RetireAll(_ context.Context, uid uuid.UUID, p emailtoken.Purpose, now time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.rows {
		if r.UserID == uid && r.Purpose == p && r.UsedAt == nil {
			r.UsedAt = &now
		}
	}
	return nil
}
func (f *tokensFake) LatestCreatedAt(_ context.Context, uid uuid.UUID, p emailtoken.Purpose) (time.Time, error) {
	var last time.Time
	for _, r := range f.rows {
		if r.UserID == uid && r.Purpose == p && r.CreatedAt.After(last) {
			last = r.CreatedAt
		}
	}
	return last, nil
}

func (f *tokensFake) CountSince(_ context.Context, uid uuid.UUID, p emailtoken.Purpose, since time.Time) (int, error) {
	n := 0
	for _, r := range f.rows {
		if r.UserID == uid && r.Purpose == p && !r.CreatedAt.Before(since) {
			n++
		}
	}
	return n, nil
}
func (f *tokensFake) RetireByID(_ context.Context, id uuid.UUID, now time.Time) error {
	for _, r := range f.rows {
		if r.ID == id && r.UsedAt == nil {
			r.UsedAt = &now
		}
	}
	return nil
}

type mailFake struct {
	got  []port.Message
	fail error
}

func (m *mailFake) Enqueue(_ context.Context, msg port.Message) error {
	if m.fail != nil {
		return m.fail
	}
	m.got = append(m.got, msg)
	return nil
}

// tokenFrom extracts the raw token from the link in a rendered message.
func tokenFrom(t testing.TB, m port.Message) string {
	t.Helper()
	_, after, ok := strings.Cut(m.Text, "#token=")
	if !ok {
		t.Fatalf("no token link in %s mail: %q", m.Template, m.Text)
	}
	return strings.Fields(after)[0]
}

type rig struct {
	svc      *Service
	users    *usersFake
	sessions *sessionsFake
	tokens   *tokensFake
	mail     *mailFake
	audit    *auditFake
	keys     *keysFake
	forgotQ  *forgotFake
	agents   *agentRig
	clock    *clockFake
	u        *user.User
}

func newRig(t testing.TB, enforce bool) *rig {
	t.Helper()
	r := &rig{users: &usersFake{byID: map[uuid.UUID]*user.User{}}, sessions: &sessionsFake{ids: map[uuid.UUID]uuid.UUID{}},
		tokens: &tokensFake{}, mail: &mailFake{}, audit: &auditFake{}, keys: &keysFake{}, forgotQ: &forgotFake{}, agents: newAgentRig(),
		clock: &clockFake{now: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)}}
	svc, err := NewService(Deps{Users: r.users, Sessions: r.sessions, APIKeys: r.keys, Hasher: plainHasher{}, Tx: inlineTx{},
		Audit: r.audit, Clock: r.clock, Tokens: r.tokens, Mail: r.mail, Forgot: r.forgotQ, WebBaseURL: "https://app.example/", RequireVerification: enforce,
		AgentSignup: &AgentSignupDeps{Signups: r.agents.signups, Queue: r.agents.queue, Keys: r.agents.keys}})
	if err != nil {
		t.Fatal(err)
	}
	r.svc = svc
	r.u = &user.User{Email: "ann@example.com", PasswordHash: "h:old password 1", Status: user.StatusActive, Plan: user.DefaultPlan}
	_ = r.users.Create(context.Background(), r.u)
	return r
}

func (r *rig) addSession() uuid.UUID {
	id := uuid.New()
	r.sessions.ids[id] = r.u.ID
	return id
}

// forgot runs the HTTP side of "forgot password" and then the worker side for whatever it queued.
func (r *rig) forgot(email string) error {
	if err := r.svc.ForgotPassword(ctx, email); err != nil {
		return err
	}
	queued := r.forgotQ.emails
	r.forgotQ.emails = nil
	for _, e := range queued {
		if err := r.svc.ProcessForgot(ctx, e); err != nil {
			return err
		}
	}
	return nil
}
