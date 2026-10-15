#!/usr/bin/env node
import { StdioServerTransport } from "@modelcontextprotocol/sdk/server/stdio.js";
import { SocialOSClient } from "./api-client.js";
import { loadConfig, readEnv } from "./config.js";
import { createHttpServer } from "./http.js";
import { log } from "./log.js";
import { buildServer, resolveAccess } from "./server.js";

async function runStdio(): Promise<void> {
  const config = loadConfig();
  const apiKey = readEnv(process.env, "API_KEY");
  const client = new SocialOSClient({ baseUrl: config.apiUrl, apiKey, timeoutMs: config.timeoutMs });
  if (!apiKey) {
    // Without a key the agent can only sign up; it restarts this server with the key it gets back.
    await buildServer(client, [], "anonymous").connect(new StdioServerTransport());
    log("info", "socialos-mcp stdio ready without STEERPOST_API_KEY: only sign_up is available", { mode: "anonymous" });
    return;
  }
  const { mode, scopes } = await resolveAccess(client);
  await buildServer(client, scopes, mode).connect(new StdioServerTransport());
  log("info", "socialos-mcp stdio ready", { mode, tools_scopes: scopes.length });
}

function runHttp(): void {
  const config = loadConfig();
  const server = createHttpServer(config);
  server.listen(config.port, config.host, () => log("info", "socialos-mcp listening", { port: config.port, api: config.apiUrl }));
  const stop = (): void => {
    server.close(() => process.exit(0));
    setTimeout(() => process.exit(0), 5000).unref();
  };
  process.on("SIGTERM", stop);
  process.on("SIGINT", stop);
}

const stdio = process.argv.includes("--stdio") || process.env.MCP_TRANSPORT === "stdio";
try {
  if (stdio) await runStdio();
  else runHttp();
} catch (err) {
  log("error", "startup failed", { error: err instanceof Error ? err.message : "unknown" });
  process.exit(1);
}
