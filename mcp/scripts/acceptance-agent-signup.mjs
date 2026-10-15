// Acceptance check of agent sign-up over MCP (D-025): an agent without a key signs its owner up, the owner confirms
// from the email, and the agent connects a token network and schedules posts on its own.
// Requires the same running stack as acceptance.mjs (API with SOCIAL_MOCK_PROVIDERS=true, worker, MCP server).
// The confirm token comes from CONFIRM_TOKEN, or else from the worker's dev mail log (MAIL_PROVIDER=log,
// APP_ENV=development) read with `docker compose logs worker` from the repository root.
//
//   node scripts/acceptance-agent-signup.mjs        (also run at the end of acceptance.mjs)
import { execFileSync } from "node:child_process";
import { dirname, resolve } from "node:path";
import { fileURLToPath, pathToFileURL } from "node:url";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";

const REPO_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
const LINK = /\/agent-access#token=([A-Za-z0-9_-]+)/g;

function step(msg) {
  console.log(`✓ ${msg}`);
}
function check(cond, msg) {
  if (!cond) throw new Error(msg);
}
const parse = (r) => JSON.parse(r.content[0].text);

async function mcpClient(mcpUrl, key) {
  const client = new Client({ name: "acceptance-signup-agent", version: "1.0.0" });
  const requestInit = key ? { headers: { authorization: `Bearer ${key}` } } : undefined;
  await client.connect(new StreamableHTTPClientTransport(new URL(mcpUrl), { requestInit }));
  return client;
}

/** The newest confirm link the dev mail log printed for this masked address since the sign-up started. */
function tokenFromWorkerLog(since, emailMasked) {
  const out = execFileSync("docker", ["compose", "logs", "--no-color", "--since", since, "worker"], {
    cwd: REPO_ROOT,
    encoding: "utf8",
    maxBuffer: 64 * 1024 * 1024,
  });
  const lines = out.split("\n").filter((l) => l.includes(emailMasked) && l.includes("/agent-access#token="));
  const last = lines.at(-1);
  if (!last) return undefined;
  return [...last.matchAll(LINK)].at(-1)?.[1];
}

async function confirmToken(since, emailMasked) {
  if (process.env.CONFIRM_TOKEN) return process.env.CONFIRM_TOKEN;
  // The worker sends the mail asynchronously; give it a few seconds.
  for (let i = 0; i < 20; i++) {
    const token = tokenFromWorkerLog(since, emailMasked);
    if (token) return token;
    await new Promise((r) => setTimeout(r, 1000));
  }
  throw new Error(`no /agent-access link for ${emailMasked} in the worker log; set CONFIRM_TOKEN to run without Docker`);
}

async function signUpAnonymously(mcpUrl, email) {
  const anon = await mcpClient(mcpUrl);
  const names = (await anon.listTools()).tools.map((t) => t.name);
  check(names.length === 1 && names[0] === "sign_up", `anonymous tools: ${names}`);
  step("anonymous agent sees only sign_up");
  const res = await anon.callTool({ name: "sign_up", arguments: { email, agent_name: "Acceptance Agent", accept_terms: true } });
  check(!res.isError, `sign_up failed: ${res.content[0].text}`);
  const signup = parse(res);
  await anon.close();
  check(signup.api_key?.startsWith("sk_live_") && signup.status === "pending_confirmation", `sign_up answer: ${JSON.stringify({ ...signup, api_key: "[redacted]" })}`);
  check(signup.dangerous_policy === "trusted_connect" && signup.scopes.includes("social:connect"), `policy/scopes: ${signup.dangerous_policy} ${signup.scopes}`);
  check(!signup.scopes.some((s) => ["posts:publish", "posts:delete", "social:disconnect"].includes(s)), `dangerous scopes: ${signup.scopes}`);
  step(`sign_up → pending key ${signup.key_prefix}…, mail to ${signup.email_masked} (delivery: ${signup.delivery})`);
  return signup;
}

async function expectPending(mcpUrl, signup) {
  const pending = await mcpClient(mcpUrl, signup.api_key);
  const r = await pending.callTool({ name: "list_social_accounts", arguments: {} });
  const msg = r.content[0].text;
  check(r.isError && msg.includes("KEY_PENDING_CONFIRMATION") && msg.includes(signup.email_masked), `pending call: ${msg}`);
  await pending.close();
  step("a call with the unconfirmed key answers KEY_PENDING_CONFIRMATION with the masked address");
}

