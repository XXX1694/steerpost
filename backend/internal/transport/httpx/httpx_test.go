package httpx

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/socialos/backend/internal/domain/errs"
)

func TestStatusOfCoversEveryCode(t *testing.T) {
	for code, want := range map[errs.Code]int{
		errs.Validation: 400, errs.Unauthenticated: 401, errs.Forbidden: 403, errs.InsufficientScope: 403, errs.EmailNotVerified: 403, errs.QuotaExceeded: 403, errs.KeyPendingConfirmation: 403, errs.NotFound: 404,
		errs.InvalidStateTransition: 409, errs.Conflict: 409, errs.RateLimited: 429, errs.SocialAccountExpired: 422,
		errs.ProviderNotAvailable: 501, errs.ProviderError: 502, errs.Internal: 500, errs.Code("SOMETHING_NEW"): 500,
	} {
		if got := StatusOf(code); got != want {
			t.Errorf("%s: %d, want %d", code, got, want)
		}
	}
}

func decodeErr(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content type %q", ct)
	}
	var out map[string]map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil || out["error"] == nil {
		t.Fatalf("not an error envelope: %s (%v)", rec.Body, err)
	}
	return out["error"]
}

func TestErrorEnvelope(t *testing.T) {
	r := httptest.NewRequest("POST", "/x", nil).WithContext(WithRequestID(context.Background(), "req-1"))
	rec := httptest.NewRecorder()
	Error(rec, r, errs.Validationf("bad input").WithField("title", "too long"))
	if rec.Code != 400 {
		t.Fatalf("status %d", rec.Code)
	}
	e := decodeErr(t, rec)
	f, _ := e["fields"].(map[string]any)
	if e["code"] != "VALIDATION_ERROR" || e["message"] != "bad input" || e["request_id"] != "req-1" || f["title"] != "too long" {
		t.Fatalf("%v", e)
	}
}

func TestErrorOmitsEmptyFields(t *testing.T) {
	rec := httptest.NewRecorder()
	ErrorCode(rec, httptest.NewRequest("GET", "/", nil), errs.NotFound, "nope")
	if strings.Contains(rec.Body.String(), "fields") {
		t.Fatalf("empty fields must be omitted: %s", rec.Body)
	}
	if e := decodeErr(t, rec); e["request_id"] != "" {
		t.Fatalf("request id when none set: %v", e)
	}
}

func TestInternalErrorsAreOpaque(t *testing.T) {
	for _, err := range []error{
		errors.New("pq: password authentication failed for user admin"),
		errs.Wrap(errs.Internal, "db exploded at 10.0.0.5", errors.New("SQLSTATE 08006")),
		errs.New(errs.Internal, "custom internal text"),
	} {
		rec := httptest.NewRecorder()
		Error(rec, httptest.NewRequest("GET", "/", nil), err)
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status %d", rec.Code)
		}
		e := decodeErr(t, rec)
		if e["code"] != "INTERNAL" || e["message"] != "internal server error" {
			t.Fatalf("%v", e)
		}
		if strings.Contains(rec.Body.String(), "10.0.0.5") || strings.Contains(rec.Body.String(), "SQLSTATE") || strings.Contains(rec.Body.String(), "admin") {
			t.Fatalf("internal detail leaked: %s", rec.Body)
		}
	}
}

func TestJSONWriter(t *testing.T) {
	rec := httptest.NewRecorder()
	JSON(rec, 201, map[string]any{"a": 1})
	if rec.Code != 201 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") || strings.TrimSpace(rec.Body.String()) != `{"a":1}` {
		t.Fatalf("%d %q %q", rec.Code, rec.Header().Get("Content-Type"), rec.Body)
	}
	rec = httptest.NewRecorder()
	JSON(rec, 204, nil)
	if rec.Body.Len() != 0 {
		t.Fatalf("nil body must write nothing: %q", rec.Body)
	}
}

func TestRequestIDRoundTrip(t *testing.T) {
	if RequestID(context.Background()) != "" {
		t.Fatal("no id by default")
	}
	if got := RequestID(WithRequestID(context.Background(), "abc")); got != "abc" {
		t.Fatalf("%q", got)
	}
}

func TestCodeSlotSeesTheRenderedErrorCode(t *testing.T) {
	ctx, slot := WithCodeSlot(context.Background())
	r := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
	JSON(httptest.NewRecorder(), 200, nil)
	if slot.Code() != "" {
		t.Fatalf("no error rendered yet: %q", slot.Code())
	}
	ErrorCode(httptest.NewRecorder(), r, errs.NotFound, "nope")
	if slot.Code() != errs.NotFound {
		t.Fatalf("code %q", slot.Code())
	}
}

func TestTransientConflictAsksForImmediateRetry(t *testing.T) {
	r := httptest.NewRequest("POST", "/x", nil)
	for name, tc := range map[string]struct {
		err  error
		want string
	}{
		"deadlock":   {errs.New(errs.Conflict, "retry").AsTransient(), "1"},
		"duplicate":  {errs.New(errs.Conflict, "exists"), ""},
		"transition": {errs.New(errs.InvalidStateTransition, "no"), ""},
	} {
		rec := httptest.NewRecorder()
		Error(rec, r, tc.err)
		if rec.Code != 409 || rec.Header().Get("Retry-After") != tc.want {
			t.Errorf("%s: status %d Retry-After %q, want 409 %q", name, rec.Code, rec.Header().Get("Retry-After"), tc.want)
		}
	}
}
