package posts

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/socialos/backend/internal/domain/actor"
	"github.com/socialos/backend/internal/domain/approval"
	"github.com/socialos/backend/internal/domain/media"
	"github.com/socialos/backend/internal/domain/post"
)

// DefaultMinAgentLead is how far ahead an API key must schedule without the owner's approval.
const DefaultMinAgentLead = 5 * time.Minute

// gated describes one dangerous call for the approval gate. The request (with its summary, which may need a media
// lookup) is only built for callers that need approval.
type gated struct {
	action approval.Action
	p      *post.Post
	// fingerprint binds the approval; see the helpers below.
	fingerprint string
	media       []media.Media // nil: load them from p.MediaIDs when the summary is built
	at          *time.Time
	resource    string // resource type, "post" by default
}

func (s *Service) require(ctx context.Context, a actor.Actor, g gated) error {
	if !a.NeedsApproval(g.action) {
		return nil
	}
	ms := g.media
	if ms == nil {
		var err error
		if ms, err = s.loadMedia(ctx, g.p.UserID, g.p.MediaIDs); err != nil {
			return err
		}
	}
	names := s.accountNames(ctx, g.p)
	req := approval.Request{Action: g.action, ResourceType: "post", ResourceID: g.p.ID.String(), Fingerprint: g.fingerprint,
		Summary: summarize(g.p, ms, g.at, names)}
	if g.resource != "" {
		req.ResourceType, req.ResourceID = g.resource, ""
	}
	return s.gate.Require(ctx, a, req)
}

// versionFingerprint binds an approval to one post at its current version: editing the post afterwards (updated_at
// moves) voids it. extra parts narrow it further (the requested time, retry options).
func versionFingerprint(p *post.Post, extra ...string) string {
	return approval.Fingerprint(append([]string{p.ID.String(), strconv.FormatInt(p.UpdatedAt.UnixMicro(), 10)}, extra...)...)
}

// stateFingerprint binds an approval to everything a post will publish: title, text, per-network text, media and run
// time. Used where the call itself changes the post (an edit, a create), so the owner approves the result.
func stateFingerprint(id string, p *post.Post, at time.Time) string {
	parts := []string{id, p.Title, p.Content, strconv.FormatInt(at.UTC().Unix(), 10)}
	targets := make([]string, 0, len(p.Targets))
	for _, t := range p.Targets {
		targets = append(targets, t.SocialAccountID.String()+"="+t.Content)
	}
	sort.Strings(targets)
	parts = append(parts, targets...)
	parts = append(parts, "media")
	for _, m := range p.MediaIDs {
		parts = append(parts, m.String())
	}
	return approval.Fingerprint(parts...)
}

// accountNames maps each target's account to "@username" for the owner's summary, so two accounts on one network can be
// told apart. An account that cannot be read is shown by its network alone: the summary is advice, the fingerprint is
// the binding.
func (s *Service) accountNames(ctx context.Context, p *post.Post) map[uuid.UUID]string {
	out := make(map[uuid.UUID]string, len(p.Targets))
	for _, t := range p.Targets {
		if acc, err := s.accounts.Get(ctx, p.UserID, t.SocialAccountID); err == nil && acc.Username != "" {
			out[t.SocialAccountID] = "@" + strings.TrimPrefix(acc.Username, "@")
		}
	}
	return out
}

