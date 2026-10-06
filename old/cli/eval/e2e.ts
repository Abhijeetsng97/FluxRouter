// Stage 2 eval: end-to-end quality + cost, stratified across tiers, Jev-as-judge with
// 10% LLM cross-check. Hard budget guard before any spend.

import { classify } from "../../src/classify.ts";
import { loadConfig } from "../../src/config.ts";
import { budgetGuard } from "../../src/cost.ts";
import { loadGsm8k, loadMath500, loadSwebenchLite, loadTrivial, loadExplain, type LabeledPrompt } from "./datasets.ts";

interface E2eArgs {
  flags: Record<string, string | boolean>;
}

interface TierPlan {
  tier: number;
  prompts: LabeledPrompt[];
}

/** Stratification: ~total/4 per tier band, tolerant of missing datasets. */
async function stratified(total: number): Promise<{ plan: TierPlan[]; failed: Array<{ source: string; error: string }> }> {
  const per = Math.max(1, Math.floor(total / 4));
  const tasks: Array<[string, Promise<LabeledPrompt[]>]> = [
    ["gsm8k", loadGsm8k(Math.ceil(total / 3))],
    ["math500", loadMath500(Math.min(Math.ceil(total / 8), 100))],
    ["swebench_lite", loadSwebenchLite(Math.ceil(total / 4))],
    ["trivial", loadTrivial(per)],
    ["explain_hand", loadExplain(Math.ceil(per / 3))],
  ];
  const settled = await Promise.allSettled(tasks.map(([, p]) => p));
  const got = new Map<string, LabeledPrompt[]>();
  const failed: Array<{ source: string; error: string }> = [];
  settled.forEach((res, i) => {
    const source = tasks[i]![0];
    if (res.status === "fulfilled") got.set(source, res.value);
    else failed.push({ source, error: (res.reason as Error)?.message ?? String(res.reason) });
  });
  const gsm = got.get("gsm8k") ?? [];
  const math = got.get("math500") ?? [];
  const swe = got.get("swebench_lite") ?? [];
  const triv = got.get("trivial") ?? [];
  const expl = got.get("explain_hand") ?? [];
  return {
    failed,
    plan: [
      { tier: 0, prompts: triv.slice(0, per) },
      { tier: 1, prompts: [...expl, ...gsm.slice(0, per)].slice(0, per) },
      { tier: 2, prompts: [...gsm.slice(-per), ...math.slice(0, Math.ceil(per / 2))].slice(0, per) },
      { tier: 3, prompts: [...math.slice(-per), ...swe.slice(0, per)].slice(0, per) },
    ],
  };
}

/** A tiny reference baseline tier→model map copied from config at run time. */
function tierModelFor(config: ReturnType<typeof loadConfig>["config"], tier: number): { id: string; upstream: string } | null {
  const t = config.tiers.find((x) => x.id === tier);
  return t ? { id: t.models[0]!.id, upstream: t.models[0]!.upstream } : null;
}

