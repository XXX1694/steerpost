package auth

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/socialos/backend/internal/adapters/mail"
	"github.com/socialos/backend/internal/domain/actor"
	"github.com/socialos/backend/internal/domain/audit"
	"github.com/socialos/backend/internal/domain/emailtoken"
	"github.com/socialos/backend/internal/domain/errs"
	"github.com/socialos/backend/internal/domain/user"
	"github.com/socialos/backend/internal/infrastructure/crypto"
)

// maxRawToken bounds what is hashed and looked up; real tokens are 43 characters.
const maxRawToken = 128

func invalidLink() error { return errs.Validationf("link is invalid or has expired") }

// link builds a mailed link. The token goes in the URL fragment, which browsers
// never send to servers or in the Referer header, so it stays out of access logs.
func (s *Service) link(path, rawToken string) string {
	return s.webURL + path + "#token=" + rawToken
}

func humanTTL(d time.Duration) string {
	if d >= time.Hour {
		return fmt.Sprintf("%d hours", int(d.Hours()))
	}
	return fmt.Sprintf("%d minutes", int(d.Minutes()))
}

// issueToken stores a fresh token (retiring older ones) and queues the mail.
// The raw token only ever lives in the rendered message.
func (s *Service) issueToken(ctx context.Context, u *user.User, p emailtoken.Purpose, tmpl, path string) error {
	raw, err := crypto.RandomToken(32)
	if err != nil {
		return err
	}
	now := s.clock.Now()
	t := &emailtoken.Token{UserID: u.ID, Purpose: p, Hash: crypto.SHA256Hex(raw), Email: u.Email,
		ExpiresAt: now.Add(p.TTL()), CreatedAt: now}
	if err := s.tokens.Create(ctx, t); err != nil {
		return err
	}
	msg, err := mail.Render(tmpl, u.Email, mail.Data{Link: s.link(path, raw), ExpiresIn: humanTTL(p.TTL())})
	if err != nil {
		return err
	}
	msg.TokenID = t.ID.String()
	return s.mail.Enqueue(ctx, msg)
}

// IssueVerification mails a verification link. It never fails the caller: a
// down queue is logged and the user can use "resend".
func (s *Service) IssueVerification(ctx context.Context, u *user.User) {
	if u.EmailVerified() || s.capped(ctx, u.ID, emailtoken.VerifyEmail) {
		return
	}
	if err := s.issueToken(ctx, u, emailtoken.VerifyEmail, mail.VerifyEmail, "/verify-email"); err != nil {
		s.log.WarnContext(ctx, "verification mail not queued", slog.String("user_id", u.ID.String()), slog.Any("error", err))
	}
}

// ResendVerification mails a new link to the session user. At most one per cooldown.
func (s *Service) ResendVerification(ctx context.Context, a actor.Actor) error {
	if err := a.RequireSession(); err != nil {
		return err
	}
	u, err := s.users.GetByID(ctx, a.UserID)
	if err != nil {
		return err
	}
	if u.EmailVerified() {
		return errs.New(errs.Conflict, "email is already verified")
	}
	if s.coolingDown(ctx, u.ID, emailtoken.VerifyEmail) {
		return errs.New(errs.RateLimited, "a verification email was sent a moment ago; wait a minute and try again")
	}
	if s.capped(ctx, u.ID, emailtoken.VerifyEmail) {
		return nil // silently dropped: at most MaxPerDay mails a day
	}
	return s.issueToken(ctx, u, emailtoken.VerifyEmail, mail.VerifyEmail, "/verify-email")
}

func (s *Service) coolingDown(ctx context.Context, uid uuid.UUID, p emailtoken.Purpose) bool {
	last, err := s.tokens.LatestCreatedAt(ctx, uid, p)
	return err == nil && !last.IsZero() && s.clock.Now().Sub(last) < emailtoken.Cooldown
}

