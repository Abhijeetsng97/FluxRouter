// Stage 1 eval: routing-only accuracy. Jev classifies each prompt; policy decides a
// tier; NO upstream generation happens. Cost ≈ $0.001/prompt; hard budget guard.

import { classify, buildJevState } from "../../src/classify.ts";
import { routeRequest } from "../../src/policy.ts";
import { loadConfig } from "../../src/config.ts";
import { budgetGuard, estimateTokens } from "../../src/cost.ts";
import { loadRoutingSet, type LabeledPrompt } from "./datasets.ts";
import { mkdirSync, writeFileSync } from "node:fs";
import { join } from "node:path";

interface RoutingArgs {
  flags: Record<string, string | boolean>;
}

const JEVD_PER_PROMPT_USD = 0.001;

interface SetStats {
  n: number;
  within1: number;
  exact: number;
  timeouts: number;
}

export async function runRoutingEval(args: RoutingArgs): Promise<void> {
  const { config } = loadConfig("fluxrouter.config.json");
  const limit = Number(args.flags["limit"] ?? 100);

  const jevKey = process.env["TYPESAFE_API_KEY"];
  if (!jevKey && args.flags["dry-run"] !== true) {
    console.error("TYPESAFE_API_KEY not set; refusing to run (set it or use --dry-run).");
    process.exit(2);
  }

  // Pre-flight budget guard (AC6): refuse to start above cap.
  const { prompts, loaded, failed } = await loadRoutingSet(limit);
  if (failed.length > 0) {
    console.warn("warning: some datasets failed to load and were skipped:");
    for (const f of failed) console.warn(`  - ${f.source}: ${f.error}`);
  }
  if (prompts.length === 0) {
    console.error("No prompts could be loaded (offline?). Nothing to do.");
    process.exit(2);
  }
  // Optional --only <source[,source]> filter for cheap targeted re-runs.
  const only = typeof args.flags["only"] === "string" ? String(args.flags["only"]).split(",").map((s) => s.trim()) : null;
  const selected = only ? prompts.filter((p) => only.includes(p.source)) : prompts;
  if (selected.length === 0) {
    console.error(`--only ${only?.join(",")} matched no prompts. Loaded: ${loaded.join(", ")}`);
    process.exit(2);
  }
  console.log(`datasets loaded: ${loaded.join(", ")}${only ? ` (filtered to ${only.join(", ")})` : ""}`);
  const projected = selected.length * JEVD_PER_PROMPT_USD;
  const guard = budgetGuard(projected, config.eval.routingCapUsd);
  console.log(
    `routing eval: ${selected.length} prompts × $${JEVD_PER_PROMPT_USD} = $${projected.toFixed(2)} (cap $${config.eval.routingCapUsd})`,
  );
  if (args.flags["dry-run"] === true) {
    console.log("dry-run: stopping before any calls.");
    return;
  }
  if (!guard.ok) {
    console.error(
      `✗ budget guard: projected $${projected.toFixed(2)} exceeds cap $${config.eval.routingCapUsd}. Nothing was spent.`,
    );
    process.exit(2);
  }

  const sets = new Map<string, SetStats>();
  const statFor = (key: string): SetStats => {
    let s = sets.get(key);
    if (!s) {
      s = { n: 0, within1: 0, exact: 0, timeouts: 0 };
      sets.set(key, s);
    }
    return s;
  };

  let timeouts = 0;
  const rows: PerPrompt[] = [];
  for (const p of selected) {
    const s = statFor(p.source);
    const tokens = estimateTokens(p.messages);
    let cls: Parameters<typeof routeRequest>[0]["classification"] = null;
    try {
      cls = await classify(buildJevState(p.messages), {
        baseUrl: config.jev.baseUrl,
        model: config.jev.model,
        timeoutMs: config.jev.timeoutMs,
        apiKey: jevKey ?? "",
      });
    } catch {
      timeouts++;
      s.timeouts++;
    }
    const outcome = routeRequest({
      classification: cls,
      requestTokens: tokens,
      stickyTier: null,
      tiers: config.tiers,
      config: {
        minConfidence: config.jev.minConfidence,
        trivialNoul: config.jev.trivialNoul,
        escalateOnlyAboveComplexity: config.jev.escalateOnlyAboveComplexity,
        complexityToTier: config.policy.default.complexityToTier,
        overrides: config.policy.overrides,
        perRequestCapUsd: config.cost.perRequestCapUsd,
        stickyEnabled: false, // each prompt independent in eval
        escapeConfidence: config.sticky.escapeConfidence,
      },
    });
    const within = p.expectedTiers.some((t) => Math.abs(t - outcome.tier) <= 1);
    const exact = p.expectedTiers.includes(outcome.tier);
    s.n++;
    if (within) s.within1++;
    if (exact) s.exact++;
    rows.push({
      source: p.source,
      expected: p.expectedTiers,
      tier: outcome.tier,
      reason: outcome.reason,
      category: outcome.category,
      complexity: outcome.complexity,
      confidence: outcome.confidence,
      trivialNoul: cls?.trivialNoul ?? null,
      within,
      exact,
      prompt: p.messages[p.messages.length - 1]!.content.slice(0, 160),
    });
  }

  // Per-prompt audit trail (JSONL) — so failures can be tuned, not just counted.
  mkdirSync(config.dataDir, { recursive: true });
  const auditPath = join(config.dataDir, `eval-routing-${Date.now()}.jsonl`);
  writeFileSync(auditPath, rows.map((r) => JSON.stringify(r)).join("\n") + "\n", "utf8");

  // Report.
  console.log("\n=== routing eval results ===");
  const pad = (v: string, n: number) => v.padEnd(n, " ");
  console.log(pad("source", 16), pad("n", 5), pad("±1 tier", 9), pad("exact", 8), "timeouts");
  let totN = 0;
  let totWithin = 0;
  let totExact = 0;
  for (const [source, s] of [...sets.entries()].sort()) {
    console.log(
      pad(source, 16),
      pad(String(s.n), 5),
      pad(`${((s.within1 / s.n) * 100).toFixed(1)}%`, 9),
      pad(`${((s.exact / s.n) * 100).toFixed(1)}%`, 8),
      String(s.timeouts),
    );
    totN += s.n;
    totWithin += s.within1;
    totExact += s.exact;
  }
  console.log(
    pad("TOTAL", 16),
    pad(String(totN), 5),
    pad(`${((totWithin / totN) * 100).toFixed(1)}%`, 9),
    pad(`${((totExact / totN) * 100).toFixed(1)}%`, 8),
    String(timeouts),
  );
  console.log(`\nEstimated spend: $${(JEVD_PER_PROMPT_USD * totN).toFixed(2)}`);

  // Tier distribution per source — shows under-routing at a glance.
  console.log("\n=== tier distribution (share of each source) ===");
  console.log(pad("source", 16), pad("t0", 7), pad("t1", 7), pad("t2", 7), pad("t3", 7), "mean complexity");
  for (const source of [...sets.keys()].sort()) {
    const rs = rows.filter((r) => r.source === source);
    const share = (t: number) => `${((rs.filter((r) => r.tier === t).length / rs.length) * 100).toFixed(0)}%`;
    const meanC = rs.reduce((a, r) => a + r.complexity, 0) / Math.max(1, rs.length);
    console.log(pad(source, 16), pad(share(0), 7), pad(share(1), 7), pad(share(2), 7), pad(share(3), 7), meanC.toFixed(2));
  }

  // Complexity histogram — the data for choosing tier bands.
  console.log("\n=== Jev complexity histogram ===");
  const buckets = [0.5, 1.0, 1.5, 2.0];
  console.log(pad("source", 16), buckets.map((b) => pad(`<${b}`, 7)).join(""), "max");
  for (const source of [...sets.keys()].sort()) {
    const rs = rows.filter((r) => r.source === source);
    const cells: string[] = [];
    let prev = 0;
    for (const b of buckets) {
      cells.push(pad(String(rs.filter((r) => r.complexity >= prev && r.complexity < b).length), 7));
      prev = b;
    }
    const maxC = Math.max(...rs.map((r) => r.complexity)).toFixed(2);
    console.log(pad(source, 16), cells.join(""), maxC);
  }

  // Failures, with the prompt and Jev's reasoning inputs — the tuning surface.
  const failures = rows.filter((r) => !r.within);
  const trivFailures = rows.filter((r) => r.source === "trivial" && !r.exact);
  if (trivFailures.length > 0) {
    console.log(`\n=== trivial contract violations (${trivFailures.length}) ===`);
    for (const r of trivFailures) {
      console.log(`  tier ${r.tier} | noul ${r.trivialNoul?.toFixed(2)} | conf ${r.confidence.toFixed(2)} | "${r.prompt}"`);
    }
  }
  if (failures.length > 0) {
    console.log(`\n=== worst misroutes (${failures.length} outside ±1; showing up to 8) ===`);
    for (const r of failures.slice(0, 8)) {
      console.log(
        `  ${pad(r.source, 15)} exp [${r.expected.join(",")}] got t${r.tier} | complexity ${r.complexity.toFixed(2)} conf ${r.confidence.toFixed(2)} | "${r.prompt.slice(0, 90)}"`,
      );
    }
  }
  console.log(`\nfull per-prompt audit: ${auditPath}`);

  const triv = sets.get("trivial");
  if (triv && triv.n > 0) {
    const exact0 = (triv.exact / triv.n) * 100;
    console.log(
      `trivial bypass (tier-0 exact): ${exact0.toFixed(1)}% ${exact0 >= 99 ? "✓ contract holds" : "✗ CONTRACT VIOLATION — tune trivialNoul/policy"}`,
    );
  }
}

interface PerPrompt {
  source: string;
  expected: number[];
  tier: number;
  reason: string;
  category: string;
  complexity: number;
  confidence: number;
  trivialNoul: number | null;
  within: boolean;
  exact: boolean;
  prompt: string;
}