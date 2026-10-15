import type { CallToolResult } from "@modelcontextprotocol/sdk/types.js";
import { ApiError } from "./api-client.js";

const SECRET_PATTERNS: RegExp[] = [/sk_live_[A-Za-z0-9_\-]+/g, /Bearer\s+[A-Za-z0-9._\-~+/=]+/gi];

const exactSecrets = new Set<string>();

/** Registers a secret with no recognisable shape (the gateway secret) so redact() removes its exact value. */
export function registerSecret(secret: string | undefined): void {
  if (secret) exactSecrets.add(secret);
}

/** Strips anything that looks like an API key / bearer token, and every registered secret. */
export function redact(s: string): string {
  let out = SECRET_PATTERNS.reduce((acc, re) => acc.replace(re, "[redacted]"), s);
  for (const secret of exactSecrets) out = out.split(secret).join("[redacted]");
  return out;
}

const HINTS: Record<string, string> = {
  INSUFFICIENT_SCOPE: "The API key lacks the scope this action needs. Keys cannot gain scopes. Ask the user to create a new MCP connection with this permission under Developer > MCP connections.",
  EMAIL_NOT_VERIFIED: "The Steerpost account has not verified its email address, so this action is blocked. Ask the user to open the verification link in their email (it can be resent from the Steerpost banner).",
  QUOTA_EXCEEDED: "The plan limit is reached and nothing was done. Tell the user which limit it is (see the message). Do not retry in a loop. get_usage shows the limits if your key has the analytics:read scope.",
  APPROVAL_REQUIRED: "This action needs the owner's approval and was NOT performed. Ask the owner to approve it in Steerpost, wait until they confirm, then repeat the identical call with the approval_id below. An approval works once and only for that exact call.",
  FORBIDDEN: "The API key is not allowed to do this.",
  UNAUTHENTICATED: "The API key is invalid, expired or revoked.",
  SOCIAL_ACCOUNT_EXPIRED: "The social account's authorization expired. The user must reconnect it in Steerpost (this cannot be done through the API).",
  INVALID_STATE_TRANSITION: "The post is not in a state that allows this action. Call get_post_status to check its current status.",
  NOT_FOUND: "The resource does not exist or belongs to another user.",
  VALIDATION_ERROR: "The request was rejected as invalid; fix the arguments and retry.",
  RATE_LIMITED: "Rate limited; wait for the Retry-After pause before retrying.",
  PROVIDER_NOT_AVAILABLE: "This social network is not available yet.",
  PROVIDER_ERROR: "The social network returned an error. It may be temporary.",
  CONFLICT: "The action conflicts with the current state of the resource.",
};

/** The owner has not confirmed a key from sign_up yet; the API names the masked address and the deadline (D-025). */
function pendingHint(f: Readonly<Record<string, string>>): string {
  return `The owner has not confirmed this key yet. Ask them to open the email Steerpost sent to ${f.email_masked ?? "their address"} and press Confirm before ${f.expires_at ?? "the link expires"}. Do not call sign_up again.`;
}

/** approval_id, where to approve and until when; the API sends them as plain strings. */
function approvalDetails(f: Readonly<Record<string, string>>): string {
  const bits = [`approval_id: ${f.approval_id ?? "unknown"}`];
  if (f.approve_url) bits.push(`ask the owner to approve at ${f.approve_url}`);
  if (f.expires_at) bits.push(`expires_at: ${f.expires_at}`);
  return `(${bits.join("; ")})`;
}

export function errorResult(message: string): CallToolResult {
  return { isError: true, content: [{ type: "text", text: redact(message) }] };
}

/** Maps any thrown value to a safe, descriptive tool error. */
export function toToolError(err: unknown): CallToolResult {
  if (err instanceof ApiError) {
    const hint = err.code === "KEY_PENDING_CONFIRMATION" ? pendingHint(err.fields) : HINTS[err.code];
    const parts = [`${err.code}: ${err.message}`];
    if (hint) parts.push(hint);
    if (err.code === "APPROVAL_REQUIRED") parts.push(approvalDetails(err.fields));
    if (err.requestId) parts.push(`(request_id: ${err.requestId})`);
    return errorResult(parts.join(" "));
  }
  return errorResult("INTERNAL: unexpected error while calling Steerpost");
}
