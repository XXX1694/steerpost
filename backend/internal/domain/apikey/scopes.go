// Package apikey defines API key entities and the scope model.
package apikey

import (
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/socialos/backend/internal/domain/errs"
)

// Scope is a single permission.
type Scope string

const (
	SocialRead       Scope = "social:read"
	PostsRead        Scope = "posts:read"
	PostsWrite       Scope = "posts:write"
	PostsSchedule    Scope = "posts:schedule"
	PostsPublish     Scope = "posts:publish"
	PostsDelete      Scope = "posts:delete"
	SocialDisconnect Scope = "social:disconnect"
	// SocialConnect lets a key hand a provider credential to Steerpost. It is
	// critical and never part of DefaultScopes (see D-009).
	SocialConnect Scope = "social:connect"
	MediaWrite    Scope = "media:write"
	AnalyticsRead Scope = "analytics:read"
)

// Risk classifies how dangerous a scope is.
type Risk string

const (
	RiskSafe      Risk = "safe"
	RiskSensitive Risk = "sensitive"
	RiskCritical  Risk = "critical"
)

var scopeRisk = map[Scope]Risk{
	SocialRead:       RiskSafe,
	PostsRead:        RiskSafe,
	PostsWrite:       RiskSafe,
	PostsSchedule:    RiskSafe,
	PostsPublish:     RiskSensitive,
	PostsDelete:      RiskSensitive,
	SocialDisconnect: RiskCritical,
	SocialConnect:    RiskCritical,
	MediaWrite:       RiskSafe,
	AnalyticsRead:    RiskSafe,
}

// AllScopes returns every scope in stable order.
func AllScopes() []Scope {
	out := make([]Scope, 0, len(scopeRisk))
	for s := range scopeRisk {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// DefaultScopes is the safe default set; never includes dangerous scopes.
func DefaultScopes() []Scope {
	return []Scope{SocialRead, PostsRead, PostsWrite, PostsSchedule, MediaWrite, AnalyticsRead}
}

// RiskOf returns the risk class of a scope.
func RiskOf(s Scope) Risk { return scopeRisk[s] }

// Dangerous reports whether a scope must be granted explicitly.
func (s Scope) Dangerous() bool { r := scopeRisk[s]; return r == RiskSensitive || r == RiskCritical }

// Valid reports whether s is a known scope.
func (s Scope) Valid() bool { _, ok := scopeRisk[s]; return ok }

// ParseScopes validates and de-duplicates scope strings.
// An empty input yields DefaultScopes.
func ParseScopes(in []string) ([]Scope, error) {
	if len(in) == 0 {
		return DefaultScopes(), nil
	}
	seen := map[Scope]bool{}
	out := make([]Scope, 0, len(in))
	for _, raw := range in {
		s := Scope(raw)
		if !s.Valid() {
			return nil, errs.Validationf("unknown scope %q", raw).WithField("scopes", "unknown scope "+raw)
		}
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

// Strings converts scopes to strings.
func Strings(in []Scope) []string {
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = string(s)
	}
	return out
}

// FromStrings converts stored strings to scopes (unknown values are dropped).
func FromStrings(in []string) []Scope {
	out := make([]Scope, 0, len(in))
	for _, s := range in {
		if Scope(s).Valid() {
			out = append(out, Scope(s))
		}
	}
	return out
}

// Key is a stored API key (raw secret is never stored).
type Key struct {
	ID              uuid.UUID
	UserID          uuid.UUID
	Name            string
	Prefix          string
	KeyHash         string
	Scopes          []Scope
	ExpiresAt       *time.Time
	RevokedAt       *time.Time
	LastUsedAt      *time.Time
	CreatedAt       time.Time
	DangerousPolicy string // "approve" (default), "trusted" or "trusted_connect"; see approval.Policy*
}

// Usable reports whether the key may authenticate at time now.
func (k *Key) Usable(now time.Time) bool {
	if k.RevokedAt != nil {
		return false
	}
	if k.ExpiresAt != nil && !now.Before(*k.ExpiresAt) {
		return false
	}
	return true
}
