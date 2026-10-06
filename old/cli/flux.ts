#!/usr/bin/env node
// `flux` CLI: report | trace | config | eval.

import { loadDotEnv } from "../src/env.ts";
import { VERSION } from "../src/version.ts";
import { defineCommand, run } from "./cmd.ts";

// Allow keys from .env for local usage; real environment variables win.
loadDotEnv();

const report = defineCommand({
  name: "report",
  description: "Aggregate spend and performance from route cards",
  flags: [
    { name: "--by", arg: "day|tier|category|model|session", description: "group by (default: day)" },
    { name: "--today", description: "only today's cards" },
  ],
  run: async (args) => {
    const { runReport } = await import("./report.ts");
    await runReport(args);
  },
});

const configCmd = defineCommand({
  name: "config",
  description: "Validate fluxrouter.config.json",
  flags: [{ name: "--file", arg: "path", description: "config path (default fluxrouter.config.json)" }],
  run: async (args) => {
    const { runConfigValidate } = await import("./config-cli.ts");
    await runConfigValidate(args);
  },
});

const traceCmd = defineCommand({
  name: "trace",
  description: "Show the full classify → policy decision for one prompt (real code path)",
  flags: [
    { name: "--offline", description: "no Jev call; supply values via other flags" },
    { name: "--category", arg: "name", description: "offline: category (default other)" },
    { name: "--complexity", arg: "0..2", description: "offline: complexity score" },
    { name: "--confidence", arg: "0..1", description: "offline: category+complexity confidence" },
    { name: "--noul", arg: "0..1", description: "offline: trivial noul probability" },
  ],
  run: async (args) => {
    const { runTrace } = await import("./trace.ts");
    await runTrace(args);
  },
});

const evalCmd = defineCommand({
  name: "eval",
  description: "Run budget-guarded evals",
  flags: [],
  subcommands: [
    defineCommand({
      name: "routing",
      description: "Stage 1: classify-only routing accuracy (~$0.50, cap $1)",
      flags: [
        { name: "--limit", arg: "n", description: "limit prompts per dataset (default 100)" },
        { name: "--only", arg: "src,src", description: "only these sources (e.g. trivial,gsm8k)" },
        { name: "--dry-run", description: "show plan and projected cost, no calls" },
      ],
      run: async (args) => {
        const { runRoutingEval } = await import("./eval/routing.ts");
        await runRoutingEval(args);
      },
    }),
    defineCommand({
      name: "e2e",
      description: "Stage 2: end-to-end quality + cost (cap $10)",
      flags: [
        { name: "--limit", arg: "n", description: "total prompts (default 120, stratified 30x4)" },
        { name: "--dry-run", description: "show plan and projected cost, no calls" },
      ],
      run: async (args) => {
        const { runE2eEval } = await import("./eval/e2e.ts");
        await runE2eEval(args);
      },
    }),
  ],
  run: async () => {
    console.log("usage: flux eval <routing|e2e> [--dry-run] [--limit n]");
  },
});

await run({
  name: "flux",
  version: VERSION,
  description: "FluxRouter control plane",
  commands: [report, configCmd, traceCmd, evalCmd],
  args: process.argv.slice(2),
});