// Package approvals lets the owner approve or deny dangerous actions that an
// API key attempted, and lets use cases consume such an approval once (D-013).
package approvals

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/socialos/backend/internal/application/port"
	"github.com/socialos/backend/internal/domain/actor"
	"github.com/socialos/backend/internal/domain/approval"
	"github.com/socialos/backend/internal/domain/audit"
	"github.com/socialos/backend/internal/domain/errs"
)

// Defaults for Config.
const (
	DefaultTTL        = 10 * time.Minute
	DefaultMaxPending = 10
)

// Config tunes the service.
type Config struct {
	TTL time.Duration
	// MaxPending caps the open approvals of one key, so a noisy key cannot block the others.
	MaxPending int
	// WebBaseURL builds the approve link given to the agent.
	WebBaseURL string
}

// Deps bundles dependencies.
type Deps struct {
	Repo   Repo
	Tx     port.TxRunner
	Audit  port.AuditRecorder
	Clock  port.Clock
	Config Config
}

// Service implements port.ApprovalGate and the owner-facing approval use cases.
type Service struct {
	repo  Repo
	tx    port.TxRunner
	audit port.AuditRecorder
	clock port.Clock
	cfg   Config
}

var _ port.ApprovalGate = (*Service)(nil)

// NewService creates the service.
func NewService(d Deps) *Service {
	if d.Config.TTL <= 0 {
		d.Config.TTL = DefaultTTL
	}
	if d.Config.MaxPending <= 0 {
		d.Config.MaxPending = DefaultMaxPending
	}
	return &Service{repo: d.Repo, tx: d.Tx, audit: d.Audit, clock: d.Clock, cfg: d.Config}
}

func binding(a actor.Actor, req approval.Request) Binding {
	return Binding{ActorType: string(a.Type), ActorID: a.ID, Action: req.Action, ResourceType: req.ResourceType,
		ResourceID: req.ResourceID, Fingerprint: req.Fingerprint}
}

func bindingOf(a *approval.Approval) Binding {
	return Binding{ActorType: a.ActorType, ActorID: a.ActorID, Action: a.Action, ResourceType: a.ResourceType,
		ResourceID: a.ResourceID, Fingerprint: a.Fingerprint}
}

// Require implements port.ApprovalGate: nil, or *port.ApprovalNeeded for the caller to pass to Open once its
// transaction is over.
func (s *Service) Require(ctx context.Context, a actor.Actor, req approval.Request) error {
	if !a.NeedsApproval(req.Action) {
		return nil
	}
	now := s.clock.Now()
	b := binding(a, req)
	if id, ok := approval.IDFrom(ctx); ok {
		used, err := s.repo.Consume(ctx, a.UserID, id, b, now)
		if err != nil {
			return err
		}
		if used {
			return s.audit.Record(ctx, a, audit.ActionApprovalUsed, "approval", id.String(), auditMeta(req))
		}
		if err := s.refuseIfDenied(ctx, a, id, b); err != nil {
			return err
		}
	}
	return &port.ApprovalNeeded{Actor: a, Request: req}
}

// refuseIfDenied tells an agent that the owner said no, so it stops asking. Any other mismatch (wrong target, reuse,
// expiry, another tenant's id) gets a fresh request and no hint about why; a failing lookup is an error, not a hint.
func (s *Service) refuseIfDenied(ctx context.Context, a actor.Actor, id uuid.UUID, b Binding) error {
	ap, err := s.repo.Get(ctx, a.UserID, id)
	if errs.Is(err, errs.NotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if ap.Status == approval.StatusDenied && bindingOf(ap) == b {
		return errs.New(errs.Forbidden, "the owner denied this action")
	}
	return nil
}

// Open implements port.ApprovalGate: it answers 428 with the open approval for the request, creating it when there is
// none. The lookup, the cap check and the insert run in one transaction under a lock on (user, key): identical first
// calls from parallel requests produce one row, and the cap holds.
func (s *Service) Open(ctx context.Context, n *port.ApprovalNeeded) error {
	a, req := n.Actor, n.Request
	now := s.clock.Now()
	b := binding(a, req)
	var ap *approval.Approval
	err := s.tx.InTx(ctx, func(ctx context.Context) error {
		if err := s.repo.LockActor(ctx, a.UserID, a.ID); err != nil {
			return err
		}
		open, err := s.repo.FindPending(ctx, a.UserID, b, now)
		if err == nil {
			ap = open
			return nil
		}
		if !errs.Is(err, errs.NotFound) {
			return err
		}
		ap, err = s.create(ctx, a, req, now)
		return err
	})
	if err != nil {
		return err
	}
	return errs.New(errs.ApprovalRequired, "this action needs the owner's approval").
		WithField("approval_id", ap.ID.String()).
		WithField("approve_url", s.cfg.WebBaseURL+"/approvals").
		WithField("expires_at", ap.ExpiresAt.UTC().Format(time.RFC3339)).
		WithField("action", string(ap.Action))
}

func (s *Service) create(ctx context.Context, a actor.Actor, req approval.Request, now time.Time) (*approval.Approval, error) {
	n, err := s.repo.CountPending(ctx, a.UserID, a.ID, now)
	if err != nil {
		return nil, err
	}
	if n >= s.cfg.MaxPending {
		return nil, errs.New(errs.RateLimited, "too many pending approvals for this key; ask the owner to decide on them first")
	}
	ap := &approval.Approval{ID: uuid.New(), UserID: a.UserID, ActorType: string(a.Type), ActorID: a.ID, ActorLabel: a.Label,
		Action: req.Action, ResourceType: req.ResourceType, ResourceID: req.ResourceID, Fingerprint: req.Fingerprint,
		Summary: req.Summary, Status: approval.StatusPending, ExpiresAt: now.Add(s.cfg.TTL), CreatedAt: now}
	if err := s.repo.Create(ctx, ap); err != nil {
		return nil, err
	}
	return ap, s.audit.Record(ctx, a, audit.ActionApprovalRequested, "approval", ap.ID.String(), auditMeta(req))
}

func auditMeta(req approval.Request) map[string]any {
	return map[string]any{"action": string(req.Action), "resource_type": req.ResourceType, "resource_id": req.ResourceID}
}

// Purge deletes approvals that were decided, used or expired before now-retention, and reports how many.
func (s *Service) Purge(ctx context.Context, retention time.Duration) (int64, error) {
	return s.repo.DeleteDecidedBefore(ctx, s.clock.Now().Add(-retention))
}
