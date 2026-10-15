package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/socialos/backend/internal/domain/agentsignup"
	"github.com/socialos/backend/internal/infrastructure/postgres"
	"github.com/socialos/backend/internal/testutil"
)

func TestMigration00009UpAndDown(t *testing.T) {
	url := testutil.FreshDatabase(t) // migrated up
	ctx := context.Background()
	db, err := postgres.Open(ctx, url, 4)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	exec := func(sql string) error { _, err := db.Pool.Exec(ctx, sql); return err }
	if err := exec(`INSERT INTO users (email, password_hash) VALUES ('agent-owner@example.com','$argon2id$x')`); err != nil {
		t.Fatal(err)
	}
	insertKey := func(prefix, policy string) error {
		return exec(`INSERT INTO api_keys (user_id, name, prefix, key_hash, scopes, dangerous_policy)
			SELECT id, 'k', '` + prefix + `', '` + prefix + `-hash', '{}', '` + policy + `' FROM users WHERE email = 'agent-owner@example.com'`)
	}
	if err := insertKey("sk_live_tc", "trusted_connect"); err != nil {
		t.Fatalf("up must allow trusted_connect: %v", err)
	}
	if err := insertKey("sk_live_bad", "bogus"); err == nil {
		t.Fatal("up must still refuse unknown policies")
	}
	if err := postgres.NewAgentSignups(db).Create(ctx, &agentsignup.Signup{Email: "x@example.com", AgentName: "a", KeyPrefix: "p",
		KeyHash: "h", Scopes: agentsignup.Scopes(), DangerousPolicy: agentsignup.Policy, ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}

	downTo(t, url, 8)
	if n := scalar[int](t, db, `SELECT count(*) FROM information_schema.tables WHERE table_name = 'agent_signups'`); n != 0 {
		t.Fatal("down kept agent_signups")
	}
	if p := scalar[string](t, db, `SELECT dangerous_policy FROM api_keys WHERE prefix = 'sk_live_tc'`); p != "approve" {
		t.Fatalf("down must fall back to the safe policy, got %q", p)
	}
	if err := insertKey("sk_live_tc2", "trusted_connect"); err == nil {
		t.Fatal("down must restore the old CHECK")
	}
	if err := postgres.Migrate(ctx, url, "up", testutil.Logger()); err != nil {
		t.Fatalf("re-up: %v", err)
	}
	if err := insertKey("sk_live_tc3", "trusted_connect"); err != nil {
		t.Fatalf("re-up: %v", err)
	}
}
