package auth

import (
	"context"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/socialos/backend/internal/domain/actor"
	"github.com/socialos/backend/internal/domain/agentsignup"
	"github.com/socialos/backend/internal/domain/apikey"
	"github.com/socialos/backend/internal/domain/audit"
	"github.com/socialos/backend/internal/domain/errs"
	"github.com/socialos/backend/internal/domain/terms"
	"github.com/socialos/backend/internal/domain/user"
)

// AgentSignupPreview is what the confirmation page shows before the owner decides.
type AgentSignupPreview struct {
	AgentName       string
	Email           string
	Scopes          []apikey.Scope
	DangerousPolicy string
	AccountExists   bool
	ExpiresAt       time.Time
}

// ConfirmAgentSignupInput is POST /auth/agent-signups/confirm.
type ConfirmAgentSignupInput struct {
	Token string
	// AcceptTerms and DisplayName are used only when the confirmation creates the account.
	AcceptTerms bool
	DisplayName string
}

// AgentSignupConfirmed is the confirmed owner. Session is set only when the confirmation created the account.
type AgentSignupConfirmed struct {
	User           *user.User
	AccountCreated bool
	Session        IssuedSession
}

// liveAgentSignup reads the live request behind a mailed token without using it.
func (s *Service) liveAgentSignup(ctx context.Context, rawToken string) (*agentsignup.Signup, string, error) {
	h, ok := ticketHash(rawToken)
	if !ok || s.agents == nil {
		return nil, "", invalidLink()
	}
	row, err := s.agents.signups.ByToken(ctx, h, s.clock.Now())
	if errs.Is(err, errs.NotFound) {
		return nil, "", invalidLink()
	}
	return row, h, err
}

// PreviewAgentSignup tells the holder of a confirmation link what confirming it would do.
func (s *Service) PreviewAgentSignup(ctx context.Context, rawToken string) (*AgentSignupPreview, error) {
	row, _, err := s.liveAgentSignup(ctx, rawToken)
	if err != nil {
		return nil, err
	}
	_, err = s.users.GetByEmail(ctx, row.Email)
	if err != nil && !errs.Is(err, errs.NotFound) {
		return nil, err
	}
	return &AgentSignupPreview{AgentName: row.AgentName, Email: row.Email, Scopes: row.Scopes, DangerousPolicy: row.DangerousPolicy,
		AccountExists: err == nil, ExpiresAt: row.ExpiresAt}, nil
}

// ConfirmAgentSignup turns a confirmed request into a working key. For an address without an account it creates one,
// signs it in and stores the Terms acceptance (D-016); the link proves control of the address, so it counts as
// verified. An existing account is never signed in by the link: the caller must already hold that account's session.
// Every refusal leaves the link usable, and the request is redeemed in the same transaction as the key, so a failed
// confirmation can be repeated and two racing ones cannot both create a key.
func (s *Service) ConfirmAgentSignup(ctx context.Context, a actor.Actor, in ConfirmAgentSignupInput, ci ClientInfo) (*AgentSignupConfirmed, error) {
	if a.Type == actor.TypeAPIKey {
		return nil, errs.New(errs.Forbidden, "this action requires a browser session")
	}
	row, h, err := s.liveAgentSignup(ctx, in.Token)
	if err != nil {
		return nil, err
	}
	u, err := s.users.GetByEmail(ctx, row.Email)
	if errs.Is(err, errs.NotFound) {
		return s.confirmNewAccount(ctx, a, h, in, ci)
	}
	if err != nil {
		return nil, err
	}
	if err := ownerMayConfirm(a, u); err != nil {
		return nil, err
	}
	err = s.tx.InTx(ctx, func(ctx context.Context) error {
		row, err := s.consumeAgentSignup(ctx, h, u.Email)
		if err != nil {
			return err
		}
		if err := s.users.MarkEmailVerified(ctx, u.ID, s.clock.Now()); err != nil {
			return err
		}
		return s.activateAgentKey(ctx, row, u, ci, false)
	})
	if err != nil {
		return nil, err
	}
	return &AgentSignupConfirmed{User: u}, nil
}

// ownerMayConfirm checks that the caller is signed in as the account the link was sent to, and that it can take a key.
func ownerMayConfirm(a actor.Actor, u *user.User) error {
	if !a.IsSession() {
		return errs.New(errs.Unauthenticated, "sign in as the account this link was sent to, then open the link again")
	}
	if a.UserID != u.ID {
		return errs.New(errs.Forbidden, "sign in as the account this link was sent to")
	}
	if !usable(u) {
		return errs.New(errs.Conflict, "this account is disabled or scheduled for deletion; it cannot give an agent access")
	}
	return nil
}

