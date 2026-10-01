// Unit + integration tests for FluxRouter core logic. Run: npm test (node --test).

import test from "node:test";
import assert from "node:assert/strict";
import { defaultConfig, validateConfig, loadConfig, mergeConfig } from "../src/config.ts";
import { estimateTokens, projectedCost, budgetGuard } from "../src/cost.ts";
import { complexityToTier, applyOverrides, routeRequest, contextGate, cheapestFittingTier, stickyEscape, type PolicyInput } from "../src/policy.ts";
import { SessionStore } from "../src/session.ts";
import { parseJevResponse, buildJevState, JevTimeoutError } from "../src/classify.ts";
import { extractUsage, costFromUsage } from "../src/router.ts";
import type { ClassificationResult, JevResponse, RouteCard, TierModel } from "../src/types.ts";

// -- helpers -----------------------------------------------------------------

function cls(over: Partial<ClassificationResult> = {}): ClassificationResult {
  return {
    category: "other",
    complexity: 0,
    complexityConfidence: 0.9,
    categoryConfidence: 0.9,
    trivialNoul: 0.01,
    jevModel: "jev-test",
    jevUsage: { input_tokens: 100, output_tokens: 10 },
    ...over,
  };
}

function policyInput(over: Partial<PolicyInput> = {}): PolicyInput {
  const cfg = defaultConfig();
  return {
    classification: cls(),
    requestTokens: 1000,
    // NB: `tiers` and the getters intentionally re-read from `cfg` lazily via closure,
    // but Partial spread replaces `config` wholesale when a test passes its own —
    // tests that mutate cfg.policy.pass it explicitly via the `config` key.
    stickyTier: null,
    tiers: cfg.tiers,
    config: {
      minConfidence: cfg.jev.minConfidence,
      trivialNoul: cfg.jev.trivialNoul,
      escalateOnlyAboveComplexity: cfg.jev.escalateOnlyAboveComplexity,
      get complexityToTier() {
        return cfg.policy.default.complexityToTier;
      },
      get overrides() {
        return cfg.policy.overrides;
      },
      perRequestCapUsd: cfg.cost.perRequestCapUsd,
      stickyEnabled: cfg.sticky.enabled,
      escapeConfidence: cfg.sticky.escapeConfidence,
    },
    ...over,
  };
}

const model = (over: Partial<TierModel> = {}): TierModel => ({
  upstream: "ollama",
  id: "m",
  ctx: 1_000_000,
  in: 0.1,
  out: 1,
  ...over,
});

// -- config -------------------------------------------------------------------

test("default config is valid", () => {
  const errors = validateConfig(defaultConfig());
  assert.deepEqual(errors, []);
});

test("validate catches bad tier and rates", () => {
  const cfg = defaultConfig();
  cfg.tiers[0]!.models[0]!.in = -1;
  cfg.policy.overrides.push({ category: "bogus" as never, tier: 0 });
  const errors = validateConfig(cfg);
  assert.ok(errors.length >= 2);
});

test("mergeConfig: user override wins, defaults preserved", () => {
  const merged = mergeConfig({ cost: { perRequestCapUsd: 5 }, server: { port: 9999 } });
  assert.equal(merged.cost.perRequestCapUsd, 5);
  assert.equal(merged.server.port, 9999);
  assert.equal(merged.jev.model, "jev-1.13.0");
  assert.equal(merged.tiers.length, 4);
});

test("loadConfig with missing file reports error", () => {
  const { errors } = loadConfig("does-not-exist.json");
  assert.equal(errors.length, 1);
});

test("loadConfig missing file without path uses defaults", () => {
  const { config, errors } = loadConfig();
  assert.deepEqual(errors, []);
  assert.equal(config.jev.model, "jev-1.13.0");
});

// -- token estimation & cost ---------------------------------------------------

