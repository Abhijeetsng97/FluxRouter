// Eval dataset loaders: download public benchmark samples, label them, sample lists.
// Ground truth for Stage-2 grading lives with each dataset (math answers / test cases).
//
// Sources & licenses (all permissive; sampled, not redistributed):
//   GSM8K            MIT            openai/grade-school-math
//   MATH-500         MIT            HuggingFaceH4/MATH-500
//   SWE-bench Lite   MIT            princeton-nlp/SWE-bench_Lite
//   trivial/explain  original       hand-written in this file
//
// Network access is cached under cli/eval/datasets-files/ so repeated eval runs cost
// nothing and do not re-download.

import { mkdirSync, existsSync, readFileSync, writeFileSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

export interface LabeledPrompt {
  source: string;
  category: string;
  /** Acceptable tiers for Stage-1 routing accuracy. */
  expectedTiers: number[];
  /** Exact/evaluable target for Stage-2 when available (e.g. numeric answer). */
  groundTruth?: string;
  messages: Array<{ role: string; content: string }>;
}

const CLI_DIR = dirname(fileURLToPath(import.meta.url));
const DATA_DIR = join(CLI_DIR, "datasets-files");

function ensureDataDir(): void {
  mkdirSync(DATA_DIR, { recursive: true });
}

/** Fetch text with a local cache. */
async function cachedText(name: string, url: string): Promise<string> {
  ensureDataDir();
  const p = join(DATA_DIR, `${name}.cache`);
  if (existsSync(p)) return readFileSync(p, "utf8");
  const res = await fetch(url);
  if (!res.ok) throw new Error(`dataset fetch ${url}: HTTP ${res.status}`);
  const text = await res.text();
  writeFileSync(p, text);
  return text;
}

/** Fetch JSON with a local cache. */
async function cachedJson(name: string, url: string): Promise<unknown> {
  return JSON.parse(await cachedText(name, `${url}`));
}

function pick<T>(arr: T[], n: number, seed: number): T[] {
  // Deterministic sampling (seeded LCG) so eval runs are reproducible.
  const out: T[] = [];
  let s = seed;
  const pool = [...arr];
  while (out.length < n && pool.length > 0) {
    s = (s * 1103515245 + 12345) % 2147483648;
    const idx = s % pool.length;
    out.push(pool.splice(idx, 1)[0]!);
  }
  return out;
}

/** HuggingFace datasets-server rows endpoint (needs an explicit config). */
function hfRows(dataset: string, length: number): string {
  return `https://datasets-server.huggingface.co/rows?dataset=${encodeURIComponent(dataset)}&config=default&split=test&offset=0&length=${length}`;
}

/** GSM8K: grade-school math; the answer follows "####" in the reference field. */
export async function loadGsm8k(n: number): Promise<LabeledPrompt[]> {
  // This endpoint serves JSONL, not JSON.
  const text = await cachedText(
    "gsm8k",
    "https://raw.githubusercontent.com/openai/grade-school-math/master/grade_school_math/data/test.jsonl",
  );
  const rows = text
    .split("\n")
    .filter((l) => l.trim().length > 0)
    .map((l) => JSON.parse(l) as { question: string; answer: string });
  return pick(rows, n, 42).map((row) => {
    const m = row.answer.match(/####\s*([-0-9,.]+)/);
    // GSM8K is grade-school level; nano/flash are the right bands, mid is the outer edge.
    return {
      source: "gsm8k",
      category: "math",
      expectedTiers: [0, 1, 2],
      ...(m?.[1] ? { groundTruth: m[1].replace(/,/g, "") } : {}),
      messages: [{ role: "user", content: row.question }],
    };
  });
}

/** MATH-500: competition-level math (the 500-problem slice of MATH).
 *
 * Expected tiers come from the dataset's own `level` field (1 = easiest, 5 = hardest),
 * not a flat guess — the set is deliberately mixed-difficulty. Mapping:
 *   L1-2 → [0,1]  (nano/flash genuinely suffice)
 *   L3   → [1,2]
 *   L4-5 → [2,3]  (real under-routing risk lives here)
 */
export async function loadMath500(n: number): Promise<LabeledPrompt[]> {
  interface Row { problem: string; answer?: string; level?: string | number; subject?: string }
  const json = (await cachedJson("math500", hfRows("HuggingFaceH4/MATH-500", 100))) as {
    rows?: Array<{ row: Row }>;
  };
  const rows = (json.rows ?? []).map((r) => r.row).filter((r) => typeof r.problem === "string");
  return pick(rows, n, 7).map((row) => {
    const level = Number(row.level ?? 3);
    const expectedTiers = level <= 2 ? [0, 1] : level === 3 ? [1, 2] : [2, 3];
    return {
      source: "math500",
      category: "math",
      expectedTiers,
      ...(row.answer ? { groundTruth: String(row.answer).replace(/\\\(|\\\)/g, "").trim() } : {}),
      messages: [{ role: "user", content: row.problem }],
    };
  });
}

/** SWE-bench Lite problem statements (debug-labeled). */
export async function loadSwebenchLite(n: number): Promise<LabeledPrompt[]> {
  interface Row { instance_id?: string; problem_statement?: string; issue?: string; repo?: string }
  const json = (await cachedJson("swebench-lite", hfRows("princeton-nlp/SWE-bench_Lite", 100))) as {
    rows?: Array<{ row: Row }>;
  };
  const rows = (json.rows ?? [])
    .map((r) => r.row)
    .filter((r) => (r.problem_statement ?? r.issue) !== undefined);
  return pick(rows, n, 11).map((row) => {
    // `problem_statement` includes the repo's issue description. Prepending the repo
    // name gives Jev context about the codebase, which measurably helps it judge
    // engineering difficulty (otherwise terse one-line issues look trivial).
    const text = (row.problem_statement ?? row.issue ?? "").slice(0, 4000);
    const repo = (row.repo ?? "unknown").replace("-", "/");
    return {
      source: "swebench_lite",
      category: "code_debug",
      expectedTiers: [2, 3],
      messages: [{ role: "user", content: `Repository: ${repo}\n\n${text}` }],
    };
  });
}

/**
 * Hand-written trivial prompts: greetings, thanks, chitchat — the contract under test
 * is that these NEVER leave tier 0. Edit freely; keep labels consistent.
 */
const TRIVIAL_PROMPTS = [
  "hello", "hi", "hey there", "good morning", "how are you?", "thanks!", "thank you",
  "ok", "cool", "nice", "what's up?", "bye", "good night", "see you tomorrow",
  "ping", "test", "are you there?", "awesome", "great job", "well done",
  "howdy", "yo", "sup", "gm", "gn", "happy friday!", "lol", "haha", "👍", "🙏",
  "Hello!", "Hi FluxRouter", "just checking in", "anything new?", "who are you?",
  "what can you do?", "tell me a one-line joke", "say hi to the team", "waves", "huzzah",
  "ready?", "go", "continue", "ok thanks", "sounds good", "perfect", "understood",
  "no problem", "my pleasure", "cheers", "later!", "cya",
];

export async function loadTrivial(n: number): Promise<LabeledPrompt[]> {
  return pick(TRIVIAL_PROMPTS, n, 13).map((p) => ({
    source: "trivial",
    category: "greeting_chitchat",
    expectedTiers: [0],
    messages: [{ role: "user", content: p }],
  }));
}

/** Hand-written explain/chat prompts (mid-band sanity). */
const EXPLAIN_PROMPTS = [
  "Explain what a closure is in JavaScript in two sentences.",
  "What does the 'volatile' keyword mean in C?",
  "Summarize: Git rebase rewrites commit history onto a new base, producing linear history but requiring force-push on shared branches.",
  "In one paragraph: why is database connection pooling important?",
  "Explain the difference between TCP and UDP to a junior dev.",
  "What is the difference between let and var in JS?",
  "How does HTTP caching with ETags work?",
  "Give me a two-line summary of what a Bloom filter does.",
  "Explain what an HTTP 402 status code means.",
  "Why do we use virtual environments in Python?",
];

export async function loadExplain(n: number): Promise<LabeledPrompt[]> {
  return pick(EXPLAIN_PROMPTS, n, 29).map((p) => ({
    source: "explain_hand",
    category: "code_explain",
    // These are short conceptual explanations; nano/flash legitimately suffice.
    expectedTiers: [0, 1],
    messages: [{ role: "user", content: p }],
  }));
}

export interface LoadReport {
  prompts: LabeledPrompt[];
  loaded: string[];
  failed: Array<{ source: string; error: string }>;
}

/**
 * Load every set needed for the routing eval. Network failures are reported, not fatal:
 * the eval runs with whatever loaded, so `--dry-run` and partial-network runs still work.
 */
export async function loadRoutingSet(limitPerSet = 100): Promise<LoadReport> {
  const tasks: Array<[string, Promise<LabeledPrompt[]>]> = [
    ["trivial", loadTrivial(limitPerSet)],
    ["explain_hand", loadExplain(Math.min(limitPerSet, 10))],
    ["gsm8k", loadGsm8k(limitPerSet)],
    ["math500", loadMath500(Math.min(limitPerSet, 100))],
    ["swebench_lite", loadSwebenchLite(limitPerSet)],
  ];
  const prompts: LabeledPrompt[] = [];
  const loaded: string[] = [];
  const failed: Array<{ source: string; error: string }> = [];
  const settled = await Promise.allSettled(tasks.map(([, p]) => p));
  settled.forEach((res, i) => {
    const source = tasks[i]![0];
    if (res.status === "fulfilled") {
      prompts.push(...res.value);
      loaded.push(source);
    } else {
      failed.push({ source, error: (res.reason as Error)?.message ?? String(res.reason) });
    }
  });
  return { prompts, loaded, failed };
}