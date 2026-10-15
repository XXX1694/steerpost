package accounts

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/socialos/backend/internal/application/port"
	"github.com/socialos/backend/internal/domain/actor"
	"github.com/socialos/backend/internal/domain/apikey"
	"github.com/socialos/backend/internal/domain/approval"
	"github.com/socialos/backend/internal/domain/errs"
	"github.com/socialos/backend/internal/domain/socialaccount"
)

// recGate records what the use case asked for and answers with err (nil = approved).
type recGate struct {
	reqs []approval.Request
	err  error
}

func (g *recGate) Require(_ context.Context, a actor.Actor, req approval.Request) error {
	if !a.NeedsApproval(req.Action) {
		return nil
	}
	g.reqs = append(g.reqs, req)
	return g.err
}

// Open is what the use case calls after its transaction ended; here it passes the answer through.
func (g *recGate) Open(_ context.Context, _ *port.ApprovalNeeded) error { return g.err }

var errNeedsApproval = errs.New(errs.ApprovalRequired, "needs approval")

func TestKeyConnectAsksForApprovalBeforeAnyNetworkCall(t *testing.T) {
	r := newTokenRig(t)
	r.gate.err = errNeedsApproval
	_, err := r.connect(apiKeyActor(apikey.SocialConnect), validFields())
	_ = wantCode(t, err, errs.ApprovalRequired)
	if r.prov.calls != 0 || len(r.repo.accs) != 0 {
		t.Fatalf("nothing may happen before approval: %d provider calls, %d accounts", r.prov.calls, len(r.repo.accs))
	}
	req := r.gate.reqs[0]
	if req.Action != approval.ActionAccountConnect || req.ResourceType != "social_provider" || req.ResourceID != "tokennet" {
		t.Fatalf("unexpected request %+v", req)
	}
}

func TestConnectApprovalNeverShowsSecretsAndBindsEveryValue(t *testing.T) {
	r := newTokenRig(t)
	if _, err := r.connect(apiKeyActor(apikey.SocialConnect), validFields()); err != nil {
		t.Fatal(err)
	}
	first := r.gate.reqs[0]
	if blob := strings.Join(summaryValues(first.Summary), "|"); strings.Contains(blob, testSecret) || strings.Contains(blob, "https://") {
		t.Fatalf("summary leaks a secret or a full URL: %v", first.Summary)
	}
	if first.Summary["instance_url"] != strings.TrimPrefix(strings.TrimPrefix(testHost, "https://"), "http://") {
		t.Fatalf("summary should show the host only: %v", first.Summary)
	}
	other := validFields()
	other["access_token"] = testSecret + "-other"
	if _, err := r.connect(apiKeyActor(apikey.SocialConnect), other); err != nil {
		t.Fatal(err)
	}
	if first.Fingerprint == r.gate.reqs[1].Fingerprint {
		t.Fatal("a different credential must need its own approval")
	}
}

func summaryValues(m map[string]any) []string {
	var out []string
	for _, v := range m {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func TestSessionConnectNeverAsks(t *testing.T) {
	r := newTokenRig(t)
	r.gate.err = errNeedsApproval
	if _, err := r.connect(session(uuid.New()), validFields()); err != nil {
		t.Fatal(err)
	}
	if len(r.gate.reqs) != 0 {
		t.Fatal("a browser session must not go through approvals")
	}
}

func TestDisconnectByKeyNeedsApprovalAndLeavesTheAccountAlone(t *testing.T) {
	r := newTokenRig(t)
	u := uuid.New()
	acc := &socialaccount.Account{ID: uuid.New(), UserID: u, Provider: "tokennet", Username: "alice", Status: socialaccount.StatusActive}
	r.repo.accs[acc.ID] = acc
	key := apiKeyActor(apikey.SocialDisconnect)
	key.UserID = u

	r.gate.err = errNeedsApproval
	_ = wantCode(t, r.svc.Disconnect(context.Background(), key, acc.ID), errs.ApprovalRequired)
	if r.repo.accs[acc.ID].Status != socialaccount.StatusActive {
		t.Fatal("the account was disconnected without approval")
	}
	if got := r.gate.reqs[0]; got.Action != approval.ActionAccountDisconnect || got.ResourceID != acc.ID.String() {
		t.Fatalf("unexpected request %+v", got)
	}

	r.gate.err = nil
	if err := r.svc.Disconnect(context.Background(), key, acc.ID); err != nil {
		t.Fatal(err)
	}
	if r.repo.accs[acc.ID].Status != socialaccount.StatusRevoked {
		t.Fatal("approved disconnect did not revoke the account")
	}
}

func TestConnectFingerprintIsKeyedNotAPlainHash(t *testing.T) {
	r := newTokenRig(t)
	fields := validFields()
	plain := approval.Fingerprint("tokennet", "access_token", fields["access_token"], "handle", "", "instance_url", fields["instance_url"])
	keyed := connectRequest([]byte("key-one-key-one-key-one-key-one!"), r.prov, fields)
	if keyed.Fingerprint == plain {
		t.Fatal("the fingerprint of a credential must not be a plain hash")
	}
	if keyed.Fingerprint == connectRequest([]byte("key-two-key-two-key-two-key-two!"), r.prov, fields).Fingerprint {
		t.Fatal("the fingerprint must depend on the key")
	}
	if keyed.Fingerprint != connectRequest([]byte("key-one-key-one-key-one-key-one!"), r.prov, fields).Fingerprint {
		t.Fatal("the fingerprint must be stable for the same key and values")
	}
}