test("estimateTokens: message-aware chars/4", () => {
  assert.equal(estimateTokens([{ content: "hello world" }]), Math.ceil((11 + 8) / 4));
  assert.equal(estimateTokens([]), 0);
});

test("projectedCost math", () => {
  // 1000 in @ 0.10/M + 100 out @ 1/M
  const usd = projectedCost(1000, 100, { in: 0.1, out: 1 });
  assert.equal(usd, 1000 / 1e6 * 0.1 + 100 / 1e6 * 1);
});

test("budgetGuard refuses over-cap", () => {
  assert.equal(budgetGuard(1.5, 1).ok, false);
  assert.equal(budgetGuard(0.99, 1).ok, true);
});

// -- policy ---------------------------------------------------------------------

test("complexityToTier maps bands", () => {
  const t = defaultConfig().policy.default.complexityToTier;
  assert.equal(complexityToTier(0, t), 0);
  assert.equal(complexityToTier(0.7, t), 0);
  assert.equal(complexityToTier(0.9, t), 1);
  assert.equal(complexityToTier(1.7, t), 2);
  assert.equal(complexityToTier(2.0, t), 3, "Jev max (2.0) reaches frontier");
  assert.equal(complexityToTier(50, t), 3);
});

test("every tier is reachable from the score alone", () => {
  // Regression guard: tier 3 must be selectable without relying on an override.
  const t = defaultConfig().policy.default.complexityToTier;
  const reached = new Set([0, 0.5, 1.0, 1.8, 2.0].map((c) => complexityToTier(c, t)));
  assert.deepEqual([...reached].sort(), [0, 1, 2, 3]);
});

test("applyOverrides matches category with minComplexity", () => {
  const ovs = defaultConfig().policy.overrides;
  assert.equal(applyOverrides("math", 1.3, ovs), 2);
  assert.equal(applyOverrides("math", 0.5, ovs), null);
  assert.equal(applyOverrides("greeting_chitchat", 0, ovs), 0);
  assert.equal(applyOverrides("other", 2, ovs), null);
});

test("trivial bypass: noul above threshold → tier 0 with reason", () => {
  const out = routeRequest(policyInput({ classification: cls({ trivialNoul: 0.97, complexity: 2 }) }));
  assert.equal(out.tier, 0);
  assert.equal(out.reason, "trivial_bypass");
});

test("no trivial bypass below threshold", () => {
  const out = routeRequest(policyInput({ classification: cls({ trivialNoul: 0.5, complexity: 0 }) }));
  assert.notEqual(out.reason, "trivial_bypass");
});

test("greeting category override → tier 0", () => {
  const out = routeRequest(policyInput({ classification: cls({ category: "greeting_chitchat", complexity: 0.2 }) }));
  assert.equal(out.tier, 0);
});

test("hard math override → tier 2", () => {
  const out = routeRequest(policyInput({ classification: cls({ category: "math", complexity: 1.3 }) }));
  assert.equal(out.tier, 2);
});

test("low confidence escalates to tier 2 when complexity warrants caution", () => {
  const out = routeRequest(
    policyInput({ classification: cls({ category: "other", complexity: 0.9, categoryConfidence: 0.2, complexityConfidence: 0.3 }) }),
  );
  assert.equal(out.tier, 2);
  assert.equal(out.reason, "low_confidence_escalation");
});

test("low confidence does NOT escalate a trivially-simple request", () => {
  // Regression: "go" scored complexity 0.02 with confidence 0.46 and was wrongly
  // escalated to tier 2, breaking the tier-0 trivial contract.
  const out = routeRequest(
    policyInput({ classification: cls({ category: "other", complexity: 0.02, categoryConfidence: 0.46, complexityConfidence: 0.46 }) }),
  );
  assert.notEqual(out.reason, "low_confidence_escalation");
  assert.equal(out.tier, 0);
});

