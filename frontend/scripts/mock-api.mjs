#!/usr/bin/env node
/**
 * In-memory implementation of the Steerpost REST contract (docs/ARCHITECTURE.md section 4),
 * so the frontend can be run and verified without the Go backend.
 *
 *   PORT=8080 node scripts/mock-api.mjs
 *   demo login: demo@example.com / demo12345
 *
 * Special behaviours: post content containing "FAIL" fails to publish (to exercise error UI).
 *
 * Telegram: POST /social/telegram/connect hands out a one-time code like the real backend. Nobody posts
 * it anywhere here, so the mock "sees" the code in a demo channel MOCK_LINK_DELAY_MS (default 5000) after
 * it was created and connects that channel. MOCK_LINK_TTL_SECONDS (default 900) shortens the code lifetime
 * to try the "expired" screen, and a delay of 0 or less never connects (the code just expires).
 *
 * Token providers (Discord, Mastodon, Bluesky): POST /social/accounts/token validates like the real API and accepts any
 * credential, except a secret containing "invalid" (400 "rejected these credentials") or the Mastodon token "rate-limit" (429).
 */
import { createServer } from 'node:http';
import { randomBytes, randomUUID } from 'node:crypto';

const PORT = Number(process.env.PORT ?? 8080);
// MOCK_VERIFICATION=enforced starts every new user unverified and restricted (the server with MAIL_PROVIDER=smtp);
// MOCK_VERIFICATION=log mimics MAIL_PROVIDER=log (no restrictions, but the "mail is off" notice shows).
const VERIFICATION = process.env.MOCK_VERIFICATION ?? '';
// MOCK_SIGN_IN_PROVIDERS="" hides the "Continue with ..." buttons (a server with no provider configured).
const SIGN_IN_PROVIDERS = (process.env.MOCK_SIGN_IN_PROVIDERS ?? 'github,google').split(',').filter(Boolean).map((id) => ({ id, name: id === 'github' ? 'GitHub' : id === 'google' ? 'Google' : id }));
const pendingSignups = new Map();
// Same rules as the API's redirect.SafePath: no backslash, control characters or embedded scheme.
const safeNext = (p) => (p && p.length <= 512 && p.startsWith('/') && !p.startsWith('//') && !/[\\\u0000-\u001f\u007f]/.test(p) && !p.includes('://') ? p : null);
const LINK_DELAY_MS = Number(process.env.MOCK_LINK_DELAY_MS ?? 5000);
const LINK_TTL_S = Number(process.env.MOCK_LINK_TTL_SECONDS ?? 900);
const MAX_ACTIVE_LINKS = 3;
// Same alphabet as the backend: no 0/O, 1/I/L.
const LINK_ALPHABET = 'ABCDEFGHJKMNPQRSTUVWXYZ23456789';
const now = () => new Date().toISOString();
const inFuture = (h) => new Date(Date.now() + h * 3600_000).toISOString();
const inPast = (h) => new Date(Date.now() - h * 3600_000).toISOString();
const ALL_SCOPES = ['social:read', 'posts:read', 'posts:write', 'posts:schedule', 'posts:publish', 'posts:delete', 'social:disconnect', 'social:connect', 'media:write', 'analytics:read'];

const caps = (o) => ({
  can_publish_text: false, can_publish_image: false, can_publish_video: false, can_schedule: false,
  can_delete: false, can_analytics: false, max_text_length: 0, max_media_count: 0, requires_approval: false, notes: '', ...o,
});
const tokenProvider = (provider, c, connect_fields) => ({ provider, configured: true, status: 'supported', capabilities: caps({ ...c, connect_method: 'token', connect_fields }) });
const PROVIDERS = [
  { provider: 'linkedin', configured: true, status: 'supported', capabilities: caps({ can_publish_text: true, can_publish_image: true, max_text_length: 3000, max_media_count: 9, requires_approval: true, notes: 'Company pages need Marketing Developer Platform approval. Video is not supported yet.' }) },
  { provider: 'telegram', configured: true, status: 'supported', capabilities: caps({ can_publish_text: true, can_publish_image: true, can_publish_video: true, can_delete: true, max_text_length: 4096, max_media_count: 10, notes: "Add the Steerpost bot as an admin with 'Post messages' to your channel or group, then post the one-time code Steerpost gives you there to prove you control it." }) },
  tokenProvider('discord', { can_publish_text: true, can_publish_image: true, can_delete: true, max_text_length: 2000, max_media_count: 10, notes: "Posts into one channel through its webhook, as the webhook's name. Text up to 2000 characters and up to 10 images; mentions are not pinged. No titles, video, threads or scheduling on Discord's side." }, [
    { name: 'webhook_url', label: 'Webhook URL', kind: 'url', secret: true, required: true, placeholder: 'https://discord.com/api/webhooks/...', help: 'Channel settings > Integrations > Webhooks > New Webhook > Copy Webhook URL. The URL is a password: anyone who has it can post in that channel.' },
  ]),
  tokenProvider('mastodon', { can_publish_text: true, can_publish_image: true, max_text_length: 500, max_media_count: 4, notes: 'Mastodon and compatible servers. Posts are public; images only (no video). Limits are read from your instance when you connect.' }, [
    { name: 'instance_url', label: 'Instance URL', kind: 'url', required: true, placeholder: 'https://mastodon.social', help: 'The https address of your server. Servers on private networks cannot be connected.' },
    { name: 'access_token', label: 'Access token', kind: 'secret', secret: true, required: true, help: 'On your server: Preferences > Development > New application. Tick write:statuses, write:media and read:accounts, then copy "Your access token".' },
  ]),
  tokenProvider('bluesky', { can_publish_text: true, can_publish_image: true, can_delete: true, max_text_length: 300, max_media_count: 4, notes: 'Text up to 300 characters, up to 4 images of 2 MB each without alt text. Links and hashtags become clickable; mentions are not linked. Uses an app password. Steerpost schedules; Bluesky has no native scheduling.' }, [
    { name: 'handle', label: 'Handle', kind: 'text', required: true, placeholder: 'name.bsky.social', help: 'Your Bluesky handle, for example name.bsky.social.' },
    { name: 'app_password', label: 'App password', kind: 'secret', secret: true, required: true, placeholder: 'xxxx-xxxx-xxxx-xxxx', help: 'Create one in Settings > Privacy and security > App passwords. Never use your main password.' },
    { name: 'pds', label: 'Server (optional)', kind: 'url', required: false, placeholder: 'https://bsky.social', help: 'Only if you host your own PDS. Leave empty for bsky.social.' },
  ]),
  { provider: 'mock', configured: true, status: 'supported', capabilities: caps({ can_publish_text: true, can_publish_image: true, can_publish_video: true, can_schedule: true, can_delete: true, can_analytics: true, max_text_length: 280, max_media_count: 4, notes: 'Deterministic mock provider for testing.' }) },
  ...['instagram', 'facebook', 'tiktok', 'youtube', 'x', 'threads', 'pinterest'].map((p) => ({
    provider: p, configured: false, status: 'unsupported', capabilities: caps({ requires_approval: true, notes: 'Registered stub: returns PROVIDER_NOT_AVAILABLE.' }),
  })),
];

