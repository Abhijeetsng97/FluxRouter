// Routing policy: context gate, 2D table, trivial bypass, confidence escalation, cost guard.

import type { ClassificationResult, RouteReason, Tier, TierId } from "./types.ts";
import { projectedCost } from "./cost.ts";

export interface PolicyInput {
  classification: ClassificationResult | null; // null => Jev failed/timed out
  categories?: string[]; // for tests / direct use
  requestTokens: number;
  stickyTier: TierId | null;
  tiers: Tier[];
  config: {
    minConfidence: number;
    trivialNoul: number;
    /** Only escalate for low confidence when complexity is at least this (default 0.5). */
    escalateOnlyAboveComplexity: number;
    complexityToTier: Array<[number, TierId]>;
    overrides: Array<{ category: string; minComplexity?: number; tier: TierId }>;
    perRequestCapUsd: number;
    stickyEnabled: boolean;
    escapeConfidence: number;
  };
  /** Tier to use when Jev cannot be reached (default 1 = flash, fail-safe for quality). */
  fallbackTier?: TierId;
  budgetTokensOut?: number; // default estimate for projection
}

export interface PolicyOutcome {
  tier: TierId;
  reason: RouteReason;
  category: string;
  complexity: number;
  confidence: number;
  projectedCostUsd: number;
  notes: string[];
}

/** Remove tiers whose models cannot fit the request context. */
export function contextGate(tiers: Tier[], requestTokens: number): Tier[] {
  return tiers.filter((t) => t.models.some((m) => m.ctx >= requestTokens));
}

/** Pick the highest tier whose models fit the request. */
export function tierByContext(tiers: Tier[], requestTokens: number): Tier | null {
  const viable = contextGate(tiers, requestTokens);
  if (viable.length === 0) return null;
  return viable[viable.length - 1]!;
}

/** Map complexity score to a tier via the configured thresholds (< upper bound). */
export function complexityToTier(
  complexity: number,
  table: Array<[number, TierId]>,
): TierId {
  for (const [threshold, tier] of table) {
    if (complexity < threshold) return tier;
  }
  // Table's last entry is the catch-all; guard anyway.
  return table[table.length - 1]?.[1] ?? 2;
}

export function applyOverrides(
  category: string,
  complexity: number,
  overrides: Array<{ category: string; minComplexity?: number; tier: TierId }>,
): TierId | null {
  for (const ov of overrides) {
    if (ov.category === category) {
      if (ov.minComplexity === undefined || complexity >= ov.minComplexity) return ov.tier;
    }
  }
  return null;
}

/** Core routing decision for a classified request. */
export function routeRequest(input: PolicyInput): PolicyOutcome {
  const notes: string[] = [];
  const cfg = input.config;

  // 1. Sticky: reuse pinned tier for the session.
  if (cfg.stickyEnabled && input.stickyTier !== undefined && input.stickyTier !== null) {
    return {
      tier: input.stickyTier,
      reason: "sticky",
      category: input.classification?.category ?? "other",
      complexity: input.classification?.complexity ?? 0,
      confidence: input.classification?.complexityConfidence ?? 1,
      projectedCostUsd: 0,
      notes,
    }
  }

  // 2. Jev unavailable → fail-safe tier (default flash, not the cheapest:
  //    under-routing hard work is the worse failure).
  if (!input.classification) {
    const wanted = input.fallbackTier ?? 1;
    const fallback = fitFallbackTier(input.tiers, wanted, input.requestTokens);
    notes.push(`jev unavailable, using fallback tier ${fallback}`);
    return {
      tier: fallback,
      reason: "jev_unavailable_fallback",
      category: "unknown",
      complexity: 0,
      confidence: 0,
      projectedCostUsd: 0,
      notes,
    };
  }

  const cls = input.classification;
  const confidence = Math.min(cls.categoryConfidence, cls.complexityConfidence);

  // 3. Trivial bypass.
  if (cls.trivialNoul > cfg.trivialNoul) {
    notes.push(`trivial noul=${cls.trivialNoul.toFixed(2)} > ${cfg.trivialNoul}`);
    const tier0 = findTier(input.tiers, 0);
    if (tier0) {
      return {
        tier: 0,
        reason: "trivial_bypass",
        category: cls.category,
        complexity: cls.complexity,
        confidence,
        projectedCostUsd: 0,
        notes,
      };
    }
    notes.push("no tier 0 available, continuing policy");
  }

  // 4. Policy: overrides then default table.
  let tier: TierId | null = applyOverrides(cls.category, cls.complexity, cfg.overrides);
  let reason: RouteReason = "policy";
  if (tier === null) {
    tier = complexityToTier(cls.complexity, cfg.complexityToTier);
  }

  // 5. Context gate: if the chosen tier's models cannot fit, move to the cheapest tier that fits.
  const viable = contextGate(input.tiers, input.requestTokens);
  if (viable.length === 0) {
    notes.push("no tier can fit request tokens, using highest-tier model anyway");
  } else if (!viable.some((t) => t.id === tier)) {
    notes.push(`tier ${tier} cannot fit ${input.requestTokens} tokens, escalating for context`);
    const fit = cheapestFittingTier(input.tiers, input.requestTokens);
    if (fit) {
      tier = fit.id;
      reason = "context_gate";
    }
  }

  // 6. Low-confidence escalation — but only where low confidence could mean
  //    under-routing. A low-confidence *zero-complexity* reading ("go", "ok") is not a
  //    risk; escalating it wastes money (see the trivial-contract eval).
  if (confidence < cfg.minConfidence && cls.complexity >= (cfg.escalateOnlyAboveComplexity ?? 0.5) && tier < 2) {
    notes.push(`confidence ${confidence.toFixed(2)} < ${cfg.minConfidence} at complexity ${cls.complexity.toFixed(2)}, escalating to mid`);
    tier = 2;
    reason = "low_confidence_escalation";
  }

  // 7. Cost guard (runs after escalation; downgrades to the cheapest *fitting* tier,
  //    preferring the closest below the original choice rather than always tier 0).
  const tierObj = findTier(input.tiers, tier);
  if (tierObj) {
    const model = tierObj.models[0]!;
    const out = input.budgetTokensOut ?? 2000;
    const projected = projectedCost(input.requestTokens, out, { in: model.in, out: model.out });
    if (projected > cfg.perRequestCapUsd) {
      notes.push(`projected $${projected.toFixed(4)} > cap $${cfg.perRequestCapUsd}, downgrading`);
      const down = nearestCheaperFittingTier(
        input.tiers,
        tier,
        input.requestTokens,
        cfg.perRequestCapUsd,
        out,
      );
      if (down !== null) {
        tier = down;
        reason = "cost_guard";
      }
    }
  }

  return {
    tier,
    reason,
    category: cls.category,
    complexity: cls.complexity,
    confidence,
    projectedCostUsd: projectedCostForTier(input.tiers, tier, input.requestTokens, input.budgetTokensOut),
    notes,
  };
}