export async function runE2eEval(args: E2eArgs): Promise<void> {
  const { config } = loadConfig("fluxrouter.config.json");
  const limit = Number(args.flags["limit"] ?? 120);
  const { plan, failed } = await stratified(limit);
  if (failed.length > 0) {
    console.warn("warning: some datasets failed to load and were skipped:");
    for (const f of failed) console.warn(`  - ${f.source}: ${f.error}`);
  }
  const totalPrompts = plan.reduce((a, p) => a + p.prompts.length, 0);

  // Rough worst-case cost model: average ~1.5k in / ~1k out tokens per prompt.
  const cfg = config;
  const worst = plan.reduce((sum, p) => {
    const m = config.tiers.find((t) => t.id === p.tier)?.models[0];
    if (!m) return sum;
    return sum + p.prompts.length * ((1500 / 1e6) * m.in + (1500 / 1e6) * m.out);
  }, 0);
  const guard = budgetGuard(worst, cfg.eval.e2eCapUsd);
  console.log(`e2e eval: ${totalPrompts} prompts, worst-case $${worst.toFixed(2)} (cap $${cfg.eval.e2eCapUsd})`);
  if (args.flags["dry-run"] === true) {
    console.log("dry-run: stopping before any calls.");
    for (const p of plan) {
      const m = tierModelFor(cfg, p.tier)!;
      console.log(`  tier ${p.tier}: ${p.prompts.length} prompts → ${m.upstream}:${m.id}`);
    }
    return;
  }
  if (!guard.ok) {
    console.error(`✗ budget guard: projected $${worst.toFixed(2)} exceeds cap $${cfg.eval.e2eCapUsd}. Nothing was spent.`);
    process.exit(2);
  }

  const ollamaKey = process.env["OLLAMA_API_KEY"];
  const openrouterKey = process.env["OPENROUTER_API_KEY"];
  const jevKey = process.env["TYPESAFE_API_KEY"];
  if (!ollamaKey || !jevKey) {
    console.error("OLLAMA_API_KEY and TYPESAFE_API_KEY are required for e2e eval.");
    process.exit(2);
  }

  console.log("NOTE: e2e sends real generation requests to your Ollama Cloud credits.");
  console.log("      Grading: exact-match where ground truth exists; Jev-as-judge otherwise.\n");

  const results = new Map<number, { n: number; pass: number; judgeDisagree: number; costUsd: number }>();
  let spend = 0;
  const openendedCount = { n: 0 };

  for (const group of plan) {
    const model = config.tiers.find((t) => t.id === group.tier)?.models[0]!;
    const key = model.upstream === "openrouter" ? openrouterKey ?? "" : ollamaKey;
    const baseUrl = cfg.upstreams[model.upstream as "ollama" | "openrouter"].baseUrl;
    const stat = results.get(group.tier) ?? { n: 0, pass: 0, judgeDisagree: 0, costUsd: 0 };
    results.set(group.tier, stat);

    for (const p of group.prompts) {
      const res = await fetch(`${baseUrl.replace(/\/$/, "")}/chat/completions`, {
        method: "POST",
        headers: { Authorization: `Bearer ${key}`, "Content-Type": "application/json" },
        body: JSON.stringify({ model: model.id, messages: p.messages, stream: false }),
      });
      if (!res.ok) {
        console.error(`  ! ${p.source} HTTP ${res.status} at tier ${group.tier}; skipping`);
        continue;
      }
      const json = (await res.json()) as any;
      const text: string = json.choices?.[0]?.message?.content ?? "";
      const u = json.usage ?? {};
      const cost =
        ((u.prompt_tokens ?? 0) / 1e6) * model.in + ((u.completion_tokens ?? 0) / 1e6) * model.out;
      spend += cost;
      stat.n++;
      stat.costUsd += cost;

      const graded = await grade(p, text, cfg.eval.judgeModel, jevKey!, {
        openendedCount,
        crossCheckEveryN: 10,
        ollamaBaseUrl: cfg.upstreams.ollama.baseUrl,
        ollamaKey: ollamaKey ?? "",
        ollamaJudgeModel: "glm-5.3-flash",
      });
      if (graded.pass) stat.pass++;
      if (graded.crossCheck !== null && graded.crossCheck !== graded.pass) stat.judgeDisagree++;
    }
  }

  // Report.
  const pad = (v: string, n: number) => v.padEnd(n, " ");
  console.log("=== e2e eval results ===");
  console.log(pad("tier", 6), pad("n", 5), pad("pass", 8), pad("judge∂", 8), pad("cost $", 8));
  let totN = 0, totPass = 0, totCost = 0, totDis = 0;
  for (const [tier, s] of [...results.entries()].sort((a, b) => a[0] - b[0])) {
    console.log(
      pad(`t${tier}`, 6),
      pad(String(s.n), 5),
      pad(`${((s.pass / Math.max(1, s.n)) * 100).toFixed(1)}%`, 8),
      pad(String(s.judgeDisagree), 8),
      pad(s.costUsd.toFixed(4), 8),
    );
    totN += s.n; totPass += s.pass; totCost += s.costUsd; totDis += s.judgeDisagree;
  }
  console.log(pad("TOTAL", 6), pad(String(totN), 5), pad(`${((totPass / Math.max(1, totN)) * 100).toFixed(1)}%`, 8), pad(String(totDis), 8), pad(totCost.toFixed(4), 8));
  const disagreeRate = totN > 0 ? totDis / totN : 0;
  console.log(`\nJev-judge cross-check disagreement: ${(disagreeRate * 100).toFixed(1)}% ${disagreeRate > 0.2 ? "⚠ > 20% — revise judge prompt" : "(ok, < 20%)"}`);
  console.log(`Actual spend: $${spend.toFixed(4)}`);
}

