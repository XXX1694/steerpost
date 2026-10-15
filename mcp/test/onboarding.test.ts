import { afterAll, afterEach, beforeAll, beforeEach, describe, expect, it, vi } from "vitest";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StdioClientTransport } from "@modelcontextprotocol/sdk/client/stdio.js";
import { parseTrustedProxies } from "../src/client-ip.js";
import { connect, connectAnonymous, FakeApi, startStack, text, VALID_KEY, type Stack } from "./helpers.js";

const PENDING_KEY = "sk_live_pending123";
const PENDING = {
  email_masked: "o***@example.com",
  expires_at: "2026-10-16T09:00:00Z",
  scopes: "posts:read posts:write social:connect social:read",
};
const SIGNUP_ANSWER = {
  api_key: "sk_live_newkey999",
  key_prefix: "sk_live_newk",
  status: "pending_confirmation",
  scopes: ["posts:read", "social:connect"],
  dangerous_policy: "trusted_connect",
  email_masked: "o***@example.com",
  expires_at: "2026-10-16T09:00:00Z",
  delivery: "smtp",
};
const SIGNUP_ARGS = { email: "owner@example.com", agent_name: "Claude Desktop", accept_terms: true };

let s: Stack;
beforeAll(async () => { s = await startStack(); });
afterAll(async () => { await s.stop(); });
beforeEach(() => {
  s.api.calls.length = 0;
  s.api.meCalls.length = 0;
  s.api.replies.clear();
  s.api.pending.set(PENDING_KEY, PENDING);
});

describe("anonymous caller", () => {
  it("sees exactly sign_up, labelled public", async () => {
    const c = await connectAnonymous(s.mcpUrl);
    const { tools } = await c.listTools();
    expect(tools.map((t) => t.name)).toEqual(["sign_up"]);
    expect(tools[0]!.description).toMatch(/^\[risk: medium; scope: public\] /);
    expect(c.getInstructions()).toContain("sign_up");
    await c.close();
  });

  it("sign_up posts to the public route without a key and adds the next step", async () => {
    s.api.replies.set("POST /auth/agent-signups", { status: 201, body: SIGNUP_ANSWER });
    const c = await connectAnonymous(s.mcpUrl);
    const r = await c.callTool({ name: "sign_up", arguments: SIGNUP_ARGS });
    expect(r.isError).toBeFalsy();
    const call = s.api.calls[0]!;
    expect(call.path).toBe("/auth/agent-signups");
    expect(call.body).toEqual(SIGNUP_ARGS);
    expect(call.headers.authorization).toBeUndefined();
    expect(call.headers["x-mcp-tool"]).toBe("sign_up");
    const out = JSON.parse(text(r)) as { api_key: string; next: string };
    expect(out.api_key).toBe(SIGNUP_ANSWER.api_key);
    expect(out.next).toContain("Authorization: Bearer");
    expect(out.next).toContain("STEERPOST_API_KEY");
    expect(out.next).toContain("o***@example.com");
    expect(out.next).not.toContain("server log");
    await c.close();
  });

  it("sign_up says where the link went when the server only logs mail", async () => {
    s.api.replies.set("POST /auth/agent-signups", { status: 201, body: { ...SIGNUP_ANSWER, delivery: "log" } });
    const c = await connectAnonymous(s.mcpUrl);
    const out = JSON.parse(text(await c.callTool({ name: "sign_up", arguments: SIGNUP_ARGS }))) as { next: string };
    expect(out.next).toContain("server log");
    await c.close();
  });

  it("sign_up refuses accept_terms=false and a bad email before calling the API", async () => {
    const c = await connectAnonymous(s.mcpUrl);
    for (const args of [{ ...SIGNUP_ARGS, accept_terms: false }, { ...SIGNUP_ARGS, email: "nope" }, { ...SIGNUP_ARGS, agent_name: "" }]) {
      const r = await c.callTool({ name: "sign_up", arguments: args }).catch((e: unknown) => e);
      expect(r instanceof Error || (r as { isError?: boolean }).isError === true).toBe(true);
    }
    expect(s.api.calls).toHaveLength(0);
    await c.close();
  });

  it("cannot call keyed tools", async () => {
    const c = await connectAnonymous(s.mcpUrl);
    const r = await c.callTool({ name: "list_posts", arguments: {} }).catch((e: unknown) => e);
    expect(r instanceof Error || (r as { isError?: boolean }).isError === true).toBe(true);
    expect(s.api.calls).toHaveLength(0);
    await c.close();
  });
});

