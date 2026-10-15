package queue

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

func TestAccountHandlersPassTheIDAndRejectBadPayloads(t *testing.T) {
	id := uuid.New()
	var got uuid.UUID
	record := func(_ context.Context, u uuid.UUID) error { got = u; return nil }
	for name, h := range map[string]struct {
		typ     string
		handler func(context.Context, *asynq.Task) error
		payload string
	}{
		"export":        {TypeAccountExport, ExportHandler(record), `{"export_id":"` + id.String() + `"}`},
		"purge":         {TypeAccountPurge, PurgeHandler(record), `{"user_id":"` + id.String() + `"}`},
		"agent sign-up": {TypeAgentSignup, AgentSignupHandler(record), `{"signup_id":"` + id.String() + `"}`},
	} {
		got = uuid.Nil
		if err := h.handler(context.Background(), asynq.NewTask(h.typ, []byte(h.payload))); err != nil || got != id {
			t.Errorf("%s: id %v err %v", name, got, err)
		}
		// A malformed payload can never succeed, so it must not be retried.
		for _, bad := range []string{`not json`, `{}`, `{"export_id":"` + uuid.Nil.String() + `","user_id":"` + uuid.Nil.String() + `","signup_id":"` + uuid.Nil.String() + `"}`} {
			err := h.handler(context.Background(), asynq.NewTask(h.typ, []byte(bad)))
			if !errors.Is(err, asynq.SkipRetry) {
				t.Errorf("%s: %q gave %v, want SkipRetry", name, bad, err)
			}
		}
	}
}

func TestAccountTaskTypes(t *testing.T) {
	if TypeAccountExport != "account:export" || TypeAccountPurge != "account:purge" {
		t.Fatalf("task types changed: %s %s", TypeAccountExport, TypeAccountPurge)
	}
}