/** Highest tier id (used when nothing fits and we must try anyway). */
export function highestTier(tiers: Tier[]): TierId {
  return tiers.reduce((max, t) => (t.id > max ? t.id : max), 0 as TierId);
}

/**
 * Jev-failure fallback: prefer the requested tier, but honour the context gate by
 * moving up to the nearest larger-context tier, then clamp to the highest available.
 */
export function fitFallbackTier(tiers: Tier[], wanted: TierId, requestTokens: number): TierId {
  const sorted = [...tiers].sort((a, b) => a.id - b.id);
  const atOrAbove = sorted.filter((t) => t.id >= wanted);
  for (const t of atOrAbove) {
    if (t.models.some((m) => m.ctx >= requestTokens)) return t.id;
  }
  const anyAbove = atOrAbove[atOrAbove.length - 1];
  if (anyAbove) return anyAbove.id;
  return sorted[sorted.length - 1]?.id ?? 1;
}

export function findTier(tiers: Tier[], id: TierId): Tier | undefined {
  return tiers.find((t) => t.id === id);
}

/** Cheapest tier with at least one model that fits the request; used by cost guard. */
export function cheapestFittingTier(tiers: Tier[], requestTokens: number): Tier | null {
  const viable = contextGate(tiers, requestTokens);
  if (viable.length === 0) return null;
  return viable[0]!;
}

/**
 * Cost-guard downgrade target: the closest tier BELOW the original choice whose
 * models fit the request and whose projected cost is within the cap.
 * Falls back to the cheapest fitting tier; null if nothing meaningful exists.
 */
export function nearestCheaperFittingTier(
  tiers: Tier[],
  originalTier: TierId,
  requestTokens: number,
  capUsd = Number.POSITIVE_INFINITY,
  budgetTokensOut = 2000,
): TierId | null {
  // Search downward from just below the original tier.
  for (let t = (originalTier - 1) as TierId; t >= 0; t = (t - 1) as TierId) {
    const cand = findTier(tiers, t);
    if (!cand) continue;
    if (!cand.models.some((m) => m.ctx >= requestTokens)) continue;
    const m = cand.models[0]!;
    const c = projectedCost(requestTokens, budgetTokensOut, { in: m.in, out: m.out });
    if (c <= capUsd) return t;
  }
  // Nothing below fits within cap: take the absolute cheapest fitting tier (even if
  // over cap) — better cap-exceeded-with-logging than a silent unbounded request.
  return cheapestFittingTier(tiers, requestTokens)?.id ?? null;
}

function projectedCostForTier(
  tiers: Tier[],
  tier: TierId,
  requestTokens: number,
  outTokens?: number,
): number {
  const t = findTier(tiers, tier);
  if (!t) return 0;
  const m = t.models[0]!;
  return projectedCost(requestTokens, outTokens ?? 2000, { in: m.in, out: m.out });
}

/**
 * Sticky escape: decide whether a session's pinned tier should be upgraded.
 * Upgrade when the current turn classifies at least one tier above the pinned tier
 * with confidence >= escapeConfidence.
 */
export function stickyEscape(
  pinned: TierId,
  cls: ClassificationResult,
  complexityBands: Array<[number, TierId]>,
  overrides: Array<{ category: string; minComplexity?: number; tier: TierId }>,
  escapeConfidence: number,
): TierId | null {
  const confidence = Math.min(cls.categoryConfidence, cls.complexityConfidence);
  if (confidence < escapeConfidence) return null;
  let tier: TierId | null = applyOverrides(cls.category, cls.complexity, overrides);
  if (tier === null) tier = complexityToTier(cls.complexity, complexityBands);
  if (tier > pinned) return tier;
  return null;
}