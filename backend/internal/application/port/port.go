// Package port contains cross-cutting ports shared by application use cases.
package port

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/socialos/backend/internal/domain/actor"
	"github.com/socialos/backend/internal/domain/approval"
	"github.com/socialos/backend/internal/domain/errs"
)

// TxRunner executes fn in a database transaction carried by ctx.
// Nested calls join the outer transaction.
type TxRunner interface {
	InTx(ctx context.Context, fn func(ctx context.Context) error) error
}

// Clock abstracts time for testability.
type Clock interface{ Now() time.Time }

// AuditRecorder writes audit entries (inside the caller's tx when present).
type AuditRecorder interface {
	Record(ctx context.Context, a actor.Actor, action, resourceType, resourceID string, meta map[string]any) error
}

// Encryptor seals/opens secrets at rest.
type Encryptor interface {
	Encrypt(plaintext string) (string, error)
	Decrypt(ciphertext string) (string, error)
	KeyVersion() string
}

// Page is a keyset pagination request.
type Page struct {
	Limit  int
	Cursor *Cursor
}

// Cursor is a keyset position (created_at, id) for descending lists.
type Cursor struct {
	At time.Time
	ID uuid.UUID
}

// Default and maximum page sizes.
const (
	DefaultLimit = 20
	MaxLimit     = 100
)

// NewPage validates limit/cursor query values.
func NewPage(limit int, cursor string) (Page, error) {
	if limit <= 0 {
		limit = DefaultLimit
	}
	if limit > MaxLimit {
		limit = MaxLimit
	}
	p := Page{Limit: limit}
	if cursor == "" {
		return p, nil
	}
	c, err := DecodeCursor(cursor)
	if err != nil {
		return Page{}, err
	}
	p.Cursor = &c
	return p, nil
}

// Encode returns the opaque cursor string.
func (c Cursor) Encode() string {
	raw := c.At.UTC().Format(time.RFC3339Nano) + "|" + c.ID.String()
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// DecodeCursor parses an opaque cursor.
func DecodeCursor(s string) (Cursor, error) {
	bad := errs.Validationf("invalid cursor").WithField("cursor", "invalid")
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return Cursor{}, bad
	}
	ts, id, ok := strings.Cut(string(raw), "|")
	if !ok {
		return Cursor{}, bad
	}
	at, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return Cursor{}, bad
	}
	uid, err := uuid.Parse(id)
	if err != nil {
		return Cursor{}, bad
	}
	return Cursor{At: at, ID: uid}, nil
}

// Result is a page of items.
type Result[T any] struct {
	Items      []T
	NextCursor string
}

// Paginate trims a limit+1 fetch and computes the next cursor.
func Paginate[T any](items []T, limit int, key func(T) Cursor) Result[T] {
	if len(items) <= limit {
		return Result[T]{Items: items}
	}
	items = items[:limit]
	return Result[T]{Items: items, NextCursor: key(items[len(items)-1]).Encode()}
}

// ApprovalNeeded is the error a use case gets from ApprovalGate.Require when an API key attempts a dangerous action
// that no matching approval covers. It carries what to ask the owner. Nothing is stored yet: the use case's
// transaction rolls back first, then OpenIfNeeded turns it into a pending approval and the 428 answer. Doing it
// after the rollback (a) keeps the row, which a rollback would erase, and (b) needs no second database connection
// while the first is held, which could exhaust the pool under load (D-013).
type ApprovalNeeded struct {
	Actor   actor.Actor
	Request approval.Request
}

func (n *ApprovalNeeded) Error() string { return "approval needed for " + string(n.Request.Action) }

// ApprovalGate decides whether an actor may perform a dangerous action now. Require returns nil for browser sessions,
// background actors and trusted keys; for any other API key it consumes a matching approved approval carried in ctx
// (inside the caller's transaction) or returns *ApprovalNeeded. Open records the pending approval (outside any
// transaction of the caller) and returns the errs.ApprovalRequired answer (D-013).
type ApprovalGate interface {
	Require(ctx context.Context, a actor.Actor, req approval.Request) error
	Open(ctx context.Context, n *ApprovalNeeded) error
}

// OpenIfNeeded converts an *ApprovalNeeded found in err into the 428 answer; any other error passes through. Call it
// where a use case returns, after its transaction ended.
func OpenIfNeeded(ctx context.Context, g ApprovalGate, err error) error {
	var n *ApprovalNeeded
	if errors.As(err, &n) {
		return g.Open(ctx, n)
	}
	return err
}

// FailClosedGate is the gate used when none is wired: agents that need approval are refused.
type FailClosedGate struct{}

// Require implements ApprovalGate.
func (FailClosedGate) Require(_ context.Context, a actor.Actor, req approval.Request) error {
	if a.NeedsApproval(req.Action) {
		return errs.New(errs.Internal, "approval gate is not configured")
	}
	return nil
}

// Open implements ApprovalGate.
func (FailClosedGate) Open(context.Context, *ApprovalNeeded) error {
	return errs.New(errs.Internal, "approval gate is not configured")
}
