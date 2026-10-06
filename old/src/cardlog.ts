// Route card logs: JSONL append + SQLite row per request.

import { appendFileSync, mkdirSync } from "node:fs";
import { join } from "node:path";
import { DatabaseSync } from "node:sqlite";
import type { RouteCard } from "./types.ts";

export class CardLog {
  private jsonlPath: string;
  private db: DatabaseSync;
  private insert: Statement;

  constructor(dataDir: string) {
    mkdirSync(dataDir, { recursive: true });
    this.jsonlPath = join(dataDir, "route-cards.jsonl");
    this.db = new DatabaseSync(join(dataDir, "route-cards.db"));
    this.db.exec(`CREATE TABLE IF NOT EXISTS route_cards (
      ts TEXT NOT NULL,
      session_id TEXT NOT NULL,
      tier INTEGER NOT NULL,
      model TEXT NOT NULL,
      upstream TEXT NOT NULL,
      reason TEXT NOT NULL,
      category TEXT NOT NULL,
      complexity REAL NOT NULL,
      confidence REAL NOT NULL,
      request_tokens_est INTEGER NOT NULL,
      input_tokens INTEGER NOT NULL,
      output_tokens INTEGER NOT NULL,
      cached_input_tokens INTEGER NOT NULL DEFAULT 0,
      cost_usd REAL NOT NULL,
      projected_cost_usd REAL NOT NULL,
      latency_jev_ms INTEGER NOT NULL,
      latency_upstream_ms INTEGER NOT NULL,
      latency_total_ms INTEGER NOT NULL,
      status INTEGER NOT NULL,
      requested_model TEXT
    );
    CREATE INDEX IF NOT EXISTS idx_rc_ts ON route_cards(ts);
    CREATE INDEX IF NOT EXISTS idx_rc_session ON route_cards(session_id);
    CREATE INDEX IF NOT EXISTS idx_rc_tier ON route_cards(tier);`);
    this.insert = this.db.prepare(
      `INSERT INTO route_cards
      (ts, session_id, tier, model, upstream, reason, category, complexity, confidence,
       request_tokens_est, input_tokens, output_tokens, cached_input_tokens, cost_usd,
       projected_cost_usd, latency_jev_ms, latency_upstream_ms, latency_total_ms, status, requested_model)
      VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
    );
  }

  /** Write one route card to both JSONL and SQLite. */
  log(card: RouteCard): void {
    appendFileSync(this.jsonlPath, `${JSON.stringify(card)}\n`, "utf8");
    this.insert.run(
      card.ts,
      card.sessionId,
      card.tier,
      card.model,
      card.upstream,
      card.reason,
      card.category,
      card.complexity,
      card.confidence,
      card.requestTokensEst,
      card.usage.input,
      card.usage.output,
      card.usage.cachedInput,
      card.costUsd,
      card.projectedCostUsd,
      card.latenciesMs.jev,
      card.latenciesMs.upstream,
      card.latenciesMs.total,
      card.status,
      card.requestedModel,
    );
  }

  /** Aggregation for flux report. */
  aggregate(groupBy: "day" | "tier" | "category" | "model" | "session"): AggRow[] {
    const col =
      groupBy === "day"
        ? `substr(ts, 1, 10)`
        : groupBy === "tier"
          ? `tier`
          : groupBy === "category"
            ? `category`
            : groupBy === "model"
              ? `model`
              : `session_id`;
    const stmt = this.db.prepare(`
      SELECT ${col} AS bucket,
             COUNT(*) AS requests,
             SUM(cost_usd) AS cost_usd,
             SUM(input_tokens) AS input_tokens,
             SUM(output_tokens) AS output_tokens,
             AVG(latency_total_ms) AS avg_latency_ms,
             SUM(CASE WHEN reason IN ('low_confidence_escalation','sticky_escape_up','cost_guard','context_gate') THEN 1 ELSE 0 END) AS escalations,
             SUM(CASE WHEN reason = 'jev_timeout_fallback' OR reason = 'jev_unavailable_fallback' THEN 1 ELSE 0 END) AS jev_fallbacks
      FROM route_cards
      GROUP BY ${col}
      ORDER BY bucket`);
    return stmt.all() as unknown as AggRow[];
  }

  totals(): Totals {
    const stmt = this.db.prepare(`
      SELECT COUNT(*) AS requests,
             SUM(cost_usd) AS cost_usd,
             SUM(input_tokens) AS input_tokens,
             SUM(output_tokens) AS output_tokens,
             AVG(latency_total_ms) AS avg_latency_ms
      FROM route_cards`);
    return stmt.get() as unknown as Totals;
  }

  close(): void {
    this.db.close();
  }
}

type Statement = ReturnType<DatabaseSync["prepare"]>;

export interface AggRow {
  bucket: string;
  requests: number;
  cost_usd: number;
  input_tokens: number;
  output_tokens: number;
  avg_latency_ms: number;
  escalations: number;
  jev_fallbacks: number;
}

export interface Totals {
  requests: number;
  cost_usd: number | null;
  input_tokens: number | null;
  output_tokens: number | null;
  avg_latency_ms: number | null;
}