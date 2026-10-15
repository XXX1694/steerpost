import { normalizeAgentAccessConfirmed, normalizeAgentAccessPreview, type AgentAccessConfirmed, type AgentAccessPreview } from './agent-access';
import { ApiError, parseErrorBody } from './api-error';
import { API_BASE, buildQuery, enc, request, setCsrfToken, uploadBase } from './api-http';
import {
  buildMcpConfig,
  normalizeCreatedApiKey,
  normalizeCreatedMcp,
  normalizeMe,
  normalizePendingSignup,
  normalizeSignInProviders,
  normalizePage,
  normalizeSignInMethods,
  normalizeTelegramLink,
  normalizeTelegramLinkState,
  normalizeProvider,
  unwrapList,
} from './normalize';
import type {
  AnalyticsResult,
  ApiKey,
  Approval,
  AuditLog,
  CreatePostInput,
  CreatedApiKey,
  CreatedMcpConnection,
  DashboardSummary,
  DataExport,
  DataExportLink,
  Me,
  McpConnection,
  Media,
  Page,
  PendingSignup,
  Post,
  Provider,
  SignInMethodList,
  SignInProvider,
  SocialAccount,
  TelegramLink,
  TelegramLinkState,
  UsageReport,
  UpdatePostInput,
  UsageSummary,
} from './types';

const MCP_URL = process.env.NEXT_PUBLIC_MCP_URL ?? 'http://localhost:3333/mcp';

export { API_BASE, ApiError, buildQuery, parseErrorBody, setCsrfToken, uploadBase };

export interface PostFilters {
  status?: string;
  from?: string;
  to?: string;
  limit?: number;
  cursor?: string;
}

/** The body of the calls that re-authenticate: `current_password` only for users who have a password. */
function reauthBody(currentPassword: string | undefined): Record<string, string> {
  return currentPassword ? { current_password: currentPassword } : {};
}

/** Only an http(s) URL may be navigated to: a `javascript:` or `data:` value in a tampered answer would run in our origin. */
function webUrl(raw: string | undefined): string {
  try {
    const u = new URL(raw ?? '');
    return u.protocol === 'https:' || u.protocol === 'http:' ? u.href : '';
  } catch {
    return '';
  }
}

