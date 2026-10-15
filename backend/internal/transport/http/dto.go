package http

import (
	"time"

	"github.com/google/uuid"
	"github.com/socialos/backend/internal/application/accounts"
	"github.com/socialos/backend/internal/application/developer"
	appmedia "github.com/socialos/backend/internal/application/media"
	"github.com/socialos/backend/internal/domain/apikey"
	"github.com/socialos/backend/internal/domain/audit"
	"github.com/socialos/backend/internal/domain/media"
	"github.com/socialos/backend/internal/domain/post"
	"github.com/socialos/backend/internal/domain/socialaccount"
	"github.com/socialos/backend/internal/domain/user"
)

// utc normalises a timestamp to UTC so every API time is RFC 3339 "…Z"
// regardless of the server's local zone (ARCHITECTURE.md §4).
func utc(t time.Time) time.Time { return t.UTC() }

// nullable maps "" to JSON null so optional strings are always present and typed string|null.
func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func utcp(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

type userDTO struct {
	ID          uuid.UUID `json:"id"`
	Email       string    `json:"email"`
	DisplayName string    `json:"display_name"`
	CreatedAt   time.Time `json:"created_at"`
	// EmailVerified is false until the owner opens the mailed link.
	EmailVerified bool   `json:"email_verified"`
	Plan          string `json:"plan"`
	// DeletionScheduledAt is when the account will be deleted (null unless the owner asked and has not cancelled).
	DeletionScheduledAt *time.Time `json:"deletion_scheduled_at"`
	// HasPassword is false for accounts created by a social sign-in until the owner sets one.
	HasPassword bool `json:"has_password"`
	// LoginMethods lists the ways to sign in: "password" and/or the linked providers ("google", "github").
	LoginMethods []string `json:"login_methods"`
}

func toUser(u *user.User, methods []string) userDTO {
	return userDTO{ID: u.ID, Email: u.Email, DisplayName: u.DisplayName, CreatedAt: utc(u.CreatedAt),
		EmailVerified: u.EmailVerified(), Plan: u.Plan, DeletionScheduledAt: utcp(u.DeletionScheduledAt),
		HasPassword: u.HasPassword(), LoginMethods: methods}
}

type accountDTO struct {
	ID                uuid.UUID      `json:"id"`
	Provider          string         `json:"provider"`
	ProviderAccountID string         `json:"provider_account_id"`
	Username          string         `json:"username"`
	DisplayName       string         `json:"display_name"`
	AvatarURL         string         `json:"avatar_url"`
	Scopes            []string       `json:"scopes"`
	Status            string         `json:"status"`
	Metadata          map[string]any `json:"metadata"`
	ConnectedAt       time.Time      `json:"connected_at"`
}

func toAccount(a *socialaccount.Account) accountDTO {
	return accountDTO{ID: a.ID, Provider: a.Provider, ProviderAccountID: a.ProviderAccountID, Username: a.Username,
		DisplayName: a.DisplayName, AvatarURL: a.AvatarURL, Scopes: a.Scopes, Status: string(a.Status),
		Metadata: a.Metadata, ConnectedAt: utc(a.ConnectedAt)}
}

// providerDTO is one entry of GET /social/providers. `id` and `name` carry the
// provider key (e.g. "linkedin"); `status` is supported | unsupported.
type providerDTO struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	DisplayName  string `json:"display_name"`
	Status       string `json:"status"`
	Supported    bool   `json:"supported"`
	Unsupported  bool   `json:"unsupported"`
	Configured   bool   `json:"configured"`
	Capabilities any    `json:"capabilities"`
}

func toProvider(p accounts.ProviderInfo) providerDTO {
	status := "supported"
	if !p.Supported {
		status = "unsupported"
	}
	return providerDTO{ID: p.Name, Name: p.Name, DisplayName: p.DisplayName, Status: status, Supported: p.Supported,
		Unsupported: !p.Supported, Configured: p.Configured, Capabilities: p.Capabilities}
}

