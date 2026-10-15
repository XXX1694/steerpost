export type Scope =
  | 'social:read'
  | 'posts:read'
  | 'posts:write'
  | 'posts:schedule'
  | 'posts:publish'
  | 'posts:delete'
  | 'social:disconnect'
  | 'social:connect'
  | 'media:write'
  | 'analytics:read';

export type PostStatus =
  | 'draft'
  | 'scheduled'
  | 'publishing'
  | 'published'
  | 'partially_published'
  | 'failed'
  | 'cancelled';

export type TargetStatus =
  | 'pending'
  | 'publishing'
  | 'published'
  | 'failed'
  | 'cancelled'
  | 'needs_review';

export type AccountStatus = 'active' | 'expired' | 'revoked' | 'error';
export type AttemptStatus = 'started' | 'succeeded' | 'failed' | 'unknown';

export interface Page<T> {
  items: T[];
  next_cursor: string | null;
}

export interface Me {
  id: string;
  email: string;
  display_name: string;
  csrf_token: string;
  scopes?: string[];
  /** False until the mailed link is opened. Servers that predate verification report true. */
  email_verified: boolean;
  /** True when unverified owners are blocked from connecting, scheduling, publishing and creating keys. */
  verification_enforced: boolean;
  /** "log" means mail is only written to the server log, so nobody receives it. */
  mail_delivery: 'log' | 'smtp';
  /** When the account will be deleted for good; null unless the owner asked and has not cancelled. */
  deletion_scheduled_at: string | null;
  /** Days between asking for deletion and the data going. */
  deletion_grace_days: number;
  /** False for accounts created with Google or GitHub that never set a password. Servers that predate D-023 report true. */
  has_password: boolean;
  /** `"password"` and/or the linked provider ids. Empty when the server does not say. */
  login_methods: string[];
}

/** A sign-in provider the server has switched on (`GET /auth/providers`). */
export interface SignInProvider {
  id: string;
  name: string;
}

/** A provider account linked to the signed-in user (`GET /auth/identities`; the provider's subject id is never sent). */
export interface Identity {
  provider: string;
  email: string;
  linked_at: string;
  last_login_at: string | null;
}

export interface SignInMethodList { identities: Identity[]; has_password: boolean }

/** What `/signup/complete` will create after the provider sign-in (`GET /auth/oauth/pending`). */
export interface PendingSignup {
  provider: string;
  email: string;
  display_name: string;
  /** The in-app path to go to once the account exists; already checked by the server. */
  next: string | null;
}

export interface Capabilities {
  canPublishText: boolean;
  canPublishImage: boolean;
  canPublishVideo: boolean;
  canSchedule: boolean;
  canDelete: boolean;
  canAnalytics: boolean;
  maxTextLength: number;
  maxMediaCount: number;
  requiresApproval: boolean;
  notes: string;
  /** How an account is connected: "oauth" (a redirect), "telegram_chat", "token" (a pasted credential) or "none". */
  connectMethod: string;
  /** The form of a `token` provider, rendered as is. Empty for every other method. */
  connectFields: ConnectField[];
}

/** One input of a "Connect with a token" form (`connect_fields` of GET /social/providers). */
export interface ConnectField {
  name: string;
  label: string;
  help: string;
  placeholder: string;
  kind: 'text' | 'secret' | 'url';
  required: boolean;
  /** A credential whatever its kind: shown as a password input and never echoed or stored. */
  secret: boolean;
}

export interface Provider {
  id: string;
  name: string;
  configured: boolean;
  unsupported: boolean;
  /** True when users can actually connect and publish. */
  available: boolean;
  capabilities: Capabilities;
}

export interface SocialAccount {
  id: string;
  provider: string;
  username: string;
  display_name: string;
  avatar_url: string | null;
  status: AccountStatus;
  scopes: string[];
  connected_at: string;
}

/** Answer of POST /social/telegram/connect: a one-time code the user posts in their chat. */
export interface TelegramLink {
  id: string;
  /** Shown once; the server keeps only its hash. */
  code: string;
  expires_at: string;
  bot_username: string;
  instructions: string;
}

export type TelegramLinkStatus = 'pending' | 'connected' | 'expired';

/** Answer of GET /social/telegram/connect/{id}. */
export interface TelegramLinkState {
  status: TelegramLinkStatus;
  account: SocialAccount | null;
}

export interface Media {
  id: string;
  kind: 'image' | 'video';
  mime_type: string;
  size_bytes: number;
  original_name: string;
  width: number | null;
  height: number | null;
  status: string;
  url?: string;
  created_at: string;
}

export interface PostTarget {
  id: string;
  social_account_id: string;
  platform: string;
  content: string;
  status: TargetStatus;
  external_url: string | null;
  published_at: string | null;
  error_code: string | null;
  error_message: string | null;
  attempt_count: number;
}

export interface PublicationAttempt {
  id: string;
  post_target_id: string;
  attempt_no: number;
  status: AttemptStatus;
  started_at: string;
  finished_at: string | null;
  error_code: string | null;
  error_message: string | null;
}

