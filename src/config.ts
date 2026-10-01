// Configuration loading and validation for FluxRouter.

import { readFileSync } from "node:fs";
import { CATEGORIES, DEFAULT_FALLBACK_TIER, type Tier, type TierId } from "./types.ts";

export interface FluxConfig {
  jev: {
    baseUrl: string;
    model: string;
    timeoutMs: number;
    minConfidence: number;
    trivialNoul: number;
    /** Only escalate for low confidence when complexity is at least this (default 0.5). */
    escalateOnlyAboveComplexity: number;
    /** Tier used when Jev is unreachable; default 1 (flash) — fail safe for quality. */
    fallbackTier: TierId;
  };
  upstreams: {
    ollama: { baseUrl: string; apiKeyEnv: string };
    openrouter: { baseUrl: string; apiKeyEnv: string };
  };
  tiers: Tier[];
  policy: {
    default: { complexityToTier: Array<[number, TierId]> };
    overrides: Array<{ category: string; minComplexity?: number; tier: TierId }>;
  };
  sticky: { enabled: boolean; escapeConfidence: number };
  cost: { perRequestCapUsd: number; openrouterMonthlyCapUsd?: number };
  eval: { routingCapUsd: number; e2eCapUsd: number; judgeModel: string };
  server: { port: number; host: string; listTierModels: boolean };
  respectIncomingModel: boolean;
  dataDir: string;
}

const DEFAULT_COMPLEXITY_TO_TIER: Array<[number, TierId]> = [
  // Jev scores span 0..2 (weighted mean over 3 levels). Bands use strict "<".
  // < 0.8 → nano; < 1.6 → flash; < 2.0 → mid; 2.0 (Jev's max, "Hard") → frontier.
  // NOTE: these are provisional; the Stage-1 routing eval is what tunes them.
  [0.8, 0],
  [1.6, 1],
  [2.0, 2],
  [999, 3],
];

function isTierId(v: unknown): v is TierId {
  return typeof v === "number" && v >= 0 && v <= 3 && Number.isInteger(v);
}

export function defaultConfig(): FluxConfig {
  return {
    jev: {
      baseUrl: "https://api.typesafe.ai/v1/systemone",
      model: "jev-1.13.0",
      timeoutMs: 700,
      minConfidence: 0.5,
      trivialNoul: 0.85,
      escalateOnlyAboveComplexity: 0.5,
      fallbackTier: 1,
    },
    upstreams: {
      ollama: { baseUrl: "https://ollama.com/v1", apiKeyEnv: "OLLAMA_API_KEY" },
      openrouter: { baseUrl: "https://openrouter.ai/api/v1", apiKeyEnv: "OPENROUTER_API_KEY" },
    },
    tiers: [
      {
        id: 0,
        name: "nano",
        models: [
          {
            upstream: "ollama",
            id: "nemotron-3-nano:30b",
            ctx: 1_000_000,
            in: 0.06,
            out: 0.24,
            failoverId: "nvidia/nemotron-3-nano",
          },
        ],
      },
      {
        id: 1,
        name: "flash",
        models: [
          {
            upstream: "ollama",
            id: "glm-5.3-flash",
            ctx: 1_000_000,
            in: 0.15,
            out: 0.5,
            failoverId: "zhipuai/glm-5.3-flash",
          },
        ],
      },
      {
        id: 2,
        name: "mid",
        models: [
          {
            upstream: "ollama",
            id: "deepseek-v4-pro:0813",
            ctx: 1_000_000,
            in: 0.66,
            out: 1.98,
            reasoningEffort: "high",
            failoverId: "deepseek/deepseek-v4-pro",
          },
        ],
      },
      {
        id: 3,
        name: "frontier",
        models: [
          {
            upstream: "ollama",
            id: "kimi-k3",
            ctx: 1_000_000,
            in: 3.0,
            out: 15.0,
            failoverId: "moonshotai/kimi-k3",
          },
        ],
      },
    ],
    policy: {
      default: { complexityToTier: DEFAULT_COMPLEXITY_TO_TIER },
      overrides: [
        { category: "math", minComplexity: 1.2, tier: 2 },
        { category: "greeting_chitchat", tier: 0 },
      ],
    },
    sticky: { enabled: true, escapeConfidence: 0.6 },
    cost: { perRequestCapUsd: 0.25 },
    eval: { routingCapUsd: 1, e2eCapUsd: 10, judgeModel: "jev-1.13.0" },
    server: { port: 8787, host: "127.0.0.1", listTierModels: false },
    respectIncomingModel: false,
    dataDir: ".fluxrouter",
  };
}

