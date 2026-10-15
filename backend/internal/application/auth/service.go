package auth

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/socialos/backend/internal/application/port"
	"github.com/socialos/backend/internal/domain/actor"
	"github.com/socialos/backend/internal/domain/audit"
	"github.com/socialos/backend/internal/domain/errs"
	"github.com/socialos/backend/internal/domain/terms"
	"github.com/socialos/backend/internal/domain/user"
)

// Service is the auth use-case facade.
type Service struct {
	users      Users
	sessions   Sessions
	keys       APIKeys
	hasher     PasswordHasher
	tx         port.TxRunner
	audit      port.AuditRecorder
	clock      port.Clock
	sessionTTL time.Duration
	dummyHash  string

	tokens EmailTokens
	mail   port.MailQueue
	forgot ForgotQueue
	log    *slog.Logger
	webURL string
	// requireVerified makes unverified owners fail RequireVerified guards.
	requireVerified bool
	// social is nil when no sign-in provider is configured.
	social *socialState
	// agents is nil when agent sign-up is off.
	agents *agentState
}

// Deps bundles Service dependencies.
type Deps struct {
	Users      Users
	Sessions   Sessions
	APIKeys    APIKeys
	Hasher     PasswordHasher
	Tx         port.TxRunner
	Audit      port.AuditRecorder
	Clock      port.Clock
	SessionTTL time.Duration

	Tokens EmailTokens
	Mail   port.MailQueue
	Forgot ForgotQueue
	Log    *slog.Logger
	// WebBaseURL is the frontend origin that mailed links point to.
	WebBaseURL string
	// RequireVerification enforces email verification (set when mail can really be delivered).
	RequireVerification bool
	// Social enables sign-in with external providers (D-023); nil leaves it off.
	Social *SocialDeps
	// AgentSignup enables sign-up by AI agents (D-025); nil leaves it off.
	AgentSignup *AgentSignupDeps
}

// NewService creates the auth service.
func NewService(d Deps) (*Service, error) {
	if d.SessionTTL == 0 {
		d.SessionTTL = 7 * 24 * time.Hour
	}
	if d.Tokens == nil || d.Mail == nil || d.Forgot == nil {
		return nil, errors.New("auth: Tokens, Mail and Forgot are required")
	}
	dummy, err := d.Hasher.Hash(context.Background(), "timing-equalizer-password")
	if err != nil {
		return nil, err
	}
	if d.Log == nil {
		d.Log = slog.Default()
	}
	svc := &Service{users: d.Users, sessions: d.Sessions, keys: d.APIKeys, hasher: d.Hasher, tx: d.Tx,
		audit: d.Audit, clock: d.Clock, sessionTTL: d.SessionTTL, dummyHash: dummy,
		tokens: d.Tokens, mail: d.Mail, forgot: d.Forgot, log: d.Log, webURL: strings.TrimRight(d.WebBaseURL, "/"),
		requireVerified: d.RequireVerification}
	if svc.agents, err = newAgentState(d.AgentSignup); err != nil {
		return nil, err
	}
	if d.Social != nil {
		if svc.social, err = newSocialState(*d.Social); err != nil {
			return nil, err
		}
	}
	return svc, nil
}

// RegisterInput is the registration payload.
type RegisterInput struct {
	Email, Password, DisplayName string
	// AcceptTerms must be true: the caller confirms the current Terms and Privacy Policy (terms.CurrentVersion).
	AcceptTerms bool
}

// Register creates a user and logs them in.
func (s *Service) Register(ctx context.Context, in RegisterInput, ci ClientInfo) (*user.User, IssuedSession, error) {
	email, err := user.NormalizeEmail(in.Email)
	if err != nil {
		return nil, IssuedSession{}, err
	}
	if err := user.ValidatePassword(in.Password); err != nil {
		return nil, IssuedSession{}, err
	}
	name, err := user.ValidateDisplayName(in.DisplayName)
	if err != nil {
		return nil, IssuedSession{}, err
	}
	if !in.AcceptTerms {
		return nil, IssuedSession{}, errs.Validationf("you must accept the Terms and the Privacy Policy").
			WithField("accept_terms", "must be accepted")
	}
	acceptedAt := s.clock.Now()
	hash, err := s.hasher.Hash(ctx, in.Password)
	if err != nil {
		return nil, IssuedSession{}, err
	}
	u := &user.User{Email: email, PasswordHash: hash, DisplayName: name, Status: user.StatusActive, Plan: user.DefaultPlan,
		TermsAcceptedAt: &acceptedAt, TermsVersion: terms.CurrentVersion}
	var issued IssuedSession
	err = s.tx.InTx(ctx, func(ctx context.Context) error {
		if err := s.users.Create(ctx, u); err != nil {
			return err
		}
		var err error
		issued, err = s.startSession(ctx, u, ci, audit.ActionUserRegistered, nil)
		return err
	})
	if err != nil {
		return nil, IssuedSession{}, err
	}
	s.IssueVerification(ctx, u)
	return u, issued, nil
}

