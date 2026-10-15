/**
 * An AI agent asked for access to Steerpost on someone's behalf (D-025): its key stays inactive until the owner opens the
 * mailed link (`/agent-access#token=…`) and confirms. Types, normalizers and the pure rules of that page.
 */
import { normalizeMe } from './normalize';
import type { Me } from './types';

type Rec = Record<string, unknown>;
const rec = (v: unknown): Rec => (typeof v === 'object' && v !== null && !Array.isArray(v) ? (v as Rec) : {});
const text = (v: unknown): string => (typeof v === 'string' ? v : '');

/** What the link will confirm (`POST /auth/agent-signups/preview`). */
export interface AgentAccessPreview {
  agentName: string;
  /** The address the link was mailed to: the account that will be created or asked for. */
  email: string;
  scopes: string[];
  /** `trusted_connect`: connecting a network does not wait for approval; everything else dangerous still does. */
  dangerousPolicy: string;
  accountExists: boolean;
  expiresAt: string;
}

export interface AgentAccessConfirmed {
  me: Me;
  /** True when the confirm created the account and signed the browser in; false when access went to an existing one. */
  accountCreated: boolean;
}

export function normalizeAgentAccessPreview(raw: unknown): AgentAccessPreview {
  const r = rec(raw);
  return {
    agentName: text(r.agent_name),
    email: text(r.email),
    scopes: Array.isArray(r.scopes) ? r.scopes.filter((s): s is string => typeof s === 'string') : [],
    dangerousPolicy: text(r.dangerous_policy),
    accountExists: r.account_exists === true,
    expiresAt: text(r.expires_at),
  };
}

export function normalizeAgentAccessConfirmed(raw: unknown): AgentAccessConfirmed {
  return { me: normalizeMe(raw), accountCreated: rec(raw).account_created === true };
}

/** What the agent will be able to do, grouped the way the mail says it; one line each in `agentAccess.can.*`. */
export type AgentAbility = 'read' | 'draft' | 'schedule' | 'analytics' | 'connect';
/** What it can never do with this key; one line each in `agentAccess.cannot.*`. */
export type AgentLimit = 'publish' | 'delete' | 'disconnect';

const ABILITIES: readonly { id: AgentAbility; scopes: readonly string[] }[] = [
  { id: 'read', scopes: ['social:read', 'posts:read'] },
  { id: 'draft', scopes: ['posts:write', 'media:write'] },
  { id: 'schedule', scopes: ['posts:schedule'] },
  { id: 'analytics', scopes: ['analytics:read'] },
  { id: 'connect', scopes: ['social:connect'] },
];
const LIMITS: readonly { id: AgentLimit; scope: string }[] = [
  { id: 'publish', scope: 'posts:publish' },
  { id: 'delete', scope: 'posts:delete' },
  { id: 'disconnect', scope: 'social:disconnect' },
];

/** The abilities the key's scopes actually grant: the page lists only what is true for this key. */
export function agentAbilities(scopes: readonly string[]): AgentAbility[] {
  return ABILITIES.filter((a) => a.scopes.some((s) => scopes.includes(s))).map((a) => a.id);
}

/** The dangerous actions the key's scopes leave out. */
export function agentLimits(scopes: readonly string[]): AgentLimit[] {
  return LIMITS.filter((l) => !scopes.includes(l.scope)).map((l) => l.id);
}

/**
 * Which screen the link shows once the preview and the session are known:
 * - `create`: no account for the address and nobody signed in; confirming creates it (Terms needed).
 * - `confirm`: the account exists and its owner is signed in.
 * - `signIn`: the account exists but nobody is signed in; confirming needs that account's session.
 * - `otherUser`: someone else is signed in in this browser; they have to sign out first.
 */
export type AgentAccessStage = 'create' | 'confirm' | 'signIn' | 'otherUser';

export function agentAccessStage(preview: AgentAccessPreview, user: Pick<Me, 'email'> | null): AgentAccessStage {
  if (!user) return preview.accountExists ? 'signIn' : 'create';
  const same = user.email.trim().toLowerCase() === preview.email.trim().toLowerCase();
  return preview.accountExists && same ? 'confirm' : 'otherUser';
}