async function confirm(api, token) {
  const res = await fetch(new URL("/api/v1/auth/agent-signups/confirm", api), {
    method: "POST",
    headers: { "content-type": "application/json" },
    body: JSON.stringify({ token, accept_terms: true }),
  });
  const body = await res.json();
  check(res.status === 200 && body.account_created === true, `confirm: ${res.status} ${JSON.stringify(body)}`);
  step("owner confirmed from the email link; a new account was created");
}

async function connectMockToken(agent) {
  const names = (await agent.listTools()).tools.map((t) => t.name);
  for (const n of ["list_platforms", "connect_account", "create_draft", "schedule_post"]) check(names.includes(n), `missing ${n}: ${names}`);
  for (const n of ["publish_post", "delete_post", "disconnect_account", "sign_up"]) check(!names.includes(n), `${n} must be hidden: ${names}`);
  step(`confirmed key sees ${names.length} tools; publish_post, delete_post, disconnect_account and sign_up hidden`);

  const platforms = parse(await agent.callTool({ name: "list_platforms", arguments: {} }));
  const mock = (platforms.items ?? platforms).find((p) => p.id === "mocktoken");
  check(mock?.capabilities?.connect_method === "token", `mocktoken: ${JSON.stringify(mock)}`);
  check(mock.capabilities.connect_fields.some((f) => f.name === "api_key" && f.required), "mocktoken has no api_key field");
  step("list_platforms shows mocktoken with connect_method token");

  const r = await agent.callTool({
    name: "connect_account",
    arguments: { provider: "mocktoken", fields: { api_key: `mt_acceptance_${Date.now()}`, handle: "acceptance" } },
  });
  check(!r.isError, `connect_account: ${r.content[0].text}`);
  const account = parse(r);
  step(`connect_account mocktoken → account ${account.id}, no approval needed`);
  return account;
}

async function draft(agent, accountId, n) {
  const d = parse(await agent.callTool({
    name: "create_draft",
    arguments: { title: `Agent sign-up ${n}`, content: `Scheduled by an agent that signed itself up (${n})`, social_account_ids: [accountId] },
  }));
  check(d.status === "draft", `draft status ${d.status}`);
  return d;
}

async function scheduleOnItsOwn(agent, accountId) {
  const later = await draft(agent, accountId, 1);
  const at = new Date(Date.now() + 10 * 60_000).toISOString();
  const r = await agent.callTool({ name: "schedule_post", arguments: { post_id: later.id, scheduled_at: at } });
  check(!r.isError && parse(r).status === "scheduled", `schedule +10 min: ${r.content[0].text}`);
  step(`create_draft → schedule_post at ${at} (+10 min) without approval`);

  const soon = await draft(agent, accountId, 2);
  const nearly = new Date(Date.now() + 60_000).toISOString();
  const refused = await agent.callTool({ name: "schedule_post", arguments: { post_id: soon.id, scheduled_at: nearly } });
  check(refused.isError && refused.content[0].text.includes("APPROVAL_REQUIRED"), `schedule +1 min: ${refused.content[0].text}`);
  step("schedule_post +1 min answers APPROVAL_REQUIRED and does nothing");

  for (const id of [later.id, soon.id]) await agent.callTool({ name: "cancel_scheduled_post", arguments: { post_id: id } });
  const publish = await agent.callTool({ name: "publish_post", arguments: { post_id: later.id } }).catch((e) => e);
  check(publish instanceof Error || publish.isError, "publish_post must not be callable");
  step("publish_post is not available to the agent");
}

export async function runAgentSignupFlow({ api, mcpUrl }) {
  const email = `agent+${Date.now()}@example.com`;
  const since = new Date(Date.now() - 5000).toISOString();
  const signup = await signUpAnonymously(mcpUrl, email);
  await expectPending(mcpUrl, signup);
  await confirm(api, await confirmToken(since, signup.email_masked));
  const agent = await mcpClient(mcpUrl, signup.api_key);
  const account = await connectMockToken(agent);
  await scheduleOnItsOwn(agent, account.id);
  await agent.close();
  console.log("\nAGENT SIGN-UP FLOW PASSED");
}

if (process.argv[1] && import.meta.url === pathToFileURL(process.argv[1]).href) {
  try {
    await runAgentSignupFlow({
      api: process.env.SOCIALOS_API_URL ?? "http://127.0.0.1:8080",
      mcpUrl: process.env.MCP_URL ?? "http://127.0.0.1:3333/mcp",
    });
  } catch (err) {
    console.error(`✗ ${err instanceof Error ? err.message : err}`);
    process.exit(1);
  }
}