test("context gate escalates when tier model can't fit", () => {
  const cfg = defaultConfig();
  // shrink tier 0/1 ctx so a 300k request must go to tier 2+
  cfg.tiers[0]!.models[0]!.ctx = 8_000;
  cfg.tiers[1]!.models[0]!.ctx = 8_000;
  const out = routeRequest(
    policyInput({
      classification: cls({ category: "other", complexity: 0.2 }), // → tier 0 normally
      requestTokens: 50_000,
      tiers: cfg.tiers,
    }),
  );
  assert.equal(out.reason, "context_gate");
  assert.equal(out.tier, 2);
});

test("cost guard downgrades when projected cost exceeds cap", () => {
  const cfg = defaultConfig();
  // extreme frontier rates
  cfg.tiers[3]!.models[0] = { upstream: "ollama", id: "expensive", ctx: 1_000_000, in: 75, out: 75 };
  // category override forces tier 3 regardless of score
  cfg.policy.overrides.push({ category: "tool_planning", tier: 3 });
  const base = policyInput();
  const out = routeRequest({
    classification: cls({ category: "tool_planning", complexity: 0.3, trivialNoul: 0.1 }), // override → tier 3
    requestTokens: 300_000,
    stickyTier: null,
    tiers: cfg.tiers,
    config: {
      ...base.config,
      overrides: cfg.policy.overrides, // include the pushed override
      perRequestCapUsd: cfg.cost.perRequestCapUsd,
    },
    budgetTokensOut: 2000,
  });
  assert.equal(out.reason, "cost_guard");
  assert.ok(out.tier < 3, "downgraded below frontier");
});

test("jev unavailable → fail-safe fallback tier (flash by default)", () => {
  const out = routeRequest(policyInput({ classification: null }));
  assert.equal(out.reason, "jev_unavailable_fallback");
  assert.equal(out.tier, 1, "defaults to flash, not the cheapest tier");
});

test("jev unavailable with fallbackTier override → uses it", () => {
  const out = routeRequest(policyInput({ classification: null, fallbackTier: 2 }));
  assert.equal(out.tier, 2);
  assert.equal(out.reason, "jev_unavailable_fallback");
});

test("jev unavailable fallback honours context gate", () => {
  const cfg = defaultConfig();
  cfg.tiers[1]!.models[0]!.ctx = 4_000; // flash too small for the request
  const out = routeRequest(
    policyInput({ classification: null, tiers: cfg.tiers, requestTokens: 50_000, fallbackTier: 1 }),
  );
  assert.equal(out.tier, 2, "moves up to a tier that fits");
});

test("sticky request reuses pinned tier, skipping classification", () => {
  const out = routeRequest(policyInput({ stickyTier: 2, classification: cls({ complexity: 0.1 }) }));
  assert.equal(out.tier, 2);
  assert.equal(out.reason, "sticky");
});

test("stickyEscape upgrades when clearly harder tier with confidence", () => {
  const cfg = defaultConfig();
  const esc = stickyEscape(
    1,
    cls({ category: "math", complexity: 1.5, categoryConfidence: 0.9, complexityConfidence: 0.9 }),
    cfg.policy.default.complexityToTier,
    cfg.policy.overrides,
    cfg.sticky.escapeConfidence,
  );
  assert.equal(esc, 2);
});

test("stickyEscape stays pinned on low confidence", () => {
  const cfg = defaultConfig();
  const esc = stickyEscape(
    1,
    cls({ category: "math", complexity: 1.5, categoryConfidence: 0.3, complexityConfidence: 0.3 }),
    cfg.policy.default.complexityToTier,
    cfg.policy.overrides,
    cfg.sticky.escapeConfidence,
  );
  assert.equal(esc, null);
});

test("cheapestFittingTier picks lowest tier that fits", () => {
  const cfg = defaultConfig();
  cfg.tiers[0]!.models[0]!.ctx = 1000;
  const t = cheapestFittingTier(cfg.tiers, 5000);
  assert.equal(t!.id, 1);
});

