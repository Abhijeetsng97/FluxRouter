// Generator: produces internal/cardlog/testdata/ts-written.{db,jsonl} using
// the TS engine's CardLog (node:sqlite) — the cross-read fixtures that prove
// the Go engine reads TS-produced databases byte-compatibly.
// Run from repo root: node old/scripts/gen-testdb.mjs
import { CardLog } from "../src/cardlog.ts";
import { mkdirSync, copyFileSync, rmSync } from "node:fs";
import { fileURLToPath } from "node:url";

const outDir = fileURLToPath(new URL("../../internal/cardlog/testdata/", import.meta.url));
mkdirSync(outDir, { recursive: true });

// Fresh temp data dir (do not touch any real .fluxrouter).
const tmp = fileURLToPath(new URL("./tmp-testdb/", import.meta.url));
rmSync(tmp, { recursive: true, force: true });

const cl = new CardLog(tmp);
cl.log({
  ts: "2026-09-30T12:00:00.000Z",
  sessionId: "a1b2c3d4e5f6a1b2c3d4e5f6",
  tier: 0,
  model: "nemotron-3-nano",
  upstream: "ollama",
  reason: "trivial_bypass",
  category: "greeting_chitchat",
  complexity: 0.2,
  confidence: 0.91,
  requestTokensEst: 34,
  usage: { input: 30, output: 28, cachedInput: 0 },
  costUsd: 0.0000149,
  projectedCostUsd: 0.0000192,
  latenciesMs: { jev: 240, upstream: 780, total: 1030 },
  status: 200,
  requestedModel: "flux",
});
cl.log({
  ts: "2026-09-30T12:01:00.000Z",
  sessionId: "a1b2c3d4e5f6a1b2c3d4e5f6",
  tier: 2,
  model: "deepseek-v4-pro:0813",
  upstream: "ollama",
  reason: "low_confidence_escalation",
  category: "math",
  complexity: 1.4,
  confidence: 0.31,
  requestTokensEst: 512,
  usage: { input: 480, output: 350, cachedInput: 0 },
  costUsd: 0.0010098,
  projectedCostUsd: 0.0010476,
  latenciesMs: { jev: 250, upstream: 1500, total: 1800 },
  status: 200,
  requestedModel: "flux",
});
cl.close();

copyFileSync(tmp + "route-cards.db", outDir + "ts-written.db");
copyFileSync(tmp + "route-cards.jsonl", outDir + "ts-written.jsonl");
rmSync(tmp, { recursive: true, force: true });
console.log("wrote internal/cardlog/testdata/ts-written.{db,jsonl}");