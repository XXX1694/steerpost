package posts

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/socialos/backend/internal/application/port"
	"github.com/socialos/backend/internal/domain/actor"
	"github.com/socialos/backend/internal/domain/approval"
	"github.com/socialos/backend/internal/domain/media"
	"github.com/socialos/backend/internal/domain/post"
)

type fixedNow struct{ t time.Time }

func (c fixedNow) Now() time.Time { return c.t }

type spyGate struct{ reqs []approval.Request }

func (g *spyGate) Require(_ context.Context, a actor.Actor, req approval.Request) error {
	if a.NeedsApproval(req.Action) {
		g.reqs = append(g.reqs, req)
	}
	return nil
}

func (g *spyGate) Open(context.Context, *port.ApprovalNeeded) error { return nil }

var now0 = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)

func guardRig(lead time.Duration) (*Service, *spyGate) {
	g := &spyGate{}
	return &Service{clock: fixedNow{now0}, gate: g, minAgentLead: lead}, g
}

func key() actor.Actor { return actor.Actor{UserID: uuid.New(), Type: actor.TypeAPIKey, ID: "k"} }
func session() actor.Actor {
	return actor.Actor{UserID: uuid.New(), Type: actor.TypeUser, SessionID: uuid.New()}
}

func testPost() *post.Post {
	return &post.Post{ID: uuid.New(), Title: "T", Content: "hello", Status: post.StatusDraft, UpdatedAt: now0,
		Targets: []post.Target{{Platform: "mock"}}}
}

func trusted(a actor.Actor) actor.Actor { a.DangerousPolicy = approval.PolicyTrusted; return a }

func TestTooSoonOnlyStopsKeysSchedulingUnderTheLead(t *testing.T) {
	for name, tc := range map[string]struct {
		a    actor.Actor
		lead time.Duration
		at   time.Time
		want bool
	}{
		"key, 4 minutes ahead":        {key(), 5 * time.Minute, now0.Add(4 * time.Minute), true},
		"key, exactly the lead":       {key(), 5 * time.Minute, now0.Add(5 * time.Minute), false},
		"key, 6 minutes ahead":        {key(), 5 * time.Minute, now0.Add(6 * time.Minute), false},
		"key, rule switched off":      {key(), 0, now0.Add(time.Second), false},
		"session, 1 minute ahead":     {session(), 5 * time.Minute, now0.Add(time.Minute), false},
		"scheduler, 1 minute ahead":   {actor.Scheduler(uuid.New()), 5 * time.Minute, now0.Add(time.Minute), false},
		"trusted key, 1 minute ahead": {trusted(key()), 5 * time.Minute, now0.Add(time.Minute), false},
	} {
		s, _ := guardRig(tc.lead)
		if got := s.tooSoon(tc.a, tc.at); got != tc.want {
			t.Errorf("%s: tooSoon=%v, want %v", name, got, tc.want)
		}
	}
}

func TestNewServiceTurnsTheLeadRuleOffExplicitly(t *testing.T) {
	if s := NewService(Deps{NoAgentLead: true, MinAgentLead: time.Hour}); s.minAgentLead != 0 {
		t.Fatalf("lead %v, want off", s.minAgentLead)
	}
	if s := NewService(Deps{}); s.minAgentLead != DefaultMinAgentLead {
		t.Fatalf("default lead %v", s.minAgentLead)
	}
}

func TestRequestsAreOnlyBuiltForCallersThatNeedApproval(t *testing.T) {
	s, g := guardRig(5 * time.Minute)
	s.media = nil // a media lookup for a session would panic
	p := testPost()
	p.MediaIDs = []uuid.UUID{uuid.New()}
	if err := s.requirePublish(context.Background(), session(), p); err != nil || len(g.reqs) != 0 {
		t.Fatalf("session: %v %d", err, len(g.reqs))
	}
}

func TestPostApprovalsAreBoundToTheVersionAndTime(t *testing.T) {
	p := testPost()
	edited := *p
	edited.UpdatedAt = now0.Add(time.Microsecond)
	if versionFingerprint(p) == versionFingerprint(&edited) {
		t.Fatal("editing the post must void the approval")
	}
	if versionFingerprint(p, "1") == versionFingerprint(p, "2") {
		t.Fatal("another time needs another approval")
	}
}

func TestStateFingerprintCoversEverythingThatWillBePublished(t *testing.T) {
	acc := uuid.New()
	mk := func() *post.Post {
		return &post.Post{ID: uuid.New(), Title: "T", Content: "hi", MediaIDs: []uuid.UUID{uuid.New()},
			Targets: []post.Target{{SocialAccountID: acc, Content: "hi"}}}
	}
	at := now0.Add(time.Minute)
	base := mk()
	fp := stateFingerprint("id1", base, at)
	for name, change := range map[string]func(*post.Post){
		"title":           func(p *post.Post) { p.Title = "T2" },
		"content":         func(p *post.Post) { p.Content = "hi!" },
		"network text":    func(p *post.Post) { p.Targets[0].Content = "other" },
		"another account": func(p *post.Post) { p.Targets[0].SocialAccountID = uuid.New() },
		"extra target": func(p *post.Post) {
			p.Targets = append(p.Targets, post.Target{SocialAccountID: uuid.New(), Content: "hi"})
		},
		"media": func(p *post.Post) { p.MediaIDs = nil },
	} {
		c := *base
		c.Targets = append([]post.Target(nil), base.Targets...)
		change(&c)
		if stateFingerprint("id1", &c, at) == fp {
			t.Errorf("changing the %s must need a new approval", name)
		}
	}
	if stateFingerprint("id1", base, at.Add(time.Second)) == fp || stateFingerprint("id2", base, at) == fp {
		t.Error("another time or post must need a new approval")
	}
}

func TestSummaryShowsEverythingTheOwnerApproves(t *testing.T) {
	p := testPost()
	p.Content = strings.Repeat("é", 1000)
	p.Targets = []post.Target{{Platform: "linkedin", SocialAccountID: uuid.New(), Content: p.Content}, {Platform: "telegram", SocialAccountID: uuid.New(), Content: "short override"}}
	p.MediaIDs = []uuid.UUID{uuid.New(), uuid.New()}
	at := now0.Add(time.Minute)
	sum := summarize(p, []media.Media{{Kind: media.KindImage}, {Kind: media.KindVideo}}, &at, map[uuid.UUID]string{p.Targets[1].SocialAccountID: "@team"})
	if sum["content"] != p.Content {
		t.Fatal("the full text must be in the summary, not a prefix")
	}
	overrides := sum["targets"].([]map[string]any)
	if sum["accounts"].([]string)[1] != "telegram · @team" || overrides[0]["account"] != "telegram · @team" {
		t.Fatalf("accounts must be told apart: %v", sum["accounts"])
	}
	if len(overrides) != 1 || overrides[0]["platform"] != "telegram" || overrides[0]["content"] != "short override" {
		t.Fatalf("per-network text: %v", overrides)
	}
	m := sum["media"].(map[string]any)
	if m["count"] != 2 || m["images"] != 1 || m["videos"] != 1 || sum["scheduled_at"] != "2026-10-09T12:01:00Z" {
		t.Fatalf("media/time: %v", sum)
	}
	same := testPost()
	same.Targets[0].Content = same.Content
	if _, ok := summarize(same, nil, nil, nil)["targets"]; ok {
		t.Fatal("no overrides, no targets entry")
	}
}