const users = new Map();
const sessions = new Map();
const db = { accounts: [], posts: [], media: [], keys: [], mcp: [], audit: [], usage: [], links: [], approvals: [], exports: [] };

// `password` is null for an account made with a provider: it has none, and an empty string must never be accepted.
function addUser(email, password, display_name) {
  const u = { id: randomUUID(), email, password: password || null, display_name, verified: VERIFICATION !== 'enforced', identities: [] };
  users.set(email, u);
  return u;
}

function svgThumb(label, hue) {
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="400" height="400"><rect width="400" height="400" fill="hsl(${hue},45%,82%)"/><text x="200" y="210" font-family="sans-serif" font-size="28" text-anchor="middle" fill="hsl(${hue},40%,25%)">${label}</text></svg>`;
  return `data:image/svg+xml;utf8,${encodeURIComponent(svg)}`;
}

function audit(actor_label, action, resource_type, resource_id, actor_type = 'user') {
  db.audit.unshift({ id: randomUUID(), actor_type, actor_label, action, resource_type, resource_id, request_id: randomUUID().slice(0, 8), ip: '127.0.0.1', created_at: now() });
}

function mkTarget(post_id, account, content, status, extra = {}) {
  return { id: randomUUID(), social_account_id: account.id, platform: account.provider, content, status, external_url: null, published_at: null, error_code: null, error_message: null, attempt_count: 0, ...extra };
}

function seed() {
  const u = addUser('demo@example.com', 'demo12345', 'Demo User');
  const mk = (provider, username, display_name) => {
    const a = { id: randomUUID(), user_id: u.id, provider, username, display_name, avatar_url: null, status: 'active', scopes: [], connected_at: inPast(72) };
    db.accounts.push(a);
    return a;
  };
  const li = mk('linkedin', 'alex-morgan', 'Alex Morgan');
  const tg = mk('telegram', '@steerpost_demo', 'Steerpost Demo Channel');
  const mock = mk('mock', 'mock-1', 'Mock Account');
  const post = (title, status, accounts, content, extra = {}) => {
    const p = { id: randomUUID(), user_id: u.id, title, status, scheduled_at: null, published_at: null, created_by: 'user', created_at: inPast(48), updated_at: now(), media_ids: [], deleted: false, attempts: [], ...extra };
    p.targets = accounts.map((a) => mkTarget(p.id, a, content, status === 'published' ? 'published' : status === 'failed' ? 'failed' : status === 'scheduled' ? 'pending' : 'pending'));
    db.posts.push(p);
    return p;
  };
  post('Launch announcement', 'scheduled', [li, tg], 'We are launching Steerpost next week. Write once, publish everywhere.', { scheduled_at: inFuture(26) });
  post('Weekly tip', 'scheduled', [tg], 'Tip: schedule posts in your audience timezone.', { scheduled_at: inFuture(5) });
  post('Draft: case study', 'draft', [li], 'Case study draft - how a 3-person team halved their publishing time.');
  const pub = post('Hello world', 'published', [li, tg], 'Hello world from Steerpost!', { published_at: inPast(30) });
  pub.targets.forEach((t) => Object.assign(t, { published_at: inPast(30), external_url: 'https://example.com/post/1', attempt_count: 1 }));
  pub.attempts = pub.targets.map((t) => ({ id: randomUUID(), post_target_id: t.id, attempt_no: 1, status: 'succeeded', started_at: inPast(30), finished_at: inPast(30), error_code: null, error_message: null }));
  const bad = post('Broken post', 'failed', [li], 'This one failed to publish.', { scheduled_at: inPast(3) });
  const bt = bad.targets[0];
  Object.assign(bt, { error_code: 'SOCIAL_ACCOUNT_EXPIRED', error_message: 'LinkedIn authorization has expired', attempt_count: 2 });
  bad.attempts = [1, 2].map((n) => ({ id: randomUUID(), post_target_id: bt.id, attempt_no: n, status: 'failed', started_at: inPast(3 - n * 0.1), finished_at: inPast(3 - n * 0.1), error_code: 'SOCIAL_ACCOUNT_EXPIRED', error_message: 'LinkedIn authorization has expired' }));
  post('Half and half', 'partially_published', [tg, mock], 'Partially published example.', { published_at: inPast(8) });
  const m = (name, kind, hue) => db.media.push({ id: randomUUID(), user_id: u.id, kind, mime_type: kind === 'image' ? 'image/png' : 'video/mp4', size_bytes: 480_000, original_name: name, width: 400, height: 400, status: 'ready', url: kind === 'image' ? svgThumb(name, hue) : undefined, created_at: inPast(20) });
  m('launch-banner.png', 'image', 230); m('team.png', 'image', 20); m('demo.mp4', 'video', 0);
  db.keys.push({ id: randomUUID(), user_id: u.id, name: 'CI reader', prefix: 'sk_live_a1b2', scopes: ['posts:read', 'social:read'], expires_at: inFuture(24 * 60), revoked_at: null, last_used_at: inPast(2), created_at: inPast(100), dangerous_policy: 'approve' });
  db.mcp.push({ id: randomUUID(), user_id: u.id, name: 'Claude Desktop', client_name: 'claude-desktop 1.2', scopes: ['social:read', 'posts:read', 'posts:write'], last_seen_at: inPast(1), revoked_at: null, created_at: inPast(50) });
  const ask = (action, actor_label, minutesAgo, summary, status = 'pending') => db.approvals.push({
    id: randomUUID(), user_id: u.id, action, resource_type: action.startsWith('social_account') ? 'social_account' : 'post', resource_id: randomUUID(),
    actor_label, summary, status, created_at: inPast(minutesAgo / 60), expires_at: new Date(Date.now() - minutesAgo * 60_000 + 10 * 60_000).toISOString(), decided_at: status === 'pending' ? null : inPast(minutesAgo / 60 - 0.05),
  });
  ask('post.publish', 'MCP: Claude Desktop', 2, { title: 'Draft: case study', content: 'Case study draft - how a 3-person team halved their publishing time. '.repeat(9).trim(), platforms: ['linkedin', 'telegram'], targets: [{ platform: 'telegram', content: 'Case study: how a 3-person team halved their publishing time.' }], media: { count: 2, images: 2, videos: 0 }, status: 'draft' });
  ask('post.schedule_soon', 'MCP: Cursor', 4, { title: 'Weekly tip', content: 'Tip: schedule posts in your audience timezone.', platforms: ['telegram'], scheduled_at: inFuture(0.05) });
  ask('social_account.disconnect', 'CI publisher', 95, { provider: 'mock', username: 'mock-1' }, 'denied');
  audit('Demo User', 'post.create', 'post', pub.id);
  audit('CI reader', 'post.list', 'post', null, 'api_key');
}
seed();

// ---- http helpers
const send = (res, status, body, headers = {}) => {
  const data = body === undefined ? '' : JSON.stringify(body);
  res.writeHead(status, { 'Content-Type': 'application/json', ...headers });
  res.end(data);
};
const fail = (res, status, code, message, fields) => send(res, status, { error: { code, message, request_id: randomUUID().slice(0, 8), ...(fields ? { fields } : {}) } });
const cookies = (req) => Object.fromEntries((req.headers.cookie ?? '').split(';').map((c) => c.trim().split('=')).filter((p) => p[0]).map(([k, ...v]) => [k, decodeURIComponent(v.join('='))]));
const readBody = (req) => new Promise((resolve) => { const c = []; req.on('data', (d) => c.push(d)); req.on('end', () => resolve(Buffer.concat(c))); });
const sessionCookies = (s) => [`socialos_session=${s.token}; Path=/; HttpOnly; SameSite=Lax`, `socialos_csrf=${s.csrf}; Path=/; SameSite=Lax`];
const meBody = (u, s) => ({
  id: u.id, email: u.email, display_name: u.display_name, csrf_token: s.csrf, scopes: ALL_SCOPES,
  user: { id: u.id, email: u.email, display_name: u.display_name, email_verified: u.verified !== false, has_password: Boolean(u.password), login_methods: [...(u.password ? ['password'] : []), ...u.identities.map((i) => i.provider)], plan: 'free', deletion_scheduled_at: u.deletion_scheduled_at ?? null },
  deletion_grace_days: 7,
  verification_enforced: VERIFICATION === 'enforced', mail_delivery: VERIFICATION === 'enforced' ? 'smtp' : 'log',
});
const badLink = (res) => fail(res, 400, 'VALIDATION_ERROR', 'link is invalid or has expired');

function newSession(u) {
  const s = { token: randomBytes(24).toString('hex'), csrf: randomBytes(16).toString('hex'), userId: u.id };
  sessions.set(s.token, s);
  return s;
}

const publicPost = (p, full) => {
  const { user_id, media_ids, deleted, attempts, ...rest } = p;
  const base = { ...rest, content: p.content ?? p.targets[0]?.content };
  return full ? { ...base, media: db.media.filter((m) => media_ids.includes(m.id)), attempts } : base;
};

function paginate(items, url) {
  const limit = Math.min(Number(url.searchParams.get('limit') ?? 20), 200);
  const offset = Number(url.searchParams.get('cursor') ?? 0);
  const slice = items.slice(offset, offset + limit);
  return { items: slice, next_cursor: offset + limit < items.length ? String(offset + limit) : null };
}

function settle(post) {
  setTimeout(() => {
    for (const t of post.targets.filter((x) => x.status === 'publishing')) {
      const acc = db.accounts.find((a) => a.id === t.social_account_id);
      const bad = t.content.includes('FAIL') || acc?.status !== 'active';
      const n = t.attempt_count + 1;
      t.attempt_count = n;
      post.attempts.push({ id: randomUUID(), post_target_id: t.id, attempt_no: n, status: bad ? 'failed' : 'succeeded', started_at: now(), finished_at: now(), error_code: bad ? 'PROVIDER_ERROR' : null, error_message: bad ? 'Provider rejected the request (simulated)' : null });
      Object.assign(t, bad ? { status: 'failed', error_code: 'PROVIDER_ERROR', error_message: 'Provider rejected the request (simulated)' } : { status: 'published', published_at: now(), external_url: `https://example.com/p/${t.id.slice(0, 6)}`, error_code: null, error_message: null });
    }
    const st = post.targets.map((t) => t.status);
    post.status = st.every((s) => s === 'published') ? 'published' : st.some((s) => s === 'published') ? 'partially_published' : 'failed';
    if (post.status !== 'failed') post.published_at = now();
    post.updated_at = now();
  }, 900);
}

