// Router service: Glue connecting classify → policy → sticky → upstream failover → card log.

import type {
  ClassificationResult,
  RouteCard,
  RouteDecision,
  Tier,
  TierId,
  TierModel,
} from "./types.ts";
import type { FluxConfig } from "./config.ts";
import { classify, buildJevState, JevTimeoutError, JevUnavailableError, type JevClientOptions } from "./classify.ts";
import { estimateTokens, projectedCost } from "./cost.ts";
import { routeRequest, findTier, stickyEscape, cheapestFittingTier, contextGate } from "./policy.ts";
import { SessionStore, type SessionState } from "./session.ts";
import { forward, pickModel, type Upstreams } from "./upstream.ts";
import { CardLog } from "./cardlog.ts";
import { Metrics } from "./metrics.ts";

export interface ChatRequest {
  messages: Array<{ role: string; content: string }>;
  stream: boolean;
  model?: string;
}

export interface RouterDeps {
  config: FluxConfig;
  upstreams: Upstreams;
  cardLog: CardLog;
  metrics: Metrics;
  jev: JevClientOptions;
}

/** Resolve incoming body JSON to a normalized chat request (or null if malformed). */
export function parseChatRequest(body: any): ChatRequest | null {
  if (!body || !Array.isArray(body.messages)) return null;
  const messages = body.messages.map((m: any) => ({
    role: String(m.role ?? "user"),
    content: typeof m.content === "string" ? m.content : JSON.stringify(m.content ?? ""),
  }));
  return {
    messages,
    stream: body.stream === true,
    model: typeof body.model === "string" ? body.model : undefined,
  };
}

export class RouterService {
  private sessions: SessionStore;
  private deps: {
    config: FluxConfig;
    upstreams: Upstreams;
    cardLog: CardLog;
    metrics: Metrics;
    sessions?: SessionStore;
  };

  constructor(
    deps: {
      config: FluxConfig;
      upstreams: Upstreams;
      cardLog: CardLog;
      metrics: Metrics;
      sessions?: SessionStore;
    },
  ) {
    this.deps = deps;
    this.sessions = deps.sessions ?? new SessionStore();
  }

  /** Classify + policy → RouteDecision (testable without upstreams). */
  async decide(req: ChatRequest, nowIso = new Date().toISOString()): Promise<RouteDecision> {
    const cfg = this.deps.config;
    const requestTokens = estimateTokens(req.messages);
    const sessionId = SessionStore.sessionIdFor(req.messages);
    const stickyState = cfg.sticky.enabled ? this.deps.sessions?.get?.(sessionId) ?? this.sessions.get(sessionId) : undefined;

    // Classify every turn. On a sticky session this call also feeds the upward-escape
    // check below; skipping it would make `sticky_escape_up` unreachable (dead code).
    // v0.2 plans to skip classification on sticky hits using a cached decision —
    // see ROADMAP.md — but correctness comes first until that is measured.
    let cls: ClassificationResult | null = null;
    let jevMs = 0;
    {
      const started = Date.now();
      try {
        cls = await classify(buildJevState(req.messages), this.jevOptions());
      } catch (e) {
        cls = null;
        this.deps.metrics.inc("jev_failures_total");
        if (e instanceof JevTimeoutError) {
          this.deps.metrics.inc("jev_timeouts_total");
        }
      }
      jevMs = Date.now() - started;
    }

    const outcome = routeRequest({
      classification: cls,
      requestTokens,
      stickyTier: stickyState?.tier ?? null,
      tiers: cfg.tiers,
      fallbackTier: cfg.jev.fallbackTier,
      config: {
        minConfidence: cfg.jev.minConfidence,
        trivialNoul: cfg.jev.trivialNoul,
        escalateOnlyAboveComplexity: cfg.jev.escalateOnlyAboveComplexity,
        complexityToTier: cfg.policy.default.complexityToTier,
        overrides: cfg.policy.overrides,
        perRequestCapUsd: cfg.cost.perRequestCapUsd,
        stickyEnabled: cfg.sticky.enabled,
        escapeConfidence: cfg.sticky.escapeConfidence,
      },
    });

    // Sticky escape: a pinned session upgrades when the new turn clearly needs more.
    // (routeRequest returns the pinned tier with reason "sticky", so this overrides it.)
    if (cfg.sticky.enabled && stickyState && cls) {
      const escape = stickyEscape(
        stickyState.tier,
        cls,
        cfg.policy.default.complexityToTier,
        cfg.policy.overrides,
        cfg.sticky.escapeConfidence,
      );
      if (escape !== null && escape > stickyState.tier) {
        outcome.tier = escape;
        outcome.reason = "sticky_escape_up";
      }
    }

    // Sticky bookkeeping: pins on first turn, refreshes on every later turn.
    if (cfg.sticky.enabled) {
      const sessions = this.deps.sessions ?? this.sessions;
      const pinned = findTier(cfg.tiers, outcome.tier)!;
      if (stickyState) {
        sessions.rePin(sessionId, outcome.tier, pinned.models[0]!.id, pinned.models[0]!.upstream);
      } else {
        sessions.pin(sessionId, outcome.tier, pinned.models[0]!.id, pinned.models[0]!.upstream);
      }
    }

    const tierObj = findTier(cfg.tiers, outcome.tier) ?? cfg.tiers[0]!;
    const model = tierObj.models[0]!;

    return {
      tier: outcome.tier,
      model: model.id,
      upstream: model.upstream,
      reason: outcome.reason,
      category: outcome.category,
      complexity: outcome.complexity,
      confidence: outcome.confidence,
      projectedCostUsd: outcome.projectedCostUsd,
      jevLatencyMs: jevMs,
    };
  }

