/**
 * In-browser implementation of the Steerpost REST contract (docs/ARCHITECTURE.md section 4).
 * A typed port of scripts/mock-api.mjs: same routes, same status codes, same error format,
 * same state machine. One user, no tenants; state lives in memory and the caller persists it
 * through `onChange`.
 */
import { postTime } from '../format';
import { SCOPES } from '../scopes';
import type {
  AnalyticsPoint,
  ApiKey,
  AuditLog,
  McpConnection,
  Media,
  Post,
  PostStatus,
  PostTarget,
  SocialAccount,
} from '../types';
import { decide, findApproval, visibleApprovals } from './approvals';
import { handleAgentSignup } from './agent-access';
import { svgThumb } from './art';
import { cancelDeletion, DEMO_GRACE_DAYS, requestDeletion } from './deletion';
import { handleExports } from './exports';
import type { DemoLink, DemoPost, DemoRequest, DemoResponse, DemoState, WireProvider } from './model';
import { PROVIDERS } from './providers';
import { completeSignup, pendingSignup, SIGN_IN_PROVIDERS, startSocialSignIn } from './social-signin';
import { demoQuotaError, demoUsage } from './quota';

const MIN = 60_000;
const DAY = 86_400_000;
const ALL_SCOPES: string[] = SCOPES.map((s) => s.scope);
const LINK_TTL_MS = 15 * MIN;
const MAX_ACTIVE_LINKS = 3;
/** Same alphabet as the backend: no 0/O, 1/I/L. */
const LINK_ALPHABET = 'ABCDEFGHJKMNPQRSTUVWXYZ23456789';
/** How long a simulated provider call takes for "Publish now". */
const PUBLISH_MS = 900;

export interface EngineOptions {
  now?: () => number;
  /** Called after every request or tick that changed state. */
  onChange?: (state: DemoState) => void;
  /** After this long the demo "sees" the Telegram code in a demo channel. 0 or less never connects. */
  telegramDelayMs?: number;
}

type Actor = { type: AuditLog['actor_type']; label: string };

const TRANSITIONS: Partial<Record<PostStatus, PostStatus[]>> = {
  draft: ['scheduled', 'publishing', 'cancelled'],
  scheduled: ['draft', 'publishing', 'cancelled'],
  failed: ['scheduled', 'publishing'],
  partially_published: ['scheduled', 'publishing'],
};

function randomBytes(n: number): number[] {
  const out = new Uint8Array(n);
  const c = globalThis.crypto;
  if (c?.getRandomValues) c.getRandomValues(out);
  else for (let i = 0; i < n; i += 1) out[i] = Math.floor(Math.random() * 256);
  return Array.from(out);
}

const hexOf = (bytes: number[]): string => bytes.map((b) => b.toString(16).padStart(2, '0')).join('');

