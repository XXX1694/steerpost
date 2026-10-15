import { describe, expect, it } from 'vitest';
import {
  agentAbilities,
  agentAccessStage,
  agentLimits,
  normalizeAgentAccessConfirmed,
  normalizeAgentAccessPreview,
  type AgentAccessPreview,
} from '@/lib/agent-access';
import { DemoEngine } from '@/lib/demo/engine';
import { buildSeed } from '@/lib/demo/seed';

const SIGNUP_SCOPES = ['analytics:read', 'media:write', 'posts:read', 'posts:schedule', 'posts:write', 'social:connect', 'social:read'];

describe('agent access rules', () => {
  const base = normalizeAgentAccessPreview({
    agent_name: 'Claude Desktop', email: 'owner@example.com', scopes: SIGNUP_SCOPES, dangerous_policy: 'trusted_connect',
    account_exists: false, expires_at: '2026-10-16T10:00:00Z',
  });

  it('reads the preview and tolerates junk', () => {
    expect(base).toMatchObject({ agentName: 'Claude Desktop', accountExists: false, dangerousPolicy: 'trusted_connect' });
    expect(normalizeAgentAccessPreview(null)).toEqual({ agentName: '', email: '', scopes: [], dangerousPolicy: '', accountExists: false, expiresAt: '' });
    expect(normalizeAgentAccessPreview({ scopes: ['posts:read', 3], account_exists: 'yes' })).toMatchObject({ scopes: ['posts:read'], accountExists: false });
  });

  it('reads the confirm answer as /me plus account_created', () => {
    const r = normalizeAgentAccessConfirmed({ id: 'u1', email: 'o@example.com', csrf_token: 'c', account_created: true, user: { has_password: false } });
    expect(r.accountCreated).toBe(true);
    expect(r.me).toMatchObject({ id: 'u1', has_password: false });
    expect(normalizeAgentAccessConfirmed({}).accountCreated).toBe(false);
  });

  it('lists what the sign-up scopes allow, and the three things they never do', () => {
    expect(agentAbilities(SIGNUP_SCOPES)).toEqual(['read', 'draft', 'schedule', 'analytics', 'connect']);
    expect(agentLimits(SIGNUP_SCOPES)).toEqual(['publish', 'delete', 'disconnect']);
    expect(agentAbilities(['posts:read'])).toEqual(['read']);
    expect(agentLimits(['posts:publish'])).toEqual(['delete', 'disconnect']);
  });

  it('picks the screen from the account and the session', () => {
    const existing: AgentAccessPreview = { ...base, accountExists: true };
    expect(agentAccessStage(base, null)).toBe('create');
    expect(agentAccessStage(base, { email: 'someone@example.com' })).toBe('otherUser');
    expect(agentAccessStage(existing, null)).toBe('signIn');
    expect(agentAccessStage(existing, { email: ' OWNER@example.com ' })).toBe('confirm');
    expect(agentAccessStage(existing, { email: 'someone@example.com' })).toBe('otherUser');
    // A session cannot "confirm" an account that does not exist yet, even with the same address.
    expect(agentAccessStage(base, { email: 'owner@example.com' })).toBe('otherUser');
  });
});

describe('demo agent sign-up', () => {
  const engine = () => new DemoEngine(buildSeed(new Date('2026-10-15T10:00:00Z')), { now: () => Date.parse('2026-10-15T10:00:00Z') });
  const post = (e: DemoEngine, path: string, body: unknown) => e.handle({ method: 'POST', path, body });

  it('answers the sign-up with the contract shape', () => {
    const r = post(engine(), '/auth/agent-signups', { email: 'owner@example.com', agent_name: 'Claude Desktop', accept_terms: true });
    expect(r.status).toBe(201);
    expect(r.body).toMatchObject({ status: 'pending_confirmation', dangerous_policy: 'trusted_connect', email_masked: 'o***@example.com', delivery: 'log' });
  });

  it('refuses an expired link and previews the others', () => {
    const e = engine();
    expect(post(e, '/auth/agent-signups/preview', { token: 'expired' }).status).toBe(400);
    expect(post(e, '/auth/agent-signups/preview', { token: 'new-1' }).body).toMatchObject({ account_exists: false, dangerous_policy: 'trusted_connect' });
    expect(post(e, '/auth/agent-signups/preview', { token: 'mine' }).body).toMatchObject({ account_exists: true });
    expect(post(e, '/auth/agent-signups/decline', { token: 'mine' }).status).toBe(204);
  });

  it('confirms on the signed-in account and the key shows under Developer as trusted_connect', () => {
    const e = engine();
    const r = post(e, '/auth/agent-signups/confirm', { token: 'mine', accept_terms: false });
    expect(r.status).toBe(200);
    expect(r.body).toMatchObject({ account_created: false });
    const keys = e.handle({ method: 'GET', path: '/developer/api-keys' }).body as { items: { dangerous_policy: string }[] };
    expect(keys.items.some((k) => k.dangerous_policy === 'trusted_connect')).toBe(true);
  });

  it('needs a session for an existing account, and the Terms and no session for a new one', () => {
    const e = engine();
    e.handle({ method: 'POST', path: '/auth/logout' });
    expect(post(e, '/auth/agent-signups/confirm', { token: 'mine' }).status).toBe(401);
    expect(post(e, '/auth/agent-signups/confirm', { token: 'new-1', accept_terms: false }).status).toBe(400);
    expect(post(e, '/auth/agent-signups/confirm', { token: 'new-1', accept_terms: true }).body).toMatchObject({ account_created: true });
    expect(post(e, '/auth/agent-signups/confirm', { token: 'new-2', accept_terms: true }).status).toBe(409);
  });
});
