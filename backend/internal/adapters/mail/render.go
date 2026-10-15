package mail

import (
	"bytes"
	"embed"
	"fmt"
	htmltemplate "html/template"
	texttemplate "text/template"

	"github.com/socialos/backend/internal/application/port"
)

// ProductName is shown in every message.
const ProductName = "Steerpost"

// Template names.
const (
	VerifyEmail     = "verify_email"
	ResetPassword   = "reset_password"
	PasswordChanged = "password_changed"
	AccountDeleted  = "account_deleted"
	// AccountDeletionScheduled tells the owner that deletion was requested and how to cancel it.
	AccountDeletionScheduled = "account_deletion_scheduled"
	ExportReady              = "export_ready"
	// IdentityLinked tells the owner that a sign-in provider was linked to the account.
	IdentityLinked = "identity_linked"
	// IdentityUnlinked tells the owner that a sign-in provider was disconnected.
	IdentityUnlinked = "identity_unlinked"
	// AgentAccess asks the owner to confirm an AI agent's sign-up (D-025).
	AgentAccess = "agent_access"
)

//go:embed templates/*.tmpl
var files embed.FS

// Data is the input of every template. Link and ExpiresIn are unused by
// templates that do not need them.
type Data struct {
	Product   string
	Link      string
	ExpiresIn string
	// KeysRevoked tells the password_changed notice whether API keys and MCP connections were revoked too.
	KeysRevoked bool
	// Date is when a scheduled account deletion happens (account_deletion_scheduled), already formatted.
	Date string
	// Provider is the display name of a linked sign-in provider (identity_linked).
	Provider string
	// AgentName is the name the agent gave itself (agent_access). It is validated to letters, digits, spaces and a few
	// punctuation marks, and the HTML part escapes it anyway.
	AgentName string
	// NewAccount selects the agent_access variant: true when no account uses the address yet.
	NewAccount bool
}

var subjects = map[string]string{
	VerifyEmail:              "Verify your email for " + ProductName,
	ResetPassword:            "Reset your " + ProductName + " password",
	PasswordChanged:          "Your " + ProductName + " password was changed",
	AccountDeleted:           "Your " + ProductName + " account was deleted",
	AccountDeletionScheduled: "Your " + ProductName + " account will be deleted",
	IdentityLinked:           "A sign-in method was added to your " + ProductName + " account",
	IdentityUnlinked:         "A sign-in method was removed from your " + ProductName + " account",
	ExportReady:              "Your " + ProductName + " data export is ready",
	AgentAccess:              "An AI agent asks for access to " + ProductName,
}

type pair struct {
	text *texttemplate.Template
	html *htmltemplate.Template
}

var parsed = func() map[string]pair {
	out := map[string]pair{}
	for name := range subjects {
		out[name] = pair{
			text: texttemplate.Must(texttemplate.ParseFS(files, "templates/"+name+".txt.tmpl")),
			html: htmltemplate.Must(htmltemplate.ParseFS(files, "templates/"+name+".html.tmpl")),
		}
	}
	return out
}()

// Render builds the message for a template. The HTML part is escaped by
// html/template; the text part is not HTML and is rendered verbatim.
func Render(template, to string, d Data) (port.Message, error) {
	p, ok := parsed[template]
	if !ok {
		return port.Message{}, fmt.Errorf("mail: unknown template %q", template)
	}
	d.Product = ProductName
	var text, html bytes.Buffer
	if err := p.text.Execute(&text, d); err != nil {
		return port.Message{}, fmt.Errorf("mail: render %s text: %w", template, err)
	}
	if err := p.html.Execute(&html, d); err != nil {
		return port.Message{}, fmt.Errorf("mail: render %s html: %w", template, err)
	}
	return port.Message{To: to, Subject: subjects[template], Text: text.String(), HTML: html.String(), Template: template}, nil
}
