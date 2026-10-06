// Token estimation, cost projection, budget guards.

export interface TokenRates {
  in: number; // $/M input
  cachedIn?: number;
  out: number; // $/M output
}

/** Estimate tokens: ~4 chars/token heuristic, message-aware. */
export function estimateTokens(messages: Array<{ content: string }>): number {
  let chars = 0;
  for (const m of messages) chars += (m.content ?? "").length + 8; // +8 role/overhead
  return Math.ceil(chars / 4);
}

/** Projected USD cost for a request against a rate set. */
export function projectedCost(tokensIn: number, tokensOut: number, rates: { in: number; out: number }): number {
  return (tokensIn / 1e6) * rates.in + (tokensOut / 1e6) * rates.out;
}

/** Guard: returns excess (0 if within budget). Used by eval runners pre-flight. */
export function budgetGuard(projectedTotalUsd: number, capUsd: number): { ok: boolean; overBy: number } {
  const overBy = Math.max(0, projectedTotalUsd - capUsd);
  return { ok: overBy === 0, overBy: Number(overBy.toFixed(4)) };
}