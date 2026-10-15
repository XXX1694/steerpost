import { randomUUID } from "node:crypto";

export class ApiError extends Error {
  constructor(
    public readonly status: number,
    public readonly code: string,
    message: string,
    public readonly requestId?: string,
    /** Extra detail the API attaches to an error (for APPROVAL_REQUIRED: approval_id, approve_url, expires_at, action). */
    public readonly fields: Readonly<Record<string, string>> = {},
  ) {
    super(message);
    this.name = "ApiError";
  }
}

export interface Me {
  scopes: string[];
  raw: unknown;
}

export interface RequestOptions {
  query?: Record<string, string | number | boolean | undefined>;
  body?: unknown;
  /** An approval the owner granted for exactly this call; sent as X-Approval-Id. */
  approvalId?: string;
}

export interface ClientOptions {
  baseUrl: string;
  /** The caller's key; omitted for anonymous calls (sign_up), which then carry no Authorization header. */
  apiKey?: string;
  timeoutMs: number;
  /** Propagated as X-Request-Id; generated when omitted. */
  requestId?: string;
  /** MCP_GATEWAY_SECRET; sent as X-SocialOS-Gateway so the API believes {@link clientIp}. Never set in stdio mode. */
  gatewaySecret?: string;
  /** The end client's address as this server saw it (HTTP mode only), sent as X-SocialOS-Client-IP. */
  clientIp?: string;
  /** Name of the MCP tool this client acts for, sent as X-MCP-Tool for the backend's audit log. */
  tool?: string;
  fetchImpl?: typeof fetch;
}

const TOOL_NAME = /^[a-z_]{1,64}$/;

/** Typed client for the Steerpost REST API, authenticated with the caller's API key when there is one. */
export class SocialOSClient {
  readonly requestId: string;
  private readonly fetchImpl: typeof fetch;

  constructor(private readonly opts: ClientOptions) {
    this.requestId = opts.requestId ?? randomUUID();
    this.fetchImpl = opts.fetchImpl ?? fetch;
  }

  /** A client for one tool call: every request it makes carries X-MCP-Tool: <name>. */
  withTool(name: string): SocialOSClient {
    return new SocialOSClient({ ...this.opts, requestId: this.requestId, fetchImpl: this.fetchImpl, tool: name });
  }

  async request<T = unknown>(method: string, path: string, o: RequestOptions = {}): Promise<T> {
    const url = new URL(this.opts.baseUrl + path);
    for (const [k, v] of Object.entries(o.query ?? {})) {
      if (v !== undefined) url.searchParams.set(k, String(v));
    }
    const headers: Record<string, string> = {
      Accept: "application/json",
      "X-Request-Id": this.requestId,
      "User-Agent": "socialos-mcp/0.1",
    };
    if (this.opts.apiKey) headers.Authorization = `Bearer ${this.opts.apiKey}`;
    if (this.opts.tool && TOOL_NAME.test(this.opts.tool)) headers["X-MCP-Tool"] = this.opts.tool;
    if (o.approvalId) headers["X-Approval-Id"] = o.approvalId;
    if (this.opts.gatewaySecret) {
      headers["X-SocialOS-Gateway"] = this.opts.gatewaySecret;
      if (this.opts.clientIp) headers["X-SocialOS-Client-IP"] = this.opts.clientIp;
    }
    let body: string | undefined;
    if (o.body !== undefined) {
      headers["Content-Type"] = "application/json";
      body = JSON.stringify(o.body);
    }

    let res: Response;
    try {
      res = await this.fetchImpl(url, { method, headers, body, signal: AbortSignal.timeout(this.opts.timeoutMs) });
    } catch (err) {
      const timedOut = err instanceof Error && (err.name === "TimeoutError" || err.name === "AbortError");
      throw new ApiError(
        timedOut ? 504 : 503,
        timedOut ? "UPSTREAM_TIMEOUT" : "UPSTREAM_UNAVAILABLE",
        timedOut ? "The Steerpost API timed out" : "The Steerpost API is unreachable",
        this.requestId,
      );
    }

    const text = await res.text();
    let json: unknown = undefined;
    if (text) {
      try {
        json = JSON.parse(text);
      } catch {
        json = undefined;
      }
    }
    if (!res.ok) {
      const e = (json as { error?: { code?: string; message?: string; request_id?: string; fields?: unknown } } | undefined)?.error;
      throw new ApiError(
        res.status,
        e?.code ?? defaultCode(res.status),
        e?.message ?? `Steerpost API returned HTTP ${res.status}`,
        e?.request_id ?? res.headers.get("x-request-id") ?? this.requestId,
        stringFields(e?.fields),
      );
    }
    return (json ?? { ok: true }) as T;
  }

  /** GET /me → the key's scopes. Accepts `scopes` at top level or nested under api_key / user. */
  async me(): Promise<Me> {
    const raw = await this.request<Record<string, unknown>>("GET", "/me");
    const nested = (k: string): unknown => (raw[k] as Record<string, unknown> | undefined)?.scopes;
    const candidate = raw.scopes ?? nested("api_key") ?? nested("key") ?? nested("user") ?? nested("auth");
    const scopes = Array.isArray(candidate) ? candidate.filter((s): s is string => typeof s === "string") : [];
    return { scopes, raw };
  }
}

function stringFields(v: unknown): Record<string, string> {
  const out: Record<string, string> = {};
  if (v && typeof v === "object") {
    for (const [k, val] of Object.entries(v)) if (typeof val === "string") out[k] = val;
  }
  return out;
}

function defaultCode(status: number): string {
  switch (status) {
    case 400: return "VALIDATION_ERROR";
    case 401: return "UNAUTHENTICATED";
    case 403: return "FORBIDDEN";
    case 404: return "NOT_FOUND";
    case 409: return "CONFLICT";
    case 428: return "APPROVAL_REQUIRED";
    case 429: return "RATE_LIMITED";
    default: return status >= 500 ? "INTERNAL" : "ERROR";
  }
}
