package postgres_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/socialos/backend/internal/domain/agentsignup"
	"github.com/socialos/backend/internal/domain/errs"
	"github.com/socialos/backend/internal/infrastructure/postgres"
	"github.com/socialos/backend/internal/testutil"
)

func TestAgentSignupRepoSemantics(t *testing.T) {
	db := testutil.OpenDB(t)
	r, ctx := postgres.NewAgentSignups(db), context.Background()
	mk := func(email, keyHash string) *agentsignup.Signup {
		s := &agentsignup.Signup{Email: email, AgentName: "Agent", KeyPrefix: "sk_live_x", KeyHash: keyHash, Scopes: agentsignup.Scopes(),
			DangerousPolicy: agentsignup.Policy, ExpiresAt: base.Add(agentsignup.TTL), CreatedAt: base}
		if err := r.Create(ctx, s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	a := mk("Repo@Example.com", "key-a")
	mk("repo@example.com", "key-b")
	if got, err := r.PendingByKeyHash(ctx, "key-a", base); err != nil || got.ID != a.ID || len(got.Scopes) != 7 || got.TokenHash != "" {
		t.Fatalf("pending: %+v %v", got, err)
	}
	if _, err := r.PendingByKeyHash(ctx, "key-a", a.ExpiresAt); !errs.Is(err, errs.NotFound) {
		t.Fatalf("expired pending: %v", err)
	}
	if n, _ := r.CountMailedSince(ctx, "repo@example.com", base); n != 0 {
		t.Fatalf("nothing mailed yet: %d", n)
	}
	if ok, err := r.SetTokenHash(ctx, a.ID, "tok-a"); err != nil || !ok {
		t.Fatalf("first token: %v %v", ok, err)
	}
	// A redelivered task must not replace the token of the link already mailed.
	if ok, err := r.SetTokenHash(ctx, a.ID, "tok-other"); err != nil || ok {
		t.Fatalf("second token: %v %v", ok, err)
	}
	if err := r.ClearTokenHash(ctx, a.ID, "tok-other"); err != nil {
		t.Fatal(err)
	}
	if got, err := r.Get(ctx, a.ID); err != nil || got.TokenHash != "tok-a" {
		t.Fatalf("a clear with another hash must change nothing: %+v %v", got, err)
	}
	// citext: the count matches the address in any case.
	if n, _ := r.CountMailedSince(ctx, "REPO@example.com", base); n != 1 {
		t.Fatalf("mailed count: %d", n)
	}
	if _, err := r.ByToken(ctx, "tok-a", a.ExpiresAt); !errs.Is(err, errs.NotFound) {
		t.Fatalf("expired token: %v", err)
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := r.ConsumeByToken(ctx, "tok-a", base); err == nil {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("a token must be consumed exactly once, got %d", wins.Load())
	}
	if n, err := r.DeleteExpired(ctx, base.Add(agentsignup.TTL)); err != nil || n != 1 {
		t.Fatalf("purge: %d %v", n, err)
	}
	if err := r.DeleteByID(ctx, a.ID); err != nil {
		t.Fatalf("deleting a missing row: %v", err)
	}
}
