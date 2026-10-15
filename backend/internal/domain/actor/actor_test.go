package actor

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/socialos/backend/internal/domain/apikey"
	"github.com/socialos/backend/internal/domain/approval"
	"github.com/socialos/backend/internal/domain/errs"
)

func TestScopeAuthorization(t *testing.T) {
	uid := uuid.New()
	key := Actor{UserID: uid, Type: TypeAPIKey, APIKeyID: uuid.New(), Scopes: []apikey.Scope{apikey.PostsRead, apikey.PostsWrite}}
	if err := key.Require(apikey.PostsRead); err != nil {
		t.Fatalf("expected granted: %v", err)
	}
	if err := key.Require(apikey.PostsPublish); !errs.Is(err, errs.InsufficientScope) {
		t.Fatalf("expected INSUFFICIENT_SCOPE, got %v", err)
	}
	if err := key.RequireSession(); !errs.Is(err, errs.Forbidden) {
		t.Fatalf("api key must not pass session check: %v", err)
	}
	sess := Actor{UserID: uid, Type: TypeUser, SessionID: uuid.New()}
	for _, s := range apikey.AllScopes() {
		if err := sess.Require(s); err != nil {
			t.Fatalf("session should have %s", s)
		}
	}
	if err := sess.RequireSession(); err != nil {
		t.Fatal(err)
	}
	if err := (Actor{}).Require(apikey.PostsRead); !errs.Is(err, errs.Unauthenticated) {
		t.Fatalf("anonymous must be unauthenticated: %v", err)
	}
	if len(key.EffectiveScopes()) != 2 || len(sess.EffectiveScopes()) != len(apikey.AllScopes()) {
		t.Fatal("effective scopes wrong")
	}
}

func TestContextRoundTrip(t *testing.T) {
	a := Scheduler(uuid.New())
	got, ok := From(With(context.Background(), a))
	if !ok || got.UserID != a.UserID || got.Type != TypeScheduler {
		t.Fatal("round trip failed")
	}
	if _, ok := From(context.Background()); ok {
		t.Fatal("expected no actor")
	}
}

func TestRequireNotDeleting(t *testing.T) {
	if err := (Actor{UserID: uuid.New(), Type: TypeUser, DeletionScheduled: true}).RequireNotDeleting(); !errs.Is(err, errs.Conflict) {
		t.Fatalf("a session of an account being deleted: %v", err)
	}
	if err := (Actor{UserID: uuid.New(), Type: TypeAPIKey, DeletionScheduled: true}).RequireNotDeleting(); !errs.Is(err, errs.Conflict) {
		t.Fatalf("an API key of an account being deleted: %v", err)
	}
	for _, a := range []Actor{{Type: TypeUser}, Scheduler(uuid.New()), System(uuid.New(), "account_deletion")} {
		a.DeletionScheduled = a.Type == TypeScheduler || a.Type == TypeSystem
		if err := a.RequireNotDeleting(); err != nil {
			t.Fatalf("%s must pass: %v", a.Type, err)
		}
	}
}

func TestSessionIsFresh(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	for name, tc := range map[string]struct {
		created time.Time
		want    bool
	}{
		"just now":      {now, true},
		"at the window": {now.Add(-FreshSessionWindow), true},
		"past it":       {now.Add(-FreshSessionWindow - time.Second), false},
		"no session":    {time.Time{}, false},
		"clock skew":    {now.Add(time.Minute), true},
	} {
		if got := (Actor{SessionCreatedAt: tc.created}).SessionIsFresh(now); got != tc.want {
			t.Errorf("%s: got %v, want %v", name, got, tc.want)
		}
	}
}

func TestNeedsApprovalByPolicyAndAction(t *testing.T) {
	key := func(policy string) Actor { return Actor{UserID: uuid.New(), Type: TypeAPIKey, DangerousPolicy: policy} }
	actions := []approval.Action{approval.ActionPostPublish, approval.ActionPostRetryNow, approval.ActionPostDelete,
		approval.ActionAccountDisconnect, approval.ActionAccountConnect, approval.ActionPostScheduleSoon}
	for _, act := range actions {
		for name, tc := range map[string]struct {
			a    Actor
			want bool
		}{
			"approve key":    {key(approval.PolicyApprove), true},
			"unset policy":   {key(""), true},
			"unknown policy": {key("bogus"), true},
			"trusted key":    {key(approval.PolicyTrusted), false},
			// trusted_connect skips only the connect approval.
			"trusted_connect key": {key(approval.PolicyTrustedConnect), act != approval.ActionAccountConnect},
			"session":             {Actor{UserID: uuid.New(), Type: TypeUser, SessionID: uuid.New()}, false},
			"scheduler":           {Scheduler(uuid.New()), false},
			"system":              {System(uuid.New(), "telegram"), false},
		} {
			if got := tc.a.NeedsApproval(act); got != tc.want {
				t.Errorf("%s, %s: NeedsApproval = %v, want %v", name, act, got, tc.want)
			}
		}
	}
}
