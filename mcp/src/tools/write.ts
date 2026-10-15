import { z } from "zod";
import { approvalId, defineTool, seg } from "./types.js";

const id = (what: string) => z.string().min(1).max(128).describe(what);
const when = (what: string) => z.iso.datetime({ offset: true }).describe(what);
const perPlatform = z
  .record(z.string(), z.string().min(1))
  .describe(
    "Optional per-account text override: map of social_account_id to the text to use for that account (for example, a shorter text for one network). Accounts not listed use `content`.",
  );

function toTargets(overrides: Record<string, string> | undefined, accountIds: string[] | undefined) {
  if (!overrides) return undefined;
  const unknown = accountIds ? Object.keys(overrides).filter((k) => !accountIds.includes(k)) : [];
  if (unknown.length) {
    throw new Error(`VALIDATION_ERROR: per_platform_content has ids not in social_account_ids: ${unknown.join(", ")}`);
  }
  return Object.entries(overrides).map(([social_account_id, content]) => ({ social_account_id, content }));
}

export const writeTools = [
  defineTool({
    name: "create_draft",
    title: "Create draft post",
    scope: "posts:write",
    risk: "safe",
    description:
      "Create a DRAFT post for one or more connected accounts. Nothing is published or scheduled; use schedule_post or publish_post afterwards. Returns the post with its id.",
    inputSchema: {
      content: z.string().min(1).describe("Post text"),
      social_account_ids: z.array(z.string().min(1)).min(1).describe("Target accounts from list_social_accounts"),
      media_ids: z.array(z.string().min(1)).optional().describe("Ids of files the user uploaded to the Steerpost media library. This server cannot upload files."),
      title: z.string().optional().describe("Post title. Networks with requires_title in list_platforms need it and publish it; others ignore it."),
      per_platform_content: perPlatform.optional(),
    },
    annotations: { title: "Create draft post", readOnlyHint: false, destructiveHint: false, idempotentHint: false, openWorldHint: false },
    handler: (c, a) =>
      c.request("POST", "/posts", {
        body: {
          title: a.title,
          content: a.content,
          social_account_ids: a.social_account_ids,
          media_ids: a.media_ids,
          targets: toTargets(a.per_platform_content, a.social_account_ids),
        },
      }),
  }),
  defineTool({
    name: "update_post",
    title: "Update post",
    scope: "posts:write",
    risk: "low",
    description:
      "Edit a post that is still a draft or scheduled (the API rejects other states with INVALID_STATE_TRANSITION). Only the fields you pass are changed.",
    inputSchema: {
      post_id: id("Post id"),
      title: z.string().optional(),
      content: z.string().min(1).optional(),
      social_account_ids: z.array(z.string().min(1)).min(1).optional(),
      media_ids: z.array(z.string().min(1)).optional(),
      per_platform_content: perPlatform.optional(),
      scheduled_at: when("New RFC 3339 schedule time (only for scheduled posts); closer than the server's minimum lead (default 5 minutes) needs the owner's approval").optional(),
      approval_id: approvalId,
    },
    annotations: { title: "Update post", readOnlyHint: false, destructiveHint: false, idempotentHint: true, openWorldHint: false },
    handler: (c, a) => {
      const { post_id, per_platform_content, approval_id, ...rest } = a;
      const body = { ...rest, targets: toTargets(per_platform_content, rest.social_account_ids) };
      if (Object.values(body).every((v) => v === undefined)) {
        throw new Error("VALIDATION_ERROR: pass at least one field to update");
      }
      return c.request("PATCH", `/posts/${seg(post_id)}`, { body, approvalId: approval_id });
    },
  }),
  defineTool({
    name: "schedule_post",
    title: "Schedule post",
    scope: "posts:schedule",
    risk: "medium",
    description:
      "Schedule a draft to be published automatically at scheduled_at (RFC 3339, must be in the future). The post goes public at that time with no further approval, unless canceled with cancel_scheduled_post. Before you call this, show the user the final text, accounts and time. A time closer than the server's minimum lead (default 5 minutes) counts as publishing now and needs the owner's approval (APPROVAL_REQUIRED, then repeat the call with approval_id).",
    inputSchema: {
      post_id: id("Post id"),
      scheduled_at: when("Publish time, RFC 3339 e.g. 2026-11-01T09:00:00Z"),
      approval_id: approvalId,
    },
    annotations: { title: "Schedule post", readOnlyHint: false, destructiveHint: false, idempotentHint: true, openWorldHint: true },
    handler: (c, a) =>
      c.request("POST", `/posts/${seg(a.post_id)}/schedule`, { body: { scheduled_at: a.scheduled_at }, approvalId: a.approval_id }),
  }),
  defineTool({
    name: "cancel_scheduled_post",
    title: "Cancel scheduled post",
    scope: "posts:write",
    risk: "medium",
    description:
      "Cancel a draft or scheduled post. A canceled post never publishes and cannot be restored; to reuse the content, create a new draft.",
    inputSchema: { post_id: id("Post id") },
    annotations: { title: "Cancel scheduled post", readOnlyHint: false, destructiveHint: true, idempotentHint: true, openWorldHint: false },
    handler: (c, a) => c.request("POST", `/posts/${seg(a.post_id)}/cancel`),
  }),
  defineTool({
    name: "publish_post",
    title: "Publish post now",
    scope: "posts:publish",
    risk: "sensitive",
    description:
      "Publishes the post to the live social networks immediately and cannot be undone by this API. Unless the key is trusted, the owner must approve it in Steerpost first: the first call answers APPROVAL_REQUIRED with an approval_id and nothing is published; once the owner approved, repeat the identical call with approval_id. Returns immediately; poll get_post_status for the outcome.",
    inputSchema: { post_id: id("Post id"), approval_id: approvalId },
    annotations: { title: "Publish post now", readOnlyHint: false, destructiveHint: true, idempotentHint: false, openWorldHint: true },
    handler: (c, a) => {
      return c.request("POST", `/posts/${seg(a.post_id)}/publish`, { approvalId: a.approval_id });
    },
  }),
  defineTool({
    name: "delete_post",
    title: "Delete post",
    scope: "posts:delete",
    risk: "sensitive",
    description:
      "Deletes a post in Steerpost. Published copies stay on the networks. Unless the key is trusted, the owner must approve it in Steerpost first (APPROVAL_REQUIRED, then repeat the call with approval_id).",
    inputSchema: { post_id: id("Post id"), approval_id: approvalId },
    annotations: { title: "Delete post", readOnlyHint: false, destructiveHint: true, idempotentHint: true, openWorldHint: false },
    handler: (c, a) => {
      return c.request("DELETE", `/posts/${seg(a.post_id)}`, { approvalId: a.approval_id });
    },
  }),
  defineTool({
    name: "disconnect_account",
    title: "Disconnect social account",
    scope: "social:disconnect",
    risk: "critical",
    description:
      "Disconnects a social account and deletes its stored credentials. Its scheduled posts fail unless the user connects it again before they are due. Published posts stay on the network. Unless the key is trusted, the owner must approve it in Steerpost first (APPROVAL_REQUIRED, then repeat the call with approval_id).",
    inputSchema: { account_id: id("Social account id"), approval_id: approvalId },
    annotations: { title: "Disconnect social account", readOnlyHint: false, destructiveHint: true, idempotentHint: true, openWorldHint: false },
    handler: (c, a) => {
      return c.request("DELETE", `/social/accounts/${seg(a.account_id)}`, { approvalId: a.approval_id });
    },
  }),
];
