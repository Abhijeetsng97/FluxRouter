#!/usr/bin/env node
// FluxRouter server entrypoint: `fluxrouter` bin / `npm start`.

import { serve } from "@hono/node-server";
import { Hono } from "hono";
import { loadConfig } from "./config.ts";
import { loadDotEnv } from "./env.ts";
import { CardLog } from "./cardlog.ts";
import { Metrics } from "./metrics.ts";
import { RouterService, parseChatRequest } from "./router.ts";
import { buildModelsResponse } from "./models.ts";
import { VERSION } from "./version.ts";
import type { Upstreams } from "./upstream.ts";
import { FLUX_MODEL_ALIAS } from "./types.ts";

interface CliOptions {
  config?: string;
  port?: number;
  host?: string;
  envFile?: string;
}

interface CliEnv {
  argv?: string[];
}

export async function main(env: CliEnv = {}): Promise<void> {
  const opts = parseOpts(env);
  const dotenv = loadDotEnv(opts.envFile);
  if (dotenv.loaded.length > 0) {
    console.log(`Loaded env file(s): ${dotenv.loaded.join(", ")} (real environment variables take precedence)`);
  }
  const { config, errors } = loadConfig(opts.config ?? (await tryFindConfig()));
  if (errors.length > 0) {
    console.error("FluxRouter config errors:");
    for (const e of errors) console.error(`  - ${e}`);
    process.exit(2);
  }

  if (opts.port) config.server.port = opts.port;
  if (opts.host) config.server.host = opts.host;

  if (config.server.host !== "127.0.0.1" && !process.env["FLUX_AUTH_TOKEN"]) {
    console.error(
      "Refusing to bind non-loopback host without FLUX_AUTH_TOKEN set (see ADR / README security section).",
    );
    process.exit(2);
  }

  const apiKeyMissing = missingKeys(config);
  if (apiKeyMissing.length > 0) {
    console.error("Missing API keys in environment:");
    for (const k of apiKeyMissing) console.error(`  - ${k}`);
    console.error("Set them in your shell, or copy .env.example to .env and fill it in.");
    process.exit(2);
  }

  // Optional lanes: warn but do not block boot.
  const optionalMissing = optionalKeys(config).filter((k) => !process.env[k]);
  if (optionalMissing.length > 0) {
    console.warn(`warning: optional key(s) not set: ${optionalMissing.join(", ")}`);
    console.warn("  OpenRouter failover/burst lane is disabled; failover will use Ollama tiers only.");
  }

  const cardLog = new CardLog(config.dataDir);
  const metrics = new Metrics();
  const upstreams: Upstreams = {
    ollama: { baseUrl: config.upstreams.ollama.baseUrl, apiKey: process.env[config.upstreams.ollama.apiKeyEnv] ?? "" },
    openrouter: {
      baseUrl: config.upstreams.openrouter.baseUrl,
      apiKey: process.env[config.upstreams.openrouter.apiKeyEnv] ?? "",
    },
  };
  const router = new RouterService({ config, upstreams, cardLog, metrics });

  const app = new Hono();
  const token = process.env["FLUX_AUTH_TOKEN"];

  // Optional auth middleware.
  app.use("*", async (c, next) => {
    if (token) {
      const auth = c.req.header("Authorization") ?? "";
      if (auth !== `Bearer ${token}`) {
        return c.json({ error: { message: "Unauthorized", type: "fluxrouter_auth" } }, 401);
      }
    }
    await next();
  });

  app.get("/v1/models", (c) => {
    return c.json(buildModelsResponse(config));
  });

  app.get("/metrics", (c) => c.text(metrics.snapshot()));

  app.get("/health", (c) => c.json({ ok: true }));

  app.post("/v1/chat/completions", async (c) => {
    metrics.inc("chat_requests_total");
    const startTs = Date.now();
    let body: any;
    try {
      body = await c.req.json();
    } catch {
      return c.json({ error: { message: "Invalid JSON body", type: "fluxrouter_bad_request" } }, 400);
    }
    const req = parseChatRequest(body);
    if (!req) {
      return c.json({ error: { message: "messages[] is required", type: "fluxrouter_bad_request" } }, 400);
    }
    if (config.respectIncomingModel && req.model && req.model !== FLUX_MODEL_ALIAS) {
      // Bypass mode: forward as-is to the same tier ladder only if the model names a tier; else passthrough tier 1.
      // For v1, respectIncomingModel=true pins to tier 1 unless the model is "flux".
      // See README. Default is false (full routing).
    }
    try {
      return await router.execute(body, req, { requestedModel: req.model, startTs });
    } catch (e) {
      metrics.inc("server_errors_total");
      return c.json({ error: { message: (e as Error).message, type: "fluxrouter_internal" } }, 500);
    }
  });

  const server = serve({ fetch: app.fetch, port: config.server.port, hostname: config.server.host }, (info) => {
    console.log(`FluxRouter v${VERSION} listening on http://${info.address}:${info.port}`);
    console.log(`  OpenAI base URL: http://${info.address}:${info.port}/v1`);
    console.log(`  model alias: ${FLUX_MODEL_ALIAS} (routing by Jev classification)`);
    console.log(`  route cards: ${config.dataDir}/route-cards.{jsonl,db}`);
  });

  const shutdown = () => {
    console.log("\nShutting down…");
    server.close(() => {
      router.dispose();
      cardLog.close();
      process.exit(0);
    });
    setTimeout(() => process.exit(0), 2000).unref();
  };
  process.on("SIGINT", shutdown);
  process.on("SIGTERM", shutdown);
}

function parseOpts(env: CliEnv): CliOptions {
  const argv = env.argv ?? process.argv.slice(2);
  const opts: CliOptions = {};
  for (let i = 0; i < argv.length; i++) {
    const a = argv[i]!;
    if (a === "--config" || a === "-c") opts.config = argv[++i];
    else if (a === "--port" || a === "-p") opts.port = Number(argv[++i]);
    else if (a === "--host") opts.host = argv[++i];
    else if (a === "--env-file") opts.envFile = argv[++i];
  }
  return opts;
}

async function tryFindConfigPath(): Promise<string | undefined> {
  const candidates = ["fluxrouter.config.json", "config/fluxrouter.config.json"];
  for (const c of candidates) {
    try {
      await import(`../${c}`, { with: { type: "json" } });
      return c;
    } catch {
      // keep looking
    }
  }
  return undefined;
}

function tryFindConfig(): Promise<string | undefined> {
  return tryFindConfigPath().catch(() => undefined);
}

/** Keys without which the server cannot route at all. */
function requiredKeys(config: { upstreams: { ollama: { apiKeyEnv: string } } }): string[] {
  return [config.upstreams.ollama.apiKeyEnv, "TYPESAFE_API_KEY"];
}

/** Keys whose absence degrades a lane but do not block boot. */
function optionalKeys(config: { upstreams: { openrouter: { apiKeyEnv: string } } }): string[] {
  return [config.upstreams.openrouter.apiKeyEnv];
}

function missingKeys(config: { upstreams: { ollama: { apiKeyEnv: string } } }): string[] {
  return requiredKeys(config).filter((k) => !process.env[k]);
}

main().catch((e) => {
  console.error(e);
  process.exit(1);
});