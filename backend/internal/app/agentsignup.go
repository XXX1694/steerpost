package app

import (
	"context"

	"github.com/socialos/backend/internal/application/auth"
	"github.com/socialos/backend/internal/application/developer"
	"github.com/socialos/backend/internal/domain/apikey"
	"github.com/socialos/backend/internal/infrastructure/postgres"
	"github.com/socialos/backend/internal/infrastructure/queue"
)

// agentKeys satisfies auth.AgentKeys with the same repositories the Developer page uses, so a confirmed agent key and
// its MCP connection are ordinary rows the owner lists and revokes there.
type agentKeys struct {
	keys  *postgres.APIKeys
	conns *postgres.MCPConnections
}

func (a agentKeys) CreateKey(ctx context.Context, k *apikey.Key) error { return a.keys.Create(ctx, k) }

func (a agentKeys) CreateConnection(ctx context.Context, c auth.AgentConnection) error {
	return a.conns.Create(ctx, &developer.MCPConnection{ID: c.ID, UserID: c.UserID, APIKeyID: c.APIKeyID, Name: c.Name, ClientName: c.ClientName})
}

// buildAgentSignup wires agent sign-up (D-025). It is always on: a request does nothing until its owner confirms it.
func buildAgentSignup(db *postgres.DB, q *queue.Client, keys *postgres.APIKeys) *auth.AgentSignupDeps {
	return &auth.AgentSignupDeps{Signups: postgres.NewAgentSignups(db), Queue: q.AgentSignupQueue(),
		Keys: agentKeys{keys: keys, conns: postgres.NewMCPConnections(db)}}
}