export function newUuid(): string {
  const c = globalThis.crypto;
  if (c?.randomUUID) return c.randomUUID();
  const b = randomBytes(16);
  b[6] = ((b[6] ?? 0) & 0x0f) | 0x40;
  b[8] = ((b[8] ?? 0) & 0x3f) | 0x80;
  const h = hexOf(b);
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-${h.slice(12, 16)}-${h.slice(16, 20)}-${h.slice(20)}`;
}

const ok = (status: number, body?: unknown): DemoResponse => ({ status, body });
const fail = (status: number, code: string, message: string): DemoResponse => ({
  status,
  body: { error: { code, message, request_id: hexOf(randomBytes(4)) } },
});

const failFields = (message: string, fields: Record<string, string>): DemoResponse => ({
  status: 400,
  body: { error: { code: 'VALIDATION_ERROR', message, request_id: hexOf(randomBytes(4)), fields } },
});

const str = (v: unknown): string => (typeof v === 'string' ? v : '');
const isRec = (v: unknown): v is Record<string, unknown> => typeof v === 'object' && v !== null && !Array.isArray(v);
const strList = (v: unknown): string[] => (Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : []);

function paginate<T>(items: T[], query: DemoRequest['query']): { items: T[]; next_cursor: string | null } {
  const limit = Math.min(Math.max(Number(query?.limit ?? 20) || 20, 1), 200);
  const offset = Math.max(Number(query?.cursor ?? 0) || 0, 0);
  const slice = items.slice(offset, offset + limit);
  return { items: slice, next_cursor: offset + limit < items.length ? String(offset + limit) : null };
}

/** Deterministic pseudo-noise in [0, 1) so analytics look organic but never change between renders. */
function noise(a: number, b: number): number {
  const x = Math.sin(a * 12.9898 + b * 78.233) * 43758.5453;
  return x - Math.floor(x);
}

export class DemoEngine {
  readonly state: DemoState;
  private readonly opts: EngineOptions;
  private dirty = false;

  constructor(state: DemoState, opts: EngineOptions = {}) {
    this.state = state;
    this.opts = opts;
  }

  private now(): number {
    return this.opts.now ? this.opts.now() : Date.now();
  }

  private iso(ms = this.now()): string {
    return new Date(ms).toISOString();
  }

  private touch(): void {
    this.dirty = true;
  }

  private flush(): void {
    if (!this.dirty) return;
    this.dirty = false;
    this.opts.onChange?.(this.state);
  }

  // ---------------------------------------------------------------- audit

  private audit(actor: Actor, action: string, resourceType: string, resourceId: string | null, at = this.now()): void {
    const entry: AuditLog = {
      id: newUuid(),
      actor_type: actor.type,
      actor_label: actor.label,
      action,
      resource_type: resourceType,
      resource_id: resourceId,
      request_id: hexOf(randomBytes(4)),
      ip: actor.type === 'user' ? '127.0.0.1' : null,
      created_at: this.iso(at),
    };
    this.state.audit.unshift(entry);
    if (this.state.audit.length > 300) this.state.audit.length = 300;
    this.touch();
  }

  private get user(): Actor {
    return { type: 'user', label: this.state.user.display_name };
  }

  // ---------------------------------------------------------------- scheduler

  /**
   * The simulated worker. Posts whose time has passed are published (even if the tab was
   * closed meanwhile: that is what "scheduled in the past" means here), and "Publish now"
   * calls settle once their simulated provider round-trip is over. Returns true when data changed.
   */
  advance(): boolean {
    this.runScheduler();
    const changed = this.dirty;
    this.flush();
    return changed;
  }

  private runScheduler(): void {
    const now = this.now();
    for (const post of this.state.posts) {
      if (post.status === 'scheduled' && post.scheduled_at && Date.parse(post.scheduled_at) <= now) {
        const at = Date.parse(post.scheduled_at);
        this.startTargets(post);
        this.settle(post, at, Math.min(at + 2000, now), { type: 'scheduler', label: 'scheduler' });
      } else if (post.status === 'publishing' && post.settle_at && Date.parse(post.settle_at) <= now) {
        this.settle(post, Date.parse(post.settle_at) - PUBLISH_MS, Date.parse(post.settle_at), { type: 'scheduler', label: 'scheduler' });
      }
    }
  }

  private startTargets(post: DemoPost): void {
    for (const t of post.targets) if (t.status === 'pending' || t.status === 'failed') t.status = 'publishing';
    post.status = 'publishing';
    this.touch();
  }

  /** Decide the outcome of every `publishing` target and derive the post status from them. */
  private settle(post: DemoPost, startedMs: number, finishedMs: number, actor: Actor): void {
    for (const t of post.targets.filter((x) => x.status === 'publishing')) {
      const acc = this.state.accounts.find((a) => a.id === t.social_account_id);
      let error: { code: string; message: string } | null = null;
      if (!acc || acc.status === 'revoked') error = { code: 'SOCIAL_ACCOUNT_EXPIRED', message: 'This account is no longer connected' };
      else if (acc.status !== 'active' || /#mock-auth/i.test(t.content))
        error = { code: 'SOCIAL_ACCOUNT_EXPIRED', message: `${labelOf(t.platform)} authorization has expired` };
      else if (/FAIL|#mock-fail/.test(t.content)) error = { code: 'PROVIDER_ERROR', message: 'Provider rejected the request (simulated)' };

      t.attempt_count += 1;
      post.attempts = post.attempts ?? [];
      post.attempts.push({
        id: newUuid(),
        post_target_id: t.id,
        attempt_no: t.attempt_count,
        status: error ? 'failed' : 'succeeded',
        started_at: this.iso(startedMs),
        finished_at: this.iso(finishedMs),
        error_code: error?.code ?? null,
        error_message: error?.message ?? null,
      });
      if (error) {
        Object.assign(t, { status: 'failed', error_code: error.code, error_message: error.message });
        this.audit(actor, 'post_target.failed', 'post_target', t.id, finishedMs);
      } else {
        Object.assign(t, {
          status: 'published',
          published_at: this.iso(finishedMs),
          external_url: `https://example.com/${t.platform}/${t.id.slice(0, 6)}`,
          error_code: null,
          error_message: null,
        });
        this.audit(actor, 'post_target.published', 'post_target', t.id, finishedMs);
      }
    }
    const live = post.targets.filter((t) => t.status !== 'cancelled');
    const states = live.map((t) => t.status);
    post.status =
      live.length === 0
        ? 'cancelled'
        : states.every((s) => s === 'published')
          ? 'published'
          : states.some((s) => s === 'published')
            ? 'partially_published'
            : 'failed';
    if (post.status === 'published' || post.status === 'partially_published') post.published_at = this.iso(finishedMs);
    post.settle_at = null;
    post.updated_at = this.iso(finishedMs);
    this.touch();
  }

  // ---------------------------------------------------------------- router

  handle(req: DemoRequest): DemoResponse {
    this.runScheduler();
    let res: DemoResponse;
    try {
      res = this.route(req);
    } catch (e) {
      res = fail(500, 'INTERNAL', e instanceof Error ? e.message : 'Internal error');
    }
    this.flush();
    return res;
  }

  private route(req: DemoRequest): DemoResponse {
    const { method: m, path, query } = req;
    const body: Record<string, unknown> = isRec(req.body) ? req.body : {};
    const s = this.state;
    let r: RegExpMatchArray | null;

    if (path === '/health') return ok(200, { status: 'ok' });

    // ---- auth
    if (path === '/auth/register' && m === 'POST') {
      const email = str(body.email).trim();
      const password = str(body.password);
      if (!email || password.length < 8) return fail(400, 'VALIDATION_ERROR', 'Email and a password of 8+ characters are required');
      if (body.accept_terms !== true) return fail(400, 'VALIDATION_ERROR', 'You must accept the Terms and the Privacy Policy');
      if (email === s.user.email) return fail(409, 'CONFLICT', 'Email already registered');
      // Single-user demo: registering renames the one demo user instead of creating a tenant.
      Object.assign(s.user, { email, password, display_name: str(body.display_name).trim() || email });
      s.signed_in = true;
      this.audit(this.user, 'user.registered', 'user', s.user.id);
      return ok(201, this.me());
    }
    if (path === '/auth/login' && m === 'POST') {
      if (str(body.email) !== s.user.email || str(body.password) !== s.user.password) {
        return fail(401, 'UNAUTHENTICATED', 'Invalid email or password');
      }
      s.signed_in = true;
      this.audit(this.user, 'user.login', 'user', s.user.id);
      return ok(200, this.me());
    }

    // Provider sign-in (D-023): simulated, nothing leaves the browser.
    if (path === '/auth/providers' && m === 'GET') return ok(200, { providers: SIGN_IN_PROVIDERS });
    if ((r = path.match(/^\/auth\/oauth\/(\w+)\/start$/)) && m === 'GET') {
      const res = startSocialSignIn(s, r[1] ?? '', query?.next);
      if (res.status === 200) this.touch();
      return res;
    }
    if (path === '/auth/oauth/pending' && m === 'GET') return pendingSignup(s);
    if (path === '/auth/oauth/complete' && m === 'POST') {
      const res = completeSignup(s, body);
      if (res.status !== 201) return res;
      this.audit(this.user, 'user.registered', 'user', s.user.id);
      return ok(201, this.me());
    }

    // Mail-driven flows. The demo sends no mail, so any token works except "expired".
    if (path === '/auth/verify-email' && m === 'POST') return this.redeemToken(str(body.token), () => ok(200, { email_verified: true }));
    if (path === '/auth/password/forgot' && m === 'POST') return ok(202, { status: 'accepted', delivery: 'log' });
    if (path === '/auth/password/reset' && m === 'POST') {
      return this.redeemToken(str(body.token), () => {
        const password = str(body.password);
        if (password.length < 8) return fail(400, 'VALIDATION_ERROR', 'password must be 8-128 characters');
        s.user.password = password;
        return ok(204);
      });
    }

    // Agent sign-up (D-025): public routes; confirming an existing account checks the session itself.
    if (path.startsWith('/auth/agent-signups') && m === 'POST') {
      const res = handleAgentSignup(s, path, body, { now: this.now(), newId: newUuid, me: () => this.me() as Record<string, unknown> });
      if (res) {
        if (res.status === 200 && path.endsWith('/confirm')) this.audit(this.user, 'agent_signup.confirmed', 'api_key', null);
        return res;
      }
    }

    // ---- everything below needs a session
    if (!s.signed_in) return fail(401, 'UNAUTHENTICATED', 'Authentication required');

    if (path === '/auth/logout' && m === 'POST') {
      this.audit(this.user, 'user.logout', 'user', s.user.id);
      s.signed_in = false;
      return ok(204);
    }
    if (path === '/me' && m === 'GET') return ok(200, this.me());
    if (path === '/auth/verify-email/resend' && m === 'POST') return fail(409, 'CONFLICT', 'email is already verified');
    if (path === '/auth/password/change' && m === 'POST') return this.changePassword(body);
    // The demo has no sign-in provider: the user has a password and nothing to link, so nothing pretends to connect.
    if (path === '/auth/identities' && m === 'GET') return ok(200, { identities: [], has_password: true });
    if (/^\/auth\/identities\/[^/]+(\/link)?$/.test(path) && (m === 'POST' || m === 'DELETE')) {
      return fail(404, 'NOT_FOUND', 'Sign-in with other providers is not available in the demo');
    }
    if (path === '/auth/password/set' && m === 'POST') return fail(409, 'CONFLICT', 'You already have a password. Change it instead.');

    // ---- social
    if (path === '/social/providers' && m === 'GET') return ok(200, { items: PROVIDERS });
    if (path === '/social/accounts' && m === 'GET') return ok(200, { items: s.accounts, next_cursor: null });
    if (path === '/social/telegram/connect' && m === 'POST') return this.startTelegramLink();
    if ((r = path.match(/^\/social\/telegram\/connect\/([^/]+)$/)) && m === 'GET') return this.telegramLinkStatus(r[1] ?? '');
    if (path === '/social/accounts/token' && m === 'POST') return this.connectWithToken(body);
    if ((r = path.match(/^\/social\/(\w+)\/connect$/)) && (m === 'POST' || m === 'GET')) return this.instantConnect(r[1] ?? '');
    if ((r = path.match(/^\/social\/accounts\/([^/]+)$/))) {
      const acc = s.accounts.find((a) => a.id === r?.[1]);
      if (!acc) return fail(404, 'NOT_FOUND', 'Account not found');
      if (m === 'DELETE') return this.disconnect(acc);
      return ok(200, acc);
    }

    // ---- posts
    if (path === '/posts' && m === 'POST') return this.createPost(body);
    if (path === '/posts' && m === 'GET') return this.listPosts(query);
    if ((r = path.match(/^\/posts\/([^/]+)(?:\/(publish|schedule|cancel|retry|status))?$/))) {
      const post = s.posts.find((p) => p.id === r?.[1]);
      if (!post) return fail(404, 'NOT_FOUND', 'Post not found');
      const act = r[2];
      if (!act && m === 'GET') return ok(200, this.publicPost(post, true));
      if (act === 'status' && m === 'GET') {
        return ok(200, { id: post.id, status: post.status, targets: post.targets.map((t) => ({ id: t.id, status: t.status })) });
      }
      if (!act && m === 'DELETE') {
        if (post.status === 'publishing') return fail(409, 'INVALID_STATE_TRANSITION', 'A post that is publishing cannot be deleted');
        s.posts = s.posts.filter((p) => p !== post);
        this.audit(this.user, 'post.deleted', 'post', post.id);
        return ok(204);
      }
      if (!act && m === 'PATCH') return this.patchPost(post, body);
      if (act && m === 'POST') return this.transition(post, act as 'publish' | 'schedule' | 'cancel' | 'retry', body);
      return fail(404, 'NOT_FOUND', 'Not found');
    }

    // ---- media
    if (path === '/media' && m === 'POST') return this.uploadMedia(req.upload);
    if (path === '/media' && m === 'GET') return ok(200, { items: [...s.media].reverse(), next_cursor: null });
    if ((r = path.match(/^\/media\/([^/]+)$/))) {
      const med = s.media.find((x) => x.id === r?.[1]);
      if (!med) return fail(404, 'NOT_FOUND', 'Media not found');
      if (m === 'DELETE') {
        s.media = s.media.filter((x) => x !== med);
        this.audit(this.user, 'media.deleted', 'media', med.id);
        return ok(204);
      }
      return ok(200, med);
    }

    // ---- dashboard, analytics, audit
    if (path === '/account/usage' && m === 'GET') return ok(200, demoUsage(s, this.now()));
    if (path === '/account/delete' && m === 'POST') {
      const res = requestDeletion(s, body, this.now());
      if (res.status === 202) this.touch();
      return res;
    }
    if (path === '/account/delete/cancel' && m === 'POST') {
      const res = cancelDeletion(s);
      if (res.status === 204) this.touch();
      return res;
    }
    const exported = handleExports(s, m, path, this.now(), () => this.touch());
    if (exported) return exported;
    if (path === '/dashboard/summary' && m === 'GET') return ok(200, this.summary());
    if (path === '/analytics' && m === 'GET') return ok(200, { items: this.analytics(query) });
    if (path === '/audit-logs' && m === 'GET') return ok(200, paginate(query?.action ? s.audit.filter((a) => a.action === query.action) : s.audit, query));

    // ---- approvals
    if (path === '/approvals' && m === 'GET') {
      return ok(200, paginate(visibleApprovals(s.approvals, query?.status === 'all' ? 'all' : 'pending', this.now()), query));
    }
    if ((r = path.match(/^\/approvals\/([^/]+)\/(approve|deny)$/)) && m === 'POST') return this.decideApproval(r[1] ?? '', r[2] === 'approve');
    if ((r = path.match(/^\/approvals\/([^/]+)$/)) && m === 'GET') {
      const a = findApproval(s.approvals, r[1] ?? '');
      return a ? ok(200, a) : fail(404, 'NOT_FOUND', 'approval not found');
    }

    // ---- developer
    if (path === '/developer/api-keys' && m === 'GET') return ok(200, { items: s.api_keys });
    if (path === '/developer/api-keys' && m === 'POST') return this.createApiKey(body);
    if ((r = path.match(/^\/developer\/api-keys\/([^/]+)$/)) && m === 'DELETE') {
      const k = s.api_keys.find((x) => x.id === r?.[1]);
      if (!k) return fail(404, 'NOT_FOUND', 'Key not found');
      k.revoked_at = this.iso();
      this.audit(this.user, 'api_key.revoked', 'api_key', k.id);
      return ok(204);
    }
    if (path === '/developer/mcp-connections' && m === 'GET') return ok(200, { items: s.mcp_connections });
    if (path === '/developer/mcp-connections' && m === 'POST') return this.createMcp(body);
    if ((r = path.match(/^\/developer\/mcp-connections\/([^/]+)$/)) && m === 'DELETE') {
      const c = s.mcp_connections.find((x) => x.id === r?.[1]);
      if (!c) return fail(404, 'NOT_FOUND', 'Connection not found');
      c.revoked_at = this.iso();
      this.audit(this.user, 'mcp_connection.revoked', 'mcp_connection', c.id);
      return ok(204);
    }
    if (path === '/developer/usage' && m === 'GET') return ok(200, this.usage());

    return fail(404, 'NOT_FOUND', 'Not found');
  }

  private decideApproval(id: string, approve: boolean): DemoResponse {
    const d = decide(this.state.approvals, id, approve ? 'approved' : 'denied', this.now());
    if (!d.ok) return fail(d.status, d.code, d.message);
    this.audit(this.user, approve ? 'approval.approved' : 'approval.denied', 'approval', id);
    return ok(200, d.approval);
  }

  // ---------------------------------------------------------------- auth

  private me(): unknown {
    const { id, email, display_name } = this.state.user;
    // The demo user is always verified and nothing is restricted.
    return {
      id, email, display_name, csrf_token: 'demo', scopes: ALL_SCOPES,
      user: {
        id, email, display_name, email_verified: true, has_password: true, login_methods: ['password'], plan: 'free',
        deletion_scheduled_at: this.state.user.deletion_scheduled_at ?? null,
      },
      verification_enforced: false, mail_delivery: 'log', deletion_grace_days: DEMO_GRACE_DAYS,
    };
  }

  private redeemToken(token: string, ok200: () => DemoResponse): DemoResponse {
    if (!token || token === 'expired') return fail(400, 'VALIDATION_ERROR', 'link is invalid or has expired');
    return ok200();
  }

  private changePassword(body: Record<string, unknown>): DemoResponse {
    const user = this.state.user;
    if (str(body.current_password) !== user.password) return fail(400, 'VALIDATION_ERROR', 'current password is incorrect');
    const next = str(body.new_password);
    if (next.length < 8 || next.length > 128) return fail(400, 'VALIDATION_ERROR', 'password must be 8-128 characters');
    user.password = next;
    this.audit(this.user, 'user.password_changed', 'user', user.id);
    return ok(204);
  }

  // ---------------------------------------------------------------- accounts

  private provider(id: string): WireProvider | undefined {
    return PROVIDERS.find((p) => p.provider === id);
  }

  /** Real OAuth cannot run in a static demo, so "Connect" adds a sample account straight away. */
  private instantConnect(providerId: string): DemoResponse {
    const p = this.provider(providerId);
    if (!p || p.status === 'unsupported' || !p.configured) return fail(501, 'PROVIDER_NOT_AVAILABLE', 'Provider not available');
    if (providerId === 'telegram') return fail(400, 'VALIDATION_ERROR', 'Telegram is connected with a one-time code');
    const full = demoQuotaError(this.state, 'connected_accounts', 1, this.now());
    if (full) return fail(403, 'QUOTA_EXCEEDED', full);
    const n = this.state.accounts.filter((a) => a.provider === providerId).length + 1;
    const people = ['Riley Chen', 'Sam Okafor', 'Taylor Brooks', 'Morgan Ito'];
    const display = providerId === 'linkedin' ? (people[(n - 1) % people.length] ?? 'Demo Profile') : `Mock Network ${n}`;
    const acc: SocialAccount = {
      id: newUuid(),
      provider: providerId,
      username: `${providerId}-demo-${n}`,
      display_name: display,
      avatar_url: null,
      status: 'active',
      scopes: [],
      connected_at: this.iso(),
    };
    this.state.accounts.push(acc);
    this.audit(this.user, 'social_account.connected', 'social_account', acc.id);
    return ok(201, acc);
  }

  /** Mirrors `POST /social/accounts/token`: same validation and error shapes; the "verify" step accepts anything but a secret containing "invalid". */
  private connectWithToken(body: unknown): DemoResponse {
    const b = (typeof body === 'object' && body !== null ? body : {}) as { provider?: unknown; fields?: unknown };
    const p = typeof b.provider === 'string' ? this.provider(b.provider) : undefined;
    const spec = p?.capabilities.connect_fields;
    if (!p || p.capabilities.connect_method !== 'token' || !spec) return fail(400, 'VALIDATION_ERROR', 'provider cannot be connected with a token');
    const input = (typeof b.fields === 'object' && b.fields !== null ? b.fields : {}) as Record<string, unknown>;
    const values: Record<string, string> = {};
    for (const [name, raw] of Object.entries(input)) {
      const f = spec.find((x) => x.name === name);
      if (!f || typeof raw !== 'string') return failFields('invalid fields', { fields: 'unknown field' });
      values[name] = raw.trim();
      if (values[name] && f.kind === 'url' && !/^https:\/\/[^/@\s]+/.test(values[name])) return failFields(`${f.label} must be an https URL`, { [name]: 'must be an https URL' });
    }
    const missing = spec.find((f) => f.required && !values[f.name]);
    if (missing) return failFields(`${missing.label} is required`, { [missing.name]: 'required' });
    if (spec.some((f) => (f.secret || f.kind === 'secret') && /invalid/i.test(values[f.name] ?? ''))) {
      return fail(400, 'VALIDATION_ERROR', `${this.providerName(p)} rejected these credentials`);
    }
    const who = values.handle || values.instance_url?.replace(/^https:\/\//, '').replace(/\/.*$/, '') || 'webhook';
    const username = p.provider === 'mastodon' ? `demo@${who}` : p.provider === 'discord' ? '#general' : who;
    let acc = this.state.accounts.find((a) => a.provider === p.provider && a.username === username);
    if (acc) acc.status = 'active';
    else {
      const full = demoQuotaError(this.state, 'connected_accounts', 1, this.now());
      if (full) return fail(403, 'QUOTA_EXCEEDED', full);
      acc = { id: newUuid(), provider: p.provider, username, display_name: `${this.providerName(p)} ${username}`, avatar_url: null, status: 'active', scopes: [], connected_at: this.iso() };
      this.state.accounts.push(acc);
    }
    this.audit(this.user, 'social_account.connected', 'social_account', acc.id);
    return ok(201, acc);
  }

  private providerName(p: WireProvider): string {
    return p.provider.charAt(0).toUpperCase() + p.provider.slice(1);
  }

  private disconnect(acc: SocialAccount): DemoResponse {
    this.state.accounts = this.state.accounts.filter((a) => a !== acc);
    // Posts that have not gone out yet no longer target it.
    for (const post of this.state.posts) {
      if (post.status !== 'draft' && post.status !== 'scheduled') continue;
      for (const t of post.targets) if (t.social_account_id === acc.id) t.status = 'cancelled';
      if (post.targets.every((t) => t.status === 'cancelled')) post.status = 'cancelled';
    }
    this.audit(this.user, 'social_account.disconnected', 'social_account', acc.id);
    return ok(204);
  }

  private startTelegramLink(): DemoResponse {
    const s = this.state;
    const t = this.now();
    // Newest first; the reverse() makes insertion order break ties between codes made in the same millisecond.
    const active = s.links
      .filter((l) => !l.account_id && Date.parse(l.expires_at) > t)
      .reverse()
      .sort((a, b) => Date.parse(b.created_at) - Date.parse(a.created_at));
    // At most 3 active codes: creating another retires the oldest.
    for (const l of active.slice(MAX_ACTIVE_LINKS - 1)) l.expires_at = this.iso(t);
    const code = `SOS-${randomBytes(8)
      .map((b) => LINK_ALPHABET[b % LINK_ALPHABET.length] ?? 'X')
      .join('')}`;
    const link: DemoLink = { id: newUuid(), code, created_at: this.iso(t), expires_at: this.iso(t + LINK_TTL_MS), account_id: null };
    s.links.push(link);
    if (s.links.length > 20) s.links.splice(0, s.links.length - 20);
    this.touch();
    return ok(201, {
      id: link.id,
      code,
      expires_at: link.expires_at,
      bot_username: 'steerpost_bot',
      instructions: `Add @steerpost_bot as an administrator of your Telegram channel or group with the "Post messages" right. Post this code there as a normal message: ${code}. The code expires in 15 minutes and works once.`,
    });
  }

  /** Stands in for "the user posted the code in their channel and the bot saw it". */
  private telegramLinkStatus(id: string): DemoResponse {
    const s = this.state;
    const link = s.links.find((l) => l.id === id);
    if (!link) return fail(404, 'NOT_FOUND', 'link not found');
    const now = this.now();
    const delay = this.opts.telegramDelayMs ?? 4000;
    if (!link.account_id && delay > 0 && Date.parse(link.expires_at) > now && now - Date.parse(link.created_at) >= delay) {
      const n = s.accounts.filter((a) => a.provider === 'telegram').length + 1;
      const acc: SocialAccount = {
        id: newUuid(),
        provider: 'telegram',
        username: `@demo_channel_${n}`,
        display_name: `Demo Channel ${n}`,
        avatar_url: null,
        status: 'active',
        scopes: ['post_messages'],
        connected_at: this.iso(now),
      };
      s.accounts.push(acc);
      link.account_id = acc.id;
      this.audit({ type: 'system', label: 'telegram' }, 'social_account.connected', 'social_account', acc.id);
    }
    if (link.account_id) {
      const acc = s.accounts.find((a) => a.id === link.account_id);
      return ok(200, { status: 'connected', account: acc });
    }
    return ok(200, { status: Date.parse(link.expires_at) > now ? 'pending' : 'expired' });
  }

  // ---------------------------------------------------------------- posts

  /** What the API returns: the stored post without server-side bookkeeping. */
  private publicPost(p: DemoPost, full: boolean): Post {
    const base: Post = {
      id: p.id,
      title: p.title,
      content: p.content ?? p.targets[0]?.content,
      status: p.status,
      scheduled_at: p.scheduled_at,
      published_at: p.published_at,
      created_by: p.created_by,
      created_by_ref: p.created_by_ref,
      created_at: p.created_at,
      updated_at: p.updated_at,
      targets: p.targets,
    };
    return full ? { ...base, media: this.state.media.filter((m) => p.media_ids.includes(m.id)), attempts: p.attempts ?? [] } : base;
  }

  private createPost(body: Record<string, unknown>): DemoResponse {
    const s = this.state;
    const content = str(body.content);
    const overrides = Array.isArray(body.targets) ? body.targets.filter(isRec) : [];
    const ids = strList(body.social_account_ids);
    if (!content.trim() && overrides.length === 0) return fail(400, 'VALIDATION_ERROR', 'content is required');
    const accs = ids.map((id) => s.accounts.find((a) => a.id === id));
    if (accs.length === 0 || accs.some((a) => !a)) return fail(400, 'VALIDATION_ERROR', 'social_account_ids must reference your accounts');
    const accounts = accs.filter((a): a is SocialAccount => !!a);
    const mediaIds = strList(body.media_ids);
    const media = mediaIds.map((id) => s.media.find((x) => x.id === id));
    if (media.some((x) => !x)) return fail(400, 'VALIDATION_ERROR', 'media_ids must reference your media');

    const built = this.buildTargets(accounts, content, overrides, media.length);
    if (!Array.isArray(built)) return built;
    const targets = built;

    const scheduleAt = str(body.scheduled_at);
    const wantSchedule = body.schedule === true && !!scheduleAt;
    if (wantSchedule && !(Date.parse(scheduleAt) > this.now())) {
      return fail(400, 'VALIDATION_ERROR', 'scheduled_at must be in the future');
    }
    const full = wantSchedule ? demoQuotaError(this.state, 'scheduled_posts_month', 1, this.now()) : null;
    if (full) return fail(403, 'QUOTA_EXCEEDED', full);
    const at = this.iso();
    const post: DemoPost = {
      id: newUuid(),
      title: str(body.title) || null,
      content,
      status: wantSchedule ? 'scheduled' : 'draft',
      scheduled_at: wantSchedule ? new Date(scheduleAt).toISOString() : null,
      published_at: null,
      created_by: 'user',
      created_by_ref: null,
      created_at: at,
      updated_at: at,
      targets,
      attempts: [],
      media_ids: mediaIds,
      settle_at: null,
      quota_counted: wantSchedule,
    };
    s.posts.push(post);
    this.audit(this.user, 'post.created', 'post', post.id);
    if (wantSchedule) this.audit(this.user, 'post.scheduled', 'post', post.id);
    return ok(201, this.publicPost(post, true));
  }

  /** One pending target per account: its override text or the base text, checked against the network's limits. */
  private buildTargets(accounts: SocialAccount[], content: string, overrides: Record<string, unknown>[], mediaCount: number): PostTarget[] | DemoResponse {
    const targets: PostTarget[] = [];
    for (const a of accounts) {
      const override = overrides.find((o) => o.social_account_id === a.id);
      const text = str(override?.content) || content;
      const cap = this.provider(a.provider)?.capabilities;
      const label = a.display_name || a.username;
      if (a.status !== 'active') return fail(422, 'SOCIAL_ACCOUNT_EXPIRED', `${label}: authorization has expired. Reconnect it first.`);
      if (cap && cap.max_text_length > 0 && Array.from(text).length > cap.max_text_length) {
        return fail(400, 'VALIDATION_ERROR', `${label}: text is longer than the ${cap.max_text_length} character limit`);
      }
      if (cap && mediaCount > cap.max_media_count) return fail(400, 'VALIDATION_ERROR', `${label}: too many attachments`);
      targets.push({
        id: newUuid(),
        social_account_id: a.id,
        platform: a.provider,
        content: text,
        status: 'pending',
        external_url: null,
        published_at: null,
        error_code: null,
        error_message: null,
        attempt_count: 0,
      });
    }
    return targets;
  }

  /** `PATCH /posts/{id}`: draft or scheduled only; absent fields stay as they are, like the real API. */
  private patchPost(post: DemoPost, body: Record<string, unknown>): DemoResponse {
    if (post.status !== 'draft' && post.status !== 'scheduled') {
      return fail(409, 'INVALID_STATE_TRANSITION', `post in status ${post.status} cannot be edited`);
    }
    const s = this.state;
    const content = typeof body.content === 'string' ? body.content : (post.content ?? post.targets[0]?.content ?? '');
    const ids = Array.isArray(body.social_account_ids) ? strList(body.social_account_ids) : post.targets.map((t) => t.social_account_id);
    const overrides = Array.isArray(body.targets) ? body.targets.filter(isRec) : post.targets.map((t) => ({ social_account_id: t.social_account_id, content: t.content }));
    if (!content.trim() && overrides.every((o) => !str(o.content).trim())) return fail(400, 'VALIDATION_ERROR', 'content is required');
    const accs = ids.map((id) => s.accounts.find((a) => a.id === id));
    if (accs.length === 0 || accs.some((a) => !a)) return fail(400, 'VALIDATION_ERROR', 'social_account_ids must reference your accounts');
    const mediaIds = Array.isArray(body.media_ids) ? strList(body.media_ids) : post.media_ids;
    if (mediaIds.some((id) => !s.media.some((m) => m.id === id))) return fail(400, 'VALIDATION_ERROR', 'media_ids must reference your media');
    let at: string | null = post.scheduled_at;
    if (typeof body.scheduled_at === 'string') {
      if (post.status !== 'scheduled') return fail(400, 'VALIDATION_ERROR', 'use POST /posts/{id}/schedule to schedule a draft');
      if (!(Date.parse(body.scheduled_at) > this.now())) return fail(400, 'VALIDATION_ERROR', 'scheduled_at must be in the future');
      at = new Date(body.scheduled_at).toISOString();
    }
    const built = this.buildTargets(accs.filter((a): a is SocialAccount => !!a), content, overrides, mediaIds.length);
    if (!Array.isArray(built)) return built;
    for (const t of built) {
      const old = post.targets.find((x) => x.social_account_id === t.social_account_id);
      if (old) t.id = old.id;
    }
    if ('title' in body) post.title = str(body.title).trim() || null;
    post.content = content;
    post.targets = built;
    post.media_ids = mediaIds;
    post.scheduled_at = at;
    post.updated_at = this.iso();
    this.audit(this.user, 'post.updated', 'post', post.id);
    return ok(200, this.publicPost(post, true));
  }

  private listPosts(query: DemoRequest['query']): DemoResponse {
    const status = str(query?.status);
    const from = str(query?.from);
    const to = str(query?.to);
    const list = this.state.posts
      .filter((p) => {
        if (status && p.status !== status) return false;
        const t = postTime(p);
        return (!from || t >= from) && (!to || t < to);
      })
      .sort((a, b) => b.created_at.localeCompare(a.created_at) || b.id.localeCompare(a.id));
    const page = paginate(list, query);
    return ok(200, { ...page, items: page.items.map((p) => this.publicPost(p, false)) });
  }

  private transition(post: DemoPost, act: 'publish' | 'schedule' | 'cancel' | 'retry', body: Record<string, unknown>): DemoResponse {
    const to: PostStatus = { publish: 'publishing', schedule: 'scheduled', cancel: 'cancelled', retry: 'publishing' }[act] as PostStatus;
    const retryable = post.status === 'failed' || post.status === 'partially_published';
    if (!(TRANSITIONS[post.status] ?? []).includes(to) || (act === 'retry' && !retryable)) {
      return fail(409, 'INVALID_STATE_TRANSITION', `Cannot go from ${post.status} to ${to}`);
    }
    const now = this.now();
    // Seeded posts carry no flag: pin it from the status before anything changes it, so a later unschedule keeps the slot.
    post.quota_counted ??= post.status !== 'draft' && post.status !== 'cancelled';
    if (!post.quota_counted && (act === 'schedule' || act === 'publish' || act === 'retry')) {
      const full = demoQuotaError(this.state, 'scheduled_posts_month', 1, now);
      if (full) return fail(403, 'QUOTA_EXCEEDED', full);
      post.quota_counted = true;
    }
    if (act === 'schedule') {
      const at = str(body.scheduled_at);
      if (!at || !(Date.parse(at) > now)) return fail(400, 'VALIDATION_ERROR', 'scheduled_at must be in the future');
      post.scheduled_at = new Date(at).toISOString();
      // A failed target is retried when the schedule fires.
      for (const t of post.targets) if (t.status === 'failed') Object.assign(t, { status: 'pending', error_code: null, error_message: null });
    }
    if (act === 'cancel') for (const t of post.targets) t.status = 'cancelled';
    if (act === 'publish' || act === 'retry') {
      for (const t of post.targets) if (t.status === 'pending' || t.status === 'failed') t.status = 'publishing';
      post.settle_at = this.iso(now + PUBLISH_MS);
    }
    post.status = to;
    post.updated_at = this.iso(now);
    const action = { publish: 'post.publish_requested', schedule: 'post.scheduled', cancel: 'post.cancelled', retry: 'post.retried' }[act];
    this.audit(this.user, action, 'post', post.id);
    return ok(200, this.publicPost(post, true));
  }

  // ---------------------------------------------------------------- media

  private uploadMedia(upload: DemoRequest['upload']): DemoResponse {
    if (!upload) return fail(400, 'VALIDATION_ERROR', 'file is required');
    if (!/^(image\/(jpeg|png|webp|gif)|video\/(mp4|quicktime))$/.test(upload.mime)) {
      return fail(400, 'VALIDATION_ERROR', 'Unsupported media type');
    }
    const kind: Media['kind'] = upload.mime.startsWith('image') ? 'image' : 'video';
    if (upload.size > (kind === 'image' ? 10 : 100) * 1024 * 1024) return fail(400, 'VALIDATION_ERROR', 'File is too large');
    const full = demoQuotaError(this.state, 'media_bytes', upload.size, this.now());
    if (full) return fail(403, 'QUOTA_EXCEEDED', full);
    const med: Media = {
      id: newUuid(),
      kind,
      mime_type: upload.mime,
      size_bytes: upload.size,
      original_name: upload.name,
      width: null,
      height: null,
      status: 'ready',
      url: kind === 'image' ? (upload.url ?? svgThumb(upload.name, Math.floor(noise(upload.size, 3) * 360))) : undefined,
      created_at: this.iso(),
    };
    this.state.media.push(med);
    this.audit(this.user, 'media.uploaded', 'media', med.id);
    return ok(201, med);
  }

  // ---------------------------------------------------------------- dashboard, analytics, usage

  private summary(): unknown {
    const ps = this.state.posts;
    const by = (st: PostStatus): DemoPost[] => ps.filter((p) => p.status === st);
    const nowD = new Date(this.now());
    const monthStart = new Date(nowD.getFullYear(), nowD.getMonth(), 1).toISOString();
    const monthEnd = new Date(nowD.getFullYear(), nowD.getMonth() + 1, 1).toISOString();
    const publishedThisMonth = ps.filter(
      (p) => (p.status === 'published' || p.status === 'partially_published') && !!p.published_at && p.published_at >= monthStart && p.published_at < monthEnd,
    );
    return {
      connected_accounts: this.state.accounts.length,
      scheduled_posts: by('scheduled').length,
      drafts: by('draft').length,
      published_this_month: publishedThisMonth.length,
      failed: by('failed').length,
      upcoming: by('scheduled')
        .sort((a, b) => (a.scheduled_at ?? '').localeCompare(b.scheduled_at ?? ''))
        .slice(0, 5)
        .map((p) => this.publicPost(p, false)),
      recent: [...by('published')]
        .sort((a, b) => (b.published_at ?? '').localeCompare(a.published_at ?? ''))
        .slice(0, 5)
        .map((p) => this.publicPost(p, false)),
    };
  }

  /** Daily counters for the Mock network (the only demo provider with analytics), derived from `now`. */
  private analytics(query: DemoRequest['query']): AnalyticsPoint[] {
    const acc = this.state.accounts.find((a) => a.provider === 'mock');
    if (!acc) return [];
    const from = str(query?.from);
    const to = str(query?.to);
    const now = this.now();
    const metrics: [string, number, number][] = [
      ['impressions', 420, 380],
      ['reactions', 28, 26],
      ['link_clicks', 9, 12],
    ];
    const out: AnalyticsPoint[] = [];
    for (let back = 89; back >= 0; back -= 1) {
      const day = Math.floor(now / DAY) - back;
      const captured_at = new Date(day * DAY + 12 * 3_600_000).toISOString();
      if ((from && captured_at < from) || (to && captured_at > to)) continue;
      metrics.forEach(([metric, base, spread], mi) => {
        const weekday = new Date(day * DAY).getUTCDay();
        const weekend = weekday === 0 || weekday === 6 ? 0.7 : 1;
        const trend = 1 + (89 - back) / 220;
        const value = Math.round((base + spread * noise(day, mi + 1)) * weekend * trend);
        out.push({ metric, value, captured_at, social_account_id: acc.id });
      });
    }
    return out;
  }

  private usage(): unknown {
    const s = this.state;
    const named = [
      ...s.api_keys.map((k) => ({ name: k.name, id: k.id, last: k.last_used_at })),
      ...s.mcp_connections.map((c) => ({ name: c.name, id: c.id, last: c.last_seen_at })),
    ];
    const by_key = named
      .map((n) => ({ name: n.name, requests: s.usage[n.id] ?? 0, last_used_at: n.last }))
      .filter((k) => k.requests > 0)
      .sort((a, b) => b.requests - a.requests);
    const total = by_key.reduce((sum, k) => sum + k.requests, 0);
    const days = 14;
    const weights = Array.from({ length: days }, (_, i) => 0.6 + noise(i + 1, 7));
    const wsum = weights.reduce((a, b) => a + b, 0);
    let left = total;
    const by_day = weights.map((w, i) => {
      const requests = i === days - 1 ? left : Math.round((w / wsum) * total);
      left -= requests;
      return { day: new Date(this.now() - (days - 1 - i) * DAY).toISOString().slice(0, 10), requests: Math.max(requests, 0) };
    });
    return { total_requests: total, by_key, by_day };
  }

  // ---------------------------------------------------------------- developer

  private rawKey(): string {
    return `sk_live_${hexOf(randomBytes(2))}_demo_${hexOf(randomBytes(10))}`;
  }

  private createApiKey(body: Record<string, unknown>): DemoResponse {
    const scopes = strList(body.scopes);
    const name = str(body.name).trim();
    if (!name || scopes.length === 0 || scopes.some((x) => !ALL_SCOPES.includes(x))) {
      return fail(400, 'VALIDATION_ERROR', 'name and valid scopes are required');
    }
    const raw = this.rawKey();
    const key: ApiKey = {
      id: newUuid(),
      name,
      prefix: raw.slice(0, 12),
      scopes,
      expires_at: str(body.expires_at) || null,
      revoked_at: null,
      last_used_at: null,
      created_at: this.iso(),
      dangerous_policy: body.dangerous_policy === 'trusted' ? 'trusted' : 'approve',
    };
    if (body.dangerous_policy !== undefined && body.dangerous_policy !== 'trusted' && body.dangerous_policy !== 'approve') {
      return fail(400, 'VALIDATION_ERROR', 'dangerous_policy must be approve or trusted');
    }
    this.state.api_keys.push(key);
    this.audit(this.user, 'api_key.created', 'api_key', key.id);
    return ok(201, { key, raw_key: raw });
  }

  private createMcp(body: Record<string, unknown>): DemoResponse {
    const scopes = strList(body.scopes);
    const name = str(body.name).trim();
    if (!name || scopes.length === 0 || scopes.some((x) => !ALL_SCOPES.includes(x))) {
      return fail(400, 'VALIDATION_ERROR', 'name and valid scopes are required');
    }
    const conn: McpConnection = { id: newUuid(), name, client_name: null, scopes, last_seen_at: null, revoked_at: null, created_at: this.iso() };
    this.state.mcp_connections.push(conn);
    this.audit(this.user, 'mcp_connection.created', 'mcp_connection', conn.id);
    return ok(201, { connection: conn, raw_key: this.rawKey() });
  }
}

const LABELS: Record<string, string> = { linkedin: 'LinkedIn', telegram: 'Telegram', mock: 'Test network' };
function labelOf(platform: string): string {
  return LABELS[platform] ?? platform;
}