// capped reports whether the user already received emailtoken.MaxPerDay tokens of this purpose in the last
// day. Callers drop the request silently, so mail cannot be used to flood an inbox.
func (s *Service) capped(ctx context.Context, uid uuid.UUID, p emailtoken.Purpose) bool {
	n, err := s.tokens.CountSince(ctx, uid, p, s.clock.Now().Add(-emailtoken.Window))
	return err == nil && n >= emailtoken.MaxPerDay
}

// VerifyEmail redeems a verification token once.
func (s *Service) VerifyEmail(ctx context.Context, rawToken string, ci ClientInfo) error {
	if rawToken == "" || len(rawToken) > maxRawToken {
		return invalidLink()
	}
	return s.tx.InTx(ctx, func(ctx context.Context) error {
		now := s.clock.Now()
		t, err := s.tokens.Consume(ctx, emailtoken.VerifyEmail, crypto.SHA256Hex(rawToken), now)
		if errs.Is(err, errs.NotFound) {
			return invalidLink()
		}
		if err != nil {
			return err
		}
		u, err := s.users.GetByID(ctx, t.UserID)
		if err != nil {
			return err
		}
		if !strings.EqualFold(u.Email, t.Email) {
			return invalidLink() // the address changed after the mail went out
		}
		if err := s.users.MarkEmailVerified(ctx, u.ID, now); err != nil {
			return err
		}
		return s.audit.Record(ctx, userActor(u, ci), audit.ActionEmailVerified, "user", u.ID.String(), nil)
	})
}

// ForgotPassword is the HTTP side of "I forgot my password". It only validates the input and queues the
// request, so it does the same work, and takes the same time, for a registered address and for any other.
// The lookup, cooldown and mail happen in ProcessForgot on the worker. A malformed address is dropped
// silently, and the caller answers 202 either way.
func (s *Service) ForgotPassword(ctx context.Context, email string) error {
	norm, err := user.NormalizeEmail(email)
	if err != nil {
		return nil
	}
	return s.forgot.EnqueueForgot(ctx, norm)
}

