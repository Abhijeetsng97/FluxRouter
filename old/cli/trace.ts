// `flux trace <prompt>` — run the REAL classify → policy pipeline on one prompt and print
// every intermediate step: the exact state sent to Jev, its typed answers, the context
// gate, each policy rule, and the final tier/model/cost.
//
// Costs ~$0.001 when it calls Jev. Use --offline with --category/--complexity/etc. to
// explore the policy engine with no network and no spend.

import { readFileSync } from "node:fs";
import { loadDotEnv } from "../src/env.ts";
import { loadConfig } from "../src/config.ts";
import { classify, buildJevState, ROUTE_QUESTIONS } from "../src/classify.ts";
import {
  applyOverrides,
  cheapestFittingTier,
  complexityToTier,
  contextGate,
  findTier,
  fitFallbackTier,
  routeRequest,
} from "../src/policy.ts";
import { estimateTokens, projectedCost } from "../src/cost.ts";
import type { ClassificationResult } from "../src/types.ts";

interface TraceArgs {
  _: string[];
  flags: Record<string, string | boolean>;
}

const line = (s = "") => console.log(s);
const rule = (title: string) => line(`\n${"─".repeat(72)}\n${title}\n${"─".repeat(72)}`);

export async function runTrace(args: TraceArgs): Promise<void> {
  loadDotEnv();
  const { config, errors } = loadConfig("fluxrouter.config.json");
  if (errors.length > 0) {
    console.error("config errors:", errors.join("; "));
    process.exit(2);
  }

  let prompt = args._.join(" ").trim();
  if (!prompt && !process.stdin.isTTY) prompt = readFileSync(0, "utf8").trim();
  if (!prompt) {
    console.error('usage: flux trace "your prompt here"  [--offline --complexity 1.2 --category math]');
    process.exit(2);
  }

  const offline = args.flags["offline"] === true || !process.env["TYPESAFE_API_KEY"];
  const messages = [{ role: "user" as const, content: prompt }];
  const tokens = estimateTokens(messages);

  line(`PROMPT: ${prompt.length > 300 ? prompt.slice(0, 300) + "…" : prompt}`);
  line(`estimated request tokens: ${tokens}  (chars/4 heuristic)`);

  // ── Step 1: what Jev is asked ────────────────────────────────────────────────
  rule("STEP 1 — Jev classification (src/classify.ts: classify())");
  const state = buildJevState(messages);
  line(`state sent to Jev (${state.length} chars, head+tail excerpt):`);
  line(`  ${state.slice(0, 400).replace(/\n/g, "\n  ")}`);
  line();
  line("questions asked in the SAME call (free fan-out):");
  line(`  category (choice): ${Object.keys(ROUTE_QUESTIONS.category.criteria).join(", ")}`);
  line(`  complexity (score): 0="${ROUTE_QUESTIONS.complexity.criteria[0].slice(0, 42)}…" … 2="…"`);
  line(`  is_trivial (noul): ${ROUTE_QUESTIONS.is_trivial.instructions.slice(0, 70)}…`);

  let cls: ClassificationResult | null = null;
  let jevMs = 0;
  if (offline) {
    cls = {
      category: String(args.flags["category"] ?? "other"),
      complexity: Number(args.flags["complexity"] ?? 0.5),
      complexityConfidence: Number(args.flags["confidence"] ?? 0.9),
      categoryConfidence: Number(args.flags["confidence"] ?? 0.9),
      trivialNoul: Number(args.flags["noul"] ?? 0.05),
      jevModel: "offline-supplied",
      jevUsage: { input_tokens: 0, output_tokens: 0 },
    };
    line("\n(--offline: using supplied values instead of calling Jev)");
  } else {
    const t0 = Date.now();
    try {
      cls = await classify(state, {
        baseUrl: config.jev.baseUrl,
        model: config.jev.model,
        timeoutMs: config.jev.timeoutMs,
        apiKey: process.env["TYPESAFE_API_KEY"]!,
      });
    } catch (e) {
      line(`\n✗ Jev failed: ${(e as Error).message}`);
      line("  → router would use the fail-safe fallback tier (see policy step 2)");
    }
    jevMs = Date.now() - t0;
  }

  if (cls) {
    line();
    line("Jev's typed answers:");
    line(`  category    = ${cls.category}            (confidence ${cls.categoryConfidence.toFixed(2)})`);
    line(`  complexity  = ${cls.complexity.toFixed(2)}                 (confidence ${cls.complexityConfidence.toFixed(2)})`);
    line(`  is_trivial  = ${cls.trivialNoul.toFixed(2)}   noul (calibrated yes/no probability)`);
    line(`  model=${cls.jevModel}  usage=${cls.jevUsage.input_tokens} in / ${cls.jevUsage.output_tokens} out  (${jevMs}ms)`);
  }

  // ── Step 2: context gate ─────────────────────────────────────────────────────
  rule("STEP 2 — Context gate (src/policy.ts: contextGate())");
  const viable = contextGate(config.tiers, tokens);
  for (const t of config.tiers) {
    const m = t.models[0]!;
    const fits = viable.some((v) => v.id === t.id);
    line(`  tier ${t.id} ${t.name.padEnd(9)} ctx ${String(m.ctx).padStart(9)}  ${fits ? "✓ fits" : "✗ request too large"}`);
  }

  // ── Step 3+: policy rules ────────────────────────────────────────────────────
  rule("STEP 3 — Policy rules, in order (src/policy.ts: routeRequest())");
  const confidence = cls ? Math.min(cls.categoryConfidence, cls.complexityConfidence) : 0;
  if (cls) {
    const triv = cls.trivialNoul > config.jev.trivialNoul;
    line(`  3a trivial bypass?   noul ${cls.trivialNoul.toFixed(2)} > ${config.jev.trivialNoul}?  ${triv ? "YES → tier 0" : "no"}`);
    const ov = applyOverrides(cls.category, cls.complexity, config.policy.overrides);
    line(`  3b category override? ${cls.category} → ${ov === null ? "no override" : `tier ${ov}`}`);
    const bandTier = complexityToTier(cls.complexity, config.policy.default.complexityToTier);
    line(`  3c complexity bands? ${cls.complexity.toFixed(2)} → tier ${bandTier}  (bands ${JSON.stringify(config.policy.default.complexityToTier)})`);
    const esc = confidence < config.jev.minConfidence && cls.complexity >= config.jev.escalateOnlyAboveComplexity;
    line(`  3d low-confidence?   conf ${confidence.toFixed(2)} < ${config.jev.minConfidence} AND complexity ≥ ${config.jev.escalateOnlyAboveComplexity}?  ${esc ? "YES → tier 2" : "no"}`);
    const outTier = findTier(config.tiers, ov ?? bandTier);
    if (outTier) {
      const m = outTier.models[0]!;
      const proj = projectedCost(tokens, 2000, { in: m.in, out: m.out });
      line(`  3e cost guard?       projected $${proj.toFixed(4)} (${tokens} in × 2000 out) > cap $${config.cost.perRequestCapUsd}?  ${proj > config.cost.perRequestCapUsd ? "YES → downgrade" : "no"}`);
    }
  } else {
    line(`  Jev unavailable → fallback tier ${config.jev.fallbackTier} (context-gated), reason jev_unavailable_fallback`);
  }

  // ── Final: the REAL policy engine decides ────────────────────────────────────
  const outcome = routeRequest({
    classification: cls,
    requestTokens: tokens,
    stickyTier: null,
    tiers: config.tiers,
    fallbackTier: config.jev.fallbackTier,
    config: {
      minConfidence: config.jev.minConfidence,
      trivialNoul: config.jev.trivialNoul,
      escalateOnlyAboveComplexity: config.jev.escalateOnlyAboveComplexity,
      complexityToTier: config.policy.default.complexityToTier,
      overrides: config.policy.overrides,
      perRequestCapUsd: config.cost.perRequestCapUsd,
      stickyEnabled: config.sticky.enabled,
      escapeConfidence: config.sticky.escapeConfidence,
    },
  });
  const tier = findTier(config.tiers, outcome.tier)!;
  const model = tier.models[0]!;

  rule("RESULT — what FluxRouter would do");
  line(`  tier        : ${outcome.tier} (${tier.name})`);
  line(`  model       : ${model.upstream}:${model.id}`);
  line(`  reason      : ${outcome.reason}`);
  line(`  category    : ${outcome.category}`);
  line(`  complexity  : ${outcome.complexity.toFixed(2)}   confidence ${outcome.confidence.toFixed(2)}`);
  line(`  projected $ : ${outcome.projectedCostUsd.toFixed(6)} for this request`);
  if (outcome.notes.length > 0) {
    line(`  rule notes  :`);
    for (const n of outcome.notes) line(`    - ${n}`);
  }
  const frontier = config.tiers.reduce((a, t) => (t.id > a.id ? t : a), config.tiers[0]!);
  const fm = frontier.models[0]!;
  const frontierCost = projectedCost(tokens, 2000, { in: fm.in, out: fm.out });
  const savedPct = frontierCost > 0 ? `${(100 - (outcome.projectedCostUsd / frontierCost) * 100).toFixed(1)}% saved` : "n/a";
  line(`  vs frontier : ${frontier.name} would be ~$${frontierCost.toFixed(6)} (${savedPct})`);
  line();

  // Reference: which tier each rule would give (helps tune bands).
  if (cls) {
    const cheapest = cheapestFittingTier(config.tiers, tokens);
    const fb = fitFallbackTier(config.tiers, config.jev.fallbackTier, tokens);
    line(`reference: cheapest fitting tier = ${cheapest?.id}, Jev-fallback tier = ${fb}`);
  }
}