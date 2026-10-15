// Package errs defines the typed application error used across all layers.
// Every error that reaches the HTTP boundary is mapped to a stable code
// (see docs/ARCHITECTURE.md §4).
package errs

import (
	"errors"
	"fmt"
	"time"
)

// Code is a stable, machine-readable error code.
type Code string

const (
	Validation        Code = "VALIDATION_ERROR"
	Unauthenticated   Code = "UNAUTHENTICATED"
	Forbidden         Code = "FORBIDDEN"
	InsufficientScope Code = "INSUFFICIENT_SCOPE"
	EmailNotVerified  Code = "EMAIL_NOT_VERIFIED"
	// ReauthRequired: the action needs a fresh sign-in (a user without a password cannot confirm with one).
	ReauthRequired Code = "REAUTH_REQUIRED"
	// KeyPendingConfirmation: the API key comes from an agent sign-up its owner has not confirmed yet (D-025).
	KeyPendingConfirmation Code = "KEY_PENDING_CONFIRMATION"
	QuotaExceeded          Code = "QUOTA_EXCEEDED"
	ApprovalRequired       Code = "APPROVAL_REQUIRED"
	NotFound               Code = "NOT_FOUND"
	InvalidStateTransition Code = "INVALID_STATE_TRANSITION"
	Conflict               Code = "CONFLICT"
	RateLimited            Code = "RATE_LIMITED"
	SocialAccountExpired   Code = "SOCIAL_ACCOUNT_EXPIRED"
	ProviderNotAvailable   Code = "PROVIDER_NOT_AVAILABLE"
	ProviderError          Code = "PROVIDER_ERROR"
	Internal               Code = "INTERNAL"
)

// Error is the canonical error type.
type Error struct {
	Code    Code
	Message string
	// Fields carries per-field validation messages (optional).
	Fields map[string]string
	// Transient marks a conflict that nothing in the request caused (a database deadlock): the same request can be
	// repeated at once. The HTTP layer adds Retry-After.
	Transient bool
	// RetryAfter, when positive, is how long the caller should wait before repeating a RATE_LIMITED request.
	RetryAfter time.Duration
	cause      error
}

func (e *Error) Error() string {
	if e.cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Message, e.cause)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error { return e.cause }

// New creates an error with a code and message.
func New(code Code, msg string) *Error { return &Error{Code: code, Message: msg} }

// Newf creates an error with a formatted message.
func Newf(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Wrap attaches a cause (never shown to clients) to a coded error.
func Wrap(code Code, msg string, cause error) *Error {
	return &Error{Code: code, Message: msg, cause: cause}
}

// AsTransient marks the error as safe to repeat immediately.
func (e *Error) AsTransient() *Error {
	e.Transient = true
	return e
}

// WithRetryAfter sets how long a rate-limited caller should wait.
func (e *Error) WithRetryAfter(d time.Duration) *Error {
	e.RetryAfter = d
	return e
}

// WithField adds a field-level validation message.
func (e *Error) WithField(field, msg string) *Error {
	if e.Fields == nil {
		e.Fields = map[string]string{}
	}
	e.Fields[field] = msg
	return e
}

// As returns the *Error in the chain, if any.
func As(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// CodeOf returns the code of err, or Internal when err is not an *Error.
func CodeOf(err error) Code {
	if e, ok := As(err); ok {
		return e.Code
	}
	return Internal
}

// Is reports whether err carries the given code.
func Is(err error, code Code) bool { return err != nil && CodeOf(err) == code }

// Validationf is a shortcut for validation errors.
func Validationf(format string, args ...any) *Error { return Newf(Validation, format, args...) }

// NotFoundf is a shortcut for not-found errors.
func NotFoundf(resource string) *Error { return Newf(NotFound, "%s not found", resource) }