test("contextGate filters small windows", () => {
  const cfg = defaultConfig();
  cfg.tiers[0]!.models[0]!.ctx = 1000;
  cfg.tiers[1]!.models[0]!.ctx = 4000;
  const viable = contextGate(cfg.tiers, 5000);
  assert.deepEqual(viable.map((t) => t.id), [2, 3]);
});

// -- sessions ---------------------------------------------------------------------

test("session id is stable across calls with same first user message", () => {
  const a = SessionStore.sessionIdFor([{ role: "system", content: "sys" }, { role: "user", content: "hello" }]);
  const b = SessionStore.sessionIdFor([{ role: "system", content: "sys" }, { role: "user", content: "hello" }]);
  assert.equal(a, b);
  const c = SessionStore.sessionIdFor([{ role: "system", content: "sys" }, { role: "user", content: "different" }]);
  assert.notEqual(a, c);
});

test("sticky store pin/get/rePin", () => {
  const store = new SessionStore();
  const id = "sess1";
  const have = store.get(id);
  assert.equal(have, undefined);
  store.pin(id, 0, "nano", "ollama");
  assert.equal(store.get(id)?.tier, 0);
  store.rePin(id, 2, "mid", "ollama");
  assert.equal(store.get(id)?.tier, 2);
  store.dispose();
});

// -- Jev response parsing -------------------------------------------------------------

test("parseJevResponse full shape", () => {
  const json: JevResponse = {
    model: "jev-1.13.0",
    answers: {
      category: { type: "choice", choice: "math", confidence: 0.9, probabilities: { math: 0.9 } },
      complexity: { type: "score", score: 1.3, confidence: 0.8 },
      is_trivial: { type: "noul", noul: 0.05 },
    },
    usage: { input_tokens: 200, output_tokens: 30 },
  };
  const r = parseJevResponse(json);
  assert.equal(r.category, "math");
  assert.equal(Math.abs(r.complexity - 1.3) < 1e-9, true);
  assert.equal(r.trivialNoul, 0.05);
  assert.equal(Math.min(r.categoryConfidence, r.complexityConfidence), 0.8);
});

test("parseJevResponse throws on missing answers", () => {
  assert.throws(
    () => parseJevResponse({ model: "x", answers: {}, usage: { input_tokens: 0, output_tokens: 0 } }),
    /missing/,
  );
});

test("buildJevState head+tail for long last message", () => {
  // 6000+6000 chars is far beyond the 2000-char excerpt: head+tail kept, middle dropped by design.
  const long = "a".repeat(6000) + "MIDDLE" + "b".repeat(6000);
  const s = buildJevState([{ role: "user", content: long }], 2000);
  assert.ok(s.startsWith("[LAST]"));
  assert.ok(s.includes("…[truncated]…"), "long messages show a truncation marker");
  assert.ok(!s.includes("MIDDLE") || s.length < 4400, "excerpt stays bounded");
  assert.ok(s.length < 4400);
});

// -- router helpers ---------------------------------------------------------------------

test("extractUsage from OpenAI-shaped response", () => {
  assert.deepEqual(
    extractUsage({ usage: { prompt_tokens: 120, completion_tokens: 40, prompt_tokens_details: { cached_tokens: 10 } } }),
    { input: 120, output: 40, cachedInput: 10 },
  );
  assert.deepEqual(extractUsage({}), { input: 0, output: 0, cachedInput: 0 });
});

test("costFromUsage with cached tier", () => {
  const m = model({ in: 1, out: 2, cachedIn: 0.1 });
  const c = costFromUsage({ input: 1_000_000, output: 1_000_000, cachedInput: 500_000 }, m);
  // 0.5M normal in + 0.5M cached in + 1M out
  assert.equal(c, 0.5 + 0.05 + 2);
});

// -- env loading ------------------------------------------------------------------

