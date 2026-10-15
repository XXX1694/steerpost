package auth

import (
	"context"

	"github.com/google/uuid"

	"github.com/socialos/backend/internal/domain/user"
	"github.com/socialos/backend/internal/infrastructure/crypto"
)

// startSession issues a fresh session (a new token every time, so a session id planted before sign-in is never reused)
// and records how the user got in. Callers run it inside the transaction that decided the sign-in.
func (s *Service) startSession(ctx context.Context, u *user.User, ci ClientInfo, action string, meta map[string]any) (IssuedSession, error) {
	issued, err := s.newSession(ctx, u.ID, ci)
	if err != nil {
		return IssuedSession{}, err
	}
	return issued, s.audit.Record(ctx, userActor(u, ci), action, "user", u.ID.String(), meta)
}

func (s *Service) newSession(ctx context.Context, userID uuid.UUID, ci ClientInfo) (IssuedSession, error) {
	token, err := crypto.RandomToken(32)
	if err != nil {
		return IssuedSession{}, err
	}
	csrf, err := crypto.RandomToken(32)
	if err != nil {
		return IssuedSession{}, err
	}
	sess := &Session{
		UserID: userID, TokenHash: crypto.SHA256Hex(token), CSRFToken: csrf,
		CreatedAt: s.clock.Now(), ExpiresAt: s.clock.Now().Add(s.sessionTTL), UserAgent: truncate(ci.UserAgent, 256), IP: ci.IP,
	}
	if err := s.sessions.Create(ctx, sess); err != nil {
		return IssuedSession{}, err
	}
	return IssuedSession{Token: token, CSRFToken: csrf, ExpiresAt: sess.ExpiresAt}, nil
}

// truncate cuts s to at most n runes, never in the middle of a character.
func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}