const AGENT_SCOPES = ['analytics:read', 'media:write', 'posts:read', 'posts:schedule', 'posts:write', 'social:connect', 'social:read'];
const agentSignups = new Map([
  ['agent-new', { email: 'new.owner@example.com', agent_name: 'Claude Desktop', key_prefix: 'sk_live_agnt', fixed: true }],
  ['agent-existing', { email: 'demo@example.com', agent_name: 'Claude Desktop', key_prefix: 'sk_live_agnt', fixed: true }],
]);

/** The agent's key starts working: an api_keys row with trusted_connect and an MCP connection the owner can revoke. */
function activateAgentKey(u, sg) {
  db.keys.push({ id: randomUUID(), user_id: u.id, name: sg.agent_name, prefix: sg.key_prefix, scopes: AGENT_SCOPES, expires_at: null, revoked_at: null, last_used_at: null, created_at: now(), dangerous_policy: 'trusted_connect' });
  db.mcp.push({ id: randomUUID(), user_id: u.id, name: sg.agent_name, client_name: sg.agent_name, scopes: AGENT_SCOPES, last_seen_at: null, revoked_at: null, created_at: now() });
  audit(u.display_name, 'agent_signup.confirmed', 'api_key', null);
}

function agentSignup(req, res, action, body) {
  if (!action) {
    const email = String(body.email ?? '').trim();
    if (!/^[^\s@]+@[^\s@]+$/.test(email)) return fail(res, 400, 'VALIDATION_ERROR', 'email is invalid', { email: 'invalid' });
    if (body.accept_terms !== true) return fail(res, 400, 'VALIDATION_ERROR', 'the owner must accept the Terms', { accept_terms: 'must be accepted' });
    const rawKey = `sk_live_${randomBytes(18).toString('hex')}`;
    const token = randomBytes(16).toString('hex');
    agentSignups.set(token, { email, agent_name: String(body.agent_name ?? 'Agent'), key_prefix: rawKey.slice(0, 12) });
    console.log(`agent sign-up for ${email}: http://localhost:3000/agent-access#token=${token}`);
    return send(res, 201, { api_key: rawKey, key_prefix: rawKey.slice(0, 12), status: 'pending_confirmation', scopes: AGENT_SCOPES, dangerous_policy: 'trusted_connect', email_masked: `${email[0]}***${email.slice(email.indexOf('@'))}`, expires_at: inFuture(24), delivery: 'log' });
  }
  const sg = agentSignups.get(String(body.token ?? ''));
  if (!sg) return badLink(res);
  const owner = users.get(sg.email);
  if (action === 'preview') return send(res, 200, { agent_name: sg.agent_name, email: sg.email, scopes: AGENT_SCOPES, dangerous_policy: 'trusted_connect', account_exists: Boolean(owner), expires_at: inFuture(24) });
  const spend = () => { if (!sg.fixed) agentSignups.delete(String(body.token)); };
  if (action === 'decline') { spend(); return send(res, 204); }
  const sess = sessions.get(cookies(req).socialos_session ?? '');
  if (sess && req.headers['x-csrf-token'] !== sess.csrf) return fail(res, 403, 'FORBIDDEN', 'Missing or invalid CSRF token');
  if (!owner) {
    if (sess) return fail(res, 409, 'CONFLICT', 'sign out before creating another account');
    if (body.accept_terms !== true) return fail(res, 400, 'VALIDATION_ERROR', 'You must accept the Terms and the Privacy Policy', { accept_terms: 'must be accepted' });
    const name = String(body.display_name ?? '').trim();
    if (name.length > 100) return fail(res, 400, 'VALIDATION_ERROR', 'display_name too long', { display_name: 'max 100 characters' });
    spend();
    const u = addUser(sg.email, null, name || sg.email);
    u.verified = true;
    activateAgentKey(u, sg);
    const s = newSession(u);
    return send(res, 200, { ...meBody(u, s), account_created: true }, { 'Set-Cookie': sessionCookies(s) });
  }
  if (!sess) return fail(res, 401, 'UNAUTHENTICATED', 'Authentication required');
  if (sess.userId !== owner.id) return fail(res, 403, 'FORBIDDEN', 'sign in as the account this link was sent to');
  spend();
  activateAgentKey(owner, sg);
  return send(res, 200, { ...meBody(owner, sess), account_created: false });
}

