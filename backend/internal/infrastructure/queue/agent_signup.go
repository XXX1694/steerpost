package queue

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
)

// TypeAgentSignup is the worker side of an agent sign-up: look the address up and mail the confirmation link (D-025).
const TypeAgentSignup = "auth:agent_signup"

type agentSignupPayload struct {
	SignupID uuid.UUID `json:"signup_id"`
}

// AgentSignupQueue implements auth.AgentSignupQueue on top of the Asynq client.
type AgentSignupQueue struct{ c *Client }

// AgentSignupQueue returns the auth.AgentSignupQueue backed by this client.
func (c *Client) AgentSignupQueue() *AgentSignupQueue { return &AgentSignupQueue{c: c} }

// EnqueueAgentSignup schedules the lookup and mail. The payload is only the request id, so no address sits in Redis;
// Retention is 0 like every mail task.
func (q *AgentSignupQueue) EnqueueAgentSignup(ctx context.Context, id uuid.UUID) error {
	payload, err := json.Marshal(agentSignupPayload{SignupID: id})
	if err != nil {
		return err
	}
	_, err = q.c.client.EnqueueContext(ctx, asynq.NewTask(TypeAgentSignup, payload),
		asynq.Queue(q.c.queue), asynq.MaxRetry(mailMaxRetry), asynq.Retention(0), asynq.Timeout(mailTimeout))
	if err != nil {
		return fmt.Errorf("queue: enqueue agent sign-up: %w", err)
	}
	return nil
}

// AgentSignupHandler adapts the worker side of an agent sign-up to an Asynq handler.
func AgentSignupHandler(process func(ctx context.Context, id uuid.UUID) error) func(context.Context, *asynq.Task) error {
	return func(ctx context.Context, t *asynq.Task) error {
		var pl agentSignupPayload
		if err := json.Unmarshal(t.Payload(), &pl); err != nil || pl.SignupID == uuid.Nil {
			return fmt.Errorf("bad payload: %w", asynq.SkipRetry)
		}
		return process(ctx, pl.SignupID)
	}
}