// ProcessForgot is the worker side of ForgotPassword: it mails a reset link when the address belongs to an
// active account that is neither in its cooldown nor over its daily cap. Everything else is a silent no-op.
// Only a failed lookup is returned, so the task is retried.
func (s *Service) ProcessForgot(ctx context.Context, email string) error {
	u, err := s.users.GetByEmail(ctx, email)
	if errs.Is(err, errs.NotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if u.Status != user.StatusActive || s.coolingDown(ctx, u.ID, emailtoken.ResetPassword) || s.capped(ctx, u.ID, emailtoken.ResetPassword) {
		return nil
	}
	if err := s.issueToken(ctx, u, emailtoken.ResetPassword, mail.ResetPassword, "/reset-password"); err != nil {
		s.log.WarnContext(ctx, "reset mail not queued", slog.String("user_id", u.ID.String()), slog.Any("error", err))
	}
	return nil
}

// RetireMailToken invalidates the token behind a mail that could not be delivered, so a message stuck in the
// queue's archive never holds a live link. tokenID is the token row id, not the raw token.
func (s *Service) RetireMailToken(ctx context.Context, tokenID string) error {
	id, err := uuid.Parse(tokenID)
	if err != nil {
		return nil
	}
	if err := s.tokens.RetireByID(ctx, id, s.clock.Now()); err != nil {
		return err
	}
	// An agent sign-up mail carries the request's id instead (ids are random UUIDs, so each table finds only its
	// own): the request is dropped, so its key can never be confirmed through a link stuck in the archive.
	if s.agents == nil {
		return nil
	}
	return s.agents.signups.DeleteByID(ctx, id)
}

// ResetPassword redeems a reset token, sets the password and signs the user out everywhere.
func (s *Service) ResetPassword(ctx context.Context, rawToken, newPassword string, revokeKeys bool, ci ClientInfo) error {
	if rawToken == "" || len(rawToken) > maxRawToken {
		return invalidLink()
	}
	if err := user.ValidatePassword(newPassword); err != nil {
		return err // before consuming: a weak password must not burn the link
	}
	var u *user.User
	err := s.tx.InTx(ctx, func(ctx context.Context) error {
		now := s.clock.Now()
		t, err := s.tokens.Consume(ctx, emailtoken.ResetPassword, crypto.SHA256Hex(rawToken), now)
		if errs.Is(err, errs.NotFound) {
			return invalidLink()
		}
		if err != nil {
			return err
		}
		if u, err = s.users.GetByID(ctx, t.UserID); err != nil {
			return err
		}
		if u.Status != user.StatusActive || !strings.EqualFold(u.Email, t.Email) {
			return invalidLink()
		}
		revoked, keys, err := s.replacePassword(ctx, u.ID, newPassword, uuid.Nil, revokeKeys)
		if err != nil {
			return err
		}
		// Opening the mailed link proves control of the address.
		if err := s.users.MarkEmailVerified(ctx, u.ID, now); err != nil {
			return err
		}
		return s.audit.Record(ctx, userActor(u, ci), audit.ActionPasswordReset, "user", u.ID.String(),
			map[string]any{"sessions_revoked": revoked, "keys_revoked": keys, "revoke_keys": revokeKeys})
	})
	if err != nil {
		return err
	}
	s.notifyPasswordChanged(ctx, u, revokeKeys)
	return nil
}

// ChangePassword changes the session user's password after re-checking the current one.
// Every other session is signed out; the caller's stays.
func (s *Service) ChangePassword(ctx context.Context, a actor.Actor, current, next string, revokeKeys bool) error {
	if err := a.RequireSession(); err != nil {
		return err
	}
	if err := user.ValidatePassword(next); err != nil {
		return err
	}
	u, err := s.users.GetByID(ctx, a.UserID)
	if err != nil {
		return err
	}
	if !u.HasPassword() {
		return errs.New(errs.Conflict, `this account has no password yet; use "Forgot password?" on the sign-in page to set one`)
	}
	if err := s.checkPassword(ctx, u, current); err != nil {
		return err
	}
	err = s.tx.InTx(ctx, func(ctx context.Context) error {
		revoked, keys, err := s.replacePassword(ctx, u.ID, next, a.SessionID, revokeKeys)
		if err != nil {
			return err
		}
		return s.audit.Record(ctx, a, audit.ActionPasswordChanged, "user", u.ID.String(),
			map[string]any{"sessions_revoked": revoked, "keys_revoked": keys, "revoke_keys": revokeKeys})
	})
	if err != nil {
		return err
	}
	s.notifyPasswordChanged(ctx, u, revokeKeys)
	return nil
}

// replacePassword stores the new hash, retires pending reset links and deletes every session of the user
// except `keep` (uuid.Nil keeps none). With revokeKeys it also revokes every API key and MCP connection;
// otherwise those survive, which the notice mail and the UI say. It returns the sessions and keys removed.
func (s *Service) replacePassword(ctx context.Context, uid uuid.UUID, password string, keep uuid.UUID, revokeKeys bool) (sessions, keys int64, err error) {
	hash, err := s.hasher.Hash(ctx, password)
	if err != nil {
		return 0, 0, err
	}
	if err := s.users.SetPassword(ctx, uid, hash); err != nil {
		return 0, 0, err
	}
	if err := s.tokens.RetireAll(ctx, uid, emailtoken.ResetPassword, s.clock.Now()); err != nil {
		return 0, 0, err
	}
	if sessions, err = s.sessions.DeleteAllForUser(ctx, uid, keep); err != nil {
		return 0, 0, err
	}
	if revokeKeys {
		if keys, err = s.keys.RevokeAllForUser(ctx, uid, s.clock.Now()); err != nil {
			return 0, 0, err
		}
	}
	return sessions, keys, nil
}

func (s *Service) notifyPasswordChanged(ctx context.Context, u *user.User, keysRevoked bool) {
	s.notify(ctx, u, mail.PasswordChanged, mail.Data{Link: s.webURL + "/developer", KeysRevoked: keysRevoked})
}