const TRANSITIONS = { draft: ['scheduled', 'publishing', 'cancelled'], scheduled: ['draft', 'publishing', 'cancelled'], failed: ['scheduled', 'publishing'], partially_published: ['scheduled', 'publishing'] };
const canMove = (p, to) => (TRANSITIONS[p.status] ?? []).includes(to);

async function handle(req, res) {
  const url = new URL(req.url, 'http://x');
  let r;
  const m = req.method ?? 'GET';
  const path = url.pathname.replace(/^\/api\/v1/, '') || '/';
  if (!url.pathname.startsWith('/api/v1')) return fail(res, 404, 'NOT_FOUND', 'Not found');
  const raw = await readBody(req);
  const ct = req.headers['content-type'] ?? '';
  let body = {};
  if (ct.includes('json') && raw.length) { try { body = JSON.parse(raw.toString()); } catch { return fail(res, 400, 'VALIDATION_ERROR', 'Invalid JSON'); } }

  if (path === '/health') return send(res, 200, { status: 'ok' });
  if (path === '/auth/register' && m === 'POST') {
    if (!body.email || !body.password || String(body.password).length < 8) return fail(res, 400, 'VALIDATION_ERROR', 'Email and a password of 8+ characters are required');
    if (body.accept_terms !== true) return fail(res, 400, 'VALIDATION_ERROR', 'You must accept the Terms and the Privacy Policy');
    if (users.has(body.email)) return fail(res, 409, 'CONFLICT', 'Email already registered');
    const u = addUser(body.email, body.password, body.display_name || body.email);
    const s = newSession(u);
    return send(res, 201, meBody(u, s), { 'Set-Cookie': sessionCookies(s) });
  }
  if (path === '/auth/login' && m === 'POST') {
    const u = users.get(body.email);
    if (!u || !u.password || u.password !== body.password) return fail(res, 401, 'UNAUTHENTICATED', 'Invalid email or password');
    const s = newSession(u);
    return send(res, 200, meBody(u, s), { 'Set-Cookie': sessionCookies(s) });
  }

  // Provider sign-in: no provider is visited; /start goes straight to the sign-up page for a new person.
  if (path === '/auth/providers' && m === 'GET') return send(res, 200, { providers: SIGN_IN_PROVIDERS });
  if ((r = path.match(/^\/auth\/oauth\/(\w+)\/start$/)) && m === 'GET') {
    if (!SIGN_IN_PROVIDERS.some((p) => p.id === r[1])) return fail(res, 404, 'NOT_FOUND', 'sign-in provider not found');
    const ticket = randomBytes(16).toString('hex');
    const next = url.searchParams.get('next') ?? '';
    pendingSignups.set(ticket, { provider: r[1], email: 'sam.rivera@example.com', display_name: 'Sam Rivera', next: safeNext(next) ?? '/dashboard' });
    res.writeHead(302, { Location: '/signup/complete', 'Set-Cookie': [`socialos_oauth_ticket=${ticket}; Path=/api/v1/auth/oauth; HttpOnly; SameSite=Lax`] });
    return res.end();
  }
  if (path === '/auth/oauth/pending' && m === 'GET') {
    const pending = pendingSignups.get(cookies(req).socialos_oauth_ticket ?? '');
    return pending ? send(res, 200, pending) : fail(res, 404, 'NOT_FOUND', 'no sign-up is waiting');
  }
  if (path === '/auth/oauth/complete' && m === 'POST') {
    const ticket = cookies(req).socialos_oauth_ticket ?? '';
    const pending = pendingSignups.get(ticket);
    if (!pending) return fail(res, 404, 'NOT_FOUND', 'no sign-up is waiting');
    if (body.accept_terms !== true) return fail(res, 400, 'VALIDATION_ERROR', 'You must accept the Terms and the Privacy Policy', { accept_terms: 'must be accepted' });
    pendingSignups.delete(ticket);
    const u = users.get(pending.email) ?? addUser(pending.email, null, String(body.display_name ?? '').trim());
    u.verified = true;
    if (!u.identities.some((i) => i.provider === pending.provider)) u.identities.push({ provider: pending.provider, email: pending.email, linked_at: new Date().toISOString(), last_login_at: new Date().toISOString() });
    const sess = newSession(u);
    return send(res, 201, meBody(u, sess), { 'Set-Cookie': [...sessionCookies(sess), 'socialos_oauth_ticket=; Path=/api/v1/auth/oauth; Max-Age=0'] });
  }

  // Mail-driven flows. No mail is sent: the token "valid-token" works, "expired" and anything else is a 400.
  if (path === '/auth/verify-email' && m === 'POST') {
    if (body.token !== 'valid-token') return badLink(res);
    const sessUser = sessions.get(cookies(req).socialos_session ?? '');
    if (sessUser) [...users.values()].filter((x) => x.id === sessUser.userId).forEach((x) => { x.verified = true; });
    return send(res, 200, { email_verified: true });
  }
  if (path === '/auth/password/forgot' && m === 'POST') return send(res, 202, { status: 'accepted', delivery: VERIFICATION === 'enforced' ? 'smtp' : 'log' });
  if (path === '/auth/password/reset' && m === 'POST') {
    if (body.token !== 'valid-token') return badLink(res);
    if (String(body.password ?? '').length < 8) return fail(res, 400, 'VALIDATION_ERROR', 'password must be 8-128 characters');
    return send(res, 204);
  }

  // Agent sign-up (D-025). Nothing is mailed: the created token is printed here. The fixed tokens "agent-new" (an address
  // without an account) and "agent-existing" (demo@example.com) always work; "expired" and unknown ones are a 400.
  if ((r = path.match(/^\/auth\/agent-signups(?:\/(preview|confirm|decline))?$/)) && m === 'POST') return agentSignup(req, res, r[1], body);

  // ---- authenticated
  const sess = sessions.get(cookies(req).socialos_session ?? '');
  const bearer = (req.headers.authorization ?? '').startsWith('Bearer sk_');
  const user = sess ? [...users.values()].find((x) => x.id === sess.userId) : bearer ? users.get('demo@example.com') : null;
  if (!user) return fail(res, 401, 'UNAUTHENTICATED', 'Authentication required');
  if (sess && m !== 'GET' && req.headers['x-csrf-token'] !== sess.csrf) return fail(res, 403, 'FORBIDDEN', 'Missing or invalid CSRF token');
  const mine = (arr) => arr.filter((x) => x.user_id === user.id);

  if (path === '/auth/logout' && m === 'POST') { sessions.delete(sess?.token); return send(res, 204, undefined, { 'Set-Cookie': ['socialos_session=; Path=/; Max-Age=0'] }); }
  if (path === '/me') return send(res, 200, meBody(user, sess ?? { csrf: '' }));
  if (path === '/auth/verify-email/resend' && m === 'POST') {
    return user.verified === false ? send(res, 202, { status: 'accepted', delivery: 'smtp' }) : fail(res, 409, 'CONFLICT', 'email is already verified');
  }
  if (path === '/account/delete' && m === 'POST') {
    if (user.deletion_scheduled_at) return fail(res, 409, 'CONFLICT', 'deletion of this account is already scheduled');
    // A password-less account confirms with the typed email alone; one with a password must send it.
    if (user.password && body.password !== user.password) return fail(res, 400, 'VALIDATION_ERROR', 'the password is incorrect', { password: 'incorrect' });
    if (String(body.confirm ?? '').trim().toLowerCase() !== user.email.toLowerCase()) return fail(res, 400, 'VALIDATION_ERROR', 'type your email address exactly to confirm', { confirm: 'does not match your email' });
    user.deletion_scheduled_at = new Date(Date.now() + 7 * 864e5).toISOString();
    sessions.delete(sess?.token);
    return send(res, 202, { status: 'scheduled', scheduled_for: user.deletion_scheduled_at }, { 'Set-Cookie': ['socialos_session=; Path=/; Max-Age=0'] });
  }
  if (path === '/account/delete/cancel' && m === 'POST') {
    if (!user.deletion_scheduled_at) return fail(res, 409, 'CONFLICT', 'no deletion is scheduled for this account');
    user.deletion_scheduled_at = null;
    return send(res, 204);
  }
  // Sign-in methods. Linking needs a provider to visit, which this mock does not have: it answers 404 and never pretends.
  if (path === '/auth/identities' && m === 'GET') return send(res, 200, { identities: user.identities, has_password: Boolean(user.password) });
  if ((r = path.match(/^\/auth\/identities\/(\w+)\/link$/)) && m === 'POST') return fail(res, 404, 'NOT_FOUND', 'sign-in method not available');
  if ((r = path.match(/^\/auth\/identities\/(\w+)$/)) && m === 'DELETE') {
    if (user.password && body.current_password !== user.password) return fail(res, 400, 'VALIDATION_ERROR', 'current password is incorrect', { current_password: 'incorrect' });
    if (!user.identities.some((i) => i.provider === r[1])) return fail(res, 404, 'NOT_FOUND', 'that sign-in method is not connected');
    if (!user.password && user.identities.length < 2) return fail(res, 409, 'CONFLICT', 'keep at least one way to sign in');
    user.identities = user.identities.filter((i) => i.provider !== r[1]);
    return send(res, 204);
  }
  if (path === '/auth/password/set' && m === 'POST') {
    if (user.password) return fail(res, 409, 'CONFLICT', 'you already have a password; change it instead');
    const next = String(body.new_password ?? '');
    if (next.length < 8 || next.length > 128) return fail(res, 400, 'VALIDATION_ERROR', 'password must be 8-128 characters', { new_password: 'must be 8-128 characters' });
    user.password = next;
    return send(res, 204);
  }
  if (path === '/auth/password/change' && m === 'POST') {
    if (!user.password || body.current_password !== user.password) return fail(res, 400, 'VALIDATION_ERROR', 'current password is incorrect');
    if (String(body.new_password ?? '').length < 8) return fail(res, 400, 'VALIDATION_ERROR', 'password must be 8-128 characters');
    user.password = body.new_password;
    return send(res, 204);
  }

  if (path === '/social/providers') return send(res, 200, { items: PROVIDERS });
  if (path === '/social/accounts' && m === 'GET') return send(res, 200, { items: mine(db.accounts).map(({ user_id, ...a }) => a), next_cursor: null });
  if (path === '/social/telegram/connect' && m === 'POST') {
    // The old direct connect (a chat name in the body) is gone: this only mints a code.
    const t = Date.now();
    const active = mine(db.links).filter((l) => !l.account_id && Date.parse(l.expires_at) > t).sort((a, b) => b.created_ms - a.created_ms);
    active.slice(MAX_ACTIVE_LINKS - 1).forEach((l) => { l.expires_at = new Date(t).toISOString(); }); // the oldest are retired
    const code = `SOS-${Array.from(randomBytes(8), (b) => LINK_ALPHABET[b % LINK_ALPHABET.length]).join('')}`;
    const link = { id: randomUUID(), user_id: user.id, code, created_ms: t, expires_at: new Date(t + LINK_TTL_S * 1000).toISOString(), account_id: null };
    db.links.push(link);
    if (LINK_DELAY_MS > 0 && LINK_DELAY_MS < LINK_TTL_S * 1000) {
      // Stand-in for "the user posted the code in their channel and the bot saw it".
      setTimeout(() => {
        if (Date.parse(link.expires_at) <= Date.now() || link.account_id) return;
        const n = mine(db.accounts).filter((a) => a.provider === 'telegram').length + 1;
        const a = { id: randomUUID(), user_id: user.id, provider: 'telegram', username: `demo_channel_${n}`, display_name: `Demo Channel ${n}`, avatar_url: null, status: 'active', scopes: ['post_messages'], connected_at: now() };
        db.accounts.push(a);
        link.account_id = a.id;
        audit('telegram', 'social_account.connected', 'social_account', a.id, 'system');
      }, LINK_DELAY_MS);
    }
    return send(res, 201, {
      id: link.id, code, expires_at: link.expires_at, bot_username: 'steerpost_bot',
      instructions: `Add @steerpost_bot as an administrator of your Telegram channel or group with the "Post messages" right. Post this code there as a normal message: ${code}. The code expires in ${Math.max(1, Math.round(LINK_TTL_S / 60))} minutes and works once.`,
    });
  }
  if ((r = path.match(/^\/social\/telegram\/connect\/([^/]+)$/)) && m === 'GET') {
    const link = mine(db.links).find((l) => l.id === r[1]); // another user's id is a 404, like the real API
    if (!link) return fail(res, 404, 'NOT_FOUND', 'link not found');
    if (link.account_id) {
      const a = db.accounts.find((x) => x.id === link.account_id);
      const { user_id, ...pub } = a ?? {};
      return send(res, 200, { status: 'connected', account: a ? pub : undefined });
    }
    return send(res, 200, { status: Date.parse(link.expires_at) > Date.now() ? 'pending' : 'expired' });
  }
  if (path === '/social/accounts/token' && m === 'POST') {
    // Same validation and error shapes as the real endpoint. "Verify" accepts anything except a secret containing "invalid".
    const p = PROVIDERS.find((x) => x.provider === body.provider);
    const spec = p?.capabilities.connect_fields;
    if (!spec) return fail(res, 400, 'VALIDATION_ERROR', 'provider cannot be connected with a token');
    const input = body.fields && typeof body.fields === 'object' ? body.fields : {};
    const bad = (message, fields) => send(res, 400, { error: { code: 'VALIDATION_ERROR', message, request_id: randomUUID().slice(0, 8), fields } });
    const values = {};
    for (const [name, raw] of Object.entries(input)) {
      const f = spec.find((x) => x.name === name);
      if (!f || typeof raw !== 'string') return bad('invalid fields', { fields: 'unknown field' });
      values[name] = raw.trim();
      if (values[name] && f.kind === 'url' && !/^https:\/\/[^/@\s]+/.test(values[name])) return bad(`${f.label} must be an https URL`, { [name]: 'must be an https URL' });
    }
    const missing = spec.find((f) => f.required && !values[f.name]);
    if (missing) return bad(`${missing.label} is required`, { [missing.name]: 'required' });
    const name = p.provider.charAt(0).toUpperCase() + p.provider.slice(1);
    if (spec.some((f) => (f.secret || f.kind === 'secret') && /invalid/i.test(values[f.name] ?? ''))) return fail(res, 400, 'VALIDATION_ERROR', `${name} rejected these credentials`);
    if (values.access_token === 'rate-limit') return fail(res, 429, 'RATE_LIMITED', 'Too many requests');
    const who = values.handle || values.instance_url?.replace(/^https:\/\//, '').replace(/\/.*$/, '') || 'webhook';
    const username = p.provider === 'mastodon' ? `demo@${who}` : p.provider === 'discord' ? '#general' : who;
    let a = mine(db.accounts).find((x) => x.provider === p.provider && x.username === username);
    if (a) a.status = 'active';
    else { a = { id: randomUUID(), user_id: user.id, provider: p.provider, username, display_name: `${name} ${username}`, avatar_url: null, status: 'active', scopes: [], connected_at: now() }; db.accounts.push(a); }
    const { user_id, ...pub } = a;
    return send(res, 201, pub);
  }
  if ((r = path.match(/^\/social\/(\w+)\/connect$/)) && m === 'GET') {
    const p = PROVIDERS.find((x) => x.provider === r[1]);
    if (!p || p.status === 'unsupported' || !p.configured) return fail(res, 501, 'PROVIDER_NOT_AVAILABLE', 'Provider not available');
    db.accounts.push({ id: randomUUID(), user_id: user.id, provider: p.provider, username: `${p.provider}-${db.accounts.length}`, display_name: `${p.provider} account ${db.accounts.length + 1}`, avatar_url: null, status: 'active', scopes: [], connected_at: now() });
    res.writeHead(302, { Location: `/accounts?connected=${p.provider}` }); return res.end();
  }
  if ((r = path.match(/^\/social\/accounts\/([^/]+)$/))) {
    const a = mine(db.accounts).find((x) => x.id === r[1]);
    if (!a) return fail(res, 404, 'NOT_FOUND', 'Account not found');
    if (m === 'DELETE') { db.accounts = db.accounts.filter((x) => x !== a); audit(user.display_name, 'social.disconnect', 'social_account', a.id); return send(res, 204); }
    const { user_id, ...pub } = a; return send(res, 200, pub);
  }

  // ---- posts
  if (path === '/posts' && m === 'POST') {
    const accs = (body.social_account_ids ?? []).map((id) => mine(db.accounts).find((a) => a.id === id));
    if (!body.content?.trim() && !body.targets?.length) return fail(res, 400, 'VALIDATION_ERROR', 'content is required');
    if (accs.length === 0 || accs.some((a) => !a)) return fail(res, 400, 'VALIDATION_ERROR', 'social_account_ids must reference your accounts');
    const p = { id: randomUUID(), user_id: user.id, title: body.title ?? null, content: body.content, status: 'draft', scheduled_at: null, published_at: null, created_by: 'user', created_at: now(), updated_at: now(), media_ids: body.media_ids ?? [], attempts: [] };
    p.targets = accs.map((a) => mkTarget(p.id, a, body.targets?.find((t) => t.social_account_id === a.id)?.content ?? body.content, 'pending'));
    if (body.scheduled_at && body.schedule) { p.status = 'scheduled'; p.scheduled_at = body.scheduled_at; }
    db.posts.push(p); audit(user.display_name, 'post.create', 'post', p.id);
    return send(res, 201, publicPost(p, true));
  }
  if (path === '/posts' && m === 'GET') {
    const st = url.searchParams.get('status'); const from = url.searchParams.get('from'); const to = url.searchParams.get('to');
    const t = (p) => p.scheduled_at ?? p.published_at ?? p.created_at;
    const list = mine(db.posts).filter((p) => (!st || p.status === st) && (!from || t(p) >= from) && (!to || t(p) < to)).sort((a, b) => t(b).localeCompare(t(a)));
    const pg = paginate(list, url); return send(res, 200, { ...pg, items: pg.items.map((p) => publicPost(p, false)) });
  }
  if ((r = path.match(/^\/posts\/([^/]+)(?:\/(publish|schedule|cancel|retry|status))?$/))) {
    const p = mine(db.posts).find((x) => x.id === r[1]);
    if (!p) return fail(res, 404, 'NOT_FOUND', 'Post not found');
    const act = r[2];
    if (!act && m === 'GET') return send(res, 200, publicPost(p, true));
    if (act === 'status') return send(res, 200, { id: p.id, status: p.status, targets: p.targets.map((t) => ({ id: t.id, status: t.status })) });
    if (!act && m === 'DELETE') { db.posts = db.posts.filter((x) => x !== p); audit(user.display_name, 'post.delete', 'post', p.id); return send(res, 204); }
    if (!act && m === 'PATCH') {
      if (p.status !== 'draft' && p.status !== 'scheduled') return fail(res, 409, 'INVALID_STATE_TRANSITION', "Only drafts and scheduled posts can be edited.");
      const content = typeof body.content === 'string' ? body.content : (p.content ?? p.targets[0]?.content ?? '');
      const ids = body.social_account_ids ?? p.targets.map((t) => t.social_account_id);
      const accs = ids.map((id) => mine(db.accounts).find((a) => a.id === id));
      if (!content.trim() || accs.length === 0 || accs.some((a) => !a)) return fail(res, 400, 'VALIDATION_ERROR', 'content and your social_account_ids are required');
      if (body.scheduled_at !== undefined) {
        if (p.status !== 'scheduled') return fail(res, 400, 'VALIDATION_ERROR', 'use POST /posts/{id}/schedule to schedule a draft');
        if (new Date(body.scheduled_at) <= new Date()) return fail(res, 400, 'VALIDATION_ERROR', 'scheduled_at must be in the future');
        p.scheduled_at = body.scheduled_at;
      }
      const old = p.targets;
      p.targets = accs.map((a) => { const t = mkTarget(p.id, a, body.targets?.find((x) => x.social_account_id === a.id)?.content || content, 'pending'); const o = old.find((x) => x.social_account_id === a.id); if (o) t.id = o.id; return t; });
      if ('title' in body) p.title = (body.title ?? '').trim() || null;
      if (body.media_ids) p.media_ids = body.media_ids;
      p.content = content; p.updated_at = now(); audit(user.display_name, 'post.update', 'post', p.id);
      return send(res, 200, publicPost(p, true));
    }
    if (m !== 'POST') return fail(res, 404, 'NOT_FOUND', 'Not found');
    const to = { publish: 'publishing', schedule: 'scheduled', cancel: 'cancelled', retry: 'publishing' }[act];
    if (!canMove(p, to)) return fail(res, 409, 'INVALID_STATE_TRANSITION', `Cannot go from ${p.status} to ${to}`);
    if (act === 'schedule') { if (!body.scheduled_at || new Date(body.scheduled_at) <= new Date()) return fail(res, 400, 'VALIDATION_ERROR', 'scheduled_at must be in the future'); p.scheduled_at = body.scheduled_at; }
    if (act === 'cancel') p.targets.forEach((t) => { t.status = 'cancelled'; });
    if (act === 'publish' || act === 'retry') { p.targets.filter((t) => t.status !== 'published').forEach((t) => { t.status = 'publishing'; }); settle(p); }
    p.status = to; p.updated_at = now(); audit(user.display_name, `post.${act}`, 'post', p.id);
    return send(res, 200, publicPost(p, true));
  }

  // ---- media
  if (path === '/media' && m === 'POST') {
    const text = raw.toString('latin1');
    const name = /filename="([^"]+)"/.exec(text)?.[1] ?? 'upload';
    const mime = /Content-Type: ([^\r\n]+)/.exec(text)?.[1] ?? 'application/octet-stream';
    if (!/^(image\/(jpeg|png|webp|gif)|video\/(mp4|quicktime))$/.test(mime)) return fail(res, 400, 'VALIDATION_ERROR', 'Unsupported media type');
    const kind = mime.startsWith('image') ? 'image' : 'video';
    const x = { id: randomUUID(), user_id: user.id, kind, mime_type: mime, size_bytes: raw.length, original_name: name, width: null, height: null, status: 'ready', url: kind === 'image' ? svgThumb(name, Math.floor(Math.random() * 360)) : undefined, created_at: now() };
    db.media.push(x); return send(res, 201, x);
  }
  if (path === '/media' && m === 'GET') return send(res, 200, { items: mine(db.media).reverse(), next_cursor: null });
  if ((r = path.match(/^\/media\/([^/]+)$/))) {
    const x = mine(db.media).find((y) => y.id === r[1]);
    if (!x) return fail(res, 404, 'NOT_FOUND', 'Media not found');
    if (m === 'DELETE') { db.media = db.media.filter((y) => y !== x); return send(res, 204); }
    return send(res, 200, x);
  }

  // ---- plan usage (same shape as GET /account/usage; the mock does not enforce the limits)
  if (path === '/account/usage' && m === 'GET') {
    const d = new Date();
    const start = new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth(), 1));
    return send(res, 200, {
      plan: 'free', period_start: start.toISOString(), period_end: new Date(Date.UTC(d.getUTCFullYear(), d.getUTCMonth() + 1, 1)).toISOString(),
      quotas: {
        connected_accounts: { used: mine(db.accounts).length, limit: 5 },
        scheduled_posts_month: { used: mine(db.posts).filter((p) => p.status !== 'draft' && p.status !== 'cancelled').length, limit: 60 },
        media_bytes: { used: mine(db.media).reduce((n, x) => n + (x.size_bytes ?? 0), 0), limit: 500 * 1024 * 1024 },
        agent_requests_per_minute: { limit: 120 },
      },
    });
  }

  // ---- data export (same states and rules as /account/exports; the archive is a small JSON "ready" after 4 s)
  if ((r = path.match(/^\/account\/exports(?:\/([^/]+))?$/))) {
    const list = mine(db.exports).map((e) => (e.status === 'pending' && Date.now() - Date.parse(e.created_at) > 4000 ? { ...e, status: 'ready', size_bytes: 2048, expires_at: new Date(Date.parse(e.created_at) + 7 * 864e5).toISOString() } : e)).sort((a, b) => b.created_at.localeCompare(a.created_at));
    if (!r[1] && m === 'GET') return send(res, 200, { items: list });
    if (!r[1] && m === 'POST') {
      if (list[0]?.status === 'pending') return fail(res, 409, 'CONFLICT', 'an export is already being prepared');
      if (list[0]?.status === 'ready') return fail(res, 429, 'RATE_LIMITED', 'you exported your data less than 24 hours ago; download that export or try again later');
      const e = { id: randomUUID(), user_id: user.id, status: 'pending', size_bytes: 0, error_code: null, created_at: now(), expires_at: null };
      db.exports.push(e); audit(user.display_name, 'account.export_requested', 'data_export', e.id);
      return send(res, 202, { ...e, user_id: undefined });
    }
    const e = list.find((x) => x.id === r[1]);
    if (!e) return fail(res, 404, 'NOT_FOUND', 'export not found');
    if (e.status !== 'ready') return fail(res, 409, 'CONFLICT', 'this export is not available for download');
    return send(res, 200, { ...e, user_id: undefined, url: 'data:application/json,%7B%22note%22%3A%22mock%20export%22%7D', url_expires_at: new Date(Date.now() + 3e5).toISOString() });
  }

  // ---- dashboard / analytics / audit
  if (path === '/dashboard/summary') {
    const ps = mine(db.posts); const month = now().slice(0, 7);
    const by = (s) => ps.filter((p) => p.status === s);
    return send(res, 200, {
      connected_accounts: mine(db.accounts).length, scheduled_posts: by('scheduled').length, drafts: by('draft').length,
      published_this_month: by('published').filter((p) => p.published_at?.startsWith(month)).length, failed: by('failed').length,
      upcoming: by('scheduled').sort((a, b) => a.scheduled_at.localeCompare(b.scheduled_at)).slice(0, 5).map((p) => publicPost(p, false)),
      recent: by('published').slice(0, 5).map((p) => publicPost(p, false)),
    });
  }
  if (path === '/analytics') {
    const acc = mine(db.accounts).find((a) => a.provider === 'mock');
    const items = acc ? Array.from({ length: 14 }, (_, i) => ({ metric: 'impressions', value: 100 + ((i * 37) % 90) + i * 5, captured_at: inPast((14 - i) * 24), social_account_id: acc.id })) : [];
    return send(res, 200, { items });
  }
  if (path === '/audit-logs') {
    const action = url.searchParams.get('action');
    return send(res, 200, paginate(action ? db.audit.filter((a) => a.action === action) : db.audit, url));
  }

  // ---- approvals (session only, tenant scoped, like the real API)
  if (path === '/approvals' && m === 'GET') {
    const all = url.searchParams.get('status') === 'all';
    const open = (a) => a.status === 'pending' && Date.parse(a.expires_at) > Date.now();
    const list = mine(db.approvals).filter((a) => all || open(a)).map(({ user_id, ...a }) => (a.status === 'pending' && !open(a) ? { ...a, status: 'expired' } : a));
    return send(res, 200, paginate(list.sort((a, b) => b.created_at.localeCompare(a.created_at)), url));
  }
  if ((r = path.match(/^\/approvals\/([^/]+)(?:\/(approve|deny))?$/))) {
    const a = mine(db.approvals).find((x) => x.id === r[1]);
    if (!a) return fail(res, 404, 'NOT_FOUND', 'approval not found');
    if (!r[2] && m === 'GET') { const { user_id, ...rest } = a; return send(res, 200, rest); }
    if (r[2] && m === 'POST') {
      if (a.status !== 'pending' || Date.parse(a.expires_at) <= Date.now()) return fail(res, 409, 'CONFLICT', 'this approval is no longer pending');
      Object.assign(a, { status: r[2] === 'approve' ? 'approved' : 'denied', decided_at: now() });
      audit(user.display_name, `approval.${r[2] === 'approve' ? 'approved' : 'denied'}`, 'approval', a.id);
      const { user_id, ...rest } = a;
      return send(res, 200, rest);
    }
  }

  // ---- developer
  if (path === '/developer/api-keys' && m === 'GET') return send(res, 200, { items: mine(db.keys).map(({ user_id, ...k }) => k) });
  if (path === '/developer/api-keys' && m === 'POST') {
    if (!body.name || !Array.isArray(body.scopes) || body.scopes.some((s) => !ALL_SCOPES.includes(s))) return fail(res, 400, 'VALIDATION_ERROR', 'name and valid scopes are required');
    if (body.dangerous_policy !== undefined && !['approve', 'trusted'].includes(body.dangerous_policy)) return fail(res, 400, 'VALIDATION_ERROR', 'dangerous_policy must be approve or trusted');
    const rawKey = `sk_live_${randomBytes(18).toString('hex')}`;
    const k = { id: randomUUID(), user_id: user.id, name: body.name, prefix: rawKey.slice(0, 12), scopes: body.scopes, expires_at: body.expires_at ?? null, revoked_at: null, last_used_at: null, created_at: now(), dangerous_policy: body.dangerous_policy === 'trusted' ? 'trusted' : 'approve' };
    db.keys.push(k); audit(user.display_name, 'apikey.create', 'api_key', k.id);
    const { user_id, ...pub } = k; return send(res, 201, { key: pub, raw_key: rawKey });
  }
  if ((r = path.match(/^\/developer\/api-keys\/([^/]+)$/)) && m === 'DELETE') {
    const k = mine(db.keys).find((x) => x.id === r[1]); if (!k) return fail(res, 404, 'NOT_FOUND', 'Key not found');
    k.revoked_at = now(); audit(user.display_name, 'apikey.revoke', 'api_key', k.id); return send(res, 204);
  }
  if (path === '/developer/mcp-connections' && m === 'GET') return send(res, 200, { items: mine(db.mcp).map(({ user_id, ...c }) => c) });
  if (path === '/developer/mcp-connections' && m === 'POST') {
    if (!body.name || !Array.isArray(body.scopes) || !body.scopes.length) return fail(res, 400, 'VALIDATION_ERROR', 'name and scopes are required');
    const rawKey = `sk_live_${randomBytes(18).toString('hex')}`;
    const c = { id: randomUUID(), user_id: user.id, name: body.name, client_name: null, scopes: body.scopes, last_seen_at: null, revoked_at: null, created_at: now() };
    db.mcp.push(c); audit(user.display_name, 'mcp.create', 'mcp_connection', c.id);
    const { user_id, ...pub } = c; return send(res, 201, { connection: pub, raw_key: rawKey });
  }
  if ((r = path.match(/^\/developer\/mcp-connections\/([^/]+)$/)) && m === 'DELETE') {
    const c = mine(db.mcp).find((x) => x.id === r[1]); if (!c) return fail(res, 404, 'NOT_FOUND', 'Connection not found');
    c.revoked_at = now(); audit(user.display_name, 'mcp.revoke', 'mcp_connection', c.id); return send(res, 204);
  }
  if (path === '/developer/usage') {
    return send(res, 200, { total_requests: 128, by_key: [{ name: 'CI reader', requests: 96, last_used_at: inPast(2) }, { name: 'Claude Desktop', requests: 32, last_used_at: inPast(1) }], by_day: Array.from({ length: 7 }, (_, i) => ({ day: inPast((6 - i) * 24).slice(0, 10), requests: 8 + ((i * 11) % 23) })) });
  }
  return fail(res, 404, 'NOT_FOUND', 'Not found');
}

createServer((req, res) => {
  handle(req, res).catch((e) => { console.error(e); fail(res, 500, 'INTERNAL', 'Internal error'); });
}).listen(PORT, () => console.log(`mock Steerpost API on http://localhost:${PORT}  (demo@example.com / demo12345)`));
