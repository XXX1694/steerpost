import { z } from "zod";
import { ApiError } from "../api-client.js";
import { approvalId, defineTool } from "./types.js";

interface SignUpAnswer {
  api_key?: string;
  email_masked?: string;
  expires_at?: string;
  delivery?: string;
  [k: string]: unknown;
}

/** The line that tells the agent what to do with the key it just received. */
function nextStep(a: SignUpAnswer): string {
  const parts = [
    "Save api_key now: it is shown once. Reconnect to this server with it, as `Authorization: Bearer <api_key>` (HTTP) or STEERPOST_API_KEY (stdio).",
    `It starts working when the owner presses Confirm in the email sent to ${a.email_masked ?? "their address"}${a.expires_at ? ` before ${a.expires_at}` : ""}. Until then every call answers KEY_PENDING_CONFIRMATION. Do not call sign_up again.`,
  ];
  if (a.delivery === "log") {
    parts.push("This server does not send mail (MAIL_PROVIDER=log, development only): the confirm link was written to the server log.");
  }
  return parts.join(" ");
}

/**
 * Removes the submitted credential values from an API error before it reaches the agent's transcript. The API never
 * echoes them; this keeps it that way if an upstream message ever does.
 */
function withoutValues(err: unknown, values: string[]): unknown {
  if (!(err instanceof ApiError)) return err;
  let message = err.message;
  for (const v of values) if (v.length >= 4) message = message.split(v).join("[redacted]");
  return new ApiError(err.status, err.code, message, err.requestId, err.fields);
}

export const onboardingTools = [
  defineTool({
    name: "sign_up",
    title: "Sign up for Steerpost",
    scope: "public",
    risk: "medium",
    description:
      "Create a Steerpost account for your human owner, or ask for access to their existing one. Steerpost emails the owner; the returned API key (shown once, save it) stays inactive until they press Confirm in that email (valid 24 hours). The answer is the same whether or not the address has an account. Once confirmed the key can read accounts, posts and analytics, write drafts, upload media, schedule posts at least 5 minutes ahead and connect token-based networks; it can never publish immediately, delete posts or disconnect accounts. Pass accept_terms=true only after the owner agreed to the Steerpost Terms. Rate limited.",
    inputSchema: {
      email: z.email().max(254).describe("The owner's email address. Steerpost sends the confirmation link there."),
      agent_name: z.string().min(1).max(60).describe("How the owner will recognise you in the email and under Developer, e.g. 'Claude Desktop'. Letters, digits, spaces and -_.()' only."),
      accept_terms: z.literal(true).describe("true only after the owner agreed to the Steerpost Terms"),
    },
    annotations: { title: "Sign up for Steerpost", readOnlyHint: false, destructiveHint: false, idempotentHint: false, openWorldHint: true },
    handler: async (c, a) => {
      const answer = await c.request<SignUpAnswer>("POST", "/auth/agent-signups", { body: a });
      return { ...answer, next: nextStep(answer) };
    },
  }),
  defineTool({
    name: "list_platforms",
    title: "List platforms",
    scope: "social:read",
    risk: "safe",
    description:
      "List the networks this Steerpost server supports and how each connects: connect_method `token` (use connect_account with its connect_fields), `oauth` or `telegram_chat` (the owner must connect it in the Steerpost web app), plus limits, whether it is configured, and notes such as app review needed.",
    inputSchema: {},
    annotations: { title: "List platforms", readOnlyHint: true, destructiveHint: false, idempotentHint: true, openWorldHint: false },
    handler: (c) => c.request("GET", "/social/providers"),
  }),
  defineTool({
    name: "connect_account",
    title: "Connect social account",
    scope: "social:connect",
    risk: "critical",
    description:
      "Connect a social account that uses a pasted credential (connect_method `token` in list_platforms), e.g. a Bluesky app password or a Discord webhook URL. Ask the owner for every value; never invent one. Steerpost checks it live with the network (up to 10 s), stores it encrypted and never returns or logs it. Keys without connect trust answer APPROVAL_REQUIRED first.",
    inputSchema: {
      provider: z.string().min(1).max(64).describe("Network id from list_platforms, e.g. 'bluesky'"),
      fields: z
        .record(z.string().min(1).max(64), z.string().max(2048))
        .describe("Values for that network's connect_fields, keyed by field name (from list_platforms). Required fields must be present."),
      approval_id: approvalId,
    },
    annotations: { title: "Connect social account", readOnlyHint: false, destructiveHint: false, idempotentHint: false, openWorldHint: true },
    handler: async (c, a) => {
      try {
        return await c.request("POST", "/social/accounts/token", {
          body: { provider: a.provider, fields: a.fields },
          approvalId: a.approval_id,
        });
      } catch (err) {
        throw withoutValues(err, Object.values(a.fields));
      }
    },
  }),
];
