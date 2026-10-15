/**
 * Demo stand-in for agent sign-up (D-025). The demo sends no mail, so the token decides the case, like the other
 * mailed links here: "expired" (or none) is refused, a token starting with "new" is for an address without an account,
 * anything else asks for the demo user's own account. The same checks as the API apply to confirming.
 */
import type { ApiKey, McpConnection } from '../types';
import type { DemoResponse, DemoState } from './model';

/** The agent the demo pretends signed up, and the address it gave for a new account. */
const DEMO_AGENT = 'Claude Desktop';
const NEW_OWNER = 'new.owner@example.com';
const SCOPES = ['analytics:read', 'media:write', 'posts:read', 'posts:schedule', 'posts:write', 'social:connect', 'social:read'];
const DAY = 86_400_000;

export interface AgentSignupContext {
  now: number;
  newId: () => string;
  /** The `/me` body of the demo user, after any change made here. */
  me: () => Record<string, unknown>;
}

const fail = (status: number, code: string, message: string, fields?: Record<string, string>): DemoResponse => ({
  status,
  body: { error: { code, message, request_id: 'demo', ...(fields ? { fields } : {}) } },
});
const invalidLink = () => fail(400, 'VALIDATION_ERROR', 'link is invalid or has expired');
const str = (v: unknown): string => (typeof v === 'string' ? v : '');

function preview(s: DemoState, token: string, ctx: AgentSignupContext): DemoResponse {
  const isNew = token.startsWith('new');
  return {
    status: 200,
    body: {
      agent_name: DEMO_AGENT,
      email: isNew ? NEW_OWNER : s.user.email,
      scopes: SCOPES,
      dangerous_policy: 'trusted_connect',
      account_exists: !isNew,
      expires_at: new Date(ctx.now + DAY).toISOString(),
    },
  };
}

/** The key the agent already holds starts working: it shows under Developer with its MCP connection. */
function activate(s: DemoState, ctx: AgentSignupContext): void {
  const created = new Date(ctx.now).toISOString();
  const key: ApiKey = { id: ctx.newId(), name: DEMO_AGENT, prefix: 'sk_live_agnt', scopes: SCOPES, expires_at: null, revoked_at: null, last_used_at: null, created_at: created, dangerous_policy: 'trusted_connect' };
  const conn: McpConnection = { id: ctx.newId(), name: DEMO_AGENT, client_name: DEMO_AGENT, scopes: SCOPES, last_seen_at: null, revoked_at: null, created_at: created };
  s.api_keys.push(key);
  s.mcp_connections.push(conn);
}

function confirm(s: DemoState, token: string, body: Record<string, unknown>, ctx: AgentSignupContext): DemoResponse {
  if (token.startsWith('new')) {
    if (s.signed_in) return fail(409, 'CONFLICT', 'sign out before creating another account');
    if (body.accept_terms !== true) return fail(400, 'VALIDATION_ERROR', 'You must accept the Terms and the Privacy Policy', { accept_terms: 'must be accepted' });
    const name = str(body.display_name).trim();
    if (name.length > 100) return fail(400, 'VALIDATION_ERROR', 'display_name too long', { display_name: 'max 100 characters' });
    // Single-user demo: the new account replaces the demo user's name and address, like provider sign-up does.
    Object.assign(s.user, { email: NEW_OWNER, display_name: name || NEW_OWNER });
    s.signed_in = true;
    activate(s, ctx);
    return { status: 200, body: { ...ctx.me(), account_created: true } };
  }
  if (!s.signed_in) return fail(401, 'UNAUTHENTICATED', 'Authentication required');
  activate(s, ctx);
  return { status: 200, body: { ...ctx.me(), account_created: false } };
}

/** `POST /auth/agent-signups[/preview|/confirm|/decline]`, or null for any other route. */
export function handleAgentSignup(s: DemoState, path: string, body: Record<string, unknown>, ctx: AgentSignupContext): DemoResponse | null {
  if (path === '/auth/agent-signups') {
    const email = str(body.email).trim();
    if (!email.includes('@')) return fail(400, 'VALIDATION_ERROR', 'email is invalid', { email: 'invalid' });
    if (body.accept_terms !== true) return fail(400, 'VALIDATION_ERROR', 'the owner must accept the Terms', { accept_terms: 'must be accepted' });
    const masked = `${email.charAt(0)}***${email.slice(email.indexOf('@'))}`;
    return {
      status: 201,
      body: { api_key: 'sk_live_agnt_demo', key_prefix: 'sk_live_agnt', status: 'pending_confirmation', scopes: SCOPES, dangerous_policy: 'trusted_connect', email_masked: masked, expires_at: new Date(ctx.now + DAY).toISOString(), delivery: 'log' },
    };
  }
  const action = /^\/auth\/agent-signups\/(preview|confirm|decline)$/.exec(path)?.[1];
  if (!action) return null;
  const token = str(body.token);
  if (!token || token === 'expired') return invalidLink();
  if (action === 'preview') return preview(s, token, ctx);
  if (action === 'decline') return { status: 204, body: undefined };
  return confirm(s, token, body, ctx);
}
