// Package httpx holds HTTP response helpers shared by middleware and handlers:
// JSON rendering, the uniform error envelope and request-scoped values.
package httpx

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"sync"

	"github.com/socialos/backend/internal/domain/errs"
)

type requestIDKey struct{}

// WithRequestID stores the request id in ctx.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestID returns the request id from ctx.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// JSON writes v with the given status.
func JSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if v != nil {
		_ = json.NewEncoder(w).Encode(v)
	}
}

// RetryAfterSeconds is the Retry-After sent with a RATE_LIMITED error that did not set its own.
const RetryAfterSeconds = "5"

// StatusOf maps an error code to an HTTP status (ARCHITECTURE.md §4).
func StatusOf(c errs.Code) int {
	switch c {
	case errs.Validation:
		return http.StatusBadRequest
	case errs.Unauthenticated:
		return http.StatusUnauthorized
	case errs.Forbidden, errs.InsufficientScope, errs.EmailNotVerified, errs.ReauthRequired, errs.QuotaExceeded,
		errs.KeyPendingConfirmation:
		return http.StatusForbidden
	case errs.NotFound:
		return http.StatusNotFound
	case errs.InvalidStateTransition, errs.Conflict:
		return http.StatusConflict
	case errs.RateLimited:
		return http.StatusTooManyRequests
	case errs.ApprovalRequired:
		return http.StatusPreconditionRequired
	case errs.SocialAccountExpired:
		return http.StatusUnprocessableEntity
	case errs.ProviderNotAvailable:
		return http.StatusNotImplemented
	case errs.ProviderError:
		return http.StatusBadGateway
	default:
		return http.StatusInternalServerError
	}
}

type errorBody struct {
	Code      errs.Code         `json:"code"`
	Message   string            `json:"message"`
	RequestID string            `json:"request_id"`
	Fields    map[string]string `json:"fields,omitempty"`
}

// Error renders the uniform error envelope. Internal errors are logged and
// replaced with a generic message so no internals leak to clients.
func Error(w http.ResponseWriter, r *http.Request, err error) {
	e, ok := errs.As(err)
	if !ok {
		e = errs.Wrap(errs.Internal, "internal server error", err)
	}
	body := errorBody{Code: e.Code, Message: e.Message, RequestID: RequestID(r.Context()), Fields: e.Fields}
	if e.Code == errs.Internal {
		slog.ErrorContext(r.Context(), "internal error", slog.String("path", r.URL.Path), slog.Any("error", err))
		body.Message = "internal server error"
	}
	if e.RetryAfter > 0 && w.Header().Get("Retry-After") == "" {
		w.Header().Set("Retry-After", strconv.Itoa(int(math.Ceil(e.RetryAfter.Seconds()))))
	}
	if e.Code == errs.RateLimited && w.Header().Get("Retry-After") == "" {
		w.Header().Set("Retry-After", RetryAfterSeconds)
	}
	if e.Transient && w.Header().Get("Retry-After") == "" {
		w.Header().Set("Retry-After", "1")
	}
	if slot, ok := r.Context().Value(codeSlotKey{}).(*CodeSlot); ok {
		slot.set(e.Code)
	}
	JSON(w, StatusOf(e.Code), map[string]any{"error": body})
}

type codeSlotKey struct{}

// CodeSlot lets an outer middleware (the audit trail) learn the error code a handler rendered: request contexts only
// flow inwards, the response body is never re-read.
type CodeSlot struct {
	mu   sync.Mutex
	code errs.Code
}

// WithCodeSlot returns ctx carrying a fresh slot that Error fills in.
func WithCodeSlot(ctx context.Context) (context.Context, *CodeSlot) {
	s := &CodeSlot{}
	return context.WithValue(ctx, codeSlotKey{}, s), s
}

// Set records a code; used by the audit middleware for a handler that panicked.
func (s *CodeSlot) Set(c errs.Code) { s.set(c) }

func (s *CodeSlot) set(c errs.Code) {
	s.mu.Lock()
	s.code = c
	s.mu.Unlock()
}

// Code returns the error code rendered through Error (empty when the request succeeded).
func (s *CodeSlot) Code() errs.Code {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.code
}

// ErrorCode renders an error from a code and message.
func ErrorCode(w http.ResponseWriter, r *http.Request, code errs.Code, msg string) {
	Error(w, r, errs.New(code, msg))
}
