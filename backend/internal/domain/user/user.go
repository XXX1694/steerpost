// Package user defines the user entity and credential rules.
package user

import (
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/socialos/backend/internal/domain/errs"
)

// Status of a user.
type Status string

const (
	StatusActive   Status = "active"
	StatusDisabled Status = "disabled"
	// StatusDeleted marks an account whose owner deleted it; it can no longer sign in.
	StatusDeleted Status = "deleted"
)

// DefaultPlan is the plan every account starts on.
const DefaultPlan = "free"

// User is an account owner (tenant).
type User struct {
	ID    uuid.UUID
	Email string
	// PasswordHash is empty for users without a password (see HasPassword).
	PasswordHash string
	DisplayName  string
	Status       Status
	CreatedAt    time.Time
	// EmailVerifiedAt is nil until the owner proves control of the address.
	EmailVerifiedAt *time.Time
	Plan            string
	DeletedAt       *time.Time
	// DeletionScheduledAt is when the account will be deleted for good; nil unless the owner asked for deletion and
	// has not cancelled. Until then the account still works (the owner can export and cancel).
	DeletionScheduledAt *time.Time
	// TermsAcceptedAt and TermsVersion record which legal texts the owner accepted at registration.
	// Accounts created before the Terms existed have a nil time and an empty version.
	TermsAcceptedAt *time.Time
	TermsVersion    string
}

// HasPassword reports whether the user can sign in with a password. Social sign-up users have none until they set one;
// the repository returns their NULL hash as "".
func (u *User) HasPassword() bool {
	return u.PasswordHash != ""
}

// EmailVerified reports whether the address was verified.
func (u *User) EmailVerified() bool { return u.EmailVerifiedAt != nil }

// Password length bounds.
const (
	MinPasswordLen = 8
	MaxPasswordLen = 128
)

// NormalizeEmail validates and lower-cases an email address.
func NormalizeEmail(raw string) (string, error) {
	e := strings.ToLower(strings.TrimSpace(raw))
	addr, err := mail.ParseAddress(e)
	if err != nil || addr.Address != e || len(e) > 254 {
		return "", errs.Validationf("invalid email address").WithField("email", "invalid email")
	}
	return e, nil
}

// ValidatePassword enforces the password policy.
func ValidatePassword(pw string) error {
	n := utf8.RuneCountInString(pw)
	if n < MinPasswordLen || n > MaxPasswordLen {
		return errs.Validationf("password must be %d-%d characters", MinPasswordLen, MaxPasswordLen).
			WithField("password", "invalid length")
	}
	return nil
}

// MaskEmail turns "alice@example.com" into "a***@example.com", for logs and for answers that must not reveal a full
// address.
func MaskEmail(addr string) string {
	at := strings.LastIndex(addr, "@")
	if at < 1 {
		return "***"
	}
	return addr[:1] + "***" + addr[at:]
}

// ValidateDisplayName trims and bounds a display name.
func ValidateDisplayName(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if utf8.RuneCountInString(s) > 100 {
		return "", errs.Validationf("display_name too long").WithField("display_name", "max 100 characters")
	}
	return s, nil
}