test("loadDotEnv: file fills missing keys, real env wins", async () => {
  const { loadDotEnv } = await import("../src/env.ts");
  const { writeFileSync, unlinkSync } = await import("node:fs");
  const tmpA = ".tmp-test-a.env";
  const tmpB = ".tmp-test-b.env";
  writeFileSync(tmpA, "FLUX_TEST_LOW=from_a\nFLUX_TEST_SHARED=from_a\nFLUX_TEST_REAL=from_a\n");
  writeFileSync(tmpB, "FLUX_TEST_HIGH=from_b\nFLUX_TEST_SHARED=from_b\nFLUX_TEST_REAL=from_b\n");
  process.env["FLUX_TEST_REAL"] = "from_environment"; // simulate a real env var
  delete process.env["FLUX_TEST_LOW"];
  delete process.env["FLUX_TEST_HIGH"];
  delete process.env["FLUX_TEST_SHARED"];

  try {
    // Explicit path replaces the default candidates.
    const res = loadDotEnv(tmpA);
    assert.deepEqual(res.loaded, [tmpA]);
    assert.equal(process.env["FLUX_TEST_LOW"], "from_a");
    assert.equal(process.env["FLUX_TEST_REAL"], "from_environment", "real env is never overridden");
    // Loading another file must not override values set by the first.
    loadDotEnv(tmpB);
    assert.equal(process.env["FLUX_TEST_SHARED"], "from_a", "first-loaded file wins");
    assert.equal(process.env["FLUX_TEST_HIGH"], "from_b", "later file fills only missing keys");
  } finally {
    for (const k of ["FLUX_TEST_LOW", "FLUX_TEST_HIGH", "FLUX_TEST_SHARED", "FLUX_TEST_REAL"]) {
      delete process.env[k];
    }
    try { unlinkSync(tmpA); } catch { /* ignore */ }
    try { unlinkSync(tmpB); } catch { /* ignore */ }
  }
});

test("loadDotEnv: missing file is a no-op", async () => {
  const { loadDotEnv } = await import("../src/env.ts");
  const res = loadDotEnv("definitely-not-a-file.env");
  assert.deepEqual(res.loaded, []);
});

test("buildModelsResponse: alias only by default", async () => {
  const { buildModelsResponse } = await import("../src/models.ts");
  const cfg = defaultConfig();
  const res = buildModelsResponse(cfg);
  assert.deepEqual(res.data.map((m) => m.id), ["flux"]);
  assert.equal(res.object, "list");
});

test("buildModelsResponse: listTierModels exposes tiers read-only", async () => {
  const { buildModelsResponse } = await import("../src/models.ts");
  const cfg = defaultConfig();
  cfg.server.listTierModels = true;
  const res = buildModelsResponse(cfg);
  const ids = res.data.map((m) => m.id);
  assert.ok(ids.includes("flux"));
  assert.ok(ids.includes("nemotron-3-nano:30b"));
  assert.ok(ids.includes("kimi-k3"));
  assert.equal(res.data.length, 5, "alias + 4 tier models");
  const nano = res.data.find((m) => m.id === "nemotron-3-nano:30b")!;
  assert.equal(nano.x_flux_tier, 0);
  assert.equal(nano.x_flux_upstream, "ollama");
});

// -- sticky integration (mocked Jev) --------------------------------------------

