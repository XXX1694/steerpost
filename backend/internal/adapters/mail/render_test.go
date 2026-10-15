package mail

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

func TestRenderGolden(t *testing.T) {
	d := Data{Link: "https://app.example.com/verify-email#token=abc123", ExpiresIn: "48 hours", Date: "16 October 2026, 12:00 UTC", Provider: "GitHub"}
	for _, name := range []string{VerifyEmail, ResetPassword, PasswordChanged, AccountDeleted, AccountDeletionScheduled, ExportReady, IdentityLinked, IdentityUnlinked} {
		m, err := Render(name, "alice@example.com", d)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got := "Subject: " + m.Subject + "\n\n--- text ---\n" + m.Text + "\n--- html ---\n" + m.HTML
		path := filepath.Join("testdata", name+".golden")
		if *update {
			if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v (run go test ./internal/adapters/mail -update)", name, err)
		}
		if string(want) != got {
			t.Errorf("%s differs from %s:\n%s", name, path, got)
		}
		if m.Template != name || m.To != "alice@example.com" {
			t.Errorf("%s: envelope fields wrong: %+v", name, m)
		}
	}
}

func TestRenderEscapesHTMLOnly(t *testing.T) {
	m, err := Render(VerifyEmail, "a@b.c", Data{Link: "https://x.test/?a=1&b=<script>", ExpiresIn: "1h"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(m.HTML, "<script>") {
		t.Fatalf("html must be escaped: %s", m.HTML)
	}
	if !strings.Contains(m.Text, "a=1&b=<script>") {
		t.Fatalf("text part must stay verbatim: %s", m.Text)
	}
}

func TestRenderUnknownTemplate(t *testing.T) {
	if _, err := Render("nope", "a@b.c", Data{}); err == nil {
		t.Fatal("expected error")
	}
}

func TestPasswordChangedSaysWhatHappenedToKeys(t *testing.T) {
	kept, err := Render(PasswordChanged, "a@b.c", Data{Link: "https://app.example/developer"})
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{kept.Text, kept.HTML} {
		if !strings.Contains(body, "were not revoked") || !strings.Contains(body, "https://app.example/developer") {
			t.Fatalf("keys-kept notice must say so and link to the developer page: %s", body)
		}
	}
	gone, _ := Render(PasswordChanged, "a@b.c", Data{Link: "https://app.example/developer", KeysRevoked: true})
	if !strings.Contains(gone.Text, "were revoked too") || strings.Contains(gone.Text, "not revoked") {
		t.Fatalf("keys-revoked notice: %s", gone.Text)
	}
}

func TestMailsUseServerAdminAndNeverPromiseAnExportPage(t *testing.T) {
	for _, name := range []string{PasswordChanged, AccountDeleted} {
		m, err := Render(name, "a@b.c", Data{Link: "https://x.test/", ExpiresIn: "1h"})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(m.Text, "tell your server admin") || strings.Contains(m.Text, "operator") || strings.Contains(m.HTML, "operator") {
			t.Errorf("%s must say server admin: %s", name, m.Text)
		}
	}
	m, err := Render(ExportReady, "a@b.c", Data{Link: "https://x.test/", ExpiresIn: "1h"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(m.Text, "settings") || strings.Contains(m.HTML, "settings") {
		t.Errorf("settings has no export page: %s", m.Text)
	}
}

func TestAgentAccessGolden(t *testing.T) {
	for file, newAccount := range map[string]bool{"agent_access_new": true, "agent_access_existing": false} {
		m, err := Render(AgentAccess, "alice@example.com", Data{Link: "https://app.example.com/agent-access#token=abc123",
			ExpiresIn: "24 hours", AgentName: "Claude Desktop", NewAccount: newAccount})
		if err != nil {
			t.Fatal(err)
		}
		got := "Subject: " + m.Subject + "\n\n--- text ---\n" + m.Text + "\n--- html ---\n" + m.HTML
		path := filepath.Join("testdata", file+".golden")
		if *update {
			if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
				t.Fatal(err)
			}
			continue
		}
		want, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("%s: %v (run go test ./internal/adapters/mail -update)", file, err)
		}
		if string(want) != got {
			t.Errorf("%s differs from %s:\n%s", file, path, got)
		}
	}
}

func TestAgentAccessEscapesTheAgentName(t *testing.T) {
	m, err := Render(AgentAccess, "a@b.c", Data{Link: "https://x.test/agent-access#token=t", ExpiresIn: "24 hours", AgentName: `<a href="https://evil.test">x</a>`})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(m.HTML, "<a href=\"https://evil.test\"") || strings.Contains(m.HTML, "evil.test\">") {
		t.Fatalf("agent name must be escaped in HTML: %s", m.HTML)
	}
	if !strings.Contains(m.HTML, "You will be asked to sign in") || strings.Contains(m.Text, "a new Steerpost account") {
		t.Fatalf("existing-account variant: %s", m.Text)
	}
}
