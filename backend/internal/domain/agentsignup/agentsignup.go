// Package agentsignup models an AI agent asking for an API key on behalf of a human owner, identified only by an email
// address (D-025). The request creates no user and no API key: it waits here until the owner confirms it by mail.
package agentsignup

import (
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/socialos/backend/internal/domain/apikey"
	"github.com/socialos/backend/internal/domain/approval"
	"github.com/socialos/backend/internal/domain/errs"
)

// Limits of a pending sign-up.
const (
	// TTL is how long the owner has to confirm; after it the key never works and the row is purged.
	TTL = 24 * time.Hour
	// MaxPerEmailPerDay caps the confirmation mails one address receives in a rolling Window, so the public sign-up
	// endpoint cannot be used to flood an inbox.
	MaxPerEmailPerDay = 5
	Window            = 24 * time.Hour
	// MaxNameLen bounds the agent name shown in the mail and on the Developer page.
	MaxNameLen = 60
	// Policy is the dangerous_policy every agent sign-up key gets: it may connect token networks without asking again,
	// nothing else (D-025).
	Policy = approval.PolicyTrustedConnect
)

// Signup is one pending request. Only hashes of the key and of the mailed token are stored.
type Signup struct {
	ID        uuid.UUID
	Email     string
	AgentName string
	KeyPrefix string
	KeyHash   string
	Scopes    []apikey.Scope
	// DangerousPolicy is the key's policy once confirmed (always Policy today).
	DangerousPolicy string
	// TokenHash is the SHA-256 of the mailed confirmation token; empty until the worker sent the mail.
	TokenHash string
	ExpiresAt time.Time
	CreatedAt time.Time
}

// Live reports whether the request can still be confirmed at now.
func (s Signup) Live(now time.Time) bool { return now.Before(s.ExpiresAt) }

// Scopes is the fixed scope set of an agent sign-up key: the safe defaults plus social:connect. It never includes
// posts:publish, posts:delete or social:disconnect.
func Scopes() []apikey.Scope {
	out := append(apikey.DefaultScopes(), apikey.SocialConnect)
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// nameSymbols are the punctuation marks an agent name may contain besides letters, digits and spaces. The set is
// small on purpose: the name lands in a mail to someone who never asked for it, so it must not carry links or markup.
const nameSymbols = "-_.()'"

// ValidateAgentName trims the name and checks it: 1 to MaxNameLen characters of letters, digits, spaces and nameSymbols,
// and no "." directly followed by a letter or digit. That last rule keeps domains ("steerpost-help.com") out of the
// name: mail clients turn them into links, so an agent could otherwise put a phishing link into the owner's mail. It
// also refuses version numbers such as "3.5"; the name is a label, not a version string.
func ValidateAgentName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	n := utf8.RuneCountInString(name)
	if n == 0 || n > MaxNameLen {
		return "", invalidName("agent_name must be 1-60 characters")
	}
	if strings.Contains(name, "://") {
		return "", invalidName("agent_name must not contain a link")
	}
	prevDot := false
	for _, r := range name {
		if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != ' ' && !strings.ContainsRune(nameSymbols, r) {
			return "", invalidName("agent_name may only contain letters, digits, spaces and - _ . ( ) '")
		}
		if prevDot && (unicode.IsLetter(r) || unicode.IsDigit(r)) {
			return "", invalidName("agent_name must not look like a domain: no letter or digit right after a dot")
		}
		prevDot = r == '.'
	}
	return name, nil
}

func invalidName(msg string) error {
	return errs.Validationf("%s", msg).WithField("agent_name", msg)
}