/** Grade one answer: exact match if ground truth, else Jev Score judge (+ 10% LLM cross-check). */
async function grade(
  p: LabeledPrompt,
  answer: string,
  judgeModel: string,
  jevKey: string,
  opts: { openendedCount: { n: number }; crossCheckEveryN: number; ollamaBaseUrl: string; ollamaKey: string; ollamaJudgeModel: string },
): Promise<{ pass: boolean; crossCheck: boolean | null }> {
  if (p.groundTruth !== undefined && p.groundTruth !== "") {
    // Exact numeric match (normalize commas/spaces).
    const norm = (s: string) => s.replace(/[,\s]/g, "").replace($RE_DOLLAR, "").replace($RE_TRAIL, "");
    const pass = norm(answer).endsWith(norm(p.groundTruth));
    return { pass, crossCheck: null };
  }
  // Jev-as-judge: score the answer 0..2.
  const res = await fetch("https://api.typesafe.ai/v1/systemone", {
    method: "POST",
    headers: { Authorization: `Bearer ${jevKey}`, "Content-Type": "application/json" },
    body: JSON.stringify({
      model: judgeModel,
      state: `QUESTION:\n${p.messages[p.messages.length - 1]!.content}\n\nANSWER:\n${answer.slice(0, 4000)}`,
      questions: {
        quality: {
          type: "score",
          instructions:
            "Does the ANSWER correctly and completely address the QUESTION? 0 = wrong/mostly wrong, 1 = partially correct, 2 = correct and complete.",
          criteria: ["wrong or mostly wrong", "partially correct or incomplete", "correct and complete"],
        },
      },
    }),
  });
  if (!res.ok) return { pass: false, crossCheck: null };
  const json = (await res.json()) as any;
  const score = Number(json.answers?.quality?.score ?? 0);
  const pass = score >= 1.5;

  // 10% cross-check with a cheap chat-model judge to detect Jev-judge bias.
  opts.openendedCount.n += 1;
  let crossCheck: boolean | null = null;
  if (opts.openendedCount.n % opts.crossCheckEveryN === 0 && opts.ollamaKey) {
    crossCheck = await llmJudge(
      p.messages[p.messages.length - 1]!.content,
      answer,
      opts.ollamaBaseUrl,
      opts.ollamaKey,
      opts.ollamaJudgeModel,
    );
  }
  return { pass, crossCheck };
}

/** Cheap chat-model judge (glm-5.3-flash by default) scoring 0-2 like the Jev judge. */
async function llmJudge(question: string, answer: string, baseUrl: string, apiKey: string, model: string): Promise<boolean | null> {
  try {
    const res = await fetch(`${baseUrl.replace(/\/$/, "")}/chat/completions`, {
      method: "POST",
      headers: { Authorization: `Bearer ${apiKey}`, "Content-Type": "application/json" },
      body: JSON.stringify({
        model,
        stream: false,
        messages: [
          {
            role: "user",
            content:
              `Grade the ANSWER to the QUESTION on 0-2 (0 wrong, 1 partial, 2 correct & complete). Reply with just the digit.\n\nQUESTION:\n${question}\n\nANSWER:\n${answer.slice(0, 4000)}`,
          },
        ],
      }),
    });
    if (!res.ok) return null;
    const json = (await res.json()) as any;
    const text: string = json.choices?.[0]?.message?.content ?? "";
    const m = text.match(/[0-2]/);
    return m ? Number(m[0]) >= 2 : null;
  } catch {
    return null;
  }
}

const $RE_DOLLAR = /\$/g;
const $RE_TRAIL = /\.$/;