type targetDTO struct {
	ID              uuid.UUID  `json:"id"`
	SocialAccountID uuid.UUID  `json:"social_account_id"`
	Platform        string     `json:"platform"`
	Content         string     `json:"content"`
	Status          string     `json:"status"`
	ExternalPostID  *string    `json:"external_post_id"`
	ExternalURL     *string    `json:"external_url"`
	PublishedAt     *time.Time `json:"published_at"`
	ErrorCode       *string    `json:"error_code"`
	ErrorMessage    *string    `json:"error_message"`
	AttemptCount    int        `json:"attempt_count"`
}

type postDTO struct {
	ID          uuid.UUID   `json:"id"`
	Title       string      `json:"title"`
	Content     string      `json:"content"`
	Status      string      `json:"status"`
	ScheduledAt *time.Time  `json:"scheduled_at"`
	PublishedAt *time.Time  `json:"published_at"`
	CreatedBy   string      `json:"created_by"`
	CreatedAt   time.Time   `json:"created_at"`
	UpdatedAt   time.Time   `json:"updated_at"`
	Targets     []targetDTO `json:"targets"`
	MediaIDs    []uuid.UUID `json:"media_ids"`
	// Media (objects with short-lived URLs) and Attempts are only present on GET /posts/{id}.
	// Pointers so an empty list still serialises as [] on the detail endpoint.
	Media    *[]mediaDTO  `json:"media,omitempty"`
	Attempts *[]attemptDT `json:"attempts,omitempty"`
}

type attemptDT struct {
	ID           uuid.UUID  `json:"id"`
	PostTargetID uuid.UUID  `json:"post_target_id"`
	AttemptNo    int        `json:"attempt_no"`
	Status       string     `json:"status"`
	StartedAt    time.Time  `json:"started_at"`
	FinishedAt   *time.Time `json:"finished_at"`
	ErrorCode    *string    `json:"error_code"`
	ErrorMessage *string    `json:"error_message"`
}

func toPost(p *post.Post) postDTO {
	d := postDTO{ID: p.ID, Title: p.Title, Content: p.Content, Status: string(p.Status), ScheduledAt: utcp(p.ScheduledAt),
		PublishedAt: utcp(p.PublishedAt), CreatedBy: string(p.CreatedBy), CreatedAt: utc(p.CreatedAt), UpdatedAt: utc(p.UpdatedAt),
		Targets: make([]targetDTO, len(p.Targets)), MediaIDs: p.MediaIDs}
	if d.MediaIDs == nil {
		d.MediaIDs = []uuid.UUID{}
	}
	for i, t := range p.Targets {
		d.Targets[i] = targetDTO{ID: t.ID, SocialAccountID: t.SocialAccountID, Platform: t.Platform, Content: t.Content,
			Status: string(t.Status), ExternalPostID: nullable(t.ExternalPostID), ExternalURL: nullable(t.ExternalURL), PublishedAt: utcp(t.PublishedAt),
			ErrorCode: nullable(t.ErrorCode), ErrorMessage: nullable(t.ErrorMessage), AttemptCount: t.AttemptCount}
	}
	return d
}

func toPostDetail(p *post.Post, attempts []post.Attempt, media []appmedia.WithURL) postDTO {
	d := toPost(p)
	at := make([]attemptDT, len(attempts))
	for i, a := range attempts {
		at[i] = attemptDT{ID: a.ID, PostTargetID: a.PostTargetID, AttemptNo: a.AttemptNo, Status: string(a.Status),
			StartedAt: utc(a.StartedAt), FinishedAt: utcp(a.FinishedAt), ErrorCode: nullable(a.ErrorCode), ErrorMessage: nullable(a.ErrorMessage)}
	}
	d.Attempts = &at
	items := make([]mediaDTO, len(media))
	for i := range media {
		items[i] = toMediaWithURL(&media[i])
	}
	d.Media = &items
	return d
}

func toPosts(ps []post.Post) []postDTO {
	out := make([]postDTO, len(ps))
	for i := range ps {
		out[i] = toPost(&ps[i])
	}
	return out
}

type mediaDTO struct {
	ID           uuid.UUID `json:"id"`
	Kind         string    `json:"kind"`
	MimeType     string    `json:"mime_type"`
	SizeBytes    int64     `json:"size_bytes"`
	OriginalName string    `json:"original_name"`
	Width        int       `json:"width"`
	Height       int       `json:"height"`
	SHA256       string    `json:"sha256"`
	Status       string    `json:"status"`
	CreatedAt    time.Time `json:"created_at"`
	URL          string    `json:"url,omitempty"`
}

