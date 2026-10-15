import { z } from "zod";
import { defineTool, seg } from "./types.js";

const id = (what: string) => z.string().min(1).max(128).describe(what);
const readOnly = { readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: false } as const;

export const readTools = [
  defineTool({
    name: "list_social_accounts",
    title: "List social accounts",
    scope: "social:read",
    risk: "safe",
    description:
      "List the social accounts connected to Steerpost (id, provider, username, status). Use the returned ids as social_account_ids when creating posts. Accounts with status 'expired' cannot publish until the user reconnects them. Networks that connect with a pasted credential can be connected with connect_account (needs the social:connect scope; list_platforms shows how each network connects); OAuth and Telegram accounts are connected or reconnected by the user in the Steerpost web app. An account's metadata may carry its own limits (for example max_characters), and some networks need a post title; the REST API rejects posts that break them.",
    inputSchema: {},
    annotations: { title: "List social accounts", ...readOnly },
    handler: (c) => c.request("GET", "/social/accounts"),
  }),
  defineTool({
    name: "get_social_account",
    title: "Get social account",
    scope: "social:read",
    risk: "safe",
    description: "Get one connected social account by id, including its status and granted provider scopes.",
    inputSchema: { account_id: id("Social account id") },
    annotations: { title: "Get social account", ...readOnly },
    handler: (c, a) => c.request("GET", `/social/accounts/${seg(a.account_id)}`),
  }),
  defineTool({
    name: "list_posts",
    title: "List posts",
    scope: "posts:read",
    risk: "safe",
    description:
      "List posts, newest first, optionally filtered by status and a time window. Paginated: pass the returned next_cursor as cursor to get the next page.",
    inputSchema: {
      status: z
        .enum(["draft", "scheduled", "publishing", "published", "partially_published", "failed", "cancelled"])
        .optional()
        .describe("Only posts in this status"),
      from: z.iso.datetime({ offset: true }).optional().describe("RFC 3339 lower bound"),
      to: z.iso.datetime({ offset: true }).optional().describe("RFC 3339 upper bound"),
      limit: z.number().int().min(1).max(100).optional().describe("Page size (default set by the API)"),
      cursor: z.string().optional().describe("next_cursor from a previous call"),
    },
    annotations: { title: "List posts", ...readOnly },
    handler: (c, a) => c.request("GET", "/posts", { query: a }),
  }),
  defineTool({
    name: "get_post",
    title: "Get post",
    scope: "posts:read",
    risk: "safe",
    description: "Get a post with its per-account targets, media and publication attempts.",
    inputSchema: { post_id: id("Post id") },
    annotations: { title: "Get post", ...readOnly },
    handler: (c, a) => c.request("GET", `/posts/${seg(a.post_id)}`),
  }),
  defineTool({
    name: "get_post_status",
    title: "Get post status",
    scope: "posts:read",
    risk: "safe",
    description:
      "Get the lifecycle status of a post and of each target (pending, publishing, published, failed, needs_review). needs_review means Steerpost cannot confirm the post went out; do not retry without asking the user, or it may post twice. Poll this after publish_post or schedule_post.",
    inputSchema: { post_id: id("Post id") },
    annotations: { title: "Get post status", ...readOnly },
    handler: (c, a) => c.request("GET", `/posts/${seg(a.post_id)}/status`),
  }),
  defineTool({
    name: "get_analytics",
    title: "Get analytics",
    scope: "analytics:read",
    risk: "safe",
    description:
      "Get analytics metrics for a time window. Most providers do not expose analytics yet, so the result may be empty.",
    inputSchema: {
      from: z.iso.datetime({ offset: true }).optional().describe("RFC 3339 start"),
      to: z.iso.datetime({ offset: true }).optional().describe("RFC 3339 end"),
    },
    annotations: { title: "Get analytics", ...readOnly },
    handler: (c, a) => c.request("GET", "/analytics", { query: a }),
  }),
  defineTool({
    name: "get_usage",
    title: "Get plan usage",
    scope: "analytics:read",
    risk: "safe",
    description:
      "Get the plan and your usage against its limits: connected accounts, posts scheduled or published this month (UTC), media storage in bytes, plus the agent requests-per-minute limit (the limit only, not the current rate). A limit of -1 means unlimited. Use it before scheduling many posts or uploading media.",
    inputSchema: {},
    annotations: { title: "Get plan usage", ...readOnly },
    handler: (c) => c.request("GET", "/account/usage"),
  }),
];
