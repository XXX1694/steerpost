import { randomUUID } from "node:crypto";
import { createServer, type IncomingMessage, type Server, type ServerResponse } from "node:http";
import type { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import { StreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/streamableHttp.js";
import { ApiError, SocialOSClient } from "./api-client.js";
import { clientIp } from "./client-ip.js";
import type { Config } from "./config.js";
import { registerSecret } from "./errors.js";
import { log } from "./log.js";
import { buildServer, resolveAccess } from "./server.js";

const MAX_BODY = 4 * 1024 * 1024;

function sendJson(res: ServerResponse, status: number, body: unknown, headers: Record<string, string> = {}): void {
  res.writeHead(status, { "Content-Type": "application/json", ...headers });
  res.end(JSON.stringify(body));
}

function rpcError(res: ServerResponse, status: number, code: number, message: string, headers: Record<string, string> = {}): void {
  sendJson(res, status, { jsonrpc: "2.0", error: { code, message }, id: null }, headers);
}

/** Extracts the API key from `Authorization: Bearer sk_live_…`; undefined when absent/malformed. */
export function bearerKey(req: IncomingMessage): string | undefined {
  const m = /^Bearer\s+(sk_live_[A-Za-z0-9_\-]+)$/.exec(req.headers.authorization ?? "");
  return m?.[1];
}

async function readJson(req: IncomingMessage): Promise<unknown> {
  const chunks: Buffer[] = [];
  let size = 0;
  for await (const chunk of req) {
    size += (chunk as Buffer).length;
    if (size > MAX_BODY) throw new Error("body too large");
    chunks.push(chunk as Buffer);
  }
  return JSON.parse(Buffer.concat(chunks).toString("utf8"));
}

export function createHttpServer(config: Config): Server {
  registerSecret(config.gatewaySecret);
  return createServer((req, res) => {
    handle(req, res, config).catch((err: unknown) => {
      log("error", "unhandled request error", { error: err instanceof Error ? err.name : "unknown" });
      if (!res.headersSent) rpcError(res, 500, -32603, "Internal server error");
      else res.end();
    });
  });
}

async function handle(req: IncomingMessage, res: ServerResponse, config: Config): Promise<void> {
  const path = new URL(req.url ?? "/", "http://localhost").pathname;

  if (path === "/health") {
    sendJson(res, 200, { status: "ok", service: "socialos-mcp" });
    return;
  }
  if (path !== "/mcp") {
    sendJson(res, 404, { error: "not found" });
    return;
  }
  if (req.method !== "POST") {
    // Stateless mode: no server-initiated SSE stream and no sessions to delete.
    rpcError(res, 405, -32000, "Method not allowed (stateless server: use POST /mcp)", { Allow: "POST" });
    return;
  }

  const requestId = String(req.headers["x-request-id"] ?? "").slice(0, 100) || randomUUID();
  const challenge = { "WWW-Authenticate": 'Bearer realm="socialos-mcp"', "X-Request-Id": requestId };

  // No Authorization header at all is an anonymous agent that may only sign up; a header we cannot parse is a mistake.
  const anonymous = req.headers.authorization === undefined;
  const apiKey = bearerKey(req);
  if (!anonymous && !apiKey) {
    rpcError(res, 401, -32001, "Malformed Authorization header; expected 'Bearer sk_live_…'", challenge);
    return;
  }

  let body: unknown;
  try {
    body = await readJson(req);
  } catch {
    rpcError(res, 400, -32700, "Parse error: body must be valid JSON (max 4 MB)", { "X-Request-Id": requestId });
    return;
  }

  const client = new SocialOSClient({
    baseUrl: config.apiUrl,
    apiKey,
    timeoutMs: config.timeoutMs,
    requestId,
    gatewaySecret: config.gatewaySecret,
    clientIp: config.gatewaySecret && config.trustedProxies ? clientIp(req, config.trustedProxies) : undefined,
  });
  const server = anonymous ? buildServer(client, [], "anonymous") : await authedServer(client, res, challenge, requestId);
  if (!server) return;

  const transport = new StreamableHTTPServerTransport({ sessionIdGenerator: undefined, enableJsonResponse: true });
  res.setHeader("X-Request-Id", requestId);
  res.on("close", () => {
    void transport.close();
    void server.close();
  });
  await server.connect(transport);
  await transport.handleRequest(req, res, body);
}

/** Builds the server for a keyed caller from GET /me, or answers the HTTP error itself and returns undefined. */
async function authedServer(
  client: SocialOSClient,
  res: ServerResponse,
  challenge: Record<string, string>,
  requestId: string,
): Promise<McpServer | undefined> {
  try {
    const access = await resolveAccess(client);
    return buildServer(client, access.scopes, access.mode);
  } catch (err) {
    if (err instanceof ApiError && (err.status === 401 || err.status === 403)) {
      rpcError(res, 401, -32001, "Invalid, expired or revoked API key", challenge);
    } else {
      const status = err instanceof ApiError && err.status === 504 ? 504 : 502;
      rpcError(res, status, -32002, "Steerpost API unavailable", { "X-Request-Id": requestId });
    }
    return undefined;
  }
}