export const api = {
  auth: {
    async register(input: { email: string; password: string; display_name: string; accept_terms: boolean }): Promise<Me> {
      return normalizeMe(await request('/auth/register', { method: 'POST', body: input }));
    },
    async login(input: { email: string; password: string }): Promise<Me> {
      return normalizeMe(await request('/auth/login', { method: 'POST', body: input }));
    },
    /** The Google/GitHub sign-in buttons this server has switched on; empty when none is configured. */
    async signInProviders(): Promise<SignInProvider[]> {
      return normalizeSignInProviders(await request('/auth/providers'));
    },
    /**
     * Where the browser goes to sign in with a provider. It is a navigation, not a fetch: the API answers with a
     * redirect to the provider and sets the state cookie. `next` is the in-app path to return to.
     */
    socialStartUrl(provider: string, next: string | null): string {
      return `${API_BASE}/auth/oauth/${enc(provider)}/start${buildQuery({ next })}`;
    },
    /** Demo build only: there is no provider to visit, so this answers with the in-app page the redirect would end on. */
    async startSocialDemo(provider: string, next: string | null): Promise<{ redirect: string }> {
      const r = (await request(`/auth/oauth/${enc(provider)}/start`, { query: { next } })) as { redirect?: string } | null;
      return { redirect: r?.redirect ?? '/login' };
    },
    /** The sign-up waiting for the Terms after a provider sign-in. 404 when there is none or it expired. */
    async pendingSignup(): Promise<PendingSignup> {
      return normalizePendingSignup(await request('/auth/oauth/pending'));
    },
    /** Creates the account and signs in. `accept_terms` must be true (D-016); a missing ticket is a 404. */
    async completeSignup(input: { display_name: string; accept_terms: boolean }): Promise<Me> {
      return normalizeMe(await request('/auth/oauth/complete', { method: 'POST', body: input }));
    },
    async logout(): Promise<void> {
      await request('/auth/logout', { method: 'POST' });
    },
    async me(): Promise<Me> {
      return normalizeMe(await request('/me'));
    },
    /** Redeems the token from a mailed link. An unknown, used or expired token is a 400. */
    async verifyEmail(token: string): Promise<void> {
      await request('/auth/verify-email', { method: 'POST', body: { token } });
    },
    /** Mails a new verification link to the signed-in user (429 inside the one-minute cooldown). */
    async resendVerification(): Promise<void> {
      await request('/auth/verify-email/resend', { method: 'POST' });
    },
    /** Always succeeds the same way for any address; `delivery` says whether mail can actually leave the server. */
    async forgotPassword(email: string): Promise<{ delivery: 'log' | 'smtp' }> {
      const r = (await request('/auth/password/forgot', { method: 'POST', body: { email } })) as { delivery?: string } | null;
      return { delivery: r?.delivery === 'log' ? 'log' : 'smtp' };
    },
    async resetPassword(token: string, password: string, revokeKeys = false): Promise<void> {
      await request('/auth/password/reset', { method: 'POST', body: { token, password, revoke_keys: revokeKeys } });
    },
    /** Signs every other session out; API keys and MCP connections are revoked only with `revokeKeys`. A wrong current password is a 400. */
    async changePassword(currentPassword: string, newPassword: string, revokeKeys = false): Promise<void> {
      await request('/auth/password/change', {
        method: 'POST',
        body: { current_password: currentPassword, new_password: newPassword, revoke_keys: revokeKeys },
      });
    },
    /** The sign-in methods of the signed-in user (D-023): linked provider accounts and whether a password is set. */
    async signInMethods(): Promise<SignInMethodList> {
      return normalizeSignInMethods(await request('/auth/identities'));
    },
    /**
     * Starts connecting a provider account; the answer is where the browser goes next. Users with a password send it
     * (`current_password`, a wrong one is a 400 with that field); users without one need a session younger than
     * 10 minutes or get 403 REAUTH_REQUIRED. 409: already connected, 404: provider switched off.
     */
    async linkIdentity(provider: string, currentPassword?: string): Promise<{ authorize_url: string }> {
      const r = (await request(`/auth/identities/${enc(provider)}/link`, { method: 'POST', body: reauthBody(currentPassword) })) as { authorize_url?: string } | null;
      return { authorize_url: webUrl(r?.authorize_url) };
    },
    /** Disconnects a provider account (same re-authentication as `linkIdentity`). 409: it is the last way to sign in. */
    async unlinkIdentity(provider: string, currentPassword?: string): Promise<void> {
      await request(`/auth/identities/${enc(provider)}`, { method: 'DELETE', body: reauthBody(currentPassword) });
    },
    /** For accounts without a password (409 otherwise). Signs the other sessions out; a session older than 10 minutes is 403 REAUTH_REQUIRED. */
    async setPassword(newPassword: string): Promise<void> {
      await request('/auth/password/set', { method: 'POST', body: { new_password: newPassword } });
    },
    /** What a mailed agent-access link (D-025) would confirm. An unknown, used or expired token is a 400. */
    async agentSignupPreview(token: string): Promise<AgentAccessPreview> {
      return normalizeAgentAccessPreview(await request('/auth/agent-signups/preview', { method: 'POST', body: { token } }));
    },
    /** Activates the agent's key. A new account is created and signed in; an existing one needs its own session (401, 403). */
    async agentSignupConfirm(input: { token: string; accept_terms: boolean; display_name?: string }): Promise<AgentAccessConfirmed> {
      return normalizeAgentAccessConfirmed(await request('/auth/agent-signups/confirm', { method: 'POST', body: input }));
    },
    /** Refuses the request: the pending key never works. */
    async agentSignupDecline(token: string): Promise<void> {
      await request('/auth/agent-signups/decline', { method: 'POST', body: { token } });
    },
  },
  social: {
    async providers(): Promise<Provider[]> {
      return unwrapList<unknown>(await request('/social/providers')).map(normalizeProvider);
    },
    async accounts(): Promise<SocialAccount[]> {
      return unwrapList<SocialAccount>(await request('/social/accounts'));
    },
    connectUrl(provider: string): string {
      return `${API_BASE}/social/${enc(provider)}/connect?redirect=${enc('/accounts')}`;
    },
    /**
     * Starts proving control of a Telegram channel or group: returns a one-time code to post
     * there (there is no way to connect a chat by name). Session only, CSRF protected.
     */
    async startTelegramLink(): Promise<TelegramLink> {
      return normalizeTelegramLink(await request('/social/telegram/connect', { method: 'POST' }));
    },
    /** Polled while the user posts the code. Another user's link id is a 404. */
    async telegramLinkStatus(id: string): Promise<TelegramLinkState> {
      return normalizeTelegramLinkState(await request(`/social/telegram/connect/${enc(id)}`));
    },
    async disconnect(id: string): Promise<void> {
      await request(`/social/accounts/${enc(id)}`, { method: 'DELETE' });
    },
    /**
     * Connects a network from a pasted credential (Discord webhook, Mastodon token, Bluesky app password).
     * The values go in the request body only: never in a URL, a log or storage. Needs `social:connect`.
     */
    async connectWithToken(provider: string, fields: Record<string, string>): Promise<SocialAccount> {
      return (await request('/social/accounts/token', { method: 'POST', body: { provider, fields } })) as SocialAccount;
    },
    /** Demo build only: OAuth cannot run in a static site, so this adds a sample account right away. */
    async connectDemo(provider: string): Promise<SocialAccount> {
      return (await request(`/social/${enc(provider)}/connect`, { method: 'POST' })) as SocialAccount;
    },
  },
  posts: {
    async list(f: PostFilters = {}): Promise<Page<Post>> {
      return normalizePage<Post>(await request('/posts', { query: { ...f } }));
    },
    async get(id: string): Promise<Post> {
      return (await request(`/posts/${enc(id)}`)) as Post;
    },
    async create(input: CreatePostInput): Promise<Post> {
      return (await request('/posts', { method: 'POST', body: input })) as Post;
    },
    /** Edits a draft or scheduled post; any other status is a 409. Only the fields present are changed. */
    async update(id: string, input: UpdatePostInput): Promise<Post> {
      return (await request(`/posts/${enc(id)}`, { method: 'PATCH', body: input })) as Post;
    },
    async publish(id: string): Promise<Post> {
      return (await request(`/posts/${enc(id)}/publish`, { method: 'POST' })) as Post;
    },
    async schedule(id: string, scheduledAt: string): Promise<Post> {
      return (await request(`/posts/${enc(id)}/schedule`, { method: 'POST', body: { scheduled_at: scheduledAt } })) as Post;
    },
    async cancel(id: string): Promise<Post> {
      return (await request(`/posts/${enc(id)}/cancel`, { method: 'POST' })) as Post;
    },
    async retry(id: string): Promise<Post> {
      return (await request(`/posts/${enc(id)}/retry`, { method: 'POST' })) as Post;
    },
    async remove(id: string): Promise<void> {
      await request(`/posts/${enc(id)}`, { method: 'DELETE' });
    },
  },
  media: {
    async list(): Promise<Media[]> {
      return unwrapList<Media>(await request('/media'));
    },
    async upload(file: File): Promise<Media> {
      const form = new FormData();
      form.append('file', file);
      return (await request('/media', { method: 'POST', form, direct: true })) as Media;
    },
    async get(id: string): Promise<Media> {
      return (await request(`/media/${enc(id)}`)) as Media;
    },
    async remove(id: string): Promise<void> {
      await request(`/media/${enc(id)}`, { method: 'DELETE' });
    },
  },
  account: {
    async usage(): Promise<UsageReport> {
      return (await request('/account/usage')) as UsageReport;
    },
    /**
     * Schedules deletion of the whole account after the grace period and ends this session (the server clears the
     * cookies). Needs the current password and the account's email typed as confirmation; a wrong one is a 400 with
     * the field named. API keys cannot do this.
     */
    async requestDeletion(password: string, confirm: string): Promise<{ scheduled_for: string }> {
      const r = (await request('/account/delete', { method: 'POST', body: { password, confirm } })) as { scheduled_for?: string } | null;
      return { scheduled_for: r?.scheduled_for ?? '' };
    },
    /** Cancels a scheduled deletion (409 when none is scheduled). */
    async cancelDeletion(): Promise<void> {
      await request('/account/delete/cancel', { method: 'POST' });
    },
    /** Account data export (session only). 409 while one is being prepared, 429 inside the 24 h cooldown. */
    exports: {
      async list(): Promise<DataExport[]> {
        return unwrapList<DataExport>(await request('/account/exports'));
      },
      async request(): Promise<DataExport> {
        return (await request('/account/exports', { method: 'POST' })) as DataExport;
      },
      /** A short-lived download URL for a ready export. */
      async link(id: string): Promise<DataExportLink> {
        return (await request(`/account/exports/${enc(id)}`)) as DataExportLink;
      },
    },
  },
  dashboard: {
    async summary(): Promise<DashboardSummary> {
      const r = (await request('/dashboard/summary')) as Partial<DashboardSummary>;
      return {
        connected_accounts: r.connected_accounts ?? 0,
        scheduled_posts: r.scheduled_posts ?? 0,
        drafts: r.drafts ?? 0,
        published_this_month: r.published_this_month ?? 0,
        failed: r.failed ?? 0,
        upcoming: r.upcoming ?? [],
        recent: r.recent ?? [],
      };
    },
  },
  analytics: {
    async get(from?: string, to?: string): Promise<AnalyticsResult> {
      return { items: unwrapList(await request('/analytics', { query: { from, to } })) };
    },
  },
  audit: {
    /** `action` keeps only entries of that action, e.g. `mcp.tool_call` for agent actions. */
    async list(limit = 50, cursor?: string, action?: string): Promise<Page<AuditLog>> {
      return normalizePage<AuditLog>(await request('/audit-logs', { query: { limit, cursor, action } }));
    },
  },
  approvals: {
    /** `all` includes decided and expired ones; the default is only those waiting for a decision. */
    async list(status: 'pending' | 'all' = 'pending', limit = 25, cursor?: string): Promise<Page<Approval>> {
      return normalizePage<Approval>(await request('/approvals', { query: { status, limit, cursor } }));
    },
    /** The agent may then repeat its call once. A decided or expired approval is a 409. */
    async approve(id: string): Promise<Approval> {
      return (await request(`/approvals/${enc(id)}/approve`, { method: 'POST' })) as Approval;
    },
    async deny(id: string): Promise<Approval> {
      return (await request(`/approvals/${enc(id)}/deny`, { method: 'POST' })) as Approval;
    },
  },
  developer: {
    async apiKeys(): Promise<ApiKey[]> {
      return unwrapList<ApiKey>(await request('/developer/api-keys'));
    },
    async createApiKey(input: { name: string; scopes: string[]; expires_at?: string; dangerous_policy?: 'approve' | 'trusted' }): Promise<CreatedApiKey> {
      return normalizeCreatedApiKey(await request('/developer/api-keys', { method: 'POST', body: input }));
    },
    async revokeApiKey(id: string): Promise<void> {
      await request(`/developer/api-keys/${enc(id)}`, { method: 'DELETE' });
    },
    async mcpConnections(): Promise<McpConnection[]> {
      return unwrapList<McpConnection>(await request('/developer/mcp-connections'));
    },
    async createMcpConnection(input: { name: string; scopes: string[] }): Promise<CreatedMcpConnection> {
      const raw = await request('/developer/mcp-connections', { method: 'POST', body: input });
      return normalizeCreatedMcp(raw, MCP_URL);
    },
    async revokeMcpConnection(id: string): Promise<void> {
      await request(`/developer/mcp-connections/${enc(id)}`, { method: 'DELETE' });
    },
    async usage(): Promise<UsageSummary> {
      const r = (await request('/developer/usage')) as Partial<UsageSummary>;
      return { total_requests: r.total_requests ?? 0, by_key: r.by_key ?? [], by_day: r.by_day ?? [] };
    },
  },
};

export { buildMcpConfig };