func (s *Service) confirmNewAccount(ctx context.Context, a actor.Actor, h string, in ConfirmAgentSignupInput, ci ClientInfo) (*AgentSignupConfirmed, error) {
	if a.UserID != uuid.Nil {
		return nil, errs.New(errs.Conflict, "you are signed in to another account; sign out, then open the link again")
	}
	if !in.AcceptTerms {
		return nil, errs.Validationf("you must accept the Terms and the Privacy Policy").WithField("accept_terms", "must be accepted")
	}
	name, err := user.ValidateDisplayName(in.DisplayName)
	if err != nil {
		return nil, err
	}
	now := s.clock.Now()
	out := &AgentSignupConfirmed{AccountCreated: true}
	err = s.tx.InTx(ctx, func(ctx context.Context) error {
		row, err := s.consumeAgentSignup(ctx, h, "")
		if err != nil {
			return err
		}
		u := &user.User{Email: row.Email, DisplayName: name, Status: user.StatusActive, Plan: user.DefaultPlan,
			TermsAcceptedAt: &now, TermsVersion: terms.CurrentVersion}
		if err := s.users.Create(ctx, u); err != nil {
			return conflictFor(err, "an account with this email already exists; sign in, then open the link again")
		}
		if err := s.users.MarkEmailVerified(ctx, u.ID, now); err != nil {
			return err
		}
		u.EmailVerifiedAt = &now
		if out.Session, err = s.startSession(ctx, u, ci, audit.ActionUserRegistered, map[string]any{"method": "agent_signup"}); err != nil {
			return err
		}
		out.User = u
		return s.activateAgentKey(ctx, row, u, ci, true)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// consumeAgentSignup redeems the request. email, when set, must still be the request's address (the account's address
// may have changed between the lookup and now).
func (s *Service) consumeAgentSignup(ctx context.Context, h, email string) (*agentsignup.Signup, error) {
	row, err := s.agents.signups.ConsumeByToken(ctx, h, s.clock.Now())
	if errs.Is(err, errs.NotFound) || (err == nil && email != "" && !strings.EqualFold(row.Email, email)) {
		return nil, invalidLink()
	}
	return row, err
}

// activateAgentKey stores the key with the hash the agent's raw key already matches, so that key starts working, and
// the MCP connection the owner sees and revokes under Developer.
func (s *Service) activateAgentKey(ctx context.Context, row *agentsignup.Signup, u *user.User, ci ClientInfo, created bool) error {
	k := &apikey.Key{ID: uuid.New(), UserID: u.ID, Name: "MCP: " + row.AgentName, Prefix: row.KeyPrefix, KeyHash: row.KeyHash,
		Scopes: row.Scopes, DangerousPolicy: row.DangerousPolicy}
	if err := s.agents.keys.CreateKey(ctx, k); err != nil {
		return err
	}
	c := AgentConnection{ID: uuid.New(), UserID: u.ID, APIKeyID: k.ID, Name: row.AgentName, ClientName: row.AgentName}
	if err := s.agents.keys.CreateConnection(ctx, c); err != nil {
		return err
	}
	who := userActor(u, ci)
	if err := s.audit.Record(ctx, who, audit.ActionAPIKeyCreated, "api_key", k.ID.String(), map[string]any{"name": k.Name,
		"scopes": apikey.Strings(k.Scopes), "prefix": k.Prefix, "dangerous_policy": k.DangerousPolicy, "method": "agent_signup"}); err != nil {
		return err
	}
	return s.audit.Record(ctx, who, audit.ActionAgentSignupConfirmed, "mcp_connection", c.ID.String(),
		map[string]any{"agent_name": row.AgentName, "key_prefix": row.KeyPrefix, "account_created": created})
}

// DeclineAgentSignup deletes the request behind a link ("Not me"): the agent's key will never work.
func (s *Service) DeclineAgentSignup(ctx context.Context, rawToken string) error {
	h, ok := ticketHash(rawToken)
	if !ok || s.agents == nil {
		return invalidLink()
	}
	_, err := s.agents.signups.ConsumeByToken(ctx, h, s.clock.Now())
	if errs.Is(err, errs.NotFound) {
		return invalidLink()
	}
	return err
}
