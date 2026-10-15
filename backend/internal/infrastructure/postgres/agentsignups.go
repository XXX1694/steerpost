package postgres

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/socialos/backend/internal/application/auth"
	"github.com/socialos/backend/internal/domain/agentsignup"
	"github.com/socialos/backend/internal/domain/apikey"
)

// AgentSignups implements auth.AgentSignups.
type AgentSignups struct{ db *DB }

var _ auth.AgentSignups = (*AgentSignups)(nil)

// NewAgentSignups creates the repo.
func NewAgentSignups(db *DB) *AgentSignups { return &AgentSignups{db: db} }

const agentSignupCols = `id, email, agent_name, key_prefix, key_hash, scopes, dangerous_policy, COALESCE(token_hash, ''), expires_at, created_at`

func scanAgentSignup(row interface{ Scan(...any) error }) (*agentsignup.Signup, error) {
	var s agentsignup.Signup
	var scopes []string
	if err := row.Scan(&s.ID, &s.Email, &s.AgentName, &s.KeyPrefix, &s.KeyHash, &scopes, &s.DangerousPolicy, &s.TokenHash,
		&s.ExpiresAt, &s.CreatedAt); err != nil {
		return nil, err
	}
	s.Scopes = apikey.FromStrings(scopes)
	return &s, nil
}

// Create stores a new request.
func (r *AgentSignups) Create(ctx context.Context, s *agentsignup.Signup) error {
	return mapErr(r.db.q(ctx).QueryRow(ctx,
		`INSERT INTO agent_signups (email, agent_name, key_prefix, key_hash, scopes, dangerous_policy, expires_at, created_at)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
		s.Email, s.AgentName, s.KeyPrefix, s.KeyHash, apikey.Strings(s.Scopes), s.DangerousPolicy, s.ExpiresAt, s.CreatedAt).Scan(&s.ID),
		"agent sign-up")
}

// Get reads a request by id (primary key).
func (r *AgentSignups) Get(ctx context.Context, id uuid.UUID) (*agentsignup.Signup, error) {
	s, err := scanAgentSignup(r.db.q(ctx).QueryRow(ctx, `SELECT `+agentSignupCols+` FROM agent_signups WHERE id = $1`, id))
	return s, mapErr(err, "agent sign-up")
}

// LockEmail takes a transaction-scoped advisory lock on the address (case-insensitive, like the citext column).
func (r *AgentSignups) LockEmail(ctx context.Context, email string) error {
	_, err := r.db.q(ctx).Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('agent_signup:' || lower($1)))`, email)
	return mapErr(err, "agent sign-up")
}

// SetTokenHash attaches the hash of the mailed token only while the request has none (primary key).
func (r *AgentSignups) SetTokenHash(ctx context.Context, id uuid.UUID, tokenHash string) (bool, error) {
	tag, err := r.db.q(ctx).Exec(ctx, `UPDATE agent_signups SET token_hash = $2 WHERE id = $1 AND token_hash IS NULL`, id, tokenHash)
	if err != nil {
		return false, mapErr(err, "agent sign-up")
	}
	return tag.RowsAffected() == 1, nil
}

// ClearTokenHash removes the token hash while it still equals tokenHash (primary key).
func (r *AgentSignups) ClearTokenHash(ctx context.Context, id uuid.UUID, tokenHash string) error {
	_, err := r.db.q(ctx).Exec(ctx, `UPDATE agent_signups SET token_hash = NULL WHERE id = $1 AND token_hash = $2`, id, tokenHash)
	return mapErr(err, "agent sign-up")
}

// PendingByKeyHash finds a live request by its key (unique key_hash).
func (r *AgentSignups) PendingByKeyHash(ctx context.Context, keyHash string, now time.Time) (*agentsignup.Signup, error) {
	s, err := scanAgentSignup(r.db.q(ctx).QueryRow(ctx,
		`SELECT `+agentSignupCols+` FROM agent_signups WHERE key_hash = $1 AND expires_at > $2`, keyHash, now))
	return s, mapErr(err, "agent sign-up")
}

// ByToken reads a live request by its token (unique token_hash) without using it.
func (r *AgentSignups) ByToken(ctx context.Context, tokenHash string, now time.Time) (*agentsignup.Signup, error) {
	s, err := scanAgentSignup(r.db.q(ctx).QueryRow(ctx,
		`SELECT `+agentSignupCols+` FROM agent_signups WHERE token_hash = $1 AND expires_at > $2`, tokenHash, now))
	return s, mapErr(err, "agent sign-up")
}

// ConsumeByToken deletes and returns a live request in one statement, so two racing confirmations cannot both get it.
func (r *AgentSignups) ConsumeByToken(ctx context.Context, tokenHash string, now time.Time) (*agentsignup.Signup, error) {
	s, err := scanAgentSignup(r.db.q(ctx).QueryRow(ctx,
		`DELETE FROM agent_signups WHERE token_hash = $1 AND expires_at > $2 RETURNING `+agentSignupCols, tokenHash, now))
	return s, mapErr(err, "agent sign-up")
}

// DeleteByID removes a request; a missing row is fine.
func (r *AgentSignups) DeleteByID(ctx context.Context, id uuid.UUID) error {
	_, err := r.db.q(ctx).Exec(ctx, `DELETE FROM agent_signups WHERE id = $1`, id)
	return mapErr(err, "agent sign-up")
}

// CountMailedSince counts the address's mailed requests since a time (agent_signups_email_idx).
func (r *AgentSignups) CountMailedSince(ctx context.Context, email string, since time.Time) (int, error) {
	var n int
	err := r.db.q(ctx).QueryRow(ctx,
		`SELECT count(*) FROM agent_signups WHERE email = $1 AND created_at >= $2 AND token_hash IS NOT NULL`, email, since).Scan(&n)
	return n, mapErr(err, "agent sign-up")
}

// DeleteExpired removes requests past their expiry (agent_signups_expires_idx).
func (r *AgentSignups) DeleteExpired(ctx context.Context, now time.Time) (int64, error) {
	tag, err := r.db.q(ctx).Exec(ctx, `DELETE FROM agent_signups WHERE expires_at <= $1`, now)
	if err != nil {
		return 0, mapErr(err, "agent sign-up")
	}
	return tag.RowsAffected(), nil
}