describe("keyed callers", () => {
  it("a working key never sees sign_up", async () => {
    const c = await connect(s.mcpUrl);
    const names = (await c.listTools()).tools.map((t) => t.name);
    expect(names).not.toContain("sign_up");
    expect(names).toContain("connect_account");
    await c.close();
  });

  it("a pending key lists the tools of its future scopes, and a call explains the wait", async () => {
    const c = await connect(s.mcpUrl, PENDING_KEY);
    const names = (await c.listTools()).tools.map((t) => t.name).sort();
    expect(names).toEqual([
      "cancel_scheduled_post", "connect_account", "create_draft", "get_post", "get_post_status", "get_social_account",
      "list_platforms", "list_posts", "list_social_accounts", "update_post",
    ]);
    expect(c.getInstructions()).toContain("KEY_PENDING_CONFIRMATION");
    const r = await c.callTool({ name: "list_posts", arguments: {} });
    expect(r.isError).toBe(true);
    const msg = text(r);
    expect(msg).toContain("KEY_PENDING_CONFIRMATION");
    expect(msg).toContain("o***@example.com");
    expect(msg).toContain("2026-10-16T09:00:00Z");
    expect(msg).toContain("Do not call sign_up again");
    await c.close();
  });
});

describe("connect_account secrecy", () => {
  const SECRET = "https://discord.com/api/webhooks/123/very-secret-token";
  let spy: ReturnType<typeof vi.spyOn>;
  beforeEach(() => { spy = vi.spyOn(process.stderr, "write"); });
  afterEach(() => { spy.mockRestore(); });

  it("never puts field values in the error text or the log, even if the API echoed one", async () => {
    s.api.replies.set("POST /social/accounts/token", {
      status: 400,
      body: { error: { code: "VALIDATION_ERROR", message: `webhook ${SECRET} was rejected`, request_id: "r-1" } },
    });
    const c = await connect(s.mcpUrl);
    const r = await c.callTool({ name: "connect_account", arguments: { provider: "discord", fields: { webhook_url: SECRET } } });
    expect(r.isError).toBe(true);
    expect(text(r)).toContain("VALIDATION_ERROR");
    expect(text(r)).not.toContain(SECRET);
    const logged = spy.mock.calls.map((args: unknown[]) => String(args[0])).join("");
    expect(logged).not.toContain(SECRET);
    expect(s.api.calls[0]!.body).toEqual({ provider: "discord", fields: { webhook_url: SECRET } });
    await c.close();
  });

  it("rejects a field value over 2048 characters before calling the API", async () => {
    const c = await connect(s.mcpUrl);
    const r = await c
      .callTool({ name: "connect_account", arguments: { provider: "discord", fields: { webhook_url: "x".repeat(2049) } } })
      .catch((e: unknown) => e);
    expect(r instanceof Error || (r as { isError?: boolean }).isError === true).toBe(true);
    expect(s.api.calls).toHaveLength(0);
    await c.close();
  });
});

describe("gateway headers on anonymous calls", () => {
  const GATEWAY = "gw-secret-0123456789abcdef0123456789abcdef";
  let g: Stack;
  beforeAll(async () => { g = await startStack({ gatewaySecret: GATEWAY, trustedProxies: parseTrustedProxies(false, "") }); });
  afterAll(async () => { await g.stop(); });

  it("forwards the secret and the client IP so the API can rate-limit sign-ups per address", async () => {
    g.api.replies.set("POST /auth/agent-signups", { status: 201, body: SIGNUP_ANSWER });
    const c = await connectAnonymous(g.mcpUrl);
    await c.callTool({ name: "sign_up", arguments: SIGNUP_ARGS });
    const sent = g.api.calls.at(-1)!;
    expect(sent.headers["x-socialos-gateway"]).toBe(GATEWAY);
    expect(sent.headers["x-socialos-client-ip"]).toBe("127.0.0.1");
    expect(sent.headers.authorization).toBeUndefined();
    await c.close();
  });
});

describe("stdio", () => {
  const api = new FakeApi();
  beforeAll(() => api.start());
  afterAll(() => api.stop());

  it("starts in sign-up mode without a key", async () => {
    const transport = new StdioClientTransport({
      command: process.execPath,
      args: ["--import", "tsx", "src/index.ts", "--stdio"],
      env: { PATH: process.env.PATH ?? "", STEERPOST_API_URL: api.url },
      stderr: "ignore",
    });
    const client = new Client({ name: "t", version: "0" });
    await client.connect(transport);
    expect((await client.listTools()).tools.map((t) => t.name)).toEqual(["sign_up"]);
    expect(api.meCalls).toHaveLength(0);
    await client.close();
  });

  it("starts in pending mode with an unconfirmed key", async () => {
    api.pending.set(PENDING_KEY, PENDING);
    api.keys.delete(VALID_KEY);
    const transport = new StdioClientTransport({
      command: process.execPath,
      args: ["--import", "tsx", "src/index.ts", "--stdio"],
      env: { PATH: process.env.PATH ?? "", STEERPOST_API_URL: api.url, STEERPOST_API_KEY: PENDING_KEY },
      stderr: "ignore",
    });
    const client = new Client({ name: "t", version: "0" });
    await client.connect(transport);
    expect(client.getInstructions()).toContain("KEY_PENDING_CONFIRMATION");
    expect((await client.listTools()).tools.map((t) => t.name)).toContain("list_posts");
    await client.close();
  });
});