/** Deep-merge a user JSON config over defaults (user wins, arrays replaced). */
export function mergeConfig(user: unknown): FluxConfig {
  const base = defaultConfig();
  if (!user || typeof user !== "object") return base;
  deepMerge(base as unknown as Record<string, unknown>, user as Record<string, unknown>);
  return base;
}

function deepMerge(target: Record<string, unknown>, src: Record<string, unknown>): void {
  for (const [k, v] of Object.entries(src)) {
    if (v && typeof v === "object" && !Array.isArray(v) && target[k] && typeof target[k] === "object") {
      deepMerge(target[k] as Record<string, unknown>, v as Record<string, unknown>);
    } else {
      target[k] = v;
    }
  }
}

/** Validate structural facts; throw with a readable message on failure. */
export function validateConfig(cfg: FluxConfig): string[] {
  const errors: string[] = [];
  if (!cfg.jev.baseUrl.startsWith("http")) errors.push("jev.baseUrl must be http(s)");
  if (!cfg.jev.model) errors.push("jev.model is required");
  if (cfg.jev.timeoutMs < 50) errors.push("jev.timeoutMs must be >= 50");
  if (cfg.jev.minConfidence <= 0 || cfg.jev.minConfidence >= 1) {
    errors.push("jev.minConfidence must be between 0 and 1 exclusive");
  }
  if (cfg.jev.trivialNoul <= 0 || cfg.jev.trivialNoul >= 1) {
    errors.push("jev.trivialNoul must be between 0 and 1 exclusive");
  }
  if (!isTierId(cfg.jev.fallbackTier)) {
    errors.push("jev.fallbackTier must be an integer 0..3");
  }
  if (!Array.isArray(cfg.tiers) || cfg.tiers.length === 0) errors.push("tiers must be a non-empty array");
  for (const t of cfg.tiers) {
    if (!isTierId(t.id)) errors.push(`tier id ${JSON.stringify(t.id)} must be an integer 0..3`);
    if (!t.name) errors.push(`tier ${t.id} missing name`);
    if (!Array.isArray(t.models) || t.models.length === 0) {
      errors.push(`tier ${t.id} must have at least one model`);
    } else {
      for (const m of t.models) {
        if (m.upstream !== "ollama" && m.upstream !== "openrouter") {
          errors.push(`tier ${t.id} model ${m.id}: upstream must be "ollama" or "openrouter"`);
        }
        if (!m.id) errors.push(`tier ${t.id} model missing id`);
        if (!(m.ctx > 0)) errors.push(`tier ${t.id} model ${m.id}: ctx must be > 0`);
        if (!(m.in >= 0) || !(m.out >= 0)) errors.push(`tier ${t.id} model ${m.id}: rates must be >= 0`);
      }
    }
  }
  const ids = new Set(cfg.tiers.map((t) => t.id).sort((a, b) => a - b));
  ids.forEach((id) => {
    if (id !== 0 && !hasTier(cfg.tiers, (id - 1) as TierId)) {
      errors.push(`tier ${id} exists but tier ${id - 1} is missing`);
    }
  });
  for (const ov of cfg.policy.overrides) {
    if (!CATEGORIES.includes(ov.category as (typeof CATEGORIES)[number])) {
      errors.push(`policy override category "${ov.category}" is not a known category`);
    }
    if (!isTierId(ov.tier)) errors.push(`policy override for ${ov.category}: tier must be 0..3`);
  }
  if (!(cfg.cost.perRequestCapUsd > 0)) errors.push("cost.perRequestCapUsd must be > 0");
  if (!(cfg.eval.routingCapUsd > 0)) errors.push("eval.routingCapUsd must be > 0");
  if (!(cfg.eval.e2eCapUsd > 0)) errors.push("eval.e2eCapUsd > 0");
  const seen = new Set<string>();
  for (const t of cfg.tiers) {
    for (const m of t.models) {
      const key = `${m.upstream}:${m.id}`;
      if (seen.has(key)) errors.push(`duplicate model across tiers: ${key}`);
      seen.add(key);
    }
  }
  return errors;
}

function hasTier(tiers: Tier[], id: TierId): boolean {
  return tiers.some((t) => t.id === id);
}

export function loadConfig(path?: string): { config: FluxConfig; errors: string[] } {
  const base = defaultConfig();
  let user: unknown = {};
  if (path) {
    try {
      user = JSON.parse(readFileSync(path, "utf8"));
    } catch (e) {
      return { config: base, errors: [`cannot read config file ${path}: ${(e as Error).message}`] };
    }
  }
  const merged = mergeConfig(user);
  const errors = validateConfig(merged);
  return { config: merged, errors };
}

export { hasTier, DEFAULT_FALLBACK_TIER };