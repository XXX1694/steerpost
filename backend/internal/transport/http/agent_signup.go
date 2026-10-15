package http

import (
	"net/http"
	"strings"
	"time"

	"github.com/socialos/backend/internal/application/auth"
	"github.com/socialos/backend/internal/domain/actor"
	"github.com/socialos/backend/internal/domain/apikey"
	"github.com/socialos/backend/internal/domain/errs"
	"github.com/socialos/backend/internal/transport/httpx"
	"github.com/socialos/backend/internal/transport/middleware"
)

type agentSignupReq struct {
	Email       string `json:"email"`
	AgentName   string `json:"agent_name"`
	AcceptTerms bool   `json:"accept_terms"`
}

// agentSignupResp is the same shape for every address (D-025).
type agentSignupResp struct {
	APIKey          string    `json:"api_key"`
	KeyPrefix       string    `json:"key_prefix"`
	Status          string    `json:"status"`
	Scopes          []string  `json:"scopes"`
	DangerousPolicy string    `json:"dangerous_policy"`
	EmailMasked     string    `json:"email_masked"`
	ExpiresAt       time.Time `json:"expires_at"`
	Delivery        string    `json:"delivery"`
}

type agentSignupPreviewResp struct {
	AgentName       string    `json:"agent_name"`
	Email           string    `json:"email"`
	Scopes          []string  `json:"scopes"`
	DangerousPolicy string    `json:"dangerous_policy"`
	AccountExists   bool      `json:"account_exists"`
	ExpiresAt       time.Time `json:"expires_at"`
}

type agentConfirmReq struct {
	Token       string `json:"token"`
	AcceptTerms bool   `json:"accept_terms"`
	DisplayName string `json:"display_name"`
}

type agentConfirmResp struct {
	meResp
	AccountCreated bool `json:"account_created"`
}

// requireJSON refuses a body that is not declared as JSON. A cross-site page can send a body without a Content-Type
// and without a preflight; demanding application/json forces the CORS preflight, so another site cannot make a browser
// confirm a link and get signed in to an account it does not own.
func requireJSON(r *http.Request) error {
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		return errs.Validationf("Content-Type must be application/json")
	}
	return nil
}

// createAgentSignup serves POST /auth/agent-signups: an agent asks for a key for its owner's address.
func (a *API) createAgentSignup(w http.ResponseWriter, r *http.Request) {
	var req agentSignupReq
	if err := decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	got, err := a.svc.Auth.StartAgentSignup(r.Context(), auth.StartAgentSignupInput{Email: req.Email, AgentName: req.AgentName, AcceptTerms: req.AcceptTerms})
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, http.StatusCreated, agentSignupResp{APIKey: got.RawKey, KeyPrefix: got.KeyPrefix, Status: "pending_confirmation",
		Scopes: apikey.Strings(got.Scopes), DangerousPolicy: got.DangerousPolicy, EmailMasked: got.EmailMasked,
		ExpiresAt: got.ExpiresAt.UTC(), Delivery: a.opt.MailDelivery})
}

// previewAgentSignup serves POST /auth/agent-signups/preview: what confirming the mailed link would do.
func (a *API) previewAgentSignup(w http.ResponseWriter, r *http.Request) {
	var req tokenReq
	if err := decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	p, err := a.svc.Auth.PreviewAgentSignup(r.Context(), req.Token)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	httpx.JSON(w, http.StatusOK, agentSignupPreviewResp{AgentName: p.AgentName, Email: p.Email, Scopes: apikey.Strings(p.Scopes),
		DangerousPolicy: p.DangerousPolicy, AccountExists: p.AccountExists, ExpiresAt: p.ExpiresAt.UTC()})
}

// confirmAgentSignup serves POST /auth/agent-signups/confirm. It answers the /me body plus account_created, and sets
// the session cookies only when the confirmation created the account. With a session, CSRF applies as everywhere.
func (a *API) confirmAgentSignup(w http.ResponseWriter, r *http.Request) {
	var req agentConfirmReq
	if err := requireJSON(r); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	act := actorOf(r)
	done, err := a.svc.Auth.ConfirmAgentSignup(r.Context(), act,
		auth.ConfirmAgentSignupInput{Token: req.Token, AcceptTerms: req.AcceptTerms, DisplayName: req.DisplayName}, middleware.ClientInfoFrom(r, a.trusted))
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	csrf := middleware.CSRFToken(r.Context())
	if done.AccountCreated {
		a.setSessionCookies(w, done.Session)
		act, csrf = actor.Actor{Type: actor.TypeUser}, done.Session.CSRFToken
	}
	me, err := a.meFor(r.Context(), done.User, act, csrf)
	if err != nil {
		httpx.Error(w, r, err)
		return
	}
	httpx.JSON(w, http.StatusOK, agentConfirmResp{meResp: me, AccountCreated: done.AccountCreated})
}

// declineAgentSignup serves POST /auth/agent-signups/decline ("Not me"): the request is deleted and its key never works.
func (a *API) declineAgentSignup(w http.ResponseWriter, r *http.Request) {
	var req tokenReq
	if err := requireJSON(r); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := decode(r, &req); err != nil {
		httpx.Error(w, r, err)
		return
	}
	if err := a.svc.Auth.DeclineAgentSignup(r.Context(), req.Token); err != nil {
		httpx.Error(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