// Login verifies credentials and issues a session. Errors never reveal whether the email exists.
func (s *Service) Login(ctx context.Context, email, password string, ci ClientInfo) (*user.User, IssuedSession, error) {
	invalid := errs.New(errs.Unauthenticated, "Wrong email or password.")
	norm, err := user.NormalizeEmail(email)
	if err != nil {
		if err := s.burnDummy(ctx, password); err != nil {
			return nil, IssuedSession{}, err
		}
		return nil, IssuedSession{}, invalid
	}
	u, err := s.users.GetByEmail(ctx, norm)
	if errs.Is(err, errs.NotFound) {
		if err := s.burnDummy(ctx, password); err != nil {
			return nil, IssuedSession{}, err
		}
		return nil, IssuedSession{}, invalid
	}
	if err != nil {
		return nil, IssuedSession{}, err
	}
	if !u.HasPassword() {
		// A social sign-up account has no hash. Spend the same time as a real check, so the answer's timing
		// does not tell which addresses belong to such accounts.
		if err := s.burnDummy(ctx, password); err != nil {
			return nil, IssuedSession{}, err
		}
		return nil, IssuedSession{}, invalid
	}
	ok, err := s.hasher.Verify(ctx, password, u.PasswordHash)
	if errs.CodeOf(err) == errs.RateLimited {
		return nil, IssuedSession{}, err
	}
	if err != nil || !ok || u.Status != user.StatusActive {
		return nil, IssuedSession{}, invalid
	}
	s.rehashIfOutdated(ctx, u, password)
	var issued IssuedSession
	err = s.tx.InTx(ctx, func(ctx context.Context) error {
		var err error
		issued, err = s.startSession(ctx, u, ci, audit.ActionUserLogin, nil)
		return err
	})
	return u, issued, err
}

// burnDummy spends the cost of a real verification for an unknown address. A saturated hasher must answer the same
// retryable error here as for a known address, otherwise load would reveal which addresses exist.
func (s *Service) burnDummy(ctx context.Context, password string) error {
	if _, err := s.hasher.Verify(ctx, password, s.dummyHash); errs.CodeOf(err) == errs.RateLimited {
		return err
	}
	return nil
}

// rehashIfOutdated upgrades a stored hash made with older argon2 parameters. It is best effort: a failure is logged and
// never fails the login that triggered it.
func (s *Service) rehashIfOutdated(ctx context.Context, u *user.User, password string) {
	if !u.HasPassword() || !s.hasher.NeedsRehash(u.PasswordHash) {
		return
	}
	verified := u.PasswordHash
	hash, err := s.hasher.Hash(ctx, password)
	if err == nil {
		// Conditional on the hash we verified against: a password reset or change that landed meanwhile wins.
		err = s.users.RehashPassword(ctx, u.ID, verified, hash)
	}
	if err != nil {
		s.log.WarnContext(ctx, "password rehash failed", slog.String("user_id", u.ID.String()), slog.Any("error", err))
	}
}

// Logout deletes the actor's session.
func (s *Service) Logout(ctx context.Context, a actor.Actor) error {
	if err := a.RequireSession(); err != nil {
		return err
	}
	return s.tx.InTx(ctx, func(ctx context.Context) error {
		if err := s.sessions.Delete(ctx, a.UserID, a.SessionID); err != nil {
			return err
		}
		return s.audit.Record(ctx, a, audit.ActionUserLogout, "user", a.UserID.String(), nil)
	})
}

// Me returns the current user.
func (s *Service) Me(ctx context.Context, a actor.Actor) (*user.User, error) {
	if a.UserID == uuid.Nil {
		return nil, errs.New(errs.Unauthenticated, "authentication required")
	}
	return s.users.GetByID(ctx, a.UserID)
}

// PurgeExpiredSessions removes expired sessions (called periodically).
func (s *Service) PurgeExpiredSessions(ctx context.Context) (int64, error) {
	return s.sessions.DeleteExpired(ctx, s.clock.Now())
}

func userActor(u *user.User, ci ClientInfo) actor.Actor {
	return actor.Actor{UserID: u.ID, Type: actor.TypeUser, ID: u.ID.String(), Label: u.Email, RequestID: ci.RequestID, IP: ci.IP}
}