test("sticky: first turn pins a tier, second turn reuses it", async () => {
  const { RouterService } = await import("../src/router.ts");
  const { Metrics } = await import("../src/metrics.ts");
  const cfg = defaultConfig();
  const sessions = new SessionStore();
  const jevJson = (complexity: number, conf = 0.9, noul = 0.01) =>
    JSON.stringify({
      model: "jev-1.13.0",
      answers: {
        category: { type: "choice", choice: "other", confidence: conf },
        complexity: { type: "score", score: complexity, confidence: conf },
        is_trivial: { type: "noul", noul },
      },
      usage: { input_tokens: 10, output_tokens: 5 },
    });
  const origFetch = globalThis.fetch;
  globalThis.fetch = (async () => new Response(jevJson(0.2), { status: 200 })) as typeof fetch;
  try {
    const svc = new RouterService({
      config: cfg,
      upstreams: { ollama: { baseUrl: "http://x", apiKey: "k" }, openrouter: { baseUrl: "http://x", apiKey: "k" } },
      cardLog: { log() {}, close() {}, aggregate: () => [], totals: () => ({}) } as never,
      metrics: new Metrics(),
      sessions,
    });
    const first = [{ role: "user", content: "hello there" }];
    const d1 = await svc.decide({ messages: first, stream: false });
    assert.equal(d1.reason, "policy", "first turn routes by policy");
    assert.equal(sessions.size, 1, "first turn pins the session");

    const second = [...first, { role: "assistant", content: "hi" }, { role: "user", content: "what's 2+2?" }];
    const d2 = await svc.decide({ messages: second, stream: false });
    assert.equal(d2.reason, "sticky", "second turn reuses the pinned tier");
    assert.equal(d2.tier, d1.tier);
    svc.dispose();
  } finally {
    globalThis.fetch = origFetch;
    sessions.dispose();
  }
});

test("sticky: upward escape re-pins when a later turn is clearly harder", async () => {
  const { RouterService } = await import("../src/router.ts");
  const { Metrics } = await import("../src/metrics.ts");
  const cfg = defaultConfig();
  const sessions = new SessionStore();
  const jevJson = (complexity: number, noul = 0.01) =>
    JSON.stringify({
      model: "jev-1.13.0",
      answers: {
        category: { type: "choice", choice: "other", confidence: 0.95 },
        complexity: { type: "score", score: complexity, confidence: 0.95 },
        is_trivial: { type: "noul", noul },
      },
      usage: { input_tokens: 10, output_tokens: 5 },
    });
  // First call easy (pins tier 0), second call hard (should escape up).
  let calls = 0;
  const origFetch = globalThis.fetch;
  globalThis.fetch = (async () =>
    new Response(calls++ === 0 ? jevJson(0.1) : jevJson(1.9), { status: 200 })) as typeof fetch;
  try {
    const svc = new RouterService({
      config: cfg,
      upstreams: { ollama: { baseUrl: "http://x", apiKey: "k" }, openrouter: { baseUrl: "http://x", apiKey: "k" } },
      cardLog: { log() {}, close() {}, aggregate: () => [], totals: () => ({}) } as never,
      metrics: new Metrics(),
      sessions,
    });
    const first = [{ role: "user", content: "hi" }];
    const d1 = await svc.decide({ messages: first, stream: false });
    assert.equal(d1.tier, 0);
    const second = [...first, { role: "assistant", content: "hello" }, { role: "user", content: "prove P=NP" }];
    const d2 = await svc.decide({ messages: second, stream: false });
    assert.equal(d2.reason, "sticky_escape_up", "escape must not be dead code");
    assert.ok(d2.tier > d1.tier, "escalated to a higher tier");
    svc.dispose();
  } finally {
    globalThis.fetch = origFetch;
    sessions.dispose();
  }
});

// -- route card shape sanity ----------------------------------------------------------

test("route card serializes cleanly", () => {
  const card: RouteCard = {
    ts: "2026-09-30T00:00:00.000Z",
    sessionId: "abc",
    tier: 1,
    model: "glm-5.3-flash",
    upstream: "ollama",
    reason: "policy",
    category: "other",
    complexity: 0.5,
    confidence: 0.9,
    requestTokensEst: 100,
    usage: { input: 100, output: 50, cachedInput: 0 },
    costUsd: 0.000065,
    latenciesMs: { jev: 120, upstream: 800, total: 920 },
    status: 200,
    requestedModel: "flux",
    projectedCostUsd: 0.00007,
  };
  const json = JSON.stringify(card);
  assert.deepEqual(JSON.parse(json), card);
});