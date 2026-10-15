import { McpServer } from "@modelcontextprotocol/sdk/server/mcp.js";
import type { CallToolResult } from "@modelcontextprotocol/sdk/types.js";
import { ApiError, type SocialOSClient } from "./api-client.js";
import { errorResult, toToolError } from "./errors.js";
import { ALL_TOOLS, type ToolDef } from "./tools/index.js";
import { jsonResult } from "./tools/types.js";

export const SERVER_INFO = { name: "steerpost", version: "0.1.0" } as const;

/** anonymous: no key (only sign_up); pending: a sign_up key the owner has not confirmed yet; authed: a working key. */
export type Mode = "anonymous" | "authed" | "pending";

const INSTRUCTIONS: Record<Mode, string> = {
  authed:
    "Steerpost lets you draft, schedule and publish social media posts. Prefer create_draft, then schedule_post. Before schedule_post, show the user the final text, accounts and time; scheduled posts publish without another check. " +
    "publish_post, delete_post, disconnect_account and scheduling or editing a post closer than the server's minimum lead (default 5 minutes) need the owner's approval in Steerpost, unless the key is trusted: " +
    "the first call answers APPROVAL_REQUIRED and does nothing. Tell the owner to approve it at the approve_url, wait until they confirm, " +
    "then repeat the identical call with the approval_id. An approval works once and only for that exact call. " +
    "The plan has limits (connected accounts, posts per month, media storage, requests per minute): get_usage shows them if your key has the analytics:read scope, and an action over a limit fails with QUOTA_EXCEEDED and is not performed.",
  anonymous:
    "Steerpost lets you draft, schedule and publish social media posts for your human owner. You are connected without an API key, so the only tool is sign_up: " +
    "ask the owner for their email address and whether they agree to the Steerpost Terms, then call it once. It returns an API key that works after the owner presses Confirm in the email Steerpost sends them. " +
    "Save the key, then reconnect with it (Authorization: Bearer for HTTP, STEERPOST_API_KEY for stdio) to see the other tools.",
  pending:
    "Steerpost lets you draft, schedule and publish social media posts. This API key comes from sign_up and the owner has not confirmed it yet, so every call answers KEY_PENDING_CONFIRMATION with the masked address and the deadline. " +
    "Ask the owner to open the email from Steerpost and press Confirm, then try again. Do not call sign_up again.",
};

/** Which tools a caller in this mode sees: public tools only without a key, never with one. */
function visible(tool: ToolDef, mode: Mode, granted: ReadonlySet<string>): boolean {
  if (mode === "anonymous") return tool.scope === "public";
  return tool.scope !== "public" && granted.has(tool.scope);
}

/** Builds a server exposing only the tools the mode and the API key's scopes allow. */
export function buildServer(client: SocialOSClient, scopes: readonly string[], mode: Mode): McpServer {
  const server = new McpServer(SERVER_INFO, { instructions: INSTRUCTIONS[mode] });
  const granted = new Set(scopes);

  for (const tool of ALL_TOOLS) {
    if (!visible(tool, mode, granted)) continue;
    server.registerTool(
      tool.name,
      {
        title: tool.title,
        description: `[risk: ${tool.risk}; scope: ${tool.scope}] ${tool.description}`,
        inputSchema: tool.inputSchema,
        annotations: tool.annotations,
      },
      async (args: unknown): Promise<CallToolResult> => {
        try {
          return jsonResult(await tool.handler(client.withTool(tool.name), args as never));
        } catch (err) {
          if (err instanceof Error && err.message.startsWith("VALIDATION_ERROR:")) return errorResult(err.message);
          return toToolError(err);
        }
      },
    );
  }
  return server;
}

export interface Access {
  mode: Exclude<Mode, "anonymous">;
  scopes: string[];
}

/**
 * Asks GET /me what the key may do. A key from sign_up that waits for the owner answers KEY_PENDING_CONFIRMATION with
 * its future scopes (space-separated), so the agent sees the tools it will get and each call explains the wait.
 * Any other failure is rethrown for the caller to map.
 */
export async function resolveAccess(client: SocialOSClient): Promise<Access> {
  try {
    return { mode: "authed", scopes: (await client.me()).scopes };
  } catch (err) {
    if (err instanceof ApiError && err.code === "KEY_PENDING_CONFIRMATION") {
      return { mode: "pending", scopes: (err.fields.scopes ?? "").split(" ").filter(Boolean) };
    }
    throw err;
  }
}
