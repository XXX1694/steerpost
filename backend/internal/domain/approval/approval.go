// Package approval models the owner's consent to one dangerous action
// requested by an API key (see D-013).
package approval

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"time"

	"github.com/google/uuid"
)

// Action names the dangerous operation an approval covers.
type Action string

const (
	ActionPostPublish       Action = "post.publish"
	ActionPostRetryNow      Action = "post.retry_now"
	ActionPostDelete        Action = "post.delete"
	ActionAccountDisconnect Action = "social_account.disconnect"
	ActionAccountConnect    Action = "social_account.connect_token"
	ActionPostScheduleSoon  Action = "post.schedule_soon"
)

// Status is the lifecycle state of an approval.
type Status string

const (
	StatusPending  Status = "pending"
	StatusApproved Status = "approved"
	StatusDenied   Status = "denied"
	StatusConsumed Status = "consumed"
	// StatusExpired is reported, never stored: a pending or approved row past its deadline reads as expired.
	StatusExpired Status = "expired"
)

// Policy is a credential's dangerous_policy.
const (
	PolicyApprove = "approve"
	PolicyTrusted = "trusted"
	// PolicyTrustedConnect skips the approval for connecting a token network only; every other dangerous action still
	// needs the owner. Only agent sign-up keys get it (D-025).
	PolicyTrustedConnect = "trusted_connect"
)

// ValidPolicy reports whether p is a policy the owner may choose for a new key. trusted_connect is not one: it is given
// only by the agent sign-up flow.
func ValidPolicy(p string) bool { return p == PolicyApprove || p == PolicyTrusted }

// KnownPolicy reports whether p is any stored dangerous_policy.
func KnownPolicy(p string) bool { return ValidPolicy(p) || p == PolicyTrustedConnect }

// Approval is one stored consent record. It is bound to the credential, the action, the target and a fingerprint of
// the payload, so it cannot be replayed for anything else.
type Approval struct {
	ID           uuid.UUID
	UserID       uuid.UUID
	ActorType    string
	ActorID      string
	ActorLabel   string
	Action       Action
	ResourceType string
	ResourceID   string
	Fingerprint  string
	Summary      map[string]any
	Status       Status
	ExpiresAt    time.Time
	DecidedAt    *time.Time
	ConsumedAt   *time.Time
	CreatedAt    time.Time
}

// EffectiveStatus reports expired for a pending or approved row past its deadline.
func (a Approval) EffectiveStatus(now time.Time) Status {
	if (a.Status == StatusPending || a.Status == StatusApproved) && !now.Before(a.ExpiresAt) {
		return StatusExpired
	}
	return a.Status
}

// Request describes the action the caller wants to perform.
type Request struct {
	Action       Action
	ResourceType string
	ResourceID   string
	Fingerprint  string
	// Summary is shown to the owner. It never holds secrets.
	Summary map[string]any
}

// Fingerprint hashes the parts of a payload an approval is bound to. Parts are length-prefixed, so ("ab","c") and
// ("a","bc") differ.
func Fingerprint(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(strconv.Itoa(len(p)) + ":" + p))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// KeyedFingerprint is Fingerprint under a secret key (HMAC-SHA256): for payloads that hold a credential, so the stored
// value cannot be used to test guesses of it.
func KeyedFingerprint(key []byte, parts ...string) string {
	m := hmac.New(sha256.New, key)
	for _, p := range parts {
		m.Write([]byte(strconv.Itoa(len(p)) + ":" + p))
	}
	return hex.EncodeToString(m.Sum(nil))
}

type ctxKey struct{}

// WithID carries the approval id the caller presented (X-Approval-Id).
func WithID(ctx context.Context, id uuid.UUID) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// IDFrom returns the presented approval id, if any.
func IDFrom(ctx context.Context) (uuid.UUID, bool) {
	id, ok := ctx.Value(ctxKey{}).(uuid.UUID)
	return id, ok
}