func toMedia(m *media.Media) mediaDTO {
	return mediaDTO{ID: m.ID, Kind: string(m.Kind), MimeType: m.MimeType, SizeBytes: m.SizeBytes, OriginalName: m.OriginalName,
		Width: m.Width, Height: m.Height, SHA256: m.SHA256, Status: string(m.Status), CreatedAt: utc(m.CreatedAt)}
}

func toMediaWithURL(m *appmedia.WithURL) mediaDTO {
	d := toMedia(&m.Media)
	d.URL = m.URL
	return d
}

type apiKeyDTO struct {
	ID         uuid.UUID  `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"prefix"`
	Scopes     []string   `json:"scopes"`
	ExpiresAt  *time.Time `json:"expires_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
	CreatedAt  time.Time  `json:"created_at"`
	// DangerousPolicy: "approve" asks the owner before publish, delete and disconnect; "trusted" does not; "trusted_connect"
	// (agent sign-up keys, D-025) skips only the approval for connecting a token network.
	DangerousPolicy string `json:"dangerous_policy"`
}

func toKey(k *apikey.Key) apiKeyDTO {
	return apiKeyDTO{ID: k.ID, Name: k.Name, Prefix: k.Prefix, Scopes: apikey.Strings(k.Scopes), ExpiresAt: utcp(k.ExpiresAt),
		RevokedAt: utcp(k.RevokedAt), LastUsedAt: utcp(k.LastUsedAt), CreatedAt: utc(k.CreatedAt),
		DangerousPolicy: k.DangerousPolicy}
}

type mcpDTO struct {
	ID         uuid.UUID  `json:"id"`
	Name       string     `json:"name"`
	ClientName string     `json:"client_name"`
	APIKeyID   uuid.UUID  `json:"api_key_id"`
	KeyPrefix  string     `json:"key_prefix"`
	Scopes     []string   `json:"scopes"`
	LastSeenAt *time.Time `json:"last_seen_at"`
	RevokedAt  *time.Time `json:"revoked_at"`
	CreatedAt  time.Time  `json:"created_at"`
}

func toMCP(c *developer.MCPConnection) mcpDTO {
	return mcpDTO{ID: c.ID, Name: c.Name, ClientName: c.ClientName, APIKeyID: c.APIKeyID, KeyPrefix: c.KeyPrefix,
		Scopes: apikey.Strings(c.Scopes), LastSeenAt: utcp(c.LastSeenAt), RevokedAt: utcp(c.RevokedAt), CreatedAt: utc(c.CreatedAt)}
}

type auditDTO struct {
	ID           uuid.UUID      `json:"id"`
	ActorType    string         `json:"actor_type"`
	ActorID      string         `json:"actor_id"`
	ActorLabel   string         `json:"actor_label"`
	Action       string         `json:"action"`
	ResourceType string         `json:"resource_type"`
	ResourceID   string         `json:"resource_id"`
	Metadata     map[string]any `json:"metadata"`
	RequestID    string         `json:"request_id"`
	IP           string         `json:"ip"`
	CreatedAt    time.Time      `json:"created_at"`
}

func toAudit(e audit.Entry) auditDTO {
	return auditDTO{ID: e.ID, ActorType: e.ActorType, ActorID: e.ActorID, ActorLabel: e.ActorLabel, Action: e.Action,
		ResourceType: e.ResourceType, ResourceID: e.ResourceID, Metadata: e.Metadata, RequestID: e.RequestID, IP: e.IP, CreatedAt: utc(e.CreatedAt)}
}

// page is the pagination envelope.
type page[T any] struct {
	Items      []T     `json:"items"`
	NextCursor *string `json:"next_cursor"`
}

func newPage[T any](items []T, next string) page[T] {
	p := page[T]{Items: items}
	if p.Items == nil {
		p.Items = []T{}
	}
	if next != "" {
		p.NextCursor = &next
	}
	return p
}
