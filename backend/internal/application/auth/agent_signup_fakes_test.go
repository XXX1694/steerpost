package auth

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/socialos/backend/internal/domain/agentsignup"
	"github.com/socialos/backend/internal/domain/apikey"
	"github.com/socialos/backend/internal/domain/errs"
)

// agentSignupsFake keeps the documented AgentSignups semantics: live rows only, single-use tokens.
type agentSignupsFake struct {
	rows map[uuid.UUID]*agentsignup.Signup
}

func (f *agentSignupsFake) Create(_ context.Context, s *agentsignup.Signup) error {
	s.ID = uuid.New()
	c := *s
	f.rows[s.ID] = &c
	return nil
}

func (f *agentSignupsFake) Get(_ context.Context, id uuid.UUID) (*agentsignup.Signup, error) {
	if r, ok := f.rows[id]; ok {
		c := *r
		return &c, nil
	}
	return nil, errs.NotFoundf("agent sign-up")
}

func (f *agentSignupsFake) LockEmail(context.Context, string) error { return nil }

func (f *agentSignupsFake) SetTokenHash(_ context.Context, id uuid.UUID, h string) (bool, error) {
	r, ok := f.rows[id]
	if !ok || r.TokenHash != "" {
		return false, nil
	}
	r.TokenHash = h
	return true, nil
}

func (f *agentSignupsFake) ClearTokenHash(_ context.Context, id uuid.UUID, h string) error {
	if r, ok := f.rows[id]; ok && r.TokenHash == h {
		r.TokenHash = ""
	}
	return nil
}

func (f *agentSignupsFake) find(match func(*agentsignup.Signup) bool, now time.Time) (*agentsignup.Signup, error) {
	for _, r := range f.rows {
		if match(r) && r.Live(now) {
			c := *r
			return &c, nil
		}
	}
	return nil, errs.NotFoundf("agent sign-up")
}

func (f *agentSignupsFake) PendingByKeyHash(_ context.Context, h string, now time.Time) (*agentsignup.Signup, error) {
	return f.find(func(r *agentsignup.Signup) bool { return r.KeyHash == h }, now)
}

func (f *agentSignupsFake) ByToken(_ context.Context, h string, now time.Time) (*agentsignup.Signup, error) {
	return f.find(func(r *agentsignup.Signup) bool { return r.TokenHash != "" && r.TokenHash == h }, now)
}

func (f *agentSignupsFake) ConsumeByToken(ctx context.Context, h string, now time.Time) (*agentsignup.Signup, error) {
	r, err := f.ByToken(ctx, h, now)
	if err == nil {
		delete(f.rows, r.ID)
	}
	return r, err
}

func (f *agentSignupsFake) DeleteByID(_ context.Context, id uuid.UUID) error {
	delete(f.rows, id)
	return nil
}

func (f *agentSignupsFake) CountMailedSince(_ context.Context, email string, since time.Time) (int, error) {
	n := 0
	for _, r := range f.rows {
		if strings.EqualFold(r.Email, email) && r.TokenHash != "" && !r.CreatedAt.Before(since) {
			n++
		}
	}
	return n, nil
}

func (f *agentSignupsFake) DeleteExpired(_ context.Context, now time.Time) (int64, error) {
	var n int64
	for id, r := range f.rows {
		if !r.Live(now) {
			delete(f.rows, id)
			n++
		}
	}
	return n, nil
}

type agentQueueFake struct{ ids []uuid.UUID }

func (q *agentQueueFake) EnqueueAgentSignup(_ context.Context, id uuid.UUID) error {
	q.ids = append(q.ids, id)
	return nil
}

type agentKeysFake struct {
	keys  []apikey.Key
	conns []AgentConnection
}

func (k *agentKeysFake) CreateKey(_ context.Context, key *apikey.Key) error {
	k.keys = append(k.keys, *key)
	return nil
}

func (k *agentKeysFake) CreateConnection(_ context.Context, c AgentConnection) error {
	k.conns = append(k.conns, c)
	return nil
}

type agentRig struct {
	signups *agentSignupsFake
	queue   *agentQueueFake
	keys    *agentKeysFake
}

func newAgentRig() *agentRig {
	return &agentRig{signups: &agentSignupsFake{rows: map[uuid.UUID]*agentsignup.Signup{}}, queue: &agentQueueFake{}, keys: &agentKeysFake{}}
}
