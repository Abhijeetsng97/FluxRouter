// Generator: writes fixtures/golden.jsonl — one decision per line, recorded
// from the TS engine, consumable by `fluxrouter parity`.
// Run from repo root: node old/scripts/gen-fixtures.mjs
import { routeRequest } from "../src/policy.ts";
import { defaultConfig } from "../src/config.ts";
import { mkdirSync, writeFileSync } from "node:fs";

const cfg = defaultConfig();
const cls = (o) => ({
  category: "other", complexity: 0, complexityConfidence: 0.9, categoryConfidence: 0.9,
  trivialNoul: 0.01, jevModel: "jev-test", jevUsage: { input_tokens: 100, output_tokens: 10 }, ...o,
});
const mkInput = (o = {}) => ({
  classification: cls(),
  requestTokens: 1000,
  stickyTier: null,
  tiers: cfg.tiers,
  fallbackTier: undefined,
  budgetTokensOut: undefined,
  config: {
    minConfidence: cfg.jev.minConfidence,
    trivialNoul: cfg.jev.trivialNoul,
    escalateOnlyAboveComplexity: cfg.jev.escalateOnlyAboveComplexity,
    complexityToTier: cfg.policy.default.complexityToTier,
    overrides: cfg.policy.overrides,
    perRequestCapUsd: cfg.cost.perRequestCapUsd,
    stickyEnabled: cfg.sticky.enabled,
    escapeConfidence: cfg.sticky.escapeConfidence,
  },
  ...o,
});

const fixtures = [];

// --- sweep: complexity x category through the pure policy engine ---
const categories = ["other", "math", "code_debug", "greeting_chitchat", "summarize", "tool_planning"];
const complexities = [0, 0.1, 0.5, 0.79, 0.8, 0.9, 1.0, 1.2, 1.59, 1.6, 1.7, 1.99, 2.0, 2.5];
const nouls = [0.01, 0.5, 0.85, 0.86, 0.99];
let n = 0;
for (const cat of categories) {
  for (const c of complexities) {
    for (const noul of nouls) {
      // deterministic confidence sweep: high / low / boundary
      for (const conf of [0.99, 0.46, 0.3]) {
        const input = mkInput({
          classification: cls({ category: cat, complexity: c, trivialNoul: noul, categoryConfidence: conf, complexityConfidence: conf }),
        });
        const o = routeRequest(structuredClone(input));
        fixtures.push({
          name: `sweep-${cat}-c${c}-n${noul}-conf${conf}`,
          messages: [{ role: "user", content: `sweep ${cat} ${c}` }],
          classification: input.classification,
          stickyTier: null,
          requestTokens: input.requestTokens,
          fallbackTier: null,
          budgetTokensOut: null,
          expected: {
            tier: o.tier, reason: o.reason, category: o.category,
            complexity: o.complexity, confidence: o.confidence,
            projectedCostUsd: o.projectedCostUsd, notes: o.notes,
          },
        });
        n++;
      }
    }
  }
}

// --- sticky tier reuse ---
for (const pinned of [0, 1, 2, 3]) {
  const o = routeRequest(mkInput({ stickyTier: pinned, classification: cls({ complexity: 0.1 }) }));
  fixtures.push({
    name: `sticky-pin-${pinned}`,
    messages: [{ role: "user", content: "sticky" }],
    classification: cls({ complexity: 0.1 }),
    stickyTier: pinned,
    requestTokens: 1000,
    expected: { tier: o.tier, reason: o.reason, category: o.category, complexity: o.complexity, confidence: o.confidence, projectedCostUsd: o.projectedCostUsd, notes: o.notes },
  });
}

// --- jev unavailable fallback ladder ---
for (const ft of [0, 1, 2, 3]) {
  const o = routeRequest(mkInput({ classification: null, fallbackTier: ft }));
  fixtures.push({
    name: `jev-fallback-${ft}`,
    messages: [{ role: "user", content: "x" }],
    classification: null,
    stickyTier: null,
    requestTokens: 1000,
    fallbackTier: ft,
    expected: { tier: o.tier, reason: o.reason, category: o.category, complexity: o.complexity, confidence: o.confidence, projectedCostUsd: o.projectedCostUsd, notes: o.notes },
  });
}

// --- context gate sweeps: shrink tiers to force gate escalations ---
const ctxCases = [
  { shrink: [0], requestTokens: 50000 },
  { shrink: [0, 1], requestTokens: 50000 },
  { shrink: [0, 1, 2], requestTokens: 50000 },
  { shrink: [0, 1, 2, 3], requestTokens: 50000 }, // nothing fits -> keep tier note
];
for (const cc of ctxCases) {
  const cfg2 = defaultConfig();
  for (const ti of cc.shrink) cfg2.tiers[ti].models[0].ctx = 8000;
  const input = { ...mkInput({ classification: cls({ complexity: 0.2 }), requestTokens: cc.requestTokens }), tiers: cfg2.tiers };
  const o = routeRequest(structuredClone(input));
  fixtures.push({
    name: `ctx-gate-shrink-${cc.shrink.join("_")}-rt${cc.requestTokens}`,
    messages: [{ role: "user", content: "big" }],
    classification: input.classification,
    stickyTier: null,
    requestTokens: cc.requestTokens,
    configPatch: { tiers: cc.shrink.map((ti) => ({ index: ti, model: { ctx: 8000 } })) },
    expected: { tier: o.tier, reason: o.reason, category: o.category, complexity: o.complexity, confidence: o.confidence, projectedCostUsd: o.projectedCostUsd, notes: o.notes },
  });
}

mkdirSync(new URL("../../fixtures/", import.meta.url), { recursive: true });
writeFileSync(new URL("../../fixtures/golden.jsonl", import.meta.url),
  fixtures.map((f) => JSON.stringify(f)).join("\n") + "\n");
console.log(`wrote ${fixtures.length} fixtures to fixtures/golden.jsonl`);