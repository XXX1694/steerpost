import { afterAll, beforeAll, beforeEach, describe, expect, it } from "vitest";
import { VALID_KEY, connect, startStack, text, type Stack } from "./helpers.js";

let s: Stack;
beforeAll(async () => { s = await startStack(); });
afterAll(async () => { await s.stop(); });
beforeEach(() => { s.api.calls.length = 0; s.api.replies.clear(); });

const EXPECTED_TOOLS = [
  "list_platforms", "connect_account",
  "list_social_accounts", "get_social_account", "list_posts", "get_post", "get_post_status", "get_analytics", "get_usage",
  "create_draft", "update_post", "schedule_post", "cancel_scheduled_post", "publish_post", "delete_post", "disconnect_account",
];

describe("tool listing", () => {
  it("lists all 16 keyed tools (never sign_up) with annotations and risk labels for a full-scope key", async () => {
    const c = await connect(s.mcpUrl);
    const { tools } = await c.listTools();
    expect(tools.map((t) => t.name).sort()).toEqual([...EXPECTED_TOOLS].sort());
    for (const t of tools) {
      expect(t.annotations).toBeDefined();
      expect(t.description).toMatch(/^\[risk: (safe|low|medium|sensitive|critical); scope: /);
    }
    const byName = Object.fromEntries(tools.map((t) => [t.name, t]));
    expect(byName.list_posts!.annotations!.readOnlyHint).toBe(true);
    expect(byName.publish_post!.annotations!.destructiveHint).toBe(true);
    // The risk label comes from the automatic prefix only; the text does not repeat it in capitals.
    expect(byName.publish_post!.description).toMatch(/^\[risk: sensitive; /);
    expect(byName.disconnect_account!.description).toMatch(/^\[risk: critical; /);
    for (const t of tools) expect(t.description).not.toMatch(/\b(SENSITIVE|CRITICAL|WILL)\b/);
    expect(byName.disconnect_account!.description).not.toContain("OAuth");
    expect(byName.get_post_status!.description).toContain("it may post twice");
    expect(byName.publish_post!.description).toContain("Unless the key is trusted");
    expect(byName.disconnect_account!.description).toContain("fail unless the user connects it again before they are due");
    await c.close();
  });

  it("filters tools by the key's scopes", async () => {
    s.api.keys.set("sk_live_readonly", ["posts:read", "social:read"]);
    const c = await connect(s.mcpUrl, "sk_live_readonly");
    const names = (await c.listTools()).tools.map((t) => t.name).sort();
    expect(names).toEqual(["get_post", "get_post_status", "get_social_account", "list_platforms", "list_posts", "list_social_accounts"]);
    await c.close();
  });

  it("exposes dangerous tools only when their scope is granted", async () => {
    s.api.keys.set("sk_live_draftonly", ["posts:write", "posts:schedule"]);
    const c = await connect(s.mcpUrl, "sk_live_draftonly");
    const names = (await c.listTools()).tools.map((t) => t.name).sort();
    expect(names).toEqual(["cancel_scheduled_post", "create_draft", "schedule_post", "update_post"]);
    await c.close();
  });

  it("rejects calls to tools that are not granted", async () => {
    s.api.keys.set("sk_live_ro", ["posts:read"]);
    const c = await connect(s.mcpUrl, "sk_live_ro");
    const r = await c.callTool({ name: "publish_post", arguments: { post_id: "p1" } }).catch((e: unknown) => e);
    const failed = r instanceof Error || (r as { isError?: boolean }).isError === true;
    expect(failed).toBe(true);
    expect(s.api.calls).toHaveLength(0);
    await c.close();
  });
});

describe("tool -> REST mapping", () => {
  const cases: { name: string; args: Record<string, unknown>; method: string; path: string; body?: unknown }[] = [
    { name: "list_platforms", args: {}, method: "GET", path: "/social/providers" },
    {
      name: "connect_account", args: { provider: "mocktoken", fields: { api_key: "mt_abc" } },
      method: "POST", path: "/social/accounts/token", body: { provider: "mocktoken", fields: { api_key: "mt_abc" } },
    },
    { name: "list_social_accounts", args: {}, method: "GET", path: "/social/accounts" },
    { name: "get_social_account", args: { account_id: "a1" }, method: "GET", path: "/social/accounts/a1" },
    { name: "list_posts", args: { status: "draft", limit: 5, cursor: "c1" }, method: "GET", path: "/posts?status=draft&limit=5&cursor=c1" },
    { name: "get_post", args: { post_id: "p1" }, method: "GET", path: "/posts/p1" },
    { name: "get_post_status", args: { post_id: "p1" }, method: "GET", path: "/posts/p1/status" },
    { name: "get_usage", args: {}, method: "GET", path: "/account/usage" },
    { name: "get_analytics", args: { from: "2026-01-01T00:00:00Z" }, method: "GET", path: "/analytics?from=2026-01-01T00%3A00%3A00Z" },
    {
      name: "create_draft",
      args: { content: "hello", social_account_ids: ["a1", "a2"], title: "T", per_platform_content: { a2: "short" } },
      method: "POST", path: "/posts",
      body: { title: "T", content: "hello", social_account_ids: ["a1", "a2"], targets: [{ social_account_id: "a2", content: "short" }] },
    },
    { name: "update_post", args: { post_id: "p1", content: "new" }, method: "PATCH", path: "/posts/p1", body: { content: "new" } },
    {
      name: "schedule_post",
      args: { post_id: "p1", scheduled_at: "2026-12-01T09:00:00Z" },
      method: "POST", path: "/posts/p1/schedule", body: { scheduled_at: "2026-12-01T09:00:00Z" },
    },
    { name: "cancel_scheduled_post", args: { post_id: "p1" }, method: "POST", path: "/posts/p1/cancel" },
    { name: "publish_post", args: { post_id: "p1" }, method: "POST", path: "/posts/p1/publish" },
    { name: "delete_post", args: { post_id: "p1" }, method: "DELETE", path: "/posts/p1" },
    { name: "disconnect_account", args: { account_id: "a1" }, method: "DELETE", path: "/social/accounts/a1" },
  ];

  it("covers every tool", () => {
    expect(cases.map((c) => c.name).sort()).toEqual([...EXPECTED_TOOLS].sort());
  });

  it.each(cases)("$name -> $method $path", async ({ name, args, method, path, body }) => {
    const c = await connect(s.mcpUrl);
    const r = await c.callTool({ name, arguments: args });
    expect(r.isError).toBeFalsy();
    expect(s.api.calls).toHaveLength(1);
    const call = s.api.calls[0]!;
    expect(call.method).toBe(method);
    expect(call.path).toBe(path);
    expect(call.body).toEqual(body);
    expect(call.headers.authorization).toBe(`Bearer ${VALID_KEY}`);
    expect(call.headers["x-request-id"]).toBeTruthy();
    await c.close();
  });

  it("encodes path segments", async () => {
    const c = await connect(s.mcpUrl);
    await c.callTool({ name: "get_post", arguments: { post_id: "a/b?c" } });
    expect(s.api.calls[0]!.path).toBe("/posts/a%2Fb%3Fc");
    await c.close();
  });

  it("propagates the inbound X-Request-Id to the backend", async () => {
    const res = await fetch(s.mcpUrl, {
      method: "POST",
      headers: { Authorization: `Bearer ${VALID_KEY}`, "Content-Type": "application/json", Accept: "application/json, text/event-stream", "X-Request-Id": "req-abc" },
      body: JSON.stringify({ jsonrpc: "2.0", id: 1, method: "tools/call", params: { name: "list_posts", arguments: {} } }),
    });
    expect(res.status).toBe(200);
    expect(res.headers.get("x-request-id")).toBe("req-abc");
    expect(s.api.calls[0]!.headers["x-request-id"]).toBe("req-abc");
  });

  it("rejects update_post with no fields and per_platform_content with unknown ids", async () => {
    const c = await connect(s.mcpUrl);
    const a = await c.callTool({ name: "update_post", arguments: { post_id: "p1" } });
    expect(a.isError).toBe(true);
    const b = await c.callTool({ name: "create_draft", arguments: { content: "x", social_account_ids: ["a1"], per_platform_content: { zz: "y" } } });
    expect(b.isError).toBe(true);
    expect(text(b)).toContain("zz");
    expect(s.api.calls).toHaveLength(0);
    await c.close();
  });
});

describe("approvals", () => {
  const APPROVAL = "3f1c7e2a-9b1d-4c52-8d8e-5a0f1b2c3d4e";
  const approvalError = {
    status: 428,
    body: {
      error: {
        code: "APPROVAL_REQUIRED", message: "this action needs the owner's approval", request_id: "rid-428",
        fields: { approval_id: APPROVAL, approve_url: "https://app.example.test/approvals", expires_at: "2026-10-09T12:10:00Z", action: "post.publish" },
      },
    },
  };
  const dangerous = [
    ["publish_post", { post_id: "p1" }, "POST /posts/p1/publish"],
    ["delete_post", { post_id: "p1" }, "DELETE /posts/p1"],
    ["disconnect_account", { account_id: "a1" }, "DELETE /social/accounts/a1"],
    ["schedule_post", { post_id: "p1", scheduled_at: "2026-10-09T12:01:00Z" }, "POST /posts/p1/schedule"],
    ["update_post", { post_id: "p1", scheduled_at: "2026-10-09T12:01:00Z" }, "PATCH /posts/p1"],
  ] as const;

  it.each(dangerous)("%s turns a 428 into a hint with the approval id and where to approve", async (name, args, route) => {
    s.api.replies.set(route, approvalError);
    const c = await connect(s.mcpUrl);
    const r = await c.callTool({ name, arguments: args });
    expect(r.isError).toBe(true);
    const t = text(r);
    expect(t).toContain("APPROVAL_REQUIRED");
    expect(t).toContain(APPROVAL);
    expect(t).toContain("ask the owner to approve at https://app.example.test/approvals");
    expect(t).toContain("NOT performed");
    expect(t).toContain("rid-428");
    await c.close();
  });

  it.each(dangerous)("%s sends approval_id as the X-Approval-Id header and never in the body", async (name, args) => {
    const c = await connect(s.mcpUrl);
    const r = await c.callTool({ name, arguments: { ...args, approval_id: APPROVAL } });
    expect(r.isError).toBeFalsy();
    const call = s.api.calls[0]!;
    expect(call.headers["x-approval-id"]).toBe(APPROVAL);
    expect(JSON.stringify(call.body ?? {})).not.toContain(APPROVAL);
    await c.close();
  });

  it("sends no approval header on the first attempt", async () => {
    const c = await connect(s.mcpUrl);
    await c.callTool({ name: "publish_post", arguments: { post_id: "p1" } });
    expect(s.api.calls[0]!.headers["x-approval-id"]).toBeUndefined();
    await c.close();
  });

  it("the old confirm flag bypasses nothing: it is not forwarded and the API still decides", async () => {
    s.api.replies.set("POST /posts/p1/publish", approvalError);
    const c = await connect(s.mcpUrl);
    const r = await c.callTool({ name: "publish_post", arguments: { post_id: "p1", confirm: true } });
    expect(r.isError).toBe(true);
    expect(text(r)).toContain("APPROVAL_REQUIRED");
    expect(JSON.stringify(s.api.calls[0]!.body ?? {})).not.toContain("confirm");
    await c.close();
  });

  it("rejects a malformed approval_id before calling the API", async () => {
    const c = await connect(s.mcpUrl);
    const r = await c.callTool({ name: "publish_post", arguments: { post_id: "p1", approval_id: "not-a-uuid" } });
    expect(r.isError).toBe(true);
    expect(s.api.calls).toHaveLength(0);
    await c.close();
  });

  it("tells the agent about the flow in the server instructions and tool descriptions", async () => {
    const c = await connect(s.mcpUrl);
    expect(c.getInstructions()).toContain("approval_id");
    expect(c.getInstructions()).toContain("unless the key is trusted");
    const publish = (await c.listTools()).tools.find((t) => t.name === "publish_post")!;
    expect(publish.description).toContain("APPROVAL_REQUIRED");
    expect(JSON.stringify(publish.inputSchema)).not.toContain("confirm");
    expect(c.getInstructions()).toContain("show the user the final text, accounts and time");
    await c.close();
  });
});

describe("error mapping", () => {
  const errs: [number, string, string][] = [
    [403, "INSUFFICIENT_SCOPE", "scope"],
    [403, "INSUFFICIENT_SCOPE", "Keys cannot gain scopes"],
    [403, "QUOTA_EXCEEDED", "Do not retry in a loop"],
    [403, "EMAIL_NOT_VERIFIED", "verification link"],
    [403, "QUOTA_EXCEEDED", "get_usage"],
    [422, "SOCIAL_ACCOUNT_EXPIRED", "reconnect"],
    [409, "INVALID_STATE_TRANSITION", "get_post_status"],
    [404, "NOT_FOUND", "does not exist"],
    [429, "RATE_LIMITED", "wait"],
  ];
  it.each(errs)("maps %i %s to a clear tool error", async (status, code, hint) => {
    s.api.replies.set("POST /posts/p1/publish", { status, body: { error: { code, message: "backend says no", request_id: "rid-9" } } });
    const c = await connect(s.mcpUrl);
    const r = await c.callTool({ name: "publish_post", arguments: { post_id: "p1" } });
    expect(r.isError).toBe(true);
    const t = text(r);
    expect(t).toContain(code);
    expect(t).toContain("backend says no");
    expect(t).toContain("rid-9");
    expect(t.toLowerCase()).toContain(hint.toLowerCase());
    await c.close();
  });

  it("never leaks keys or bearer tokens from upstream messages", async () => {
    s.api.replies.set("GET /posts/p1", {
      status: 400,
      body: { error: { code: "VALIDATION_ERROR", message: `bad token sk_live_SECRET99 and Bearer abc.def-ghi`, request_id: "r" } },
    });
    const c = await connect(s.mcpUrl);
    const t = text(await c.callTool({ name: "get_post", arguments: { post_id: "p1" } }));
    expect(t).not.toContain("SECRET99");
    expect(t).not.toContain("abc.def-ghi");
    expect(t).toContain("[redacted]");
    await c.close();
  });

  it("maps non-JSON upstream failures to an INTERNAL tool error", async () => {
    s.api.replies.set("GET /posts/p1", { status: 500, body: undefined });
    const c = await connect(s.mcpUrl);
    const r = await c.callTool({ name: "get_post", arguments: { post_id: "p1" } });
    expect(r.isError).toBe(true);
    expect(text(r)).toContain("INTERNAL");
    await c.close();
  });
});

describe("HTTP edge", () => {
  const rpc = { jsonrpc: "2.0", id: 1, method: "tools/list" };
  const post = (headers: Record<string, string>) =>
    fetch(s.mcpUrl, { method: "POST", headers: { "Content-Type": "application/json", Accept: "application/json, text/event-stream", ...headers }, body: JSON.stringify(rpc) });

  it("GET /health is open", async () => {
    const res = await fetch(`${s.baseUrl}/health`);
    expect(res.status).toBe(200);
    expect(await res.json()).toMatchObject({ status: "ok" });
  });

  it("missing bearer -> anonymous server that offers only sign_up, without asking the API", async () => {
    s.api.meCalls.length = 0;
    const res = await post({});
    expect(res.status).toBe(200);
    const body = (await res.json()) as { result: { tools: { name: string }[] } };
    expect(body.result.tools.map((t) => t.name)).toEqual(["sign_up"]);
    expect(s.api.meCalls).toHaveLength(0);
  });

  it.each(["", "Basic abc", "Bearer", "Bearer not_a_key"])("malformed auth %j -> 401", async (h) => {
    expect((await post({ Authorization: h })).status).toBe(401);
  });

  it("key rejected by the backend -> 401", async () => {
    const res = await post({ Authorization: "Bearer sk_live_unknown" });
    expect(res.status).toBe(401);
  });

  it("GET /mcp -> 405 (stateless)", async () => {
    const res = await fetch(s.mcpUrl, { headers: { Authorization: `Bearer ${VALID_KEY}`, Accept: "text/event-stream" } });
    expect(res.status).toBe(405);
  });

  it("invalid JSON -> 400", async () => {
    const res = await fetch(s.mcpUrl, { method: "POST", headers: { Authorization: `Bearer ${VALID_KEY}`, "Content-Type": "application/json" }, body: "{nope" });
    expect(res.status).toBe(400);
  });

  it("unknown route -> 404; backend down -> 502", async () => {
    expect((await fetch(`${s.baseUrl}/nope`)).status).toBe(404);
    const { createHttpServer } = await import("../src/http.js");
    const srv = createHttpServer({ apiUrl: "http://127.0.0.1:1/api/v1", port: 0, host: "127.0.0.1", timeoutMs: 500 });
    await new Promise<void>((r) => srv.listen(0, "127.0.0.1", r));
    const port = (srv.address() as { port: number }).port;
    const res = await fetch(`http://127.0.0.1:${port}/mcp`, {
      method: "POST",
      headers: { "Content-Type": "application/json", Accept: "application/json, text/event-stream", Authorization: `Bearer ${VALID_KEY}` },
      body: JSON.stringify(rpc),
    });
    expect(res.status).toBe(502);
    await new Promise<void>((r) => srv.close(() => r()));
  });

  it("serves independent stateless requests concurrently", async () => {
    const clients = await Promise.all([connect(s.mcpUrl), connect(s.mcpUrl), connect(s.mcpUrl)]);
    const lists = await Promise.all(clients.map((c) => c.listTools()));
    for (const l of lists) expect(l.tools).toHaveLength(EXPECTED_TOOLS.length);
    await Promise.all(clients.map((c) => c.close()));
  });
});