  /**
   * Execute a proxied chat request: route, forward with failover, stream back,
   * and write the route card.
   */
  async execute(
    rawBody: any,
    req: ChatRequest,
    opts: { requestedModel?: string; startTs: number },
  ): Promise<Response> {
    const cfg = this.deps.config;
    const sessionId = SessionStore.sessionIdFor(req.messages);
    const decision = await this.decide(req);
    const jevMs = decision.jevLatencyMs ?? 0;
    const tier = findTier(cfg.tiers, decision.tier)!;
    const startedUpstream = Date.now();
    const attempt = await this.forwardWithFailover(tier, req, rawBody);
    const upstreamMs = Date.now() - startedUpstream;

    // For streaming pass-through, wrap the body to observe usage chunks.
    const totalMs = opts.startTs ? Date.now() - opts.startTs : upstreamMs;

    const usage = attempt.json
      ? extractUsage(attempt.json)
      : { input: 0, output: 0, cachedInput: 0 };

    const model = tier.models[0]!;
    const card: RouteCard = {
      ts: new Date(opts.startTs).toISOString(),
      sessionId,
      tier: decision.tier,
      model: decision.model,
      upstream: decision.upstream,
      reason: decision.reason,
      category: decision.category,
      complexity: decision.complexity,
      confidence: decision.confidence,
      requestTokensEst: estimateTokens(req.messages),
      usage,
      costUsd: costFromUsage(usage, model),
      latenciesMs: { jev: jevMs, upstream: upstreamMs, total: totalMs },
      status: attempt.status,
      requestedModel: opts.requestedModel ?? "",
      projectedCostUsd: decision.projectedCostUsd,
    };
    this.deps.cardLog.log(card);
    this.deps.metrics.inc("requests_total");
    this.deps.metrics.inc(`tier_${decision.tier}_total`);
    this.deps.metrics.inc(`cost_usd_total_micro`, Math.round(card.costUsd * 1e6));
    this.deps.metrics.observe("upstream_latency_ms", upstreamMs);

    // Rebuild response headers deliberately: do NOT pass through upstream
    // content-length / content-encoding / transfer-encoding, because undici has already
    // decoded the body — forwarding those makes the client socket terminate early.
    // Connection headers also belong to the upstream hop, not this one.
    const STRIPPED = new Set([
      "content-length",
      "content-encoding",
      "transfer-encoding",
      "connection",
      "keep-alive",
      "proxy-authenticate",
      "proxy-authorization",
      "te",
      "trailer",
      "upgrade",
    ]);
    const headers = new Headers();
    for (const [k, v] of Object.entries(attempt.headers)) {
      if (STRIPPED.has(k.toLowerCase())) continue;
      headers.set(k, v);
    }
    headers.set("X-Flux-Route-Tier", String(decision.tier));
    headers.set("X-Flux-Route-Model", decision.model);
    headers.set("X-Flux-Route-Reason", decision.reason);
    headers.set("X-Flux-Session", sessionId);

    if (attempt.body) {
      return new Response(attempt.body, { status: attempt.status, headers });
    }
    const payload = attempt.json ?? {};
    headers.set("Content-Type", "application/json");
    return new Response(JSON.stringify(payload), { status: attempt.status, headers });
  }

