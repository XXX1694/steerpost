package auth

import (
	"context"
	"strings"
	"time"

	"github.com/socialos/backend/internal/domain/actor"
	"github.com/socialos/backend/internal/domain/apikey"
	"github.com/socialos/backend/internal/domain/errs"
	"github.com/socialos/backend/internal/domain/user"
	"github.com/socialos/backend/internal/infrastructure/crypto"
)

func unauthenticated() error { return errs.New(errs.Unauthenticated, "invalid or expired credentials") }

// AuthenticateSession resolves a session cookie. It returns the actor and the
// session's CSRF token (for double-submit verification by the transport).
func (s *Service) AuthenticateSession(ctx context.Context, rawToken string, ci ClientInfo) (actor.Actor, string, error) {
	if rawToken == "" || len(rawToken) > 128 {
		return actor.Actor{}, "", unauthenticated()
	}
	sess, err := s.sessions.GetByTokenHash(ctx, crypto.SHA256Hex(rawToken))
	if errs.Is(err, errs.NotFound) {
		return actor.Actor{}, "", unauthenticated()
	}
	if err != nil {
		return actor.Actor{}, "", err
	}
	if !s.clock.Now().Before(sess.ExpiresAt) {
		return actor.Actor{}, "", unauthenticated()
	}
	u, err := s.users.GetByID(ctx, sess.UserID)
	if err != nil {
		return actor.Actor{}, "", unauthenticated()
	}
	if u.Status != user.StatusActive {
		return actor.Actor{}, "", unauthenticated()
	}
	a := actor.Actor{
		UserID: u.ID, Type: actor.TypeUser, ID: u.ID.String(), Label: u.Email,
		SessionID: sess.ID, SessionCreatedAt: sess.CreatedAt, RequestID: ci.RequestID, IP: ci.IP, EmailVerified: s.verified(u), DeletionScheduled: u.DeletionScheduledAt != nil,
	}
	return a, sess.CSRFToken, nil
}

// AuthenticateAPIKey resolves a bearer API key (SHA-256 lookup, constant format check).
func (s *Service) AuthenticateAPIKey(ctx context.Context, raw string, ci ClientInfo) (actor.Actor, error) {
	if !crypto.LooksLikeAPIKey(raw) {
		return actor.Actor{}, unauthenticated()
	}
	hash := crypto.SHA256Hex(raw)
	k, err := s.keys.GetByHash(ctx, hash)
	if errs.Is(err, errs.NotFound) {
		return actor.Actor{}, s.pendingKeyError(ctx, hash)
	}
	if err != nil {
		return actor.Actor{}, err
	}
	now := s.clock.Now()
	if !k.Usable(now) {
		return actor.Actor{}, unauthenticated()
	}
	u, err := s.users.GetByID(ctx, k.UserID)
	if err != nil || u.Status != user.StatusActive {
		return actor.Actor{}, unauthenticated()
	}
	if err := s.keys.TouchLastUsed(ctx, k.ID, now); err != nil {
		return actor.Actor{}, err
	}
	return actor.Actor{
		UserID: k.UserID, Type: actor.TypeAPIKey, ID: k.ID.String(), Label: k.Name,
		Scopes: k.Scopes, APIKeyID: k.ID, RequestID: ci.RequestID, IP: ci.IP, EmailVerified: s.verified(u),
		DangerousPolicy: k.DangerousPolicy, DeletionScheduled: u.DeletionScheduledAt != nil,
	}, nil
}

// pendingKeyError explains a key that is not an API key (yet): one minted by an agent sign-up that its owner has not
// confirmed answers KEY_PENDING_CONFIRMATION with where the mail went and until when, so the agent can tell its owner
// instead of signing up again (D-025). Anything else is UNAUTHENTICATED.
func (s *Service) pendingKeyError(ctx context.Context, hash string) error {
	if s.agents == nil {
		return unauthenticated()
	}
	row, err := s.agents.signups.PendingByKeyHash(ctx, hash, s.clock.Now())
	if errs.Is(err, errs.NotFound) {
		return unauthenticated()
	}
	if err != nil {
		return err
	}
	return errs.New(errs.KeyPendingConfirmation, "this API key is waiting for the owner's confirmation").
		WithField("email_masked", user.MaskEmail(row.Email)).
		WithField("expires_at", row.ExpiresAt.UTC().Format(time.RFC3339)).
		WithField("scopes", strings.Join(apikey.Strings(row.Scopes), " "))
}

// verified is the actor's EmailVerified: true when the owner verified the
// address or when this server does not enforce verification.
func (s *Service) verified(u *user.User) bool { return !s.requireVerified || u.EmailVerified() }

// VerificationEnforced reports whether unverified owners are restricted.
func (s *Service) VerificationEnforced() bool { return s.requireVerified }
