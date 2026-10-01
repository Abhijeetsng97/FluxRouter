// Jev System One client for routing classification.

import type { ClassificationResult, JevResponse } from "./types.ts";

export interface JevClientOptions {
  baseUrl: string;
  model: string;
  timeoutMs: number;
  apiKey: string;
  fetchImpl?: typeof fetch;
}

const ROUTE_QUESTIONS = {
  category: {
    type: "choice",
    instructions:
      "Classify the primary intent of the LAST user message in this conversation excerpt. " +
      "Pick the single best category.",
    criteria: {
      math: "Maths: equations, arithmetic, competition-style problems, probability",
      code_debug: "Diagnosing a bug, error, test failure, stack trace, or wrong behavior",
      code_implement: "Write new code / implement a feature or function from scratch",
      code_explain: "Explain how code works, walk through code, teach a concept",
      refactor: "Restructure, rename, or improve existing code without changing behavior",
      greeting_chitchat: "Greetings, small talk, pleasantries, thanks, one-word replies",
      summarize: "Condense a document, thread, diff, or long text into a summary",
      tool_planning: "Deciding how to use tools, planning multi-step agent work",
      creative_writing: "Prose, stories, marketing copy, naming, brainstorming flavor",
      other: "None of the above fit cleanly",
    },
  },
  complexity: {
    type: "score",
    instructions:
      "How hard is this for a state-of-the-art LLM to complete correctly? Rate the task, not the message length.",
    criteria: [
      "Routine: simple lookup, greeting, small edit, anything a small cheap model reliably does",
      "Judgement: multi-step reasoning, moderate math/coding, needs care but is standard",
      "Hard: deep reasoning, large-scope engineering, subtle debugging, competition math",
    ],
  },
  is_trivial: {
    type: "noul",
    instructions:
      "Is this request trivially answerable — a greeting, thanks, filler, or something needing essentially no thought? Answer true/false.",
  },
} as const;

/** Build the Jev `state` as head+tail excerpt of the conversation. */
export function buildJevState(messages: Array<{ role: string; content: string }>, maxCharsEach = 2000): string {
  const parts: string[] = [];
  const lastIdx = messages.length - 1;
  messages.forEach((m, i) => {
    const isLast = i === lastIdx;
    const content = m.content ?? "";
    let slice: string;
    if (content.length <= maxCharsEach) {
      slice = content;
    } else if (isLast) {
      // head+tail of the final (most important) message
      const half = Math.floor(maxCharsEach / 2);
      slice = `${content.slice(0, half)} …[truncated]… ${content.slice(-half)}`;
    } else {
      slice = `${content.slice(0, Math.floor(maxCharsEach / 4))} …[truncated]…`;
    }
    parts.push(`[${i === lastIdx ? "LAST" : `HIST-${messages.length - 1 - i}`}] ${m.role}: ${slice}`);
  });
  return parts.join("\n");
}

export class JevTimeoutError extends Error {
  constructor(ms: number) {
    super(`Jev classified timed out after ${ms}ms`);
    this.name = "JevTimeoutError";
  }
}

export class JevUnavailableError extends Error {
  constructor(msg: string) {
    super(msg);
    this.name = "JevUnavailableError";
  }
}

/** One Jev call classifying category + complexity + triviality. */
export async function classify(
  state: string,
  opts: JevClientOptions,
): Promise<ClassificationResult> {
  const body = {
    model: opts.model,
    state,
    questions: ROUTE_QUESTIONS,
  };
  const ac = new AbortController();
  const timer = setTimeout(() => ac.abort(), opts.timeoutMs);
  try {
    const res = await (opts.fetchImpl ?? fetch)(opts.baseUrl, {
      method: "POST",
      headers: {
        Authorization: `Bearer ${opts.apiKey}`,
        "Content-Type": "application/json",
      },
      body: JSON.stringify(body),
      signal: ac.signal,
    });
    if (res.status === 408 || res.status === 504) throw new JevTimeoutError(opts.timeoutMs);
    if (!res.ok) {
      throw new JevUnavailableError(`Jev HTTP ${res.status}: ${(await res.text()).slice(0, 200)}`);
    }
    const json = (await res.json()) as JevResponse;
    return parseJevResponse(json);
  } catch (e) {
    if (e instanceof JevTimeoutError || e instanceof JevUnavailableError) throw e;
    const err = e as Error;
    if (err.name === "AbortError") throw new JevTimeoutError(opts.timeoutMs);
    throw new JevUnavailableError(`Jev request failed: ${err.message}`);
  } finally {
    clearTimeout(timer);
  }
}

/** Map a raw Jev response into typed classification fields. */
export function parseJevResponse(json: JevResponse): ClassificationResult {
  const cat = json.answers["category"];
  const comp = json.answers["complexity"];
  const triv = json.answers["is_trivial"];
  if (!cat || !comp || !triv) {
    throw new JevUnavailableError("Jev response missing category/complexity/is_trivial answers");
  }
  const category = cat.choice ?? "other";
  const complexity = typeof comp.score === "number" ? comp.score : 0;
  const trivial = typeof triv.noul === "number" ? triv.noul : 0;
  return {
    category,
    complexity,
    complexityConfidence: comp.confidence ?? 0,
    categoryConfidence: cat.confidence ?? 0,
    trivialNoul: trivial,
    jevModel: json.model,
    jevUsage: {
      input_tokens: json.usage?.input_tokens ?? 0,
      output_tokens: json.usage?.output_tokens ?? 0,
    },
  };
}

export { ROUTE_QUESTIONS };