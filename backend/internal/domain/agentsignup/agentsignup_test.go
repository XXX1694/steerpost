package agentsignup

import (
	"strings"
	"testing"
	"time"

	"github.com/socialos/backend/internal/domain/apikey"
	"github.com/socialos/backend/internal/domain/errs"
)

func TestValidateAgentName(t *testing.T) {
	for in, want := range map[string]string{
		"Claude Desktop":        "Claude Desktop",
		"  my-agent_v1 (beta) ": "my-agent_v1 (beta)",
		"Agent (v2)":            "Agent (v2)",
		"Bob's bot":             "Bob's bot",
		"Agent... 2":            "Agent... 2",
		"O'Brien's bot":         "O'Brien's bot",
		"Агент 7":               "Агент 7",
		strings.Repeat("a", 60): strings.Repeat("a", 60),
	} {
		got, err := ValidateAgentName(in)
		if err != nil || got != want {
			t.Errorf("%q: got %q, %v", in, got, err)
		}
	}
	for _, in := range []string{"", "   ", strings.Repeat("a", 61), "https://evil.example", "evil.example/x", "a<b>",
		"line\nbreak", "tab\there", "x@y", "50% off!", "evil.com", "a.b.example", "verify at steerpost-help.com", "Claude 3.5", "agent.v2", "a .b.c", "Агент.рф", "a:b", `quote"d`, "emoji 🙂"} {
		_, err := ValidateAgentName(in)
		e, ok := errs.As(err)
		if !ok || e.Code != errs.Validation || e.Fields["agent_name"] == "" {
			t.Errorf("%q: want VALIDATION_ERROR on agent_name, got %v", in, err)
		}
	}
}

func TestScopesAreTheDefaultsPlusConnectAndNothingDangerous(t *testing.T) {
	got := Scopes()
	want := []string{"analytics:read", "media:write", "posts:read", "posts:schedule", "posts:write", "social:connect", "social:read"}
	if strings.Join(apikey.Strings(got), ",") != strings.Join(want, ",") {
		t.Fatalf("scopes: %v", got)
	}
	for _, s := range got {
		if s == apikey.PostsPublish || s == apikey.PostsDelete || s == apikey.SocialDisconnect {
			t.Fatalf("an agent sign-up key must never hold %s", s)
		}
	}
	// The result is a fresh slice: callers cannot change the defaults through it.
	got[0] = "x"
	if Scopes()[0] == "x" || apikey.DefaultScopes()[0] == "x" {
		t.Fatal("Scopes shares its backing array")
	}
}

func TestLive(t *testing.T) {
	now := time.Date(2026, 10, 15, 12, 0, 0, 0, time.UTC)
	s := Signup{ExpiresAt: now.Add(time.Minute)}
	if !s.Live(now) || s.Live(now.Add(time.Minute)) {
		t.Fatal("live until, not including, expires_at")
	}
}
