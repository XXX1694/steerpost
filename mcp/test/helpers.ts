import { createServer, type Server } from "node:http";
import type { AddressInfo } from "node:net";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";
import type { Config } from "../src/config.js";
import { createHttpServer } from "../src/http.js";

export interface Recorded {
  method: string;
  path: string; // includes query
  headers: Record<string, string | string[] | undefined>;
  body: unknown;
}

export interface FakeReply {
  status?: number;
  body?: unknown;
}

export const ALL_SCOPES = [
  "social:read", "posts:read", "posts:write", "posts:schedule",
  "posts:publish", "posts:delete", "social:disconnect", "social:connect", "media:write", "analytics:read",
];

/** What the API says about a key from sign_up that the owner has not confirmed (fields of KEY_PENDING_CONFIRMATION). */
export interface PendingKey {
  email_masked: string;
  expires_at: string;
  scopes: string;
}

/** Routes the fake API answers without a key, like the real public sign-up route. */
const PUBLIC_ROUTES = new Set(["POST /auth/agent-signups"]);

export const VALID_KEY = "sk_live_testkey123";

/** A fake Steerpost REST API. Keys map to scopes; unknown keys get 401. */
export class FakeApi {
  server!: Server;
  url = "";
  calls: Recorded[] = [];
  /** Requests to GET /me (the per-request key check), kept apart from tool calls. */
  meCalls: Recorded[] = [];
  keys = new Map<string, string[]>([[VALID_KEY, ALL_SCOPES]]);
  /** Keys that answer 403 KEY_PENDING_CONFIRMATION on every route, /me included. */
  pending = new Map<string, PendingKey>();
  /** Override reply for "METHOD /path" (path without query and without /api/v1). */
  replies = new Map<string, FakeReply>();

  async start(): Promise<void> {
    this.server = createServer((req, res) => {
      const chunks: Buffer[] = [];
      req.on("data", (c: Buffer) => chunks.push(c));
      req.on("end", () => {
        const raw = Buffer.concat(chunks).toString();
        const rec: Recorded = { method: req.method ?? "", path: req.url ?? "", headers: req.headers, body: raw ? JSON.parse(raw) : undefined };
        const send = (status: number, body?: unknown) => {
          res.writeHead(status, { "Content-Type": "application/json" });
          res.end(body === undefined ? "" : JSON.stringify(body));
        };
        const key = /^Bearer (.+)$/.exec(req.headers.authorization ?? "")?.[1];
        const u = new URL(rec.path, "http://x");
        const route = u.pathname.replace(/^\/api\/v1/, "");
        const waiting = key ? this.pending.get(key) : undefined;
        if (waiting) {
          (route === "/me" ? this.meCalls : this.calls).push({ ...rec, path: route });
          return send(403, { error: { code: "KEY_PENDING_CONFIRMATION", message: "this API key is waiting for the owner's confirmation", request_id: "r-403", fields: waiting } });
        }
        const scopes = key ? this.keys.get(key) : undefined;
        const open = !key && PUBLIC_ROUTES.has(`${rec.method} ${route}`);
        if (!scopes && !open) return send(401, { error: { code: "UNAUTHENTICATED", message: "bad key", request_id: "r-401" } });
        if (route === "/me") {
          this.meCalls.push({ ...rec, path: route });
          return send(200, { id: "u1", email: "a@b.c", scopes });
        }
        this.calls.push({ ...rec, path: route + u.search });
        const o = this.replies.get(`${rec.method} ${route}`);
        if (o) return send(o.status ?? 200, o.body);
        if (rec.method === "DELETE") return send(204);
        send(200, { ok: true, route });
      });
    });
    await new Promise<void>((r) => this.server.listen(0, "127.0.0.1", r));
    this.url = `http://127.0.0.1:${(this.server.address() as AddressInfo).port}`;
  }

  async stop(): Promise<void> {
    await new Promise<void>((r) => this.server.close(() => r()));
  }
}

export interface Stack {
  api: FakeApi;
  mcpUrl: string;
  baseUrl: string;
  stop: () => Promise<void>;
}

export async function startStack(cfg: Partial<Config> = {}): Promise<Stack> {
  const api = new FakeApi();
  await api.start();
  const mcp = createHttpServer({ apiUrl: `${api.url}/api/v1`, port: 0, host: "127.0.0.1", timeoutMs: 3000, ...cfg });
  await new Promise<void>((r) => mcp.listen(0, "127.0.0.1", r));
  const baseUrl = `http://127.0.0.1:${(mcp.address() as AddressInfo).port}`;
  return {
    api,
    baseUrl,
    mcpUrl: `${baseUrl}/mcp`,
    stop: async () => {
      await new Promise<void>((r) => mcp.close(() => r()));
      await api.stop();
    },
  };
}

export async function connect(mcpUrl: string, key = VALID_KEY): Promise<Client> {
  const client = new Client({ name: "test", version: "0" });
  await client.connect(
    new StreamableHTTPClientTransport(new URL(mcpUrl), { requestInit: { headers: { Authorization: `Bearer ${key}` } } }),
  );
  return client;
}

/** An MCP client that sends no Authorization header (an agent that has not signed up yet). */
export async function connectAnonymous(mcpUrl: string): Promise<Client> {
  const client = new Client({ name: "test", version: "0" });
  await client.connect(new StreamableHTTPClientTransport(new URL(mcpUrl)));
  return client;
}

export function text(result: unknown): string {
  const r = result as { content: { type: string; text: string }[] };
  return r.content.map((c) => c.text).join("\n");
}
