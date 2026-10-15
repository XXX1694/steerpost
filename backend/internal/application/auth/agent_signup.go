package auth

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/socialos/backend/internal/adapters/mail"
	"github.com/socialos/backend/internal/domain/agentsignup"
	"github.com/socialos/backend/internal/domain/apikey"
	"github.com/socialos/backend/internal/domain/errs"
	"github.com/socialos/backend/internal/domain/user"
	"github.com/socialos/backend/internal/infrastructure/crypto"
)

// AgentSignupPath is the web page the confirmation mail links to.
const AgentSignupPath = "/agent-access"

// StartAgentSignupInput is POST /auth/agent-signups.
type StartAgentSignupInput struct {
	Email     string
	AgentName string
	// AcceptTerms is the agent stating that its owner agreed to the Terms; a new account still asks the owner at confirm.
	AcceptTerms bool
}

// AgentSignupStarted is the answer to the agent. RawKey is shown once and never stored.
type AgentSignupStarted struct {
	RawKey          string
	KeyPrefix       string
	Scopes          []apikey.Scope
	DangerousPolicy string
	EmailMasked     string
	ExpiresAt       time.Time
}

// StartAgentSignup is the HTTP side of an agent sign-up (D-025). It validates the input, mints the key, stores the
// request and queues it, and never looks the address up: the answer, and the work behind it, are the same whether or
// not an account uses the address. The worker decides what to mail (ProcessAgentSignup).
func (s *Service) StartAgentSignup(ctx context.Context, in StartAgentSignupInput) (*AgentSignupStarted, error) {
	if s.agents == nil {
		return nil, errAgentSignupOff()
	}
	email, name, err := validateAgentSignup(in)
	if err != nil {
		return nil, err
	}
	gen, err := crypto.GenerateAPIKey()
	if err != nil {
		return nil, err
	}
	now := s.clock.Now()
	row := &agentsignup.Signup{Email: email, AgentName: name, KeyPrefix: gen.Prefix, KeyHash: gen.Hash,
		Scopes: agentsignup.Scopes(), DangerousPolicy: agentsignup.Policy, ExpiresAt: now.Add(agentsignup.TTL), CreatedAt: now}
	if err := s.agents.signups.Create(ctx, row); err != nil {
		return nil, err
	}
	if err := s.agents.queue.EnqueueAgentSignup(ctx, row.ID); err != nil {
		// Without the task nobody is ever mailed, so the key could never work: drop the row and let the agent retry.
		if derr := s.agents.signups.DeleteByID(ctx, row.ID); derr != nil {
			s.log.WarnContext(ctx, "agent sign-up row not removed", slog.String("signup_id", row.ID.String()), slog.Any("error", derr))
		}
		return nil, err
	}
	return &AgentSignupStarted{RawKey: gen.Raw, KeyPrefix: gen.Prefix, Scopes: row.Scopes, DangerousPolicy: row.DangerousPolicy,
		EmailMasked: user.MaskEmail(email), ExpiresAt: row.ExpiresAt}, nil
}

// validateAgentSignup checks every field and reports all problems in one VALIDATION_ERROR, so an agent fixes them in
// one round trip.
func validateAgentSignup(in StartAgentSignupInput) (email, name string, err error) {
	out := errs.Validationf("check the highlighted fields")
	email, emailErr := user.NormalizeEmail(in.Email)
	if emailErr != nil {
		out = out.WithField("email", "invalid email")
	}
	name, nameErr := agentsignup.ValidateAgentName(in.AgentName)
	if e, ok := errs.As(nameErr); ok {
		out = out.WithField("agent_name", e.Fields["agent_name"])
	}
	if !in.AcceptTerms {
		out = out.WithField("accept_terms", "the owner must accept the Terms and the Privacy Policy")
	}
	if len(out.Fields) > 0 {
		return "", "", out
	}
	return email, name, nil
}

// ProcessAgentSignup is the worker side of StartAgentSignup: it mails the confirmation link, in the new-account or the
// existing-account variant. Nothing is mailed for an account that is disabled, deleted or scheduled for deletion, or
// once the address got agentsignup.MaxPerEmailPerDay mails in a day; the request then simply expires. Only failed
// lookups and a failed enqueue are returned, so the task is retried.
func (s *Service) ProcessAgentSignup(ctx context.Context, id uuid.UUID) error {
	if s.agents == nil {
		return nil
	}
	now := s.clock.Now()
	row, err := s.agents.signups.Get(ctx, id)
	if errs.Is(err, errs.NotFound) || (err == nil && !row.Live(now)) {
		return nil
	}
	if err != nil {
		return err
	}
	u, err := s.users.GetByEmail(ctx, row.Email)
	newAccount := errs.Is(err, errs.NotFound)
	if err != nil && !newAccount {
		return err
	}
	if !newAccount && !usable(u) {
		return nil
	}
	return s.mailAgentSignup(ctx, row, newAccount)
}

// mailAgentSignup claims one of the address's daily mail slots and the request's single token, then queues the mail.
// The count and the claim run in one transaction under a per-address lock, so a burst processed by several workers at
// once cannot pass the cap together; a request that already has a token (a redelivered task) is not mailed again.
func (s *Service) mailAgentSignup(ctx context.Context, row *agentsignup.Signup, newAccount bool) error {
	raw, err := crypto.RandomToken(32)
	if err != nil {
		return err
	}
	hash := crypto.SHA256Hex(raw)
	var claimed bool
	err = s.tx.InTx(ctx, func(ctx context.Context) error {
		if err := s.agents.signups.LockEmail(ctx, row.Email); err != nil {
			return err
		}
		sent, err := s.agents.signups.CountMailedSince(ctx, row.Email, s.clock.Now().Add(-agentsignup.Window))
		if err != nil || sent >= agentsignup.MaxPerEmailPerDay {
			return err
		}
		claimed, err = s.agents.signups.SetTokenHash(ctx, row.ID, hash)
		return err
	})
	if err != nil || !claimed {
		return err
	}
	msg, err := mail.Render(mail.AgentAccess, row.Email, mail.Data{Link: s.link(AgentSignupPath, raw),
		ExpiresIn: humanTTL(agentsignup.TTL), AgentName: row.AgentName, NewAccount: newAccount})
	if err == nil {
		// The row id lets the mail worker drop the request if the mail can never be delivered (RetireMailToken).
		msg.TokenID = row.ID.String()
		err = s.mail.Enqueue(ctx, msg)
	}
	if err != nil {
		// Nothing was queued: give the token back so the retried task can mail a fresh link.
		if cerr := s.agents.signups.ClearTokenHash(ctx, row.ID, hash); cerr != nil {
			s.log.WarnContext(ctx, "agent sign-up token not released", slog.String("signup_id", row.ID.String()), slog.Any("error", cerr))
		}
		return err
	}
	return nil
}

// PurgeExpiredAgentSignups removes requests nobody confirmed in time (called by the worker).
func (s *Service) PurgeExpiredAgentSignups(ctx context.Context) (int64, error) {
	if s.agents == nil {
		return 0, nil
	}
	return s.agents.signups.DeleteExpired(ctx, s.clock.Now())
}
