package auth

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/socialos/backend/internal/domain/agentsignup"
	"github.com/socialos/backend/internal/domain/apikey"
	"github.com/socialos/backend/internal/domain/errs"
)

// AgentSignups persists pending agent sign-ups (D-025). Rows are not tenant data: no user owns one until it is
// confirmed, and confirming deletes it.
type AgentSignups interface {
	// Create stores a new request (CreatedAt is the application clock's, like ExpiresAt) and sets its ID.
	Create(ctx context.Context, s *agentsignup.Signup) error
	// Get reads a request by id (the worker's lookup). NOT_FOUND when it is gone.
	Get(ctx context.Context, id uuid.UUID) (*agentsignup.Signup, error)
	// LockEmail serialises mail decisions for one address until the surrounding transaction ends, so the daily cap is
	// counted and claimed in one step. It must be called inside a transaction.
	LockEmail(ctx context.Context, email string) error
	// SetTokenHash attaches the hash of the mailed confirmation token once: it reports false, and changes nothing, when
	// the request already has one (a redelivered task) or is gone.
	SetTokenHash(ctx context.Context, id uuid.UUID, tokenHash string) (bool, error)
	// ClearTokenHash removes the token hash again while it still equals tokenHash (its mail could not be queued), so
	// a retry of the task can mail a new link.
	ClearTokenHash(ctx context.Context, id uuid.UUID, tokenHash string) error
	// PendingByKeyHash finds the live request whose key has this hash. NOT_FOUND when there is none or it expired.
	PendingByKeyHash(ctx context.Context, keyHash string, now time.Time) (*agentsignup.Signup, error)
	// ByToken reads the live request of a confirmation token without using it.
	ByToken(ctx context.Context, tokenHash string, now time.Time) (*agentsignup.Signup, error)
	// ConsumeByToken atomically deletes and returns the live request of a token, so a link works exactly once.
	ConsumeByToken(ctx context.Context, tokenHash string, now time.Time) (*agentsignup.Signup, error)
	// DeleteByID removes a request (its mail could not be delivered). Missing rows are not an error.
	DeleteByID(ctx context.Context, id uuid.UUID) error
	// CountMailedSince counts the requests for the address created at or after since whose mail was sent.
	CountMailedSince(ctx context.Context, email string, since time.Time) (int, error)
	// DeleteExpired removes requests past their expiry and returns how many.
	DeleteExpired(ctx context.Context, now time.Time) (int64, error)
}

// AgentSignupQueue hands a new request to the worker, so the HTTP request does the same work for every address
// (validate, store, enqueue, answer) and its timing reveals nothing about who has an account.
type AgentSignupQueue interface {
	EnqueueAgentSignup(ctx context.Context, id uuid.UUID) error
}

// AgentConnection is the MCP connection a confirmed sign-up shows under Developer, bound to the new key.
type AgentConnection struct {
	ID, UserID, APIKeyID uuid.UUID
	Name, ClientName     string
}

// AgentKeys writes the confirmed key and its MCP connection, inside the confirming transaction.
type AgentKeys interface {
	CreateKey(ctx context.Context, k *apikey.Key) error
	CreateConnection(ctx context.Context, c AgentConnection) error
}

// AgentSignupDeps enables agent sign-up; nil in Deps leaves it off.
type AgentSignupDeps struct {
	Signups AgentSignups
	Queue   AgentSignupQueue
	Keys    AgentKeys
}

type agentState struct {
	signups AgentSignups
	queue   AgentSignupQueue
	keys    AgentKeys
}

func newAgentState(d *AgentSignupDeps) (*agentState, error) {
	if d == nil {
		return nil, nil
	}
	if d.Signups == nil || d.Queue == nil || d.Keys == nil {
		return nil, errs.New(errs.Internal, "auth: agent sign-up needs Signups, Queue and Keys")
	}
	return &agentState{signups: d.Signups, queue: d.Queue, keys: d.Keys}, nil
}

func errAgentSignupOff() error {
	return errs.New(errs.NotFound, "agent sign-up is not enabled on this server")
}
