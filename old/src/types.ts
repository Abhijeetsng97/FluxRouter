// Shared types for FluxRouter.

export type TierId = 0 | 1 | 2 | 3;

export type UpstreamName = "ollama" | "openrouter";

export interface TierModel {
  upstream: UpstreamName;
  /** Upstream model id, e.g. "nemotron-3-nano" or "meta-llama/llama-3.3-70b-instruct" */
  id: string;
  /** Context window in tokens */
  ctx: number;
  /** USD per million input tokens (off-peak) */
  in: number;
  /** USD per million cached input tokens, if any */
  cachedIn?: number;
  /** USD per million output tokens */
  out: number;
  /** Optional reasoning effort hint (model variant selection) */
  reasoningEffort?: string;
  /** OpenRouter failover id for this tier's ollama model */
  failoverId?: string;
}

export interface Tier {
  id: TierId;
  name: string;
  models: TierModel[];
}

export type RouteReason =
  | "trivial_bypass"
  | "policy"
  | "low_confidence_escalation"
  | "sticky"
  | "sticky_escape_up"
  | "cost_guard"
  | "context_gate"
  | "jev_timeout_fallback"
  | "jev_unavailable_fallback"
  | "upstream_exhausted";

/** The routing decision (pre-execution). */
export interface RouteDecision {
  tier: TierId;
  model: string;
  upstream: UpstreamName;
  reason: RouteReason;
  category: string;
  complexity: number;
  confidence: number;
  projectedCostUsd: number;
  /** Jev classification latency for this decision (0 when sticky/Jev skipped/failed). */
  jevLatencyMs?: number;
}

/** One per proxied request: the full audit trail. */
export interface RouteCard {
  ts: string;
  sessionId: string;
  tier: TierId;
  model: string;
  upstream: UpstreamName;
  reason: RouteReason;
  category: string;
  complexity: number;
  confidence: number;
  requestTokensEst: number;
  usage: { input: number; output: number; cachedInput: number };
  costUsd: number;
  latenciesMs: { jev: number; upstream: number; total: number };
  status: number;
  requestedModel: string;
  projectedCostUsd: number;
}

/** Jev "System One" API shapes. */
export interface JevAnswer {
  type: "choice" | "score" | "noul";
  choice?: string;
  noul?: number;
  score?: number;
  confidence?: number;
  probabilities?: Record<string, number>;
}

export interface JevResponse {
  model: string;
  answers: Record<string, JevAnswer>;
  usage: { input_tokens: number; output_tokens: number };
}

export interface ClassificationResult {
  category: string;
  complexity: number;
  complexityConfidence: number;
  categoryConfidence: number;
  trivialNoul: number;
  jevModel: string;
  jevUsage: { input_tokens: number; output_tokens: number };
}

export const FLUX_MODEL_ALIAS = "flux";

export const CATEGORIES = [
  "math",
  "code_debug",
  "code_implement",
  "code_explain",
  "refactor",
  "greeting_chitchat",
  "summarize",
  "tool_planning",
  "creative_writing",
  "other",
] as const;

export const DEFAULT_FALLBACK_TIER: TierId = 1;