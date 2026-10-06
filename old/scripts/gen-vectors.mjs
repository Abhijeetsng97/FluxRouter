// Generator: records cross-engine test vectors from the TS engine.
// Run from repo root:  node old/scripts/gen-vectors.mjs
// Output: old/scripts/vectors.json — recorded truth for Go parity tests.
import { SessionStore } from "../src/session.ts";
import { estimateTokens } from "../src/cost.ts";
import { buildJevState } from "../src/classify.ts";
import { routeRequest, complexityToTier, applyOverrides, stickyEscape } from "../src/policy.ts";
import { defaultConfig } from "../src/config.ts";
import { writeFileSync } from "node:fs";

const out = {};

// --- session ids (catalog item 7) ---
out.sessionIds = [];
const convos = [
  [{ role: "system", content: "sys" }, { role: "user", content: "hello" }],
  [{ role: "user", content: "hi" }],
  [{ role: "system", content: "héllo🎉 sys" }, { role: "user", content: "x" }],
  [{ role: "system", content: "You are a helpful assistant" }, { role: "user", content: "Prove that there are infinitely many primes. Justify every step." }],
];
for (const c of convos) out.sessionIds.push(SessionStore.sessionIdFor(c));

// --- estimateTokens vectors (catalog item 16) ---
out.estimateTokens = [
  estimateTokens([{ content: "hello world" }]),
  estimateTokens([{ content: "héllo🎉" }]),
  estimateTokens([{ content: "a" }, { content: "bb" }]),
  estimateTokens([]),
];

// --- buildJevState vectors (catalog item 8) ---
out.jevStates = [
  buildJevState([{ role: "user", content: "hello" }]),
  buildJevState([{ role: "user", content: "a".repeat(6000) + "MIDDLE" + "b".repeat(6000) }], 2000),
  buildJevState([
    { role: "system", content: "s".repeat(3000) },
    { role: "user", content: "question " + "c".repeat(3000) },
  ]),
];

// --- policy vectors (pure decisions) ---
const cfg = defaultConfig();
const cls = (o) => ({
  category: "other",
  complexity: 0,
  complexityConfidence: 0.9,
  categoryConfidence: 0.9,
  trivialNoul: 0.01,
  jevModel: "jev-test",
  jevUsage: { input_tokens: 100, output_tokens: 10 },
  ...o,
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
const policyCases = [
  { name: "trivial bypass", i: mkInput({ classification: cls({ trivialNoul: 0.97, complexity: 2 }) }) },
  { name: "no bypass below threshold", i: mkInput({ classification: cls({ trivialNoul: 0.5 }) }) },
  { name: "greeting override", i: mkInput({ classification: cls({ category: "greeting_chitchat", complexity: 0.2 }) }) },
  { name: "math override", i: mkInput({ classification: cls({ category: "math", complexity: 1.3 }) }) },
  { name: "low conf escalates", i: mkInput({ classification: cls({ category: "other", complexity: 0.9, categoryConfidence: 0.2, complexityConfidence: 0.3 }) }) },
  { name: "low conf no escalate trivial", i: mkInput({ classification: cls({ complexity: 0.02, categoryConfidence: 0.46, complexityConfidence: 0.46 }) }) },
  { name: "sticky reuse", i: mkInput({ stickyTier: 2, classification: cls({ complexity: 0.1 }) }) },
  { name: "jev unavailable default", i: mkInput({ classification: null }) },
  { name: "jev unavailable fallback tier 2", i: mkInput({ classification: null, fallbackTier: 2 }) },
];
out.policy = [];
for (const { name, i } of policyCases) {
  const o = routeRequest(structuredClone(i));
  out.policy.push({
    name,
    tier: o.tier,
    reason: o.reason,
    category: o.category,
    complexity: o.complexity,
    confidence: o.confidence,
    projectedCostUsd: o.projectedCostUsd,
    notes: o.notes,
  });
}

// context-gate case: mutated config
{
  const cfg2 = defaultConfig();
  cfg2.tiers[0].models[0].ctx = 8000;
  cfg2.tiers[1].models[0].ctx = 8000;
  const o = routeRequest({ ...mkInput({ classification: cls({ complexity: 0.2 }), requestTokens: 50000, tiers: cfg2.tiers }) });
  out.policy.push({ name: "context gate escalates", tier: o.tier, reason: o.reason, notes: o.notes });
}
// cost-guard case: replicate the TS unit test exactly — the MUTATED overrides
// and rates must be passed through the input's config/tiers, not just mutated.
{
  const cfg3 = defaultConfig();
  cfg3.tiers[3].models[0] = { upstream: "ollama", id: "expensive", ctx: 1000000, in: 75, out: 75 };
  cfg3.policy.overrides.push({ category: "tool_planning", tier: 3 });
  const base = mkInput();
  const i = {
    ...base,
    classification: cls({ category: "tool_planning", complexity: 0.3, trivialNoul: 0.1 }),
    requestTokens: 300000,
    stickyTier: null,
    tiers: cfg3.tiers,
    budgetTokensOut: 2000,
    config: { ...base.config, overrides: cfg3.policy.overrides, perRequestCapUsd: cfg3.cost.perRequestCapUsd },
  };
  const o = routeRequest(structuredClone(i));
  out.policy.push({ name: "cost guard downgrades", tier: o.tier, reason: o.reason, notes: o.notes });
}
// jev unavailable fallback honours context gate (flash too small)
{
  const cfg4 = defaultConfig();
  cfg4.tiers[1].models[0].ctx = 4000;
  const o = routeRequest({ ...mkInput({ classification: null, requestTokens: 50000, tiers: cfg4.tiers, fallbackTier: 1 }) });
  out.policy.push({ name: "jev fallback context gate", tier: o.tier, reason: o.reason, notes: o.notes });
}

// --- sticky escape vectors (catalog item 20) ---
out.stickyEscape = [
  stickyEscape(1, cls({ category: "math", complexity: 1.5, categoryConfidence: 0.9, complexityConfidence: 0.9 }), cfg.policy.default.complexityToTier, cfg.policy.overrides, cfg.sticky.escapeConfidence),
  stickyEscape(1, cls({ category: "math", complexity: 1.5, categoryConfidence: 0.3, complexityConfidence: 0.3 }), cfg.policy.default.complexityToTier, cfg.policy.overrides, cfg.sticky.escapeConfidence),
  stickyEscape(2, cls({ category: "other", complexity: 0.1, categoryConfidence: 0.9, complexityConfidence: 0.9 }), cfg.policy.default.complexityToTier, cfg.policy.overrides, cfg.sticky.escapeConfidence),
];

// --- scalar helpers ---
out.complexityToTier = [0, 0.7, 0.9, 1.7, 2.0, 50].map((c) => complexityToTier(c, cfg.policy.default.complexityToTier));
out.applyOverrides = [
  applyOverrides("math", 1.3, cfg.policy.overrides),
  applyOverrides("math", 0.5, cfg.policy.overrides),
  applyOverrides("greeting_chitchat", 0, cfg.policy.overrides),
  applyOverrides("other", 2, cfg.policy.overrides) ?? "null",
];

writeFileSync(new URL("./vectors.json", import.meta.url), JSON.stringify(out, null, 2));
console.log("vectors written to old/scripts/vectors.json");