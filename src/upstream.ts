// Upstream adapters: forward OpenAI-compatible chat completion requests.

import { Agent, request } from "undici";
import type { TierModel } from "./types.ts";

export interface ApiKey {
  key: string;
}

export interface Upstreams {
  ollama: { baseUrl: string; apiKey: string };
  openrouter: { baseUrl: string; apiKey: string };
}

export interface ForwardOptions {
  body: unknown;
  stream: boolean;
  signal?: AbortSignal;
}

export interface ForwardResult {
  status: number;
  headers: Record<string, string>;
  body: ReadableStream<Uint8Array> | null; // null => buffered json
  json: unknown | null; // non-stream convenience
  latencyMs: number;
}

// Shared connection pools per upstream (keep-alive, pipelining).
const agents = new Map<string, Agent>();

function agentFor(upstream: string): Agent {
  let a = agents.get(upstream);
  if (!a) {
    a = new Agent({ connections: 32, pipelining: 1, keepAliveTimeout: 30_000 });
    agents.set(upstream, a);
  }
  return a;
}

export function closeAllAgents(): void {
  for (const [, a] of agents) {
    void a.close().catch(() => {});
    agents.delete;
  }
  agents.clear();
}

function endpointFor(upstream: { baseUrl: string }, path: string): string {
  return `${upstream.baseUrl.replace(/\/$/, "")}${path}`;
}

function headersFor(upstream: string, apiKey: string): Record<string, string> {
  const h: Record<string, string> = { "Content-Type": "application/json" };
  if (upstream === "openrouter") {
    h["Authorization"] = `Bearer ${apiKey}`;
    h["HTTP-Referer"] = "https://github.com/fluxrouter";
    h["X-Title"] = "FluxRouter";
  } else {
    h["Authorization"] = `Bearer ${apiKey}`;
  }
  return h;
}

/**
 * Forward a chat completions request. Streams are passed through chunk-by-chunk
 * (never buffered whole). Returns enough for the caller to pipe and log.
 */
export async function forward(
  upstream: { baseUrl: string },
  upstreamName: string,
  apiKey: string,
  path: string,
  opts: ForwardOptions,
): Promise<ForwardResult> {
  const started = Date.now();
  const { statusCode, headers, body } = await request(endpointFor(upstream, path), {
    method: "POST",
    headers: {
      ...headersFor(upstreamName, apiKey),
      ...(opts.stream ? { Accept: "text/event-stream" } : {}),
    },
    body: JSON.stringify(opts.body),
    dispatcher: agentFor(upstreamName),
    signal: opts.signal,
  });

  const latencyMs = Date.now() - started;

  if (opts.stream) {
    if (!body) throw new Error(`upstream ${upstreamName} returned no body for stream request`);
    return { status: statusCode, headers: recordHeaders(headers), body: body as unknown as ReadableStream<Uint8Array>, json: null, latencyMs };
  }

  const text = await body?.text();
  let json: unknown = null;
  try {
    json = text ? JSON.parse(text) : null;
  } catch {
    json = { raw: text };
  }
  return { status: statusCode, headers: recordHeaders(headers), body: null, json, latencyMs };
}

function recordHeaders(h: Record<string, string | string[] | undefined>): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [k, v] of Object.entries(h)) {
    if (typeof v === "string") out[k] = v;
  }
  return out;
}

/** Choose the model object for a tier, preferring the primary model. */
export function pickModel(tierModels: TierModel[], preferUpstream?: string): TierModel {
  if (preferUpstream) {
    const preferred = tierModels.find((m) => m.upstream === preferUpstream);
    if (preferred) return preferred;
  }
  return tierModels[0]!;
}

/** Map an abstract model id to the actual body model field (respects reasoning effort). */
export function upstreamModelId(m: TierModel): string {
  return m.id;
}