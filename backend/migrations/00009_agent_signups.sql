-- +goose Up
SET LOCAL lock_timeout = '5s';
-- Agent sign-up (D-025): an AI agent asks for a key on behalf of an email address. Until the owner confirms by mail the
-- request lives here only: no users row and no api_keys row exist, so a pending request cannot pre-claim an address
-- or act as a key. Only hashes of the key and of the mailed token are stored.
CREATE TABLE agent_signups (
  id               uuid PRIMARY KEY DEFAULT gen_random_uuid(),
  email            citext NOT NULL,
  agent_name       text NOT NULL CHECK (char_length(agent_name) BETWEEN 1 AND 60),
  key_prefix       text NOT NULL,
  -- Serves the pending-key lookup of an unknown bearer key.
  key_hash         text NOT NULL UNIQUE,
  scopes           text[] NOT NULL,
  dangerous_policy text NOT NULL CHECK (dangerous_policy IN ('approve','trusted','trusted_connect')),
  -- NULL until the worker mailed the link. Serves the preview, confirm and decline lookups.
  token_hash       text UNIQUE,
  expires_at       timestamptz NOT NULL,
  created_at       timestamptz NOT NULL DEFAULT now(),
  updated_at       timestamptz NOT NULL DEFAULT now()
);
-- Serves the per-address daily mail cap.
CREATE INDEX agent_signups_email_idx ON agent_signups(email, created_at DESC);
-- Serves the hourly purge of expired requests.
CREATE INDEX agent_signups_expires_idx ON agent_signups(expires_at);

CREATE TRIGGER agent_signups_updated_at BEFORE UPDATE ON agent_signups
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();

-- A confirmed agent key may connect token networks without asking again, nothing else.
ALTER TABLE api_keys DROP CONSTRAINT api_keys_dangerous_policy_check;
ALTER TABLE api_keys ADD CONSTRAINT api_keys_dangerous_policy_check
  CHECK (dangerous_policy IN ('approve','trusted','trusted_connect'));

-- +goose Down
SET LOCAL lock_timeout = '5s';
DROP TABLE IF EXISTS agent_signups;
-- The older code knows only approve and trusted; the safe one keeps every dangerous action behind an approval.
UPDATE api_keys SET dangerous_policy = 'approve' WHERE dangerous_policy = 'trusted_connect';
ALTER TABLE api_keys DROP CONSTRAINT api_keys_dangerous_policy_check;
ALTER TABLE api_keys ADD CONSTRAINT api_keys_dangerous_policy_check CHECK (dangerous_policy IN ('approve','trusted'));