  /** Failover ladder per plan.md: primary → openrouter failover id → next tier up → error. */
  private async forwardWithFailover(
    tier: Tier,
    req: ChatRequest,
    rawBody: any,
  ): Promise<{ status: number; headers: Record<string, string>; body: ReadableStream<Uint8Array> | null; json: unknown | null }> {
    const cfg = this.deps.config;
    const attempts: Array<{ model: TierModel }> = [];
    for (const m of tier.models) attempts.push({ model: m });
    const tIdx = cfg.tiers.findIndex((t) => t.id === tier.id);
    if (tIdx >= 0 && tIdx + 1 < cfg.tiers.length) {
      const next = cfg.tiers[tIdx + 1]!;
      for (const m of next.models) attempts.push({ model: m });
    }

    let lastStatus = 500;
    let lastJson: unknown = null;
    for (const { model } of attempts) {
      const upstreamCfg = cfg.upstreams[model.upstream];
      const key =
        model.upstream === "ollama" ? this.deps.upstreams.ollama.apiKey : this.deps.upstreams.openrouter.apiKey;
      // Skip lanes whose key is absent (e.g. optional OpenRouter not configured).
      if (!key) continue;
      const body = {
        ...(rawBody as Record<string, unknown>),
        model: model.id,
      };
      try {
        const res = await forward(upstreamCfg, model.upstream, key, "/chat/completions", {
          body,
          stream: req.stream,
        });
        if (res.status >= 200 && res.status < 300) return res;
        lastStatus = res.status;
        lastJson = res.json;
        // 429: no blind retry, surface via ladder; try next candidate (e.g. openrouter)
        // Other 4xx (except 429/408): likely request problem — surface immediately.
        if (res.status >= 400 && res.status < 500 && res.status !== 429 && res.status !== 408) {
          break;
        }
      } catch (e) {
        lastStatus = 502;
        lastJson = { error: { message: (e as Error).message } };
      }
    }
    return {
      status: lastStatus,
      headers: {},
      body: null,
      json:
        lastJson ??
        { error: { message: `FluxRouter: all upstreams exhausted for tier ${tier.id}`, type: "fluxrouter_upstream_exhausted" } },
    };
  }

  private jevOptions(): JevClientOptions {
    const cfg = this.deps.config;
    return {
      baseUrl: cfg.jev.baseUrl,
      model: cfg.jev.model,
      timeoutMs: cfg.jev.timeoutMs,
      apiKey: process.env["TYPESAFE_API_KEY"] ?? "",
      fetchImpl: undefined,
    };
  }

  dispose(): void {
    this.sessions.dispose();
  }
}

/** Extract OpenAI usage from a completion response. */
export function extractUsage(json: any): { input: number; output: number; cachedInput: number } {
  const u = json?.usage ?? {};
  return {
    input: u.prompt_tokens ?? 0,
    output: u.completion_tokens ?? u.output_tokens ?? 0,
    cachedInput: u.prompt_tokens_details?.cached_tokens ?? u.cached_input_tokens ?? 0,
  };
}

/** Actual USD cost from usage + rates. */
export function costFromUsage(usage: { input: number; output: number; cachedInput: number }, model: TierModel): number {
  const cached = model.cachedIn !== undefined ? usage.cachedInput : 0;
  const normalIn = usage.input - cached;
  let cost = (normalIn / 1e6) * model.in;
  if (model.cachedIn !== undefined) cost += (cached / 1e6) * model.cachedIn;
  cost += (usage.output / 1e6) * model.out;
  return Number(cost.toFixed(6));
}