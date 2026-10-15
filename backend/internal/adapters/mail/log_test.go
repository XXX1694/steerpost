package mail

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/socialos/backend/internal/application/port"
)

func TestLogMailerHidesBodyOutsideDevelopment(t *testing.T) {
	msg := port.Message{To: "alice@example.com", Subject: "Hello", Text: "SECRET-LINK-123", HTML: "<p>SECRET-LINK-123</p>", Template: VerifyEmail}
	for _, tc := range []struct {
		env      string
		wantBody bool
	}{{"development", true}, {"production", false}, {"test", false}, {"", false}} {
		var buf bytes.Buffer
		l := NewLog(slog.New(slog.NewTextHandler(&buf, nil)), tc.env)
		if err := l.Send(context.Background(), msg); err != nil {
			t.Fatal(err)
		}
		out := buf.String()
		if got := strings.Contains(out, "SECRET-LINK-123"); got != tc.wantBody {
			t.Errorf("env %q: body logged = %v, want %v\n%s", tc.env, got, tc.wantBody, out)
		}
		if strings.Contains(out, "alice@") || !strings.Contains(out, "a***@example.com") {
			t.Errorf("env %q: recipient must be masked: %s", tc.env, out)
		}
		if !strings.Contains(out, "verify_email") || !strings.Contains(out, "Hello") {
			t.Errorf("env %q: template and subject missing: %s", tc.env, out)
		}
	}
}
