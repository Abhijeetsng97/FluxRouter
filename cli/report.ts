// `flux report` — aggregate route cards into spend/performance reports.

import { loadConfig } from "../src/config.ts";
import { CardLog, type AggRow, type Totals } from "../src/cardlog.ts";

export interface ReportArgs {
  flags: Record<string, string | boolean>;
}

/** All-frontier counterfactual: cost if every request had used the tier-3 model. */
function counterfactualTotals(rows: AggRow[], tierRates: Map<number, { in: number; out: number }>, frontierTier: number): number {
  const f = tierRates.get(frontierTier);
  if (!f) return 0;
  let usd = 0;
  for (const r of rows) {
    usd += ((r.input_tokens ?? 0) / 1e6) * f.in + ((r.output_tokens ?? 0) / 1e6) * f.out;
  }
  return usd;
}

export async function runReport(args: { flags: Record<string, string | boolean> }): Promise<void> {
  const { config, errors } = loadConfig("fluxrouter.config.json");
  if (errors.length > 0) {
    console.error("config errors:", errors.join("; "));
    process.exit(2);
  }
  const cardLog = new CardLog(config.dataDir);
  const by = (args.flags["by"] as string ?? "day") as "day" | "tier" | "category" | "model" | "session";
  const today = args.flags["today"] === true;

  let rows = cardLog.aggregate(by);
  if (today) {
    const t0 = new Date().toISOString().slice(0, 10);
    rows = rows.filter((r) => r.bucket.startsWith(t0));
  }
  const totals: Totals = cardLog.totals();

  const tierRates = new Map<number, { in: number; out: number }>();
  for (const t of config.tiers) tierRates.set(t.id, { in: t.models[0]!.in, out: t.models[0]!.out });

  console.log("=== FluxRouter report ===");
  console.log(`group: ${by}${today ? " (today)" : ""}\n`);
  if (rows.length === 0) {
    console.log("no route cards recorded yet.");
  } else {
    console.log(
      pad("bucket", 18),
      pad("reqs", 6),
      pad("cost $", 10),
      pad("in tok", 10),
      pad("out tok", 10),
      pad("avg ms", 8),
      pad("esc", 5),
      pad("jevfb", 6),
    );
    for (const r of rows) {
      console.log(
        pad(String(r.bucket), 18),
        pad(String(r.requests), 6),
        pad((r.cost_usd ?? 0).toFixed(4), 10),
        pad(String(r.input_tokens ?? 0), 10),
        pad(String(r.output_tokens ?? 0), 10),
        pad((r.avg_latency_ms ?? 0).toFixed(0), 8),
        pad(String(r.escalations ?? 0), 5),
        pad(String(r.jev_fallbacks ?? 0), 6),
      );
    }
    const frontier = counterfactualTotals(
      cardLog.aggregate("tier"),
      tierRates,
      Math.max(...config.tiers.map((t) => t.id)),
    );
    console.log("\nTotals:");
    console.log(`  actual spend:      $${(totals.cost_usd ?? 0).toFixed(4)}`);
    console.log(`  all-frontier same: $${frontier.toFixed(4)} (counterfactual)`);
    const saved = totals.cost_usd !== null && frontier > 0 ? 1 - totals.cost_usd / frontier : 0;
    console.log(`  saving vs frontier: ${(saved * 100).toFixed(1)}%`);
  }
  cardLog.close();
}

function pad(s: string, n: number): string {
  return s.padEnd(n, " ");
}