// summarize is what the owner reads before deciding, and it shows everything the fingerprint covers: the full text
// (validation caps it at MaxContentLen), every per-network text that differs from it, the media and the run time.
func summarize(p *post.Post, ms []media.Media, at *time.Time, names map[uuid.UUID]string) map[string]any {
	platforms := make([]string, 0, len(p.Targets))
	accounts := make([]string, 0, len(p.Targets))
	var overrides []map[string]any
	for _, t := range p.Targets {
		label := t.Platform
		if n := names[t.SocialAccountID]; n != "" {
			label += " · " + n
		}
		platforms = append(platforms, t.Platform)
		accounts = append(accounts, label)
		if t.Content != p.Content {
			overrides = append(overrides, map[string]any{"platform": t.Platform, "account": label, "content": t.Content})
		}
	}
	out := map[string]any{"title": p.Title, "content": p.Content, "platforms": platforms, "accounts": accounts, "status": string(p.Status),
		"media": mediaSummary(len(p.MediaIDs), ms)}
	if len(overrides) > 0 {
		out["targets"] = overrides
	}
	if at != nil {
		out["scheduled_at"] = at.UTC().Format(time.RFC3339)
	}
	return out
}

func mediaSummary(n int, ms []media.Media) map[string]any {
	images, videos := 0, 0
	for _, m := range ms {
		if m.Kind == media.KindVideo {
			videos++
		} else {
			images++
		}
	}
	return map[string]any{"count": n, "images": images, "videos": videos}
}

func (s *Service) requirePublish(ctx context.Context, a actor.Actor, p *post.Post) error {
	return s.require(ctx, a, gated{action: approval.ActionPostPublish, p: p, fingerprint: versionFingerprint(p)})
}

func (s *Service) requireRetryNow(ctx context.Context, a actor.Actor, p *post.Post, in RetryInput) error {
	return s.require(ctx, a, gated{action: approval.ActionPostRetryNow, p: p,
		fingerprint: versionFingerprint(p, strconv.FormatBool(in.IncludeNeedsReview))})
}

// requireDelete is bound to the post id only: deleting is the same decision whatever the draft says.
func (s *Service) requireDelete(ctx context.Context, a actor.Actor, p *post.Post) error {
	return s.require(ctx, a, gated{action: approval.ActionPostDelete, p: p, fingerprint: approval.Fingerprint(p.ID.String())})
}

// tooSoon: a key scheduling closer than the minimum lead is publishing now with extra steps (OD-2). A lead of 0
// switches the rule off.
func (s *Service) tooSoon(a actor.Actor, at time.Time) bool {
	return a.NeedsApproval(approval.ActionPostScheduleSoon) && s.minAgentLead > 0 && at.Before(s.clock.Now().Add(s.minAgentLead))
}

// requireSoon gates a schedule or retry-for-later of an unchanged post.
func (s *Service) requireSoon(ctx context.Context, a actor.Actor, p *post.Post, at time.Time, extra ...string) error {
	if !s.tooSoon(a, at) {
		return nil
	}
	return s.require(ctx, a, gated{action: approval.ActionPostScheduleSoon, p: p, at: &at,
		fingerprint: versionFingerprint(p, append([]string{strconv.FormatInt(at.UTC().Unix(), 10)}, extra...)...)})
}

// requireSoonAfterEdit gates an edit that leaves the post running within the minimum lead, whether or not the time
// itself changed. p is the post AFTER the edit and the approval covers that state, so it cannot be replayed with
// other text, networks or media.
func (s *Service) requireSoonAfterEdit(ctx context.Context, a actor.Actor, p *post.Post, at time.Time) error {
	if !s.tooSoon(a, at) {
		return nil
	}
	return s.require(ctx, a, gated{action: approval.ActionPostScheduleSoon, p: p, at: &at, fingerprint: stateFingerprint(p.ID.String(), p, at)})
}

// requireSoonCreate gates a create-and-schedule: the approval covers the whole payload (the post has no id yet).
func (s *Service) requireSoonCreate(ctx context.Context, a actor.Actor, p *post.Post, ms []media.Media, at time.Time) error {
	if !s.tooSoon(a, at) {
		return nil
	}
	if ms == nil {
		ms = []media.Media{}
	}
	return s.require(ctx, a, gated{action: approval.ActionPostScheduleSoon, p: p, at: &at, media: ms, resource: "post_input",
		fingerprint: stateFingerprint("", p, at)})
}