export interface Post {
  id: string;
  title: string | null;
  content?: string;
  status: PostStatus;
  scheduled_at: string | null;
  published_at: string | null;
  created_by: 'user' | 'api_key';
  created_by_ref?: string | null;
  created_at: string;
  updated_at?: string;
  targets: PostTarget[];
  media?: Media[];
  attempts?: PublicationAttempt[];
}

export interface CreatePostInput {
  title?: string;
  content: string;
  social_account_ids: string[];
  media_ids?: string[];
  targets?: { social_account_id: string; content: string }[];
  scheduled_at?: string;
  schedule?: boolean;
}

/** Body of `PATCH /posts/{id}`: every field is optional and only the ones present are changed. */
export interface UpdatePostInput {
  title?: string;
  content?: string;
  social_account_ids?: string[];
  media_ids?: string[];
  targets?: { social_account_id: string; content: string }[];
  /** Scheduled posts only; a draft is scheduled through `POST /posts/{id}/schedule`. */
  scheduled_at?: string;
}

/** One line of `GET /account/usage`. `used` is absent for limits that are not counted (the agent request rate). `limit` -1 = unlimited. */
export interface QuotaLine {
  used?: number;
  limit: number;
}

export interface UsageReport {
  plan: string;
  period_start: string;
  period_end: string;
  quotas: {
    connected_accounts: QuotaLine;
    scheduled_posts_month: QuotaLine;
    media_bytes: QuotaLine;
    agent_requests_per_minute: QuotaLine;
  };
}

export interface DashboardSummary {
  connected_accounts: number;
  scheduled_posts: number;
  drafts: number;
  published_this_month: number;
  failed: number;
  upcoming: Post[];
  recent: Post[];
}

export interface AnalyticsPoint {
  metric: string;
  value: number;
  captured_at: string;
  social_account_id?: string;
}

export interface AnalyticsResult {
  items: AnalyticsPoint[];
}

export interface ApiKey {
  id: string;
  name: string;
  prefix: string;
  scopes: string[];
  expires_at: string | null;
  revoked_at: string | null;
  last_used_at: string | null;
  created_at: string;
  /**
   * `approve`: publish, delete, disconnect and near-term schedules wait for the owner (default). `trusted`: they do not.
   * `trusted_connect`: only connecting a network skips the wait; given to keys an agent signed up for (D-025).
   */
  dangerous_policy?: 'approve' | 'trusted' | 'trusted_connect';
}

export interface CreatedApiKey {
  key: ApiKey;
  rawKey: string;
}

export interface McpConnection {
  id: string;
  name: string;
  client_name: string | null;
  scopes: string[];
  last_seen_at: string | null;
  revoked_at: string | null;
  created_at: string;
}

export interface McpConfigSnippets {
  http: string;
  stdio: string;
}

export interface CreatedMcpConnection {
  connection: McpConnection;
  rawKey: string;
  config: McpConfigSnippets;
}

export interface UsageSummary {
  total_requests: number;
  by_key: { name: string; requests: number; last_used_at: string | null }[];
  by_day: { day: string; requests: number }[];
}

export interface AuditLog {
  id: string;
  actor_type: 'user' | 'api_key' | 'scheduler' | 'system';
  actor_label: string;
  action: string;
  resource_type: string;
  resource_id: string | null;
  request_id: string | null;
  ip: string | null;
  created_at: string;
  /** Allow-listed details; for `mcp.tool_call`: tool, route, status, error_code, client. */
  metadata?: Record<string, unknown>;
}

/** What a dangerous action an agent attempted would do; `post.schedule_soon` is a schedule under the minimum lead. */
export type ApprovalAction =
  | 'post.publish'
  | 'post.retry_now'
  | 'post.delete'
  | 'post.schedule_soon'
  | 'social_account.disconnect'
  | 'social_account.connect_token';

export type ApprovalStatus = 'pending' | 'approved' | 'denied' | 'consumed' | 'expired';

/** An API key's request that waits for (or got) the owner's decision. `summary` never holds secrets. */
export interface Approval {
  id: string;
  action: ApprovalAction;
  resource_type: string;
  resource_id: string;
  actor_label: string;
  summary: Record<string, unknown>;
  status: ApprovalStatus;
  expires_at: string;
  decided_at: string | null;
  created_at: string;
}

export type DataExportStatus = 'pending' | 'running' | 'ready' | 'failed' | 'expired';

/** One account data export (`/account/exports`). `expires_at` is when a ready ZIP is deleted. */
export interface DataExport {
  id: string;
  status: DataExportStatus;
  size_bytes: number;
  error_code: string | null;
  created_at: string;
  expires_at: string | null;
}

/** `GET /account/exports/{id}` for a ready export: a download URL that works for `url_expires_at` only. */
export interface DataExportLink extends DataExport {
  url: string;
  url_expires_at: string;
}
