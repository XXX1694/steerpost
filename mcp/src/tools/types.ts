import type { CallToolResult, ToolAnnotations } from "@modelcontextprotocol/sdk/types.js";
import { z, type ZodRawShape } from "zod";
import type { SocialOSClient } from "../api-client.js";

export type Scope =
  | "social:read"
  | "posts:read"
  | "posts:write"
  | "posts:schedule"
  | "posts:publish"
  | "posts:delete"
  | "social:disconnect"
  | "social:connect"
  | "media:write"
  | "analytics:read"
  /** No key needed: the only tools an anonymous caller sees (sign_up). Never shown to a caller with a key. */
  | "public";

export type Risk = "safe" | "low" | "medium" | "sensitive" | "critical";

export interface ToolDef<S extends ZodRawShape = ZodRawShape> {
  name: string;
  title: string;
  scope: Scope;
  risk: Risk;
  /** Human description; the risk label is prepended automatically. */
  description: string;
  inputSchema: S;
  annotations: ToolAnnotations;
  handler: (client: SocialOSClient, args: z.infer<z.ZodObject<S>>) => Promise<unknown>;
}

/** Helper that preserves the schema type for the handler's args. */
export function defineTool<S extends ZodRawShape>(def: ToolDef<S>): ToolDef {
  return def as unknown as ToolDef;
}

export function jsonResult(data: unknown): CallToolResult {
  return { content: [{ type: "text", text: JSON.stringify(data, null, 2) }] };
}

/**
 * The owner's approval for one dangerous call. The first call answers APPROVAL_REQUIRED with an approval_id; once the
 * owner approved it in Steerpost, repeat the identical call with that id (D-013). It works once.
 */
export const approvalId = z
  .uuid()
  .optional()
  .describe("approval_id from an earlier APPROVAL_REQUIRED answer, after the owner approved it in Steerpost. Repeat the identical call with it. Works once.");

export const seg = (id: string): string => encodeURIComponent(id);
